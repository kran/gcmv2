# gcmv2 Roadmap

> 基线：v0.8.4；当前开发目标：v0.9.0
>
> 本文不是功能愿望清单，而是 gcm 从原型走向稳定“类型 + 关系”应用内核的工程路线。

## 当前判断

gcm 目前证明了以下想法可行：

- 用类型定义驱动通用实体管理
- 用 Node + Edge 表达内容和关系
- 自动生成后台表单、列表和树视图
- 在同一个小型 Go 应用中组合认证、搜索、上传和插件

但它的核心还处于早期阶段。目前更准确的描述是：

> 一个已经能支撑小型 CMS 的实体关系原型，而不是成熟的 CRM/业务应用框架。

主要问题不是“缺少 CRM 页面”，而是底层仍缺少稳定的数据不变量、查询边界、权限模型、认证抽象、审计和并发控制。如果直接增加看板、报表、客户和商机模板，只会把不稳定的核心放大。

## 当前阶段的兼容原则

- 当前仍是 v0，优先建立正确、简单的核心，不背负尚未稳定的旧 API。
- 不为了源码兼容保留双实现、适配层、弃用字段、隐式默认值或旧行为开关。
- 设计确定后直接修改框架、现有站点和测试；破坏性变化用简短迁移说明记录。
- 数据库中的用户数据需要安全迁移，但“数据迁移”不等于“永久保留旧代码路径”。
- 每次只保留一个权威实现，避免新旧 API 长期并存。

---

# 0. 设计文档

ADR 状态与实现进度：

- [x] [ADR-001：Node、Schema 与 Capability 的边界](docs/adr/001-node-schema-capabilities.md) — Accepted / Implemented
- [x] [ADR-002：统一 Query AST，Lisp 作为文本前端](docs/adr/002-query-ast.md) — Accepted / Core Implemented
- [x] [ADR-003：Auth Realm 与统一 Actor](docs/adr/003-auth-realm-actor.md) — Accepted / Core Implemented
- [x] [ADR-004：Edge、关系 Node 与引用完整性](docs/adr/004-relations.md)
- [x] [v0.9 Core 边界复审](docs/core-review-v0.9.md)

> `[x]` 表示文档已完成；是否接受和实现以每条后的状态及 ADR 正文为准。

---

# 1. 目标定位

## 1.1 建议定位

```text
Schema-driven Entity & Relation Application Kernel
类型定义驱动的实体—关系应用内核
```

gcm 提供通用原语：

```text
Schema       类型、字段、约束和能力
Entity       通用实体记录
Relation     简单引用和关系实体
Query        查询表达式、排序、分页和聚合
Policy       行级、操作级和字段级权限
Actor        认证后的行为主体
Workflow     状态和合法转换
History      审计、版本和业务时间线
View         后台展示元数据
Plugin       外部认证、文件、通知等适配器
```

CMS、CRM、协会管理、项目管理和知识库应由项目定义业务类型，不写死在框架中。

## 1.2 不应内置的业务模型

- [ ] 不内置固定的 user、customer、contact、lead、opportunity、contract。
- [ ] 不把“发布文章”当成所有实体的默认生命周期。
- [ ] 不把某一家微信、短信、邮件、支付实现写进 core。
- [ ] 不用运行时脚本代替明确的 Go Hook 和错误处理。
- [ ] 不引入 repository/service/controller 多层包装。
- [ ] 不让公网客户端直接决定内部 NodeType、SQL 排序或任意查询表达式。

## 1.3 默认部署边界

当前更适合采用：

```text
一个 Site / 一个 SQLite 数据库 / 一个业务租户
```

HostMux 可以托管多个 Site，但每个 Site 仍然独立数据库。

- [ ] v1 前不引入共享数据库 tenant_id，除非出现明确的真实需求。
- [ ] 多租户优先使用“每租户一库”，保持隔离和备份简单。

---

# 2. 先确定的核心不变量

