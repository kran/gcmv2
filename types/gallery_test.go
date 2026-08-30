package types

import "testing"

func TestGalleryKind(t *testing.T) {
	ts := New()
	if err := ts.Load([]byte(`
types:
  event:
    title: name
    fields:
      - { name: name, kind: text }
      - { name: gallery, kind: gallery }
`)); err != nil {
		t.Fatal(err)
	}
	td, _ := ts.Type("event")
	gallery := td.Fields[1]
	// 合法: 图片路径数组
	if err := ts.ValidateValue("event", gallery, []any{"/uploads/a.jpg", "/uploads/b.jpg"}); err != nil {
		t.Fatalf("valid gallery: %v", err)
	}
	// 非法: 混入非 string
	if err := ts.ValidateValue("event", gallery, []any{"/uploads/a.jpg", 42}); err == nil {
		t.Fatal("mixed should fail")
	}
	// 非法: 非数组
	if err := ts.ValidateValue("event", gallery, "/uploads/a.jpg"); err == nil {
		t.Fatal("single string should fail")
	}
}
