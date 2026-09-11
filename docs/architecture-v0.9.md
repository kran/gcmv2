# gcmv2 v0.9：设计理念、核心概念、架构与实现状态

- 文档性质：v0.9 开发期架构总览与状态快照
- 更新时间：2026-09-10
- 适用范围：`types`、`query`、`core`、`web`、通用插件及 Site 组合方式
- 详细决策：参见 `docs/adr/001-004`

本文集中回答四个问题：gcmv2 是什么、核心名词是什么意思、系统如何分层运行、当前已经实现和仍未实现什么。

---

## 1. 产品定位

gcmv2 的定位是：

> **类型定义驱动的实体—关系应用内核（Schema-driven Entity & Relation Application Kernel）**

它提供一组可组合的通用原语：

```text
Schema / Node / Edge / Query / Policy / Actor / Auth Realm / Plugin / View
```

CMS、CRM、协会管理、项目管理和知识库不是 Core 内置产品，而是由站点使用这些原语组合出的上层应用。

因此，Core 不应写死以下业务概念：

```text
user / member / customer / contact / opportunity / article / author / role
```

例如：

- “会员”是 association 定义的 `member` NodeType，不是 Core 内置用户表。
- “文章已发布”是 publication capability 描述的业务状态，不是 Node 固定状态。
- “作者”是 association 的 ref 字段，不是通用 Web 层应理解的固定关系。
- “任职”具有职位、部门和有效期，应建模为关系 Node，而不是给 Edge 增加业务字段。

---

## 2. 设计理念

### 2.1 小内核，上层组合

Core 只负责所有业务都会复用的数据不变量和执行机制：

- Node 的身份与版本
- Schema 校验
- Edge 引用完整性
- Query AST 编译
- Policy Scope 合并
- Realm/Session 基础认证
- 事务、索引和迁移

业务词汇、页面流程、角色体系和状态机留给 Site。

### 2.2 Schema 是契约，不是表单提示

`types.yaml` 不只是后台表单配置。它决定：

- 字段类型和合法值
- 字段是否必填、默认、不可变
- ref/ref[] 的目标类型和基数
- 删除策略和关系代数
- 可搜索、可寻址、发布、认证、树、关系 Node 等能力
- 数据库唯一约束和索引

任何写入入口都不应绕过这些约束。Schema 错误或存量数据违反约束时，系统应 fail-loud，而不是静默修复或降级。

### 2.3 能力显式启用

NodeType 默认不具备发布、公开地址、全文检索、认证或树等语义。只有显式声明 capability 后，相关行为才成立。

这避免 Core 根据字段名或具体 Kind 名猜测业务，例如：

- 不因为存在 `status` 就认为它可发布。
- 不因为类型名叫 `user` 就认为它可登录。
- 不因为字段名叫 `parent` 就认为它是一棵树。
- 不因为 Kind 名看起来像数字就允许排序。

### 2.4 Node 与 Edge 分工明确

- **Node** 表示有身份、字段、生命周期和版本的实体。
- **Edge** 只表示没有业务属性的轻量引用。
- 有角色、状态、金额、时间、权限、附件或审计需求的关系，使用普通 **关系 Node**。

Edge 不承载任意 JSON，避免形成第二套弱约束实体系统。

### 2.5 一个查询模型，多种输入方式

所有结构化筛选统一进入封闭的 Query AST：

```text
Go Builder ────────┐
JSON QuerySpec ────┼─> AST -> Schema Validate -> Policy Merge -> SQL Compile
Lisp Parser ───────┘
```

Lisp 只是受限文本前端，不直接生成 SQL，也不是公网业务参数协议。Compiler 不接受 Raw SQL 扩展。

### 2.6 权限是不可伪造的查询范围

用户条件和权限条件不是同一个信任级别：

```text
最终条件 = 用户 Where AND 服务端 Policy Scope
```

`core.ListQuery` 必须显式携带：

- `core.PolicyScope(expr)`：服务端计算的强制范围；或
- `core.BypassPolicy()`：可信管理/后台任务显式旁路。

零值 Scope 直接报错，避免调用者忘记授权范围后意外返回全表。

### 2.7 认证身份与业务实体分离

- Realm 是服务端注册的认证入口名称。
- Realm 映射到一个启用 authentication capability 的 NodeType。
- Credential 插件只验证自己的凭据。
- Session 只保存 token hash、Realm 和 NodeID。
- Actor 表示请求身份来源；Principal 是 Actor 背后的业务 Node。

客户端不能提交内部 NodeType，也不能依赖名为 `user` 的固定模型。

### 2.8 默认保守，破坏性操作显式

- 公共 DELETE 默认归档。
- 永久删除只通过明确的管理/Core 操作执行。
- required ref 默认 `restrict`。
- 可选 ref 默认 `set_null`。
- `cascade` 只允许关系 Node endpoint。
- 不安全的直接 Merge 已删除；只有 Preview，没有隐式执行。

