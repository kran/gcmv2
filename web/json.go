package web

import (
	"encoding/json"
	"errors"
	"io"
)

// 请求体大小不由内核限制 —— 部署侧（nginx `client_max_body_size`）负责。
// 上传路由自己那道上限见 admin.go（按文件类型收紧）。
//
// 这里只保证"解码语义"：恰好一个 JSON 值、拒绝未知字段。

// BindStrictJSON decodes exactly one JSON value and rejects unknown fields.
func (c *CmsCtx) BindStrictJSON(dst any) error {
	return decodeStrictJSON(c.R.Body, dst)
}

// decodeStrictJSON 只解一个 JSON 值：拒绝未知字段、拒绝多余值。
func decodeStrictJSON(r io.Reader, dst any) error {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return BadRequest("%s", err.Error())
	}
	var extra any
	err := decoder.Decode(&extra)
	if !errors.Is(err, io.EOF) {
		if err == nil {
			return BadRequest("request body must contain one JSON value")
		}
		return BadRequest("%s", err.Error())
	}
	return nil
}
