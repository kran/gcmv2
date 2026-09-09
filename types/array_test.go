package types

import "testing"

func TestArrayUploadImage(t *testing.T) {
	ts := New()
	if err := ts.Load([]byte(`
types:
  event:
    fields:
      - { name: name, kind: text }
      - name: gallery
        kind: array
        item: { kind: upload-image }
`)); err != nil {
		t.Fatalf("array<upload-image> should load: %v", err)
	}
	td, _ := ts.Type("event")
	gallery := td.Fields[1]
	// 合法: 图片路径数组
	if err := ts.ValidateValue("event", gallery, []any{"/uploads/a.jpg", "/uploads/b.jpg"}); err != nil {
		t.Fatalf("valid gallery should pass: %v", err)
	}
	// 非法: 数组里混入非 string
	if err := ts.ValidateValue("event", gallery, []any{"/uploads/a.jpg", 42}); err == nil {
		t.Fatal("mixed array should fail")
	}
}
