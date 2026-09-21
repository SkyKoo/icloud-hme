package mail

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap"
)

const (
	FolderInbox = "inbox"
	FolderJunk  = "junk"
	FolderAll   = "all"
)

// ValidFolder 限制公开 API 的范围，避免把任意文件夹名传给上游。
func ValidFolder(folder string, allowAll bool) bool {
	return folder == FolderInbox || folder == FolderJunk || (allowAll && folder == FolderAll)
}

func requestedFolder(folders []string) (string, error) {
	folder := FolderInbox
	if len(folders) > 0 {
		folder = folders[0]
	}
	if len(folders) > 1 || !ValidFolder(folder, false) {
		return "", fmt.Errorf("无效邮件文件夹")
	}
	return folder, nil
}

func messageDate(raw string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC1123Z, time.RFC1123} {
		if date, err := time.Parse(layout, raw); err == nil {
			return date
		}
	}
	return time.Time{}
}

// CollectMessages 合并指定文件夹；按来源区分 UID，统一过滤时间并限制总条数。
// 任一文件夹读取失败都返回错误，避免把遗漏垃圾邮件的结果当成完整结果。
func CollectMessages(scope string, limit, days int, read func(string) ([]Message, error)) ([]Message, error) {
	if !ValidFolder(scope, true) {
		return nil, fmt.Errorf("无效邮件范围")
	}
	folders := []string{scope}
	if scope == FolderAll {
		folders = []string{FolderInbox, FolderJunk}
	}
	out := make([]Message, 0)
	seen := make(map[string]bool)
	cutoff := time.Now().AddDate(0, 0, -days)
	for _, folder := range folders {
		messages, err := read(folder)
		if err != nil {
			return nil, err
		}
		for _, message := range messages {
			message.Folder = folder
			date := messageDate(message.Date)
			if days > 0 && !date.IsZero() && date.Before(cutoff) {
				continue
			}
			key := folder + ":" + message.ID
			if message.ID != "" && seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, message)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return messageDate(out[i].Date).After(messageDate(out[j].Date)) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func junkMailbox(mailboxes []*imap.MailboxInfo) string {
	for _, mailbox := range mailboxes {
		for _, attr := range mailbox.Attributes {
			if strings.EqualFold(attr, `\Junk`) {
				return mailbox.Name
			}
		}
	}
	for _, name := range []string{"Junk", "Spam", "Junk E-mail", "Junk Email", "[Gmail]/Spam", "垃圾邮件", "垃圾郵件"} {
		for _, mailbox := range mailboxes {
			if strings.EqualFold(mailbox.Name, name) {
				return mailbox.Name
			}
		}
	}
	return ""
}

func (c *Client) selectMailbox(readOnly bool, folders ...string) (*imap.MailboxStatus, error) {
	if c.cli == nil {
		return nil, fmt.Errorf("未连接")
	}
	folder, err := requestedFolder(folders)
	if err != nil {
		return nil, err
	}
	name := "INBOX"
	if folder == FolderJunk {
		ch := make(chan *imap.MailboxInfo, 16)
		done := make(chan error, 1)
		go func() { done <- c.cli.List("", "*", ch) }()
		var mailboxes []*imap.MailboxInfo
		for info := range ch {
			mailboxes = append(mailboxes, info)
		}
		if err := <-done; err != nil {
			return nil, err
		}
		name = junkMailbox(mailboxes)
		if name == "" {
			return nil, fmt.Errorf("未找到垃圾邮件文件夹，请选择仅查询收件箱")
		}
	}
	return c.cli.Select(name, readOnly)
}
