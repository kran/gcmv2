#!/usr/bin/env node
/* 闸门体检：把每个闸门**弄坏一次**，确认 check.js 真的以退出码 1 结束。
 *
 * 为什么需要它：闸门返回的失败数要汇总成退出码，一旦某一步返回 undefined（例如函数
 * 忘了 return），整个加总变成 NaN —— NaN 是 falsy，于是"打印了一堆 FAIL、最后说全部通过、
 * 退出码 0"。闸门就成了摆设，比没有还危险（让人以为验过了）。
 *
 * 做法：整个 web/admin 拷到临时目录（check.js 的 ADMIN_DIR 按自身位置推导），在副本里
 * 破坏一处，跑一次，断言退出码正好是 1 —— 工作区不动。
 *
 * 用法: node web/admin/_tools/selfcheck.js
 */
const fs = require('fs')
const os = require('os')
const path = require('path')
const { spawnSync } = require('child_process')

const ADMIN_DIR = path.resolve(__dirname, '..')
const TMP = path.join(os.tmpdir(), 'admin-selfcheck')

// [说明, 模式, 文件, 原文, 改成]
const CASES = [
    ['静态资源闸门', 'sfc', 'index.html', '<script src="js/widgets.js"></script>', '<script src="js/nope.js"></script>'],
    ['破坏性措辞闸门', 'sfc', 'pages/NodeOps.vue', '永久删除', '删除'],
    ['抽屉关闭闸门', 'sfc', 'pages/NodeEditDialog.vue', ':before-close="requestClose"', ''],
    ['时间控件闸门', 'sfc', 'widgets/timestamp.vue', 'type="datetime"', 'type="datetime" value-format="x"'],
    ['组件样式闸门（组件样式跑回公共表）', 'sfc', 'css/main.less', '.w-cell {', '.w-image { display:flex; }\n.w-cell {'],
    ['页面编译闸门', 'sfc', 'pages/nodes.vue', "name: 'NodesPage'", 'name: NodesPage'],
    ['组件 style 必须纯 CSS（// 注释）', 'sfc', 'widgets/number.vue', '<style>', '<style>\n// 数字：右对齐'],
    ['调度器不许认识 kind 名', 'render', 'pages/FieldRenderer.vue', "f.kind === 'array'", "f.kind === 'array' || f.kind === 'timestamp'"],
    ['cell 必须渲染出值', 'render', 'widgets/text.vue', 'Widgets.truncate(modelValue)', "''"],
    ['引用必须是链接', 'render', 'widgets/ref.vue', 'class="w-ref-link"', 'class="w-plain"'],
    ['点引用要能打开抽屉', 'render', 'pages/nodes.vue', 'typeName: target.type }', "typeName: '' }"],
    ['编辑器链路（穿异步组件）', 'render', 'widgets/text.vue', "this.$emit('update:modelValue', v)", 'void v'],
]

let bad = 0
for (const [name, mode, rel, from, to] of CASES) {
    fs.rmSync(TMP, { recursive: true, force: true })
    fs.cpSync(ADMIN_DIR, TMP, { recursive: true })
    const file = path.join(TMP, rel)
    const src = fs.readFileSync(file, 'utf8')
    if (src.indexOf(from) < 0) {
        console.log('  ?? ' + name + ': 找不到要破坏的文本（' + rel + ': ' + from.slice(0, 40) + '）')
        bad++
        continue
    }
    fs.writeFileSync(file, src.replace(from, to))
    const args = [path.join(TMP, '_tools/check.js')].concat(mode === 'sfc' ? [] : [mode])
    const res = spawnSync('node', args, { encoding: 'utf8' })
    if (res.status === 1) {
        console.log('  ok   ' + name + '（' + mode + '）→ 退出码 1')
    } else {
        bad++
        console.log('  FAIL ' + name + '（' + mode + '）→ 退出码 ' + res.status + '（闸门没拦住）')
        const lines = (res.stdout || '').trim().split('\n')
        console.log('       ' + lines[lines.length - 1])
    }
}
fs.rmSync(TMP, { recursive: true, force: true })
console.log(bad ? '\n' + bad + ' 个闸门没起作用' : '\n' + CASES.length + ' 个闸门都能让命令失败')
process.exit(bad ? 1 : 0)