以下内容必须先写成 ADR 或设计说明。没有这些约束，不应继续扩展 CRM 功能。

## 2.1 Entity 不变量

- [x] Node.ID 创建后不可修改。
- [x] Node.Type 创建后不可修改。
- [x] Node.Display 是所有实体唯一必备的可读标签。
- [x] scalar/object/array 字段只存 Fields JSON。
- [x] ref/ref[] 只存 Edge，不同时在 Fields 中保留副本。
- [ ] Create、Patch 已统一 Schema 校验；Import、Batch 尚未实现。
- [ ] 所有写操作在单事务中完成实体、引用、索引、审计和 outbox 写入。

## 2.2 通用列语义重新评审

当前 Node 固定包含：

```text
id / type / display / slug / status / sort / fields / timestamps
```

其中 `status = draft/published` 明显带有 CMS 语义。CRM 中的客户、联系人、任务和商机并不存在统一的“发布”概念。

需要做出明确设计：

- [x] `display` 保留为通用实体标签。
- [x] `slug` 改为类型可选的 addressable capability 字段。
- [x] `sort` 改为类型自己的 position 等字段，不再作为默认排序。
- [x] `status` 已从 Node 删除，不再默认解释为 draft/published。
- [x] 发布能力下沉为 publication capability。
- [x] CRM/认证状态使用自己的字段；association 会员使用 approval_state。
- [x] association 已提供旧列的一次性数据迁移。

建议方向：升级工具把旧列数据迁入显式字段，随后新运行时代码停止读取并删除旧列；不同时维护两套状态语义。只有声明 `publishable` 的类型才使用发布策略。

## 2.3 删除和引用不变量

当前 DeleteNode 会删除所有入边和出边，然后物理删除 Node。CRM 中这可能让联系人、商机或合同悄悄失去必填关系。

- [ ] FieldDef 支持 `on_delete: restrict|set_null|cascade`。
- [ ] required ref 默认使用 restrict。
- [ ] 删除前返回明确的入边阻塞信息。
- [ ] 普通业务默认软删除/归档，不直接物理删除。
- [ ] 永久删除只允许明确的管理操作。
- [ ] 删除、归档和恢复全部进入审计。

## 2.4 关系不变量

- [ ] Edge 只表示没有业务属性的轻量关系。
- [ ] Edge 必须遵守字段归属、目标类型、基数和删除策略。
- [ ] 带角色、状态、时间、金额、备注的关系使用关系 Node。
- [ ] 不给 Edge 增加任意 JSON，避免产生第二套 EAV。

示例：

```text
contact -> account                 简单 ref
employment(contact, account, role, start_at, end_at)  关系 Node
```

## 2.5 Actor 不变量

- [x] Core 不假定存在名为 user 的类型。
- [x] Session 指向一个 auth-enabled Node，并绑定服务端 Realm。
- [x] Web 层将 Session、Admin 解析成 Actor，并提供 API Key Actor 适配入口。
- [ ] Policy 只依赖 Actor 能力，不依赖固定业务类型名。
- [x] 客户端永远不能直接决定内部认证 NodeType。

## 2.6 Query 不变量

- [ ] 所有查询最终进入同一种 AST。
- [ ] 所有字段、关系、操作符和排序必须经过 Schema 校验。
- [ ] Policy 条件与用户条件在 AST 层合并，用户不能覆盖 Policy。
- [ ] 公网 Query 必须有复杂度、分页和执行时间限制。
- [ ] 文本 Lisp 只是 AST 的一种输入格式，不是核心数据结构。

---

# 3. 当前已确认的问题

## 3.1 安全与正确性