### 2.9 v0 期间不维护伪兼容

当前仍是 v0：

- 不保留旧 API 与新 API 双实现。
- 不保留隐式 fallback 或废弃字段读取。
- 设计确定后同步修改框架、站点和测试。
- 对真实数据库提供备份、迁移和完整性验证。
- 所有破坏性源码变化记录在 `CHANGELOG.md` 和迁移文档中。

---

## 3. 核心概念与名词

### 3.1 Node

所有业务实体的统一记录。当前固定系统列为：

```text
id / type / display / revision / fields / created_at / updated_at / archived_at
```

- `ID`：稳定身份，创建后不可修改。
- `Type`：Schema 类型名，创建后不可修改。
- `Display`：通用可读标签。
- `Revision`：乐观锁版本。
- `Fields`：只保存标量、对象和数组，不保存 ref/ref[]。
- `ArchivedAt`：非空表示已归档。

`slug/status/sort` 已不再是 Node 固定列。

### 3.2 TypeDef

一个 NodeType 的完整定义，分为四部分：

- `Fields`：字段定义。
- `Constraints`：唯一约束和索引。
- `Capabilities`：运行时能力。
- `Admin`：后台展示元数据。

展示配置不能改变数据约束或公开策略。

### 3.3 FieldDef 与 Kind

`FieldDef` 描述字段名称、Kind、required/default/immutable、引用目标和关系规则。

`Kind` 是字段值类型及能力声明。查询能力由 `QueryOps` 显式声明：

```go
type QueryOps struct {
    Equal    bool
    Ordered  bool
    Text     bool
    Sortable bool
}
```

Compiler 不根据 `number`、`timestamp`、`select` 等具体名字推断能力。

`array` / `object` 是复合字段的结构语法，不是注册 kind：形状由 `item` / `fields`
递归描述，值存在 `fields` JSON。复合结构内部**只允许标量 kind**：

```yaml
# 允许
- { name: tags, kind: array, item: { kind: text } }
- { name: meta, kind: object, fields: [ { name: og_title, kind: text } ] }

# 拒绝（Load 期报错）
- { name: members, kind: array, item: { kind: ref, to: person } }
```

原因是嵌套引用没有路径可落 Edge：若降级成标量存进 `fields` JSON，就只剩裸 ID，
没有外键、基数、删除策略，`CheckRelations` 也看不到。“数组/对象里带引用”的正确
建模是关系 Node。

### 3.4 Capability

Type 选择启用的通用行为：

- `searchable`：哪些字段进入全文索引。
- `addressable`：哪个 slug 字段提供全局地址。
- `publication`：哪个字段及哪些值表达草稿/发布。
- `authentication`：该 Type 是否可以成为认证 Principal。
- `tree`：parent 和可选 order 字段。
- `relation`：该 Type 是否是带属性的关系 Node，以及 from/to endpoint。

### 3.5 Edge

Edge 是 ref/ref[] 的物理存储：

```text
from_node / field / to_node / sort / single_ref / symmetric / created_at
```

其中 `single_ref` 和 `symmetric` 是数据库约束元数据，不是业务字段。

- `ref`：0..1；required 时为 1。
- `ref[]`：0..N；required 时至少为 1。
- `sort`：ref[] 内部顺序。

### 3.6 关系 Node

关系本身有属性时，使用普通 Node 表达。例如：

```text
employment(contact, account, title, department, start_at, end_at)
```

`relation.from/to` 指定两个 required 单 ref endpoint。关系 Node 仍通过通用 Node API、查询和后台管理，不使用第二套存储。

### 3.7 关系代数

- `symmetric`：对称关系；存一条 Edge，读取时双向解释。
- `transitive`：允许有向可达性遍历，不写入传递闭包。
- `equivalence`：无向、可传递的等价类关系。
- `tree.parent`：树的父引用，默认执行环检测。

三种字段代数互斥，并且只能用于 self-reference。

### 3.8 Query AST

Query AST 是查询的唯一结构化核心，包括：

- 逻辑：AND、OR、NOT。
- 标量：EQ、NE、GT、GTE、LT、LTE、Contains、Prefix。
- 集合：IN、OneOf、SubtreeOf。
- 关系：Exists、Related、出边路径、显式来源 Type 的入边路径。
- 排序、分页和 Typed Expand。

所有字段、路径、值、操作符和排序都经过当前 Type Schema 校验。

### 3.9 Expand

Expand 使用 typed path 加载关联 Node，并在每一跳切换 Schema 上下文。

- 出边由当前 Type 推导目标 Type。
- 入边必须显式声明来源 Type。
- symmetric/equivalence 自动恢复双向语义。
- 普通 Expand 不返回已归档目标。

### 3.10 EditableNode

`Node.Fields` 永远只表示实际持久化的非引用字段。

管理和授权场景需要完整编辑值时使用：

```go
type EditableNode struct {
    Node
    Values Fields // scalar + ref IDs
}
```

