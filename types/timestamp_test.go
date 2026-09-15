package types

import "testing"

func TestTimestampKind(t *testing.T) {
	ts := New()
	if err := ts.Load([]byte(`
types:
  event:
    fields:
      - { name: name, kind: text }
      - { name: start_at, kind: timestamp }
`)); err != nil {
		t.Fatal(err)
	}
	td, _ := ts.Type("event")
	startAt := td.Fields[1]

	// 统一格式通过（TimeFormat：UTC + 秒精度 + Z）。
	if err := ts.ValidateValue("event", startAt, "2026-09-14T23:06:41Z"); err != nil {
		t.Fatalf("canonical timestamp should pass: %v", err)
	}
	// 数字是旧表示（Unix 秒）：拒绝，不给"两种写法并存"留口子。
	for _, bad := range []any{
		1700000000, float64(1700000000), "1700000000",
		"2026-09-15 07:06:57",       // 裸墙钟（无时区）
		"2026-09-15T07:06:57+08:00", // 带偏移：请调用方先归一化
		"2026-09-14T23:06:41.615Z",  // 带毫秒：破坏定宽
		"2026-09-15",                // 纯日期
		"",                          // 空串（清空请用 null，走 merge 删除）
		nil,                         // 非字符串
		"不是时间",                      // 垃圾
	} {
		if err := ts.ValidateValue("event", startAt, bad); err == nil {
			t.Fatalf("%#v 应当被拒（timestamp 只收统一格式字符串）", bad)
		}
	}
	// required 检查用的空判断：非字符串/空串算空，非法字符串不算空（由 Validate 报错）。
	if !ts.isEmpty(KindTimestamp, nil) || !ts.isEmpty(KindTimestamp, "") {
		t.Fatal("nil 与空串应当算空")
	}
	if ts.isEmpty(KindTimestamp, "不是时间") {
		t.Fatal("非法字符串不该算空（Validate 会报错）")
	}
	// Class 是字段
	if k, _ := ts.Kind(KindTimestamp); k.Class() != ClassField {
		t.Fatalf("timestamp should be ClassField")
	}
}