- [x] 公共 `/api/nodes/{type}` 默认只返回已发布记录。
- [x] Sort 改为结构化 `[]SortField`，不再接收原始 SQL。
- [x] View/Update/Delete 统一验证 URL type 与 Node.Type。
- [x] Patch 对标量、select、timestamp、ref 和 required 删除执行校验。
- [x] `/api/upload` 默认要求前台登录。
- [x] `/admin/upload` 已移入管理员认证组。
- [x] Admin logout 同时清除服务端 session key。
- [x] Admin Session 增加服务端过期时间。
- [x] Cookie Secure 属性可由 Site 配置。
- [x] AddAuthMethod 校验 Node 存在、NodeType 和 auth capability。
- [x] bind 的 bcrypt 错误不再忽略，且绑定类型由 Session Node 决定。
- [x] ExpandPathMany 按输入 ID 顺序返回并按 ID 回填。
- [x] types.yaml 使用 KnownFields，未知属性直接启动失败。

## 3.2 核心一致性

- [x] Create 和 Patch 已统一字段类型校验；Patch 单独定义 null/required 语义。
- [ ] 普通 Node.Fields 不包含 ref，但缺少明确的 RefID/RefIDs API，容易误用。
- [ ] 直接 SQL seed 不会触发搜索同步。
- [x] TypeDef 标量 unique/index 和全局 address 索引在启动时按 Schema 同步。
- [ ] 业务 Web Hook 与 Core Hook 的作用范围容易混淆。
- [ ] Engine 接口逐渐变大，但尚未明确哪些能力属于稳定公共 API。
- [ ] DB 查询普遍不接收 request context，取消和超时无法贯穿。

## 3.3 CMS 假设泄漏

- [x] status 已从 Node 通用列删除，公开范围由 publication capability 决定。
- [ ] Web 默认挂载首页、Node 页面和公开内容 API。
- [ ] public/admin/auth/render 被 Site 一次性装配，能力边界不够可选。
- [x] TypeDef 已分为 Fields/Constraints/Capabilities/Admin。
- [x] 删除无效 `title:` 配置，并通过严格 YAML 解析拒绝再次出现。

## 3.4 已修复但尚未发布

- [x] Admin 同一路由不同 params 使用不同 router-view key。
- [x] 类型树从 category 切换到 region/organization 时页面实例会重建。
- [x] 页面标题同时匹配 route name 和 params。
- [x] 增加对应管理 UI 回归测试。

---

# 4. Query Engine 2.0

## 4.1 对现有 Lisp 的结论

现有 Lisp 查询器有价值，不应删除。它已经证明可以自然表达：

```lisp
(and
  (= $stage "qualified")
  (>= $amount 100000)
  (in ->owner [12 18])
  (in ->industry (subtree "manufacturing")))
```

它适合：

- 服务端模板
- 管理后台高级筛选
- 保存视图的文本表示
- 开发调试
- 站点内部规则

但它不适合作为普通小程序或公网客户端的主要协议。association 已经实际暴露出以下问题：

- 前端必须理解 `$field`、`->ref` 和 `<-type.field`
- 前端容易拼出错误表达式
- 公共 API 没有完整暴露参数绑定
- 字符串拼接难以安全组合 Policy
- sort 独立于 Lisp，仍然是原始 SQL 面
- 难以生成稳定的 TypeScript 类型

因此正确方向是：

> 保留 Lisp Parser，但将 Lisp 降级为统一 Query AST 的一种文本前端。

## 4.2 目标分层

```text
业务查询参数 ───────┐
JSON QuerySpec ─────┼─> Filter AST -> Schema Validator -> Policy Merge -> SQL Compiler
Go Query Builder ───┤
Lisp Parser ────────┘
```

核心不再接收必须手工拼接的字符串。

## 4.3 Filter AST

- [x] 从私有 `lispExpr` 提炼独立 `query` 包的 Filter AST。
- [x] Parser 只负责 Lisp 文本到 AST。
- [x] Compiler 只负责已校验 AST 到参数化 SQL。
- [x] AST 节点不可携带原始 SQL。
- [x] AST 可以由 Go Builder 安全组合。
- [x] Builder 复制集合输入，Policy 可通过新建 And 节点组合而不修改用户条件。

概念示例，最终 API 另行评审：

