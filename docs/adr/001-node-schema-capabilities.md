# ADR-001：Node、Schema 与 Capability 的边界

- 状态：Accepted / Implemented
- 目标版本：v0.9.0
- 日期：2026-09-09

## 背景

gcm 当前使用一张 `nodes` 表承载所有实体：

```text
id / type / display / slug / status / sort / fields / created_at / updated_at
```

这个结构很适合文章、页面、分类等 CMS 数据，但其中三个通用列实际带有明显业务假设：

- `slug`：假设实体需要公开 URL
- `status`：在 core/web/search/tree 中被解释为草稿和发布
- `sort`：假设实体需要人工排序

CRM 中的 account、contact、lead、opportunity、task 并不天然拥有这些语义。继续把这些列当作所有实体的固定能力，会造成：

- core 强制携带 CMS publication 逻辑
- Search、Tree、Public API 隐式依赖 status=1
- CRM 的业务 stage 容易与 publication status 混用
- 所有实体都暴露没有意义的 slug 和 sort
- Schema、Web 能力和 Admin 展示配置混在 TypeDef 中

## 决策

### 1. Node 只保留真正通用的系统字段

v0.9 的目标 Node：

```go
type Node struct {
    ID         int64
    Type       string
    Display    string
    Revision   int64
    Fields     Fields
    CreatedAt  time.Time
    UpdatedAt  time.Time
    ArchivedAt *time.Time

    Expand map[string]any
    Extra  map[string]any
}
```

语义：

- `ID`：数据库身份，创建后不可变
- `Type`：Schema 身份，创建后不可变
- `Display`：所有实体统一的可读标签
- `Revision`：乐观锁版本，每次有效更新递增
- `Fields`：类型定义中的业务值；不包含 ref/ref[]
- `ArchivedAt`：通用软删除元数据
- `Expand/Extra`：只读装配数据，不持久化

### 2. slug/status/sort 不再是通用 Node 语义

它们改为普通 Schema 字段或 Capability 配置。

示例：

```yaml
types:
  article:
    fields:
      - { name: slug, kind: slug }
      - { name: publication_state, kind: select, options: [draft, published] }
      - { name: position, kind: number }
```

这样：

- CRM contact 不需要 slug
- opportunity 使用 `stage`，不使用 published
- task 使用 `state`
- category 可以声明 `position`
- article 可以声明 publication capability

数据库中旧列的处理属于一次性数据迁移，不在新 API 中保留双语义。

### 3. Schema、Capability、View 分组但不拆成复杂层级

保持单个类型配置入口，但明确三部分：

```yaml
types:
  article:
    fields: []
    constraints: {}
    capabilities:
      searchable: {}
      publication: {}
      addressable: {}
    admin:
      view: list
      icon: Document
      columns: [display, publication_state, updated_at]
```

Go 层建议：

```go
type TypeDef struct {
    Name         string
    Fields       []FieldDef
    Constraints  Constraints
    Capabilities Capabilities
    Admin        AdminView
}
```

这里的分组是为了阻止语义混杂，不要求建立多个相互转发的 service package。

## Schema 负责什么

Schema 只描述数据及其约束：

- 字段名和 Kind
- required
- default
- immutable
- ref 目标和基数
- 唯一约束
- 索引
- 删除策略
- 跨字段 Validator 标识

Schema 不负责：

- 当前请求者是否有权限
- 哪些记录公开
- 页面布局细节
- 状态转换权限
- 外部认证实现

## Capability 负责什么

Capability 表示某类型选择启用的通用行为。

### searchable

```yaml
capabilities:
  searchable:
    fields: [display, name, body]
```

不再隐式要求 `status=published`。哪些记录可搜索由 Policy/publication scope 决定。

### addressable

```yaml
capabilities:
  addressable:
    field: slug
    unique: global
```

只有启用该能力的类型才拥有 slug 路由。

### publication

```yaml
capabilities:
  publication:
    field: publication_state
    draft: draft
    published: published
```

公开 Web、Sitemap 和公开搜索只作用于启用 publication 的类型。

### authentication

```yaml
capabilities:
  authentication: true
```

表示该类型可以成为认证主体，但认证路由仍由 Auth Realm 配置。

### tree

```yaml
capabilities:
  tree:
    parent: parent
    order: position
```

Tree 不再硬编码 `parent`、`sort` 和 `status=1`。

### workflow

Workflow 在后续 ADR 单独设计，Capability 这里只保存入口配置。

## Admin View 负责什么

Admin View 只影响后台展示：

- list/tree/kanban/calendar
- 图标
- 默认列
- 默认排序
- 表单分组
- 详情页 Tab

删除 Admin View 配置不能影响数据校验和业务 API。

## 字段和约束

### 默认值

默认值只在 Create 缺失字段时应用，Patch 不自动补默认值。

### immutable

immutable 字段：

