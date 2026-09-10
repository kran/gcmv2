# gcmv2 Core Review（v0.9 开发期）

- 日期：2026-09-09
- 范围：`types`、`core`、通用 `web`、认证/搜索插件边界
- 原则：识别业务语义、具体 Kind、公开策略或 UI 假设向底层泄漏的位置；不为旧 API 保留兼容层。

## 本轮已修正

Query Compiler 曾按具体 Kind 名判断：

```go
kind == "array" || kind == "object" || kind == "gallery"
kind == "number" || kind == "timestamp"
```

现改为每个 Kind 显式实现：

```go
type QueryOps struct {
    Equal    bool
    Ordered  bool
    Text     bool
    Sortable bool
}
```

`Kind.QueryOps()` 现在共同驱动：

- eq/ne/in
- gt/gte/lt/lte
- contains/prefix
- sortable
- searchable 字段校验

自定义 Kind 必须明确声明能力，没有名称推断或隐式回退。

同时修正 Type capability 中两处同类判断：

- publication 字段改为要求 `Equal` 并用 Kind 校验 draft/published 值，不再写死 select。
- tree.order 改为要求 `Sortable`，不再写死 number。
- tree.parent 按 `ClassRef` 单引用判断，不再写死 ref 名称。

addressable 仍要求 `slug` Kind，这是刻意的安全约束：公开路径段必须执行统一字符白名单，而不是普通 Query 能力。

## 发现的问题

### 已修复：Expand 逐层 Schema 上下文

原 `expandBatch` 会遍历所有 Type，按第一个同名字段猜测 ref/ref[] 形态。现已替换为：

- `query.ExpandPath` 使用与 Query 相同的 typed `query.Path`。
- 每一跳根据当前 Type 验证字段并推导下一 Type。
- 入边必须显式写 `query.Incoming(sourceType, field)`；文本前端使用 `<-type.field`。
- 混合根类型按 Type 分组，各自决定单值/多值形态。
- 目标 Node 类型不匹配时 fail-loud，不静默忽略损坏 Edge。
- Expand/ExpandMany 接收 Context。
- 旧字符串 Engine API 已删除；字符串只保留为 Admin/模板 Parser 前端。

### 已解决：关系代数与引用完整性进入统一写路径

位置：`core/traverse.go`、`core/edge.go`

ADR-004 核心约束已实现：

- Traverse/Subtree 只接受 transitive 或 tree.parent 字段，并限制深度、拒绝新环。
- EquivalenceClass 验证字段归属和 equivalence 声明。
- AddEdge/Create/Patch 统一执行 ref/ref[] 基数、目标类型、归档目标和 symmetric 规范化。
- `on_delete` 支持 restrict/set_null/cascade；cascade 只允许关系 Node endpoint。
- 公共删除改为归档，永久删除执行引用策略。
- 新增 EditableNode/Ref API 和只读关系完整性报告。
- 旧的直接执行 Merge 已删除，新增 Merge Preview；执行合并留待字段决策和审计完成。

### 已解决：认证核心中的 password/user 假设

位置：

- `plugin/password/password.go`
- `core/auth.go`
- `web/auth.go`

ADR-003 已完成：

- password 插件必须绑定服务端注册的 Realm，不存在 `"user"` fallback。
- Register/Login DTO 已删除 Type，并使用严格 JSON 解码拒绝客户端传入 NodeType。
- bcrypt 和 `data["password"]` 仅存在于 password credential 插件；core 只存不透明 Data。
- `CmsCtx.User` 和 `RequireRole` 已删除，改为 `Actor` 与 `Principal`。
- 前台 Session、Admin 和 API Key 适配入口统一为 Actor。
- Session 绑定 Realm，数据库只保存 Token hash。

后续 Policy 只依赖 Actor，不再读取固定业务角色字段。

### 已解决：Searchable 与 Publication 索引层解耦

位置：`core/search.go` 的 `shouldIndex`

现在所有 active + searchable Node 都进入 FTS，publication 状态变化不再删除或重新加入索引。

`SearchQuery` 为每个目标 Type 携带独立的 Where 和 QueryScope：

- 公共搜索由 Web 读授权（未注册规则时 publication 默认）添加 scope。
- Admin 搜索显式使用 `core.BypassPolicy()`。
- 每个 Type 的 Scope 都重新执行 Schema 校验。
- 相关性排序和分页在 Policy 过滤之后执行。