```go
query.And(
    query.EQ(query.Field("stage"), "qualified"),
    query.GTE(query.Field("amount"), 100000),
    query.OneOf(query.Ref("owner"), ownerIDs...),
)
```

## 4.4 Schema-aware 校验

编译 SQL 前必须知道当前 Type：

- [x] `$field` 必须存在于当前 TypeDef。
- [x] `->field` 必须是 ref/ref[]。
- [x] `<-type.field` 必须存在且目标类型匹配。
- [x] 大小比较由 Kind.QueryOps().Ordered 决定。
- [x] contains/prefix 由 Kind.QueryOps().Text 决定。
- [x] 查询值复用 Kind 校验，select 值必须属于 options。
- [x] sort 字段必须存在且 Kind 声明 Sortable。
- [ ] 错误已有稳定分类，但文本表达式位置尚未加入错误对象。

内部迁移工具如需宽松查询，应使用显式 Unsafe API，不能让默认编译器静默放行。

## 4.5 公网 QuerySpec

通用管理客户端需要复杂查询时，使用结构化 JSON，而不是 Lisp QueryString：

```json
{
  "where": {
    "op": "and",
    "args": [
      {"op": "eq", "field": "stage", "value": "qualified"},
      {"op": "in", "ref": "owner", "values": [12, 18]},
      {"op": "gte", "field": "amount", "value": 100000}
    ]
  },
  "sort": [
    {"column": "updated_at", "desc": true}
  ],
  "page": {"number": 1, "size": 20}
}
```

- [x] 增加严格 JSON QuerySpec 到 AST 的解析器。
- [x] 管理端复杂查询使用受认证的 POST `/admin/query/{type}`。
- [x] QuerySpec 字段、操作符和排序全部走 Schema 白名单。
- [x] 普通业务客户端继续使用简单业务参数。
- [x] 完整 Query API 当前只向管理员开放。

## 4.6 公网业务 API 原则

普通小程序不应理解 gcm 查询语言：

```text
GET /articles?category_id=10
GET /opportunities?owner_id=12&stage=qualified
```

业务 Handler 使用 Go Builder 生成 AST：

```text
简单参数 -> 服务端校验 -> AST -> Policy -> SQL
```

Lisp 不再作为公共业务契约。

## 4.7 查询操作符补齐

- [x] `is-null` / `not-null`（Builder: IsNull/IsNotNull）。
- [x] `exists` / `missing`。
- [ ] `between`。
- [x] `contains` / `prefix`，自动转义 SQL LIKE 通配符。
- [ ] ref 的 `any` / `all` / `none`。
- [x] 关系目标谓词（RelatedTo/related）。
- [ ] 明确时间范围语义。
- [ ] FTS `match` 是否进入 AST 另行评审，不强行合并。

## 4.8 Sort 和分页

- [x] Sort 改为结构化 `[]SortField`。
- [x] sort 不允许表达式或任意 SQL。
- [x] JSON 字段排序由 Schema 编译出 `json_extract`，客户端不接触 SQL。
- [x] 保留页码分页并限制公开 page size。
- [x] 显式排序自动追加 ID，保证页码分页稳定。
- [ ] 增加游标分页。
- [ ] 游标必须包含完整排序键和 ID，避免翻页重复/遗漏。

## 4.9 查询成本限制

- [x] Filter 文本限制为 4096 bytes。
- [x] 当前 Lisp AST 深度限制为 12，总节点限制为 256。
- [x] and/or 受 AST 总节点上限约束，空逻辑表达式直接拒绝。
- [x] IN 集合和 subtree 结果限制为 100。
- [x] Filter 嵌套和 expand 路径深度均受限。
- [x] expand 表达式限制为 1024 bytes、32 条路径、每批 1000 条引用。
- [x] 公开 page size 上限为 100。
- [x] Query/QueryPage 接收 request context，并有取消测试。
- [ ] SQLite 查询超时/中断策略。
- [ ] 仅管理员可使用 explain/debug。

## 4.10 Policy 合并

权限不能靠拼接字符串：

