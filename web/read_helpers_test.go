package web

import (
	"testing"

	"github.com/kran/gcmv2/core"
)

// 引擎读面只有 GetNodes / CountNodes（列表与计数分开）。测试里两步合成。

func countAndRead(t *testing.T, e core.Engine, q core.NodeQuery, limit, offset int) ([]core.Node, int64, error) {
	t.Helper()
	total, err := e.CountNodes(t.Context(), q, core.CountExact)
	if err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}
	ptrs, err := e.GetNodes(t.Context(), q, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	return nodeValues(ptrs), total, nil
}

func getNodes(t *testing.T, e core.Engine, q core.NodeQuery, limit, offset int) ([]core.Node, error) {
	t.Helper()
	ptrs, err := e.GetNodes(t.Context(), q, limit, offset)
	if err != nil {
		return nil, err
	}
	return nodeValues(ptrs), nil
}
