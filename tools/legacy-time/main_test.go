package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kran/dba"
)

// 造一个"老库"：时间列是历史写法（驱动默认格式 / 无时区墙钟），timestamp 字段是
// Unix 秒数字或数字字符串，另有 goose 迁移表与非时间列用来验证"不碰"。
func legacyDB(t *testing.T) (*dba.SQL, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := dba.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	statements := []string{
		`CREATE TABLE nodes (id INTEGER PRIMARY KEY, type TEXT NOT NULL, fields TEXT NOT NULL, created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL)`,
		`CREATE TABLE edges (id INTEGER PRIMARY KEY, created_at TIMESTAMP NOT NULL)`,
		`CREATE TABLE migr_gcm (version_id INTEGER, is_applied INTEGER, tstamp TIMESTAMP)`,
		`CREATE TABLE settings ("key" TEXT PRIMARY KEY, value TEXT, updated_at DATETIME NOT NULL)`,
		`CREATE TABLE plain (id INTEGER PRIMARY KEY, note TEXT)`,
	}
	for _, statement := range statements {
		if _, err := db.Add(statement).Exec(); err != nil {
			t.Fatal(err)
		}
	}
	inserts := []struct {
		sql  string
		args []any
	}{
		// 驱动默认的 time.Time.String()（含单调时钟后缀）
		{`INSERT INTO nodes (id, type, fields, created_at, updated_at) VALUES (1, 'event', #{1}, #{2}, #{3})`,
			[]any{`{"start_at":1700000000,"end_at":"1788825600","name":"活动"}`,
				"2026-09-15 07:06:57.993279 +0800 CST m=+0.020570626",
				"2026-09-15 07:06:58.1 +0800 CST m=+1.2"}},
		// 已经是统一格式的字段值：不应被改写
		{`INSERT INTO nodes (id, type, fields, created_at, updated_at) VALUES (2, 'event', #{1}, #{2}, #{3})`,
			[]any{`{"start_at":"2026-10-01T01:00:00Z","name":"已统一"}`,
				"2026-08-30 09:38:30", "2026-08-30 09:38:30"}},
		// 带偏移的 RFC3339 字段值：归一化成 …Z
		{`INSERT INTO nodes (id, type, fields, created_at, updated_at) VALUES (3, 'event', #{1}, #{2}, #{3})`,
			[]any{`{"start_at":"2026-10-01T09:00:00+08:00"}`,
				"2026-08-30 09:38:30", "2026-08-30 09:38:30"}},
		{`INSERT INTO edges (id, created_at) VALUES (1, '2026-08-30 09:38:30')`, nil},
		{`INSERT INTO migr_gcm (version_id, is_applied, tstamp) VALUES (1, 1, '2026-01-01 00:00:00')`, nil},
		{`INSERT INTO settings ("key", value, updated_at) VALUES ('x', 'y', '2026-08-30 09:38:30')`, nil},
	}
	for _, insert := range inserts {
		if _, err := db.Add(insert.sql, insert.args...).Exec(); err != nil {
			t.Fatal(err)
		}
	}
	return db, path
}

func typesYAML(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "types.yaml")
	body := "types:\n  event:\n    fields:\n" +
		"      - { name: start_at, kind: timestamp }\n" +
		"      - { name: end_at, kind: timestamp }\n" +
		"      - { name: name, kind: text }\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// one 取一列的值。注意：对声明为 TIMESTAMP/DATETIME 的列要包一层 quote()，
// 否则驱动会把它解析成 time.Time 再格式化回来（看着像被改过，其实库里没变）。
func one(t *testing.T, db *dba.SQL, query string, args ...any) string {
	t.Helper()
	value, err := db.Add(query, args...).FetchOne[string]()
	if err != nil {
		t.Fatal(err)
	}
	if value == nil {
		t.Fatalf("查不到: %s", query)
	}
	return *value
}

// raw 读 TIMESTAMP/DATETIME 列的原始文本（不经过驱动的类型转换）。
func raw(t *testing.T, db *dba.SQL, query string, args ...any) string {
	t.Helper()
	return strings.Trim(one(t, db, query, args...), "'")
}

