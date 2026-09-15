# ADR 005：节点读投影 —— 值默认补全，展开按需

状态：**已定，待落地**
讨论结论：`FullNode` 不是"Node 漏了东西的补丁"，而是**读投影**。统一成一种形状、
把"完整"从类型降级成读选项，消除"有时完整有时不完整"的隐式状态。

## 形状（唯一出口）

```
读：  Node{ 行字段…, Fields: {字段: 值}, Expand: {字段: 目标节点} }
写：  只提交 Fields
```

- `Fields` = **所有字段的值**：标量取值 ✓；引用取 `id`（单）/ `[]int64`（多）
- `Expand` = 目标节点对象（`display`、跳转用），**只读**、**只一层**、不参与提交
- 写侧仍按 `kind.Class()` 分流（标量 → `nodes.fields` JSON，引用 → `edges`）——
  **存储不变**：JSON 里不存引用 id，单一真相仍在边表

## 五条规则

1. **默认补全 `Fields`**：任何公开读都读出引用 id 注进 `Fields`。实现是**每批一次**
   `WHERE from_node IN (…)`（不是每节点一次）；可进一步并进主查询（LEFT JOIN）把额外
   查询降到 0。
2. **`Expand` 按需**：按字段请求（字段名列表或 `*`），深度恒 1 —— 目标的 `Expand` 恒空
   （环引用/树不递归展开）。
3. **`Fields` 语义恒定**：不再有"没请求 expand 就没有引用 id"的隐式状态。
4. **引擎读节点 = 补全 `Fields`**：只有一条路，不开 lean 开关（为假想需求预留抽象）。
   内部过程不经过这个读投影：FTS 重建在自己的包里扫行写索引、`Count()` 只数数、
   backup 是 `VACUUM INTO` 级别的拷贝、sitemap 只需要 `slug`/`display`（走公开读，
   每批 +1 次边查询，可忽略）。真出现热路径再加，那时才有真调用方。
5. **类型收紧**：引用 id 单值 `int64`、多值 `[]int64`（今天是 `[]any`）。

## 落地点

- **`core.Service` 一处实现 hydrate**（今天 `expandMany` 在 `web/admin.go` 里 —— 位置就是
  信号：它属于 core）。所有出口共用：列表 / 详情 / 表单 / 公开 API。
- `core.FullNode` / `EditableNode` / `Values` 三个概念删除：`Node.Fields` 即完整值。
- 编辑器不必再单独拉 `/admin/expand`（读一次带回来），列表端点不再自己拼 expand。
- 前端随之收敛：组件只看 `node` + `field` + `mode`（`preset` / `expand` 两个 prop 删除）。

## 验收

- 同一个节点经"详情 / 列表第 N 行 / 编辑器"三条路径得到**相同**的 `Fields` 与 `Expand`。
- 每批读的 SQL 次数与页大小无关（20 行与 200 行同样 1 次边查询）。
- 闸门：组件里出现 `props.preset` / `props.expand` 即 FAIL；`web` 层不得自己拼 expand。

## 未决定（落地时再定）

- `Expand` 请求粒度是否允许"只要某几个字段"（现在 admin 用 `*`）。
- 公开 API（小程序）是否一次切换到这个形状，还是先并存。

## 影响面（已测量）

非测试引用点（`FullNode` / `EditableNode` / `.Values`）：`core/relation_read.go`(14)、
`core/merge.go`(7)、`web/admin.go`(4)、`web/read.go`(3)、`core/engine.go`(2)、
`core/query_compile.go`(1) —— 定义点只有一个文件：`core/relation_read.go`。
测试：`core/edge_test.go`、`web/auth_test.go`。

第一刀顺序：`core/relation_read.go`（定义处 → 把 hydrate 做成唯一投影）→ `core/merge.go`
（合并冲突读）→ `core/engine.go`/`core/query_compile.go` → `web/read.go`/`web/admin.go`
（列表出口不再自己拼 expand）。

## ③ 前端改动清单（已测量，逐处）

服务端已完成：`HydrateFields`（core，唯一实现，也在 `core.Engine` 接口上）+ 列表
`web/admin.go:583` 先补全再展开 + 详情 `getNode` 补 Expand（`expandMany` 单节点）→
**列表/详情/编辑器三条路径都是 `Node{Fields(完整), Expand}`**。

前端按此收敛（值仍走 `modelValue`：父组件是唯一改状态的人；`node` 是上下文）：

- `pages/FieldRenderer.vue:53` `:preset="(refPreset||{})[f.name]"` → 传 `node`（props 加 `node`）
- `pages/NodeEditDialog.vue:128-139` 删掉 `/admin/expand` 那次请求 → 详情响应已带 `expand`，
  直接 `this.form.expand = r.expand`；`:ref-preset="…"` → `:node="form"`
