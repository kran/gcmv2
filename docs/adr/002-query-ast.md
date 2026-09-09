# ADR-002：统一 Query AST，Lisp 作为文本前端

- 状态：Proposed
- 目标版本：v0.9.0
- 日期：2026-09-09

## 背景

当前查询入口是：

```go
core.ListQuery{
    Filter: `(and (= status 1) (in ->owner {:ids}))`,
    Sort:   []core.SortField{...},
}
```

Lisp Parser 和 SQL Compiler 已能表达标量、JSON 字段、出边、入边和 subtree。这部分能力有价值，但目前存在结构性问题：

- 核心 API 仍以字符串为主
- 业务代码需要拼括号和字段前缀
- Policy 无法安全追加条件
- 编译器缺少完整 Schema 类型信息
- 自定义 Lisp 函数可以返回 SQL 片段
- 公网客户端难以正确构造表达式
- Filter、Sort、Expand、Search 分属不同协议

v0.8.4 已完成：

- 公网 `/api/nodes/{type}` 移除 filter/expand
- Sort 改为结构化 SortField
- Lisp 和 expand 增加复杂度上限

v0.9 需要把 AST 变成真正的核心契约。

## 决策

### 1. Filter AST 是唯一核心表示

所有查询入口最终生成同一个 AST：

```text
Lisp Parser ────────┐
Go Builder ─────────┼─> Filter AST -> Validate -> Apply Policy -> Compile SQL
JSON QuerySpec ─────┤
业务 API 参数 ──────┘
```

SQL Compiler 不再解析字符串，也不接收原始 SQL。

### 2. AST 使用封闭节点集合

概念模型：

```go
type Expr interface {
    filterExpr()
}

type Compare struct {
    Op    CompareOp
    Left  Path
    Value any
}

type Logic struct {
    Op   LogicOp
    Args []Expr
}

type In struct {
    Path   Path
    Values []any
}

type Relation struct {
    Path  Path
    Where Expr
}
```

不提供 `RawSQLExpr`。

如果站点需要 core 尚不支持的查询，应先增加明确 AST 节点或在站点使用独立 SQL，不通过“万能 raw SQL”污染通用 Query。

### 3. Path 是结构化值

当前文本前缀：

```text
status
$name
->owner
<-activity.contact
```

AST 中改为：

```go
type Path struct {
    Kind       PathKind // SystemField / Field / OutRef / InRef
    SourceType string   // InRef 使用
    Field      string
}
```

Builder 示例：

```go
query.System("created_at")
query.Field("amount")
query.Ref("owner")
query.InRef("activity", "contact")
```

Lisp 的 `$`、`->`、`<-` 只存在于文本语法，不进入其他 API。

## ListQuery

目标 API：

```go
type ListQuery struct {
    Type   string
    Where  Expr
    Sort   []SortField
    Expand []ExpandPath
    Page   Page
}
```

Type 成为 Query 的明确属性，不再要求每个调用方手工添加：

```lisp
(= type {:type})
```

这样 Schema Validator 能准确知道 `$field` 属于哪个 Type。

### Sort

```go
type SortField struct {
    Path Path
    Desc bool
}
```

规则：

- 系统字段必须属于允许排序的系统字段
- 动态字段必须存在于当前 Type
- ref 默认不可直接排序
- 不接受 SQL 表达式
- 为稳定分页自动追加 ID 次序

### Page

```go
type Page struct {
    Number int
    Size   int
}
```

v0.9 保留页码分页。后续增加 Cursor，但不同时设计两套 Filter。

## Schema-aware Validator

AST 必须在 SQL 编译前结合 TypeDef 校验。

### 比较操作

| Kind | 允许操作 |
|---|---|
| text/string/select | eq、ne、contains、prefix、in、is-null |
| number/money | eq、ne、gt、gte、lt、lte、between、in、is-null |
| timestamp | eq、gt、gte、lt、lte、between、is-null |
| bool | eq、ne、is-null |
| ref/ref[] | has、any、all、none、exists、missing |
| array/object/gallery | 初期仅 exists/missing，其他操作按 Kind 扩展 |

### 关系路径

- OutRef 字段必须存在于当前 Type 且是 ref/ref[]
- InRef 必须明确来源 Type 和来源字段
- 每深入一层，Validator 切换当前 Type
- 最大关系深度默认 4
- 任意方向通配只允许受信管理查询

### 值校验

查询值复用 Kind 的值转换/校验规则，但查询操作不应调用“required”规则。

需要区分：

```text
写入值校验
查询值校验
```

例如 timestamp 查询允许一个范围，写入仍然只接受单个时间戳。

## Go Builder

目标是让业务代码不拼字符串：

```go
where := query.And(
    query.Eq(query.Field("stage"), "qualified"),
    query.GTE(query.Field("amount"), money.FromCents(100_000_00)),
    query.Any(query.Ref("owner"), ownerIDs...),
)

result, err := engine.Query(ctx, core.ListQuery{
    Type:  "opportunity",
    Where: where,
    Sort: []core.SortField{
        {Path: query.System("updated_at"), Desc: true},
    },
    Page: core.Page{Number: 1, Size: 20},
})
```

