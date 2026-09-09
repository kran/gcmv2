package web

import (
	"fmt"
	"strings"

	gquery "github.com/kran/gcmv2/query"
)

// parseSort parses field,-field. Dynamic fields use the explicit $field form;
// core validates every path against the query Type.
func parseSort(raw string) ([]gquery.SortField, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 4 {
		return nil, fmt.Errorf("sort: at most 4 fields")
	}
	result := make([]gquery.SortField, 0, len(parts))
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
		path := gquery.System(field)
		if strings.HasPrefix(field, "$") {
			path = gquery.Field(strings.TrimPrefix(field, "$"))
		}
		result = append(result, gquery.SortField{Path: path, Desc: desc})
	}
	return result, nil
}
