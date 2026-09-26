// Package server - 统一响应格式与稳定错误码。
package server

import (
	"icloud-hme/internal/hme"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

// apiResp 是统一 API 响应。
type apiResp struct {
	Success        bool   `json:"success"`
	Code           string `json:"code,omitempty"`
	Message        string `json:"message,omitempty"`
	Data           any    `json:"data,omitempty"`
	Stage          string `json:"stage,omitempty"`
	UpstreamStatus int    `json:"upstream_status,omitempty"`
	RetryAfter     string `json:"retry_after,omitempty"`
}

// ok 返回统一成功响应。
func ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, apiResp{Success: true, Data: data})
}

// failCode 返回统一失败响应。
func failCode(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, apiResp{Success: false, Code: code, Message: message})
}

// backendFail 把 Backend 错误映射为统一失败响应。
func backendFail(c *gin.Context, err error) {
	be := asBackendError(err)
	retry := hme.NormalizeRetryAfter(be.RetryAfter)
	if retry != "" {
		c.Header("Retry-After", retry)
	}
	if be.Stage != "" {
		log.Printf("icloud_upstream_failed code=%s stage=%s upstream_status=%d retry_after=%q", be.Code, be.Stage, be.UpstreamStatus, retry)
	}
	c.AbortWithStatusJSON(be.Status, apiResp{Success: false, Code: be.Code, Message: be.Message, Stage: be.Stage, UpstreamStatus: be.UpstreamStatus, RetryAfter: retry})
}
