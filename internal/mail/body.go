package mail

import (
	"fmt"
	"io"
	"net/mail"
	"strings"

	"github.com/emersion/go-message"
)

// readBody 将 MIME 邮件解码为纯文本，正文优先使用 text/plain，再从 HTML 提取文字。
func readBody(msg *mail.Message) (string, error) {
	entity, err := message.New(message.HeaderFromMap(msg.Header), msg.Body)
	if err != nil {
		return "", err
	}
	remaining := int64(8 << 20)
	body, err := readMIMEBody(entity, 0, &remaining)
	if err != nil {
		return "", err
	}
	if body.plain != "" {
		return body.plain, nil
	}
	return body.html, nil
}

type mimeBody struct{ plain, html string }

func readMIMEBody(entity *message.Entity, depth int, remaining *int64) (mimeBody, error) {
	var body mimeBody
	if depth > 32 {
		return body, fmt.Errorf("邮件 MIME 嵌套过深")
	}
	mediaType, params, err := entity.Header.ContentType()
	if err != nil {
		return body, err
	}
	disposition, dispParams, _ := entity.Header.ContentDisposition()
	// 在遍历子部分之前排除附件，包括带文件名的文本及 multipart 附件。
	if strings.EqualFold(disposition, "attachment") || dispParams["filename"] != "" || params["name"] != "" {
		return body, nil
	}
	if multipart := entity.MultipartReader(); multipart != nil {
		for {
			part, err := multipart.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return mimeBody{}, err
			}
			candidate, err := readMIMEBody(part, depth+1, remaining)
			if err != nil {
				return mimeBody{}, err
			}
			if body.plain == "" {
				body.plain = candidate.plain
			}
			if body.html == "" {
				body.html = candidate.html
			}
			// multipart/alternative 的不同格式表示同一正文，不拼接重复内容。
			if body.plain != "" {
				break
			}
		}
		return body, nil
	}
	if mediaType != "text/plain" && mediaType != "text/html" {
		return body, nil
	}
	// go-message 已按该部分的 transfer encoding 和 charset 解码为 UTF-8。
	raw, err := io.ReadAll(io.LimitReader(entity.Body, *remaining+1))
	if err != nil {
		return body, err
	}
	if int64(len(raw)) > *remaining {
		return body, fmt.Errorf("邮件正文超过读取上限")
	}
	*remaining -= int64(len(raw))
	if mediaType == "text/html" {
		body.html = sanitizePreview(string(raw))
	} else {
		body.plain = sanitizePlainPreview(string(raw))
	}
	return body, nil
}
