# ADR-004：Edge、关系 Node 与引用完整性

- 状态：Accepted / Core Implemented
- 目标版本：v0.9.0～v0.10.0
- 日期：2026-09-09

## 背景

gcm 当前将 ref/ref[] 存在统一 edges 表：

```text
from_node / field / to_node / sort / created_at
```

这非常适合 CMS 分类、作者和树结构，也可以表达 CRM 的负责人、客户和联系人引用。

但 CRM 中很多“关系”本身包含业务数据：

- 联系人在企业中的职位、部门、任职时间
- 联系人在商机中的角色和影响力
- 企业合作关系的类型、状态和有效期
- 项目成员的职责、工时和权限

如果不断向 edges 增加 JSON 或动态列，edges 会成为第二套缺乏约束的实体系统。

此外当前 DeleteNode 会直接删除全部入边和出边，没有考虑 required ref 和业务完整性。

## 决策

### 1. Edge 只表达轻量引用

Edge 适合以下关系：

```text
article.category
record.owner
contact.primary_account
task.assignee
category.parent
```

判断标准：关系本身没有需要独立查询、审计、授权或维护的属性。

Edge 固定结构保持简单：

```go
type Edge struct {
    ID        int64
    FromNode  int64
    Field     string
    ToNode    int64
    Sort      int
    SingleRef bool // storage constraint metadata
    Symmetric bool // storage constraint metadata
    CreatedAt time.Time
}
```

不增加通用 `fields JSON`。`single_ref` 和 `symmetric` 只用于数据库约束，不承载业务属性。

### 2. 有属性的关系使用关系 Node

只要关系具有以下任一内容，就使用 Node：

- 角色
- 状态
- 起止时间
- 金额
- 备注
- 附件
- 独立权限
- 独立审计

示例：

```yaml
types:
  employment:
    capabilities:
      relation:
        from: contact
        to: account
    constraints:
      unique:
        - [contact, account, start_at]
    fields:
      - { name: contact, kind: ref, to: contact, required: true }
      - { name: account, kind: ref, to: account, required: true }
      - { name: title, kind: text }
      - { name: department, kind: text }
      - { name: start_at, kind: timestamp }
      - { name: end_at, kind: timestamp }
```

`relation` 是后台和 API 展示提示，数据本质仍是普通 Node + ref，不建立第二套存储引擎。

## 引用基数

字段 Kind 决定基数：

```text
ref    0..1，required 时 1
ref[]  0..N，required 时 1..N
```

必须保证：

- [x] ref 在数据库中最多一条 Edge。
- [x] ref[] 不允许重复 target。
- [x] Edge target 必须存在且未归档。
- [x] target.Type 必须等于 FieldDef.To。
- [x] required ref 在 Create 和 Patch 后不能为空。
- [x] 并发写入由数据库 partial unique index 和 symmetric trigger 兜底。

当前 `UNIQUE(from_node, field, to_node)` 只能防止 ref[] 重复，不能保证 ref 最多一条。需要为 ref 写路径增加约束检查，必要时增加物化基数标识或事务保证。

## 删除策略

FieldDef 增加：

```yaml
on_delete: restrict | set_null | cascade
```

### restrict

只要存在该字段的入边，就拒绝删除目标。

适合：

- 商机的客户
- 合同的签约主体
- 任职关系的联系人和企业

required ref 默认 restrict。

### set_null

删除目标，同时删除引用 Edge；来源 Node 保留。

适合：

- 可选负责人
- 可选标签
- 临时推荐关系

可选 ref/ref[] 默认 set_null。

### cascade

删除目标时级联删除来源关系 Node。

只建议用于明确的从属实体：

- 关系 Node
- 无独立生命周期的明细 Node

普通业务实体不默认 cascade。

## 软删除与引用

软删除不立即删除 Edge。

已实现行为：

- 普通查询和展开不返回已归档目标
- `EditableNode` 和 Ref API 保留已归档目标 ID，供管理和修复
- required ref 指向归档目标时进入数据完整性报告
- 恢复目标后引用自动恢复可见
- 公共 DELETE 执行归档；Admin 永久删除时才执行 on_delete
- 认证 Node 归档时撤销其全部 Session

是否允许新引用指向已归档 Node：不允许。

## 对称、传递和等价

当前 FieldDef 支持：

```text
symmetric
transitive
equivalence
```

需要明确它们是查询语义，不是自动复制 Edge：

### symmetric

存一条边，查询时双向解释。

- [x] 写入时规范化端点顺序，避免 A→B 和 B→A 重复。
- [x] 唯一性按无向关系保证。
- [x] 单值 symmetric ref 在两个端点上都执行数据库级基数约束。
- [x] `OutEdges`、`InEdges`、Typed Expand、Query 和 Ref API 都恢复双向语义；`InEdges` 必须按字段过滤。

### transitive

允许 Traverse/Subtree，不代表数据库自动写传递闭包。

- [x] 必须有最大深度。
- [x] 写入时检测环，读取时也使用路径去重防御损坏数据。
- [x] Tree capability 的 parent 字段默认禁止环。

### equivalence

