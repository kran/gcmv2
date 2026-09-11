package core

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kran/dba"
	gquery "github.com/kran/gcmv2/query"
	"github.com/kran/gcmv2/types"
	_ "modernc.org/sqlite"
)

// 规模基准: 当前内核在不同数据量下的写入速率、库大小和读延迟。
//
// 默认跳过（构建数据集耗时）。跑法:
//
//	GCM_SCALE=1000,10000,100000 go test -run TestScale -v -timeout 60m ./core/
//	GCM_SCALE=1000000 GCM_SCALE_BULK=1 go test -run TestScale -v -timeout 60m ./core/
//
// GCM_SCALE_BULK=1 时用单事务直插构建数据集（API 写路径太慢，见报告里的写入速率），
// 只测读侧; 两种模式的读延迟可直接对比。
const scaleTypesYAML = `
types:
  category:
    capabilities:
      addressable: { field: slug, unique: global }
      publication: { field: publication_state, draft: draft, published: published }
      tree: { parent: parent, order: position }
    fields:
      - { name: slug, kind: slug }
      - { name: publication_state, kind: select, options: [draft, published], default: draft }
      - { name: position, kind: number, default: 0 }
      - { name: parent, kind: ref, to: category }
  article:
    capabilities:
      searchable: { fields: [display, title, body] }
      addressable: { field: slug, unique: global }
      publication: { field: publication_state, draft: draft, published: published }
    constraints:
      indexes: [[publication_state, publish_time]]
    fields:
      - { name: slug, kind: slug }
      - { name: publication_state, kind: select, options: [draft, published], default: draft }
      - { name: position, kind: number, default: 0 }
      - { name: title, kind: text }
      - { name: body, kind: richtext }
      - { name: publish_time, kind: timestamp }
      - { name: views, kind: number }
      - { name: categories, kind: "ref[]", to: category }
`

func TestScale(t *testing.T) {
	spec := os.Getenv("GCM_SCALE")
	if spec == "" {
		t.Skip("规模基准: 设 GCM_SCALE=<节点数,...> 才跑")
	}
	bulk := os.Getenv("GCM_SCALE_BULK") == "1"
	for _, field := range strings.Split(spec, ",") {
		size, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil || size <= 0 {
			t.Fatalf("GCM_SCALE 解析失败: %q", field)
		}
		runScale(t, size, bulk)
	}
}

// scaleFixture 一个规模下的数据集与结果。
type scaleFixture struct {
	size       int
	dir        string
	service    *Service
	db         *dba.SQL
	categories []int64
	articleIDs []int64
	subtree    []int64 // 用于子树过滤的分类 id 集合
}

func runScale(t *testing.T, size int, bulk bool) {
	t.Helper()
	ctx := t.Context()
	f := newScaleFixture(t, size)
	if bulk {
		f.loadBulk(t)
	} else {
		f.loadAPI(t)
	}
	t.Logf("\n%s\n规模 %d 节点%s\n%s", strings.Repeat("=", 78), size, bulkSuffix(bulk), strings.Repeat("=", 78))
	f.reportSize(t)
	f.reportWrites(t, ctx)
	f.reportReads(t, ctx)
	f.reportPlans(t, ctx)
	f.reportCandidates(t)
	f.reportRebuild(t, ctx)
	f.reportConcurrent(t, ctx)
}

func bulkSuffix(bulk bool) string {
	if bulk {
		return "（直插构建，只测读）"
	}
	return "（走 API 写路径）"
}

// lastSQL/lastArgs 捕获最近执行的 SQL（EXPLAIN 用）— 基准单线程顺序执行, 够用。
var (
	lastSQL  string
	lastArgs []any
)

