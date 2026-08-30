package types

import "fmt"

// timestampKind 时间戳（Unix 秒 — 跨时区无歧义, 数值可比较, JSON 序列化自然）。
// 值语义: 数字（int64/float64）。模板/API 拿到时间戳自行格式化;
// filter 对时间戳字段按数值比较（> start_at 1_700_000_000）。
type timestampKind struct{}

// KindTimestamp kind 名; WidgetTimestamp 编辑控件（日期时间选择器 — 值绑定时间戳）。
const KindTimestamp = "timestamp"

func (timestampKind) Name() string { return KindTimestamp }
func (timestampKind) Validate(v any) error {
	if !isNumber(v) {
		return fmt.Errorf("expects timestamp (number), got %T", v)
	}
	return nil
}
func (timestampKind) IsEmpty(v any) bool { return !isNumber(v) }

func (timestampKind) ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	return rejectRefAttrs(typeName, f)
}
func (timestampKind) Class() Class { return ClassField }
