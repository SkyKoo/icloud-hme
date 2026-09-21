// Package mail - iCloud Web 邮件客户端
//
// 使用 Cookie 认证通过 iCloud Web API 读取邮件，
// 无需 App Password。基于 mccgateway 服务。
package mail

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"github.com/google/uuid"
)

// WebClientBuildNumber 是与浏览器一致的 mccgateway 邮件接口构建号。
const WebClientBuildNumber = "2624Build13"

// WebClient 是 iCloud Web 邮件客户端。
type WebClient struct {
	cookies       map[string]string
	dsid          string
	clientID      string
	mccGatewayURL string
	mailboxIDs    map[string]string
	host          string // "icloud.com" 或 "icloud.com.cn"
	httpc         tls_client.HttpClient
}

// NewWebClient 创建一个 Web 邮件客户端。
func NewWebClient(cookies map[string]string, dsid, host string) *WebClient {
	jar := tls_client.NewCookieJar()
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(profiles.Chrome_146),
		tls_client.WithCookieJar(jar),
		tls_client.WithNotFollowRedirects(),
	}

	httpc, _ := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)

	if host == "" {
		host = "icloud.com"
	}

	// Cookie 中的 DSID 可能保留外层引号，不能将其作为账号标识的一部分。
	c := &WebClient{
		cookies:  cookies,
		dsid:     strings.Trim(strings.TrimSpace(dsid), `"`),
		clientID: uuid.New().String(),
		host:     host,
		httpc:    httpc,
	}

	// 设置 Cookie 到所有相关域名(确保跨域请求能传递 Cookie)
	if len(cookies) > 0 {
		suffix := "icloud.com"
		if host == "icloud.com.cn" {
			suffix = "icloud.com.cn"
		}
		domains := []string{
			"https://setup." + suffix,
			"https://www." + suffix,
			"https://p217-mccgateway." + suffix,
			"https://p217-maildomainws." + suffix,
		}
		for _, domain := range domains {
			u, _ := url.Parse(domain)
			httpCookies := make([]*http.Cookie, 0, len(cookies))
			for k, v := range cookies {
				httpCookies = append(httpCookies, &http.Cookie{
					Name:  k,
					Value: v,
					Path:  "/",
				})
			}
			jar.SetCookies(u, httpCookies)
		}
	}

	return c
}

// origin 返回当前账号对应的 Web Origin。
func (c *WebClient) origin() string {
	return "https://www." + c.host
}

// setCommonHeaders 设置与浏览器一致的通用请求头。
func (c *WebClient) setCommonHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.origin())
	req.Header.Set("Referer", c.origin()+"/")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-site")
}

// withParams 给 URL 追加 clientBuildNumber / clientId / dsid 查询参数。
func (c *WebClient) withParams(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	q.Set("clientBuildNumber", WebClientBuildNumber)
	q.Set("clientMasteringNumber", WebClientBuildNumber)
	q.Set("clientId", c.clientID)
	q.Set("dsid", c.dsid)
	u.RawQuery = q.Encode()
	return u.String()
}

// resolveMccGateway 从 validate 响应中获取 mccgateway URL。
func (c *WebClient) resolveMccGateway() error {
	if c.mccGatewayURL != "" {
		return nil
	}

	setupURL := "https://setup." + c.host + "/setup/ws/1/validate"
	req, err := http.NewRequest("POST", c.withParams(setupURL), nil)
	if err != nil {
		return err
	}
	c.setCommonHeaders(req)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("validate 失败: HTTP %d - %s", resp.StatusCode, truncate(string(body), 200))
	}

	var parsed struct {
		Webservices struct {
			Mccgateway struct {
				URL string `json:"url"`
			} `json:"mccgateway"`
		} `json:"webservices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("解析 validate 响应失败: %w", err)
	}

	mccURL := parsed.Webservices.Mccgateway.URL
	if mccURL == "" {
		return fmt.Errorf("未找到 mccgateway URL,响应: %s", truncate(string(body), 200))
	}
	if !strings.HasPrefix(mccURL, "https://") {
		mccURL = "https://" + mccURL
	}
	// 去掉端口号(如 :443)——tls-client 的 cookie jar 按不带端口的 host 存储 Cookie,
	// 带端口的 URL 会导致 Cookie 无法附加,返回 403。
	if u, err := url.Parse(mccURL); err == nil && u.Host != "" {
		u.Host = u.Hostname()
		mccURL = u.String()
	}
	c.mccGatewayURL = strings.TrimRight(mccURL, "/")
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
