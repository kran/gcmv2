# 后台 UI 校验

admin 没有构建步骤：`.vue` 由 `vue3-sfc-loader` 在浏览器里现编译，模板或脚本写错
只有打开页面才发现。这两个校验用仓库自带的 `vendor/vue.global.prod.js` +
`vendor/vue3-sfc-loader.js`（与浏览器同一套运行时）在 node 里跑，**不需要 npm install**。

```bash
node web/admin/_tools/check.js          # 编译全部 pages/*.vue
node web/admin/_tools/check.js render   # 挂载渲染 + 交互回归
```

| 模式 | 覆盖 |
| --- | --- |
| `sfc` | 每个页面都能被 SFC 加载器编译：模板/脚本语法、相对 import 能否解析 |
| `render` | 自建渲染器 + `el-*` 桩件真实挂载组件（不需要浏览器），断言结构性回归：<br>① 节点表单里手写的 display 行与 `FieldRenderer` 的字段行结构同构（结构在两边各写一份，靠这条比对防漂移）；<br>② 字段行按 schema 渲染，嵌套 `object` 不会多出行；<br>③ 分类过滤选中/清除都显式重置 el-tree 高亮（`current-node-key` 只在初始化时生效） |

改完后台 UI 跑一遍，退出码非 0 即失败。

目录以 `_` 开头，`//go:embed admin` 不会把它打进二进制（Go embed 忽略 `_`/`.` 开头的路径）。
新增结构性约束（例如新的复合字段行结构）时，把断言加在 `checkRender` 里 —— 没有构建步骤的
前端只有这里能拦住回归。