- Create 可写
- Patch 出现该字段即拒绝
- 管理员若需修改，使用显式维护操作，不隐式绕过

### required

- Create 后的完整记录必须满足 required
- Patch 删除 required 字段必须失败
- Patch 修改其他字段时，不要求客户端重复提交 required 字段

### 唯一约束

支持单字段和组合约束：

```yaml
constraints:
  unique:
    - [external_id]
    - [account, phone]
```

标量字段优先编译为 SQLite partial/expression unique index。包含 ref 的组合唯一约束需要专门设计和事务校验，不能假装普通 JSON index 已经覆盖。

### 索引

```yaml
constraints:
  indexes:
    - [stage, owner]
    - [next_follow_up_at]
```

Schema loader 负责验证，Schema migrator 负责创建物理索引。

## 写入流程

所有写入入口必须调用同一个流程：

```text
Resolve Type
→ Apply Create Defaults（仅创建）
→ Reject Unknown Fields
→ Validate Field Values
→ Validate Required/Immutable
→ Validate Cross-field Rules
→ Resolve/Validate Refs
→ Validate Unique Constraints
→ Begin Transaction
→ Policy/Before Hook
→ Write Node + Edges
→ Increment Revision
→ Audit + Search + Outbox
→ Commit
```

Admin、Public API、Import、Batch 不允许各自绕过其中一部分。

## 数据库存储选择

继续采用：

```text
nodes.fields JSON + edges
```

但在 v0.9 实现前必须完成真实基准。

优先方案：

- 标量筛选：SQLite JSON expression index
- type 内唯一：partial unique index
- ref 查询：edges 索引
- 金额：整数最小单位或规范化 decimal，不使用 float64

暂不采用：

- 通用 EAV values 表
- 每个 Type 一张物理表
- 同一个 ref 同时存 Fields 和 Edge

如果 100 万 Node 基准证明现有方案不可接受，再重新决策。

## 数据迁移原则

当前是 v0，不保留旧 Go API，但必须保护真实数据。

迁移步骤建议：

1. 新 Schema 中为需要的类型显式声明 slug/publication/position 字段。
2. 站点迁移把旧 `nodes.slug/status/sort` 写入新字段。
3. 新代码只读取新字段。
4. 完成校验后重建 nodes 表，删除旧列。
5. 重建索引和搜索。
6. 不保留同时读取旧列和新字段的长期兼容分支。

## 影响

### 正面

- core 不再绑定 CMS 发布模型
- CRM 状态与内容发布状态不冲突
- 类型只携带实际需要的能力
- Tree/Search/Web 行为由显式配置决定
- 约束可以成为数据库和应用共同保证的不变量

### 代价

- Node、NodePatch、后台 UI 和现有站点需要一次性修改
- 需要数据迁移
- 动态 JSON 字段索引需要 Schema migrator
- v0.9 是明确的破坏性版本

## 不采用的方案

### 保留 status 并让 CRM 自行忽略

拒绝。Search、Tree、Web 已经依赖 status，继续保留会让 CMS 语义持续扩散。

### 将所有字段都改成数据库列

拒绝。会失去动态类型的主要价值，并把 Schema 变更变成大量表迁移。

### 增加通用 EAV values 表

暂不采用。查询、类型转换和维护复杂度高，先验证 SQLite JSON expression index。

## 实施结果

v0.9 开发分支已经完成：

- Node 固定列删除 slug/status/sort，增加 revision/archived_at。
- TypeDef 改为 Fields/Constraints/Capabilities/Admin 四个明确分组。
- 实现 searchable/addressable/publication/authentication/tree capability。
- 实现字段 default 和 immutable。
- 实现标量单字段/组合 unique 与普通 expression/partial index。
- 实现全局 address 唯一索引和 `GetNodeByAddress`。
- Patch 使用 Revision 执行乐观锁，冲突返回 `ErrRevisionConflict`。
- association 提供一次性旧列迁移，并区分 publication_state 与 member.approval_state。
- Admin 表单和列表不再硬编码 slug/status/sort。

基准环境：Apple M1 Pro、SQLite、100,000 个 opportunity、stage+amount 两字段筛选、20 次查询：

```text
JSON composite expression index   1.77 ms/op
EAV indexed self-join            71.15 ms/op
```

这是当前工作负载的工程证据，不宣称适用于所有数据库和所有查询形态。基准代码位于 `core/schema_index_bench_test.go`。

## 验收条件

- [x] Node 新通用字段集合确定。
- [x] publication/addressable/tree/searchable capability 语义确定。
- [x] TypeDef 的 Schema/Capability/Admin 配置边界确定。
- [x] 当前 Create/Patch 写入入口使用统一 Schema 校验。
- [x] 唯一约束和索引物理实现有原型和基准。
- [x] association 数据迁移方案可执行且可回滚。
- [x] ADR 已审核通过并完成首轮实现。
