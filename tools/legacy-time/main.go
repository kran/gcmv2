// Command legacy-time 把老库里的时间值改成内核统一格式（core.TimeFormat，"…Z"）。
//
// 它**不属于内核**：内核只认统一格式，见到别的就 fail-loud（启动闸门 checkTimeFormats）。
// 历史写法的知识只活在这里。升级顺序：停服 → 跑本工具 → 启动新版。
//
// 用法：
//
//	go run ./tools/legacy-time -db /path/to/gcm.sqlite [-types types.yaml] [-dry-run]
//
// 处理两处：
//  1. 库里所有声明为 TIMESTAMP / DATETIME 的列（跳过 goose 的 migr_* 表与 sqlite 内部表）
//  2. 给了 -types 时：types.yaml 里 kind: timestamp 的字段值
//     （老库里是 Unix 秒数字，或是被演示 SQL 写成字符串的数字）
//
// 幂等：已经统一过的库跑第二遍不做任何改动。认不出来的值会报错退出（不静默跳过）。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kran/dba"
	"github.com/kran/gcmv2/types"

	_ "modernc.org/sqlite"
)

// canonical 与 core.TimeFormat 一致：UTC、RFC3339、秒精度、带 Z。
const canonical = "2006-01-02T15:04:05Z"

// zoneLayouts 是历史出现过的**自带时区**的写法，解析结果无歧义。
var zoneLayouts = []string{
	canonical,
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999999 -0700 MST", // 驱动默认 time.Time.String()
	"2006-01-02 15:04:05.999999999-07:00",     // 驱动的 _time_format=sqlite
}

// wallLayouts 是没有时区的墙钟写法（种子 SQL 手写的）。这些值是当年按本地墙钟写的，
// 所以按**服务器本地时区**解释 —— 唯一一处依赖时区的转换，只在这个一次性工具里。
var wallLayouts = []string{
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// Options 是工具入参。
type Options struct {
	DBPath    string
	TypesPath string // 可选：给了才处理 timestamp 字段值
	DryRun    bool
	Out       io.Writer
}

// Column 是一次列迁移的统计。
type Column struct {
	Table, Name string
	Scanned     int
	Changed     int
}

// Report 是整体结果。
type Report struct {
	Columns []Column
	Fields  int // 改动的 timestamp 字段值个数
}

// Run 执行迁移；DryRun 时只统计不写库。
func Run(ctx context.Context, opts Options) (Report, error) {
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	db, err := dba.Open("sqlite", opts.DBPath+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return Report{}, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	var report Report
	columns, err := discoverTimeColumns(ctx, db)
	if err != nil {
		return Report{}, err
	}
	for _, col := range columns {
		stat, err := migrateColumn(ctx, db, col, opts.DryRun)
		if err != nil {
			return Report{}, err
		}
		report.Columns = append(report.Columns, stat)
	}
	if opts.TypesPath != "" {
		n, err := migrateTimestampFields(ctx, db, opts.TypesPath, opts.DryRun)
		if err != nil {
			return Report{}, err
		}
		report.Fields = n
	}
	for _, col := range report.Columns {
		if col.Changed > 0 {
			fmt.Fprintf(out, "  %s.%s: %d/%d 行改写\n", col.Table, col.Name, col.Changed, col.Scanned)
		}
	}
	if report.Fields > 0 {
		fmt.Fprintf(out, "  timestamp 字段值: %d 个改写\n", report.Fields)
	}
	if opts.DryRun {
		fmt.Fprintln(out, "（dry-run：没有写库）")
	}
	return report, nil
}

func migrateColumn(ctx context.Context, db *dba.SQL, col column, dryRun bool) (Column, error) {
	stat := Column{Table: col.table, Name: col.column}
	rows, err := db.WithCtx(ctx).Add(
		`SELECT rowid AS row_id, quote(` + col.column + `) AS raw FROM ` + col.table +
			` WHERE ` + col.column + ` IS NOT NULL`).FetchList[struct {
		RowID int64  `db:"row_id"`
		Raw   string `db:"raw"`
	}]()
	if err != nil {
		return stat, fmt.Errorf("读取 %s.%s: %w", col.table, col.column, err)
	}
	for _, row := range rows {
		stat.Scanned++
		raw := strings.Trim(row.Raw, "'")
		canonicalValue, err := normalizeLegacyTime(raw)
		if err != nil {
			return stat, fmt.Errorf("%s.%s rowid=%d: %w", col.table, col.column, row.RowID, err)
		}
		if canonicalValue == raw {
			continue
		}
		stat.Changed++
		if dryRun {
			continue
		}
		if _, err := db.WithCtx(ctx).Add(
			`UPDATE `+col.table+` SET `+col.column+` = #{1} WHERE rowid = #{2}`, canonicalValue, row.RowID).Exec(); err != nil {
			return stat, fmt.Errorf("改写 %s.%s rowid=%d: %w", col.table, col.column, row.RowID, err)
		}
	}
	return stat, nil
}

// normalizeLegacyTime 解析历史写法并输出统一格式。无时区的裸字符串按**服务器本地时区**
// 解释（那些值是人按本地墙钟写的），这是唯一一处依赖时区的转换。
func normalizeLegacyTime(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("空值")
	}
	// 驱动默认格式带单调时钟后缀（" m=+0.02"）：先切掉。
	if i := strings.Index(s, " m=+"); i > 0 {
		s = strings.TrimSpace(s[:i])
	}
	for _, layout := range zoneLayouts {
		if parsed, err := time.Parse(layout, s); err == nil {
			return parsed.UTC().Truncate(time.Second).Format(canonical), nil
		}
	}
	for _, layout := range wallLayouts {
		if parsed, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return parsed.UTC().Truncate(time.Second).Format(canonical), nil
		}
	}
	return "", fmt.Errorf("认不出来的时间值 %q（自带时区或 %s 本地墙钟）", raw, "YYYY-MM-DD HH:MM:SS")
}