配套 API：`FullNode`、`FullNodes`、`RefID`、`RefIDs`、`HasRef`。

### 3.11 授权：按类型定义的读写事件

`QueryScope` 仍是 Core 读取 API 的可信边界（零值报错、只由 `PolicyScope`/`BypassPolicy` 构造、
在 AST 层与用户条件合并）。Web 层不再有独立的策略注册表：**授权就是一组按类型定义的事件**，
由 hook 总线承载（schema 加载后、站点注册前定义）：

```text
web.read.list|view|search|export.<type>     读规则：收窄行范围 + 声明不可见字段
web.write.create|update|delete.<type>       写规则：身份 + 可写字段 + 值加工
```

读规则的两个出参与写规则的 `allow` 对称：**行范围**回答“这个角色能看到哪些行”，
**字段掩码**回答“这个角色能看到哪些字段”。

```go
// 读规则：范围必须把过滤 AND 进 *expr（nil = 尚未收窄）。多个 handler 依次收窄（AND — 只会更窄）。
// 字段掩码把“看不到的字段”Append 进 *hide（不碰 = 全部可见）；多个 handler 取并集。
site.ReadRule(web.ReadView, "member", func(ctx *web.CmsCtx, _ string, expr *gquery.Expr, hide *core.List[string]) error {
	if !isVerifiedMember(ctx) {
		hide.Append("phone", "contact") // 只影响输出，不影响排序/筛选能力
	}
	*expr = gquery.And(*expr, gquery.EQ(gquery.Field("approval_state"), "approved"))
	return nil
})
```

```go
// 写规则：签名由 action 决定，不匹配在注册期就报错。
site.WriteRule(web.WriteCreate, "article", func(ctx *web.CmsCtx, node *core.Node, allow *core.List[string]) error {
	member, err := publishingMember(ctx)   // 身份/归属判断：返回错误即拒绝
	if err != nil {
		return err
	}
	allow.Append("title", "body", "cover") // 客户端可写字段（并集 — 多个 handler 各自声明）
	node.Fields["author"] = member.ID      // 客户端不可设置的值由规则就地加工
	node.Fields["publication_state"] = "draft"
	return nil
})
site.WriteRule(web.WriteUpdate, "article", func(*web.CmsCtx, int64, *core.NodePatch, *core.List[string]) error { ... })
site.WriteRule(web.WriteDelete, "article", func(*web.CmsCtx, int64) error { ... })   // 只有 err，无字段
```

**未注册 handler 时的行为**（两边都没有"默认放行"）：

```text
读: 系统读动作（list/view/search/export）→ publication 默认
      有 publication capability → 只读已发布记录
      没有该 capability         → 全拒（没有可发布的记录）
    站点读动作（如 my_content）  → 报错（拼错动作名不会静默回退到公开默认）
    注册了规则但 *expr 仍为 nil  → 报错（要"谁都看不到"就显式写 gquery.False()）
写: 拒绝（匿名 401 unauthorized / 已认证 403 forbidden）
```

**写路径的三个生效点**（POST / PUT / DELETE `/api/nodes/{type}[/{id}]`）：

```text
1) 准入    Has(web.write.<action>.<type>)? 没有 → 401/403
           （PUT 先于存在性检查：未注册写规则的类型不能当"这个 id 存不存在"的探测器）
2) 规则    Fire(...) → 返回错误即拒绝；create/update 收集 core.List[string] 作为白名单
           客户端提交的字段名在规则加工前快照，规则里的加工不影响判断
           allow 为空（一个字段都没声明）→ 403（等于没有授权这个动作）
           集合外的提交字段 → 422 invalid_value + details 指名
3) 写入    Core：Schema 校验 + 引用基数 + revision 乐观锁
```

职责划分：

```text
Schema / DB    写进去的行必须合法（取值、唯一、引用基数）—— 不知道 Actor
读写事件        谁能读（行范围）、谁能写（身份）、客户端能填哪些字段
值加工          客户端不可设置的值（author、publication_state、HTML 清洗）也在同一个规则里
```

**读写入口：ctx 层是策略层，engine 是可信层。**

站点的业务端点自己拼响应，框架看不到它的输出 —— 所以策略必须在**数据跨出进程之前**
由入口本身保证，而不是留给调用方补最后一步：

```go
// 读（解析读规则 → 填 Scope → 引擎 → 裁字段，一步到位）
page, total, err := ctx.ReadPage(web.ReadList, core.ListQuery{Type: "article", Page: ...})
node, err := ctx.ReadOne(web.ReadView, id)          // 不可见 → (nil, nil)
node, err := ctx.ReadAddress(web.ReadView, slug)
full, err := ctx.ReadFull(web.ReadView, id)         // 带 ref 值（编辑表单）
nodes, total, err := ctx.ReadSearch(query, typeName)
nodes, err := ctx.ReadTree(web.ReadList, "category")
page, total, err := ctx.ReadPage(actionMyContent, q) // 站点自定义读动作

// 写（"客户端发起的写"：Fire 写规则 → 引擎 → 返回裁剪过的节点）
node, err := ctx.CreateNode(&core.Node{...})
node, err := ctx.UpdateNode(id, &core.NodePatch{...})
err := ctx.DeleteNode(id)
```

