package core

import (
	"strings"
	"testing"
	"time"
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

// Scan 除统一格式外还要认历史写法：驱动默认的 time.Time.String()、种子 SQL 的无时区字符串。
func TestTimeScanLegacy(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"2026-09-14T23:06:41Z", "2026-09-14T23:06:41Z"},
		{"2026-09-15T07:06:41.615238+08:00", "2026-09-14T23:06:41Z"},
		// 驱动默认格式：带时区缩写与单调时钟后缀
		{"2026-09-15 07:06:57.993279 +0800 CST m=+0.020570626", "2026-09-14T23:06:57Z"},
		// 驱动的 _time_format=sqlite
		{"2026-09-15 07:06:57.993279+08:00", "2026-09-14T23:06:57Z"},
		// 无时区 → 按 UTC（Scan 必须与服务器时区无关）
		{"2026-09-15 07:06:57", "2026-09-15T07:06:57Z"},
		{"2026-08-30", "2026-08-30T00:00:00Z"},
	}
	for _, tc := range cases {
		var got Time
		if err := got.Scan(tc.raw); err != nil {
			t.Fatalf("Scan(%q): %v", tc.raw, err)
		}
		if got.String() != tc.want {
			t.Fatalf("Scan(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
	var got Time
	if err := got.Scan([]byte("2026-09-14T23:06:41Z")); err != nil || got.String() != "2026-09-14T23:06:41Z" {
		t.Fatalf("Scan([]byte) = %q, %v", got, err)
	}
	if err := got.Scan(time.Date(2026, 9, 14, 23, 6, 41, 0, time.UTC)); err != nil || got.String() != "2026-09-14T23:06:41Z" {
		t.Fatalf("Scan(time.Time) = %q, %v", got, err)
	}
	if err := got.Scan(int64(1789427201)); err != nil || got.Unix() != 1789427201 {
		t.Fatalf("Scan(int64) = %q, %v", got, err)
	}
	if err := got.Scan(nil); err != nil || !got.IsZero() {
		t.Fatalf("Scan(nil) = %q, %v", got, err)
	}
	// 认不出来必须报错，不能静默变成零值。
	if err := got.Scan("昨天下午"); err == nil {
		t.Fatal("garbage must fail loud")
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

// 端到端：引擎写的时间必须是统一格式、SQLite 的日期函数能用；
// 手写 SQL 塞进历史脏值时闸门必须抓住，迁移把它洗干净。
func TestTimeGateAndMigration(t *testing.T) {
	db := testDB(t)
	s := New(db, newTypes(t, testTypesYAML))
	id, err := s.CreateNode(t.Context(), &Node{Type: "article", Display: "时间"})
	if err != nil {
		t.Fatal(err)
	}
	raw := func(expr string) string {
		var v string
		if err := db.Pool().QueryRow(`SELECT `+expr+` FROM nodes WHERE id = ?`, id).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if got := strings.Trim(raw("quote(created_at)"), "'"); got != TimeOf(time.Now()).Format(TimeFormat) && got != raw("strftime('%Y-%m-%dT%H:%M:%SZ', created_at)") {
		t.Fatalf("引擎写入的不是统一格式: %q", got)
	}
	// 真正的收益：SQLite 能解析它（改之前 datetime() 返回 NULL，按时间的排序/筛选全废）。
	if got := raw("COALESCE(datetime(created_at), 'NULL')"); got == "NULL" {
		t.Fatal("datetime(created_at) 解析不了 —— 时间格式没统一成功")
	}
	if err := s.checkTimeFormats(t.Context()); err != nil {
		t.Fatalf("引擎写入应当通过闸门: %v", err)
	}

	// 手写 SQL 绕过类型系统塞进驱动默认格式（编译器看不见这种写入）。
	if _, err := db.Add(`UPDATE nodes SET created_at = #{1} WHERE id = #{2}`,
		"2026-09-15 07:06:57.993279 +0800 CST m=+0.020570626", id).Exec(); err != nil {
		t.Fatal(err)
	}
	if err := s.checkTimeFormats(t.Context()); err == nil {
		t.Fatal("闸门必须抓住非统一格式的时间")
	}
	// 连 SQLite 解析不了的脏值也要抓住（COALESCE 那处防的就是这个漏网）。
	if _, err := db.Add(`UPDATE nodes SET updated_at = '不是时间' WHERE id = #{1}`, id).Exec(); err != nil {
		t.Fatal(err)
	}
	if err := s.checkTimeFormats(t.Context()); err == nil {
		t.Fatal("闸门必须抓住解析不了的脏值")
	}

	if err := s.normalizeLegacyTimes(t.Context()); err == nil {
		t.Fatal("遇到解析不了的值，迁移应当报错而不是跳过")
	}
	// 修掉不可解析的那行，再迁移：闸门必须通过。
	if _, err := db.Add(`UPDATE nodes SET updated_at = '2026-08-30 09:38:30' WHERE id = #{1}`, id).Exec(); err != nil {
		t.Fatal(err)
	}
	if err := s.normalizeLegacyTimes(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.checkTimeFormats(t.Context()); err != nil {
		t.Fatalf("迁移后应当干净: %v", err)
	}
	if got := strings.Trim(raw("quote(created_at)"), "'"); got != "2026-09-14T23:06:57Z" {
		t.Fatalf("驱动默认格式没被归一化: %q", got)
	}
	// 迁移是无时区值按本地时区解释：09:38:30 本地 = 01:38:30 UTC（+08:00）。
	if got := strings.Trim(raw("quote(updated_at)"), "'"); !strings.HasSuffix(got, "Z") || !strings.Contains(got, "T") {
		t.Fatalf("无时区值没被归一化: %q", got)
	}
	// 幂等：再跑一遍不做任何改动。
	if err := s.normalizeLegacyTimes(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.checkTimeFormats(t.Context()); err != nil {
		t.Fatal(err)
	}
}
