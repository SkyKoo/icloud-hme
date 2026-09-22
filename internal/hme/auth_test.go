package hme

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"testing"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
)

// 嵌入真实客户端以保留 Cookie jar，仅替换 HTTP 传输，不访问 Apple。
type loginHTTPStub struct {
	tls_client.HttpClient
	respond func(*http.Request) (*http.Response, error)
}

func (s *loginHTTPStub) Do(r *http.Request) (*http.Response, error) { return s.respond(r) }
func authResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func TestLoginFailureStages(t *testing.T) {
	cases := []struct {
		name   string
		stage  LoginStage
		status int
		body   string
		otp    bool
		kind   LoginFailure
	}{
		{"start forbidden", LoginStart, 403, "private-cookie", false, LoginRejected},
		{"federate unauthorized", LoginFederate, 401, "private-account", false, LoginRejected},
		{"challenge forbidden", LoginPassword, 403, "private-response", false, LoginRejected},
		{"challenge HTML", LoginPassword, 200, "<html>private-token</html>", false, LoginInvalidResponse},
		{"challenge missing fields", LoginPassword, 200, `{}`, false, LoginInvalidResponse},
		{"password rejected", LoginComplete, 401, "private-password", false, LoginRejected},
		{"terms", LoginComplete, 412, "private-response", false, LoginTermsRequired},
		{"OTP needed", LoginComplete, 409, "private-response", false, LoginOTPRequired},
		{"OTP wrong", LoginOTP, 400, "private-otp", true, LoginOTPInvalid},
		{"OTP throttled", LoginOTP, 429, "private-otp", true, LoginRateLimited},
		{"trust unavailable", LoginTrust, 503, "private-token", false, LoginUnavailable},
		{"session rejected", LoginWebSession, 421, "private-token", false, LoginRejected},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewClient(nil, "icloud.com", "", false)
			if err != nil {
				t.Fatal(err)
			}
			reached := false
			c.httpc = &loginHTTPStub{HttpClient: c.httpc, respond: func(r *http.Request) (*http.Response, error) {
				var stage LoginStage
				status, body := 200, `{}`
				switch {
				case strings.Contains(r.URL.Path, "authorize/signin"):
					stage = LoginStart
				case r.URL.Path == "/appleauth/auth/federate":
					stage = LoginFederate
				case r.URL.Path == "/appleauth/auth/signin/init":
					stage = LoginPassword
					body = `{"iteration":2,"salt":"c2FsdA==","b":"Ag==","c":"server-challenge","protocol":"s2k"}`
				case r.URL.Path == "/appleauth/auth/signin/complete":
					stage = LoginComplete
					var payload map[string]any
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					if payload["c"] != "server-challenge" {
						t.Fatalf("wrong authentication challenge: %v", payload["c"])
					}
					if tt.otp {
						status = 409
					}
				case strings.Contains(r.URL.Path, "verify/trusteddevice"):
					stage = LoginOTP
					status = 204
				case r.URL.Path == "/appleauth/auth/2sv/trust":
					stage = LoginTrust
					status = 204
				case r.URL.Path == "/setup/ws/1/accountLogin":
					stage = LoginWebSession
				default:
					t.Fatalf("unexpected request %s", r.URL.Path)
				}
				if stage == tt.stage {
					reached = true
					status = tt.status
					body = tt.body
				}
				return authResponse(r, status, body), nil
			}}
			var otp OTPProvider
			if tt.otp {
				otp = func() (string, error) { return "123456", nil }
			}
			err = c.Login("test@example.com", "private-password", otp)
			var got *LoginError
			if !reached || !errors.As(err, &got) {
				t.Fatalf("no staged failure: reached=%v err=%v", reached, err)
			}
			stage := tt.stage
			if tt.kind == LoginOTPRequired {
				stage = LoginOTP
			}
			status := tt.status
			if tt.kind == LoginInvalidResponse {
				status = 0
			}
			if got.Stage != stage || got.Kind != tt.kind || got.Status != status {
				t.Fatalf("wrong diagnostic: %v", got)
			}
			if strings.Contains(got.Error(), "private-") || strings.Contains(got.Error(), "test@example.com") {
				t.Fatal("diagnostic leaked sensitive data")
			}
		})
	}
}

func TestLoginChallengeRoundTrip(t *testing.T) {
	c, err := NewClient(nil, "icloud.com", "", false)
	if err != nil {
		t.Fatal(err)
	}
	sawChallenge := false
	c.httpc = &loginHTTPStub{HttpClient: c.httpc, respond: func(r *http.Request) (*http.Response, error) {
		status, body := 200, `{}`
		switch r.URL.Path {
		case "/appleauth/auth/signin/init":
			body = `{"iteration":2,"salt":"c2FsdA==","b":"Ag==","c":"server-challenge","protocol":"s2k"}`
		case "/appleauth/auth/signin/complete":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			sawChallenge = payload["c"] == "server-challenge"
		case "/appleauth/auth/2sv/trust":
			status = 204
		case "/setup/ws/1/accountLogin":
			body = `{"dsInfo":{"dsid":"123"}}`
			u, _ := url.Parse(c.Origin())
			c.httpc.SetCookies(u, []*http.Cookie{{Name: "X-APPLE-WEBAUTH-TOKEN", Value: "synthetic-token", Path: "/"}})
		}
		return authResponse(r, status, body), nil
	}}
	if err := c.Login("test@example.com", "synthetic-password", nil); err != nil {
		t.Fatal(err)
	}
	if !sawChallenge || c.Cookies["X-APPLE-WEBAUTH-TOKEN"] != "synthetic-token" {
		t.Fatal("challenge or cookie handoff failed")
	}
}

func TestLoginTransportErrorIsSanitized(t *testing.T) {
	c, err := NewClient(nil, "icloud.com", "", false)
	if err != nil {
		t.Fatal(err)
	}
	c.httpc = &loginHTTPStub{HttpClient: c.httpc, respond: func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("private-password https://user:private-proxy@example.com/?token=private-token")
	}}
	err = c.Login("private-account@example.com", "private-password", nil)
	var got *LoginError
	if !errors.As(err, &got) || got.Stage != LoginStart || got.Kind != LoginUnavailable || strings.Contains(err.Error(), "private-") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestVerboseRequestDoesNotExposeSecrets(t *testing.T) {
	c, err := NewClient(map[string]string{"X-APPLE-WEBAUTH-TOKEN": "private-cookie"}, "icloud.com", "", true)
	if err != nil {
		t.Fatal(err)
	}
	c.httpc = &loginHTTPStub{HttpClient: c.httpc, respond: func(r *http.Request) (*http.Response, error) {
		return authResponse(r, 401, `{"secret":"private-response"}`), nil
	}}
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { os.Stdout = old; r.Close(); w.Close() }()
	os.Stdout = w
	_, requestErr := c.request("POST", "https://example.com/validate?dsid=private-id", nil, 0, 1)
	w.Close()
	os.Stdout = old
	output, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if requestErr == nil || !strings.Contains(requestErr.Error(), "401") {
		t.Fatalf("missing status: %v", requestErr)
	}
	for _, secret := range []string{"private-cookie", "private-response", "private-id"} {
		if strings.Contains(string(output)+requestErr.Error(), secret) {
			t.Fatalf("verbose output exposed %s", secret)
		}
	}
}
