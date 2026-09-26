// Package hme - iCloud 认证模块
//
// 基于 Go-iClient 项目实现完整的 SRP (Secure Remote Password) 登录流程,
// 支持双重认证 (2FA),登录成功后提取 session token Cookie。
package hme

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/crypto/pbkdf2"

	http "github.com/bogdanfinn/fhttp"
	"icloud-hme/internal/srp"
)

// AuthEndpoints iCloud 认证 API 端点
const (
	OAuthClientID = "d39ba9916b7251055b22c7f910e2ea796ee65e98b2ddecea8f5dde8d9d1a815d"

	authStartFmt    = "https://idmsa.apple.com/appleauth/auth/authorize/signin?frame_id=auth-%s&language=en_US&skVersion=7&iframeId=auth-%s&client_id=%s&redirect_uri=https://www.icloud.com&response_type=code&response_mode=web_message&state=auth-%s&authVersion=latest"
	authFederate    = "https://idmsa.apple.com/appleauth/auth/federate?isRememberMeEnabled=true"
	authInit        = "https://idmsa.apple.com/appleauth/auth/signin/init"
	authComplete    = "https://idmsa.apple.com/appleauth/auth/signin/complete?isRememberMeEnabled=true"
	authOptions     = "https://idmsa.apple.com/appleauth/auth"
	submitSecurity  = "https://idmsa.apple.com/appleauth/auth/verify/%s/securitycode"
	authTrust       = "https://idmsa.apple.com/appleauth/auth/2sv/trust"
	authWebFmt      = "https://setup.icloud.com/setup/ws/1/accountLogin"
	authValidateFmt = "https://setup.icloud.com/setup/ws/1/validate?clientBuildNumber=%s&clientMasteringNumber=%s&clientId=%s"
)

// OTPProvider 双重认证回调函数,返回 2FA 验证码
type OTPProvider func() (string, error)

// authState 保存认证过程中的状态
type authState struct {
	username   string
	frameId    string
	clientId   string
	challenge  string
	authAttr   string
	sessionID  string
	scnt       string
	authToken  string
	trustToken string
	country    string
	dsid       string
}

// Login 使用 iCloud 账号密码登录,获取 session token Cookie。
//
// 登录成功后,可以通过 client.GetCookies() 获取 Cookie。
// 启用 2FA 时,会调用 otpProvider 获取验证码。
func (c *Client) Login(username, password string, otpProvider OTPProvider) error {
	c.pendingAuth = nil
	state := &authState{
		username: username,
	}

	// 1. 初始化 frameId 和 clientId
	if err := c.authStart(state); err != nil {
		return WrapLoginError(LoginStart, err)
	}

	// 2. 提交用户名
	if err := c.authFederate(state); err != nil {
		return WrapLoginError(LoginFederate, err)
	}

	// 3. SRP 协议初始化
	params := srp.GetParams(2048)
	params.NoUserNameInX = true
	srpClient := srp.NewSRPClient(params, nil)

	// 4. 获取 salt 和 B
	authInitResp, err := c.authInit(state, base64.StdEncoding.EncodeToString(srpClient.GetABytes()))
	if err != nil {
		return WrapLoginError(LoginPassword, err)
	}

	// 5. 解码 salt 和 B
	bDec, err := base64.StdEncoding.DecodeString(authInitResp.B)
	if err != nil {
		return &LoginError{Stage: LoginPassword, Kind: LoginInvalidResponse}
	}
	saltDec, err := base64.StdEncoding.DecodeString(authInitResp.Salt)
	if err != nil {
		return &LoginError{Stage: LoginPassword, Kind: LoginInvalidResponse}
	}

	// 6. 生成密码密钥
	passHash := sha256.Sum256([]byte(password))
	passKey := pbkdf2.Key(passHash[:], saltDec, authInitResp.Iteration, 32, sha256.New)

	// 7. 处理挑战
	srpClient.ProcessClientChanllenge([]byte(username), passKey, saltDec, bDec)

	// 8. 提交 SRP 响应 (可能触发 2FA)
	if err := c.authComplete(state, base64.StdEncoding.EncodeToString(srpClient.M1), base64.StdEncoding.EncodeToString(srpClient.M2), otpProvider); err != nil {
		return WrapLoginError(LoginComplete, err)
	}

	return c.finishLogin(state)
}

