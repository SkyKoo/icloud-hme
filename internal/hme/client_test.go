package hme

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestValidationURLs(t *testing.T) {
	tests := []struct {
		name string
		host string
		want []string
	}{
		{
			name: "全球账号只用全球端点",
			host: "icloud.com",
			want: []string{"https://setup.icloud.com/setup/ws/1/validate"},
		},
		{
			name: "国区账号回退全球端点",
			host: "icloud.com.cn",
			want: []string{
				"https://setup.icloud.com.cn/setup/ws/1/validate",
				"https://setup.icloud.com/setup/ws/1/validate",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{Host: tt.host}
			if got := client.validationURLs(); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("validationURLs() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestRequestOrigin(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://setup.icloud.com/setup/ws/1/validate", "https://www.icloud.com"},
		{"https://setup.icloud.com.cn/setup/ws/1/validate", "https://www.icloud.com.cn"},
		{"https://p123-maildomainws.icloud.com.cn/v2/hme/list", "https://www.icloud.com.cn"},
		{"https://p123-maildomainws.icloud.com/v2/hme/list", "https://www.icloud.com"},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := requestOrigin(tt.url); got != tt.want {
				t.Fatalf("requestOrigin(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

// iCloud uses 421 for an expired web session, not a transient gateway failure.
func TestExpiredSessionDoesNotRetry(t *testing.T) {
	for _, status := range []int{401, 403, 421} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"success":false,"error":1}`))
			}))
			defer upstream.Close()
			client, err := NewClient(nil, "icloud.com", "", false)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.request("POST", upstream.URL+"/validate", nil, 0, MaxRetries)
			if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", status)) {
				t.Fatalf("expected HTTP %d error, got %v", status, err)
			}
			if calls.Load() != 1 {
				t.Fatalf("expired session was retried: %d calls", calls.Load())
			}
		})
	}
}
