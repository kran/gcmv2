package web

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/kran/gcmv2/core"
)

// Code 是稳定的机器可读错误标识。客户端按 Code 分支, 不解析 Message:
// Message 面向人类, 可以本地化或改写; Code 是契约, 变更需要走版本说明。
type Code string

const (
	// CodeInvalidRequest 请求格式/参数非法（JSON 解析、缺参数、类型不存在）。
	CodeInvalidRequest Code = "invalid_request"
	// CodeInvalidValue 字段值不符合 Schema（表单可用 Details 回显）。
	CodeInvalidValue Code = "invalid_value"
	// CodeInvalidQuery 查询字段、操作符或值未通过 Schema 校验。
	CodeInvalidQuery Code = "invalid_query"
	// CodeQueryTooComplex 查询超出深度/节点/集合/分页预算。
	CodeQueryTooComplex Code = "query_too_complex"
	// CodeUnauthorized 未认证, 或凭据无效/已过期。
	CodeUnauthorized Code = "unauthorized"
	// CodeForbidden 已认证但无权执行该操作。
	CodeForbidden Code = "forbidden"
	// CodeNotFound 目标不存在, 或对当前调用者不可见。
	CodeNotFound Code = "not_found"
	// CodeConflict 版本冲突、唯一冲突、归档状态冲突。
	CodeConflict Code = "conflict"
	// CodeDeleteRestricted 永久删除被 incoming 引用阻止。
	CodeDeleteRestricted Code = "delete_restricted"
	// CodeUploadInvalid 上传缺文件、类型/大小/内容不合法。
	CodeUploadInvalid Code = "upload_invalid"
	// CodeInternal 未预期的服务端错误（细节只进日志）。
	CodeInternal Code = "internal"
	// CodeUnavailable 依赖不可用（数据库、引擎未就绪）。
	CodeUnavailable Code = "unavailable"
)

// Error 是 HTTP 边界上的结构化错误: HTTP Status + 稳定 Code + 人类可读 Message。
// 站点和 Hook 可以返回 *Error 精确表达语义; 返回普通 error 时框架按调用位置
// 决定默认语义（Hook 拒绝 → 403, 其他 → 500）。
//
// 响应体形态（与 cho BaseContext.Error 兼容并扩展）:
//
//	{"error":"人类可读信息","code":"invalid_value","details":{"title":"必填"}}
type Error struct {
	Status  int
	Code    Code
	Message string
	// Details 可选字段级错误（表单回显用; 键是字段名, 值是面向用户的信息）。
	Details map[string]string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("web: %s (%d): %s", e.Code, e.Status, e.Message)
}

// WithDetails 返回带字段级错误的副本。
func (e *Error) WithDetails(details map[string]string) *Error {
	clone := *e
	clone.Details = details
	return &clone
}