```go
q.Where = query.And(
    q.Where,
    query.In(query.Ref("owner"), allowedOwnerIDs),
)
```

- [ ] 用户 Query 和 Policy Query 分开构建。
- [ ] Policy 在 AST 层强制合并。
- [ ] 用户不能覆盖、删除或弱化 Policy 条件。
- [ ] count、list、export、aggregate 必须使用完全相同的 Policy。

## 4.11 聚合查询独立设计

不要把 Filter Lisp 扩展成另一套 SQL。

```go
type AggregateQuery struct {
    Type    string
    Where   FilterExpr
    GroupBy []Group
    Metrics []Metric
}
```

- [ ] count/sum/avg/min/max。
- [ ] scalar 字段分组。
- [ ] ref 目标分组。
- [ ] 日/周/月/季度时间分桶。
- [ ] 聚合字段和分组字段 Schema 校验。
- [ ] 聚合继承同一 Policy。

## 4.12 直接替换原则

- [x] 已删除 `ListQuery.Filter string`，没有双查询路径。
- [x] Lisp Parser 直接输出新 AST，旧 Lisp-to-SQL 编译器已删除。
- [x] 后台、模板和 association 已一次性改到新查询入口。
- [x] 公网 `/api/nodes/{type}` 已直接移除原始 Lisp filter/expand，不增加兼容开关。
- [x] 变更写入 Changelog/ADR，未增加旧 API 适配代码。

---

# 5. Schema 与存储可行性验证

CRM 不能只验证功能，还要验证 JSON + Edge 模型在真实数据量下是否成立。

## 5.1 必做基准

- [ ] 10 万、100 万 Node 的按 type 分页。
- [ ] JSON 单字段等值、范围和排序。
- [ ] 单 ref 和 ref[] 筛选。
- [ ] 两跳关系筛选。
- [ ] 负责人 + 状态 + 时间组合筛选。
- [ ] FTS 与普通条件组合。
- [ ] 批量 Expand。
- [ ] 聚合查询原型。
- [ ] SQLite WAL 并发读写测试。

## 5.2 索引方案 ADR

优先评估 SQLite JSON expression index 和 partial index：

```sql
CREATE INDEX ... ON nodes(type, json_extract(fields, '$.stage'));
CREATE UNIQUE INDEX ... ON nodes(json_extract(fields, '$.external_id'))
WHERE type = 'contact';
```

ref 查询继续使用 edges 索引。

- [ ] 验证表达式索引是否覆盖 CRM 常用查询。
- [ ] 验证唯一约束错误能映射到字段。
- [ ] 验证类型配置变更时索引迁移。
- [ ] 在基准证明不足前，不引入通用 EAV value 表。
- [ ] 在基准证明不足前，不改为每类型一张物理表。

## 5.3 金额和精度

CRM 的金额不能默认使用 float64。

- [ ] 增加 decimal/money 字段策略。
- [ ] 明确 SQLite 存储形式：整数最小单位或规范化 decimal string。
- [ ] 聚合和排序必须保持精度。
- [ ] API JSON 不能把高精度金额隐式转成 float64。

---

# 6. 发布路线

## Phase 0：v0.8.4 先修现有系统

> 不改变总体架构，只消除已经确认的安全和正确性问题。

### 公共 API

- [x] 公共列表默认只返回已发布内容。
- [x] 未知 type 返回 400。
- [x] Sort 使用结构化字段和方向，不接受原始 SQL。
- [x] type/id 一致性检查。
- [x] 公网通用列表直接移除 filter/expand；内部 Lisp 增加长度、深度、节点、集合和展开路径限制。

### 写入路径

- [x] Patch 执行字段类型校验。
- [x] required 字段不能删除或写为空值。
- [x] 可选 ref 的 null/空集合清空语义统一。
- [x] ExpandPathMany 按 ID 对齐，不依赖 SQL IN 顺序。

### 上传

