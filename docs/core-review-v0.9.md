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

### P1：关系代数声明没有在所有入口执行

位置：`core/traverse.go`、`core/edge.go`

当前：

- Traverse/Subtree 只验证“是 ref”，不要求 `transitive`。
- EquivalenceClass 没有验证字段归属和 `equivalence`。
- AddEdge 没有完整执行 ref/ref[] 基数和 symmetric 规范化。
- checkTarget 允许新 Edge 指向已归档 Node。
- Merge 没有预览、类型约束、Hook、Search/Auth/Audit 一致性。

目标：ADR-004。

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

- 公共搜索由 Web PolicyRegistry 添加 publication scope。
- Admin 搜索显式使用 `core.BypassPolicy()`。
- 每个 Type 的 Scope 都重新执行 Schema 校验。
- 相关性排序和分页在 Policy 过滤之后执行。

FTS 现在只负责检索，不决定可见性。

### P2：Tree 原语与“公开树”耦合

位置：`core/tree.go`

`LoadTree` 当前强制要求 publication capability，并只加载 published Node。CRM 的组织树、区域树可能完全没有公开发布语义。

同时 `web/admin.go` 的 inbound subtree 仍写死字段名 `parent`。

建议：

```text
Core LoadTree(ctx, type, where)
Public Web 追加 publication scope
Admin 使用 nil/admin scope
```

Tree Parent/Order 始终从 tree capability 读取。

### P2：通用 Web API 写死 author 业务模型

位置：`web/api.go` 的 `/api/nodes/mine`

当前通用 Web 层查找名为 `author` 的引用字段。这是内容投稿模型，不属于实体关系内核。CRM 可能使用 owner、assignee、created_by，或关系 Node。

建议：从通用 web 删除 mine endpoint，由 association 等站点业务 API 实现，未来由 Actor/Policy 提供 owner scope。

### P2：Kind 值校验仍有具体 Kind 分支

位置：`types/types.go`

当前 `Types.ValidateValue` 对 select 使用：

```go
if field.Kind == KindSelect { ... }
```

array/object 的递归校验也由 Types 容器特殊处理。

select 是可以消除的泄漏：Kind 的值校验接口应接收 FieldDef，使 options 校验由 selectKind 自己完成。

array/object 是否注册成正式 Kind 需要单独决定。它们需要递归访问 Types，不能只为了消除两个 switch 就引入循环或复杂接口。

### P2：Types 包保留未使用的模板命名规则

位置：`types.TypeDef.TemplateCandidates`

`node--{type}.html` 是 Web/CMS 展示约定，不属于 Schema。该方法当前没有生产调用方，应直接删除，模板候选由 web 包负责。

### P2：Context 只覆盖新 Query

当前 Query/QueryPage 已接收 Context，但以下路径仍不可由请求取消：

- Search/RebuildSearch
- Traverse/Subtree/Ancestors
- Auth/Session 数据库操作
- Settings

建议逐步给可能执行数据库或外部 IO 的入口增加 Context；不要同时保留有/无 Context 两套长期 API。

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
3. [ ] 实现 ADR-004 的基数、删除策略、关系代数和 Merge
4. [ ] 清理 mine endpoint、TemplateCandidates、select 特判等边界问题
5. [ ] 继续 Context 贯穿
```
