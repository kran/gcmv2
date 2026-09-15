package core

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kran/gcmv2/types"
)

// Time 是内核统一的时间类型（格式见 types.TimeFormat：UTC + RFC3339 + 秒精度 + …Z）。
// 内嵌 time.Time，Format/IsZero/Before/After/Sub 等照旧可用；只在写库、读库、JSON
// 三个边界上换成统一格式。
type Time struct{ time.Time }

// TimeOf 把任意 time.Time 归一化成内核时间（UTC + 截断到秒）。
func TimeOf(t time.Time) Time { return Time{t.UTC().Truncate(time.Second)} }

// Value 实现 driver.Valuer：写进库里的永远是这个字符串。
func (t Time) Value() (driver.Value, error) {
	if t.IsZero() {
		return nil, nil
	}
	return types.FormatTime(t.Time), nil
}

// Scan 实现 sql.Scanner。收三种形态，都是实测出来的驱动行为：
//
//   - time.Time：modernc 会把声明为 TIMESTAMP/DATETIME 的列解析成 time.Time 再交过来
//     （统一格式也走这里）。注意这条路**看不见库里的原始文本** —— 无时区墙钟、带 m=+ 的
//     怪物格式一样能解析成功，所以 core.Time 自己发现不了它们，只有启动闸门能挡。
//   - string：驱动解析不了的文本（脏值），以及 TEXT 列。只认统一格式 —— 列里带偏移或
//     带毫秒会让"字典序 = 时间序"失效，而闸门只在启动时跑，这里要挡住运行期读到的脏值。
//   - nil：可空列（如 accounts.session_expires_at）。
//
// 其余（epoch 数字、[]byte）报错：时间列不是 BLOB，驱动也只对 BLOB 给 []byte。
func (t *Time) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*t = Time{}
		return nil
	case time.Time:
		*t = TimeOf(v)
		return nil
	case string:
		parsed, err := time.Parse(types.TimeFormat, strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("core: time: %q 不是统一格式 %s（老库请先跑 tools/legacy-time）", v, types.TimeFormat)
		}
		*t = Time{parsed}
		return nil
	default:
		return fmt.Errorf("core: time: cannot scan %T", src)
	}
}

// ParseTime 解析 API 输入：统一格式，或带时区偏移的 RFC3339（归一化到 UTC）。
// 裸的本地墙钟值拒收（没有时区就没有确定的瞬间）。
//
// **不认**历史写法（驱动默认的 time.Time.String()、Unix 秒）—— 老库里的那些值由一次性
// 工具 tools/legacy-time 在升级前处理，内核不做兼容；漏掉的由启动闸门 checkTimeFormats 挡。
func ParseTime(s string) (Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Time{}, nil
	}
	parsed, err := types.ParseTime(s)
	if err != nil {
		return Time{}, fmt.Errorf("core: time: %w", err)
	}
	return TimeOf(parsed), nil
}

// MarshalJSON 输出统一格式（零值输出 null）。
func (t Time) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(types.FormatTime(t.Time))
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
	return types.FormatTime(t.Time)
}
