package core

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kran/dba"
)

// TimeFormat 是内核统一的时间表示：UTC、RFC3339、秒精度、带 Z 后缀。
// 库里、JSON 里、查询条件里都是同一个字符串，例如 "2026-09-14T23:06:41Z"。
//
// 为什么自己管而不交给驱动：modernc 的 SQLite 驱动默认按 time.Time.String() 写时间，
// 落库是 "2026-09-15 07:06:57.993279 +0800 CST m=+0.02" —— SQLite 的 datetime() 解析
// 不了它（返回 NULL），于是"按时间排序 / 范围筛选 / 按月统计"在 SQL 里全部失效。
// 它另外两种可配格式分别是"可变小数 + 服务器时区偏移"和"完全不带时区"，也都不能当
// 唯一形状：小数位不固定会破坏字典序（'.6Z' 会排在 '.615238Z' 之后），带偏移则随
// 服务器时区变化。这里显式写 UTC + 秒精度，定宽 ⇒ 字典序就是时间序。
const TimeFormat = "2006-01-02T15:04:05Z"

// Time 是内核统一的时间类型。内嵌 time.Time，Format/IsZero/Before/After/Sub 等照旧可用；
// 只在写库、读库、JSON 三个边界上换成统一格式。
type Time struct{ time.Time }

// TimeOf 把任意 time.Time 归一化成内核时间（UTC + 截断到秒）。
func TimeOf(t time.Time) Time { return Time{t.UTC().Truncate(time.Second)} }

// Value 实现 driver.Valuer：写进库里的永远是这个字符串。
func (t Time) Value() (driver.Value, error) {
	if t.IsZero() {
		return nil, nil
	}
	return t.UTC().Truncate(time.Second).Format(TimeFormat), nil
}

// Scan 实现 sql.Scanner：只认统一格式。
//
// 驱动若把列直接映射成 time.Time（例如开了 _texttotime）也照收并归一化；
// 其余一律报错 —— 库里出现别的写法说明没跑那个一次性工具，宁可起不来也不要静默读错。
func (t *Time) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*t = Time{}
		return nil
	case time.Time:
		*t = TimeOf(v)
		return nil
	case []byte:
		return t.Scan(string(v))
	case string:
		parsed, err := ParseTime(v)
		if err != nil {
			return err
		}
		*t = parsed
		return nil
	default:
		return fmt.Errorf("core: time: cannot scan %T", src)
	}
}

// ParseTime 解析时间输入：统一格式（…Z）或任意带偏移的 RFC3339。
//
// **不认**历史写法（驱动默认的 time.Time.String()、无时区的裸字符串、Unix 秒）——
// 老库里的那些值由一次性工具 tools/legacy-time 在升级前处理，内核不做兼容：
// 见到非统一格式一律报错，由启动闸门 checkTimeFormats 兜住。
func ParseTime(s string) (Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Time{}, nil
	}
	for _, layout := range []string{TimeFormat, time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, s); err == nil {
			return TimeOf(parsed), nil
		}
	}
	return Time{}, fmt.Errorf("core: time: %q is not %s (or RFC3339 with an offset)", s, TimeFormat)
}

// MarshalJSON 输出统一格式（零值输出 null）。
func (t Time) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(t.UTC().Truncate(time.Second).Format(TimeFormat))
}

// UnmarshalJSON 接受统一格式与任意带偏移的 RFC3339，一律归一化。
func (t *Time) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := ParseTime(s)
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}

// String 输出统一格式（日志、调试、模板都用它）。
func (t Time) String() string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Truncate(time.Second).Format(TimeFormat)
}

// checkTimeFormats 是时间闸门：库里每个时间列的值都必须**就是**统一格式。
//
// 干什么用：core.Time 管住了走 dba 映射的读写，但 `dba.H{...}` 和手写 SQL 绕过类型
// 系统，编译器看不见（历史上就是这么漏掉的）。这里在启动时数一遍，有一行不对就
// fail-loud —— 老库没跑 tools/legacy-time 就会挡在这里，而不是静默读错时间。
//
// 列清单不写死：按声明的类型（TIMESTAMP / DATETIME）从 pragma 里发现，新表新列自动
// 纳入；跳过 goose 自己的迁移表（migr_*）与 sqlite 内部表。
// COALESCE 是必要的：strftime 解析不了的值返回 NULL，而 `x <> NULL` 恒为 NULL，
// 不会进计数 —— 那正好会漏掉最该抓的脏值。
func (s *Service) checkTimeFormats(ctx context.Context) error {
	columns, err := discoverTimeColumns(ctx, s.db)
	if err != nil {
		return err
	}
	for _, col := range columns {
		n, err := s.db.WithCtx(ctx).Add(
			`SELECT COUNT(*) FROM ` + col.table + ` WHERE ` + col.column + ` IS NOT NULL` +
				` AND COALESCE(strftime('%Y-%m-%dT%H:%M:%SZ', ` + col.column + `), '') <> ` + col.column).FetchOne[int]()
		if err != nil {
			return fmt.Errorf("core: time gate %s.%s: %w", col.table, col.column, err)
		}
		if n != nil && *n > 0 {
			return fmt.Errorf("core: %s.%s 有 %d 行不是统一时间格式（应为 %s）："+
				"老库请先跑 tools/legacy-time", col.table, col.column, *n, TimeFormat)
		}
	}
	return nil
}

// timeColumn 是一个时间列的定位。
type timeColumn struct{ table, column string }

// discoverTimeColumns 找出库里所有声明为时间类型的列（跳过 goose 与 sqlite 内部表）。
func discoverTimeColumns(ctx context.Context, db *dba.SQL) ([]timeColumn, error) {
	tables, err := db.WithCtx(ctx).Add(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE 'migr_%'`).
		FetchList[string]()
	if err != nil {
		return nil, fmt.Errorf("core: time gate: list tables: %w", err)
	}
	var out []timeColumn
	for _, table := range tables {
		columns, err := db.WithCtx(ctx).Add(
			`SELECT name FROM pragma_table_info(#{1}) WHERE type IN ('TIMESTAMP', 'DATETIME')`, table).
			FetchList[string]()
		if err != nil {
			return nil, fmt.Errorf("core: time gate: %s columns: %w", table, err)
		}
		for _, column := range columns {
			out = append(out, timeColumn{table: table, column: column})
		}
	}
	return out, nil
}
