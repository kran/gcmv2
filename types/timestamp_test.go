package types

import "testing"

func TestTimestampKind(t *testing.T) {
	ts := New()
	if err := ts.Load([]byte(`
types:
  event:
    title: name
    fields:
      - { name: name, kind: text }
      - { name: start_at, kind: timestamp }
`)); err != nil {
		t.Fatal(err)
	}
	td, _ := ts.Type("event")
	startAt := td.Fields[1]
	// 合法时间戳（数字）通过
	if err := ts.ValidateValue("event", startAt, float64(1700000000)); err != nil {
		t.Fatalf("valid timestamp should pass: %v", err)
	}
	// 非数字拒绝
	if err := ts.ValidateValue("event", startAt, "2024-01-01"); err == nil {
		t.Fatal("string timestamp should be rejected")
	}
	// Class 是字段
	if k, _ := ts.Kind(KindTimestamp); k.Class() != ClassField {
		t.Fatalf("timestamp should be ClassField")
	}
}