两条硬规则：

- 行范围只能由规则算：`ReadPage`/`ReadOne`/... 收到非零 `QueryScope` 直接报错
  （`QueryScope.IsZero()`），不会静默覆盖成"更宽的范围"。
- `engine.Query / GetNodeById / FullNode / PatchNode / ...` 是显式的可信调用：后台、插件、
  迁移、"系统自己要看/要改"的代码走这里。**"客户端发起的写"没被入口覆盖时（多节点事务），
  自己 Fire 写规则再用引擎落库，而不是把身份判断在 handler 里手写一遍。**

字段白名单（`allow.Append`）约束的是"客户端提交了哪些字段"，那个集合只在框架自己解码时
存在（`apiCreateNode` 里加工前快照）。站点用自建 DTO 时 DTO 就是白名单；空 `allow` 仍视为
"没有授权这个动作"（403）。

组合语义（无单例限制，多个 handler 叠加）：读按 AND 收窄、字段掩码取并集；写按并集放宽 ——
后者是显式选择，代价归注册者（多加一条 `allow.Append` 就等于多放开一个字段）。

渲染层（HTML 模板 helper）与 JSON API 共用同一套读规则：`list` / `filterList` 用 `ReadList`，
`search` 对每个目标类型用 `ReadSearch`，`get` 用 `ReadView`（不可见即返回 nil）。
所以模板里没有"框架私有的一份 publication 规则"，站点注册的读规则在 HTML 与 API 上结果一致；
非 publication 类型也不再让模板 helper 直接 panic（未注册规则 = 全拒 = 空列表）。
**字段掩码同样在渲染层生效**（模板拿不到被隐藏的字段）。

解析只有一处：`CmsCtx.ReadRule(action, type)` —— 同一请求内按 (action, type) 只触发一次规则，
范围与掩码一起出来。缓存放在 `CmsCtx` 上：它的生命周期正好等于 Actor（`actor`/`principal`
也是缓存在这里的），换身份（`SetActor`）时作废，不跨请求复用、不并发共享。

应用只有一个原语：`web.MaskNode` / `MaskNodes` / `MaskTree`（拷贝后删键，递归进 `Expand`，
按子节点自己的类型解析规则）。框架的出口（`/api/nodes/*`、内置 `/node/{id}` 路由、模板 helper、
`/api/auth/login|me`）已经自动套用；**站点自建 JSON 出口要自己调一次**（association 的
`queryPage`/`content`）—— 后台与插件（`BypassPolicy` 路径）不做字段裁剪。
模板里的 `outRefs` / `inRefs` / `expand` 行范围仍直接使用 Core 原语、不做可见性过滤（边目标逐条
解析规则的收益不划算）；**字段掩码仍然按目标类型套用**（与 `get` 同源）。

框架自己的出口（通用 `/api/nodes/*`、内置 `/node/{id}` 路由、模板 helper、`/api/auth/login|me`）
都走上面这套入口；站点自建端点用 `ctx.Read*` / `ctx.Write*` 即可，不需要自己拼 Scope 或调
`MaskNode`。`MaskNode` / `MaskNodes` / `MaskTree` 仍然导出，留给"自己组装节点"的代码
（例如拿 `FullNode` 的 ref 值手工拼 Fields —— 那是可信层，自己负责裁）。

`core.CreateNode/PatchNode/DeleteNode` 是内核原语，不做写授权：后台管理路径与站点自建端点
都是受信调用方，自己负责校验（例如 association 的 `/api/me/profile` 手建 patch）。

### 3.12 Auth Realm、Actor 与 Principal

- **Auth Realm**：公开稳定的认证域名，例如 `member`。
- **Actor**：当前请求的身份元数据。
- **Principal**：Node Actor 对应的业务 Node。
- **AuthMethod**：由 credential 插件拥有的不透明凭据数据。
- **Session**：Realm-bound 会话，只持久化 token hash。

Actor 类型：

```text
Anonymous / Node / Admin / APIKey
```

### 3.13 Archive 与 Permanent Delete

- Archive：设置 `archived_at`，保留 Node 和 Edge，默认查询不可见。
- Restore：清除 `archived_at`，原引用恢复可见。
- Permanent Delete：执行所有入边的 `on_delete` 策略后物理删除。

归档认证 Node 会撤销其全部 Session。

### 3.14 Relation Integrity Report

`CheckRelations(ctx)` 是只读检查，不自动修复数据。它可以发现：

- 悬空 source/target
- 未声明引用字段
- target Type 不匹配
- 重复 Edge
- 单 ref 多边
- required ref 缺失
- required ref 指向已归档目标
- Edge 元数据与 Schema 不一致
- 无向边未规范化
- transitive/tree 环