- `pages/nodes.vue:79,99` `:expand="expandOf(r, c)"` → `:node="r"`（`expandOf` 可删）
- `widgets/ref.vue:23-24`、`widgets/refs.vue` 删 `preset`/`expand` 两个 prop →
  改读 `(node.expand||{})[field.name]`
- `web/admin.go:425` 路由 + `b.expand`(902) **与前端同一次改动一起删**（不留兼容）
- `_tools/check.js` 加两条闸门：组件里出现 `props.preset`/`props.expand` → FAIL；
  `web/` 里再出现自拼 expand/`/admin/expand` → FAIL

## 读面定稿（12 → 7，参照 PocketBase 的约束方式）

```go
type NodeQuery struct { Filter gquery.Expr; Sort []SortKey }   // 选什么；不含分页
GetNode(ctx, NodeRef) (*Node, error)                // NodeRef = ByID | ByAddress（合一）
GetNodesByIDs(ctx, ids []int64) ([]*Node, error)
GetNodes(ctx, q NodeQuery, limit, offset int) ([]*Node, error)  // limit 0 = 不限
CountNodes(ctx, q NodeQuery) (int64, error)          // 独立读；只吃 q，不可能被分页污染
Expand(ctx, nodes []*Node, paths ...ExpandPath) ([]*Node, error) // paths 空 = 按类型自动
ExpandNode(ctx, n *Node, paths ...ExpandPath) (*Node, error)     // 4 行包装（PB 的 ExpandRecord 同款）
RefIDs(ctx, nodeID, field) ([]int64, error)          // 边级关系读
```
- PB 的启示：读面按「**怎么定位**」组织（id/ids/filter ✓），排序过滤分页是**参数**；expand
  在**读面之外**（读完之后的独立一步 ✓ 吃已加载节点 ✓）；权限靠**注入取数函数**（`ExpandFetchFunc`）
  —— 我们现在没有第二个策略调用方，按"不预埋"原则先不加，等真调用方出现再进 `Expand` 参数。
- 元素类型统一 `*Node`（单数/复数/展开三者同形，就不用泛型）；泛型在此无解（约束不了导出字段）。
- 删除：`RefID`/`HasRef`（0 生产调用方）、`QueryPage`（拆成 GetNodes + CountNodes）、
  `FullNode`/`FullNodes`（→ GetNode/GetNodesByIDs）、`GetNodeByID`（→ 内部行读）、
  expand 五函数（→ Expand/ExpandNode）、`HydrateFields`（收回内部）、`ListQuery` 改名 `NodeQuery`。

进度：`RefID`/`HasRef` 已删（含 Engine 接口）✓ `go build ./...` 绿 ✓。

## 落地结果（已完成，全绿）

读面最终 7 个（`core.Engine`）：
```go
GetNode(ctx, ref any) (*Node, error)                 // int/int64=id；string 先当 id（纯数字），
                                                     // 查不到再当地址；其它类型 fail-loud；不存在 = ErrNotFound
GetNodesByIDs(ctx, ids []int64) ([]*Node, error)
GetNodes(ctx, q NodeQuery, limit, offset int) ([]*Node, error)   // limit 0 = 不限
CountNodes(ctx, q NodeQuery, countLimit int) (int64, error)      // 0 = DefaultCountLimit；CountExact = 精确
RefIDs(ctx, nodeID, field) ([]int64, error)          // 只认多引用字段，别的 fail-loud
Expand(ctx, nodes []*Node, paths ...ExpandPath) ([]*Node, error) // 空 paths = 按类型自动
ExpandNode(ctx, n *Node, paths ...ExpandPath) (*Node, error)
```
- `NodeQuery`：Type/Where/Scope/Sort（**不含分页、不含展开**）；`ListQuery`/`QueryPage`/`Query`/
  `FullNode`/`FullNodes`/`GetNodeByID`/`GetNodeByAddress`/`RefID`/`HasRef`/`HydrateFields`/
  expand 五函数 全部消失。`GetNodeByID`→内部 `nodeRow`，`GetNodeByAddress`→内部 `nodeRowByAddress`。
- 内部读：`nodeRow` / `nodeRowByAddress`（只给写入路径与索引重建用，不导出）。
- Web 层：`ReadPage(action, q, limit, offset)` / `ReadOne` / `ReadAddress` / `ReadSearch` / `ReadTree`；
  `ReadFull` 删除（每个读都带完整 Fields）；`ReadPage`/`ReadOne`/`ReadAddress` **自动展开一层**再掩码。
- 掩码：展开出来的目标也过同一套读规则；目标类型对该动作**没有读规则**时**整条引用不下发**
  （fail-closed —— 不因为展开而泄漏，也不让整个读 500）。
- 行为变化：单节点读不存在从 `(nil, nil)` 变成 `ErrNotFound`（调用方按 404 处理）。
