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
}

// SearchPlanTarget contains a Type and its already policy-merged filter.
type SearchPlanTarget struct {
	Type  string
	Where gquery.Expr
}

// SearchPlan is passed to SearchIndex implementations only after Service has
// checked target Types and merged every mandatory Policy scope.
type SearchPlan struct {
	Text    string
	Targets []SearchPlanTarget
	Page    gquery.Page
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
	Rebuild() error
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

// searchableText 只拼接 searchable.fields 显式声明的值。
func (s *Service) searchableText(n *Node) string {
	capability, ok := s.types.Searchable(n.Type)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(capability.Fields))
	for _, field := range capability.Fields {
		if field == "display" {
			parts = append(parts, n.Display)
			continue
		}
		value, ok := n.Fields[field].(string)
		if ok && value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, " ")
}

// Sync upsert 索引（rowid = node id）。
func (f *ftsIndex) Sync(tx *dba.SQL, n *Node) error {
	body := bigram(f.svc.searchableText(n))
	if body == "" {
		// 无可搜文本: 不索引（也清残留）
		return f.Delete(tx, n.ID)
	}
	// FTS5 虚拟表不支持 UPSERT: 先删后插（同事务, 原子）
	if _, err := tx.Add(`DELETE FROM nodes_fts WHERE rowid = #{1}`, n.ID).Exec(); err != nil {
		return fmt.Errorf("core: fts sync: %w", err)
	}
	if _, err := tx.Add(
		`INSERT INTO nodes_fts (rowid, type, display, body_text) VALUES (#{1}, #{2}, #{3}, #{4})`,
		n.ID, n.Type, bigram(n.Display), body).Exec(); err != nil {
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

// Search performs bm25 ranking after applying every target's mandatory scope.
func (f *ftsIndex) Search(ctx context.Context, search SearchPlan) ([]Node, int64, error) {
	bq := bigram(strings.TrimSpace(search.Text))
	if bq == "" {
		return nil, 0, fmt.Errorf("core: search: empty query")
	}
	page := normalizePage(search.Page)
	scope, err := f.compileTargets(search.Targets)
	if err != nil {
		return nil, 0, err
	}
	match := `"` + bq + `"`
	where := dba.Expr(`nodes_fts MATCH #{1} AND n.archived_at IS NULL AND #{2}`, match, scope)

	totalPtr, err := f.svc.db.WithCtx(ctx).Add(
		`SELECT COUNT(1) FROM nodes_fts JOIN nodes n ON n.id = nodes_fts.rowid WHERE #{1}`,
		where).FetchOne[int64]()
	if err != nil {
		return nil, 0, fmt.Errorf("core: search: %w", err)
	}
	var total int64
	if totalPtr != nil {
		total = *totalPtr
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

func (f *ftsIndex) compileTargets(targets []SearchPlanTarget) (dba.Node, error) {
	compiler := &queryCompiler{service: f.svc}
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
func (f *ftsIndex) Rebuild() error {
	return f.svc.db.Transaction(func(tx *dba.SQL) error {
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
func (s *Service) RebuildSearch() error {
	return s.search.Rebuild()
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
	plan := SearchPlan{Text: search.Text, Targets: targets, Page: normalizePage(search.Page)}
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