- [x] admin upload 进入认证组。
- [x] api upload 默认认证。
- [x] 文件使用 O_EXCL 创建，避免覆盖。
- [x] 文件名使用 pathologize 清洗，扩展名与文件头 MIME 双重检查。
- [x] 上传文件响应设置 nosniff，PDF/ZIP 强制下载。

### 认证和 Session

- [x] AddAuthMethod 校验 Node 存在、实际 NodeType 和 auth capability。
- [x] bind 使用 Session Node.Type，不接受伪造 type。
- [x] Admin logout 使服务端 Session 失效，未认证请求不能触发全局登出。
- [x] Admin Session 服务端过期。
- [x] 前台和后台 Cookie Secure 属性可配置。

### 配置

- [x] types.yaml 使用 KnownFields 拒绝未知字段。
- [x] 删除测试和 association 中无效的 `title:` 配置。

### 已完成

- [x] 修复 Admin 类型树同路由不同参数不刷新。
- [x] 修复类型树页面标题参数匹配。
- [x] 增加对应回归测试。

### v0.8.4 验收

- [x] 安全和正确性修复均增加对应回归测试。
- [x] `go test ./...`、`go test -race ./...`、`go vet ./...` 通过。
- [x] `govulncheck ./...` 无可达漏洞；升级 `golang.org/x/image` 至 v0.43.0。
- [x] 未加入 CRM 业务模型或无关后台改版。

## Phase 1：v0.9.0 重建核心契约

> 这是最重要的一阶段。完成前不开始 CRM 看板和报表。

### Schema

- [ ] 完成 Node 通用列语义 ADR。
- [ ] 分离 Schema、Capability 和 View 元数据。
- [ ] Create/Patch/Import 使用统一 Validator。
- [ ] 唯一约束和索引声明。
- [ ] 默认值、immutable、条件必填和跨字段校验。
- [ ] 删除策略 restrict/set_null/cascade。

### Query Engine 2.0

- [ ] 建立公开 Filter AST。
- [ ] Lisp Parser 改为 AST 输入适配器。
- [ ] 增加 Go Query Builder。
- [ ] Schema-aware 校验。
- [x] 结构化 Sort（已在 v0.8.4 提前完成）。
- [ ] Policy AST 合并。
- [x] 当前 Lisp/expand 复杂度限制（已在 v0.8.4 提前完成）。
- [ ] request context 取消和数据库查询超时。
- [ ] JSON QuerySpec 原型。

### Auth Realm 与 Actor

password/web auth 已改为服务端 Realm 映射，不再存在 `type == "" -> "user"`。

建议：

```go
site.Auth().Register(web.AuthRealm{
    Name:     "member",
    NodeType: "member",
    Default:  true,
})
password.Mount(site, password.Options{Realm: "member"})
```

- [x] 删除框架对 `user` 类型的假设。
- [x] Realm 只在服务端注册，启动后只读。
- [x] Realm 注册时验证 NodeType 存在且启用 authentication capability。
- [x] register/login 不接受任意 NodeType，未知 JSON 字段直接拒绝。
- [x] bind 校验 Session Actor 的 Realm 与真实 NodeType。
- [x] 所有 credential 路由统一使用 `/api/auth/{realm}/...`。
- [x] password/微信只负责各自 credential method。
- [x] Session、Admin 和 API Key 适配入口统一为 Actor。
- [x] Session 仅保存 Token hash，并记录 Realm。
- [x] Realm 上线时直接从请求 DTO 删除 Type，并同步修改所有站点；未保留旧字段、旧路由和 fallback。

### Error Contract

- [ ] 定义 Status/Code/Message 错误。
- [ ] 401/403/404/409/422 语义稳定。
- [ ] Hook 可以返回结构化错误。
- [ ] 客户端不依赖中文错误字符串。

### Ref API

- [ ] `RefID(nodeID, field)`。
- [ ] `RefIDs(nodeID, field)`。
- [ ] `HasRef(nodeID, field, targetID)`。
- [ ] `FullNode(nodeID)`。
- [ ] 批量版本，避免权限判断 N+1。

