package hme

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	http "github.com/bogdanfinn/fhttp"
)

type diagnosticTimeout struct{}

func (diagnosticTimeout) Error() string   { return "private-cookie private-proxy" }
func (diagnosticTimeout) Timeout() bool   { return true }
func (diagnosticTimeout) Temporary() bool { return true }

func TestCreateAliasPreservesStageAndDoesNotReplay(t *testing.T) {
	cases := []struct {
		name        string
		stage       UpstreamStage
		status      int
		body, retry string
		transport   error
		kind        UpstreamKind
	}{
		{"validate rate limit", StageValidate, 429, "private-body", "120", nil, UpstreamRateLimited},
		{"generate rate limit", StageGenerate, 429, "private-body", "120", nil, UpstreamRateLimited},
		{"reserve rate limit", StageReserve, 429, "private-body", "120", nil, UpstreamRateLimited},
		{"session expired", StageGenerate, 421, "private-cookie", "", nil, UpstreamSessionExpired},
		{"server unavailable", StageReserve, 503, "private-token", "60", nil, UpstreamUnavailable},
		{"bad gateway is not rate limit", StageReserve, 502, "rate limit maybe private-body", "", nil, UpstreamUnavailable},
		{"business rejection is not rate limit", StageGenerate, 200, `{"success":false,"error":{"errorMessage":"rate limit private-secret"}}`, "", nil, UpstreamRejected},
		{"invalid JSON", StageReserve, 200, "<html>private-token</html>", "", nil, UpstreamInvalidResponse},
		{"missing candidate", StageGenerate, 200, `{"success":true,"result":{}}`, "", nil, UpstreamInvalidResponse},
		{"transport", StageReserve, 0, "", "", errors.New("private-password proxy URL 401"), UpstreamNetwork},
		{"timeout", StageReserve, 0, "", "", diagnosticTimeout{}, UpstreamTimeout},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewClient(nil, "icloud.com.cn", "", false)
			if err != nil {
				t.Fatal(err)
			}
			counts := map[UpstreamStage]int{}
			c.httpc = &loginHTTPStub{HttpClient: c.httpc, respond: func(r *http.Request) (*http.Response, error) {
				var stage UpstreamStage
				body := `{"success":true}`
				switch {
				case strings.HasSuffix(r.URL.Path, "/validate"):
					stage = StageValidate
					body = `{"webservices":{"premiummailsettings":{"url":"https://maildomainws.icloud.com"}}}`
				case strings.HasSuffix(r.URL.Path, "/generate"):
					stage = StageGenerate
					body = `{"success":true,"result":{"hme":"synthetic@example.test"}}`
				case strings.HasSuffix(r.URL.Path, "/reserve"):
					stage = StageReserve
				default:
					t.Fatalf("unexpected path %s", r.URL.Path)
				}
				counts[stage]++
				if stage == tt.stage {
					if tt.transport != nil {
						return nil, tt.transport
					}
					response := authResponse(r, tt.status, tt.body)
					response.Header.Set("Retry-After", tt.retry)
					return response, nil
				}
				return authResponse(r, 200, body), nil
			}}
			_, err = c.CreateAlias("synthetic", 5)
			var got *UpstreamError
			if !errors.As(err, &got) || got.Stage != tt.stage || got.Kind != tt.kind || got.Status != tt.status || got.RetryAfter != tt.retry {
				t.Fatalf("metadata lost: %#v, %v", got, err)
			}
			if counts[tt.stage] != 1 {
				t.Fatalf("failure was replayed: %#v", counts)
			}
			if strings.Contains(err.Error(), "private-") {
				t.Fatal("error leaks upstream details")
			}
			if tt.stage == StageGenerate && counts[StageReserve] != 0 {
				t.Fatal("reserved after generation failed")
			}
		})
	}
}

func TestCreateAliasSuccessSupportsNestedCandidate(t *testing.T) {
	for _, candidate := range []string{`"synthetic@example.test"`, `{"hme":"synthetic@example.test"}`, `{"email":"synthetic@example.test"}`} {
		c, _ := NewClient(nil, "icloud.com", "", false)
		c.serviceURL = "https://maildomainws.icloud.com"
		calls := 0
		c.httpc = &loginHTTPStub{HttpClient: c.httpc, respond: func(r *http.Request) (*http.Response, error) {
			calls++
			if strings.HasSuffix(r.URL.Path, "/generate") {
				return authResponse(r, 200, `{"success":true,"result":{"hme":`+candidate+`}}`), nil
			}
			return authResponse(r, 200, `{"success":true}`), nil
		}}
		result, err := c.CreateAlias("synthetic", 5)
		if err != nil || result.Email != "synthetic@example.test" || calls != 2 {
			t.Fatalf("success broken: %v %#v calls=%d", err, result, calls)
		}
	}
}

func TestSafeRetryAfterAndLoginPropagation(t *testing.T) {
	for _, tt := range []struct{ raw, want string }{
		{"120", "120"}, {"0", "0"}, {" 0030 ", "30"},
		{"Wed, 30 Sep 2026 12:00:00 GMT", "Wed, 30 Sep 2026 12:00:00 GMT"},
		{"private-cookie", ""}, {"12\r\nCookie: private", ""}, {"-1", ""}, {"1.5", ""}, {strings.Repeat("9", 200), ""},
	} {
		if got := NormalizeRetryAfter(tt.raw); got != tt.want {
			t.Errorf("bad Retry-After normalization: %q", got)
		}
	}
	for _, err := range []error{
		&HTTPStatusError{StatusCode: 429, RetryAfter: "60"},
		&UpstreamError{Stage: StageValidate, Kind: UpstreamRateLimited, Status: 429, RetryAfter: "60"},
	} {
		got := WrapLoginError(LoginValidate, fmt.Errorf("private-context: %w", err))
		if got.Kind != LoginRateLimited || got.Status != 429 || got.RetryAfter != "60" {
			t.Fatalf("login lost metadata: %#v", got)
		}
	}
}