用于同义/同组关系。

- [x] equivalence 采用无向规范化存储，并在读取时双向传递遍历。
- [x] 写入时阻止自环和重复边。

## 关系读取 API

补充明确的 API，避免业务代码误读 `Node.Fields`：

```go
RefID(ctx, nodeID, field) (int64, bool, error)
RefIDs(ctx, nodeID, field) ([]int64, error)
HasRef(ctx, nodeID, field, targetID) (bool, error)
FullNode(ctx, nodeID) (*EditableNode, error)
FullNodes(ctx, ids) ([]*EditableNode, error)

type EditableNode struct {
    Node
    Values map[string]any // scalar + ref IDs
}
```

原始 `Node.Fields` 始终只包含持久化标量，`EditableNode.Values` 才包含标量和 ref ID。旧 `FullFields` 已删除，不保留近义 API。

## 关系查询

Query AST 中关系使用结构化路径：

```go
query.Any(query.Ref("industry"), industryIDs...)
query.Exists(query.Ref("owner"))
query.Related(
    query.Ref("account"),
    query.Eq(query.Field("status"), "active"),
)
query.Incoming("activity", "contact")
```

要求：

- 当前 Type 下字段必须存在
- OutRef 的目标类型由 Schema 推导
- InRef 必须声明来源 Type 和字段
- 多跳时每一层更新当前 Schema 上下文
- Policy 可以安全增加 owner/team 关系条件

## 关系 Node 的后台体验

声明 relation capability 后，后台可以提供：

- 在 from 实体详情显示关系列表
- 在 to 实体详情显示反向关系列表
- 内联新建和编辑关系记录
- 显示关系字段
- 独立筛选、审计和权限

但 relation Node 仍可在普通类型管理中打开，不创建专用存储路径。

## Merge

现有 Merge 会改写入边和出边并删除来源 Node。CRM 使用前需要补：

- [x] Merge Preview：字段冲突、入边、出边、认证方式。
- [ ] 选择每个字段保留来源还是目标。
- [ ] 关系 Node 唯一冲突处理。
- [ ] auth_methods 合并规则。
- [ ] sessions 撤销规则。
- [ ] required ref 和 on_delete 检查。
- [ ] 审计记录 source/target 和决策。
- [ ] 整体事务回滚测试。

原有直接执行且无预览的 `Merge` 已删除。当前提供只读 `PreviewMerge`，报告字段冲突、入边、出边和认证方式；真正执行合并要等字段选择、唯一冲突处理和审计契约完成后再开放。

## 性能和索引

edges 至少需要：

```text
(from_node, field, sort)
(to_node, field)
(from_node, field, to_node) unique
```

还需基准验证：

- owner 查询
- team 范围
- ref[] 多选
- 两跳关系
- 大量入边的详情页
- relation Node 列表
- 删除 restrict 的入边检查

## 数据完整性检查

增加管理命令或后台操作：

- 找出目标不存在的 Edge
- 找出字段已经从 Schema 删除的 Edge
- 找出目标类型不匹配的 Edge
- 找出 ref 多于一条的 Node
- 找出 required ref 缺失的 Node
- 找出 Tree 环
- 找出指向归档目标的 required ref

检查默认只报告；修复必须显式确认，不能静默删数据。

## 不采用的方案

### 给 edges 增加 fields JSON

拒绝。会复制 Node 的全部问题，同时让关系约束、管理 UI 和查询形成第二套实现。

### 所有关系都改成关系 Node

拒绝。简单 category/owner/parent 会变得冗长，并增加查询和后台操作成本。

### 删除目标时统一删除所有 Edge

拒绝。适合 CMS 清理，但无法保证 CRM 数据完整性。

## 直接迁移

- 未显式声明 `on_delete` 时，required ref 规范化为 `restrict`，可选 ref/ref[] 规范化为 `set_null`。
- Core migration `00011_edge_integrity.sql` 增加 `single_ref`、`symmetric` 存储元数据和并发约束。
- `SyncRelationSchema` 在启动时根据当前 Schema 重建 Edge 元数据；原始 SQL 导入 Edge 后必须再次调用。
- 新写路径立即执行新规则，不保留旧删除函数。
- 现有带属性的 Edge 如果存在，由项目迁移成关系 Node。
- 旧的直接执行 `Merge` 已删除，不增加兼容入口。

## 验收条件

- [x] ref/ref[] 基数由应用校验和数据库约束保证。
- [x] 删除策略有 restrict/set_null/cascade 测试。
- [x] 软删除保留 Edge，永久删除执行 on_delete；新引用不能指向归档 Node。
- [x] relation capability 的关系 Node 仍通过通用 Node API 管理。
- [x] 关系查询和 Expand 经过逐跳 Schema 校验，并支持 symmetric 语义。
- [x] EditableNode/RefID/RefIDs/HasRef 不再让业务代码误读 Node.Fields。
- [x] 数据完整性检查可发现悬空、未知字段、目标类型、基数、required、归档引用、元数据和环问题。
- [ ] CRM 示例中的 employment 和 opportunity_contact 可以完整建模并完成 UI 验证。
