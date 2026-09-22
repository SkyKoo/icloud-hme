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
				resp := authResponse(r, status, body)
				if r.URL.Path == "/appleauth/auth/signin/complete" && status == 200 {
					resp.Header.Set("X-Apple-Session-Token", "synthetic-session-token")
				}
				return resp, nil
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
		resp := authResponse(r, status, body)
		if r.URL.Path == "/appleauth/auth/signin/complete" && status == 200 {
			resp.Header.Set("X-Apple-Session-Token", "synthetic-session-token")
		}
		return resp, nil
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

func TestOTPTokenSurvivesTrust(t *testing.T) {
	c, err := NewClient(nil, "icloud.com", "", false)
	if err != nil {
		t.Fatal(err)
	}
	state := &authState{}
	c.httpc = &loginHTTPStub{HttpClient: c.httpc, respond: func(r *http.Request) (*http.Response, error) {
		resp := authResponse(r, 204, "")
		if strings.Contains(r.URL.Path, "verify/trusteddevice") {
			resp.Header.Set("X-Apple-Session-Token", "synthetic-session-token")
		} else {
			resp.Header.Set("X-Apple-TwoSV-Trust-Token", "synthetic-trust-token")
		}
		return resp, nil
	}}
	if err := c.handleTwoFactor(state, authResponse(nil, 409, ""), func() (string, error) { return "123456", nil }); err != nil {
		t.Fatal(err)
	}
	if err := c.getTrust(state); err != nil {
		t.Fatal(err)
	}
	if state.authToken != "synthetic-session-token" || state.trustToken != "synthetic-trust-token" {
		t.Fatal("OTP session token was lost while obtaining trust token")
	}
}

func TestTwoPhaseLoginPreservesSession(t *testing.T) {
	for _, host := range []string{"icloud.com", "icloud.com.cn"} {
		t.Run(host, func(t *testing.T) {
			c, err := NewClient(nil, host, "", false)
			if err != nil {
				t.Fatal(err)
			}
			calls := map[string]int{}
			c.httpc = &loginHTTPStub{HttpClient: c.httpc, respond: func(r *http.Request) (*http.Response, error) {
				path := r.URL.Path
				calls[path]++
				resp := authResponse(r, 200, `{}`)
				switch {
				case strings.Contains(path, "authorize/signin"):
					c.httpc.SetCookies(r.URL, []*http.Cookie{{Name: "synthetic-auth", Value: "same-jar", Path: "/"}})
				case path == "/appleauth/auth/signin/init":
					resp = authResponse(r, 200, `{"iteration":2,"salt":"c2FsdA==","b":"Ag==","c":"server-challenge","protocol":"s2k"}`)
				case path == "/appleauth/auth/signin/complete":
					resp = authResponse(r, 409, `{}`)
					resp.Header.Set("X-Apple-ID-Session-Id", "synthetic-session")
					resp.Header.Set("scnt", "first-scnt")
					resp.Header.Set("X-Apple-ID-Account-Country", "CHN")
				case strings.Contains(path, "verify/trusteddevice"):
					if r.Header.Get("X-Apple-Widget-Key") != OAuthClientID || r.Header.Get("X-Apple-Oauth-Client-Id") != OAuthClientID || r.Header.Get("X-Apple-Frame-Id") == "auth-" {
						t.Fatal("OTP lost OAuth client identity")
					}
					var payload struct {
						SecurityCode struct {
							Code string `json:"code"`
						} `json:"securityCode"`
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					scnt := "first-scnt"
					if calls[path] > 1 {
						scnt = "retry-scnt"
					}
					if r.Header.Get("X-Apple-ID-Session-Id") != "synthetic-session" || r.Header.Get("scnt") != scnt {
						t.Fatal("OTP lost the original login session")
					}
					cookies := c.httpc.GetCookies(r.URL)
					if len(cookies) != 1 || cookies[0].Value != "same-jar" {
						t.Fatal("OTP lost the original cookie jar")
					}
					if payload.SecurityCode.Code == "000000" {
						resp = authResponse(r, 400, `{}`)
						resp.Header.Set("scnt", "retry-scnt")
					} else {
						resp = authResponse(r, 204, "")
						resp.Header.Set("scnt", "verified-scnt")
						resp.Header.Set("X-Apple-Session-Token", "synthetic-session-token")
					}
				case path == "/appleauth/auth/2sv/trust":
					if r.Header.Get("scnt") != "verified-scnt" {
						t.Fatal("trust lost updated scnt")
					}
					resp = authResponse(r, 204, "")
					resp.Header.Set("X-Apple-TwoSV-Trust-Token", "synthetic-trust-token")
				case path == "/setup/ws/1/accountLogin":
					var payload map[string]any
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					if payload["dsWebAuthToken"] != "synthetic-session-token" || payload["trustToken"] != "synthetic-trust-token" || payload["accountCountryCode"] != "CHN" {
						t.Fatal("incorrect session token/country handoff")
					}
					if r.URL.Host != "setup."+host {
						t.Fatal("wrong regional setup host")
					}
					resp = authResponse(r, 200, `{"dsInfo":{"dsid":"123"}}`)
					u, _ := url.Parse(c.Origin())
					c.httpc.SetCookies(u, []*http.Cookie{{Name: "X-APPLE-WEBAUTH-TOKEN", Value: "synthetic-cookie", Path: "/"}})
				}
				return resp, nil
			}}
			assertKind := func(err error, kind LoginFailure) {
				t.Helper()
				var failure *LoginError
				if !errors.As(err, &failure) || failure.Kind != kind {
					t.Fatalf("unexpected result: %v", err)
				}
			}
			assertKind(c.Login("synthetic@example.com", "synthetic-password", nil), LoginOTPRequired)
			assertKind(c.ContinueLogin("000000"), LoginOTPInvalid)
			if err := c.ContinueLogin("123456"); err != nil {
				t.Fatal(err)
			}
			assertKind(c.ContinueLogin("123456"), LoginExpired)
			if calls["/appleauth/auth/signin/init"] != 1 || calls["/appleauth/auth/signin/complete"] != 1 || calls["/setup/ws/1/accountLogin"] != 1 {
				t.Fatal("OTP restarted or replayed login")
			}
			if c.GetCookies()["X-APPLE-WEBAUTH-TOKEN"] != "synthetic-cookie" {
				t.Fatal("new cookie missing")
			}
		})
	}
}

func TestAuthenticateWebRejectsMissingToken(t *testing.T) {
	c, err := NewClient(nil, "icloud.com", "", false)
	if err != nil {
		t.Fatal(err)
	}
	c.httpc = &loginHTTPStub{HttpClient: c.httpc, respond: func(*http.Request) (*http.Response, error) {
		t.Fatal("sent an empty auth token to Apple")
		return nil, nil
	}}
	var failure *LoginError
	err = c.authenticateWeb(&authState{})
	if !errors.As(err, &failure) || failure.Kind != LoginInvalidResponse || failure.Stage != LoginWebSession {
		t.Fatalf("unexpected error: %v", err)
	}
}
