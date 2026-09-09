package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// BindStrictJSON decodes exactly one JSON value and rejects unknown fields.
func (c *CmsCtx) BindStrictJSON(dst any) error {
	return decodeStrictJSON(c.R.Body, dst)
}

func decodeStrictJSON(r io.Reader, dst any) error {
	decoder := json.NewDecoder(r)
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
