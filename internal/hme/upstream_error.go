package hme

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// UpstreamStage 只允许固定阶段，不携带请求 URL、账号或邮箱。
type UpstreamStage uint8

const (
	StageValidate UpstreamStage = iota + 1
	StageGenerate
	StageReserve
	StageList
	StageDeactivate
	StageReactivate
	StageDelete
)

func (s UpstreamStage) String() string {
	switch s {
	case StageValidate:
		return "session_validate"
	case StageGenerate:
		return "alias_generate"
	case StageReserve:
		return "alias_reserve"
	case StageList:
		return "alias_list"
	case StageDeactivate:
		return "alias_deactivate"
	case StageReactivate:
		return "alias_reactivate"
	case StageDelete:
		return "alias_delete"
	default:
		return "unknown"
	}
}
func (s UpstreamStage) Label() string {
	switch s {
	case StageValidate:
		return "校验 iCloud 会话"
	case StageGenerate:
		return "生成别名"
	case StageReserve:
		return "保留别名"
	case StageList:
		return "读取别名"
	default:
		return "操作别名"
	}
}

type UpstreamKind uint8

const (
	UpstreamUnknown UpstreamKind = iota
	UpstreamRateLimited
	UpstreamSessionExpired
	UpstreamUnavailable
	UpstreamNetwork
	UpstreamTimeout
	UpstreamInvalidResponse
	UpstreamRejected
)

// UpstreamError 只保存可安全传递的元数据，不保存原始响应体、错误或凭据。
type UpstreamError struct {
	Stage      UpstreamStage
	Kind       UpstreamKind
	Status     int
	RetryAfter string
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("icloud upstream stage=%s kind=%d HTTP %d", e.Stage, e.Kind, e.Status)
}

// NormalizeRetryAfter 仅接受秒数或 HTTP 日期，不转发任意上游字符串。
func NormalizeRetryAfter(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 128 {
		return ""
	}
	if n, err := strconv.ParseUint(raw, 10, 31); err == nil {
		return strconv.FormatUint(n, 10)
	}
	if t, err := http.ParseTime(raw); err == nil {
		return t.UTC().Format(http.TimeFormat)
	}
	return ""
}

type transportError struct{ timeout bool }

func (e *transportError) Error() string { return "upstream transport failure" }
func newTransportError(err error) error {
	var timeout net.Error
	return &transportError{timeout: errors.As(err, &timeout) && timeout.Timeout()}
}
func wrapUpstream(stage UpstreamStage, err error) *UpstreamError {
	var existing *UpstreamError
	if errors.As(err, &existing) {
		return existing
	}
	result := &UpstreamError{Stage: stage}
	var status *HTTPStatusError
	var transport *transportError
	switch {
	case errors.As(err, &status):
		result.Status = status.StatusCode
		result.RetryAfter = NormalizeRetryAfter(status.RetryAfter)
		switch {
		case status.StatusCode == 429:
			result.Kind = UpstreamRateLimited
		case status.StatusCode == 401 || status.StatusCode == 403 || status.StatusCode == 421:
			result.Kind = UpstreamSessionExpired
		case status.StatusCode >= 500 && status.StatusCode <= 599:
			result.Kind = UpstreamUnavailable
		default:
			result.Kind = UpstreamRejected
		}
	case errors.As(err, &transport):
		result.Kind = UpstreamNetwork
		if transport.timeout {
			result.Kind = UpstreamTimeout
		}
	}
	return result
}