// ContinueLogin 在同一个客户端、Cookie jar 和 Apple 会话中提交验证码。调用方负责串行化与过期管理。
func (c *Client) ContinueLogin(otp string) error {
	state := c.pendingAuth
	if state == nil {
		return &LoginError{Stage: LoginOTP, Kind: LoginExpired}
	}
	if err := c.verifyOTP(state, otp); err != nil {
		var failure *LoginError
		if !errors.As(err, &failure) || failure.Kind != LoginOTPInvalid {
			c.pendingAuth = nil
		}
		return err
	}
	c.pendingAuth = nil
	return c.finishLogin(state)
}

func (c *Client) finishLogin(state *authState) error {
	// 9. 信任设备
	if err := c.getTrust(state); err != nil {
		return WrapLoginError(LoginTrust, err)
	}

	// 10. 获取 iCloud Web 服务 Cookie
	if err := c.authenticateWeb(state); err != nil {
		return WrapLoginError(LoginWebSession, err)
	}

	// 11. 保存 Cookie 到 Client
	cookies := c.extractSessionCookies()
	c.Cookies = cookies
	c.log("登录成功,获取到 %d 个 Cookie", len(cookies))
	return nil
}

// --- 认证流程的各步骤 ---

// authStart 初始化 frameId 和 clientId
func (c *Client) authStart(state *authState) error {
	state.frameId = strings.ToLower(uuid.New().String())
	state.clientId = OAuthClientID

	req, err := http.NewRequest("GET", fmt.Sprintf(authStartFmt, state.frameId, state.frameId, state.clientId, state.frameId), nil)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")

	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	state.captureHeaders(resp.Header)

	if resp.StatusCode != 200 {
		return &HTTPStatusError{StatusCode: resp.StatusCode, RetryAfter: NormalizeRetryAfter(resp.Header.Get("Retry-After"))}
	}

	return nil
}

// authFederate 提交用户名
func (c *Client) authFederate(state *authState) error {
	data := `{"accountName":"` + state.username + `","rememberMe":true}`
	req, err := http.NewRequest("POST", authFederate, bytes.NewReader([]byte(data)))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header = c.updateAuthHeaders(req.Header, state)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	state.captureHeaders(resp.Header)

	if resp.StatusCode != 200 {
		return &HTTPStatusError{StatusCode: resp.StatusCode, RetryAfter: NormalizeRetryAfter(resp.Header.Get("Retry-After"))}
	}
	return nil
}

// authInitResp authInit 响应
type authInitResp struct {
	Iteration int    `json:"iteration"`
	Salt      string `json:"salt"`
	Protocol  string `json:"protocol"`
	B         string `json:"b"`
	C         string `json:"c"`
}

// authInit 初始化 SRP 认证
func (c *Client) authInit(state *authState, a string) (*authInitResp, error) {
	reqBody := map[string]interface{}{
		"a":           a,
		"accountName": state.username,
		"protocols":   []string{"s2k", "s2k_fo"},
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", authInit, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header = c.updateAuthHeaders(req.Header, state)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	state.captureHeaders(resp.Header)

	if resp.StatusCode != 200 {
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, RetryAfter: NormalizeRetryAfter(resp.Header.Get("Retry-After"))}
	}
	var result authInitResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, &LoginError{Stage: LoginPassword, Kind: LoginInvalidResponse}
	}
	if result.C == "" || result.Salt == "" || result.B == "" || result.Iteration <= 0 || result.Iteration > 1_000_000 {
		return nil, &LoginError{Stage: LoginPassword, Kind: LoginInvalidResponse}
	}
	// complete 必须回传本次服务端挑战值，不能传 OAuth client ID。
	state.challenge = result.C
	return &result, nil
}