// normalizeTimestampField 处理 timestamp kind 的字段值：老的 Unix 秒（数字或数字字符串）
// 转成统一格式；已经是统一格式的原样返回。
func normalizeTimestampField(v any) (string, bool, error) {
	switch value := v.(type) {
	case nil:
		return "", false, nil
	case float64:
		return time.Unix(int64(value), 0).UTC().Format(canonical), true, nil
	case json.Number:
		seconds, err := value.Int64()
		if err != nil {
			return "", false, fmt.Errorf("数字 %v: %w", value, err)
		}
		return time.Unix(seconds, 0).UTC().Format(canonical), true, nil
	case string:
		if strings.TrimSpace(value) == "" {
			return "", false, nil
		}
		// 被演示 SQL 写成字符串的数字（"1788825600"）
		if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
			return time.Unix(seconds, 0).UTC().Format(canonical), true, nil
		}
		normalized, err := normalizeLegacyTime(value)
		if err != nil {
			return "", false, err
		}
		return normalized, normalized != value, nil
	default:
		return "", false, fmt.Errorf("timestamp 值类型不支持: %T", v)
	}
}

func migrateTimestampFields(ctx context.Context, db *dba.SQL, typesPath string, dryRun bool) (int, error) {
	raw, err := os.ReadFile(typesPath)
	if err != nil {
		return 0, fmt.Errorf("读 types.yaml: %w", err)
	}
	ts := types.New()
	if err := ts.Load(raw); err != nil {
		return 0, fmt.Errorf("解析 types.yaml: %w", err)
	}
	changed := 0
	for _, typeName := range ts.Names() {
		def, _ := ts.Type(typeName)
		var fields []string
		for _, field := range def.Fields {
			if field.Kind == types.KindTimestamp {
				fields = append(fields, field.Name)
			}
		}
		if len(fields) == 0 {
			continue
		}
		rows, err := db.WithCtx(ctx).Add(
			`SELECT rowid AS row_id, fields AS raw FROM nodes WHERE type = #{1}`, typeName).FetchList[struct {
			RowID int64  `db:"row_id"`
			Raw   string `db:"raw"`
		}]()
		if err != nil {
			return changed, fmt.Errorf("读取 %s 节点: %w", typeName, err)
		}
		for _, row := range rows {
			decoded := map[string]any{}
			decoder := json.NewDecoder(strings.NewReader(row.Raw))
			decoder.UseNumber()
			if err := decoder.Decode(&decoded); err != nil {
				return changed, fmt.Errorf("%s rowid=%d 的 fields 不是 JSON: %w", typeName, row.RowID, err)
			}
			dirty := false
			for _, name := range fields {
				value, ok := decoded[name]
				if !ok {
					continue
				}
				normalized, isChanged, err := normalizeTimestampField(value)
				if err != nil {
					return changed, fmt.Errorf("%s.%s rowid=%d: %w", typeName, name, row.RowID, err)
				}
				if isChanged {
					decoded[name] = normalized
					dirty = true
				}
			}
			if !dirty {
				continue
			}
			changed++
			if dryRun {
				continue
			}
			encoded, err := json.Marshal(decoded)
			if err != nil {
				return changed, err
			}
			if _, err := db.WithCtx(ctx).Add(
				`UPDATE nodes SET fields = #{1} WHERE rowid = #{2}`, string(encoded), row.RowID).Exec(); err != nil {
				return changed, fmt.Errorf("改写 %s rowid=%d: %w", typeName, row.RowID, err)
			}
		}
	}
	return changed, nil
}

// column 是一个时间列的定位；discoverTimeColumns 与内核的闸门用同一套发现规则。
type column struct{ table, column string }

func discoverTimeColumns(ctx context.Context, db *dba.SQL) ([]column, error) {
	tables, err := db.WithCtx(ctx).Add(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE 'migr_%'`).
		FetchList[string]()
	if err != nil {
		return nil, fmt.Errorf("列出表: %w", err)
	}
	var out []column
	for _, table := range tables {
		names, err := db.WithCtx(ctx).Add(
			`SELECT name FROM pragma_table_info(#{1}) WHERE type IN ('TIMESTAMP', 'DATETIME')`, table).
			FetchList[string]()
		if err != nil {
			return nil, fmt.Errorf("读取 %s 的列: %w", table, err)
		}
		for _, name := range names {
			out = append(out, column{table: table, column: name})
		}
	}
	return out, nil
}

func main() {
	var opts Options
	flag.StringVar(&opts.DBPath, "db", "", "SQLite 数据库路径（必填）")
	flag.StringVar(&opts.TypesPath, "types", "", "types.yaml 路径（可选：给了才处理 timestamp 字段值）")
	flag.BoolVar(&opts.DryRun, "dry-run", false, "只统计不写库")
	flag.Parse()
	if opts.DBPath == "" {
		flag.Usage()
		os.Exit(2)
	}
	report, err := Run(context.Background(), opts)
	if err != nil {
		log.Fatalf("legacy-time: %v", err)
	}
	columns, changed := 0, 0
	for _, col := range report.Columns {
		columns++
		changed += col.Changed
	}
	fmt.Printf("检查 %d 个时间列，改写 %d 行；timestamp 字段改写 %d 个\n", columns, changed, report.Fields)
	if changed == 0 && report.Fields == 0 {
		fmt.Println("已经是统一格式，无需改动")
	}
}
