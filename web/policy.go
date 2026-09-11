package web

import (
	"fmt"
	"sort"

	"github.com/kran/gcmv2/core"
	gquery "github.com/kran/gcmv2/query"
)

// 授权在 web 层就是一组按类型定义的事件：
//
//	web.read.<action>.<type>    读规则：收窄行范围（收窄 = AND 组合 — 只会更窄）
//	web.write.<action>.<type>   写规则：判断身份 + 声明客户端可写字段 + 加工值
//
// 框架在 schema 加载后为每个类型定义这七个事件（definePolicyEvents），站点用
// Site.ReadRule / Site.WriteRule 注册 handler。没有 handler 时：
//
//	读 → 系统读动作走 publication 默认（没有 publication capability 则全拒）
//	写 → 拒绝（匿名 401 unauthorized / 已认证 403 forbidden）
//
// 事件名是总线上的普通事件名：写错名字或签名不匹配在注册期（Start 之前）就报错。

// ReadAction 系统读动作。站点可以定义自己的读动作（任意非空名字），
// 例如 association 的 "my_content"：同一个类型对不同入口需要不同的行范围。
type ReadAction string

const (
	ReadList   ReadAction = "list"
	ReadView   ReadAction = "view"
	ReadSearch ReadAction = "search"
	ReadExport ReadAction = "export"
)

// WriteAction 系统写动作。写动作由框架拥有（对应三个公开写端点），站点不能自定义。
type WriteAction string

const (
	WriteCreate WriteAction = "create"
	WriteUpdate WriteAction = "update"
	WriteDelete WriteAction = "delete"
)

// systemReadActions 决定“没有 handler 时”的行为：系统读动作走 publication 默认，
// 站点读动作未注册则报错（不静默回退到公开默认）。
var systemReadActions = map[ReadAction]struct{}{
	ReadList:   {},
	ReadView:   {},
	ReadSearch: {},
	ReadExport: {},
}

var systemWriteActions = map[WriteAction]struct{}{
	WriteCreate: {},
	WriteUpdate: {},
	WriteDelete: {},
}

// ReadRule 描述一次读取对这个角色开放什么：行范围（*expr）+ 字段可见性（*hide）。
//
// 行范围：必须把过滤 AND 进 *expr（nil = 尚未收窄）：
//
//	*expr = gquery.And(*expr, gquery.EQ(gquery.Field("author"), member.ID))
//
// 字段可见性：把“这个角色看不到的字段”Append 进 *hide（不碰 = 全部字段可见）。
// 只影响输出，不影响行范围/排序/筛选能力。名字必须是该类型声明的顶层字段，
// 拼错会在解析时报错（该藏的没藏 = 泄漏，不能静默放行）。
//
//	if !verified(ctx) { hide.Append("phone", "contact") }
//
// 多个 handler 依次收窄（范围 AND 组合；hide 取并集）。返回错误即拒绝这次读取
// （可直接返回 *Error）。Fire 之后 *expr 仍为 nil 视为配置错误（要“谁都看不到”
// 就显式写 gquery.False()）。
type ReadRule func(*CmsCtx, string, *gquery.Expr, *core.List[string]) error

// ReadEvent 读事件名：web.read.<action>.<type>。
func ReadEvent(action ReadAction, typeName string) string {
	if action == "" || typeName == "" {
		panic("web: read action and type are required")
	}
	return "web.read." + string(action) + "." + typeName
}

// WriteEvent 写事件名：web.write.<action>.<type>。
func WriteEvent(action WriteAction, typeName string) string {
	if action == "" || typeName == "" {
		panic("web: write action and type are required")
	}
	return "web.write." + string(action) + "." + typeName
}