Builder 只创建 AST，不执行查询，不保存全局状态。

## Lisp Parser

Lisp 继续支持，但职责缩小：

```go
expr, err := query.ParseLisp(`
  (and
    (= $stage "qualified")
    (any ->owner [12 18]))
`)
```

Parser 输出与 Go Builder 相同的 AST。

### 保留场景

- 服务端模板
- 管理后台高级搜索
- 配置中的保存视图
- 开发调试

### 不保留场景

- 公网 `/api/nodes` 的 filter 参数
- 普通小程序业务接口
- Policy 内字符串拼接

### 直接替换

新 AST Compiler 完成后：

- 删除旧 `ListQuery.Filter string`
- 删除旧 Compiler 的独立 AST
- Lisp Parser 直接产生新 AST
- 同步修改 gcm 后台、模板和现有项目
- 不保留旧查询执行路径

## JSON QuerySpec

受信通用客户端使用结构化 JSON：

```json
{
  "where": {
    "op": "and",
    "args": [
      {"op": "eq", "field": "stage", "value": "qualified"},
      {"op": "any", "ref": "owner", "values": [12, 18]}
    ]
  },
  "sort": [
    {"field": "updated_at", "desc": true}
  ],
  "page": {"number": 1, "size": 20}
}
```

规则：

- QuerySpec 不是任意 AST 反序列化
- 每个 op 使用显式 DTO 校验
- 默认只对管理员或受信 API Key 开放
- 普通业务 API 继续接受简单参数

## Policy 合并

Policy 返回 AST，而不是 SQL 或 Lisp：

```go
func opportunityScope(actor Actor) query.Expr {
    if actor.IsAdmin() {
        return query.True()
    }
    return query.Any(query.Ref("owner"), actor.NodeID)
}
```

最终查询：

```go
where = query.And(userWhere, policyWhere)
```

不允许用户输入替换 Policy，也不允许 count/list/export 使用不同 Scope。

## 查询成本

Validator 在执行前计算 Query Cost：

- AST 节点数
- 最大深度
- IN 集合长度
- 关系跳数
- Expand 路径数
- Page Size
- 是否包含全文搜索
- 是否包含未索引字段排序

建议初始限制：

```text
AST nodes      256
AST depth      12
IN values      100
relation depth 4
expand paths   32
page size      100
```

管理员可以拥有更高限额，但不应完全无限制。

## Context

目标执行接口必须接收 Context：

```go
Query(ctx context.Context, q ListQuery) ([]Node, error)
QueryPage(ctx context.Context, q ListQuery) ([]Node, int64, error)
```

要求：

- HTTP 断开后取消 SQLite 查询
- 聚合和导出必须设置 deadline
- 不在 Query 内部创建不可取消的 goroutine

## Search

FTS 暂时保持独立：

```go
Search(ctx, SearchQuery)
```

原因：

- FTS 有相关性分数
- 排序语义不同
- 查询语法和普通比较不同

后续可以让 SearchQuery 接受 Filter AST 作为附加范围，但不把 FTS 强行伪装成普通比较操作。

## AggregateQuery

聚合与列表共享 Where/Policy，但使用独立请求类型：

```go
type AggregateQuery struct {
    Type    string
    Where   Expr
    GroupBy []Group
    Metrics []Metric
}
```

不向 Lisp 中继续堆 `group-by/sum/select`，避免把 Filter DSL 发展成第二套 SQL。

## 错误

查询错误需要稳定分类：

```text
ErrInvalidField
ErrInvalidOperator
ErrInvalidValue
ErrQueryTooComplex
ErrSortNotAllowed
ErrRelationDepth
ErrQueryTimeout
```

错误应包含：

- Type
- 字段路径
- 操作符
- AST 或文本位置
- 对外安全 Message

## 包边界

Query AST 是独立且可测试的领域，允许建立一个顶层 `query` 包。它不依赖 web，也不执行数据库操作。

```text
query/   AST、Builder、Lisp Parser、QuerySpec DTO 校验
core/    结合 Types 校验并编译/执行 SQLite
web/     HTTP 参数和响应
```

如果实际实现证明拆包只增加循环依赖，则保留在 core；不要为了目录美观强拆。

## 验收条件

- [ ] Go Builder、Lisp、QuerySpec 产生同一种 AST。
- [ ] SQL Compiler 不接受原始 SQL。
- [ ] 当前 Type 的字段和操作符全部经过 Schema 校验。
- [ ] Policy 可以安全追加且无法被用户弱化。
- [ ] 公网业务 API 不接触 Lisp。
- [ ] count/list/export/aggregate 使用同一 Where 和 Policy。
- [ ] 查询复杂度和 Context 取消有测试。
- [ ] association 全部查询迁移到新 Builder。
- [ ] 旧 Filter string 和旧编译路径被删除。
