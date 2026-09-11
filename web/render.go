// Package web 渲染引擎: 模板引擎（级联/片段/sprig）+ 查询函数注入 + 模板函数
// （内置/sprig/查询原语）。
//
// 查询函数定义在 Go 层（引擎原语 + dba）, 注入 funcMap 后模板一行调用:
//
//	{{ $arts := outRefs 5 "authors" 1 10 }}{{ range $arts }}...{{ end }}
//
// 模板作者不写 SQL。查询错误 fail-loud: panic 传播为渲染错误
// （html/template 捕获 panic 作为 Execute 错误, 渲染层统一处理）。
//
// 无缓存设计: 每次渲染读文件+解析, 天然热重载（改文件下一请求生效）;
// 解析失败每次请求响亮 500（失败响亮）。
package web

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Masterminds/sprig/v3"
	"github.com/kran/gcmv2/core"
	gquery "github.com/kran/gcmv2/query"
	"github.com/kran/gcmv2/types"
	"jaytaylor.com/html2text"
)

// Render 渲染引擎: 模板根目录 + 函数表 + 核心服务（查询注入源）。
type Render struct {
	mu    sync.RWMutex
	root  string
	funcs template.FuncMap // 自定义函数（查询函数 + 站点业务函数）
	eng   core.Engine
}

// NewRender 建渲染引擎。root 是模板目录; svc 提供查询函数。
func NewRender(root string, eng core.Engine) *Render {
	return &Render{root: root, eng: eng, funcs: template.FuncMap{}}
}

// Func 注册自定义模板函数（站点项目扩展, 如业务查询）。
func (e *Render) Func(name string, fn any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.funcs[name] = fn
}

