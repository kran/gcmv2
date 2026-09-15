package core

import (
	"strings"
	"testing"
	"time"

	"github.com/kran/gcmv2/types"
)

// 统一时间格式：UTC、秒精度、…Z。带偏移的输入一律归一化。
func TestTimeCanonicalForm(t *testing.T) {
	in := time.Date(2026, 9, 14, 23, 6, 41, 615238000, time.UTC).In(time.FixedZone("CST", 8*3600))
	if got := TimeOf(in).String(); got != "2026-09-14T23:06:41Z" {
		t.Fatalf("TimeOf = %q, want 2026-09-14T23:06:41Z", got)
	}
	v, err := TimeOf(in).Value()
	if err != nil {
		t.Fatal(err)
	}
	if v != "2026-09-14T23:06:41Z" {
		t.Fatalf("Value = %#v", v)
	}
	// 零值：Value 出 nil（NOT NULL 列写 nil 会报错 = fail-loud，不静默塞一个假时间）。
	if v, err := (Time{}).Value(); err != nil || v != nil {
		t.Fatalf("zero Value = %#v, %v", v, err)
	}
	// 定宽 ⇒ 字典序 = 时间序（这正是选秒精度的原因：小数位不固定会排错）。
	early, late := TimeOf(in.Add(-time.Second)).String(), TimeOf(in).String()
	if !(early < late) {
		t.Fatalf("字典序坏了: %q !< %q", early, late)
	}
}

// Scan 只认统一格式（外加驱动可能给的 time.Time / nil / []byte）。
// 历史写法必须报错：内核不做兼容，老数据由 tools/legacy-time 处理。
func TestTimeScanStrict(t *testing.T) {
	var got Time
	accept := []struct{ raw, want string }{
		{"2026-09-14T23:06:41Z", "2026-09-14T23:06:41Z"},
	}
	for _, tc := range accept {
		if err := got.Scan(tc.raw); err != nil {
			t.Fatalf("Scan(%q): %v", tc.raw, err)
		}
		if got.String() != tc.want {
			t.Fatalf("Scan(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
	if err := got.Scan(time.Date(2026, 9, 14, 23, 6, 41, 0, time.UTC)); err != nil || got.String() != "2026-09-14T23:06:41Z" {
		t.Fatalf("Scan(time.Time) = %q, %v", got, err)
	}
	if err := got.Scan(nil); err != nil || !got.IsZero() {
		t.Fatalf("Scan(nil) = %q, %v", got, err)
	}
	// 统一格式以外的写法一律 fail-loud —— 包括曾经用过的那些。
	for _, raw := range []string{
		"2026-09-15 07:06:57.993279 +0800 CST m=+0.020570626", // 驱动默认
		"2026-09-15 07:06:57.993279+08:00",                    // _time_format=sqlite
		"2026-09-15 07:06:57",                                 // 无时区
		"2026-08-30",                                          // 纯日期
		"1789427201",                                          // epoch
		"昨天下午",
	} {
		if err := got.Scan(raw); err == nil {
			t.Fatalf("Scan(%q) 应当报错（内核不认非统一格式）", raw)
		}
	}
	// 驱动只对 BLOB 列给 []byte，时间列不会是 BLOB：不认，别静默当字符串收下。
	if err := got.Scan([]byte("2026-09-14T23:06:41Z")); err == nil {
		t.Fatal("Scan([]byte) 应当报错")
	}
}

// 客户端输入：统一格式或带偏移的 RFC3339 都收（归一化），裸本地时间与历史写法拒收。
func TestTimeParseInput(t *testing.T) {
	if parsed, err := ParseTime("2026-09-15T07:06:41+08:00"); err != nil || parsed.String() != "2026-09-14T23:06:41Z" {
		t.Fatalf("带偏移输入 = %q, %v", parsed, err)
	}
	if parsed, err := ParseTime("2026-09-15T07:06:41.615238+08:00"); err != nil || parsed.String() != "2026-09-14T23:06:41Z" {
		t.Fatalf("带小数输入 = %q, %v", parsed, err)
	}
	for _, raw := range []string{"2026-09-15 07:06:57", "2026-09-15 07:06:57 +0800 CST m=+0.02", "1789427201", "昨天"} {
		if _, err := ParseTime(raw); err == nil {
			t.Fatalf("ParseTime(%q) 应当报错", raw)
		}
	}
}

func TestTimeJSON(t *testing.T) {
	in := time.Date(2026, 9, 14, 23, 6, 41, 0, time.UTC)
	data, err := TimeOf(in).MarshalJSON()
	if err != nil || string(data) != `"2026-09-14T23:06:41Z"` {
		t.Fatalf("MarshalJSON = %s, %v", data, err)
	}
	if data, err := (Time{}).MarshalJSON(); err != nil || string(data) != "null" {
		t.Fatalf("zero MarshalJSON = %s, %v", data, err)
	}
	// 带偏移的输入归一化；垃圾输入报错。
	var got Time
	if err := got.UnmarshalJSON([]byte(`"2026-09-15T07:06:41+08:00"`)); err != nil || got.String() != "2026-09-14T23:06:41Z" {
		t.Fatalf("UnmarshalJSON = %q, %v", got, err)
	}
	if err := got.UnmarshalJSON([]byte(`"昨天"`)); err == nil {
		t.Fatal("garbage JSON must fail loud")
	}
}

// 内核写出来的时间**就是**统一格式（不再有启动闸门全表扫描，这条由测试保证）：
// 时间列走 core.Time，配置表也走 types.FormatTime。
func TestWritesAreCanonical(t *testing.T) {
	db := testDB(t)
	s := New(db, newTypes(t, testTypesYAML))
	id, err := s.CreateNode(t.Context(), &Node{Type: "article", Display: "时间"})
	if err != nil {
		t.Fatal(err)
	}
	// 真正的收益：SQLite 能解析它（改之前 datetime() 返回 NULL，按时间排序/筛选全废）。
	stored, err := db.Add(`SELECT quote(created_at) FROM nodes WHERE id = #{1}`, id).FetchOne[string]()
	if err != nil || stored == nil {
		t.Fatal(err)
	}
	if got := strings.Trim(*stored, "'"); got != types.FormatTime(time.Now()) {
		t.Fatalf("nodes.created_at = %q，不是统一格式", got)
	}
	if got, err := db.Add(`SELECT COALESCE(datetime(created_at), 'NULL') FROM nodes WHERE id = #{1}`, id).FetchOne[string](); err != nil || *got == "NULL" {
		t.Fatalf("datetime(created_at) 解析不了（%v, %v）—— 时间格式没统一成功", got, err)
	}
}

// settings.updated_at 也要统一格式：曾经用 SQLite 的 datetime('now')（"2026-09-15 07:06:57"，
// 空格 + 无 Z）。
func TestSettingTimeIsCanonical(t *testing.T) {
	db := testDB(t)
	s := New(db, newTypes(t, testTypesYAML))
	if err := s.SetSetting(t.Context(), "site_name", "basic", "string", "商会"); err != nil {
		t.Fatal(err)
	}
	stored, err := db.Add(`SELECT quote(updated_at) FROM settings WHERE "key" = 'site_name'`).FetchOne[string]()
	if err != nil || stored == nil {
		t.Fatal(err)
	}
	if got := strings.Trim(*stored, "'"); got != types.FormatTime(time.Now()) {
		t.Fatalf("settings.updated_at = %q，不是统一格式", got)
	}
}
