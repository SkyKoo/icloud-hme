package mail

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
)

// 使用 iCloud 当前网页的单封邮件接口。会话摘要在 Junk 中可能缺少内容，
// thread/get 也可能返回空列表，不能将 thread ID 当作邮件 UID。
func (c *WebClient) mailRequest(path string, payload any, result any) error {
	if err := c.resolveMccGateway(); err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", c.withParams(c.mccGatewayURL+path), strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	c.setCommonHeaders(req)
	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("邮件接口 HTTP %d", resp.StatusCode)
	}
	const maxResponse = 8 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return err
	}
	if len(raw) > maxResponse {
		return fmt.Errorf("邮件内容超过读取上限")
	}
	var status struct {
		Success   *bool           `json:"success"`
		ErrorCode json.RawMessage `json:"errorCode"`
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		return fmt.Errorf("邮件响应格式错误")
	}
	if status.Success != nil && !*status.Success {
		return fmt.Errorf("邮件服务返回错误")
	}
	if len(status.ErrorCode) > 0 && string(status.ErrorCode) != "null" && string(status.ErrorCode) != "0" {
		return fmt.Errorf("邮件服务返回错误码")
	}
	if err := json.Unmarshal(raw, result); err != nil {
		return fmt.Errorf("邮件响应字段格式错误")
	}
	return nil
}

func webFolderName(folder string) string {
	if folder == FolderJunk {
		return "Junk"
	}
	return "INBOX"
}

func webSessionHeaders(folder string) map[string]any {
	return map[string]any{"folder": webFolderName(folder), "modseq": nil, "threadmodseq": nil, "condstore": 1, "qresync": 1, "threadmode": 1}
}

func (c *WebClient) mailboxID(folder string) (string, error) {
	if !ValidFolder(folder, false) {
		return "", fmt.Errorf("无效邮件文件夹")
	}
	if id := c.mailboxIDs[folder]; id != "" {
		return id, nil
	}
	var response struct {
		DomainObjects []struct {
			Identifier string `json:"identifier"`
			Name       string `json:"name"`
		} `json:"domainObjects"`
	}
	payload := map[string]any{
		"domain": "mailbox", "properties": []string{"identifier", "name"},
		"predicate": map[string]any{"type": "in", "expression": map[string]any{"property": "name"}, "value": []string{"INBOX", "Junk"}},
	}
	if err := c.mailRequest("/mailws2/v1/geqs/query", payload, &response); err != nil {
		return "", err
	}
	c.mailboxIDs = make(map[string]string)
	for _, box := range response.DomainObjects {
		for _, logical := range []string{FolderInbox, FolderJunk} {
			if box.Name == webFolderName(logical) {
				c.mailboxIDs[logical] = box.Identifier
			}
		}
	}
	if id := c.mailboxIDs[folder]; id != "" {
		return id, nil
	}
	return "", fmt.Errorf("未找到所选邮件文件夹")
}

type webMessagePart struct {
	PartID      string `json:"partId"`
	ContentType string `json:"contentType"`
	Attach      bool   `json:"attach"`
	IsAttach    bool   `json:"isAttach"`
	FileName    string `json:"fileName"`
	Disposition string `json:"disposition"`
}

type webMessageMetadata struct {
	UID        uint32 `json:"uid"`
	MailboxRef struct {
		ID string `json:"id"`
	} `json:"mboxRef"`
	Date      float64          `json:"stateInternalDate"`
	From      string           `json:"from"`
	To        string           `json:"to"`
	Subject   string           `json:"subject"`
	PreviewID string           `json:"previewId"`
	Parts     []webMessagePart `json:"parts"`
}

func (m webMessageMetadata) summary(folder string) Message {
	date := ""
	if m.Date > 0 {
		date = time.UnixMilli(int64(m.Date)).Format(time.RFC3339)
	}
	return Message{ID: strconv.FormatUint(uint64(m.UID), 10), Folder: folder, From: m.From, To: m.To, Subject: m.Subject, Date: date}
}

func (c *WebClient) messageMetadata(folder string, limit int, alias string, uid uint32) ([]webMessageMetadata, error) {
	boxID, err := c.mailboxID(folder)
	if err != nil {
		return nil, err
	}
	conditions := []any{map[string]any{"type": "eq", "expression": map[string]any{"type": "fieldOf", "property": "flags", "value": "DELETED"}, "value": false}}
	if uid != 0 {
		conditions = append(conditions, map[string]any{"type": "eq", "expression": map[string]any{"property": "uid"}, "value": uid})
	}
	if alias != "" {
		conditions = append(conditions, map[string]any{"type": "textMatch", "expression": map[string]any{"type": "fieldOf", "property": "rfc822HeaderBytes", "value": "To"}, "value": alias})
	}
	properties := []any{"uid", "mboxRef", "stateInternalDate", "previewId", "parts"}
	for _, field := range []string{"From", "To", "Subject"} {
		properties = append(properties, map[string]any{"type": "fieldOf", "property": "rfc822HeaderBytes", "value": field, "alias": strings.ToLower(field), "decoder": "encodedWords"})
	}
	payload := map[string]any{
		"domain": "email", "properties": properties, "limit": limit, "strictLimit": true,
		"predicate": map[string]any{"type": "eq", "expression": map[string]any{"property": "mboxRef"}, "value": boxID, "and": conditions},
		"orderby":   map[string]any{"expressions": []any{map[string]any{"property": "stateInternalDate", "type": "property"}}, "ascending": false},
	}
	var response struct {
		DomainObjects []webMessageMetadata `json:"domainObjects"`
	}
	if err := c.mailRequest("/mailws2/v1/message/list", payload, &response); err != nil {
		return nil, err
	}
	if response.DomainObjects == nil {
		return nil, fmt.Errorf("邮件列表响应缺少数据")
	}
	for _, message := range response.DomainObjects {
		if message.UID == 0 || message.MailboxRef.ID != boxID || (uid != 0 && message.UID != uid) {
			return nil, fmt.Errorf("邮件标识或所属文件夹不匹配")
		}
	}
	return response.DomainObjects, nil
}

