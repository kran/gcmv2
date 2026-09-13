package core

// 全文检索引擎（可替换接口 + 默认实现）。
//
// 设计:
//   - 接口: Sync/DeleteNode/Search/Rebuild — 换引擎（jieba 分词 / Bleve 等）只换实现,
//     调用方零改动。
//   - 所有 active + searchable 节点进入索引；可见范围由查询时 Policy Scope 决定。
//   - Sync 收 tx: SQLite 实现与 nodes 同事务（强一致）; 外部引擎忽略 tx 自行管理。
//   - 默认实现: SQLite FTS5 表 + bigram 预分词（CJK 2 字符滑窗, 英文/数字保留原词）。
//     bigram 子串级精确（phrase 查询连续序列）; 零依赖, 新词自动覆盖。

import (
	"context"
	"fmt"
	"strings"

	"github.com/kran/dba"
	gquery "github.com/kran/gcmv2/query"
)

// SearchTarget applies one Type-specific user filter and mandatory Policy scope.
type SearchTarget struct {
	Type  string
	Where gquery.Expr
	Scope QueryScope
}

// SearchQuery is the caller-facing full-text search contract. Targets are
// explicit because each Type has its own Schema and Policy scope.
type SearchQuery struct {
	Text    string
	Targets []SearchTarget
	Page    gquery.Page
	// CountLimit 同 ListQuery, 但默认更小（DefaultSearchCountLimit）:
	// 检索命中可能命中全库, 且每个命中的统计成本远高于列表（FTS 扫描 + 节点 JOIN）。
	CountLimit int
}

// DefaultSearchCountLimit 检索默认统计上限: 热门词命中可能占满全库,
// 精确统计要扫完整个命中集（10 万命中约 7s）, 而首页只要 ~50µs。
const DefaultSearchCountLimit = 1_000

// SearchPlanTarget contains a Type and its already policy-merged filter.
type SearchPlanTarget struct {
	Type  string
	Where gquery.Expr
}

// SearchPlan is passed to SearchIndex implementations only after Service has
// checked target Types and merged every mandatory Policy scope.
type SearchPlan struct {
	Text       string
	Targets    []SearchPlanTarget
	Page       gquery.Page
	CountLimit int
}

// SearchIndex 全文检索引擎契约。
type SearchIndex interface {
	// Sync 事务内同步单个节点（upsert）: 引擎只负责"给什么索引什么",
	// 是否该进索引由 Service 判断（调用方保证）。
	Sync(tx *dba.SQL, n *Node) error
	// Delete 事务内删除节点索引（调用方保证节点确实不该在索引里）。
	Delete(tx *dba.SQL, id int64) error
	// Search executes an already policy-merged full-text plan.
	Search(context.Context, SearchPlan) ([]Node, int64, error)
	// Rebuild 全量重建索引（类型声明变化后调用, 如新增 searchable 类型）。
	Rebuild(context.Context) error
}

// SetSearchIndex 替换检索引擎（默认 FTS5+bigram; 换引擎后调用方需自行 Rebuild）。
func (s *Service) SetSearchIndex(idx SearchIndex) {
	if idx == nil {
		panic("core: SetSearchIndex(nil)")
	}
	s.search = idx
}

// bigram CJK 连续段 → 2 字符滑窗, 其余（英文/数字）保留原词:
//
//	"人工智能与AI" → "人工 工智 智能 与AI"（"与AI" 含 CJK+拉丁混合, 整段不切）
//
// 查询侧同样处理; 多字查询用 phrase（连续 bigram = 原文子串, 精确）。
func bigram(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	cjk := func(r rune) bool { return r >= 0x4e00 && r <= 0x9fff }
	// 按 CJK/非 CJK 交替切段
	segStart := 0
	inCJK := cjk(rune(s[0]))
	flush := func(end int) {
		seg := strings.TrimSpace(s[segStart:end])
		if seg == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		if inCJK {
			// CJK 段: bigram 滑窗
			runes := []rune(seg)
			for i := 0; i < len(runes)-1; i++ {
				if i > 0 {
					b.WriteByte(' ')
				}
				b.WriteString(string(runes[i : i+2]))
			}
			if len(runes) == 1 {
				b.WriteString(string(runes))
			}
		} else {
			b.WriteString(seg)
		}
	}
	for i, r := range s {
		isCJK := cjk(r)
		if isCJK != inCJK {
			flush(i)
			segStart = i
			inCJK = isCJK
		}
	}
	flush(len(s))
	return b.String()
}