func newScaleFixture(t *testing.T, size int) *scaleFixture {
	t.Helper()
	dir := t.TempDir()
	dsn := filepath.Join(dir, "scale.db") + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := dba.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = migrateUp(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	ts := types.New()
	err = ts.Load([]byte(scaleTypesYAML))
	if err != nil {
		t.Fatal(err)
	}
	// dba 的 SetLogger 返回克隆（不是原地改）— 必须用返回值建引擎, 否则捕获不到 SQL
	db = db.SetLogger(func(_ context.Context, _ time.Time, query string, args []any, err error) {
		if err != nil {
			return
		}
		lastSQL, lastArgs = query, args
	})
	f := &scaleFixture{size: size, dir: dir, db: db}
	f.service = New(db, ts)
	return f
}

// loadAPI 走正常写路径（CreateNode + 引用边 + FTS 同步）建数据。
func (f *scaleFixture) loadAPI(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	catCount := categoryCount(f.size)
	f.createCategories(ctx, t, catCount)
	start := time.Now()
	for i := 0; i < f.size; i++ {
		node := f.articleNode(i)
		id, err := f.service.CreateNode(ctx, node)
		if err != nil {
			t.Fatalf("CreateNode #%d: %v", i, err)
		}
		f.articleIDs = append(f.articleIDs, id)
	}
	elapsed := time.Since(start)
	t.Logf("API 写入 %d 节点: %s (%.0f 节点/秒, %s/节点)", f.size, elapsed.Round(time.Millisecond),
		float64(f.size)/elapsed.Seconds(), (elapsed / time.Duration(f.size)).Round(time.Microsecond))
}

// loadBulk 单事务直插（节点 + 边）后同步关系元数据与 FTS 索引 — 只用于把数据集做大。
func (f *scaleFixture) loadBulk(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	catCount := categoryCount(f.size)
	f.createCategories(ctx, t, catCount)

	sqlDB := f.db.Pool().DB
	tx, err := sqlDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	insertNode, err := tx.Prepare(`INSERT INTO nodes(type, display, fields, revision, created_at, updated_at)
		VALUES ('article', ?, ?, 1, ?, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	insertEdge, err := tx.Prepare(`INSERT INTO edges(from_node, field, to_node, sort, created_at, single_ref, symmetric)
		VALUES (?, 'categories', ?, 0, ?, 0, 0)`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i := 0; i < f.size; i++ {
		fields := fmt.Sprintf(`{"slug":"article-%d","publication_state":"published","position":%d,"title":"规模测试文章 %d","body":%q,"publish_time":%d,"views":%d}`,
			i, i%50, i, scaleBody(i), 1_700_000_000+int64(i%30_000_000), i%9973)
		res, err := insertNode.Exec(fmt.Sprintf("规模测试文章 %d", i), fields, now, now)
		if err != nil {
			t.Fatal(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		f.articleIDs = append(f.articleIDs, id)
		cat := f.categories[i%len(f.categories)]
		_, err = insertEdge.Exec(id, cat, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	err = tx.Commit()
	if err != nil {
		t.Fatal(err)
	}
	err = f.service.SyncRelationSchema()
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	t.Logf("直插 %d 节点+边: %s (%.0f 节点/秒)", f.size, elapsed.Round(time.Millisecond), float64(f.size)/elapsed.Seconds())

	start = time.Now()
	err = f.service.RebuildSearch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("RebuildSearch: %s", time.Since(start).Round(time.Millisecond))
}

func (f *scaleFixture) createCategories(ctx context.Context, t *testing.T, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		node := &Node{
			Type: "category", Display: fmt.Sprintf("分类 %d", i),
			Fields: Fields{
				"slug": fmt.Sprintf("cat-%d", i), "publication_state": "published",
				"position": i,
			},
		}
		id, err := f.service.CreateNode(ctx, node)
		if err != nil {
			t.Fatalf("CreateNode category #%d: %v", i, err)
		}
		f.categories = append(f.categories, id)
		if i > 0 && i%12 == 0 {
			parent := f.categories[i/12-1]
			_, err = f.service.AddEdge(ctx, id, parent, "parent", 0)
			if err != nil {
				t.Fatalf("AddEdge parent #%d: %v", i, err)
			}
		}
	}
	f.subtree = f.categories[:max(1, len(f.categories)/20)]
}

func (f *scaleFixture) articleNode(i int) *Node {
	return &Node{
		Type: "article", Display: fmt.Sprintf("规模测试文章 %d", i),
		Fields: Fields{
			"slug":              fmt.Sprintf("article-%d", i),
			"publication_state": "published",
			"position":          i % 50,
			"title":             fmt.Sprintf("规模测试文章 %d", i),
			"body":              scaleBody(i),
			"publish_time":      1_700_000_000 + i%30_000_000,
			"views":             i % 9973,
			"categories":        []any{f.categories[i%len(f.categories)]},
		},
	}
}

// scaleBody 约 1.5KB 中文正文 — 让 FTS bigram 索引有真实体量。
// 每 1000 条埋一个只在本文出现的标记词, 用于对比"高选择性检索"与常见词检索。
func scaleBody(i int) string {
	var b strings.Builder
	b.WriteString("<p>")
	for j := 0; j < 40; j++ {
		b.WriteString(fmt.Sprintf("第%d段关于产业政策与企业战略的研究笔记，涉及区域经济、品牌建设、组织能力。", i+j))
	}
	if i%1000 == 0 {
		b.WriteString(fmt.Sprintf("（唯一标记 zzmark%08d 仅此篇出现）", i))
	}
	b.WriteString("</p>")
	return b.String()
}

func categoryCount(size int) int {
	return min(2000, max(20, size/200))
}

// ── 报告 ────────────────────────────────────────────────────────────

func (f *scaleFixture) reportSize(t *testing.T) {
	t.Helper()
	nodes, err := f.db.Add(`SELECT COUNT(*) FROM nodes`).FetchOne[int64]()
	if err != nil {
		t.Fatal(err)
	}
	edges, err := f.db.Add(`SELECT COUNT(*) FROM edges`).FetchOne[int64]()
	if err != nil {
		t.Fatal(err)
	}
	main := fileSize(filepath.Join(f.dir, "scale.db"))
	wal := fileSize(filepath.Join(f.dir, "scale.db-wal"))
	fts := int64(0)
	row, err := f.db.Add(`SELECT COALESCE(SUM(LENGTH(block)),0) AS bytes FROM nodes_fts_data`).FetchOneMap()
	if err == nil && row != nil {
		fts = toInt64(row["bytes"])
	}
	t.Logf("数据: nodes=%d edges=%d | 主库=%.1fMB wal=%.1fMB | FTS 索引≈%.1fMB | 库体积/节点≈%.0fB",
		*nodes, *edges, mb(main), mb(wal), mb(fts), float64(main+wal)/float64(*nodes))
	t.Logf("表级明细: %s", f.tableSizes())
}

func (f *scaleFixture) reportWrites(t *testing.T, ctx context.Context) {
	t.Helper()
	if len(f.articleIDs) == 0 {
		return
	}
	// 单节点更新 + 单节点切发布状态（后台最常见的写）
	samples := f.sampleIDs(min(200, len(f.articleIDs)))
	var update, publish []time.Duration
	for i, id := range samples {
		node, err := f.service.GetNodeById(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		revision := node.Revision
		start := time.Now()
		err = f.service.PatchNode(ctx, id, &NodePatch{Revision: &revision, Fields: Fields{"views": 100000 + i}})
		if err != nil {
			t.Fatal(err)
		}
		update = append(update, time.Since(start))
		revision++

		start = time.Now()
		err = f.service.PatchNode(ctx, id, &NodePatch{Revision: &revision, Fields: Fields{"publication_state": "draft"}})
		if err != nil {
			t.Fatal(err)
		}
		publish = append(publish, time.Since(start))
		revision++
		err = f.service.PatchNode(ctx, id, &NodePatch{Revision: &revision, Fields: Fields{"publication_state": "published"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("写: 更新字段 p50=%s p95=%s | 切发布状态(含 FTS 同步) p50=%s p95=%s",
		p50(update), p95(update), p50(publish), p95(publish))
}

func (f *scaleFixture) reportReads(t *testing.T, ctx context.Context) {
	t.Helper()
	published := PolicyScope(gquery.EQ(gquery.Field("publication_state"), "published"))
	listSort := []gquery.SortField{gquery.Desc(gquery.Field("publish_time")), gquery.Desc(gquery.System("id"))}
	iterations := 60
	if f.size >= 100_000 {
		iterations = 20
	}
	// 病态查询（全扫/大命中）单独降迭代次数, 否则一次规模跑要十几分钟
	slow := max(3, iterations/4)

	measure(t, "列表页(已发布+发表时间序,25条)", iterations, func() {
		_, _, err := f.service.QueryPage(ctx, ListQuery{
			Type: "article", Scope: published, Sort: listSort,
			Page: gquery.Page{Number: 1, Size: 25},
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	measure(t, "列表页(不带 total, 只取 25 条)", iterations, func() {
		_, err := f.service.Query(ctx, ListQuery{
			Type: "article", Scope: published, Sort: listSort,
			Page: gquery.Page{Number: 1, Size: 25},
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	measure(t, "列表页(无索引字段排序: views)", slow, func() {
		_, _, err := f.service.QueryPage(ctx, ListQuery{
			Type: "article", Scope: published,
			Sort: []gquery.SortField{gquery.Desc(gquery.Field("views"))},
			Page: gquery.Page{Number: 1, Size: 25},
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	ids := make([]any, len(f.subtree))
	for i, id := range f.subtree {
		ids[i] = id
	}
	measure(t, fmt.Sprintf("分类子树过滤(%d 个分类)+排序", len(f.subtree)), slow, func() {
		_, _, err := f.service.QueryPage(ctx, ListQuery{
			Type: "article", Scope: published,
			Where: gquery.OneOf(gquery.Ref("categories"), ids...), Sort: listSort,
			Page: gquery.Page{Number: 1, Size: 25},
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	measure(t, "深翻页(第 100 页)", iterations, func() {
		_, _, err := f.service.QueryPage(ctx, ListQuery{
			Type: "article", Scope: published, Sort: listSort,
			Page: gquery.Page{Number: 100, Size: 25},
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	measure(t, "检索: 常见词(命中 ~全部)", slow, func() {
		_, _, err := f.service.Search(ctx, SearchQuery{
			Text: "产业政策", Targets: []SearchTarget{{Type: "article", Scope: published}},
			Page: gquery.Page{Number: 1, Size: 10},
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	measure(t, "检索: 常见词(计数上限 10, 只看取页)", min(iterations, 20), func() {
		_, _, err := f.service.Search(ctx, SearchQuery{
			Text: "产业政策", Targets: []SearchTarget{{Type: "article", Scope: published}},
			Page: gquery.Page{Number: 1, Size: 10}, CountLimit: 10,
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	measure(t, "检索: 稀有词(命中 1/1000)", iterations, func() {
		idx := rand.Intn(f.size/1000+1) * 1000
		_, _, err := f.service.Search(ctx, SearchQuery{
			Text: fmt.Sprintf("zzmark%08d", idx), Targets: []SearchTarget{{Type: "article", Scope: published}},
			Page: gquery.Page{Number: 1, Size: 10},
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	measure(t, "地址查询(slug, 唯一索引)", slow, func() {
		idx := rand.Intn(len(f.articleIDs))
		_, err := f.service.GetNodeByAddress(ctx, fmt.Sprintf("article-%d", idx))
		if err != nil {
			t.Fatal(err)
		}
	})

	measure(t, "主键详情", iterations, func() {
		_, err := f.service.GetNodeById(ctx, f.articleIDs[rand.Intn(len(f.articleIDs))])
		if err != nil {
			t.Fatal(err)
		}
	})

	measure(t, "引用读出(ref[] → 分类节点)", iterations, func() {
		id := f.articleIDs[rand.Intn(len(f.articleIDs))]
		_, _, err := f.service.OutEdges(ctx, "article", id, "categories", 1, 10)
		if err != nil {
			t.Fatal(err)
		}
	})

	measure(t, "分类树加载", min(iterations, 10), func() {
		_, err := f.service.LoadTree(ctx, "category", published)
		if err != nil {
			t.Fatal(err)
		}
	})

	measure(t, "子树遍历(内存+边)", min(iterations, 10), func() {
		_, err := f.service.Subtree(ctx, "category", f.categories[0], "parent", 20)
		if err != nil {
			t.Fatal(err)
		}
	})
}

// reportPlans 捕获引擎实际生成的 SQL 并打印查询计划 — 索引有没有被用上一看便知。
// reportCandidates 现状瓶颈的候选实现对照 — 都是纯 SQL, 用来量化"改哪里能快多少"。
func (f *scaleFixture) reportCandidates(t *testing.T) {
	t.Helper()
	iterations := 20
	slow := max(3, iterations/4)

	// ① 地址查询（现状: type IN (...) + json_extract 过滤 → 全类型扫描）
	caseExpr := "(CASE type WHEN 'article' THEN NULLIF(json_extract(fields,'$.slug'),'') " +
		"WHEN 'category' THEN NULLIF(json_extract(fields,'$.slug'),'') END)"
	slug := func() string { return fmt.Sprintf("article-%d", rand.Intn(f.size)) }

	// 候选 A: 显式带上 type IN (...) — 部分索引（WHERE type IN ...）必须能推出条件才可用
	measurePool(t, f, iterations, "候选A: 地址走全局表达式索引(+type IN)", func() {
		var id int64
		row := f.db.Pool().QueryRow(`SELECT id FROM nodes WHERE archived_at IS NULL
			AND type IN ('article','category') AND `+caseExpr+` = ? LIMIT 1`, slug())
		if err := row.Scan(&id); err != nil {
			t.Fatal(err)
		}
	})

	// 候选 B: 按类型建地址索引（addressable capability 天然知道自己有哪些类型）
	_, err := f.db.Pool().Exec(`CREATE INDEX IF NOT EXISTS cand_addr_article
		ON nodes (json_extract(fields,'$.slug')) WHERE type = 'article' AND archived_at IS NULL`)
	if err != nil {
		t.Fatal(err)
	}
	measurePool(t, f, iterations, "候选B: 地址走按类型索引", func() {
		var id int64
		row := f.db.Pool().QueryRow(`SELECT id FROM nodes WHERE archived_at IS NULL
			AND type = 'article' AND json_extract(fields,'$.slug') = ? LIMIT 1`, slug())
		if err := row.Scan(&id); err != nil {
			t.Fatal(err)
		}
	})

	// ② 检索: 现状是精确 count（扫全部命中）+ bm25 排序
	match := `"` + bigram("产业政策") + `"`
	measurePool(t, f, iterations, "候选: 检索截断计数(上限 1000)", func() {
		var total int64
		row := f.db.Pool().QueryRow(
			`SELECT COUNT(*) FROM (SELECT rowid FROM nodes_fts WHERE nodes_fts MATCH ? LIMIT 1001)`, match)
		if err := row.Scan(&total); err != nil {
			t.Fatal(err)
		}
	})
	measurePool(t, f, iterations, "候选: 检索只取首页(不算总数)", func() {
		rows, err := f.db.Pool().Query(`SELECT rowid FROM nodes_fts WHERE nodes_fts MATCH ?
			ORDER BY bm25(nodes_fts, 0.0, 10.0, 1.0) LIMIT 10`, match)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
		}
	})

	// ②b 检索取首页: 区分"bm25 排名（要给全部命中打分）"与"计数"两类成本
	searchFilter := `nodes_fts MATCH ? AND n.archived_at IS NULL AND n.type = 'article'
		AND json_extract(n.fields,'$.publication_state') = 'published'`
	measurePool(t, f, iterations, "候选: 检索页(bm25 排名)", func() {
		rows, err := f.db.Pool().Query(`SELECT n.id FROM nodes_fts JOIN nodes n ON n.id = nodes_fts.rowid
			WHERE `+searchFilter+` ORDER BY bm25(nodes_fts, 0.0, 10.0, 1.0), n.id DESC LIMIT 10`, match)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
		}
	})
	measurePool(t, f, iterations, "候选: 检索页(改按时间序, 不算 total)", func() {
		rows, err := f.db.Pool().Query(`SELECT n.id FROM nodes_fts JOIN nodes n ON n.id = nodes_fts.rowid
			WHERE `+searchFilter+` ORDER BY n.id DESC LIMIT 10`, match)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
		}
	})

	// ③ 列表页: 现状是精确 count（扫全部匹配行）
	measurePool(t, f, iterations, "候选: 列表页截断计数(上限 1000)", func() {
		var total int64
		row := f.db.Pool().QueryRow(`SELECT COUNT(*) FROM (SELECT 1 FROM nodes
			WHERE archived_at IS NULL AND type = 'article'
			AND json_extract(fields,'$.publication_state') = 'published' LIMIT 1001)`)
		if err := row.Scan(&total); err != nil {
			t.Fatal(err)
		}
	})

	// ④ 分类子树过滤: 现状是每行 EXISTS 探边; 候选从边表驱动（idx_edges_to 命中）
	ids := make([]any, 0, len(f.subtree))
	placeholders := make([]string, 0, len(f.subtree))
	for _, id := range f.subtree {
		ids = append(ids, id)
		placeholders = append(placeholders, "?")
	}
	measurePool(t, f, slow, "候选: 子树过滤由边表驱动", func() {
		query := `SELECT id FROM nodes WHERE archived_at IS NULL AND type = 'article'
			AND json_extract(fields,'$.publication_state') = 'published'
			AND id IN (SELECT from_node FROM edges WHERE field = 'categories' AND to_node IN (` +
			strings.Join(placeholders, ",") + `))
			ORDER BY json_extract(fields,'$.publish_time') DESC, id DESC LIMIT 25`
		rows, err := f.db.Pool().Query(query, ids...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
		}
	})
}

// measurePool 直接对原生连接测（候选实现不经过引擎）。
func measurePool(t *testing.T, f *scaleFixture, iterations int, name string, fn func()) {
	t.Helper()
	samples := make([]time.Duration, 0, iterations)
	for i := 0; i < iterations; i++ {
		start := time.Now()
		fn()
		samples = append(samples, time.Since(start))
	}
	t.Logf("读: %-38s p50=%-9s p95=%-9s max=%s", name, p50(samples), p95(samples), maxDur(samples))
}

func (f *scaleFixture) reportPlans(t *testing.T, ctx context.Context) {
	t.Helper()
	published := PolicyScope(gquery.EQ(gquery.Field("publication_state"), "published"))
	listSort := []gquery.SortField{gquery.Desc(gquery.Field("publish_time")), gquery.Desc(gquery.System("id"))}
	cases := []struct {
		name string
		run  func()
	}{
		{"列表页(已发布+发表时间序)", func() {
			_, _, _ = f.service.QueryPage(ctx, ListQuery{
				Type: "article", Scope: published, Sort: listSort,
				Page: gquery.Page{Number: 1, Size: 25},
			})
		}},
		{"地址查询(slug)", func() {
			_, _ = f.service.GetNodeByAddress(ctx, "article-1")
		}},
		{"分类子树过滤+排序", func() {
			ids := make([]any, 0, len(f.subtree))
			for _, id := range f.subtree {
				ids = append(ids, id)
			}
			_, _, _ = f.service.QueryPage(ctx, ListQuery{
				Type: "article", Scope: published, Where: gquery.OneOf(gquery.Ref("categories"), ids...),
				Sort: listSort, Page: gquery.Page{Number: 1, Size: 25},
			})
		}},
		{"全文检索", func() {
			_, _, _ = f.service.Search(ctx, SearchQuery{
				Text: "产业政策", Targets: []SearchTarget{{Type: "article", Scope: published}},
				Page: gquery.Page{Number: 1, Size: 10},
			})
		}},
	}
	for _, c := range cases {
		lastSQL, lastArgs = "", nil
		c.run()
		if lastSQL == "" {
			continue
		}
		plan, err := f.explain(lastSQL, lastArgs)
		if err != nil {
			t.Logf("计划 %-26s: (无法 EXPLAIN: %v)", c.name, err)
			continue
		}
		t.Logf("计划 %-26s: %s", c.name, plan)
	}
}

// explain 对捕获到的 SQL 取 EXPLAIN QUERY PLAN 摘要。
// 注意: 捕获到的是 dba 已渲染好的 SQL（占位符已是 ?）, 所以走原生连接执行,
// 不能再交给 dba.Add（那会二次渲染占位符）。
func (f *scaleFixture) explain(query string, args []any) (string, error) {
	rows, err := f.db.Pool().Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	parts := make([]string, 0, 4)
	for rows.Next() {
		var id, parent, unused int
		var detail string
		err = rows.Scan(&id, &parent, &unused, &detail)
		if err != nil {
			return "", err
		}
		parts = append(parts, detail)
	}
	return strings.Join(parts, " | "), rows.Err()
}

func (f *scaleFixture) reportRebuild(t *testing.T, ctx context.Context) {
	t.Helper()
	start := time.Now()
	err := f.service.RebuildSearch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("重建全文索引(启动/迁移后一次): %s", time.Since(start).Round(time.Millisecond))
}

// reportConcurrent 读写并发: 1 写者 + 4 读者（WAL 档位）。
func (f *scaleFixture) reportConcurrent(t *testing.T, ctx context.Context) {
	t.Helper()
	published := PolicyScope(gquery.EQ(gquery.Field("publication_state"), "published"))
	listSort := []gquery.SortField{gquery.Desc(gquery.Field("publish_time"))}
	done := make(chan struct{})
	stop := make(chan struct{})
	var writeErr, readErr error
	readSamples := make([]time.Duration, 0, 400)
	var readMu = make(chan struct{}, 1)
	readMu <- struct{}{}

	go func() { // 写者: 持续切发布状态（含 FTS 同步）
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			id := f.articleIDs[i%len(f.articleIDs)]
			node, err := f.service.GetNodeById(ctx, id)
			if err != nil {
				writeErr = err
				return
			}
			rev := node.Revision
			err = f.service.PatchNode(ctx, id, &NodePatch{Revision: &rev, Fields: Fields{"views": i}})
			if err != nil {
				writeErr = err
				return
			}
		}
	}()
	for r := 0; r < 4; r++ {
		go func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				start := time.Now()
				_, _, err := f.service.QueryPage(ctx, ListQuery{
					Type: "article", Scope: published, Sort: listSort,
					Page: gquery.Page{Number: 1, Size: 25},
				})
				if err != nil {
					readErr = err
					return
				}
				elapsed := time.Since(start)
				<-readMu
				readSamples = append(readSamples, elapsed)
				readMu <- struct{}{}
			}
		}()
	}
	time.Sleep(3 * time.Second)
	close(stop)
	<-done
	time.Sleep(200 * time.Millisecond)
	if writeErr != nil || readErr != nil {
		t.Fatalf("并发读写失败: write=%v read=%v", writeErr, readErr)
	}
	t.Logf("并发(1 写 + 4 读, 3s): 读延迟 p50=%s p95=%s 样本=%d", p50(readSamples), p95(readSamples), len(readSamples))
}

// ── 小工具 ──────────────────────────────────────────────────────────

// tableSizes 按表统计页占用（dbstat; 不可用时返回空串）。
func (f *scaleFixture) tableSizes() string {
	rows, err := f.db.Add(`SELECT name, SUM(pgsize) AS bytes FROM dbstat GROUP BY name ORDER BY bytes DESC LIMIT 8`).FetchMaps()
	if err != nil {
		return "(dbstat 不可用: " + err.Error() + ")"
	}
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		parts = append(parts, fmt.Sprintf("%s=%.1fMB", fmt.Sprint(row["name"]), mb(toInt64(row["bytes"]))))
	}
	return strings.Join(parts, " ")
}

func (f *scaleFixture) sampleIDs(n int) []int64 {
	out := make([]int64, 0, n)
	step := max(1, len(f.articleIDs)/n)
	for i := 0; i < len(f.articleIDs) && len(out) < n; i += step {
		out = append(out, f.articleIDs[i])
	}
	return out
}

func measure(t *testing.T, name string, iterations int, fn func()) {
	t.Helper()
	samples := make([]time.Duration, 0, iterations)
	for i := 0; i < iterations; i++ {
		start := time.Now()
		fn()
		samples = append(samples, time.Since(start))
	}
	t.Logf("读: %-38s p50=%-9s p95=%-9s max=%s", name, p50(samples), p95(samples), maxDur(samples))
}

func p50(samples []time.Duration) time.Duration { return percentile(samples, 0.50) }
func p95(samples []time.Duration) time.Duration { return percentile(samples, 0.95) }

func percentile(samples []time.Duration, q float64) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(float64(len(sorted)-1) * q)
	return sorted[idx].Round(time.Microsecond)
}

func maxDur(samples []time.Duration) time.Duration {
	var out time.Duration
	for _, s := range samples {
		if s > out {
			out = s
		}
	}
	return out.Round(time.Microsecond)
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func mb(bytes int64) float64 { return float64(bytes) / (1 << 20) }

func toInt64(v any) int64 {
	switch value := v.(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	case []byte:
		n, _ := strconv.ParseInt(string(value), 10, 64)
		return n
	case string:
		n, _ := strconv.ParseInt(value, 10, 64)
		return n
	}
	return 0
}