// Render 按候选序取第一个存在的模板执行（级联: node--{type}.html → node.html）。
func (e *Render) Render(c *CmsCtx, w io.Writer, candidates []string, data any) error {
	for _, name := range candidates {
		full := filepath.Join(e.root, name)
		if _, err := os.Stat(full); err != nil {
			continue
		}
		if err := e.execute(c, w, name, full, data); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("render: no template for %q (candidates: %s)", e.root, strings.Join(candidates, ", "))
}

// fail 查询错误 → panic（html/template 捕获为 Execute 错误, fail-loud）。
func fail(err error) {
	if err != nil {
		panic(err)
	}
}

// queryFuncs 查询原语（Go 层实现）+ 展示工具。
// 返回 1 值 — 模板一行调用; 错误走 panic。模板执行时经 funcMap()
// （tpl.go）合并 sprig + 内置函数后整体注入。
func (e *Render) queryFuncs(c *CmsCtx) template.FuncMap {
	eng := e.eng
	// 读规则（行范围 + 字段掩码）按 (action, type) 在 CmsCtx 上缓存：模板里
	// get/list 会被多次调用，同一请求只触发一次规则。
	scopeFor := func(action ReadAction, typeName string) core.QueryScope {
		scope, _, err := c.ReadRule(action, typeName)
		fail(err)
		return scope
	}
	// maskList 模板 helper 的裁剪出口（失败→ panic，与其它查询错误一致）。
	maskList := func(action ReadAction, nodes []core.Node) []core.Node {
		fail(MaskNodes(c, action, nodes))
		return nodes
	}
	return template.FuncMap{
		// ── 查询原语 ─────────────────────────
		// get: 单节点（id 兼容 JSON float64 / int64）。
		// 可见性按 ReadView 读规则解析：不可见 → nil（与 API 的 view 路径同语义），
		// 隐藏字段按同一条规则裁掉。
		"get": func(id any) *core.Node {
			nid, err := types.ToID(id)
			fail(err)
			n, err := eng.GetNodeById(c.R.Context(), nid)
			fail(err)
			if n == nil {
				return nil
			}
			visible, err := eng.Query(c.R.Context(), core.ListQuery{
				Type: n.Type, Where: gquery.EQ(gquery.System("id"), nid),
				Scope: scopeFor(ReadView, n.Type), Page: gquery.Page{Size: 1},
			})
			fail(err)
			if len(visible) == 0 {
				return nil
			}
			node, err := MaskNode(c, ReadView, &visible[0])
			fail(err)
			return node
		},
		// list: 公开类型列表 —— 行范围与字段掩码与 API 共用同一条读规则（ReadList）。
		"list": func(typ string, page, size int) []core.Node {
			list, err := eng.Query(c.R.Context(), core.ListQuery{
				Type: typ, Scope: scopeFor(ReadList, typ),
				Page: gquery.Page{Number: page, Size: size},
			})
			fail(err)
			return maskList(ReadList, list)
		},
		// setting: 站点配置值（按 key 取; 缺失 → nil; 值按 JSON 形态,
		// richtext 模板自行 safeHTML）
		"setting": func(key string) any {
			st, err := eng.GetSetting(c.R.Context(), key)
			fail(err)
			if st == nil {
				return nil
			}
			return st.Value
		},
		// search: 全文检索（每个目标类型各自解析 ReadSearch 读规则）。
		"search": func(q, typ string, page, size int) []core.Node {
			targets := searchTargets(c, eng, typ)
			list, _, err := eng.Search(c.R.Context(), core.SearchQuery{
				Text: q, Targets: targets, Page: gquery.Page{Number: page, Size: size},
			})
			fail(err)
			return maskList(ReadSearch, list)
		},
		// outRefs: 出边目标节点列表（symmetric 双向）。行范围 trusted（按边直接取），
		// 字段掩码按各节点自己的类型套用（与 get 同源）。
		"outRefs": func(from int64, field string, page, size int) []core.Node {
			return e.targets(c, true, func() ([]core.Edge, int64, error) {
				n, err := eng.GetNodeById(c.R.Context(), from)
				if err != nil || n == nil {
					return nil, 0, fmt.Errorf("outRefs: node %d not found", from)
				}
				return eng.OutEdges(c.R.Context(), n.Type, from, field, page, size)
			})
		},
		// inRefs: 入边来源节点列表（inverse 反向 — 取 from_node 端）。行范围 trusted。
		"inRefs": func(to int64, field string, page, size int) []core.Node {
			return e.targets(c, false, func() ([]core.Edge, int64, error) {
				return eng.InEdges(c.R.Context(), to, field, page, size)
			})
		},
		// traverse: 出边递归（祖先链）
		"traverse": func(start int64, field string, maxHops int) []int64 {
			return e.graph(c, start, field, maxHops, eng.Traverse)
		},
		// subtree: 入边递归（子树）
		"subtree": func(start int64, field string, maxHops int) []int64 {
			return e.graph(c, start, field, maxHops, eng.Subtree)
		},
		// equivalence: 等价类
		"equivalence": func(start int64, field string, maxHops int) []int64 {
			return e.graph(c, start, field, maxHops, eng.EquivalenceClass)
		},
		// filterList: Lisp filter 筛选列表（表达式 + 分页）。
		// 用法: {{ filterList "article" "(in ->categories (subtree {:address}))" (dict "address" "news") 1 10 }}
		"filterList": func(typ, expr string, params map[string]any, page, size int) []core.Node {
			where, err := gquery.ParseLisp(expr, params)
			fail(err)
			list, err := e.eng.Query(c.R.Context(), core.ListQuery{
				Type: typ, Where: where, Scope: scopeFor(ReadList, typ),
				Page: gquery.Page{Number: page, Size: size},
			})
			fail(err)
			return maskList(ReadList, list)
		},
		// expand: 统一路径展开 — 输入任意形态（单值或列表）, 返回 any。
		// 用法（管道: 数据是末参）:
		//   {{ $n := get 5 | expand "authors, categories" }}  → *core.Node（带 Expand）
		//   {{ $arts | expand "authors" }}                   → []*core.Node（批量, 查询次数与列表大小无关）
		// 输入: *core.Node / core.Node / int64 / int / float64 / []core.Node / []*core.Node / []int64 / []int / []any(id)
		"expand": func(expr string, v any) any {
			switch value := v.(type) {
			case *core.Node:
				return expandTemplateNodes(c, eng, expr, []int64{value.ID}, false)
			case core.Node:
				return expandTemplateNodes(c, eng, expr, []int64{value.ID}, false)
			case int64, int, float64:
				id, err := types.ToID(v)
				fail(err)
				return expandTemplateNodes(c, eng, expr, []int64{id}, false)
			case []core.Node:
				return expandTemplateNodes(c, eng, expr, nodeIDs(value), true)
			case []*core.Node:
				ids := make([]int64, 0, len(value))
				for _, node := range value {
					if node != nil {
						ids = append(ids, node.ID)
					}
				}
				return expandTemplateNodes(c, eng, expr, ids, true)
			case []int64:
				return expandTemplateNodes(c, eng, expr, value, true)
			case []int:
				ids := make([]int64, len(value))
				for i, id := range value {
					ids[i] = int64(id)
				}
				return expandTemplateNodes(c, eng, expr, ids, true)
			case []any:
				ids := make([]int64, 0, len(value))
				for _, item := range value {
					id, err := types.ToID(item)
					fail(err)
					ids = append(ids, id)
				}
				return expandTemplateNodes(c, eng, expr, ids, true)
			default:
				fail(fmt.Errorf("expand: unsupported input %T (want core.Node / id / list of them)", v))
				return nil
			}
		},

		// ── 展示工具 ─────────────────────────
		// datefmt: 时间格式化（core.Node.CreatedAt 等）
		"datefmt": func(t time.Time, layout string) string {
			return t.Format(layout)
		},
	}
}

// searchTargets 每个可搜索类型的搜索范围都解析 ReadSearch 读规则（与站点搜索 API 一致）。
func searchTargets(c *CmsCtx, eng core.Engine, typeName string) []core.SearchTarget {
	names := eng.Types().Names()
	if typeName != "" {
		names = []string{typeName}
	}
	targets := make([]core.SearchTarget, 0, len(names))
	for _, name := range names {
		if _, ok := eng.Types().Searchable(name); !ok {
			continue
		}
		scope, _, err := c.ReadRule(ReadSearch, name)
		fail(err)
		targets = append(targets, core.SearchTarget{Type: name, Scope: scope})
	}
	return targets
}

func expandTemplateNodes(c *CmsCtx, eng core.Engine, expression string, ids []int64, many bool) any {
	if len(ids) == 0 {
		if many {
			return []*core.Node{}
		}
		return (*core.Node)(nil)
	}
	var paths []gquery.ExpandPath
	var err error
	if strings.TrimSpace(expression) == "" || strings.TrimSpace(expression) == "*" {
		node, getErr := eng.GetNodeById(c.R.Context(), ids[0])
		fail(getErr)
		if node == nil {
			fail(core.ErrNotFound)
		}
		paths = eng.AutoExpand(node.Type)
	} else {
		paths, err = gquery.ParseExpand(expression)
		fail(err)
	}
	expanded, err := eng.ExpandMany(c.R.Context(), ids, paths...)
	fail(err)
	// 展开出来的节点可能属于不同（甚至不可见）类型：行范围 trusted，但字段按
	// 各自类型读规则裁（ReadView —— 与单独取该节点同源）。
	expanded, err = maskNodesPtr(c, ReadView, expanded)
	fail(err)
	if many {
		return expanded
	}
	if len(expanded) == 0 {
		return (*core.Node)(nil)
	}
	return expanded[0]
}

// nodeIDs 节点切片 → id 列表。
func nodeIDs(nodes []core.Node) []int64 {
	ids := make([]int64, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	return ids
}

// targets 边 → 端点节点列表（保持边序; N+1 顶着, 页面量小毫秒级）。
// wantTo: 取 to_node（出边目标）; false 取 from_node（入边来源）。
// graph 模板函数桥: 查节点类型（模板场景只有 id）后转发图原语。
func (e *Render) graph(c *CmsCtx, start int64, field string, maxHops int, fn func(context.Context, string, int64, string, int) ([]int64, error)) []int64 {
	n, err := e.eng.GetNodeById(c.R.Context(), start)
	if err != nil || n == nil {
		fail(fmt.Errorf("graph: node %d not found", start))
		return nil
	}
	ids, err := fn(c.R.Context(), n.Type, start, field, maxHops)
	fail(err)
	return ids
}

func (e *Render) targets(c *CmsCtx, wantTo bool, q func() ([]core.Edge, int64, error)) []core.Node {
	edges, _, err := q()
	fail(err)
	nodes := make([]core.Node, 0, len(edges))
	for _, ed := range edges {
		id := ed.FromNode
		if wantTo {
			id = ed.ToNode
		}
		n, err := e.eng.GetNodeById(c.R.Context(), id)
		fail(err)
		if n == nil {
			continue
		}
		masked, err := MaskNode(c, ReadView, n)
		fail(err)
		nodes = append(nodes, *masked)
	}
	return nodes
}

// execute 单个模板文件独立解析执行 (无隐式布局 — 页面结构由模板自行
// 经 partial 引入)。
func (e *Render) execute(c *CmsCtx, w io.Writer, name, full string, data any) error {
	tpl, err := template.New(filepath.Base(full)).Funcs(e.funcMap(c)).ParseFiles(full)
	if err != nil {
		return fmt.Errorf("render: parse %s: %w", name, err)
	}
	return tpl.Execute(w, data)
}

// Partial 渲染片段模板 (独立解析执行; 不参与布局)。
// 无缓存, 每次渲染读当前文件 — 片段也热重载。
// 模板内请用 partial/partialOr 函数（自动带上当前请求 Context）。
func (e *Render) Partial(c *CmsCtx, name string, data any) (template.HTML, error) {
	return e.partial(c, name, data)
}

func (e *Render) partial(c *CmsCtx, name string, data any) (template.HTML, error) {
	clean := filepath.Clean(name)
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", fmt.Errorf("render: partial %q escapes templates root", name)
	}
	full := filepath.Join(e.root, clean)
	if _, err := os.Stat(full); err != nil {
		return "", errPartialNotFound
	}
	tpl, err := template.New(filepath.Base(full)).Funcs(e.funcMap(c)).ParseFiles(full)
	if err != nil {
		return "", fmt.Errorf("render: parse partial %s: %w", name, err)
	}
	var sb strings.Builder
	if err := tpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("render: partial %q: %w", name, err)
	}
	return template.HTML(sb.String()), nil
}