// definePolicyEvents 为每个类型定义读/写授权事件（schema 加载后、站点注册前）。
func (s *Site) definePolicyEvents() {
	hooks := s.engine.Hooks()
	readProto := func(*CmsCtx, string, *gquery.Expr, *core.List[string]) error { return nil }
	writeProtos := map[WriteAction]any{
		WriteCreate: func(*CmsCtx, *core.Node, *core.List[string]) error { return nil },
		WriteUpdate: func(*CmsCtx, int64, *core.NodePatch, *core.List[string]) error { return nil },
		WriteDelete: func(*CmsCtx, int64) error { return nil },
	}
	for _, typeName := range s.engine.Types().Names() {
		for action := range systemReadActions {
			if err := hooks.DefineHook(ReadEvent(action, typeName), readProto); err != nil {
				panic("web: define read events: " + err.Error())
			}
		}
		for action, proto := range writeProtos {
			if err := hooks.DefineHook(WriteEvent(action, typeName), proto); err != nil {
				panic("web: define write events: " + err.Error())
			}
		}
	}
}

// ReadRule 注册一个类型的读规则。系统读动作的事件由框架预定义（注册即覆盖默认行为）；
// 站点自定义读动作在首次注册时定义事件。
func (s *Site) ReadRule(action ReadAction, typeName string, rule ReadRule) {
	if s.started {
		panic("web: register read rule after Start")
	}
	if rule == nil {
		panic("web: nil read rule")
	}
	if _, ok := s.engine.Types().Type(typeName); !ok {
		panic(fmt.Sprintf("web: policy type %q not defined", typeName))
	}
	event := ReadEvent(action, typeName)
	s.defineEvent(event, func(*CmsCtx, string, *gquery.Expr, *core.List[string]) error { return nil })
	s.Hook(event, rule)
}

// WriteRule 注册一个类型的写规则。签名由 action 决定，不匹配在注册期报错：
//
//	create: func(*CmsCtx, *core.Node, *core.List[string]) error
//	update: func(*CmsCtx, int64, *core.NodePatch, *core.List[string]) error
//	delete: func(*CmsCtx, int64) error
//
// 规则负责三件事：身份/归属判断（返回错误即拒绝）、allow.Append 声明客户端可写字段、
// 就地加工客户端不可设置的值（如 author、publication_state）。
// create/update 若一个字段都没声明，等于没有授权这个动作（403）。
//
// allow 的强制力取决于"谁解码请求体"，两种模式别混：
//
//	框架解码（POST/PUT /api/nodes/{type}）→ 硬拦：客户端提交了未放行的字段 → 422 + details
//	                          （提交字段名在规则加工前快照，规则自己加的字段不参与判断）
//	站点自建 DTO（CmsCtx.CreateNode/UpdateNode、自建端点）→ 只表示"这个动作被授权"
//	                          （+ 当字段文档）；字段范围由 DTO 决定，schema 仍拒未声明字段。
//	                          规则想收窄就在规则里删 node.Fields 里没放行的键（它有指针）。
func (s *Site) WriteRule(action WriteAction, typeName string, rule any) {
	if s.started {
		panic("web: register write rule after Start")
	}
	if _, ok := systemWriteActions[action]; !ok {
		panic(fmt.Sprintf("web: unknown write action %q", action))
	}
	if _, ok := s.engine.Types().Type(typeName); !ok {
		panic(fmt.Sprintf("web: policy type %q not defined", typeName))
	}
	s.Hook(WriteEvent(action, typeName), rule)
}

// defineEvent 定义事件（已定义则忽略 — 系统动作由框架预定义）。
func (s *Site) defineEvent(name string, proto any) {
	if s.engine.Hooks().Defined(name) {
		return
	}
	if err := s.engine.Hooks().DefineHook(name, proto); err != nil {
		panic("web: define policy event: " + err.Error())
	}
}

// readRule 一次读规则的解析结果（范围 + 不可见字段 + 错误）。
type readRule struct {
	scope core.QueryScope
	hide  []string
	err   error
}

// ReadRule 解析 (action, type) 的读规则：行范围 + 不可见字段。
//
// 同一请求内按 (action, type) 只触发一次规则 —— 结果是当前 Actor 的函数，
// 而 CmsCtx 与 Actor 同生命周期（actor/principal 也是缓存在这里的）。
// 因此换身份（SetActor）时必须清空：CmsCtx 不并发安全，也不跨请求复用。
func (c *CmsCtx) ReadRule(action ReadAction, typeName string) (core.QueryScope, []string, error) {
	key := string(action) + "\x00" + typeName
	if got, ok := c.readRules[key]; ok {
		return got.scope, got.hide, got.err
	}
	scope, hide, err := resolveReadRule(c.site.engine, c, action, typeName)
	if c.readRules == nil {
		c.readRules = make(map[string]readRule, 4)
	}
	c.readRules[key] = readRule{scope: scope, hide: hide, err: err}
	return scope, hide, err
}

