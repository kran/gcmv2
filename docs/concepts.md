# gcmv2 概念清单（v0.9 现状）

- 文档性质：概念盘点，不含裁剪决策
- 更新时间：2026-09-10
- 目的：把当前代码里真实存在的概念全部列出来，标注位置、职责、使用状态，供判断复杂度用

概念分三类读者：

| 读者 | 需要理解的概念 |
|---|---|
| Schema 作者（写 `types.yaml`） | TypeDef、FieldDef、Kind、Capability、Constraints、ref/ref[]、关系代数、access 语义 |
| Site 开发者（写 `main.go` + hooks） | Site、CmsCtx、Actor、AuthRealm、Policy、Hook、Error、Engine |
| 内核维护者 | 全部，含存储、查询编译、完整性、迁移 |

---

## 1. 分层地图

```text
                    ┌─────────────────────────────────────────┐
   Schema 作者 ─────▶│ types: TypeDef/FieldDef/Kind/Capability │
                    └────────────────┬────────────────────────┘
                                     │ 定义
   Site 开发者 ────▶┌────────────────▼────────────────────────┐
                    │ web: Site/CmsCtx/Actor/Realm/Policy/Err │
                    └────────┬───────────────────────┬────────┘
                             │ 调用                   │ 挂载
                    ┌────────▼──────────────┐  ┌─────▼──────────┐
                    │ core: Node/Edge/Query │  │ plugin: 7 个    │
                    │ Auth/Search/Tree/Hook │  └────────────────┘
                    └────────┬──────────────┘
                             │ 编译
                    ┌────────▼──────────────┐
                    │ query: Expr/Path/Set  │
                    └───────────────────────┘
```

---

## 2. Schema 层（`types` 包）

### 2.1 定义结构

| 概念 | 位置 | 职责 |
|---|---|---|
| `TypeDef` | `types/types.go` | 一个 NodeType 的完整定义：Fields + Constraints + Capabilities + Admin |
| `FieldDef` | `types/types.go` | 字段：name/label/kind/to/required/default/immutable/options/item/fields/algebra/on_delete |
| `Constraints` | `types/types.go` | 类型级数据库约束：`unique` / `indexes`（标量组合，引用不参与） |
| `Capabilities` | `types/types.go` | 运行时能力开关（见 2.3） |
| `AdminView` | `types/types.go` | 后台展示元数据：list/tree、icon、columns |
| `Types` | `types/types.go` | Kind 注册表 + TypeDef 容器，Schema 校验入口 |
| `Kind` | `types/kind.go` | 字段值类型契约：`Name/Validate/IsEmpty/Class/QueryOps/ValidateField` |
| `Class` | `types/kind.go` | 值存哪里：`ClassField`（fields JSON）/ `ClassRef`（1 条 Edge）/ `ClassRefList`（N 条 Edge） |
| `QueryOps` | `types/kind.go` | Kind 声明的查询能力：Equal/Ordered/Text/Sortable |
| `OnDelete` | `types/capability.go` | 永久删除目标时的策略：restrict / set_null / cascade |
| `Seg`（未导出） | `types/` | — |

**13 个内置 Kind**（kind 名即前端控件名）：

```text
text(text)      textarea(text)  richtext        number          bool
select          timestamp       slug            ref             ref[]
upload-image    upload-file     gallery
```

> 命名注意：常量 `KindString` 的值是 `"text"`，常量 `KindText` 的值是 `"textarea"` —— 代码里读起来容易搞混。

**2 个结构语法**（不是 Kind，不进注册表）：`array`（`item`）、`object`（`fields`）。复合结构内禁止 ref/ref[]。

### 2.2 引用与关系代数

| 概念 | 表达 | 语义 |
|---|---|---|
| 单引用 | `kind: ref, to: X` | 0..1（required 时 1..1） |
| 多引用 | `kind: "ref[]", to: X` | 0..N（required 时 1..N） |
| 顺序 | Edge.sort | ref[] 内的关系顺序 |
| `symmetric` | 字段级布尔 | 无向：存一条 Edge，读写双向解释 |
| `transitive` | 字段级布尔 | 有向可达：Traverse / Subtree，写入检环 |
| `equivalence` | 字段级布尔 | 无向等价类：EquivalenceClass |
| `on_delete` | 字段级枚举 | 永久删除目标时的 incoming 处理 |
| 树 | `capabilities.tree` | parent（self ref）+ order（可排序字段），写入检环 |
| 关系 Node | `capabilities.relation` | from/to 两个 required 单 ref，声明"这个 Node 是一段关系" |

### 2.3 六个 Capability