### 生命周期

- [ ] `Site.Close() error`。
- [ ] 增加返回 error 的 `web.Open()`，保留 panic 便捷入口。
- [ ] DB 和 Migrator 支持 Context。
- [ ] 类型 Schema hash 和按需搜索重建。

### v0.9.0 验收

- [ ] association 不再需要自己修补草稿、sort、type、upload 等框架漏洞。
- [ ] 至少一个非 CMS 示例只使用自定义 Type，而不声明 user/article/page。
- [ ] 所有现有站点完成一次性源码迁移，并提供简短变更说明；框架不保留旧 API 适配层。
- [ ] 核心 API 在 v1 前进入冻结候选。

## Phase 2：v0.10.0 业务应用可靠性

### 软删除和并发

- [ ] archive/restore/permanent-delete。
- [ ] 默认查询排除归档。
- [ ] revision/expected_revision 乐观锁。
- [ ] 冲突返回 409。

### 审计

- [ ] Actor、Action、Node、RequestID、Timestamp。
- [ ] 创建、更新、归档、恢复、删除、登录和状态转换写审计。
- [ ] 记录字段级 before/after。
- [ ] 审计只追加。

### Policy

- [ ] BeforeList/BeforeView/BeforeSearch/BeforeUpload。
- [ ] Create/Update/Delete/Transition 权限统一。
- [ ] 行级查询范围。
- [ ] 字段只读和字段写入白名单。
- [ ] Admin 绕过 Policy 必须显式，不使用隐含旁路。

### Workflow

- [ ] 类型声明状态集合和初始状态。
- [ ] 声明合法转换。
- [ ] 转换权限。
- [ ] 转换必填字段。
- [ ] 独立 Transition API。
- [ ] BeforeTransition/AfterTransition。
- [ ] 转换自动进入审计。

### 关系 Node

- [ ] 文档明确简单 Edge 与关系 Node 的边界。
- [ ] relation/through 类型元数据。
- [ ] 关系节点组合唯一约束。
- [ ] 后台内联查看和编辑关系节点。

### v0.10.0 验收

- [ ] 可以建模 account/contact/opportunity/activity，但 gcm 不内置这些类型。
- [ ] 能表达本人、负责人和团队范围权限。
- [ ] 任意关键修改可追踪。
- [ ] 并发编辑不会静默覆盖。

## Phase 3：v0.11.0 查询、运营和 CRM 视图

- [ ] AggregateQuery：count/sum/avg/min/max。
- [ ] scalar/ref 分组和日期分桶。
- [ ] 保存视图和共享视图。
- [ ] 通用实体详情页。
- [ ] 关联记录 Tab。
- [ ] 审计历史 Tab。
- [ ] 业务活动时间线。
- [ ] Kanban 声明和 Workflow 拖拽转换。
- [ ] Calendar 开始/结束字段声明。
- [ ] CSV/XLSX 导入预检、错误报告和导出。
- [ ] 重复检测、Merge 预览和安全合并。
- [ ] 批量操作和幂等键。

### v0.11.0 验收

- [ ] 使用配置和少量站点代码可完成一个基础 CRM。
- [ ] 聚合、导出、保存视图与普通列表遵守同一 Policy。
- [ ] 大批量操作不会阻塞普通 HTTP 请求。

## Phase 4：v1.0 集成和稳定化

- [ ] Outbox/Webhook，支持签名、重试和死信。
- [ ] SQLite 持久 Job，支持取消和优雅停机。
- [ ] API Key 哈希存储、范围、撤销和过期。
- [ ] OAuth/OIDC 插件边界。
- [ ] 站内通知和外部通知插件。
- [ ] healthz/readyz。
- [ ] API 和错误码文档。
- [ ] Schema/迁移/备份/升级指南。
- [ ] 导出 API 兼容策略和语义版本规则。

---

# 7. CRM 建模验证项目