// Errorf 构造任意 Status/Code 的结构化错误。
func Errorf(status int, code Code, format string, args ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

// BadRequest 请求格式或参数不对（400）。
func BadRequest(format string, args ...any) *Error {
	return Errorf(http.StatusBadRequest, CodeInvalidRequest, format, args...)
}

// InvalidValue 字段值不符合 Schema（422）。
func InvalidValue(format string, args ...any) *Error {
	return Errorf(http.StatusUnprocessableEntity, CodeInvalidValue, format, args...)
}

// InvalidFields 字段级校验失败（422, 带 Details）。
func InvalidFields(details map[string]string) *Error {
	return &Error{
		Status: http.StatusUnprocessableEntity, Code: CodeInvalidValue,
		Message: "invalid fields", Details: details,
	}
}

// Unauthorized 未认证或凭据失效（401）。
func Unauthorized(format string, args ...any) *Error {
	return Errorf(http.StatusUnauthorized, CodeUnauthorized, format, args...)
}

// Forbidden 已认证但无权执行（403）。
func Forbidden(format string, args ...any) *Error {
	return Errorf(http.StatusForbidden, CodeForbidden, format, args...)
}

// NotFound 目标不存在（404）。
func NotFound(format string, args ...any) *Error {
	return Errorf(http.StatusNotFound, CodeNotFound, format, args...)
}

// Conflict 状态冲突（409）。
func Conflict(format string, args ...any) *Error {
	return Errorf(http.StatusConflict, CodeConflict, format, args...)
}

// Unavailable 依赖不可用（503）。
func Unavailable(format string, args ...any) *Error {
	return Errorf(http.StatusServiceUnavailable, CodeUnavailable, format, args...)
}

// Internal 服务端错误（500）— 只用于无法进一步分类的情况。
func Internal(format string, args ...any) *Error {
	return Errorf(http.StatusInternalServerError, CodeInternal, format, args...)
}

// codeForStatus 兜底映射: 直接用 (status, message) 写响应时也必须带 Code。
func codeForStatus(status int) Code {
	switch status {
	case http.StatusBadRequest:
		return CodeInvalidRequest
	case http.StatusUnauthorized:
		return CodeUnauthorized
	case http.StatusForbidden:
		return CodeForbidden
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusConflict:
		return CodeConflict
	case http.StatusRequestEntityTooLarge:
		return CodeUploadInvalid
	case http.StatusUnprocessableEntity:
		return CodeInvalidValue
	case http.StatusServiceUnavailable:
		return CodeUnavailable
	}
	if status >= 500 {
		return CodeInternal
	}
	return CodeInvalidRequest
}

// CoreError 把 core/types 的哨兵错误映射到 HTTP 契约; 未知错误返回 nil。
func CoreError(err error) *Error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, core.ErrNotFound), errors.Is(err, core.ErrEdgeNotFound):
		return NotFound("not found")
	case errors.Is(err, core.ErrRevisionConflict):
		return Conflict("revision conflict: reload and retry")
	case errors.Is(err, core.ErrNodeArchived):
		return Conflict("node is archived")
	case errors.Is(err, core.ErrDeleteRestricted):
		return Errorf(http.StatusConflict, CodeDeleteRestricted, "%s", err.Error())
	case errors.Is(err, core.ErrRequiredReference), errors.Is(err, core.ErrRelationCardinality):
		return Conflict("%s", err.Error())
	case errors.Is(err, core.ErrInvalidFields):
		return InvalidValue("%s", err.Error())
	case errors.Is(err, core.ErrQueryTooComplex):
		return Errorf(http.StatusUnprocessableEntity, CodeQueryTooComplex, "%s", err.Error())
	case errors.Is(err, core.ErrInvalidQuery), errors.Is(err, core.ErrInvalidField),
		errors.Is(err, core.ErrInvalidOperator), errors.Is(err, core.ErrInvalidValue):
		return Errorf(http.StatusUnprocessableEntity, CodeInvalidQuery, "%s", err.Error())
	default:
		return nil
	}
}

// payload 响应体（保持 "error" 字段兼容既有客户端, 新增 code/details）。
func (e *Error) payload() map[string]any {
	body := map[string]any{"error": e.Message, "code": string(e.Code)}
	if len(e.Details) > 0 {
		body["details"] = e.Details
	}
	return body
}

// write 输出结构化错误并返回写错误（cho handler 忽略返回值）。
func (e *Error) write(c *CmsCtx) error {
	return c.Json(e.Status, e.payload())
}

// Error 覆盖 cho 的同名方法: 保持 (status, message) 调用形态, 但响应体固定带上
// 由 status 推导的稳定 Code, 避免出现“没有 Code”的响应。需要更精确的语义时
// 使用 Fail/Reject 或显式构造 *Error。
func (c *CmsCtx) Error(status int, message string) error {
	return (&Error{Status: status, Code: codeForStatus(status), Message: message}).write(c)
}

// Fail API 层统一错误出口:
//
//   - *Error: 按声明输出（保留 Status/Code/Details）。
//   - core/types 已知错误: 映射到 404/409/422 等语义。
//   - 其他 error: 记日志并输出通用 500 — 内部细节不返回给客户端。
//
// error 为 nil 时 panic（调用方 bug, 不是运行时错误）。
func (c *CmsCtx) Fail(err error) {
	if err == nil {
		panic("web: Fail(nil)")
	}
	var structured *Error
	if errors.As(err, &structured) {
		_ = structured.write(c)
		return
	}
	if mapped := CoreError(err); mapped != nil {
		_ = mapped.write(c)
		return
	}
	c.internalError(err)
}

// Reject 权限/业务 Hook 的拒绝出口:
//
//   - *Error: 按声明输出（Hook 可返回 401/403/409/422）。
//   - core/types 已知错误: 映射后输出。
//   - 其他 error: 视为站点自定义拒绝, 输出 403 + 原始信息（站点文案随站点变,
//     客户端应改用 Code 判断）。
func (c *CmsCtx) Reject(err error) {
	if err == nil {
		panic("web: Reject(nil)")
	}
	var structured *Error
	if errors.As(err, &structured) {
		_ = structured.write(c)
		return
	}
	if mapped := CoreError(err); mapped != nil {
		_ = mapped.write(c)
		return
	}
	_ = (&Error{Status: http.StatusForbidden, Code: CodeForbidden, Message: err.Error()}).write(c)
}

// internalError 未预期错误: 细节进日志, 响应只给通用 500 + Code。
func (c *CmsCtx) internalError(err error) {
	slog.Error("web internal error",
		"method", c.R.Method, "path", c.R.URL.Path, "err", err)
	_ = Internal("internal error").write(c)
}