| 能力 | 声明内容 | 影响 |
|---|---|---|
| `searchable` | fields 列表 | 进 FTS 索引（可见性由查询时 Policy 决定） |
| `addressable` | field（必须 slug kind）+ unique | `GetNodeByAddress`、`/node/{address}`、树 byAddress |
| `publication` | field + draft/published 取值 | `IsPublished`、**公网读默认策略**、published_only 过滤 |
| `authentication` | 布尔 | 该 Type 可成为认证主体（Realm 指向它） |
| `tree` | parent + order | LoadTree / Subtree / Ancestors |
| `relation` | from + to | cascade 白名单、关系端点语义（后台 UI 待实现） |

### 2.4 Schema 校验规则（fail-loud 点）

- 保留列名冲突、字段名格式、重复字段
- Kind 存在、嵌套 Kind 存在、嵌套字段约束
- 复合结构内禁止 ref/ref[]；array 不能声明 fields；object 不能声明 item；object 子字段不重名
- ref 必须声明 `to` 且目标 Type 存在；代数互斥且必须 self-ref
- `set_null` 不能用于 required；`cascade` 只能用于 relation endpoint
- publication 字段必须支持相等且 draft ≠ published；tree.parent 必须是 self 单 ref；tree.order 必须可排序
- addressable 字段必须是 slug kind；unique 必须是 global
- 约束组只能含标量字段、不能重复

---

## 3. 存储层

### 3.1 表

```text
nodes          id/type/display/revision/fields(JSON)/created_at/updated_at/archived_at
edges          id/from_node/field/to_node/sort/single_ref/symmetric/created_at
auth_methods   id/type/node_id/method/identifier/data(JSON, 不透明)/timestamps
sessions       token_hash/realm/node_id/expires_at/created_at
settings       key/group_name/type/value(JSON)/updated_at
accounts       后台管理员单账号：username/password_hash/session_key/session_expires_at
nodes_fts      FTS5 虚拟表（rowid = node id）
migr_gcm       引擎迁移版本；migr_site 站点迁移版本（各自独立）
```

### 3.2 值模型

| 概念 | 位置 | 说明 |
|---|---|---|
| `Node` | `core/node.go` | 实体值模型；`Fields` 只存标量，ref 在 Edge |
| `Fields` | `core/node.go` | `map[string]any` + 容错取值（Str/Int/Bool/Slice/Map） |
| `NodePatch` | `core/node_patch.go` | 差量更新（全指针 + revision 乐观锁） |
| `Edge` | `core/edge_query.go` | 一条引用；`SingleRef`/`Symmetric` 是约束元数据，不是业务字段 |
| `EditableNode` | `core/relation_read.go` | `Node` + `Values`（标量 + ref ID），编辑/鉴权用 |
| `AuthMethod` | `core/auth.go` | 一条凭据（NodeType + method + identifier + 不透明 data） |
| `Session` | `core/auth.go` | Realm 绑定的会话，只存 token hash |
| `Setting` | `core/settings.go` | 站点 key-value 配置 |

---

## 4. 查询层（`query` 包）

### 4.1 AST 节点

```text
Expr（接口）
├── Constant                 真/假
├── Compare                  字段 比较 值（EQ/NE/GT/GTE/LT/LTE/Contains/Prefix）
├── Logic                    And / Or / Not
├── In                       集合包含（值集合 = 字面量或 Set）
├── Exists                   存在/缺失（含 missing 取反）
└── Related                   关系目标谓词（出边/入边 + 子条件）

Path                        字段路径：PathSystem / PathField / PathOutRef / PathInRef
Set                         集合来源：Subtree（address 子树）等
SortField                   结构化排序（Path + Desc）
Page                        Number + Size
ExpandPath                  Typed expand 路径（每跳 Path）
```

### 4.2 三个输入前端，一个 AST

| 前端 | 入口 | 用途 |
|---|---|---|
| Go Builder | `query.Eq/And/OneOf/...`（25 个导出函数） | 站点代码 |
| Lisp | `query.ParseLisp` | Admin/模板文本查询、调试 |
| QuerySpec | `query.DecodeSpec` | 受信管理端 JSON |

Lisp 不生成 SQL；Compiler 不接受 Raw SQL。

### 4.3 查询限制（写死的预算）

```text
文本 4096 bytes；AST 深度 12、节点 256；IN/subtree 集合 100
expand 32 路径、每批 1000 引用、深度 4；sort 8 字段；page size 100
relation depth 4；search targets 64
```

---

## 5. 执行层（`core` 包）

### 5.1 门面

| 概念 | 说明 |
|---|---|
| `Service` | 引擎实现，绑定一个 db + 一个 Types |
| `Engine` | 44 方法的对外接口（编译期断言 Service 实现） |
| `Open` / `New` | 初始化：迁移 + Edge 元数据同步 + Schema 索引；`Open` 返回 error，`New` panic |
| `Migrator` | 站点迁移执行器（goose，独立版本表） |

### 5.2 Hook 事件（18 个）

