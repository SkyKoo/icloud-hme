package account

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"icloud-hme/internal/hme"
)

const loginTTL = 5 * time.Minute
const maxPendingLogins = 32
const maxOTPAttempts = 5

type loginClient interface {
	Login(string, string, hme.OTPProvider) error
	ContinueLogin(string) error
	ValidateSession() error
	AccountInfo() *hme.AccountInfo
	GetCookies() map[string]string
}

type loginKey struct{ accountID, sessionID string }
type pendingLogin struct {
	mu       sync.Mutex
	client   loginClient
	expires  time.Time
	attempts int
	timer    *time.Timer
}

func expiredLogin() error { return &hme.LoginError{Stage: hme.LoginOTP, Kind: hme.LoginExpired} }

// LoginAccount 将两次 HTTP 请求绑定到同一个 Apple 登录。内存中仅保留临时认证状态，
// 不保存密码或验证码；成功、超时或不可恢复的错误都会移除状态。
func (m *Manager) LoginAccount(id, sessionID, password, otp string) (Summary, error) {
	snap, ok := m.GetAccount(id)
	if !ok {
		return Summary{}, fmt.Errorf("账号不存在")
	}
	if sessionID == "" {
		return Summary{}, expiredLogin()
	}
	key := loginKey{id, sessionID}
	var pending *pendingLogin
	if otp == "" {
		email := firstNonEmpty(snap.ICloudEmail, snap.RealEmail)
		if email == "" {
			return Summary{}, fmt.Errorf("账号未设置邮箱地址")
		}
		factory := m.newLoginClient
		if factory == nil {
			factory = func(host, proxy string) (loginClient, error) { return hme.NewClient(nil, host, proxy, false) }
		}
		client, err := factory(snap.Host, snap.Proxy)
		if err != nil {
			return Summary{}, err
		}
		pending = &pendingLogin{client: client, expires: time.Now().Add(loginTTL)}
		pending.mu.Lock()
		defer pending.mu.Unlock()
		m.loginMu.Lock()
		if m.logins == nil {
			m.logins = make(map[loginKey]*pendingLogin)
		}
		if previous := m.logins[key]; previous != nil {
			previous.timer.Stop()
			delete(m.logins, key)
		}
		if len(m.logins) >= maxPendingLogins {
			m.loginMu.Unlock()
			return Summary{}, &hme.LoginError{Stage: hme.LoginStart, Kind: hme.LoginRateLimited}
		}
		m.logins[key] = pending
		pending.timer = time.AfterFunc(loginTTL, func() { m.discardLogin(key, pending) })
		m.loginMu.Unlock()
		err = client.Login(email, password, nil)
		return m.finishLoginAttempt(key, pending, err)
	}
	m.loginMu.Lock()
	pending = m.logins[key]
	m.loginMu.Unlock()
	if pending == nil {
		return Summary{}, expiredLogin()
	}
	pending.mu.Lock()
	defer pending.mu.Unlock()
	m.loginMu.Lock()
	valid := m.logins[key] == pending && time.Now().Before(pending.expires)
	m.loginMu.Unlock()
	if !valid {
		m.discardLogin(key, pending)
		return Summary{}, expiredLogin()
	}
	pending.attempts++
	return m.finishLoginAttempt(key, pending, pending.client.ContinueLogin(otp))
}

// 调用方持有 pending.mu；网络请求在全局锁外执行。
func (m *Manager) finishLoginAttempt(key loginKey, pending *pendingLogin, err error) (Summary, error) {
	if err != nil {
		var failure *hme.LoginError
		if errors.As(err, &failure) && (failure.Kind == hme.LoginOTPRequired || failure.Kind == hme.LoginOTPInvalid) {
			if pending.attempts < maxOTPAttempts {
				return Summary{}, err
			}
			err = expiredLogin()
		}
		m.discardLogin(key, pending)
		return Summary{}, err
	}
	if err = pending.client.ValidateSession(); err != nil {
		m.discardLogin(key, pending)
		return Summary{}, hme.WrapLoginError(hme.LoginValidate, err)
	}
	m.loginMu.Lock()
	defer m.loginMu.Unlock()
	if m.logins[key] != pending || !time.Now().Before(pending.expires) {
		if m.logins[key] == pending {
			pending.timer.Stop()
			delete(m.logins, key)
		}
		return Summary{}, expiredLogin()
	}
	pending.timer.Stop()
	delete(m.logins, key)
	return m.saveValidatedLogin(key.accountID, pending.client)
}

func (m *Manager) discardLogin(key loginKey, pending *pendingLogin) {
	m.loginMu.Lock()
	defer m.loginMu.Unlock()
	if m.logins[key] == pending {
		pending.timer.Stop()
		delete(m.logins, key)
	}
}

func (m *Manager) validateAndSaveLogin(id string, client loginClient) (Summary, error) {
	if err := client.ValidateSession(); err != nil {
		return Summary{}, hme.WrapLoginError(hme.LoginValidate, err)
	}
	return m.saveValidatedLogin(id, client)
}

// 只有新会话校验成功才替换原 Cookie；持久化失败时恢复内存中的原账号。
func (m *Manager) saveValidatedLogin(id string, client loginClient) (Summary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.accounts[id]
	if !ok {
		return Summary{}, fmt.Errorf("账号不存在")
	}
	previous := copyAccount(cur)
	cur.Cookies = client.GetCookies()
	cur.Status = "active"
	cur.LastValidated = time.Now().Format(time.RFC3339)
	cur.LastError = ""
	if info := client.AccountInfo(); info != nil {
		cur.RealEmail = firstNonEmpty(info.AppleID, info.PrimaryEmail)
		if cur.ICloudEmail == "" {
			cur.ICloudEmail = deriveICloudEmail(info)
		}
	}
	if err := m.save(); err != nil {
		m.accounts[id] = previous
		return Summary{}, hme.WrapLoginError(hme.LoginSave, err)
	}
	return cur.Summary(), nil
}