在宣布“支持 CRM”前，应建立一个独立示例，不把业务模型写进 core。

建议最小类型：

```text
account       客户企业
contact       联系人
employment    联系人与企业的任职关系
lead          线索
opportunity   商机
activity      电话/拜访/会议/备注
task          待办任务
```

验证场景：

- [ ] 一个联系人可以有多段任职关系。
- [ ] 商机有负责人、协作人、阶段、金额和预计成交日期。
- [ ] 普通成员只能查看本人和团队记录。
- [ ] 商机状态只能走合法转换。
- [ ] 客户详情展示联系人、商机和活动时间线。
- [ ] 修改金额、负责人和状态有审计。
- [ ] 两人同时编辑时后提交者收到冲突。
- [ ] CSV 导入可以匹配企业和联系人。
- [ ] 重复客户可以预览并安全合并。
- [ ] 按负责人、阶段、月份汇总商机金额。

该示例通过前，不应在 README 中宣称 gcm 是成熟 CRM 框架。

---

# 8. 实施原则

## 8.1 先修不变量，再加功能

优先顺序固定为：

1. 安全和数据正确性
2. Node/Field/Relation 语义
3. Query AST 与 Schema 校验
4. Actor/Auth Realm/Policy
5. 软删除、版本、审计、Workflow
6. 聚合与通用 CRM 视图
7. 导入导出和外部集成

## 8.2 用真实项目驱动抽象

- association 验证 publishable、member、公开内容和小程序 API。
- CRM 示例验证 owner/team、workflow、timeline、aggregation。
- 至少两个项目出现相同需求后，再考虑抽进通用插件。

## 8.3 保持 Go API 直接

- 具体 struct 优先。
- 接口由消费方按需要定义。
- 不创建万能 Repository 或 Service 层。
- 错误显式返回并携带上下文。
- I/O 接收 context.Context。
- 不启动无法停止的 goroutine。

## 8.4 每个能力都要有失败测试

不仅测试成功路径，还必须覆盖：

- 未授权
- 类型不匹配
- 非法字段和值
- 删除约束
- 版本冲突
- 查询复杂度超限
- 空集合和 NULL
- 事务回滚
- 并发写入
- 数据迁移和升级后不变量

---

# 9. 下一步，只做这一批

在继续设计 CRM UI 前，先完成 v0.8.4：

1. 公共列表默认发布范围
2. sort 白名单
3. Patch 完整校验
4. URL type 校验
5. admin/api upload 默认认证
6. Admin logout 和 Session 过期
7. AddAuthMethod 类型校验
8. ExpandPathMany 按 ID 对齐
9. types.yaml 严格字段解析
10. 查询复杂度上限
11. 对应回归测试

同时只写 ADR，不立即实现：

- Node status/publication 语义
- JSON + Edge 索引和约束策略
- Query AST/QuerySpec
- Auth Realm/Actor
- Edge 与关系 Node 边界

完成上述内容并由 association 回归通过后，再开始 v0.9.0。

---

# 10. v1.0 完成定义

- [ ] 定位清楚：核心不依赖 CMS 或 CRM 固定模型。
- [ ] 默认安全：公开接口、上传、认证和 Cookie 采用安全默认值。
- [ ] 数据正确：所有写入口使用同一 Schema 和约束。
- [ ] 关系可靠：基数、类型、删除策略和关系 Node 边界明确。
- [ ] 查询可靠：AST、Schema 校验、Policy 合并、成本限制和稳定分页完成。
- [ ] 认证解耦：没有 user 假设，Realm 和 Actor 边界清楚。
- [ ] 权限完整：List/View/Create/Update/Delete/Transition/Upload 均可授权。
- [ ] 可追踪：软删除、乐观锁和审计完整。
- [ ] 可扩展：Workflow、聚合、时间线和批量能力可组合。
- [ ] 可运维：Context、Close、健康检查、迁移、备份和升级文档完整。
- [ ] 经过验证：至少有一个 CMS 项目和一个 CRM 示例通过回归。
