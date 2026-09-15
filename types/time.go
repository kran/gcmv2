package types

import (
	"fmt"
	"time"
)

// TimeFormat 是内核里时间的唯一表示：UTC、RFC3339、秒精度、…Z。
//
// 为什么是它（每一条都是实测出来的）：
//   - SQLite 原生认得：datetime()/strftime()/julianday() 都能解析 "2026-09-14T23:06:41Z"
//   - 定宽 ⇒ 字典序 = 时间序：可以直接当文本比较/排序（ORDER BY、范围筛选一条 SQL）
//   - 全 UTC ⇒ 同一瞬间只有一种写法（带本地偏移会让"同一瞬间"有两种字符串）
//   - 与 JSON、客户端常识一致（new Date('…Z') 在小程序/iOS 上都可靠）
//
// 反例（都是我们踩过的）：驱动默认的 time.Time.String()（datetime() 返回 NULL）、
// 可变小数（'.6Z' 会排在 '.615238Z' 之后）、不带时区的墙钟（换个时区就认错）。
const TimeFormat = "2006-01-02T15:04:05Z"

// FormatTime 归一化：UTC + 截断到秒（秒精度是"定宽"的前提）。
func FormatTime(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format(TimeFormat)
}

// ParseTime 解析时间输入：统一格式，或带时区偏移的 RFC3339（归一化到 UTC）。
//
// 裸的本地墙钟值（"2026-09-15 07:06:57"）拒收 —— 没有时区就没有确定的瞬间，
// 猜一个时区就是替用户做错决定。
func ParseTime(s string) (time.Time, error) {
	if parsed, err := time.Parse(TimeFormat, s); err == nil {
		return parsed, nil
	}
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("expects %s (or RFC3339 with a zone offset), got %q", TimeFormat, s)
	}
	return parsed.UTC().Truncate(time.Second), nil
}