// errPartialNotFound 片段缺失哨兵: partialOr 据此回落兜底。
var errPartialNotFound = errors.New("render: partial not found")

// funcMap 内置函数 + sprig (安全子集) + 查询/自定义函数。
// sprig: HermeticHtmlFuncMap — 无 env/expandenv 等环境访问的安全子集
// (dict/default/trunc 等常用模板工具函数)。
// safeHTML: 受信富文本原样输出 (匿名提交内容禁用, XSS)。
func (e *Render) funcMap(c *CmsCtx) template.FuncMap {
	e.mu.RLock()
	defer e.mu.RUnlock()
	m := template.FuncMap{}
	maps.Copy(m, sprig.HermeticHtmlFuncMap())
	builtins := template.FuncMap{
		"safeHTML": func(v any) template.HTML { return template.HTML(fmt.Sprint(v)) },
		// img 图片裁剪 URL: 本地 /uploads/ 才拼 ?w=&h=&mode=&fmt=（CDN/外部 URL 原样返回）。
		// 单边 0 = 按比例; mode: cover(默认)/fit/crop; fmt: jpg/png（照片类 jpg 降体积）。
		// url 节点前台地址（addressable capability 优先，id 兜底）。
		"url": func(n *core.Node) string {
			if n == nil {
				return "#"
			}
			if address := e.eng.Types().Address(n.Type, n.Fields); address != "" {
				return "/node/" + address
			}
			return "/node/" + strconv.FormatInt(n.ID, 10)
		},
		"img": func(url string, w, h int, mode string, fmtArgs ...string) string {
			if !strings.HasPrefix(url, "/uploads/") && !strings.HasPrefix(url, "/static/") {
				return url
			}
			var b strings.Builder
			b.WriteString(url)
			b.WriteString("?")
			if w > 0 {
				b.WriteString(fmt.Sprintf("w=%d", w))
			}
			if h > 0 {
				if w > 0 {
					b.WriteString("&")
				}
				b.WriteString(fmt.Sprintf("h=%d", h))
			}
			if mode != "" {
				b.WriteString("&mode=" + mode)
			}
			if len(fmtArgs) > 0 && fmtArgs[0] != "" {
				b.WriteString("&fmt=" + fmtArgs[0])
			}
			return b.String()
		},
		"partial": func(name string, data any) (template.HTML, error) {
			return e.partial(c, name, data)
		},
		"partialOr": func(name, fallback string, data any) (template.HTML, error) {
			out, err := e.partial(c, name, data)
			if err != nil {
				if errors.Is(err, errPartialNotFound) {
					return e.partial(c, fallback, data)
				}
				return "", err // 解析/执行错误响亮上抛, 不吞进兜底
			}
			return out, nil
		},
		// HTML → 纯文本 (列表/首页缩略): 剥标签 + 块元素转换行
		"plainText": func(html string) string {
			if html == "" {
				return ""
			}
			text, err := html2text.FromString(html)
			if err != nil {
				return strings.TrimSpace(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(html, " "))
			}
			return strings.TrimSpace(text)
		},
	}
	// 内置覆盖 sprig 同名 (如有)
	maps.Copy(m, builtins)
	maps.Copy(m, e.queryFuncs(c)) // 查询原语按请求 Context 构造
	maps.Copy(m, e.funcs)
	return m
}