// ReadScope 解析一次读取的行范围（字段掩码见 CmsCtx.ReadRule / MaskNode）：
// 注册了规则就用规则；否则系统读动作走 publication 默认，站点读动作未注册则报错
// （拼错动作名不会静默回退到公开默认）。
func (s *Site) ReadScope(ctx *CmsCtx, action ReadAction, typeName string) (core.QueryScope, error) {
	scope, _, err := ctx.ReadRule(action, typeName)
	return scope, err
}

// resolveReadRule 读规则的一次真实解析（模板层、API 层、站点 handler 共用）。
func resolveReadRule(eng core.Engine, ctx *CmsCtx, action ReadAction, typeName string) (core.QueryScope, []string, error) {
	if _, ok := eng.Types().Type(typeName); !ok {
		return core.QueryScope{}, nil, fmt.Errorf("web: policy type %q not defined", typeName)
	}
	event := ReadEvent(action, typeName)
	if eng.Hooks().Has(event) {
		var expr gquery.Expr
		var hide core.List[string]
		if err := eng.Hooks().Fire(event, ctx, typeName, &expr, &hide); err != nil {
			return core.QueryScope{}, nil, err
		}
		if expr == nil {
			return core.QueryScope{}, nil, fmt.Errorf("web: read rule %s produced no scope", event)
		}
		names := hide.Items()
		for _, name := range names {
			if _, ok := eng.Types().Field(typeName, name); !ok {
				return core.QueryScope{}, nil,
					fmt.Errorf("web: read rule %s hides undeclared field %q", event, name)
			}
		}
		return core.PolicyScope(expr), names, nil
	}
	if _, ok := systemReadActions[action]; !ok {
		return core.QueryScope{}, nil, fmt.Errorf("web: no read rule registered for action %q on type %q", action, typeName)
	}
	return defaultScope(eng, typeName), nil, nil
}

// Exposes 该类型的这个读动作是否被站点显式注册了规则。公开路由与 sitemap 用它
// 判断没有 publication capability 的类型是否被站点主动暴露。
func (s *Site) Exposes(typeName string, action ReadAction) bool {
	return s.engine.Hooks().Has(ReadEvent(action, typeName))
}

// defaultScope 系统读动作未注册规则时的行为：有 publication capability 的只读已发布
// 记录；没有该 capability 的类型没有可发布记录，全拒。
func defaultScope(eng core.Engine, typeName string) core.QueryScope {
	publication, ok := eng.Types().Publication(typeName)
	if !ok {
		return core.PolicyScope(gquery.False())
	}
	return core.PolicyScope(gquery.EQ(gquery.Field(publication.Field), publication.Published))
}

// deniedWrite 未注册写规则的回复：匿名 401，已认证 403。
func deniedWrite(ctx *CmsCtx, action WriteAction, typeName string) *Error {
	message := fmt.Sprintf("%s is not allowed for type %q", action, typeName)
	if ctx.Actor().Kind == ActorAnonymous {
		return Unauthorized("%s", message)
	}
	return Forbidden("%s", message)
}

// fieldNames 客户端提交的字段名（写规则加工前快照 — 白名单只约束客户端，不约束规则）。
func fieldNames(fields map[string]any) []string {
	if len(fields) == 0 {
		return nil
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// rejectFields 返回不在 allowed 里的提交字段，键为字段名，可直接作为 422 details。
func rejectFields(submitted []string, allowed []string) map[string]string {
	if len(submitted) == 0 {
		return nil
	}
	permitted := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		permitted[name] = struct{}{}
	}
	var rejected map[string]string
	for _, name := range submitted {
		if _, ok := permitted[name]; ok {
			continue
		}
		if rejected == nil {
			rejected = make(map[string]string, len(submitted))
		}
		rejected[name] = "field is not client-writable"
	}
	return rejected
}
