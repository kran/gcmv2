package web

import (
	"fmt"
	"strings"

	"github.com/kran/gcmv2/core"
)

// parseSort 解析公开 API 的简洁排序语法：field,-field。
// 字段是否合法由 core 根据列和类型定义再次校验。
func parseSort(raw string) ([]core.SortField, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 4 {
		return nil, fmt.Errorf("sort: at most 4 fields")
	}
	result := make([]core.SortField, 0, len(parts))
	for _, part := range parts {
		field := strings.TrimSpace(part)
		if field == "" {
			return nil, fmt.Errorf("sort: empty field")
		}
		desc := strings.HasPrefix(field, "-")
		field = strings.TrimPrefix(field, "-")
		if field == "" {
			return nil, fmt.Errorf("sort: empty field")
		}
		result = append(result, core.SortField{Field: field, Desc: desc})
	}
	return result, nil
}
