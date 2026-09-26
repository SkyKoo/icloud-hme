package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"icloud-hme/internal/hme"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUpstreamSessionErrors(t *testing.T) {
	for _, stage := range []string{"validate 失败", "邮件接口", "获取别名列表"} {
		for _, status := range []int{401, 403, 421} {
			t.Run(fmt.Sprintf("%s/%d", stage, status), func(t *testing.T) {
				upstream := fmt.Errorf("%s: HTTP %d: private-response", stage, status)
				got := classifyUpstreamErr("读取失败", fmt.Errorf("junk: %w", upstream))
				if got.Status != http.StatusUnauthorized || got.Code != "UPSTREAM_UNAUTHORIZED" || got.Message != "iCloud 会话已失效，请到「账号」更新 Cookie 或重新登录 iCloud" {
					t.Fatalf("unexpected error: %#v", got)
				}
			})
		}
	}
}

func TestUpstreamOtherErrors(t *testing.T) {
	for _, upstream := range []string{"HTTP 500: unavailable", "连接超时", "unexpected message id 421"} {
		got := classifyUpstreamErr("读取失败", fmt.Errorf("%s", upstream))
		if got.Status != http.StatusBadGateway || got.Code != "UPSTREAM_FAILURE" || got.Message != "读取失败" {
			t.Fatalf("unexpected error: %#v", got)
		}
	}
}

func TestTypedUpstreamMetadataInHTTPResponseAndLogs(t *testing.T) {
	cases := []struct {
		kind             hme.UpstreamKind
		stage            hme.UpstreamStage
		upstream, status int
		code             string
	}{
		{hme.UpstreamRateLimited, hme.StageGenerate, 429, 429, "UPSTREAM_RATE_LIMITED"},
		{hme.UpstreamSessionExpired, hme.StageValidate, 421, 401, "UPSTREAM_UNAUTHORIZED"},
		{hme.UpstreamUnavailable, hme.StageReserve, 502, 503, "UPSTREAM_UNAVAILABLE"},
		{hme.UpstreamNetwork, hme.StageReserve, 0, 502, "UPSTREAM_NETWORK_ERROR"},
		{hme.UpstreamTimeout, hme.StageReserve, 0, 504, "UPSTREAM_TIMEOUT"},
		{hme.UpstreamRejected, hme.StageReserve, 200, 502, "ALIAS_CREATE_FAILED"},
		{hme.UpstreamInvalidResponse, hme.StageGenerate, 200, 502, "UPSTREAM_INVALID_RESPONSE"},
	}
	var logs bytes.Buffer
	before := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(before)
	for _, tt := range cases {
		err := fmt.Errorf("private-cookie private-response: %w", &hme.UpstreamError{Stage: tt.stage, Kind: tt.kind, Status: tt.upstream, RetryAfter: "60"})
		mapped := classifyUpstreamErr("操作失败", err)
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		backendFail(ctx, mapped)
		var body apiResp
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if recorder.Code != tt.status || body.Code != tt.code || body.Stage != tt.stage.String() || body.UpstreamStatus != tt.upstream || body.RetryAfter != "60" || recorder.Header().Get("Retry-After") != "60" {
			t.Fatalf("bad error response: %d %+v", recorder.Code, body)
		}
		if strings.Contains(recorder.Body.String(), "private-") {
			t.Fatal("response leaked secret")
		}
	}
	if strings.Contains(logs.String(), "private-") || !strings.Contains(logs.String(), "stage=alias_reserve upstream_status=200") {
		t.Fatalf("unsafe or incomplete logs: %s", logs.String())
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	backendFail(ctx, classifyUpstreamErr("操作失败", &hme.UpstreamError{Stage: hme.StageReserve, Kind: hme.UpstreamUnavailable, Status: 503, RetryAfter: "private-cookie"}))
	if recorder.Header().Get("Retry-After") != "" || strings.Contains(recorder.Body.String(), "private-") {
		t.Fatal("unsanitized Retry-After")
	}
}
