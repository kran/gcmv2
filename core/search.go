package core

// 全文检索引擎（可替换接口 + 默认实现）。
//
// 设计:
//   - 接口: Sync/DeleteNode/Search/Rebuild — 换引擎（jieba 分词 / Bleve 等）只换实现,
//     调用方零改动。
//   - "哪些节点进索引"是业务规则（类型 search:true + 已发布）, 由 Service 判断,
//     引擎无脑"给什么索引什么"。
//   - Sync 收 tx: SQLite 实现与 nodes 同事务（强一致）; 外部引擎忽略 tx 自行管理。
//   - 默认实现: SQLite FTS5 表 + bigram 预分词（CJK 2 字符滑窗, 英文/数字保留原词）。
//     bigram 子串级精确（phrase 查询连续序列）; 零依赖, 新词自动覆盖。

import (
	"fmt"
	"strings"

	"github.com/kran/dba"
)

// SearchIndex 全文检索引擎契约。
type SearchIndex interface {
	// Sync 事务内同步单个节点（upsert）: 引擎只负责"给什么索引什么",
	// 是否该进索引由 Service 判断（调用方保证）。
	Sync(tx *dba.SQL, n *Node) error
	// Delete 事务内删除节点索引（调用方保证节点确实不该在索引里）。
	Delete(tx *dba.SQL, id int64) error
	// Search 全文搜索: q 为原始查询词, typ 空 = 全类型; 返回节点分页 + 总数。
	Search(q, typ string, page, size int) ([]Node, int64, error)
	// Rebuild 全量重建索引（类型声明变化后调用, 如新增 search:true 类型）。
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

// searchableText 拼接可搜文本: title 列 + 全部 string/text/richtext 标量字段值。
func (s *Service) searchableText(n *Node) string {
	td, ok := s.types.Type(n.Type)
	if !ok {
		return n.Display
	}
	parts := []string{n.Display}
	for _, f := range td.Fields {
		if !s.types.IsRefKind(f.Kind) {
			if v, ok := n.Fields[f.Name]; ok {
				if str, ok := v.(string); ok && str != "" {
					parts = append(parts, str)
				}
			}
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

// Search bm25 相关性排序 + JOIN nodes 取完整节点。
func (f *ftsIndex) Search(q, typ string, page, size int) ([]Node, int64, error) {
	bq := bigram(strings.TrimSpace(q))
	if bq == "" {
		return nil, 0, fmt.Errorf("core: search: empty query")
	}
	// phrase 查询: 连续 bigram = 原文子串（多词精确）; 单 bigram 直接匹配
	match := `"` + bq + `"`
	var total int64
	where := `nodes_fts MATCH #{1}`
	args := []any{match}
	if typ != "" {
		where += ` AND type = #{2}`
		args = append(args, typ)
	}
	totalPtr, err := f.svc.db.Add(
		`SELECT COUNT(1) FROM nodes_fts WHERE `+where, args...).FetchOne[int64]()
	if err != nil {
		return nil, 0, fmt.Errorf("core: search: %w", err)
	}
	if totalPtr != nil {
		total = *totalPtr
	}
	// fts5 列在 JOIN 下歧义: 子查询限行, 外层 JOIN nodes 取完整节点
	// bm25 升序 = 相关性降序; 权重: type 0（过滤列）, title 10, body 1
	// 链式 Add: 子查询分页段独立计数（#{1} = size, #{2} = offset）
	dq := f.svc.db.Add(
		`SELECT n.* FROM nodes n JOIN (
			SELECT rowid FROM nodes_fts WHERE `+where+`
			ORDER BY bm25(nodes_fts, 0.0, 10.0, 1.0)`, args...).
		Add(`LIMIT #{1} OFFSET #{2}) f ON f.rowid = n.id`, size, (page-1)*size)
	rows, err := dq.FetchList[Node]()
	if err != nil {
		return nil, 0, fmt.Errorf("core: search: %w", err)
	}
	return rows, total, nil
}

// Rebuild 全量重建: 全表清空 + 全部已发布可搜类型重索引。
func (f *ftsIndex) Rebuild() error {
	return f.svc.db.Transaction(func(tx *dba.SQL) error {
		if _, err := tx.Add(`DELETE FROM nodes_fts`).Exec(); err != nil {
			return err
		}
		var rows []Node
		q := tx.Add(`SELECT * FROM nodes WHERE status = #{1}`, StatusPublished)
		rows, err := q.FetchList[Node]()
		if err != nil {
			return err
		}
		for i := range rows {
			if !f.svc.searchableType(rows[i].Type) {
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

// searchableType 类型是否声明可搜索（search: true）。
func (s *Service) searchableType(typeName string) bool {
	td, ok := s.types.Type(typeName)
	if !ok {
		return false
	}
	return td.Search
}

// Search 全文搜索原语（类型 search:true + 已发布才在索引里; 引擎由
// SetSearchIndex 替换）。typ 空 = 全类型。
func (s *Service) Search(q, typ string, page, size int) ([]Node, int64, error) {
	return s.search.Search(q, typ, page, size)
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
	if s.searchableType(n.Type) && n.Status == StatusPublished {
		return s.search.Sync(tx, n)
	}
	return s.search.Delete(tx, n.ID)
}

// searchDelete 搜索索引删除（AfterDelete; 事务内）。
func (s *Service) searchDelete(tx *dba.SQL, id int64) error {
	return s.search.Delete(tx, id)
}
