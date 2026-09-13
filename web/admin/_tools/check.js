#!/usr/bin/env node
/*
 * 后台 UI 校验 — gcm 的 admin 是无构建 SFC（vue3-sfc-loader 在浏览器里现编译），
 * 改了 .vue 没有编译器兜底：模板写错只有打开页面才炸。这个脚本用仓库自带的
 * vendor/vue.global.prod.js + vendor/vue3-sfc-loader.js（与浏览器同一套运行时）
 * 在 node 里跑校验，不需要 npm install。
 *
 *   node web/admin/_tools/check.js            # 编译全部 pages/*.vue
 *   node web/admin/_tools/check.js render     # 挂载渲染 + 过滤行为回归
 *
 * 退出码非 0 = 有失败项。web/admin/_tools 以 _ 开头，不会被 //go:embed 打进二进制。
 */
'use strict'

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const ADMIN_DIR = path.join(__dirname, '..')
const PAGES_DIR = path.join(ADMIN_DIR, 'pages')

function read(file) { return fs.readFileSync(file, 'utf8') }

// ── 与浏览器等价的运行时（Vue + SFC 加载器 + 最小 DOM 桩） ──────────────
function loadRuntime() {
    const elem = () => ({
        style: {}, setAttribute() {}, appendChild() {}, removeChild() {},
        classList: { add() {} }, textContent: '', innerHTML: '', children: [],
    })
    const document = {
        head: elem(), body: elem(), createElement: elem, createElementNS: elem,
        createTextNode: () => ({ textContent: '' }), querySelector: () => null,
        querySelectorAll: () => [], documentElement: elem(),
        addEventListener() {}, getElementById: () => null,
    }
    const sandbox = {
        document, navigator: { userAgent: 'node' }, location: { href: 'http://localhost/admin/ui/' },
        console, setTimeout, clearTimeout, Promise, URL,
        fetch: () => Promise.reject(new Error('check.js 不联网')),
    }
    sandbox.window = sandbox
    sandbox.self = sandbox
    sandbox.globalThis = sandbox
    vm.createContext(sandbox)
    vm.runInContext(read(path.join(ADMIN_DIR, 'vendor/vue.global.prod.js')), sandbox, { filename: 'vue.js' })
    vm.runInContext(read(path.join(ADMIN_DIR, 'vendor/vue3-sfc-loader.js')), sandbox, { filename: 'loader.js' })
    const Vue = sandbox.Vue
    const loadModule = sandbox['vue3-sfc-loader'].loadModule

    // 模块 id 用 /pages/x.vue 形态（与浏览器一致：相对 import 靠它解析）
    const options = () => ({
        moduleCache: {
            vue: Vue,
            'vue-router': { useRouter: () => ({}), useRoute: () => ({}), createRouter: () => ({}), createWebHashHistory: () => ({}) },
            '$api': {},
        },
        async getFile(url) {
            const file = path.join(ADMIN_DIR, url.replace(/^\//, ''))
            return { getContentData: () => Promise.resolve(read(file)) }
        },
        addStyle() {},
        log(type, scope, message) { if (type === 'error') console.error('   [' + scope + ']', message) },
    })
    return { Vue, loadComponent: (rel) => loadModule(rel, options()) }
}

function pageList() {
    return fs.readdirSync(PAGES_DIR).filter(f => f.endsWith('.vue')).sort()
}

// ── ① 编译校验：每个页面都能被 SFC 加载器编译 ────────────────────────
async function checkSFC() {
    const { loadComponent } = loadRuntime()
    let failed = 0
    for (const name of pageList()) {
        const rel = '/pages/' + name
        try {
            const mod = await loadComponent(rel)
            const comp = mod.default || mod
            if (!(comp.render || comp.template || comp.setup)) throw new Error('没有 render/template/setup')
            console.log('  ok   ' + rel.padEnd(34) + (comp.name || ''))
        } catch (err) {
            failed++
            console.log('  FAIL ' + rel.padEnd(34) + String(err.message).split('\n')[0])
        }
    }
    return failed
}

// ── ② 挂载渲染 + 行为回归 ───────────────────────────────────────────
// 无浏览器挂载：自建渲染器 + el-* 桩件，只关心结构/类名与调用序列。
function makeRenderer(Vue) {
    const node = (tag) => ({ tag, children: [], props: {}, parent: null })
    const ops = {
        createElement: node,
        createText: (text) => ({ tag: '#text', text, children: [], props: {}, parent: null }),
        createComment: (text) => ({ tag: '#comment', text, children: [], props: {}, parent: null }),
        setText: (n, text) => { n.text = text },
        setElementText: (n, text) => { n.text = text },
        insert(child, parent, anchor) {
            child.parent = parent
            const i = anchor ? parent.children.indexOf(anchor) : -1
            if (i < 0) parent.children.push(child)
            else parent.children.splice(i, 0, child)
        },
        remove(child) {
            const p = child.parent
            if (p) p.children.splice(p.children.indexOf(child), 1)
        },
        parentNode: (n) => n.parent,
        nextSibling(n) {
            const p = n.parent
            return p ? p.children[p.children.indexOf(n) + 1] : null
        },
        patchProp(el, key, prev, next) { el.props[key] = next },
        querySelector: () => null,
        setScopeId() {},
        cloneNode: (n) => n,
    }
    return {
        renderer: Vue.createRenderer(ops),
        node,
        // el-* / 扩展控件桩件：保留 tag 便于断言结构
        stub: (name) => ({
            name,
            props: ['modelValue'],
            setup(props, { slots }) {
                return () => Vue.h(name, { value: props.modelValue }, slots.default ? slots.default() : [])
            },
        }),
        stubs: ['el-input', 'el-form', 'el-form-item', 'el-button', 'el-select', 'el-option',
            'el-input-number', 'el-date-picker', 'el-switch', 'el-icon', 'el-divider'],
    }
}

function walk(n, out = []) { out.push(n); (n.children || []).forEach(c => walk(c, out)); return out }

async function checkRender() {
    const { Vue, loadComponent } = loadRuntime()
    const { renderer, node, stub, stubs } = makeRenderer(Vue)
    let failed = 0
    const fail = (msg) => { failed++; console.log('  FAIL ' + msg) }
    const pass = (msg) => console.log('  ok   ' + msg)

    // 行结构（.fr-item > .fr-label + 控件）— 字段行与节点表单里手写的 display 行必须一致
    const shapeOf = (row) => {
        const label = (row.children || []).find(c => c.props && c.props.class === 'fr-label') || { children: [] }
        return {
            classes: row.props.class,
            spanClasses: label.children.filter(c => c.tag === 'span').map(c => c.props.class || ''),
            texts: label.children.filter(c => c.tag === 'span').map(c => c.text || ''),
            control: ((row.children || []).find(c => c.tag && c.tag.indexOf('el-') === 0) || {}).tag,
        }
    }
    const mount = (comp, props, components) => {
        const host = node('#root')
        const app = renderer.createApp(comp, props)
        components.forEach(name => app.component(name, stub(name)))
        app.mount(host)
        return host
    }
    const noReq = (s) => JSON.stringify(s.spanClasses.filter(c => c !== 'fr-req'))
    const printRow = (tag, s) => console.log('       ' + tag + ': ' + JSON.stringify(s))

    // ① FieldRenderer 渲染字段行（含嵌套 object，不应多出行来）
    const FieldRenderer = await loadComponent('/pages/FieldRenderer.vue')
    const fields = [
        { name: 'title', kind: 'text', label: '标题', required: true },
        { name: 'position', kind: 'number', label: '排序' },
        { name: 'meta', kind: 'object', label: '元信息', fields: [{ name: 'note', kind: 'text', label: '备注' }] },
    ]
    const fieldHost = mount(FieldRenderer.default || FieldRenderer,
        { fields, modelValue: { title: 'T', position: 1, meta: { note: 'N' } } }, stubs)
    const fieldRows = walk(fieldHost).filter(n => n.tag === 'div' && n.props.class === 'fr-item').map(shapeOf)
    fieldRows.forEach((s, i) => printRow('字段行' + i, s))
    if (fieldRows.length !== 4) fail('字段行数 = ' + fieldRows.length + '（期望 3 字段 + 1 嵌套）')
    else pass('字段行按 schema 渲染（含嵌套 object）')

    // ② NodeEditDialog 里的 display 行（手写）必须与字段行同构
    const NodeEditDialog = await loadComponent('/pages/NodeEditDialog.vue')
    const dialogHost = mount(NodeEditDialog.default || NodeEditDialog, {
        visible: true, isEdit: false, typeName: 'article',
        defs: { article: { fields } },
    }, stubs.concat(['el-drawer']))
    const dialogRows = walk(dialogHost).filter(n => n.tag === 'div' && n.props.class === 'fr-item')
    if (dialogRows.length !== 1) fail('节点表单里 .fr-item 行数 = ' + dialogRows.length + '（display 行应恰好 1 行）')
    else {
        const display = shapeOf(dialogRows[0])
        printRow('display', display)
        const ref = fieldRows.find(r => r.control === 'el-input' && r.texts[1] === 'text')
        if (display.texts[0] !== '显示' || display.texts[1] !== 'display') fail('display 行 label/kind 不对')
        else if (display.classes !== ref.classes || display.control !== ref.control || noReq(display) !== noReq(ref)) {
            fail('display 行结构与字段行不一致（应与 ' + JSON.stringify(ref) + ' 同构）')
        } else if (display.spanClasses.indexOf('fr-req') < 0) fail('display 为必填, 应带 * 标记')
        else pass('节点表单 display 行与字段行同构（label/kind/必填 + 同一控件）')
    }

    // ② nodes.vue 分类过滤: 选中/清除都要显式 setCurrentKey（el-tree 只在初始化读 current-node-key）
    const NodesPage = await loadComponent('/pages/nodes.vue')
    const methods = (NodesPage.default || NodesPage).methods
    const calls = []
    const fake = {
        query: { page: 7 },
        $refs: { 'tree-category': [{ setCurrentKey(key) { calls.push(key) } }] },
        setTreeCurrent: methods.setTreeCurrent,
        collectSubtree: methods.collectSubtree,
        combineTreeFilters: methods.combineTreeFilters,
        titleOf: () => '新闻',
        refresh() {},
    }
    const ft = { field: 'category', active: 0, activeLabel: '', _ids: null }
    fake.filterTrees = [ft]
    methods.onTreeClick.call(fake, ft, { id: 9, children: [{ id: 10 }] })
    const selected = { key: calls[0], filter: fake.query.filter, active: ft.active }
    methods.clearTreeFilter.call(fake, ft)
    const cleared = { keys: calls.slice(), filter: fake.query.filter, active: ft.active, ids: ft._ids }
    console.log('       选中: ' + JSON.stringify(selected) + '  清除: ' + JSON.stringify(cleared))
    if (selected.key !== 9 || selected.filter !== '(in ->category [9 10])') fail('选中分类没有同步 el-tree 高亮/过滤串')
    else if (cleared.keys[1] !== null || cleared.filter !== '' || cleared.active !== 0 || cleared.ids !== null) fail('点“全部”后旧分类高亮未重置')
    else pass('分类过滤: 选中与清除都重置了 el-tree 高亮')

    // ③ 左侧类型列表: 没填 admin.group 的排最前（不归入"其他"），组按首次出现，label 缺省回退类型名
    const grouped = methods.buildTypeGroups.call({
        typeNames: ['article', 'industry', 'event', 'member', 'category'],
        typeDefs: {
            article: { admin: { label: '文章', group: '内容' } },
            industry: { admin: { label: '行业', group: '分类字典' } },
            event: { admin: { label: '活动' } },        // 没填 group
            member: { admin: {} },                       // 连 label 都没填
            category: { admin: { label: '分类', group: '分类字典' } },
        },
    })
    const shape = grouped.map(g => (g.name || '(未分组)') + ':' + g.items.map(i => i.label).join('/'))
    console.log('       ' + JSON.stringify(shape))
    const ungrouped = grouped[0]
    const first = ungrouped && ungrouped.name === '' ? ungrouped.items.map(i => i.name) : []
    if (first.length !== 2 || !first.includes('event') || !first.includes('member')) {
        fail('没填 group 的类型没有排在最前')
    } else if (grouped[1].name !== '内容' || grouped[2].name !== '分类字典' || grouped.length !== 3) {
        fail('分组顺序/数量不对（应按首次出现）')
    } else if (grouped[1].items[0].label !== '文章') {
        fail('admin.label 没有生效')
    } else if (!grouped[0].items.some(i => i.name === 'member' && i.label === 'member')) {
        fail('admin.label 缺省没有回退类型名')
    } else if (!grouped[1].items.every(i => i.name && i.label)) {
        fail('列表项要同时带上类型键与显示名（模板在名字后附英文键）')
    } else {
        pass('类型列表: 未分组在最前 + 分组顺序 + label 回退')
    }
    return failed
}

// ── 入口 ────────────────────────────────────────────────────────────
async function main() {
    const mode = process.argv[2] || 'sfc'
    if (mode !== 'sfc' && mode !== 'render') {
        console.error('用法: node web/admin/_tools/check.js [sfc|render]')
        process.exit(2)
    }
    console.log(mode === 'sfc' ? '编译校验 (' + pageList().length + ' 个页面)' : '渲染回归')
    const failed = mode === 'sfc' ? await checkSFC() : await checkRender()
    console.log(failed ? failed + ' 项失败' : '全部通过')
    process.exit(failed ? 1 : 0)
}

main().catch((err) => { console.error(err); process.exit(1) })
