package core

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
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

// Scan 实现 sql.Scanner。除统一格式外，还认历史遗留写法（迁移期用，见 ParseTime）。
func (t *Time) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*t = Time{}
		return nil
	case time.Time:
		*t = TimeOf(v)
		return nil
	case int64:
		*t = TimeOf(time.Unix(v, 0))
		return nil
	case float64:
		*t = TimeOf(time.Unix(int64(v), 0))
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

// ParseTime 解析统一格式，以及历史遗留的写法：
// 驱动默认的 time.Time.String()（"… +0800 CST m=+0.02"）、无时区的种子 SQL 字符串、
// RFC3339（任意偏移）、纯日期。无时区的写法按 UTC 处理（不依赖服务器时区，结果确定）。
func ParseTime(s string) (Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Time{}, nil
	}
	// 驱动默认格式带单调时钟后缀（" m=+0.02"）：先切掉。
	if i := strings.Index(s, " m=+"); i > 0 {
		s = strings.TrimSpace(s[:i])
	}
	for _, layout := range []string{
		TimeFormat,            // 2026-09-14T23:06:41Z（统一格式）
		time.RFC3339Nano,      // 2026-09-14T23:06:41.615238+08:00
		time.RFC3339,          // 2026-09-14T23:06:41+08:00
		"2006-01-02T15:04:05", // 无时区
		"2006-01-02 15:04:05.999999999 -0700 MST", // time.Time.String()
		"2006-01-02 15:04:05.999999999-07:00",     // 驱动的 _time_format=sqlite
		"2006-01-02 15:04:05.999999999",           // 无时区 + 小数
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if parsed, err := time.Parse(layout, s); err == nil {
			return TimeOf(parsed), nil
		}
	}
	return Time{}, fmt.Errorf("core: time: unrecognized %q", s)
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

// timeColumns 是所有存时间的内核列。迁移与闸门共用这一张清单：
// 新加表/新列时在这里补一行，闸门会跟着检查。
var timeColumns = []struct{ table, column string }{
	{"nodes", "created_at"},
	{"nodes", "updated_at"},
	{"edges", "created_at"},
	{"auth_methods", "created_at"},
	{"auth_methods", "updated_at"},
	{"sessions", "expires_at"},
	{"sessions", "created_at"},
	{"settings", "updated_at"},
	{"accounts", "created_at"},
	{"accounts", "updated_at"},
	{"accounts", "session_expires_at"},
}

// checkTimeFormats 是时间闸门：每个时间列的值都必须**就是**统一格式。
//
// 干什么用：Time 的 Value/Scan 管住了走 dba 映射的读写，但 `dba.H{...}` 和手写 SQL
// 绕过了类型系统，编译器看不见（历史上就是这么漏掉的）。这里在启动时数一遍，
// 有一行不对就 fail-loud —— 谁再直接塞 time.Now() 进来，立刻会在这里被抓住。
// COALESCE 是必要的：strftime 解析不了的值会返回 NULL，而 `x <> NULL` 恒为 NULL，
// 不会进计数 —— 那正好会漏掉最该抓的那些脏值。
func (s *Service) checkTimeFormats(ctx context.Context) error {
	for _, col := range timeColumns {
		n, err := s.db.WithCtx(ctx).Add(
			`SELECT COUNT(*) FROM ` + col.table + ` WHERE ` + col.column + ` IS NOT NULL` +
				` AND COALESCE(strftime('%Y-%m-%dT%H:%M:%SZ', ` + col.column + `), '') <> ` + col.column).FetchOne[int]()
		if err != nil {
			return fmt.Errorf("core: time gate %s.%s: %w", col.table, col.column, err)
		}
		if n != nil && *n > 0 {
			return fmt.Errorf("core: %s.%s 有 %d 行不是统一时间格式（应为 %s，见 core.Time）",
				col.table, col.column, *n, TimeFormat)
		}
	}
	return nil
}

// normalizeLegacyTimes 一次性迁移：把历史时间写法统一成 TimeFormat。
//
// 处理三种历史值：
//   - 驱动默认的 time.Time.String()（"… +0800 CST m=+0.02"）→ 带偏移，无歧义
//   - RFC3339（任意偏移）→ 无歧义
//   - 无时区的裸字符串（种子 SQL 手写的 "2026-08-30 09:38:30"）→ 按**服务器本地时区**
//     解释（那些值是人按本地墙钟写的），这是唯一一处依赖时区的转换，只此一次。
//
// 所有库升级完成后（v1）可以连同这个函数一起删掉。
func (s *Service) normalizeLegacyTimes(ctx context.Context) error {
	changed := 0
	for _, col := range timeColumns {
		rows, err := s.db.WithCtx(ctx).Add(
			`SELECT rowid AS row_id, quote(` + col.column + `) AS raw FROM ` + col.table + ` WHERE ` + col.column + ` IS NOT NULL`).
			FetchList[struct {
			RowID int64  `db:"row_id"`
			Raw   string `db:"raw"`
		}]()
		if err != nil {
			return fmt.Errorf("core: normalize %s.%s: %w", col.table, col.column, err)
		}
		for _, row := range rows {
			raw := strings.Trim(row.Raw, "'")
			parsed, err := parseLegacyTime(raw)
			if err != nil {
				return fmt.Errorf("core: normalize %s.%s row %d: %w", col.table, col.column, row.RowID, err)
			}
			canonical := parsed.String()
			if canonical == raw {
				continue
			}
			if _, err := s.db.WithCtx(ctx).Add(
				`UPDATE `+col.table+` SET `+col.column+` = #{1} WHERE rowid = #{2}`, canonical, row.RowID).Exec(); err != nil {
				return fmt.Errorf("core: normalize %s.%s row %d: %w", col.table, col.column, row.RowID, err)
			}
			changed++
		}
	}
	if changed > 0 {
		slog.Info("core: legacy time values normalized", "rows", changed)
	}
	return nil
}

// parseLegacyTime 解析历史值；无时区的裸字符串按服务器本地时区解释（迁移专用）。
func parseLegacyTime(raw string) (Time, error) {
	trimmed := strings.TrimSpace(raw)
	if strings.Index(trimmed, "Z") >= 0 || strings.Contains(trimmed, "+") || strings.Contains(trimmed, "-0700") {
		return ParseTime(trimmed)
	}
	for _, layout := range []string{"2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, trimmed, time.Local); err == nil {
			return TimeOf(t), nil
		}
	}
	return ParseTime(trimmed)
}