### 3.15 错误契约

HTTP 边界的错误是结构化值，不是字符串：

```json
{"error":"人类可读信息","code":"invalid_value","details":{"title":"必填"}}
```

- `error`：面向人的信息（可以本地化、可以改写）。
- `code`：稳定机器标识（契约），客户端只能按 code 分支。
- `details`：可选字段级错误，表单回显用。

Code 与 HTTP 语义的固定映射：

```text
invalid_request    400  请求体/参数格式错误
unauthorized       401  未认证或凭据失效
forbidden          403  已认证但无权限
not_found          404  不存在或不可见
conflict           409  版本/唯一/归档状态冲突
delete_restricted  409  incoming 引用阻止永久删除
invalid_value      422  字段值不符合 Schema
invalid_query      422  查询字段/操作符/值未过 Schema 校验
query_too_complex  422  查询超出预算
upload_invalid     413/422  上传缺文件、类型/大小/内容非法
internal           500  未预期错误（细节只进日志）
unavailable        503  依赖不可用
```

三条例外：

- `CmsCtx.Error(status, message)` 保留 cho 的调用形态，但响应体自动带上由 status
  推导的 Code，不会出现“没有 Code”的响应。
- `CmsCtx.Fail(err)` 用于 API 出口：`*Error` 原样输出，core/types 已知错误映射到
  404/409/422，其他错误记日志后输出通用 500（内部细节不外泄）。
- `CmsCtx.Reject(err)` 用于权限/业务 Hook：`*Error` 原样输出，其他错误默认 403 +
  原始信息（站点文案随站点变，客户端改用 Code）。

站点 Hook 可以返回 `web.Forbidden(...)` / `web.Unauthorized(...)` /
`web.InvalidFields(...)` 精确表达语义；框架自己的信息保持简短英文，站点文案自行决定。

### 3.16 Merge Preview

`PreviewMerge` 只报告：

- source/target 完整编辑值
- 字段冲突和建议值
- 入边与出边
- 双方认证方式

它不修改数据。真正 Merge 必须等冲突决策、唯一约束、认证迁移、Session、审计和回滚契约完成。

---

## 4. 架构分层

### 4.1 包依赖

```text
Site / Application
    │
    ├── web ─────────────── HTTP、Actor、Realm、Policy、Admin
    │     │
    ├── plugins ─────────── password、sitemap、backup、oss 等适配器
    │     │
    └──── core ──────────── Node、Edge、Query 执行、Auth 存储、Search、迁移
           │  │
           │  └── query ─── AST、Builder、Lisp Parser、QuerySpec、ExpandPath
           │
           └──── types ──── Schema、Kind、Capability、值校验
```

依赖方向应保持向下：Core 不导入具体 Site 业务，Types 不理解 Web 或 CMS 模板。

### 4.2 数据模型

```text
nodes
  ├── fields JSON：标量/对象/数组
  └── revision + archived_at：并发和生命周期

edges
  ├── from_node + field + to_node：引用
  ├── sort：ref[] 顺序
  └── single_ref + symmetric：数据库约束元数据

auth_methods
  └── NodeType + NodeID + method + identifier + opaque data

sessions
  └── token_hash + realm + node_id + expiry

nodes_fts
  └── active + searchable Node 的搜索投影

settings / accounts
  └──站点设置与独立后台管理员凭据
```

### 4.3 写入路径

```text
Create/Patch request
  -> Web 权限 Hook
  -> Schema 校验
  -> 标量与引用拆分
  -> Node 写入
  -> Edge 类型/基数/代数校验
  -> 数据库约束兜底
  -> Hook / Search 同步
  -> 同一事务提交
```

引用写入统一检查：

- 字段属于 source Type。
- 字段是 ref/ref[]。
- target 存在、未归档且 Type 匹配。
- ref[] 无重复并保存数组顺序。
- symmetric/equivalence 端点规范化。
- transitive/tree 写入不形成环。

### 4.4 查询路径

```text
Builder / Lisp / QuerySpec
  -> Query AST
  -> Schema-aware validation
  -> mandatory QueryScope merge
  -> SQL compiler
  -> stable sort + pagination
  -> optional Typed Expand
```

FTS 只负责检索候选和相关性，不负责公开可见性。草稿可以存在于索引中，但 Policy 会在查询时过滤。

### 4.5 认证路径

```text
/api/auth/{realm}/...
  -> server-side Realm lookup
  -> credential plugin verification
  -> AuthMethod lookup
  -> hashed Session
  -> request Actor
  -> lazy Principal loading
```

密码哈希只由 password 插件解释；Core 将 credential data 视为不透明数据。

### 4.6 生命周期与运维探针

