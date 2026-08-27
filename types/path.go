package types

import (
	"errors"
	"fmt"
	"strings"
)

// 路径语言（统一核心抽象）: filter 表达式、expand 表达式、title 穿透声明
// 共用同一套路径语法与解析器 — 语法统一, 语义各验。
//
//	语法:   [<-] 段 ("." 段)*
//	段:     "$.字段"（fields JSON）| "字段"（列或引用, 由消费方按三层存储裁决）
//	"<-" 任意段（入边方向, 每段独立）; "$." 任意段（filter 首段 $.x = 本节点
//	JSON 字段; title/expand 在语义层各自拒绝不支持的形态）。
//
// 消费方:
//	filter  "authors.$.level = 1"  → 首段引用, 二段 JSON
//	expand  "a.b, <-c"             → 段全是引用字段（JSON 段被拒绝）
//	title   "person.$.name"        → 首段引用, 二段 JSON 或列（限两段）

// Seg 路径段。
type Seg struct {
	Field string // 字段名（剥掉前缀后的名字）
	JSON  bool   // "$." 前缀: 目标节点 fields JSON 字段
	In    bool   // "<-" 前缀: 入边（仅首段）
}

// ParsePath 解析路径表达式（语法层; 空段/非法前缀响亮报错）。
// 注意 "$." 是 JSON 前缀标记（非分隔符）— 逐段扫描, 不能先 Split(".")。
func ParsePath(raw string) ([]Seg, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("types: path: empty expression")
	}
	var out []Seg
	rest := raw
	for rest != "" {
		seg := Seg{}
		// "<-" 每段独立（多层入边: "<-a.<-b" = 每层都走入边; filter 首段语义不变）
		if strings.HasPrefix(rest, "<-") {
			seg.In = true
			rest = rest[2:]
		}
		if strings.HasPrefix(rest, "$.") {
			seg.JSON = true
			rest = rest[2:]
		}
		name, tail := rest, ""
		if idx := strings.Index(rest, "."); idx >= 0 {
			name, tail = rest[:idx], rest[idx+1:]
		}
		if name == "" {
			return nil, fmt.Errorf("types: path %q: empty segment", raw)
		}
		seg.Field = name
		out = append(out, seg)
		rest = tail
	}
	return out, nil
}

// IsNodeColumn 节点列全集（title 穿透第二段 / filter 无前缀段的三层归属之一）。
func IsNodeColumn(name string) bool { return nodeColumns[name] }
