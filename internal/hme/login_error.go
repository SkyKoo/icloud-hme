package hme

import (
	"errors"
	"fmt"
)

// LoginStage 标识固定登录阶段，不含账号、请求 URL 或服务端返回文本。
type LoginStage uint8

const (
	LoginStart LoginStage = iota + 1
	LoginFederate
	LoginPassword
	LoginComplete
	LoginOTP
	LoginTrust
	LoginWebSession
	LoginValidate
	LoginSave
)

func (s LoginStage) String() string {
	return map[LoginStage]string{LoginStart: "start", LoginFederate: "federate", LoginPassword: "password_challenge", LoginComplete: "password_verify", LoginOTP: "otp_verify", LoginTrust: "trust", LoginWebSession: "web_session", LoginValidate: "session_validate", LoginSave: "session_save"}[s]
}
func (s LoginStage) Label() string {
	label := map[LoginStage]string{LoginStart: "建立登录连接", LoginFederate: "提交账号", LoginPassword: "获取密码验证参数", LoginComplete: "验证账号密码", LoginOTP: "验证验证码", LoginTrust: "确认受信任设备", LoginWebSession: "获取新会话", LoginValidate: "校验新会话", LoginSave: "保存新会话"}[s]
	if label == "" {
		return "登录"
	}
	return label
}

type LoginFailure uint8

const (
	LoginUnavailable LoginFailure = iota + 1
	LoginRejected
	LoginOTPRequired
	LoginOTPInvalid
	LoginTermsRequired
	LoginRateLimited
	LoginInvalidResponse
	LoginExpired
)

func (k LoginFailure) String() string {
	return map[LoginFailure]string{LoginUnavailable: "unavailable", LoginRejected: "rejected", LoginOTPRequired: "otp_required", LoginOTPInvalid: "otp_invalid", LoginTermsRequired: "terms_required", LoginRateLimited: "rate_limited", LoginInvalidResponse: "invalid_response", LoginExpired: "expired"}[k]
}

// LoginError 只携带枚举和状态码；不持有原始响应、密码、Cookie 或代理 URL。
type LoginError struct {
	Stage  LoginStage
	Kind   LoginFailure
	Status int
}

func (e *LoginError) Error() string {
	return fmt.Sprintf("icloud login stage=%s kind=%s upstream_status=%d", e.Stage, e.Kind, e.Status)
}

// HTTPStatusError 不把可能含 Cookie/令牌的上游响应体放进错误字符串。
type HTTPStatusError struct{ StatusCode int }

func (e *HTTPStatusError) Error() string { return fmt.Sprintf("HTTP %d", e.StatusCode) }

// WrapLoginError 保留认证阶段，避免使用会话关键词去推断登录失败原因。
func WrapLoginError(stage LoginStage, err error) *LoginError {
	var login *LoginError
	if errors.As(err, &login) {
		return login
	}
	result := &LoginError{Stage: stage, Kind: LoginUnavailable}
	var response *HTTPStatusError
	if errors.As(err, &response) {
		result.Status = response.StatusCode
		switch response.StatusCode {
		case 429:
			result.Kind = LoginRateLimited
		case 401, 403, 421:
			result.Kind = LoginRejected
		}
		if stage == LoginOTP && (response.StatusCode == 400 || response.StatusCode == 401 || response.StatusCode == 403 || response.StatusCode == 422) {
			result.Kind = LoginOTPInvalid
		}
	}
	return result
}
