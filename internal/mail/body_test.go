package mail

import (
	"encoding/base64"
	"net/mail"
	"strings"
	"testing"
)

// Synthetic fixtures exercise the formats returned by IMAP BODY.PEEK[].
const alternativeBody = "Content-Type: multipart/alternative; boundary=alt\r\n\r\n" +
	"--alt\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n<div>HTML duplicate</div>\r\n" +
	"--alt\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n验证码 123456\r\n--alt--\r\n"

func TestReadBodyMIME(t *testing.T) {
	cases := []struct{ name, raw, want string }{
		{"alternative prefers plain even after html", alternativeBody, "验证码 123456"},
		{"base64 utf8", "Content-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n" + base64.StdEncoding.EncodeToString([]byte("验证码 654321")), "验证码 654321"},
		{"quoted printable charset", "Content-Type: text/plain; charset=iso-8859-1\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\ncaf=E9 =31=32=33", "café 123"},
		{"html only", "Content-Type: text/html; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n" + base64.StdEncoding.EncodeToString([]byte(`<html><head><style>.foo{color:red}</style></head><body><p>验证码 &amp; 123456</p><script>hidden()</script><img src="https://example.com/tracker"></body></html>`)), "验证码 & 123456"},
		{"default plain preserves angle brackets", "Subject: plain\r\n\r\nUse <123456> to sign in", "Use <123456> to sign in"},
		{"empty plain falls back to html", "Content-Type: multipart/alternative; boundary=a\r\n\r\n--a\r\nContent-Type: text/plain\r\n\r\n \r\n--a\r\nContent-Type: text/html\r\n\r\n<p>123456</p>\r\n--a--\r\n", "123456"},
		{"nested body excludes named text and binary attachments", "Content-Type: multipart/mixed; boundary=outer\r\n\r\n" +
			"--outer\r\nContent-Type: text/plain; name=secret.txt\r\n\r\nattachment secret\r\n" +
			"--outer\r\nContent-Type: application/octet-stream\r\nContent-Transfer-Encoding: base64\r\n\r\nYWJj\r\n" +
			"--outer\r\n" + alternativeBody + "\r\n--outer--\r\n", "验证码 123456"},
		{"multipart attachment subtree excluded", "Content-Type: multipart/mixed; boundary=outer\r\n\r\n" +
			"--outer\r\nContent-Disposition: attachment; filename=attached.eml\r\n" + alternativeBody +
			"\r\n--outer\r\nContent-Type: text/html\r\n\r\n<p>actual body</p>\r\n--outer--\r\n", "actual body"},
		{"attachment only", "Content-Type: text/plain\r\nContent-Disposition: attachment; filename=secret.txt\r\n\r\nattachment secret", ""},
		{"related skips inline image", "Content-Type: multipart/related; boundary=a\r\n\r\n--a\r\nContent-Type: text/html\r\n\r\n<p>body</p>\r\n--a\r\nContent-Type: image/png\r\nContent-Disposition: inline\r\nContent-Transfer-Encoding: base64\r\n\r\nYWJj\r\n--a--\r\n", "body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg, err := mail.ReadMessage(strings.NewReader(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			got, err := readBody(msg)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("body = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadBodyRejectsMalformedMultipart(t *testing.T) {
	msg, err := mail.ReadMessage(strings.NewReader("Content-Type: multipart/mixed\r\n\r\nnot a decoded body"))
	if err != nil {
		t.Fatal(err)
	}
	if body, err := readBody(msg); err == nil || body != "" {
		t.Fatalf("raw MIME must not be exposed: %q, %v", body, err)
	}
}