FTS 现在只负责检索，不决定可见性。

### 已解决：Tree 原语与“公开树”解耦

位置：`core/tree.go`

`LoadTree` 不再要求 publication capability，也不再自行拼接发布条件：

```go
LoadTree(ctx, typeName, scope)
```

- Core 只读 tree capability 的 parent/order。
- 公开 Web 路由传入 Policy Scope；association 公开树和分类筛选都经 `web.PolicyList`。
- 非公开树（CRM 组织/区域）用 `core.BypassPolicy()` 加载。
- 零值 Scope 直接报错，与列表/搜索一致。
- 树节点查询改走统一查询构建器，parent 边按来源 Type 过滤。

`web/admin.go` 的 inbound subtree 改为读取 tree capability 的 parent 字段，不再写死 `parent`。

### 已解决：mine endpoint 移出通用 Web

位置：`web/api.go` 的 `/api/nodes/mine`

通用 Web 层不再查找名为 `author` 的引用字段。该端点已删除，由 association 的 `GET /api/me/content` 实现，只覆盖 `memberContentTypes`，因此“我的发布”不再依赖内核猜测业务字段。

### 已解决：Kind 值校验不再有具体 Kind 分支

位置：`types/types.go`

`Kind.Validate` 现在接收 FieldDef：

```go
Validate(f FieldDef, v any) error
```

select 的 options 校验由 `selectKind` 自己完成，`ValidateValue` 不再按 Kind 名特判。

array/object 仍保留为结构语法（不进 kinds 注册表），但容器只负责形状递归；叶子 kind 的存在性、字段约束和引用限制都在 Load 期校验（复合结构内禁止 ref/ref[]）。

### 已解决：Types 包不再包含模板命名规则

`TypeDef.TemplateCandidates` 已删除。模板候选（含 address 级联）由 `web.nodeCandidates` 独占，Schema 不认识展示约定。

### 已解决：Context 贯穿全部 I/O 入口

除 `Render.Partial`（站点程序式调用，内部 Background）外，所有数据库与外部 I/O 入口
都接收 Context，且没有保留旧的无 Context 重载：

- 写入：CreateNode / PatchNode / Archive / Restore / DeleteNode / AddEdge / RemoveEdge
- 读取与图：Query / GetNodeById / GetNodeByAddress / LoadTree / Traverse / Subtree / Ancestors /
  EquivalenceClass / OutEdges / InEdges
- 认证与配置：RegisterAuth / FindAuth / AddAuthMethod / RemoveAuthMethod / Session 全套 / Settings 全套
- 迁移与检索：Migrator.Up / UpDir / SearchIndex.Rebuild
- 渲染：Render 与模板查询函数（模板内 partial 自动继承请求 Context）

`TestWriteAndGraphHonorCanceledContext` 覆盖写路径、图原语、树加载、搜索重建和单节点读取的取消行为。

### 已解决：生命周期入口

`web.Open` / `core.Open` 返回初始化错误（`New` 保留 panic 便捷入口），`Site.Close()`
幂等释放连接池，`/healthz` 与 `/readyz` 提供存活与就绪探针。

## 属于合理边界、不是泄漏

以下内容暂不视为问题：

- Query Compiler 知道 `nodes`、`fields JSON` 和 `edges`：这是存储编译器职责。
- Core 知道 Node 系统列：这些列由 core 自己拥有。
- Edge 的 `sort`：它表示 ref[] 内的关系顺序，不是旧 Node.Sort。
- addressable 要求 slug Kind：这是公开路径安全不变量。
- TypeDef 中保留 Admin 分组：已经与 Schema/Capability 分组，core 数据校验不读取 Admin。

## 建议顺序

```text
1. [x] 实现 ADR-003 Auth Realm / Actor，删除 user/password/role 假设
2. [x] 实现查询 Policy Scope，并解除 Searchable/Publication 索引耦合
3. [x] 实现 ADR-004 核心基数、删除策略、关系代数、完整性检查和 Merge Preview
4. [x] 清理 mine endpoint、TemplateCandidates、select 特判、LoadTree/publication 耦合
5. [ ] 继续 Context 贯穿
```
