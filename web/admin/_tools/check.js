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

// KERNEL_KINDS 内核 types 包声明的 kind（改动 types/*.go 时这里要跟着对）——
// 只用来在检查时提示"哪些 kind 在后台还没有控件"。
const KERNEL_KINDS = ['array', 'bool', 'gallery', 'number', 'object', 'ref', 'richtext',
    'select', 'slug', 'strings', 'text', 'textarea', 'timestamp', 'upload-file', 'upload-image']

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
    return { Vue, sandbox, loadComponent: (rel) => loadModule(rel, options()) }
}

function pageList() {
    return fs.readdirSync(PAGES_DIR).filter(f => f.endsWith('.vue')).sort()
}

// ── ⓿ 静态资源引用：index.html 里写到的文件必须真的存在 ──────────────
// 路径写错不会报错，只是 404 + 静默失效（favicon 没了、样式不生效），所以在这里钉住。
function checkAssets() {
    const html = read(path.join(ADMIN_DIR, 'index.html'))
    const refs = []
    for (const m of html.matchAll(/(?:href|src)="([^"]+)"/g)) {
        const url = m[1]
        if (/^(https?:)?\/\//.test(url) || url.startsWith('data:') || url.startsWith('#')) continue
        refs.push(url.startsWith('/admin/ui/') ? url.slice('/admin/ui/'.length) : url)
    }
    let failed = 0
    for (const rel of refs) {
        if (!fs.existsSync(path.join(ADMIN_DIR, rel))) {
            failed++
            console.log('  FAIL index.html 引用了不存在的文件: ' + rel)
        }
    }
    if (!failed) console.log('  ok   index.html 引用的静态资源都存在（' + refs.length + ' 个）')
    return failed
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
    const { Vue, sandbox, loadComponent } = loadRuntime()
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
    // 渲染期抛错必须让用例失败 —— 不然组件在浏览器里报错, 这里照样是绿的。
    const renderErrors = []
    const mount = (comp, props, components) => {
        const host = node('#root')
        const app = renderer.createApp(comp, props)
        app.config.errorHandler = (err) => { renderErrors.push(err && err.message ? err.message : String(err)) }
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

    // ③ 同上的组件在 nodes.vue 里的真实用法: :is-edit 恒为 true, 而 node 在点开某一行之前是 null。
    renderErrors.length = 0
    mount(NodeEditDialog.default || NodeEditDialog, {
        visible: false, isEdit: true, node: null, typeName: 'article', defs: { article: { fields } },
    }, stubs.concat(['el-drawer']))
    if (renderErrors.length) fail('NodeEditDialog 在 node=null + isEdit=true 下渲染报错: ' + renderErrors[0])
    else pass('NodeEditDialog 在 node=null + isEdit=true 下不报错（nodes.vue 的真实用法）')

    // ③b FieldRenderer 的引用预载：首次打开下拉拉一批；之后（包括用户搜过之后）不再拉 ——
    //     否则打字搜出来的几条会在下次打开时被预载结果覆盖掉。
    const FR = await loadComponent('/pages/FieldRenderer.vue')
    const frMethods = (FR.default || FR).methods
    // ③a 模板里有分支的 kind 必须都登记为内置控件 —— 漏登记的会去拉 ui-extras/<kind>.vue
    //     然后 404（字段本身还能渲染，所以只有控制台报错，很容易漏掉）。
    const frComp = FR.default || FR
    const builtin = frMethods.builtinWidgets.call({})
    const renderSrc = (frComp.render || (() => ({}))).toString()
    const templateKinds = new Set()
    for (const m of renderSrc.matchAll(/kind\s*===\s*['"]([a-z-]+(?:\[\])?)['"]/g)) templateKinds.add(m[1])
    const missing = [...templateKinds].filter(k => builtin.indexOf(k) < 0)
    const noWidget = KERNEL_KINDS.filter(k => builtin.indexOf(k) < 0)
    console.log('       模板 kind: ' + [...templateKinds].sort().join(' '))
    console.log('       声明了 kind 但没有内置控件（会走 ui-extras 或警告块）: ' + noWidget.join(' '))
    if (missing.length) fail('模板有分支但未登记为内置控件: ' + missing.join(' '))
    else pass('模板里的 kind 都登记为内置控件（不会白拉 ui-extras）')

    const searchCalls = []
    sandbox.$api = {
        refLabel: (n) => n.display || ('#' + n.id),
        search: async (params) => { searchCalls.push(params); return { items: [] } },
    }
    const fctx = { refLoaded: {}, refLoading: {}, refOptions: {}, refPreset: {}, defs: {} }
    for (const name of Object.keys(frMethods)) fctx[name] = frMethods[name].bind(fctx)
    const refField = { name: 'category', to: 'category', kind: 'ref' }
    await fctx.preloadRef(refField)
    await fctx.preloadRef(refField)
    await fctx.searchRef(refField, '新闻')
    await fctx.preloadRef(refField)
    const searched = searchCalls.map(c => ({ q: c.q, sort: c.sort || '', type: c.type }))
    console.log('       引用控件调用: ' + JSON.stringify(searched))
    if (searchCalls.length !== 2) fail('引用预载调用次数 = ' + searchCalls.length + '（期望 2: 预载一次 + 打字一次）')
    else if (searched[0].q !== '' || searched[0].sort !== '-id' || searched[0].type !== 'category') {
        fail('预载应带空 q + sort=-id + 目标类型: ' + JSON.stringify(searched[0]))
    } else if (searched[1].q !== '新闻' || searched[1].sort !== '') {
        fail('打字搜索不该带 sort: ' + JSON.stringify(searched[1]))
    } else {
        pass('引用预载: 首次打开拉一批、之后不重复、打字搜索不受影响')
    }

    // ④ nodes.vue 引用筛选: 每个 ref 字段一项。树目标选分类（含子树）、其他目标搜索选节点;
    //    选中/清除都要显式 setCurrentKey（el-tree 只在初始化读 current-node-key），多字段 AND 组合。
    const NodesPage = await loadComponent('/pages/nodes.vue')
    const nodesComp = NodesPage.default || NodesPage
    const methods = nodesComp.methods
    const calls = []
    // 假上下文以组件自己的 data() 为底 —— 不然 data 里少个字段（比如被注释吃掉一行）
    // 这里也照样过, 页面却在浏览器里炸。
    const fake = {
        ...(typeof nodesComp.data === 'function' ? nodesComp.data() : {}),
        query: { ...((typeof nodesComp.data === 'function' ? nodesComp.data() : {}).query || {}), page: 7, filter: '' },
        $refs: {
            'tree-category': [{ setCurrentKey(key) { calls.push(key) } }],
            'fp-category': [{ hide() { calls.push('hide') } }],
        },
        setTreeCurrent: methods.setTreeCurrent,
        collectSubtree: methods.collectSubtree,
        combineFilters: methods.combineFilters,
        applyFilters: methods.applyFilters,
        closeFilterPopover: methods.closeFilterPopover,
        titleOf: () => '新闻',
        refresh() {},
    }
    // 组件的方法统统绑到假上下文上（只补没显式覆盖的）—— 免得每加一个方法就得回来补测试脚手架。
    for (const name of Object.keys(methods)) {
        if (fake[name] === undefined) fake[name] = methods[name].bind(fake)
    }
    // 假上下文里用到的 data 字段, 必须在组件自己的 data() 里真实存在 ——
    // 否则测试自己造了一个组件里根本没有的字段, 页面在浏览器里炸了这里却是绿的。
    const baseData = typeof nodesComp.data === 'function' ? nodesComp.data() : {}
    for (const key of Object.keys(fake)) {
        if (key.startsWith('$') || typeof fake[key] === 'function') continue
        if (!(key in baseData)) throw new Error('nodes.vue 的 data() 缺少 ' + key + '（data() 被改坏了?）')
    }
    const ft = { field: 'category', to: 'category', tree: true, active: 0, activeLabel: '', _ids: null }
    const fr = { field: 'event', to: 'event', tree: false, active: 0, activeLabel: '', _ids: null,
                 options: [{ id: 83, label: '2026 新能源产业对接会 #83' }] }
    fake.filters = [ft, fr]
    methods.pickTreeNode.call(fake, ft, { id: 9, children: [{ id: 10 }] })
    const selected = { key: calls[0], filter: fake.query.filter, active: ft.active, page: fake.query.page }
    methods.pickRef.call(fake, fr, 83)
    const both = fake.query.filter
    methods.clearFilter.call(fake, ft)
    methods.pickRef.call(fake, fr, undefined)
    const cleared = { keys: calls.slice(), filter: fake.query.filter, active: ft.active, ids: ft._ids }
    console.log('       选分类: ' + JSON.stringify(selected) + '\n       再选活动: ' + JSON.stringify(both)
        + '\n       清空: ' + JSON.stringify(cleared))
    if (selected.key !== 9 || selected.filter !== '(in ->category [9 10])' || selected.page !== 1) {
        fail('树目标选中没有同步 el-tree 高亮/过滤串/回到第 1 页')
    } else if (both !== '(and (in ->category [9 10]) (in ->event [83]))') {
        fail('多字段引用筛选没有 AND 组合')
    } else if (!cleared.keys.includes(null) || cleared.filter !== '' || cleared.active !== 0 || cleared.ids !== null) {
        fail('点“全部”后旧分类高亮未重置')
    } else {
        pass('引用筛选: 树目标含子树、其他目标单选节点、多字段 AND、清除后高亮重置')
    }

    // ④ 切换类型要清掉上一个类型的查询残留: 搜索框(q)/筛选表达式(filter)/页码
    fake.query.q = '上一个类型的搜索词'
    fake.query.filter = '(in ->category [9])'
    fake.query.page = 7
    methods.selectType.call(fake, 'signup')
    if (fake.query.q !== '' || fake.query.filter !== '' || fake.query.page !== 1) {
        fail('切换类型后查询残留: ' + JSON.stringify({ q: fake.query.q, filter: fake.query.filter, page: fake.query.page }))
    } else {
        pass('切换类型清空搜索词/筛选/页码')
    }

    // ⑤ 左侧类型列表: 组内按类型键排序；没填 group 的与站点自命的"未分组"并成同一节，且排最前
    const grouped = methods.buildTypeGroups.call({
        typeNames: ['article', 'banner', 'category', 'event', 'industry', 'member', 'page', 'supply'],
        typeDefs: {
            article: { admin: { label: '文章', group: '内容' } },
            banner: { admin: { group: '内容' } },                  // 没填 label → 回退类型键
            category: { admin: { label: '分类', group: '基础数据' } },
            event: { admin: { label: '活动' } },                   // 没填 group
            industry: { admin: { label: '行业', group: '基础数据' } },
            member: { admin: { label: '会员单位' } },              // 没填 group
            page: { admin: { label: '单页', group: '未分组' } },    // 站点自己就叫"未分组"
            supply: { admin: { label: '供需', group: '内容' } },
        },
    })
    const shape = grouped.map(g => g.name + ':' + g.items.map(i => i.label).join('/'))
    console.log('       ' + JSON.stringify(shape))
    const keys = (g) => grouped[g] ? grouped[g].items.map(i => i.name).join() : '(缺组)'
    if (grouped[0].name !== '未分组' || keys(0) !== 'event,member,page') {
        fail('"未分组"没有合并成一节并排在最前')
    } else if (grouped[1].name !== '内容' || keys(1) !== 'article,banner,supply') {
        fail('组内没有按类型键排序')
    } else if (grouped[2].name !== '基础数据' || keys(2) !== 'category,industry' || grouped.length !== 3) {
        fail('分组顺序/数量不对（应按首次出现）')
    } else if (grouped[1].items.find(i => i.name === 'banner').label !== 'banner') {
        fail('admin.label 缺省没有回退类型名')
    } else {
        pass('类型列表: 未分组合并且最前 + 组内按类型键 + label 回退')
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
    const failed = checkAssets() + (mode === 'sfc' ? await checkSFC() : await checkRender())
    console.log(failed ? failed + ' 项失败' : '全部通过')
    process.exit(failed ? 1 : 0)
}

main().catch((err) => { console.error(err); process.exit(1) })