// ftsIndex 默认实现: SQLite FTS5 + bigram。
type ftsIndex struct {
	svc *Service
}

// NewFTSIndex 建默认检索引擎（FTS5 表由迁移 00002 创建）。
func NewFTSIndex(svc *Service) SearchIndex { return &ftsIndex{svc: svc} }

// searchableText 只拼接 searchable.fields 声明的类型字段值。
// display 不在这里: 它是节点列, 索引时总是写进 nodes_fts 的 display 列（权重最高）。
func (s *Service) searchableText(n *Node) string {
	capability, ok := s.types.Searchable(n.Type)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(capability.Fields))
	for _, field := range capability.Fields {
		value, ok := n.Fields[field].(string)
		if ok && value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, " ")
}

// Sync upsert 索引（rowid = node id）。
func (f *ftsIndex) Sync(tx *dba.SQL, n *Node) error {
	display := bigram(n.Display)
	body := bigram(f.svc.searchableText(n))
	if display == "" && body == "" {
		// 连标签都没有: 不索引（也清残留）
		return f.Delete(tx, n.ID)
	}
	// FTS5 虚拟表不支持 UPSERT: 先删后插（同事务, 原子）
	if _, err := tx.Add(`DELETE FROM nodes_fts WHERE rowid = #{1}`, n.ID).Exec(); err != nil {
		return fmt.Errorf("core: fts sync: %w", err)
	}
	if _, err := tx.Add(
		`INSERT INTO nodes_fts (rowid, type, display, body_text) VALUES (#{1}, #{2}, #{3}, #{4})`,
		n.ID, n.Type, display, body).Exec(); err != nil {
		return fmt.Errorf("core: fts sync: %w", err)
	}
	return nil
}

// Delete 删索引。
func (f *ftsIndex) Delete(tx *dba.SQL, id int64) error {
	if _, err := tx.Add(`DELETE FROM nodes_fts WHERE rowid = #{1}`, id).Exec(); err != nil {
		return fmt.Errorf("core: fts delete: %w", err)
	}
	return nil
}

