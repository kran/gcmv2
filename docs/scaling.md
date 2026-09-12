# 规模与容量（实测）

gcm 的数据量能顶到哪、瓶颈在哪，都靠 `core/scale_bench_test.go` 实测，不靠外推。

```bash
# 走 API 写路径建数据集 + 全量读测（1000 / 10000 / 100000 节点）
GCM_SCALE=1000,10000,100000 go test -run TestScale -v -timeout 60m ./core/

# 用单事务直插建数据集（API 写路径太慢），只测读
GCM_SCALE=1000000 GCM_SCALE_BULK=1 go test -run TestScale -v -timeout 90m ./core/
```

不设 `GCM_SCALE` 时该测试跳过，日常 `go test ./...` 不受影响。

## 数据集形状

CMS 形态，避免误读数字：分类 1/200 比例（最多 2000 个，含三层 parent 树）、文章为 主体，
每篇 ~1.5KB 中文正文 + title/excerpt/position/publish_time/views + 一个 `ref[]` 分类，
`searchable: [title, body]`（display 天生可搜，不必声明）、`addressable: {field: slug}`、`publication` 齐全，
并声明 `indexes: [[publication_state, publish_time]]`。写入走真实 `CreateNode`（含边与 FTS 同步）。

## 结果（p50，本机 macOS + SSD）

| 指标 | 1k | 10k | 100k | 1M |
| --- | --- | --- | --- | --- |
| 写入 / 节点 | 676 µs | 712 µs | 743 µs | 1.03 ms |
| 库体积 / 节点 | 19.0 KB | 7.3 KB | 6.8 KB | 6.8 KB |
| 主库 | 14.5 MB | 64.7 MB | 649 MB | 6.5 GB |
| 主键详情 | 47 µs | 49 µs | 48 µs | 302 µs |
| 地址查询（slug） | 93 µs¹ | 99 µs | 96 µs | 422 µs |
| 引用读出（ref[]） | 101 µs | 105 µs | 106 µs | 1.05 ms |
| 分类树加载 | 175 µs | 318 µs | 2.5 ms | 9.9 ms |
| 子树遍历 | 73 µs | 64 µs | 75 µs | 87 µs |
| 列表页（不带 total） | — | 744 µs | 758 µs | 736 µs |
| 列表页（带 total） | 923 µs | 2.55 ms | 2.49 ms | 2.46 ms |
| 深翻页（第 100 页） | 348 µs | 2.84 ms | 2.83 ms | 3.02 ms |
| 检索·稀有词（命中 1/1000） | 145 µs | 252 µs | 352 µs | 969 µs |
| 子树过滤（默认计数） | 2.1 ms | 3.5 ms | 35.8 ms | 310 ms |
| 子树过滤（只取页, CountLimit 10） | — | — | 14.1 ms | — |
| 检索·命中全库 | 24 ms | 172 ms | 1.66 s | 44.8 s |
| 无索引字段排序 | — | 65 ms | 608 ms | 30.9 s |
| 重建全文索引（一次性） | 464 ms | 2.66 s | 29 s | 13 m 37 s |
| 并发 1 写 + 4 读（读 p50） | 1.76 ms | 3.67 ms | 3.22 ms | 19.1 ms |

¹ 1k 列填的是 10k 的值（小规模下该查询的固定开销占主导）。

## 三点必须理解

**1. “常数级”指与表大小无关，但不等于与缓存无关。** 1M 节点时主键详情从 48µs 变成
302µs、引用读出从 106µs 变成 1.05ms：6.5GB 库超出页缓存，索引页与 2KB 的 JSON 行更容易缺页。
写侧同理（743µs → 1.03ms）。服务器内存更大 / 盘更快会更接近常数，更差则更慢。

**2. 只有三类查询随规模线性增长**，其余路径在 1M 仍是亚毫秒到毫秒级：

| 查询 | 1M 实测 | 原因 | 处理 |
| --- | --- | --- | --- |
| 检索·命中全库 | 44.8 s | 每条命中都要回表做 scope 过滤 + bm25 打分 | 收窄命中集；或把发布状态推进 FTS 索引（改索引结构）；换时间序只省 12% |
| 无索引字段排序 | 30.9 s | 无索引 → 全表扫 + 排序 | 声明 `constraints.indexes`，或不给用户这个排序入口 |
| 子树过滤 | 310 ms | 命中集大：取页 ∝ 命中数，计数 ∝ min(命中数, CountLimit) | 大分类的列表传小 `CountLimit`（100k 实测 35.8ms → 14.1ms） |

**3. 重建全文索引是升级窗口的主要成本**：100k 29s、1M 13m37s。它只在
“本次启动应用了迁移”时触发（`core.Open` 检测到迁移数 > 0），不是每次启动。

## 启动成本：声明式索引每次启动重建

`core.Open` → `syncSchemaIndexes` 会**先删光所有 `gcm_schema_%` 索引再按当前 Schema 建**，
所以改了 `types.yaml` 的 `constraints.indexes` / `constraints.unique` / `addressable` 后
**重启即生效**（换字段、加、删都会同步，不会残留旧索引；`core/schema_indexes_test.go`
的 `TestSchemaIndexesFollowTypes` 钉住这个行为）。

代价是每次启动都白做一遍：100k 节点实测 4 个索引 `DROP`+`CREATE` 合计 2.2s
（`i_article_0` 948ms、`address_global` 608ms、`address_article` 582ms、`address_category` 37ms），
1M 节点约 20s 量级。

## 规模建议

- **≤ 10 万节点**：全线可用，最慢是“命中全库的检索”（1.7s）。
- **10 万 ~ 100 万**：可用，但需要三条产品约束：不给未索引字段排序、大分类列表传小
  `CountLimit`、搜索收窄命中集（限定分类/时间）。
- **≥ 100 万**：再加专用搜索引擎（Meilisearch/Typesense 等）；其余路径仍够用 ——
  6.8KB/节点（100 万 ≈ 6.5GB）、~1000 节点/s 写入、1 写 + 4 读并发无锁冲突。
- 商协会 / 企业站（200 ~ 1000 节点）距边界三个数量级，不需要任何约束。

## 备注

- 绝对数字是本机（macOS、SSD、现代 Go SQLite 驱动）实测，服务器会有差异；
  **哪些查询随规模线性增长是结构性的**（索引是否被用上），换机器不会变。
- 报告里同时打印引擎实际生成的 SQL 的 `EXPLAIN QUERY PLAN`，以及针对每个瓶颈的
  候选 SQL 实测（改之前就知道能快多少）。