func (c *WebClient) listMessages(limit int, alias string, folders ...string) ([]Message, error) {
	folder, err := requestedFolder(folders)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	metadata, err := c.messageMetadata(folder, limit, alias, 0)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(metadata))
	for _, message := range metadata {
		if message.PreviewID != "" {
			ids = append(ids, message.PreviewID)
		}
	}
	previews := make(map[string]string)
	if len(ids) > 0 {
		var response struct {
			Result []struct {
				GUID    string `json:"messageGuid"`
				Preview string `json:"preview"`
			} `json:"result"`
		}
		payload := map[string]any{"folder": webFolderName(folder), "previewIds": ids, "sessionHeaders": webSessionHeaders(folder)}
		if err := c.mailRequest("/mailws2/v1/message/preview", payload, &response); err != nil {
			return nil, err
		}
		if response.Result == nil {
			return nil, fmt.Errorf("邮件摘要响应缺少数据")
		}
		for _, preview := range response.Result {
			previews[preview.GUID] = sanitizePreview(preview.Preview)
		}
	}
	messages := make([]Message, 0, len(metadata))
	for _, meta := range metadata {
		message := meta.summary(folder)
		message.Preview = previews[webFolderName(folder)+"/"+message.ID]
		messages = append(messages, message)
	}
	return messages, nil
}

// ListInbox 按单封邮件读取指定文件夹，省略文件夹时使用收件箱。
func (c *WebClient) ListInbox(limit int, folders ...string) ([]Message, error) {
	return c.listMessages(limit, "", folders...)
}

// FindByAlias 在服务器按 To 查询，避免会话摘要缺少收件人而漏信。
func (c *WebClient) FindByAlias(alias string, limit int, folders ...string) ([]Message, error) {
	return c.listMessages(limit, alias, folders...)
}

// GetFull 只读取邮件正文部分，保留已读状态，不下载附件或外部图片。
func (c *WebClient) GetFull(uid uint32, folder string) (*FullMessage, error) {
	if uid == 0 || !ValidFolder(folder, false) {
		return nil, fmt.Errorf("无效邮件标识")
	}
	messages, err := c.messageMetadata(folder, 1, "", uid)
	if err != nil {
		return nil, err
	}
	if len(messages) != 1 {
		return nil, fmt.Errorf("邮件不存在或已被移动")
	}
	meta := messages[0]
	full := &FullMessage{Message: meta.summary(folder), ContentType: "text/plain"}
	var plain, html []string
	for _, part := range meta.Parts {
		if part.PartID == "" || part.Attach || part.IsAttach || part.FileName != "" || strings.EqualFold(part.Disposition, "attachment") {
			continue
		}
		kind := strings.ToLower(strings.TrimSpace(strings.SplitN(part.ContentType, ";", 2)[0]))
		if kind == "text/plain" {
			plain = append(plain, part.PartID)
		}
		if kind == "text/html" {
			html = append(html, part.PartID)
		}
	}
	parts := plain
	if len(parts) == 0 {
		parts = html
	}
	if len(parts) == 0 {
		return full, nil
	}
	payload := map[string]any{"uid": strconv.FormatUint(uint64(uid), 10), "parts": parts, "dontMarkAsRead": true, "sessionHeaders": webSessionHeaders(folder)}
	var response struct {
		Parts []struct {
			GUID    string `json:"guid"`
			Content string `json:"content"`
		} `json:"parts"`
	}
	if err := c.mailRequest("/mailws2/v1/message/get", payload, &response); err != nil {
		return nil, err
	}
	content := make(map[string]string)
	for _, part := range response.Parts {
		content[part.GUID] = part.Content
	}
	bodies := make([]string, 0, len(parts))
	for _, id := range parts {
		text, ok := content["messagepart:"+webFolderName(folder)+"/"+full.ID+"-"+id]
		if !ok {
			return nil, fmt.Errorf("邮件正文部分缺失")
		}
		if len(plain) == 0 {
			text = stripHTML(text)
		} else {
			text = normalizePreview(text)
		}
		bodies = append(bodies, text)
	}
	full.Body = strings.Join(bodies, "\n\n")
	return full, nil
}
