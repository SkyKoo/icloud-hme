package mail

import (
	"fmt"
	"io"
	"net/url"
	"strings"
	"testing"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
)

type webMailTestTransport func(*http.Request) (*http.Response, error)

func (f webMailTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type webMailTestClient struct {
	tls_client.HttpClient
	client *http.Client
}

func (c *webMailTestClient) Do(r *http.Request) (*http.Response, error) {
	return c.client.Do(r)
}

func TestListInboxWithQuotedDSID(t *testing.T) {
	for _, dsid := range []string{"12345", `12345"`, `"12345"`} {
		t.Run(dsid, func(t *testing.T) {
			c := NewWebClient(map[string]string{"session": "test-session"}, dsid, "icloud.com")
			var paths []string
			transport := webMailTestTransport(func(r *http.Request) (*http.Response, error) {
				paths = append(paths, r.URL.Path)
				// 模拟上游拒绝未编码的引号，并校验从 Cookie 提取的账号标识。
				if strings.ContainsAny(r.URL.RawQuery, `" `) || r.URL.Query().Get("dsid") != "12345" {
					return &http.Response{StatusCode: 400, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
				}
				if cookie, err := r.Cookie("session"); err != nil || cookie.Value != "test-session" {
					return nil, fmt.Errorf("missing session cookie")
				}
				var body string
				switch r.URL.Path {
				case "/setup/ws/1/validate":
					body = `{"webservices":{"mccgateway":{"url":"https://p42-mccgateway.icloud.com:443"}}}`
				case "/mailws2/v1/thread/search":
					if r.URL.Host != "p42-mccgateway.icloud.com" {
						return nil, fmt.Errorf("wrong mail gateway")
					}
					body = `{"totalThreadsReturned":1,"threadList":[{"threadId":"1","subject":"test subject","senders":["sender@example.com"],"preview":"test preview"}]}`
				default:
					return nil, fmt.Errorf("unexpected path: %s", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			c.httpc = &webMailTestClient{HttpClient: c.httpc, client: &http.Client{Jar: c.httpc.GetCookieJar(), Transport: transport}}
			messages, err := c.ListInbox(1)
			if err != nil {
				t.Fatalf("ListInbox(): %v", err)
			}
			if len(messages) != 1 || messages[0].Subject != "test subject" {
				t.Fatalf("unexpected messages: %#v", messages)
			}
			if len(paths) != 2 {
				t.Fatalf("expected validate and search requests, got %v", paths)
			}
		})
	}
}

func TestWebMailParamsEncodeValuesAndPreserveURL(t *testing.T) {
	c := &WebClient{dsid: `123"&injected=yes`, clientID: "client+id"}
	u, err := url.Parse(c.withParams("https://example.com/search?keep=a%2Bb&dsid=old#section"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("dsid") != c.dsid || len(q["dsid"]) != 1 || q.Has("injected") {
		t.Fatalf("DSID was not encoded as a single value: %v", q)
	}
	if q.Get("clientId") != c.clientID || q.Get("keep") != "a+b" || u.Fragment != "section" {
		t.Fatalf("query or fragment changed unexpectedly: %s", u)
	}
	if q.Get("clientBuildNumber") != WebClientBuildNumber || q.Get("clientMasteringNumber") != WebClientBuildNumber {
		t.Fatalf("missing build parameters: %v", q)
	}
	if strings.ContainsAny(u.RawQuery, `" `) {
		t.Fatalf("unsafe raw query: %q", u.RawQuery)
	}
}
