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

### 3.11 QueryScope 与 Policy

`QueryScope` 是 Core 读取 API 的可信边界。Web `PolicyRegistry` 按以下键解析范围：

```text
Actor + Action + Type
```

当前 Action：

```text
list / view / search / export
```

未注册公开规则时：有 publication capability 的 Type 默认只读已发布记录；没有 publication capability 的 Type 默认拒绝全部记录。

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

### 3.15 Merge Preview

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

### 4.6 删除路径

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
- Web PolicyRegistry 支持 list/view/search/export。
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

已覆盖：

- Query/QueryPage
- Search
- Expand/ExpandMany
- RefID/RefIDs/HasRef/FullNode/FullNodes
- Archive/Restore
- CheckRelations/PreviewMerge

尚未贯穿：

- Create/Patch/Permanent Delete
- Traverse/Subtree/Ancestors/LoadTree
- Auth/Session
- Settings
- Migrator/RebuildSearch

v0 不应长期保留有 Context 和无 Context 两套平行 API；后续直接完成迁移。

### 6.2 Policy

读取侧行级 Policy 已完成。以下仍依赖 Web Hook 或尚未统一：

- Create/Update/Delete/Transition 权限模型
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
- 通用 Import/Export、批处理及幂等键。
- Schema hash 和按需索引重建。
- 稳定的结构化 API Error Code 契约。
- `Site.Close()`、返回 error 的 Open、healthz/readyz。

### 7.2 已知边界泄漏

- 通用 `/api/nodes/mine` 仍写死 `author` 字段，应移到 association。
- `Types.ValidateValue` 仍直接处理 select options。
- `TypeDef.TemplateCandidates` 仍包含 Web 模板命名规则。
- `LoadTree` 仍同时承担 publication 过滤，不适合非公开 CRM 树。

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

---

## 9. 迁移和运行要求

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

1. 清理通用 Web/Types 中的业务和展示边界泄漏。
2. 将 Context 贯穿剩余数据库与外部 I/O API。
3. 设计递归 Policy 的 typed Set 子查询。
4. 建立结构化错误码和统一写操作授权。
5. 建立审计模型，再开放可执行 Merge。
6. 增加关系 Node 组合唯一约束和后台关系视图。
7. 实现 Workflow、Aggregate、Import/Export 等应用能力。
8. 最后用独立 CRM 示例验证，而不是把 CRM 名词写入 Core。

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