```text
web.Open(basedir) -> (*Site, error)   站点初始化（db/types/迁移/元数据同步）
web.New(basedir)  -> *Site             相等语义, 失败 panic（便捷入口）
core.Open(db, ts) -> (*Service, error) 引擎初始化（内置迁移 + Edge 元数据 + Schema 索引）
core.New(db, ts)  -> *Service          失败 panic

Site.Close() -> error                  幂等关闭站点并释放连接池

GET /healthz 进程存活（不碰数据库）
GET /readyz  可接客（Ping 连接池; Close 后 503）
```

进程入口用 Open 记录错误后退出；测试与嵌入场景用 Open 拿到错误而不是崩溃。

### 4.7 删除路径

```text
Public DELETE -> ArchiveNode

Admin/Core permanent delete
  -> 收集 incoming refs
  -> restrict: 返回结构化阻塞错误
  -> cascade: 递归删除关系 Node，带循环保护
  -> set_null: 删除 Edge，递增存活 source revision
  -> 删除目标的全部 Edge、Node、Auth、Session、Search 投影
  -> 整体事务提交或回滚
```

---

## 5. 已实现状态

### 5.1 ADR-001：Node、Schema、Capability

已实现：

- Node 移除固定 `slug/status/sort`。
- 增加 `revision` 和 `archived_at`。
- TypeDef 分离 Fields、Constraints、Capabilities、Admin。
- searchable/addressable/publication/authentication/tree/relation capability。
- 字段默认值和 immutable。
- 标量唯一约束和表达式/partial indexes。
- Kind 自描述 QueryOps。
- 创建和更新乐观锁。
- Core migration `00009_node_schema_capabilities.sql`。

### 5.2 ADR-002：统一 Query AST

已实现：

- 封闭 AST 和 Go Builder。
- Lisp-to-AST Parser。
- 严格 JSON QuerySpec。
- Schema-aware 字段、操作符、值、关系和排序校验。
- Typed Expand 和显式来源 Type 的入边。
- Related、SubtreeOf 和稳定分页排序。
- 删除旧 Lisp-to-SQL Compiler、Raw SQL 扩展和字符串 Expand API。

### 5.3 ADR-003：Auth Realm 与 Actor

已实现：

- 服务端 Realm 注册与 NodeType 映射。
- 客户端认证 DTO 不再接收 Type。
- password 插件绑定指定 Realm。
- Anonymous/Node/Admin/APIKey Actor 表达。
- Principal 惰性加载。
- Realm-bound Session。
- Session token SHA-256 后持久化。
- 跨 Realm bind 拒绝。
- 归档认证 Node 时撤销 Session。
- Core migration `00010_auth_realms.sql`。

### 5.4 Query Policy 与 Search

已实现：

- `ListQuery` 必须显式使用 PolicyScope 或 BypassPolicy。
- `total` 是截断计数（`CountLimit`）：精确计数要扫过整个匹配集（10 万行约 17ms、
  百万行约 170ms），而列表页本身不到 1ms。默认上限 10 万以下精确、
  超过即饱和（列表 10000 / 检索 1000），需要精确总量的调用方显式传 `core.CountExact`。
- 地址查询走 `addressable` capability 的单类型地址索引（`(type, address)`）；
  全局 CASE 唯一索引只负责跨类型唯一性，查询无法把它当点查用。
- Web 读授权支持 list/view/search/export 四个读动作。
- 公共列表、详情、搜索和 sitemap 应用服务端范围。
- SearchQuery 为每个 Type 使用独立 Scope。
- FTS 索引与 publication 可见性解耦。
- Admin 旁路必须显式声明。

### 5.5 ADR-004：关系完整性

已实现：

- ref/ref[] 基数及数据库并发约束。
- ref[] 重复拒绝和顺序保存。
- symmetric/equivalence canonical storage。
- `OutEdges`、`InEdges`、Query、Expand 和 Ref API 的无向语义。
- `InEdges` 字段过滤。
- transitive/tree 环检测与遍历能力检查。
- `on_delete` 默认值和 restrict/set_null/cascade。
- cascade 仅用于 relation capability endpoint。
- Archive/Restore 与永久删除分离。
- 已归档 target 不可新增引用；普通 Query/Expand 不返回它。
- EditableNode 和 Typed Ref API。
- 只读 Relation Integrity Report。
- 只读 Merge Preview；旧危险 Merge 已删除。
- Core migration `00011_edge_integrity.sql`。

### 5.6 association 参考站点

已完成：

- `member` Auth Realm。
- 公开内容 Policy Scope。
- association 查询迁移到 Go Query Builder。
- 编辑和鉴权改用 `FullNode(...).Values`。
- 站点 SQL migration 后调用 `SyncRelationSchema()`。
- 实际数据库已升级至 core v11、site v3。
- `PRAGMA integrity_check` 为 `ok`，`foreign_key_check` 无异常，关系完整性问题为 0。
- 迁移前备份：`backups/pre-v09-relation-integrity-20260910-091443.db`。

---

## 6. 部分实现

以下能力已有基础，但契约尚未完整：

### 6.1 Context

已贯穿全部数据库与外部 I/O 入口，没有“带/不带 Context”的双轨：

