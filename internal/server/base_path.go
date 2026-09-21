package server

import (
	"fmt"
	"regexp"
	"strings"
)

var basePathPattern = regexp.MustCompile(`^(/[A-Za-z0-9_-]+)+$`)

// normalizeBasePath 只接受本地路径段,避免配置被解释成 URL、查询或 HTML。
func normalizeBasePath(raw string) (string, error) {
	if raw == "" || raw == "/" {
		return "", nil
	}
	value := strings.TrimSuffix(raw, "/")
	if !basePathPattern.MatchString(value) {
		return "", fmt.Errorf("ICLOUD_HME_BASE_PATH 必须为 / 或 /hme 等路径,路径段仅支持字母、数字、下划线和连字符")
	}
	return value, nil
}
