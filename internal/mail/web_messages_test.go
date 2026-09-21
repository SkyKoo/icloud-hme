package mail

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	http "github.com/bogdanfinn/fhttp"
)

func mockWebMessages(t *testing.T, handler func(string, map[string]any) string) *WebClient {
	t.Helper()
	c := NewWebClient(nil, "12345", "icloud.com")
	c.mccGatewayURL = "https://p42-mccgateway.icloud.com"
	c.mailboxIDs = map[string]string{FolderInbox: "box-inbox", FolderJunk: "box-junk"}
	c.httpc = &webMailTestClient{HttpClient: c.httpc, client: &http.Client{Transport: webMailTestTransport(func(req *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		body := handler(req.URL.Path, payload)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}}
	return c
}

func TestWebMessageListReadsHeadersAndMatchesPreviewByFolderUID(t *testing.T) {
	calls := 0
	c := mockWebMessages(t, func(path string, p map[string]any) string {
		calls++
		switch path {
		case "/mailws2/v1/message/list":
			predicate := p["predicate"].(map[string]any)
			if predicate["value"] != "box-junk" || p["limit"] != float64(20) {
				t.Fatalf("wrong scope: %#v", p)
			}
			filters := predicate["and"].([]any)
			aliasFilter := filters[1].(map[string]any)
			expression := aliasFilter["expression"].(map[string]any)
			if expression["value"] != "To" || aliasFilter["value"] != "alias@icloud.com" || aliasFilter["type"] != "textMatch" {
				t.Fatalf("incorrect recipient filter: %#v", aliasFilter)
			}
			return `{"domainObjects":[{"uid":6,"mboxRef":{"id":"box-junk"},"from":"Sender <sender@example.com>","to":"alias@icloud.com","subject":"code","stateInternalDate":1789980000000,"previewId":"opaque-preview-id"}]}`
		case "/mailws2/v1/message/preview":
			if p["folder"] != "Junk" || p["sessionHeaders"].(map[string]any)["folder"] != "Junk" || p["previewIds"].([]any)[0] != "opaque-preview-id" {
				t.Fatalf("wrong preview lookup: %#v", p)
			}
			return `{"result":[{"messageGuid":"INBOX/6","preview":"wrong folder"},{"messageGuid":"Junk/6","preview":"<style>bad{}</style><p>123456</p>"}]}`
		default:
			t.Fatalf("unexpected %s", path)
			return ""
		}
	})
	messages, err := c.FindByAlias("alias@icloud.com", 20, FolderJunk)
	if err != nil || len(messages) != 1 {
		t.Fatalf("list: %#v %v", messages, err)
	}
	m := messages[0]
	if m.Folder != FolderJunk || m.ID != "6" || m.From == "" || m.To != "alias@icloud.com" || m.Preview != "123456" || m.Date == "" {
		t.Fatalf("incomplete message: %#v", m)
	}
	if _, err := c.ListInbox(20, "Trash"); err == nil || calls != 2 {
		t.Fatal("invalid scope reached upstream")
	}
}

func TestWebMessageDetailsPreferPlainTextAndDoNotMarkRead(t *testing.T) {
	for _, htmlOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "html"}[htmlOnly], func(t *testing.T) {
			c := mockWebMessages(t, func(path string, p map[string]any) string {
				switch path {
				case "/mailws2/v1/message/list":
					predicate := p["predicate"].(map[string]any)
					filter := predicate["and"].([]any)[1].(map[string]any)
					if predicate["value"] != "box-junk" || filter["expression"].(map[string]any)["property"] != "uid" || filter["value"] != float64(6) {
						t.Fatal("wrong identity")
					}
					plain := `,{"partId":"1.1","contentType":"text/plain; charset=utf-8","attach":false}`
					if htmlOnly {
						plain = ""
					}
					return `{"domainObjects":[{"uid":6,"mboxRef":{"id":"box-junk"},"from":"sender@example.com","to":"alias@icloud.com","parts":[{"partId":"1.2","contentType":"text/html","attach":false},{"partId":"2","contentType":"text/plain","attach":true,"fileName":"secret.txt"}` + plain + `]}]}`
				case "/mailws2/v1/message/get":
					parts := p["parts"].([]any)
					want := "1.1"
					if htmlOnly {
						want = "1.2"
					}
					if p["uid"] != "6" || p["dontMarkAsRead"] != true || p["sessionHeaders"].(map[string]any)["folder"] != "Junk" || len(parts) != 1 || parts[0] != want {
						t.Fatalf("unsafe body request: %#v", p)
					}
					if htmlOnly {
						return `{"parts":[{"guid":"messagepart:Junk/6-1.2","content":"<html><head><style>.x{display:none}</style></head><body><script>alert(1)</script><p>验证码 123456</p><img src='https://tracker.example/a'></body></html>"}]}`
					}
					return `{"parts":[{"guid":"messagepart:Junk/6-1.1","content":"验证码 123456"}]}`
				default:
					t.Fatalf("unexpected path %s", path)
					return ""
				}
			})
			message, err := c.GetFull(6, FolderJunk)
			if err != nil || message.Body != "验证码 123456" || message.Folder != FolderJunk || message.ContentType != "text/plain" {
				t.Fatalf("detail: %#v %v", message, err)
			}
		})
	}
}

func TestWebMessageRejectsCrossFolderAndMalformedResults(t *testing.T) {
	for _, body := range []string{`{}`, `{"domainObjects":[{"uid":6,"mboxRef":{"id":"box-inbox"}}]}`, `{"domainObjects":[{"uid":7,"mboxRef":{"id":"box-junk"}}]}`, `{"success":false}`} {
		c := mockWebMessages(t, func(string, map[string]any) string { return body })
		if _, err := c.GetFull(6, FolderJunk); err == nil {
			t.Fatalf("accepted invalid result: %s", body)
		}
	}
	c := mockWebMessages(t, func(string, map[string]any) string { return `{"domainObjects":[]}` })
	messages, err := c.ListInbox(20, FolderJunk)
	if err != nil || messages == nil || len(messages) != 0 {
		t.Fatalf("empty folder: %#v %v", messages, err)
	}
	if _, err := c.GetFull(6, FolderJunk); err == nil {
		t.Fatal("missing mail should report error")
	}
}
