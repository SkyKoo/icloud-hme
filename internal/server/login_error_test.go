package server

import (
	"bytes"
	"fmt"
	"icloud-hme/internal/hme"
	"log"
	"strings"
	"testing"
)

func TestLoginFailureDoesNotClaimExpiredCookie(t *testing.T) {
	for _, message := range []string{"auth start: unexpected status: 403", "auth federate: unexpected status: 401", "auth complete: HTTP 421", "auth init: Cookie private-secret", "用户名或密码错误"} {
		got := classifyLoginErr(fmt.Errorf("%s", message))
		if got.Code == "UPSTREAM_UNAUTHORIZED" || strings.Contains(got.Message, "会话已失效") || strings.Contains(got.Message, "private-secret") {
			t.Fatalf("login failure misclassified: %#v", got)
		}
	}
}

func TestTypedLoginFailureMappingAndSafeLogs(t *testing.T) {
	cases := []struct {
		stage    hme.LoginStage
		kind     hme.LoginFailure
		upstream int
		want     int
		code     string
	}{
		{hme.LoginStart, hme.LoginRejected, 403, 401, "ICLOUD_LOGIN_REJECTED"},
		{hme.LoginComplete, hme.LoginRejected, 401, 401, "ICLOUD_LOGIN_REJECTED"},
		{hme.LoginOTP, hme.LoginOTPRequired, 409, 409, "OTP_REQUIRED"},
		{hme.LoginOTP, hme.LoginExpired, 0, 409, "ICLOUD_LOGIN_EXPIRED"},
		{hme.LoginOTP, hme.LoginOTPInvalid, 400, 401, "OTP_INVALID"},
		{hme.LoginComplete, hme.LoginTermsRequired, 412, 400, "ICLOUD_LOGIN_ACTION_REQUIRED"},
		{hme.LoginOTP, hme.LoginRateLimited, 429, 429, "ICLOUD_LOGIN_RATE_LIMITED"},
		{hme.LoginPassword, hme.LoginInvalidResponse, 0, 502, "ICLOUD_LOGIN_PROTOCOL_ERROR"},
		{hme.LoginTrust, hme.LoginUnavailable, 503, 502, "ICLOUD_LOGIN_FAILED"},
		{hme.LoginValidate, hme.LoginRejected, 421, 401, "ICLOUD_LOGIN_REJECTED"},
	}
	var logs bytes.Buffer
	before := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(before)
	for _, tt := range cases {
		err := fmt.Errorf("private-envelope: %w", &hme.LoginError{Stage: tt.stage, Kind: tt.kind, Status: tt.upstream})
		got := classifyLoginErr(err)
		if got.Status != tt.want || got.Code != tt.code || strings.Contains(got.Message, "会话已失效") || strings.Contains(got.Message, "private-") {
			t.Fatalf("bad mapping: %#v", got)
		}
		logLoginFailure(err)
	}
	logLoginFailure(fmt.Errorf("private-password private-cookie private-proxy"))
	if strings.Contains(logs.String(), "private-") || !strings.Contains(logs.String(), "stage=start kind=rejected upstream_status=403") {
		t.Fatalf("bad diagnostic log: %s", logs.String())
	}
}