- 写：CreateNode / PatchNode / Archive / Restore / DeleteNode / AddEdge / RemoveEdge
- 读：Query / QueryPage / GetNodeById / GetNodeByAddress / LoadTree
- 图：Traverse / Subtree / Ancestors / EquivalenceClass / OutEdges / InEdges
- 关系：RefID / RefIDs / HasRef / FullNode / FullNodes / CheckRelations / PreviewMerge
- 检索：Search / SearchIndex.Rebuild / RebuildSearch
- 认证：RegisterAuth / FindAuth / AddMethod / RemoveMethod / Session 全套
- 配置：Setting 全套；迁移：Migrator.Up / UpDir
- 渲染：Render / 模板查询函数（模板内 `partial` 自动继承请求 Context）

唯一保留的不带 Context 入口是 `Render.Partial`（站点程序式调用），它内部用 Background；
模板请使用 `partial`/`partialOr` 函数。

### 6.2 Policy

读取侧行级 Policy 已完成，错误语义已有稳定 Code。以下仍依赖 Web Hook 或尚未统一：

- Create/Update/Delete/Transition 权限模型（当前仅“无 Hook = 默认拒绝”）
- 字段级读取和写入白名单
- 上传授权的统一 Policy 表达
- 嵌套 typed Set 子查询中的递归 Type Policy

### 6.3 Schema 约束

已实现字段校验、默认值、immutable 和标量唯一/索引；尚未实现：

- 条件必填
- 跨字段校验
- 引用参与的组合唯一约束
- Import/Batch 与普通写入的统一验证入口

### 6.4 关系 Node 后台体验

relation capability 已可声明，关系 Node 可通过普通 Node API 管理；尚未实现：

- 实体详情中的关系 Tab
- from/to 两侧的内联列表
- 内联新建和编辑
- 关系字段专用冲突提示

### 6.5 Archive 管理体验

Core 与 Admin API 已支持 archive/restore，但当前通用后台列表默认排除已归档记录，前端也没有回收站、归档筛选和恢复按钮。现阶段只能在知道 Node ID 时通过管理 API 恢复。

### 6.6 Merge

已实现 Preview，但没有执行 API。仍需：

- 每个字段的 source/target 选择
- 关系 Node 唯一冲突处理
- auth_methods 迁移规则
- Session 撤销规则
- required/on_delete 再校验
- 审计记录
- 全事务回滚测试

---

## 7. 尚未实现

### 7.1 核心能力

- 通用 typed Set 子查询：`SelectNodes`、`SelectValues`、`IN (subquery)`。
- AggregateQuery：count/sum/avg/min/max、分组和日期桶。
- Workflow 状态机和 Transition API。
- append-only 审计日志与字段 before/after。
- Outbox/Webhook 和持久任务。
- API Key 的签发、哈希存储、撤销和过期管理；当前只有 Actor 适配入口。
- 游标分页（页码分页的稳定排序已完成）。
- 通用 Import/Export、批处理及幂等键。
- Schema hash 与“Schema 未变则跳过重建”的搜索索引刷新策略。

### 7.2 已清理的边界泄漏

以下项目曾是内核泄漏，现已处理：

- 通用 `/api/nodes/mine` 已删除，改由 association 的 `GET /api/me/content` 实现。
- select options 校验下沉到 `selectKind.Validate(f, v)`，容器不再按 Kind 名特判。
- `TypeDef.TemplateCandidates` 已删除，模板候选由 `web` 独占。
- `LoadTree(ctx, type, scope)`：Core 只读 tree capability，发布可见范围由调用方 Scope 决定。
- 复合字段（array/object）内部禁止 ref/ref[]，嵌套 Kind 与字段约束改为 Load 期校验。

### 7.3 明确不在 Core 内实现

- 固定 CRM 实体模型。
- 固定角色名称和组织结构。
- 微信、邮箱、短信等具体凭据语义。
- 给 Edge 增加任意业务 JSON。
- 让公网客户端提交 Raw SQL 或无限制 Lisp。
- 在 v0 同时维护旧、新两套 API。

---

## 8. 安全与正确性不变量

实现和评审时必须持续满足：

1. 客户端不能决定认证 NodeType。
2. 客户端不能构造 QueryScope 或绕过 Policy。
3. 公网 API 不接收 Raw SQL、任意排序字符串或未限制查询。
4. ref/ref[] 只存在 Edge，不复制进 `Node.Fields`。
5. 新 Edge 不能指向不存在、类型错误或已归档 Node。
6. 单 ref 的最终基数由数据库约束兜底。
7. symmetric/equivalence 只存一条 canonical Edge。
8. 永久删除必须执行 on_delete；公共删除只归档。
9. 归档认证 Node 必须使其 Session 失效。
10. 所有列表、详情、搜索和导出必须具有显式 Scope。
11. 管理旁路必须写出 `BypassPolicy()`，不能依赖空值。
12. 数据完整性修复必须显式执行，检查器不得静默改数据。
13. 任何请求体都有硬上限（`maxBodyBytes` 8MB，`CmsCtxMaker` 里兜住，插件路由也覆盖）。
14. JSON 解码额外收紧到 `maxJSONBytes` 1MB，并由 `BindStrictJSON` 统一映射成 413/400。
15. SQLite 连接档位必须满足 `journal_mode=wal`、`busy_timeout>0`、`foreign_keys=1`；
    不满足时 `core.Open` 拒绝启动（`verifySQLiteProfile`）。