```text
core.Node（10 个，事务内）:
  before/after create · update · delete · archive · restore

web（8 个）:
  web.before_create / before_update / before_delete   写路径业务校验
  web.auth_register / auth_login                      认证流程注入
  web.node_enrich / node_render / candidates / render  渲染数据注入
  web.serve_file                                    文件端点改写
  site.before_mount                                 挂中间件/路由
```

### 5.3 读取边界

| 概念 | 说明 |
|---|---|
| `ListQuery` | 唯一结构化查询契约：Type + Where + **Scope** + Sort + Expand + Page |
| `QueryScope` | 不可伪造的行范围：`PolicyScope(expr)` / `BypassPolicy()`；零值 = 报错 |
| `SearchQuery` / `SearchTarget` | 搜索契约：每个 Type 独立 Where + Scope |
| `SearchPlan` / `SearchIndex` | 已合并 Policy 的计划 + 可替换检索引擎（默认 FTS5 + bigram） |

### 5.4 关系与完整性

| 概念 | 说明 |
|---|---|
| `Traverse/Subtree/Ancestors` | 图原语（要求 transitive 或 tree.parent） |
| `EquivalenceClass` | 等价类（要求 equivalence） |
| `OutEdges/InEdges` | 边分页（无向关系双向语义） |
| `RelationReport` / `RelationIssue` | 只读完整性报告（11 种问题类型） |
| `DeleteRestrictedError` / `IncomingReference` | 永久删除被引用阻止时的结构化信息 |
| `MergePreview` / `MergeFieldConflict` / `MergeCredential` | 只读合并预览（执行未开放） |
| `Tree` / `TreeNode` | 内存树：Roots/Children/Parent/Ancestors/Subtree |

### 5.5 哨兵错误（13 个）

```text
ErrNotFound · ErrRevisionConflict · ErrNodeArchived · ErrInvalidFields
ErrEdgeNotFound · ErrRequiredReference · ErrRelationCardinality · ErrDeleteRestricted
ErrInvalidQuery · ErrInvalidField · ErrInvalidOperator · ErrInvalidValue · ErrQueryTooComplex
```

---

## 6. Web 层（`web` 包）

### 6.1 装配

| 概念 | 说明 |
|---|---|
| `Site` | 一个数据库 + 一个引擎 + 路由 + 渲染；`Open/New/Setup(Start)/Close` |
| `CmsCtx` | 请求上下文：cho BaseContext + site + actor + principal + 渲染/响应/错误出口 |
| `HostMux` | 多站分发（每站独立库） |
| `Render` | 无缓存模板引擎（级联 `node--{type}--{slug}.html`）+ 查询函数注入 |
| `AdminPanel` | 插件向后台注册面板的扩展点（14 处引用，association 未用） |

### 6.2 身份

| 概念 | 说明 |
|---|---|
| `Actor` / `ActorKind` | 请求身份：Anonymous / Node / Admin / APIKey |
| `Principal` | Actor 背后的业务 Node（惰性加载） |
| `AuthRealm` / `AuthRegistry` | 服务端注册的认证域：Name ↔ NodeType（+ AllowRegister/Default） |
| `AdminService` / `Admin` | 独立后台账号（bcrypt + session key + 过期） |

### 6.3 授权（读 / 写）

| 概念 | 说明 |
|---|---|
| `ReadAction` | list / view / search / export（系统读动作）；站点可自定义读动作（如 `my_content`） |
| `WriteAction` | create / update / delete（系统写动作，对应三个公开写端点） |
| 授权事件 | `web.read.<action>.<type>` / `web.write.<action>.<type>` —— schema 加载后按类型定义，授权就是注册这些事件的 handler |
| `ReadRule` | `func(*CmsCtx, string, *gquery.Expr, *core.List[string]) error` —— AND 收窄行范围 + 声明不可见字段（`Site.ReadRule` 注册） |
| `CmsCtx.ReadRule` | 一次解析（范围 + 字段掩码），按 `(action, type)` 缓存在请求上下文上（生命周期 = Actor） |
| `MaskNode` / `MaskNodes` / `MaskTree` | 应用字段掩码（拷贝后删键、递归进 `Expand`）；站点自建 JSON 出口调用 |
| 写规则 | create/update: `func(*CmsCtx, …, *core.List[string]) error`；delete: `func(*CmsCtx, int64) error`（`Site.WriteRule` 注册） |
| `Site.ReadScope` | 解析读范围：有 handler → 用规则；无 → 系统读动作走 publication 默认，站点读动作报错 |
| `Site.Exposes` | `Has(web.read.<action>.<type>)` —— 该类型是否被站点显式暴露 |
| `deniedWrite` / `rejectFields` | 未注册写规则 → 401/403；白名单外字段 → 422 + details |

### 6.4 错误契约

