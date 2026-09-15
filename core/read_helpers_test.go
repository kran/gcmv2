package core

import (
	"testing"

	gquery "github.com/kran/gcmv2/query"
)

// 读面本身只有 GetNode / GetNodes / GetNodesByIDs / CountNodes 和"读完之后的独立一步"
// Expand / ExpandNode。下面是测试用的两步合成，避免每个用例重复写"先读再展开"。

func expandAuto(t *testing.T, s *Service, id int64) (*Node, error) {
	t.Helper()
	n, err := s.GetNode(t.Context(), id)
	if err != nil {
		return nil, err
	}
	return s.ExpandNode(t.Context(), n)
}

func expandAutoMany(t *testing.T, s *Service, ids []int64) ([]*Node, error) {
	t.Helper()
	nodes, err := s.GetNodesByIDs(t.Context(), ids)
	if err != nil {
		return nil, err
	}
	return s.Expand(t.Context(), nodes)
}

func expandByIDs(t *testing.T, s *Service, ids []int64, paths ...gquery.ExpandPath) ([]*Node, error) {
	t.Helper()
	nodes, err := s.GetNodesByIDs(t.Context(), ids)
	if err != nil {
		return nil, err
	}
	return s.Expand(t.Context(), nodes, paths...)
}

// nodeValues 指针切片 → 值切片（测试里断言按值走）。
func nodeValues(nodes []*Node) []Node {
	out := make([]Node, len(nodes))
	for i, n := range nodes {
		if n != nil {
			out[i] = *n
		}
	}
	return out
}

// countAndRead 是 CountNodes + GetNodes 的合成（旧 QueryPage 的测试形态）。
func countAndRead(t *testing.T, s *Service, q NodeQuery, limit, offset int) ([]*Node, int64, error) {
	t.Helper()
	return countAndReadLimit(t, s, q, limit, offset, CountExact)
}

// countAndReadLimit 同上, 但统计上限可调（基准里模拟大表截断计数）。
func countAndReadLimit(t *testing.T, s *Service, q NodeQuery, limit, offset, countLimit int) ([]*Node, int64, error) {
	t.Helper()
	total, err := s.CountNodes(t.Context(), q, countLimit)
	if err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}
	nodes, err := s.GetNodes(t.Context(), q, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	return nodes, total, nil
}
