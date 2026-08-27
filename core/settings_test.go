package core

import (
	"testing"
)

// settings（cmx piece 语义 + group）: 透传/分组/批量/解 JSON。
func TestSettings(t *testing.T) {
	s := newFilterSvc(t)
	// 写入（自由 JSON 透传: 标量/复合都行）
	if err := s.SetSetting("footer", "site", "richtext", "<p>版权</p>"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting("nav_links", "site", "array", []any{"首页", "关于"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting("seo", "site", "object", map[string]any{"og": "x"}); err != nil {
		t.Fatal(err)
	}
	// 批量取
	got, err := s.GetSettings([]string{"footer", "nav_links", "missing"})
	if err != nil || len(got) != 2 {
		t.Fatalf("getmany: %d %v", len(got), err)
	}
	// GetSettingValue 解进结构
	var arr []string
	if ok, err := s.GetSettingValue("nav_links", &arr); err != nil || !ok || len(arr) != 2 {
		t.Fatalf("getvalue: %v %v", arr, err)
	}
	// 分组列表
	list, err := s.ListSettings("site")
	if err != nil || len(list) != 3 {
		t.Fatalf("list by group: %d %v", len(list), err)
	}
	// 删除 + 不存在报错
	if err := s.DeleteSetting("seo"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSetting("seo"); err == nil {
		t.Fatal("delete missing must fail")
	}
	// key 校验
	if err := s.SetSetting("Bad Key", "g", "string", "x"); err == nil {
		t.Fatal("bad key must fail")
	}
}
