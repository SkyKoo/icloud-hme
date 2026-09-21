package server

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestBasePathValidation(t *testing.T) {
	for _, raw := range []string{"", "/", "/hme", "/tools/hme/"} {
		if _, err := New(nil, Config{AdminPassword: "strong-test-password", BasePath: raw}); err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
	}
	for _, raw := range []string{"hme", "//evil.example", "/a/../b", "/hme?x", "/hme#x", "/%2f", "/a//b", "/a\""} {
		if _, err := New(nil, Config{AdminPassword: "strong-test-password", BasePath: raw}); err == nil {
			t.Fatalf("应拒绝 %q", raw)
		}
	}
}

// TestMountedApplication 覆盖深链接、静态资源、API 隔离、登录与退出 Cookie。
func TestMountedApplication(t *testing.T) {
	oldFS := webuiFS
	webuiFS = fstest.MapFS{
		"index.html":        {Data: []byte(`<meta name="hme-base-path" content="/"><script src="./assets/app-abc.js"></script><link href="./assets/app-abc.css">`)},
		"assets/app-abc.js": {Data: []byte("export default 1")},
	}
	defer func() { webuiFS = oldFS }()
	s := newWithBackend(&fakeBackend{}, Config{AdminPassword: "strong-test-password", SecureCookie: true, BasePath: "/hme"})
	ts := httptest.NewTLSServer(s.Handler())
	defer ts.Close()
	client := ts.Client()
	client.Jar, _ = cookiejar.New(nil)
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	for _, tc := range []struct {
		path     string
		status   int
		contains string
	}{
		{"/hme", 308, ""},
		{"/hme?x=1", 308, ""},
		{"/hme/", 200, `name="hme-base-path" content="/hme/"`},
		{"/hme/accounts", 200, `src="/hme/assets/app-abc.js"`},
		{"/hme/index.html", 200, `href="/hme/assets/app-abc.css"`},
		{"/hme/assets/app-abc.js", 200, "export default 1"},
		{"/hme/assets/missing.js", 404, ""},
		{"/hme/api/missing", 404, `"success":false`},
		{"/hme/api/accounts", 401, `AUTH_REQUIRED`},
		{"/api/accounts", 404, ""},
		{"/assets/app-abc.js", 404, ""},
		{"/hme-other/", 404, ""},
		{"/", 404, ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", tc.path, nil))
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.contains) {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if tc.path == "/hme?x=1" && rec.Header().Get("Location") != "/hme/?x=1" {
				t.Fatal("重定向丢失查询参数")
			}
		})
	}
	resp, err := client.Post(ts.URL+"/hme/api/auth/login", "application/json", strings.NewReader(`{"password":"strong-test-password"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("登录失败: %d", resp.StatusCode)
	}
	var payload struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(resp.Cookies()) != 1 {
		t.Fatal("缺少会话 Cookie")
	}
	cookie := resp.Cookies()[0]
	if cookie.Path != "/hme/" || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("Cookie 属性错误")
	}
	authed, err := client.Get(ts.URL + "/hme/api/accounts")
	if err != nil {
		t.Fatal(err)
	}
	authed.Body.Close()
	if authed.StatusCode != 200 {
		t.Fatalf("子路径 Cookie 未发送: %d", authed.StatusCode)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/hme/api/auth/logout", nil)
	req.Header.Set("X-CSRF-Token", payload.Data.CSRF)
	out, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out.Body.Close()
	if out.StatusCode != 200 || len(out.Cookies()) != 1 || out.Cookies()[0].Path != "/hme/" || out.Cookies()[0].MaxAge != -1 {
		t.Fatal("退出未清除同路径 Cookie")
	}
	anon, err := client.Get(ts.URL + "/hme/api/accounts")
	if err != nil {
		t.Fatal(err)
	}
	anon.Body.Close()
	if anon.StatusCode != 401 {
		t.Fatal("退出后仍可访问账户")
	}
}
