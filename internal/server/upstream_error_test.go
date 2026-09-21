package server

import (
	"fmt"
	"net/http"
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