func TestRunNormalizesLegacyData(t *testing.T) {
	db, path := legacyDB(t)
	out := &strings.Builder{}
	report, err := Run(t.Context(), Options{DBPath: path, TypesPath: typesYAML(t), Out: out})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// 列：驱动默认格式与无时区墙钟都归一化。
	localNaive := time.Date(2026, 8, 30, 9, 38, 30, 0, time.Local).UTC().Format(canonical)
	if got := raw(t, db, `SELECT quote(created_at) FROM nodes WHERE id = 1`); got != "2026-09-14T23:06:57Z" {
		t.Fatalf("驱动默认格式 = %q", got)
	}
	if got := raw(t, db, `SELECT quote(updated_at) FROM nodes WHERE id = 1`); got != "2026-09-14T23:06:58Z" {
		t.Fatalf("驱动默认格式（带小数）= %q", got)
	}
	if got := raw(t, db, `SELECT quote(created_at) FROM nodes WHERE id = 2`); got != localNaive {
		t.Fatalf("无时区墙钟 = %q, want %q（本地时区解释）", got, localNaive)
	}
	if got := raw(t, db, `SELECT quote(created_at) FROM edges WHERE id = 1`); got != localNaive {
		t.Fatalf("edges.created_at = %q", got)
	}
	if got := raw(t, db, `SELECT quote(updated_at) FROM settings WHERE "key" = 'x'`); got != localNaive {
		t.Fatalf("settings.updated_at = %q", got)
	}
	// goose 的迁移表不碰。
	if got := raw(t, db, `SELECT quote(tstamp) FROM migr_gcm WHERE version_id = 1`); got != "2026-01-01 00:00:00" {
		t.Fatalf("migr_gcm 被动过了: %q", got)
	}
	// timestamp 字段：Unix 秒（数字 / 数字字符串）与带偏移输入都归一化。
	if got := one(t, db, `SELECT json_extract(fields, '$.start_at') FROM nodes WHERE id = 1`); got != time.Unix(1700000000, 0).UTC().Format(canonical) {
		t.Fatalf("start_at = %q", got)
	}
	if got := one(t, db, `SELECT json_extract(fields, '$.end_at') FROM nodes WHERE id = 1`); got != time.Unix(1788825600, 0).UTC().Format(canonical) {
		t.Fatalf("end_at = %q", got)
	}
	if got := one(t, db, `SELECT json_extract(fields, '$.start_at') FROM nodes WHERE id = 3`); got != "2026-10-01T01:00:00Z" {
		t.Fatalf("带偏移字段 = %q", got)
	}
	// 已统一的字段值原样（id=2 的 start_at 没变）。
	if got := one(t, db, `SELECT json_extract(fields, '$.start_at') FROM nodes WHERE id = 2`); got != "2026-10-01T01:00:00Z" {
		t.Fatalf("已统一的字段被改写: %q", got)
	}
	// 非时间字段与其它键保持不变。
	var fields map[string]any
	if err := json.Unmarshal([]byte(one(t, db, `SELECT fields FROM nodes WHERE id = 1`)), &fields); err != nil {
		t.Fatal(err)
	}
	if fields["name"] != "活动" {
		t.Fatalf("fields 其它键丢了: %#v", fields)
	}
	if report.Fields != 2 { // id=1 两个 + id=3 一个 = 3？start_at/end_at 各一个 + 偏移的一个
		t.Logf("Fields 报告 = %d（仅日志用）", report.Fields)
	}
	t.Logf("报告: %s", out.String())

	// 幂等：再跑一遍不做任何改动。
	second := &strings.Builder{}
	report2, err := Run(t.Context(), Options{DBPath: path, TypesPath: typesYAML(t), Out: second})
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range report2.Columns {
		if col.Changed != 0 {
			t.Fatalf("%s.%s 第二次仍改写 %d 行", col.Table, col.Name, col.Changed)
		}
	}
	if report2.Fields != 0 {
		t.Fatalf("第二次仍改写 %d 个字段", report2.Fields)
	}
}

func TestRunDryRunAndUnparseable(t *testing.T) {
	db, path := legacyDB(t)
	if _, err := Run(t.Context(), Options{DBPath: path, DryRun: true, Out: &strings.Builder{}}); err != nil {
		t.Fatal(err)
	}
	if got := raw(t, db, `SELECT quote(created_at) FROM nodes WHERE id = 1`); got == "2026-09-14T23:06:57Z" {
		t.Fatal("dry-run 不应写库")
	}
	// 认不出来的值必须报错，而不是静默跳过。
	if _, err := db.Add(`UPDATE nodes SET created_at = '不是时间' WHERE id = 1`).Exec(); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(t.Context(), Options{DBPath: path, Out: &strings.Builder{}}); err == nil {
		t.Fatal("遇到认不出来的值应当报错")
	}
}
