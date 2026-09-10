package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// 请求体上限（两道）：
//
//	maxBodyBytes  任何请求体的硬上限（CmsCtxMaker 里兜住 — 上传按 8MB 设计）
//	maxJSONBytes  JSON 解码上限（BindStrictJSON 里再收紧 — 表单类请求不该有大 body）
//
// 内层只会更小：MaxBytesReader 只能压低上限，无法放宽。
const (
	maxBodyBytes = 8 << 20
	maxJSONBytes = 1 << 20
)

// BindStrictJSON decodes exactly one JSON value, rejects unknown fields, and caps
// the JSON body so one request cannot allocate unbounded memory. The returned
// error is already structured: 413 for an oversized body, 400 otherwise.
func (c *CmsCtx) BindStrictJSON(dst any) error {
	if err := decodeStrictJSON(c.W, c.R.Body, dst, maxJSONBytes); err != nil {
		return bodyError(err)
	}
	return nil
}

// decodeStrictJSON 只解一个 JSON 值：拒绝未知字段、拒绝多余值，并把 body 限制在 max 字节内。
// w 用于在超限时标记连接（可为 nil）。
func decodeStrictJSON(w http.ResponseWriter, r io.ReadCloser, dst any, max int64) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r, max))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	err := decoder.Decode(&extra)
	if !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("request body must contain one JSON value")
		}
		return err
	}
	return nil
}

// bodyError 把 body 解码错误映射成结构化错误：超限是 413，其余是 400。
func bodyError(err error) *Error {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return Errorf(http.StatusRequestEntityTooLarge, CodeInvalidRequest,
			"request body too large (max %dKB)", maxJSONBytes>>10)
	}
	return BadRequest("%s", err.Error())
}