| 概念 | 说明 |
|---|---|
| `Error` | Status + Code + Message + Details |
| `Code`（12 个） | invalid_request / invalid_value / invalid_query / query_too_complex / unauthorized / forbidden / not_found / conflict / delete_restricted / upload_invalid / internal / unavailable |
| `Fail` / `Reject` | 统一出口：结构化错误映射，未知错误 500 + 日志 |

### 6.5 运维

```text
/healthz  /readyz     探针
uploadsDir            上传目录（扩展名 + 文件头双重校验，排除 svg/html）
secureCookies         Cookie Secure 开关
debug                 渲染错误详情页开关
```

---

## 7. 插件（7 个）

```text
password    凭据插件：Realm 绑定、email/password、bcrypt（唯一持有 password hash 语义）
sitemap     公开内容 sitemap（走 PolicyExport）
backup      后台备份/恢复
oss         图片链接转对象存储桶
imgproc     本地图片裁剪（/uploads /static 带参数）
cors        CORS 中间件
highlight   代码高亮
```

---

## 8. 概念数量统计

| 层 | 概念数 | 明细 |
|---|---|---|
| Schema 定义 | 10 | TypeDef/FieldDef/Constraints/Capabilities/AdminView/Types/Kind/Class/QueryOps/OnDelete |
| Kind | 13 + 2 结构语法 | 见 2.1 |
| Capability | 6 | searchable/addressable/publication/authentication/tree/relation |
| 关系代数 | 3 + 2 | symmetric/transitive/equivalence + tree + relation |
| 存储 | 8 表 / 8 值类型 | 见 3 |
| 查询 AST | 7 Expr + 4 PathKind + Set/SortField/Page/ExpandPath | 见 4.1 |
| 查询前端 | 3 | Builder / Lisp / QuerySpec |
| Hook 事件 | 18 | 10 core + 8 web |
| Engine 方法 | 44 | 写 5 / 读 14 / 图 9 / 搜索展开 5 / 认证 8 / 容器 3 |
| 哨兵错误 | 13 | 见 5.5 |
| Web 概念 | ~20 | Site/CmsCtx/Actor/Realm/Policy/Error/Code/Admin/Render/... |
| 插件 | 7 | 见 7 |

---

## 9. 使用状态（谁真的在用）

| 概念 | 使用状态 |
|---|---|
| Node/Edge/Fields/Kind/Capability | association 全部类型在用 |
| publication / searchable / addressable / tree | association 10 个类型在用 |
| authentication + AuthRealm | association 的 `member` 在用 |
| `relation` capability | **无真实使用者**（只有测试） |
| `equivalence` | **无真实使用者**（只有测试；association 用不上） |
| `on_delete: cascade` | **无真实使用者**（association 全是默认） |
| 读授权事件 | association 的读动作 `my_content` + 公开读默认（publication）在用 |
| 写授权事件 | association 为 article/event/supply 各注册 create/update/delete 规则（身份 + 字段 + 加工） |
| 按角色的字段集 | web 测试覆盖（会员只写 body，编辑还能写发布状态）；association 无编辑流程 |
| `Set`（SubtreeOf） | association 用 `Tree` + ref 集合替代 |
| Lisp 前端 | Admin 列表 filter、模板 filterList |
| QuerySpec | Admin `/admin/query/{type}` |
| `MergePreview` | 只有测试（执行未开放） |
| `RelationReport` | Admin `/integrity/relations` |
| `AdminPanel` | 插件扩展点，association 未用 |
| `Setting` | association 用 `site.name` / `site.logo`；Admin 设置页 |
| `backup` / `oss` / `highlight` / `sitemap` | 挂载但功能边界窄 |
| `ActorAPIKey` | **无真实使用者**（只有 Actor 适配入口和测试） |
| `symmetric` | 无真实使用者（association 用不上） |

---

## 10. 复杂度来源（观察，不含结论）

概念数的增长主要来自**正交组合**，而不是单个概念本身复杂：

```text
1. Kind(13) × QueryOps(4) × Class(3)      → 值类型的查询与存储矩阵
2. Capability(6) × 站点              → Schema 声明面
3. 关系代数(3) + tree + relation      → 四种关系形态, association 只用到 tree
4. Hook(18)                          → 生命周期 + 渲染 + 认证四类扩展点
5. Expr(7) × Path(4) × Set(1)        → 查询表达力
6. 授权事件 (4 读 + 3 写) × Type(n) → 事件名空间
7. Code(12) × Status                 → 错误契约
```

三层"当前只有测试使用者"的概念：

```text
关系语义层:  relation · equivalence · symmetric · on_delete:cascade
授权层:      ActorAPIKey（读/写授权事件已成为主路径，不再是“只有测试用”的概念）
运维/合并层: MergePreview
```

这些概念各自都有完整实现和测试，但没有真实站点需求在压。是否保留/推迟，是下一步的决策点。