16. HTML 渲染与 JSON API 必须共用同一套读授权（模板 helper 不得自带一份可见性规则）。
17. 渲染失败必须返回 500 并写日志；不得以 200 + 注释的形式藏起来。
18. 字段掩码只由读规则声明（不在 types.yaml 里定义等级），只影响输出、不改变行集；
    业务端点的读必须走 `CmsCtx.Read*`（直接调引擎属于显式可信路径，自己负责）。
19. 读规则解析结果只能缓存在 `CmsCtx` 上（生命周期 = Actor）。禁止缓存到 `Site`/`Engine`/
    `BaseContext`：跨请求复用会把一个角色的字段掩码给另一个角色。
20. `CmsCtx.Read*` 不接受调用方自带的 `QueryScope`（非零即报错）；行范围只能来自读规则。
21. 业务端点里"客户端发起的写"走 `CmsCtx.CreateNode/UpdateNode/DeleteNode`；直接调
    `engine.PatchNode` 等属于系统写（或者必须自己 Fire 规则），不能两者都不做。

---

## 9. 迁移和运行要求

数据库连接档位（`web.Open` 已按此拼 DSN；自己开库的站点必须照抄）：

```text
_pragma=foreign_keys(1)      引用完整性/级联删除依赖
_pragma=journal_mode(WAL)    读者不阻塞写者、写者不阻塞读者（非 WAL 下并发读写直接撞锁）
_pragma=busy_timeout(5000)   撞锁等待而不是立即 SQLITE_BUSY（dba 不做重试）
```

`core.Open` 启动时校验这三项，不满足直接报错（fail-loud），避免在并发下静默退化。

WAL 的运维含义：数据库旁边会多出 `gcm.sqlite-wal` / `gcm.sqlite-shm`；
在线备份继续用 `VACUUM INTO`（一致快照）；**离线恢复时必须在停服状态下同时删除旧库的
`-wal` / `-shm`**，否则旧日志会被重放到新库上。

请求体上限：`maxBodyBytes` 8MB（所有路由，`CmsCtxMaker`），`maxJSONBytes` 1MB（JSON 解码，
`BindStrictJSON` → 413 `invalid_request`）。上传沿用 8MB（`saveUpload` 自带 `MaxBytesReader`）。

v0.9 当前包含三项核心迁移：

```text
00009_node_schema_capabilities.sql
00010_auth_realms.sql
00011_edge_integrity.sql
```

升级顺序：

1. 备份数据库。
2. 使用新 `types.yaml` 启动并应用 Core migration。
3. 执行 Site migration，搬迁旧字段或插入业务数据。
4. 若 Site migration 直接写入 Edge，调用 `SyncRelationSchema()`。
5. 重建 Search Index。
6. 执行 `CheckRelations()`。
7. 执行 `PRAGMA integrity_check` 和 `PRAGMA foreign_key_check`。
8. 验证后才删除旧备份或旧迁移中间表。

任何单 ref 多边、反向重复无向边或 Schema 不匹配都应阻止正常启动/验收，不应自动猜测修复方式。

---

## 10. 下一步实施顺序

建议按以下顺序继续，避免在不稳定内核上堆业务功能：

1. [x] 清理通用 Web/Types 中的业务和展示边界泄漏。
2. [x] 将 Context 贯穿剩余数据库与外部 I/O API。
3. [x] 生命周期：Open/Close、healthz/readyz、DB 与 Migrator Context。
4. [x] 结构化错误码（Status/Code/Message、Hook 结构化错误）。
5. [ ] 统一写操作授权（Create/Update/Delete 的 Policy 与字段白名单）。
6. [ ] 设计递归 Policy 的 typed Set 子查询。
7. [ ] 建立审计模型，再开放可执行 Merge。
8. [ ] 增加关系 Node 组合唯一约束和后台关系视图。
9. [ ] 实现 Workflow、Aggregate、Import/Export 等应用能力。
10. [ ] 用独立非 CMS 示例验证，而不是把 CRM 名词写入 Core。

---

## 11. 相关文档

- `docs/adr/001-node-schema-capabilities.md`
- `docs/adr/002-query-ast.md`
- `docs/adr/003-auth-realm-actor.md`
- `docs/adr/004-relations.md`
- `docs/core-review-v0.9.md`
- `docs/migration-v0.9.md`
- `ROADMAP.md`
- `CHANGELOG.md`