// authComplete 提交 SRP 响应
func (c *Client) authComplete(state *authState, m1, m2 string, otpProvider OTPProvider) error {
	reqBody := map[string]interface{}{
		"accountName": state.username,
		"rememberMe":  true,
		"trustTokens": []string{},
		"m1":          m1,
		"c":           state.challenge,
		"m2":          m2,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", authComplete, bytes.NewReader(data))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header = c.updateAuthHeaders(req.Header, state)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	state.captureHeaders(resp.Header)

	switch resp.StatusCode {
	case 200:
		return nil
	case 409:
		// 需要 2FA
		return c.handleTwoFactor(state, resp, otpProvider)
	case 403:
		return &HTTPStatusError{StatusCode: resp.StatusCode, RetryAfter: NormalizeRetryAfter(resp.Header.Get("Retry-After"))}
	case 412:
		return &LoginError{Stage: LoginComplete, Kind: LoginTermsRequired, Status: resp.StatusCode}
	default:
		return &HTTPStatusError{StatusCode: resp.StatusCode, RetryAfter: NormalizeRetryAfter(resp.Header.Get("Retry-After"))}
	}
}

// handleTwoFactor 处理双重认证
func (c *Client) handleTwoFactor(state *authState, signinResp *http.Response, otpProvider OTPProvider) error {
	state.captureHeaders(signinResp.Header)

	if otpProvider == nil {
		c.pendingAuth = state
		return &LoginError{Stage: LoginOTP, Kind: LoginOTPRequired, Status: signinResp.StatusCode}
	}

	otp, err := otpProvider()
	if err != nil {
		return WrapLoginError(LoginOTP, err)
	}

	return c.verifyOTP(state, otp)
}

func (c *Client) verifyOTP(state *authState, otp string) error {
	// 提交 2FA 验证码
	reqBody := map[string]interface{}{
		"securityCode": map[string]string{"code": otp},
	}

	data, _ := json.Marshal(reqBody)
	req, err := http.NewRequest("POST", fmt.Sprintf(submitSecurity, "trusteddevice"), bytes.NewReader(data))
	if err != nil {
		return WrapLoginError(LoginOTP, err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header = c.updateAuthHeaders(req.Header, state)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return WrapLoginError(LoginOTP, err)
	}
	defer resp.Body.Close()
	state.captureHeaders(resp.Header)

	if resp.StatusCode != 204 {
		return WrapLoginError(LoginOTP, &HTTPStatusError{StatusCode: resp.StatusCode, RetryAfter: NormalizeRetryAfter(resp.Header.Get("Retry-After"))})
	}

	return nil
}

// getTrust 获取 trust token
func (c *Client) getTrust(state *authState) error {
	req, err := http.NewRequest("GET", authTrust, nil)
	if err != nil {
		return err
	}

	req.Header = c.updateAuthHeaders(req.Header, state)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	state.captureHeaders(resp.Header)

	if resp.StatusCode != 204 {
		return &HTTPStatusError{StatusCode: resp.StatusCode, RetryAfter: NormalizeRetryAfter(resp.Header.Get("Retry-After"))}
	}

	return nil
}

// authenticateWeb 认证 iCloud Web 服务
func (c *Client) authenticateWeb(state *authState) error {
	if state.authToken == "" {
		return &LoginError{Stage: LoginWebSession, Kind: LoginInvalidResponse}
	}
	payload := map[string]interface{}{
		"dsWebAuthToken": state.authToken,
		"extended_login": true,
		"trustToken":     state.trustToken,
	}
	if state.country != "" {
		payload["accountCountryCode"] = state.country
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", c.SetupURL()+"/accountLogin", bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.Origin())
	req.Header.Set("Accept", "*/*")

	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	state.captureHeaders(resp.Header)

	if resp.StatusCode != 200 {
		return &HTTPStatusError{StatusCode: resp.StatusCode, RetryAfter: NormalizeRetryAfter(resp.Header.Get("Retry-After"))}
	}

	var result struct {
		DsInfo struct {
			Dsid string `json:"dsid"`
		} `json:"dsInfo"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return &LoginError{Stage: LoginWebSession, Kind: LoginInvalidResponse}
	}
	state.dsid = result.DsInfo.Dsid

	// 复制 idmsa.apple.com 的 Cookie 到 icloud.com
	u1, _ := url.Parse("https://idmsa.apple.com")
	u2, _ := url.Parse("https://" + c.Host)
	cookies := c.httpc.GetCookies(u1)
	c.httpc.SetCookies(u2, cookies)

	return nil
}

// extractSessionCookies 提取 session token Cookie
func (c *Client) extractSessionCookies() map[string]string {
	cookies := make(map[string]string)
	u, _ := url.Parse(c.Origin())
	for _, cookie := range c.httpc.GetCookies(u) {
		cookies[cookie.Name] = cookie.Value
	}
	return cookies
}

// updateAuthHeaders 更新认证请求所需的头部
func (c *Client) updateAuthHeaders(header http.Header, state *authState) http.Header {
	if state.scnt != "" {
		header.Set("scnt", state.scnt)
	}
	if state.sessionID != "" {
		header.Set("X-Apple-ID-Session-Id", state.sessionID)
	}

	// 与 authorize 中的客户端和 frame 保持一致，供 Apple 将令牌签发给当前 iCloud 登录。
	header.Set("X-Apple-Widget-Key", state.clientId)
	header.Set("X-Apple-Oauth-Client-Id", state.clientId)
	header.Set("X-Apple-Oauth-Client-Type", "firstPartyAuth")
	header.Set("X-Apple-Oauth-Redirect-URI", "https://www.icloud.com")
	header.Set("X-Apple-Oauth-Require-Grant-Code", "true")
	header.Set("X-Apple-Oauth-Response-Mode", "web_message")
	header.Set("X-Apple-Oauth-Response-Type", "code")
	header.Set("X-Apple-Oauth-State", "auth-"+state.frameId)
	header.Set("X-Apple-Frame-Id", "auth-"+state.frameId)
	if state.authAttr != "" {
		header.Set("X-Apple-Auth-Attributes", state.authAttr)
	}
	header.Set("X-Requested-With", "XMLHttpRequest")
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "application/json")
	header.Set("Referer", "https://idmsa.apple.com/")
	header.Set("Origin", "https://idmsa.apple.com")
	header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")

	return header
}

// Validate 验证当前 Cookie 是否有效
func (c *Client) Validate() (bool, error) {
	if len(c.Cookies) == 0 {
		return false, fmt.Errorf("无 Cookie")
	}
	// 简单实现：尝试调用 validate 端点
	err := c.ValidateSession()
	if err != nil {
		return false, err
	}
	return true, nil
}

// captureHeaders 累积认证响应头：验证码响应可能携带会话令牌，trust 响应只补充信任令牌。
func (state *authState) captureHeaders(header http.Header) {
	for name, target := range map[string]*string{
		"X-Apple-ID-Session-Id":      &state.sessionID,
		"scnt":                       &state.scnt,
		"X-Apple-Auth-Attributes":    &state.authAttr,
		"X-Apple-Session-Token":      &state.authToken,
		"X-Apple-TwoSV-Trust-Token":  &state.trustToken,
		"X-Apple-ID-Account-Country": &state.country,
	} {
		if value := header.Get(name); value != "" {
			*target = value
		}
	}
}

// GetCookies 返回会话 Cookie 的副本；只应在服务端保存，不返回给管理台。
func (c *Client) GetCookies() map[string]string {
	cookies := make(map[string]string, len(c.Cookies))
	for name, value := range c.Cookies {
		cookies[name] = value
	}
	return cookies
}
