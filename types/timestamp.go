package types

import "fmt"

// timestampKind 时间点。值 = 统一格式字符串（TimeFormat，例如 "2026-09-14T23:06:41Z"）。
//
// 为什么不是 Unix 秒数（v0.9 之前存数字）：
//   - 库里、JSON、筛选值同一个字符串 —— 前端不必猜单位（秒？毫秒？），也不必自己换算
//   - 定宽 ⇒ 字典序 = 时间序，SQL 侧可以直接文本比较/排序（ORDER BY、范围筛选）
//   - SQLite 的 datetime()/strftime()/julianday() 都认得它
//
// 只收**已经归一化**的值：带偏移或带毫秒的输入一旦入库，库里就有两种写法，"定宽"
// 这条前提就没了。需要归一化在调用方做（如 core.ParseTime / 前端 new Date().toISOString()）。
type timestampKind struct{}

// KindTimestamp kind 名（值 = TimeFormat 字符串）。
const KindTimestamp = "timestamp"

func (timestampKind) Name() string { return KindTimestamp }

func (timestampKind) Validate(_ FieldDef, v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("expects %s string, got %T", TimeFormat, v)
	}
	if s == "" {
		return fmt.Errorf("expects %s string, got empty string", TimeFormat)
	}
	parsed, err := ParseTime(s)
	if err != nil {
		return err
	}
	if canonical := FormatTime(parsed); canonical != s {
		return fmt.Errorf("expects canonical %s, got %q", TimeFormat, s)
	}
	return nil
}

// IsEmpty 空/非法（required 检查用）：与旧实现同语义 —— 非字符串或空串都算空。
func (timestampKind) IsEmpty(v any) bool {
	s, ok := v.(string)
	return !ok || s == ""
}

func (timestampKind) ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	return rejectRefAttrs(typeName, f)
}
func (timestampKind) Class() Class { return ClassField }
func (timestampKind) QueryOps() QueryOps {
	return QueryOps{Equal: true, Ordered: true, Sortable: true}
}