// ftsPhrase 把一个词元写成 FTS5 短语字面量（内部的 " 翻倍转义 —— FTS5 的查询语言
// 有自己的语法, 参数绑定只保护 SQL 那一层, MATCH 后面的字符串还会被 FTS5 再解析一次;
// 官方没有转义函数, 也没有 db.quote() 之类的替代）。
//
// 每个词元都包成短语, 关键字和操作符因此只是普通词: AND/OR/NOT/NEAR 不会当操作符,
// a:b 不会变成列过滤, abc* 不会变成前缀查询, 单个 " 也不再是语法错误.
func ftsPhrase(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// ftsOr 把 bigram 串（空格分隔）拆成 OR 的短语串; 只有一个词元时返回空
// （没有放宽余地 —— 它确实不在索引里）。
func ftsOr(bq string) string {
	tokens := strings.Fields(bq)
	if len(tokens) < 2 {
		return ""
	}
	phrases := make([]string, len(tokens))
	for i, token := range tokens {
		phrases[i] = ftsPhrase(token)
	}
	return strings.Join(phrases, " OR ")
}

// presentTokens 只保留语料里真实存在的 bigram 短语（探针看的是"这个词存不存在",
// 与调用方的读范围无关 —— 这里判断的是词元是不是接缝噪音, 不是可见性）。
func (f *ftsIndex) presentTokens(ctx context.Context, bq string) ([]string, error) {
	tokens := strings.Fields(bq)
	kept := make([]string, 0, len(tokens))
	for _, token := range tokens {
		phrase := ftsPhrase(token)
		found, err := f.svc.db.WithCtx(ctx).Add(
			`SELECT 1 FROM nodes_fts WHERE nodes_fts MATCH #{1} LIMIT 1`, phrase).FetchOne[int]()
		if err != nil {
			return nil, fmt.Errorf("core: search token probe: %w", err)
		}
		if found != nil {
			kept = append(kept, phrase)
		}
	}
	return kept, nil
}

// ftsWhere 一次检索的行范围片段: 命中集合 ∩ 调用方给的目标范围。
// 片段自带的 #{1}/#{2} 在拼进外层语句时整体变成一个占位符。
func ftsWhere(match string, scope dba.Node) dba.Node {
	return dba.Expr(`nodes_fts MATCH #{1} AND n.archived_at IS NULL AND #{2}`, match, scope)
}

// countMatches 统计命中数。limit > 0 时截断（子查询里的 LIMIT 让扫描提前结束,
// 调用方按"至少这么多"渲染）; limit == 0 精确计数。
// 注意 dba 的 #{n} 按片段各自编号: 每个 Add/AddIf 都从 #{1} 起算。
func (f *ftsIndex) countMatches(ctx context.Context, where dba.Node, limit int) (int64, error) {
	total, err := f.svc.db.WithCtx(ctx).
		Add(`SELECT COUNT(1) FROM (SELECT n.id FROM nodes_fts
			JOIN nodes n ON n.id = nodes_fts.rowid WHERE #{1}`, where).
		AddIf(limit > 0, ` LIMIT #{1}`, limit+1).
		Add(`)`).
		FetchOne[int64]()
	if err != nil {
		return 0, fmt.Errorf("core: search: %w", err)
	}
	if total == nil {
		return 0, nil
	}
	if limit > 0 && *total > int64(limit) {
		return int64(limit), nil
	}
	return *total, nil
}

// resolveMatch 决定这次检索用哪个 MATCH 表达式, 并连同行范围一起返回。
// 三级, 逐级放宽, 命中数随 match 一起变:
//  1. 精确子串 —— 整个查询是一个 phrase（连续 bigram = 原文子串）
//  2. 丢掉语料里不存在的 bigram 后 AND —— "农业著名" → 农业 AND 著名
//  3. OR —— 接缝 bigram 恰好存在时 AND 会太严, 宁可多给, 不要空手
func (f *ftsIndex) resolveMatch(ctx context.Context, bq string, scope dba.Node, limit int) (dba.Node, int64, error) {
	where := ftsWhere(ftsPhrase(bq), scope)
	total, err := f.countMatches(ctx, where, limit)
	if err != nil || total > 0 {
		return where, total, err
	}
	kept, err := f.presentTokens(ctx, bq)
	if err != nil {
		return where, 0, err
	}
	if len(kept) > 0 {
		where = ftsWhere(strings.Join(kept, " AND "), scope)
		if total, err = f.countMatches(ctx, where, limit); err != nil || total > 0 {
			return where, total, err
		}
	}
	if alt := ftsOr(bq); alt != "" {
		where = ftsWhere(alt, scope)
		if total, err = f.countMatches(ctx, where, limit); err != nil {
			return where, 0, err
		}
	}
	return where, total, nil
}

// Search performs bm25 ranking after applying every target's mandatory scope.
func (f *ftsIndex) Search(ctx context.Context, search SearchPlan) ([]Node, int64, error) {
	bq := bigram(strings.TrimSpace(search.Text))
	if bq == "" {
		return nil, 0, fmt.Errorf("core: search: empty query")
	}
	page := normalizePage(search.Page)
	scope, err := f.compileTargets(ctx, search.Targets)
	if err != nil {
		return nil, 0, err
	}
	limit := search.CountLimit
	if limit == 0 {
		limit = DefaultSearchCountLimit
	}
	// 命中数决定放宽到哪一级（见 resolveMatch）; where 与它配套, 直接给下面的取行用。
	where, total, err := f.resolveMatch(ctx, bq, scope, limit)
	if err != nil {
		return nil, 0, err
	}

	rows, err := f.svc.db.WithCtx(ctx).Add(
		`SELECT n.* FROM nodes_fts JOIN nodes n ON n.id = nodes_fts.rowid
		 WHERE #{1} ORDER BY bm25(nodes_fts, 0.0, 10.0, 1.0), n.id DESC
		 LIMIT #{2} OFFSET #{3}`,
		where, page.Size, (page.Number-1)*page.Size).FetchList[Node]()
	if err != nil {
		return nil, 0, fmt.Errorf("core: search: %w", err)
	}
	return rows, total, nil
}

func (f *ftsIndex) compileTargets(ctx context.Context, targets []SearchPlanTarget) (dba.Node, error) {
	compiler := &queryCompiler{service: f.svc, ctx: ctx}
	clauses := make([]dba.Node, 0, len(targets))
	for _, target := range targets {
		compiled, err := compiler.compile(target.Where, target.Type, "n", 0)
		if err != nil {
			return dba.Node{}, err
		}
		clauses = append(clauses, dba.Expr(`(n.type = #{1} AND #{2})`, target.Type, compiled))
	}
	args := make([]any, len(clauses))
	parts := make([]string, len(clauses))
	for i := range clauses {
		args[i] = clauses[i]
		parts[i] = fmt.Sprintf("#{%d}", i+1)
	}
	return dba.Expr("("+strings.Join(parts, " OR ")+")", args...), nil
}

// Rebuild 全量重建：所有 active + searchable Node 都进入索引。
func (f *ftsIndex) Rebuild(ctx context.Context) error {
	return f.svc.db.WithCtx(ctx).Transaction(func(tx *dba.SQL) error {
		if _, err := tx.Add(`DELETE FROM nodes_fts`).Exec(); err != nil {
			return err
		}
		q := tx.Add(`SELECT * FROM nodes WHERE archived_at IS NULL`)
		rows, err := q.FetchList[Node]()
		if err != nil {
			return err
		}
		for i := range rows {
			if !f.svc.shouldIndex(&rows[i]) {
				continue
			}
			if err := f.Sync(tx, &rows[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

// RebuildSearch 全量重建搜索索引（seed/批量导入后调用 — 索引是写路径同步的,
// 直接 INSERT 的数据不会进索引）。
func (s *Service) RebuildSearch(ctx context.Context) error {
	return s.search.Rebuild(ctx)
}

func (s *Service) shouldIndex(node *Node) bool {
	if node == nil || node.ArchivedAt != nil {
		return false
	}
	_, ok := s.types.Searchable(node.Type)
	return ok
}

// Search executes schema-aware full-text search with mandatory Type scopes.
// Scope merging happens before delegation so custom SearchIndex implementations
// receive only effective, non-overridable filters.
func (s *Service) Search(ctx context.Context, search SearchQuery) ([]Node, int64, error) {
	if strings.TrimSpace(search.Text) == "" {
		return nil, 0, fmt.Errorf("%w: search text required", ErrInvalidQuery)
	}
	if len(search.Targets) == 0 {
		return nil, 0, fmt.Errorf("%w: search targets required", ErrInvalidQuery)
	}
	if len(search.Targets) > 64 {
		return nil, 0, fmt.Errorf("%w: search exceeds 64 target types", ErrQueryTooComplex)
	}
	seen := make(map[string]bool, len(search.Targets))
	targets := make([]SearchPlanTarget, len(search.Targets))
	for i, target := range search.Targets {
		if seen[target.Type] {
			return nil, 0, fmt.Errorf("%w: duplicate search type %q", ErrInvalidQuery, target.Type)
		}
		seen[target.Type] = true
		if _, ok := s.types.Searchable(target.Type); !ok {
			return nil, 0, fmt.Errorf("%w: type %q is not searchable", ErrInvalidQuery, target.Type)
		}
		effective, err := target.Scope.apply(target.Where)
		if err != nil {
			return nil, 0, err
		}
		if effective == nil {
			effective = gquery.True()
		}
		targets[i] = SearchPlanTarget{Type: target.Type, Where: effective}
	}
	plan := SearchPlan{Text: search.Text, Targets: targets, Page: normalizePage(search.Page), CountLimit: search.CountLimit}
	return s.search.Search(ctx, plan)
}

// ── 搜索同步 hook（注册在 New 的 Define 之后） ──

// initSearch 搜索初始化（New 调用 — Define 之后）: 建默认 FTS5 索引 + 注册
// 同步 hook（AfterCreate/AfterUpdate 同步, AfterDelete 删除）。
// SetSearchIndex 可换实现 — searchSync 运行时读 s.search, 替换后 hook 照常。
func (s *Service) initSearch() {
	s.search = NewFTSIndex(s)
	if err := s.hooks.AddHook(HookNodeAfterCreate, s.searchSync); err != nil {
		panic("core: register search sync hook: " + err.Error())
	}
	if err := s.hooks.AddHook(HookNodeAfterUpdate, s.searchSync); err != nil {
		panic("core: register search sync hook: " + err.Error())
	}
	if err := s.hooks.AddHook(HookNodeAfterDelete, s.searchDelete); err != nil {
		panic("core: register search delete hook: " + err.Error())
	}
	if err := s.hooks.AddHook(HookNodeAfterArchive, s.searchSync); err != nil {
		panic("core: register search archive hook: " + err.Error())
	}
	if err := s.hooks.AddHook(HookNodeAfterRestore, s.searchSync); err != nil {
		panic("core: register search restore hook: " + err.Error())
	}
}

// searchSync 搜索索引同步（AfterCreate/AfterUpdate; 事务内）。
func (s *Service) searchSync(tx *dba.SQL, n *Node) error {
	if s.shouldIndex(n) {
		return s.search.Sync(tx, n)
	}
	return s.search.Delete(tx, n.ID)
}

// searchDelete 搜索索引删除（AfterDelete; 事务内）。
func (s *Service) searchDelete(tx *dba.SQL, id int64) error {
	return s.search.Delete(tx, id)
}
