package account

import (
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"icloud-hme/internal/hme"
)

type stubLoginClient struct {
	starts, verifies atomic.Int32
	validateErr      error
	block, entered   chan struct{}
}

func (c *stubLoginClient) Login(_, _ string, _ hme.OTPProvider) error {
	c.starts.Add(1)
	return &hme.LoginError{Stage: hme.LoginOTP, Kind: hme.LoginOTPRequired}
}
func (c *stubLoginClient) ContinueLogin(code string) error {
	c.verifies.Add(1)
	if c.entered != nil {
		close(c.entered)
		<-c.block
	}
	if code == "000000" {
		return &hme.LoginError{Stage: hme.LoginOTP, Kind: hme.LoginOTPInvalid}
	}
	return nil
}
func (c *stubLoginClient) ValidateSession() error        { return c.validateErr }
func (c *stubLoginClient) AccountInfo() *hme.AccountInfo { return nil }
func (c *stubLoginClient) GetCookies() map[string]string {
	return map[string]string{"session": "new-synthetic-cookie"}
}

func loginTestManager(t *testing.T) (*Manager, *stubLoginClient, string) {
	t.Helper()
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	sum, err := m.AddAccountWithInput(AddAccountInput{Name: "synthetic", ICloudEmail: "synthetic@icloud.com", Host: "icloud.com"})
	if err != nil {
		t.Fatal(err)
	}
	c := &stubLoginClient{}
	m.newLoginClient = func(string, string) (loginClient, error) { return c, nil }
	return m, c, sum.ID
}
func requireLoginKind(t *testing.T, err error, kind hme.LoginFailure) {
	t.Helper()
	var failure *hme.LoginError
	if !errors.As(err, &failure) || failure.Kind != kind {
		t.Fatalf("unexpected login failure: %v", err)
	}
}

func TestLoginContinuationScopeAndPersistence(t *testing.T) {
	m, c, id := loginTestManager(t)
	if err := m.SaveCookies(id, map[string]string{"session": "old-synthetic-cookie"}); err != nil {
		t.Fatal(err)
	}
	_, err := m.LoginAccount(id, "admin-session", "synthetic-password", "")
	requireLoginKind(t, err, hme.LoginOTPRequired)
	_, err = m.LoginAccount(id, "other-session", "", "123456")
	requireLoginKind(t, err, hme.LoginExpired)
	other, err := m.AddAccountWithInput(AddAccountInput{Name: "other", ICloudEmail: "other@icloud.com", Host: "icloud.com"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.LoginAccount(other.ID, "admin-session", "", "123456")
	requireLoginKind(t, err, hme.LoginExpired)
	_, err = m.LoginAccount(id, "admin-session", "", "000000")
	requireLoginKind(t, err, hme.LoginOTPInvalid)
	acc, _ := m.GetAccount(id)
	if acc.Cookies["session"] != "old-synthetic-cookie" {
		t.Fatal("failed login replaced cookies")
	}
	if _, err = m.LoginAccount(id, "admin-session", "", "123456"); err != nil {
		t.Fatal(err)
	}
	_, err = m.LoginAccount(id, "admin-session", "", "123456")
	requireLoginKind(t, err, hme.LoginExpired)
	acc, _ = m.GetAccount(id)
	if acc.Cookies["session"] != "new-synthetic-cookie" {
		t.Fatal("new cookies not persisted")
	}
	if c.starts.Load() != 1 || c.verifies.Load() != 2 {
		t.Fatal("login restarted or scope/replay permitted")
	}
	data, err := os.ReadFile(m.dataFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"synthetic-password", "123456", "admin-session"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("temporary credential persisted")
		}
	}
}

func TestLoginExpiryAndAttemptLimit(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{true: "TTL", false: "attempt cap"}[expired], func(t *testing.T) {
			m, c, id := loginTestManager(t)
			_, err := m.LoginAccount(id, "admin-session", "synthetic-password", "")
			requireLoginKind(t, err, hme.LoginOTPRequired)
			if expired {
				m.logins[loginKey{id, "admin-session"}].expires = time.Now().Add(-time.Second)
			} else {
				for i := 0; i < maxOTPAttempts-1; i++ {
					_, err = m.LoginAccount(id, "admin-session", "", "000000")
					requireLoginKind(t, err, hme.LoginOTPInvalid)
				}
			}
			_, err = m.LoginAccount(id, "admin-session", "", "000000")
			requireLoginKind(t, err, hme.LoginExpired)
			if len(m.logins) != 0 {
				t.Fatal("expired login retained")
			}
			if expired && c.verifies.Load() != 0 {
				t.Fatal("expired session contacted Apple")
			}
		})
	}
}

func TestLoginValidationFailurePreservesCookies(t *testing.T) {
	m, c, id := loginTestManager(t)
	if err := m.SaveCookies(id, map[string]string{"session": "old-synthetic-cookie"}); err != nil {
		t.Fatal(err)
	}
	c.validateErr = &hme.HTTPStatusError{StatusCode: 421}
	_, err := m.LoginAccount(id, "admin-session", "synthetic-password", "")
	requireLoginKind(t, err, hme.LoginOTPRequired)
	_, err = m.LoginAccount(id, "admin-session", "", "123456")
	requireLoginKind(t, err, hme.LoginRejected)
	acc, _ := m.GetAccount(id)
	if acc.Cookies["session"] != "old-synthetic-cookie" {
		t.Fatal("failed validation overwrote working cookies")
	}
	if len(m.logins) != 0 {
		t.Fatal("failed login retained")
	}
}

func TestConcurrentOTPIsNotReplayed(t *testing.T) {
	m, c, id := loginTestManager(t)
	_, err := m.LoginAccount(id, "admin-session", "synthetic-password", "")
	requireLoginKind(t, err, hme.LoginOTPRequired)
	c.block = make(chan struct{})
	c.entered = make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := m.LoginAccount(id, "admin-session", "", "123456"); results <- err }()
	}
	<-c.entered
	close(c.block)
	wg.Wait()
	close(results)
	success, expired := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			requireLoginKind(t, err, hme.LoginExpired)
			expired++
		}
	}
	if success != 1 || expired != 1 || c.verifies.Load() != 1 {
		t.Fatal("concurrent OTP replayed")
	}
}
