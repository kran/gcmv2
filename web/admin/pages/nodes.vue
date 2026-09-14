<template>
  <div class="nodes-page">
    <!-- 左侧 type 列表（类型定义驱动, 动态） -->
    <div class="nodes-tree">
      <!--<div class="nodes-tree-header">
        <span style="font-size:14px;">类型</span>
        <el-button link type="primary" size="small" @click="loadTypes">刷新</el-button>
      </div>-->
      <div class="type-list">
        <template v-for="g in typeGroups" :key="g.name">
          <div class="type-group">{{ g.name }}</div>
          <div v-for="item in g.items" :key="item.name"
               class="type-item" :class="{ active: query.type === item.name }"
               @click="selectType(item.name)">
            <el-icon :size="15"><component :is="typeIcon(item.name)" /></el-icon>
            <span>{{ item.label }}</span>
            <span v-if="item.label !== item.name" class="type-key">{{ item.name }}</span>
          </div>
        </template>
      </div>
    </div>

    <!-- 右侧列表 -->
    <div class="nodes-list">
      <div style="display:flex;gap:8px;align-items:center;margin-bottom:14px;flex-wrap:wrap;">
        <span style="font-size:16px;">{{ query.type || '未选择类型' }}</span>
        <el-input v-model="query.q" placeholder="搜索显示名称" size="small" style="width:180px"
                  clearable @change="refresh" />
        <!-- 引用筛选：类型的每个 ref 字段一项。目标类型是树（capabilities.tree）→ 选分类含子树；
             其他引用 → 与编辑表单同款的可搜索选择（远程搜节点）。 -->
        <el-popover v-for="f in filters" :key="f.field" trigger="click" placement="bottom-start"
                    :show-timeout="0" :hide-timeout="0"
                    :width="f.tree ? 200 : 280" style="margin-left:8px;" :ref="'fp-' + f.field"
                    @show="onFilterOpen(f)">
          <template #reference>
            <el-button link size="small" class="filter-link" :class="{ active: f.active }">
              {{ f.active ? (f.activeLabel + ' ✕') : ('按' + f.label + '筛选') }}
            </el-button>
          </template>
          <div v-if="f.tree" style="max-height:300px;overflow:auto;">
            <div class="type-item" :class="{ active: f.active === 0 }" @click="clearFilter(f)">
              <span>全部</span>
            </div>
            <el-tree :ref="'tree-' + f.field" :data="f.nodes" node-key="id" default-expand-all
                     :expand-on-click-node="false" highlight-current
                     :current-node-key="f.active" @node-click="(n) => pickTreeNode(f, n)">
              <template #default="{ data }">
                <span class="tree-node-label" style="font-size:13px;">{{ titleOf(data) }}</span>
              </template>
            </el-tree>
          </div>
          <div v-else>
            <el-select :model-value="f.active || null" filterable remote clearable
                       :remote-method="(q) => searchFilterRef(f, q)" :loading="f.loading"
                       placeholder="搜索并选择" style="width:100%"
                       @update:model-value="(v) => pickRef(f, v)">
              <el-option v-for="o in f.options" :key="o.id" :label="o.label" :value="o.id" />
            </el-select>
          </div>
        </el-popover>
        <el-button size="small" @click="refresh"><el-icon><Refresh /></el-icon>刷新</el-button>
        <div style="flex:1;"></div> <!-- 右侧靠拢 -->
        <el-button size="small" :loading="rebuilding" @click="rebuildSearch"><el-icon><Refresh /></el-icon>重建索引</el-button>
        <el-button type="primary" size="small" :disabled="!query.type" @click="createVisible = true">
          <el-icon><Plus /></el-icon>新建 {{ query.type ? typeLabel(query.type) : '' }}
        </el-button>
      </div>

      <!-- 树视图（view: tree 类型, 全量不分页; el-table 树形模式, 行操作: 编辑/新建子/删除） -->
      <el-table v-if="treeMode" :data="treeNodes" v-loading="loading" row-key="id"
                :tree-props="{ children: 'children' }" default-expand-all >
        <el-table-column label="标题" min-width="260" show-overflow-tooltip>
          <template #default="{ row: r }"><a class="node-title-link" @click.prevent="openEdit(r)">{{ titleOf(r) }}</a></template>
        </el-table-column>
        <el-table-column v-for="c in adminColumns" :key="c" :label="fieldLabel(c)" min-width="130" show-overflow-tooltip>
          <template #default="{ row: r }">{{ fieldValue(r, c) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="300" fixed="right">
          <template #default="{ row: r }">
            <node-ops :node="r" :defs="typeDefs" :type-name="query.type" show-create
                      :parent-id="r.id" @changed="refresh" />
          </template>
        </el-table-column>
      </el-table>

      <el-table v-else :data="rows" v-loading="loading">
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column label="标题" min-width="260" show-overflow-tooltip>
          <template #default="{ row: r }"><a class="node-title-link" @click.prevent="openEdit(r)">{{ titleOf(r) }}</a></template>
        </el-table-column>
        <el-table-column v-for="c in adminColumns" :key="c" :label="fieldLabel(c)" min-width="130" show-overflow-tooltip>
          <template #default="{ row: r }">{{ fieldValue(r, c) }}</template>
        </el-table-column>
        <el-table-column label="更新时间" width="165">
          <template #default="{ row: r }">{{ fmt(r.updated_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="200" fixed="right">
          <template #default="{ row: r }">
            <node-ops :node="r" :defs="typeDefs" :type-name="query.type" @changed="refresh" />
          </template>
        </el-table-column>
      </el-table>

      <div v-if="!treeMode" style="display:flex;justify-content:flex-end;margin-top:12px;">
        <el-pagination background layout="total, prev, pager, next" :total="total"
                       :page-size="query.size" :current-page="query.page"
                       @current-change="onPageChange" />
      </div>
      <!-- 页面级新建（行编辑走 NodeOps） -->
      <node-edit-dialog v-model:visible="createVisible" :type-name="query.type" :defs="typeDefs"
                        @changed="refresh" />
      <!-- 标题链接编辑（列表标题点击 → 编辑对话框） -->
      <node-edit-dialog v-model:visible="editVisible" :node="editNode" :type-name="query.type"
                        :is-edit="true" :defs="typeDefs" @changed="refresh" />
    </div>




  </div>
</template>

<script>
import FieldRenderer from './FieldRenderer.vue'
export default {
    name: 'NodesPage',
    components: {
        FieldRenderer,
        NodeOps: Vue.defineAsyncComponent(() => window.Panel.loadComponent('pages/NodeOps.vue')),
        NodeEditDialog: Vue.defineAsyncComponent(() => window.Panel.loadComponent('pages/NodeEditDialog.vue')),
    },
    data() {
        return {
            typeNames: [],
            typeDefs: {},
            rows: [],
            total: 0,
            loading: false,
            treeMode: false,
            treeNodes: [],
            parentField: 'parent',
            filters: [],       // 引用筛选: [{field, to, label, tree, nodes, options, active, activeLabel}]
            query: { type: '', q: '', page: 1, size: 25 },
            rebuilding: false,
            createVisible: false,
            editVisible: false,
            editNode: null,
        }
    },
    computed: {
        // 左侧类型列表：没填 admin.group 的排最前（不归入"其他"），其余按首次出现的顺序
        typeGroups() {
            return this.buildTypeGroups()
        },
        adminColumns() {
            const def = this.typeDefs[this.query.type] || {}
            return ((def.admin && def.admin.columns) || []).filter(c => !['id', 'display', 'updated_at'].includes(c))
        },
    },
    async mounted() { await this.loadTypes() },
    methods: {
        // 类型的中文名：admin.label 缺省回退类型名（配置键），没填的 types.yaml 照常工作
        typeLabel(name) {
            const admin = (this.typeDefs[name] || {}).admin || {}
            return admin.label || name
        },
        // 分组只作用于左侧类型列表：没填 group 的按名字并进"未分组"（站点自己叫这个名也并进来，
        // 不做特殊处理），这一节固定排最前；组内顺序一律按类型键（typeNames 已是键序），组按首次出现。
        buildTypeGroups() {
            const groups = []
            const byName = {}
            this.typeNames.forEach(name => {
                const admin = (this.typeDefs[name] || {}).admin || {}
                const group = admin.group || '未分组'
                if (!byName[group]) {
                    byName[group] = { name: group, items: [] }
                    groups.push(byName[group])
                }
                byName[group].items.push({ name, label: admin.label || name })
            })
            const at = groups.findIndex(g => g.name === '未分组')
            if (at > 0) groups.unshift(groups.splice(at, 1)[0])
            return groups
        },
        // 标题链接 → 编辑对话框
        openEdit(node) {
            this.editNode = node
            this.editVisible = true
        },
        // 图标来自 Admin View，不参与数据语义。
        typeIcon(t) {
            const def = this.typeDefs[t] || {}
            return (def.admin && def.admin.icon) || 'Files'
        },
        fieldLabel(name) {
            const def = this.typeDefs[this.query.type] || {}
            const field = (def.fields || []).find(f => f.name === name)
            return (field && field.label) || name
        },
        // 单元格: ref/ref[] 的值不在 fields 里（存 edges 表），只能取 expand 里的显示名 ——
        // 列表接口本来就批量展开了一层出边，这里只是把它用上。
        fieldValue(node, name) {
            const def = this.typeDefs[this.query.type] || {}
            const field = (def.fields || []).find(f => f.name === name)
            if (field && (field.kind === 'ref' || field.kind === 'ref[]')) {
                const expanded = (node.expand || {})[name]
                const list = Array.isArray(expanded) ? expanded : (expanded ? [expanded] : [])
                return list.filter(Boolean).map(n => n.display || '#' + n.id).join('、')
            }
            const value = node.fields && node.fields[name]
            if (Array.isArray(value)) return value.join(', ')
            return value === undefined || value === null ? '' : value
        },
        async loadTypes() {
            const res = await window.$api.types()
            this.typeDefs = res.types || {}
            this.typeNames = Object.keys(this.typeDefs).sort()
            // 默认选第一个
            if (!this.query.type && this.typeNames.length) this.selectType(this.typeNames[0])
        },
        selectType(t) {
            this.query.type = t
            this.query.page = 1
            this.query.filter = '' // 类型切换清残留（旧类型的字段对不上新类型, fail-loud 报错）
            const def = this.typeDefs[t] || {}
            this.treeMode = !!(def.admin && def.admin.view === 'tree')
            this.setupFilters(def)
            if (this.treeMode) this.loadTree()
            else this.refresh()
        },
        // 树父字段由 capability 明确声明。
        selfRefField(def) {
            return def && def.capabilities && def.capabilities.tree
                ? def.capabilities.tree.parent : ''
        },
        async loadTree() {
            if (!this.query.type) return
            this.loading = true
            try {
                const res = await window.$api.get('/admin/tree', { type: this.query.type })
                this.parentField = this.selfRefField(this.typeDefs[this.query.type]) || 'parent'
                this.treeNodes = this.buildTree(res.items || [], this.parentField)
            } finally { this.loading = false }
        },
        // 引用筛选：类型的每个 ref/ref[] 字段一项（自引用除外 —— 那种类型的列表本身就是树）。
        // 目标类型声明了 tree capability → 用分类树选（含子树，多字段 AND）；
        // 其他目标 → 与编辑表单同款的远程搜索选择（一个节点）。
        setupFilters(def) {
            this.filters = []
            if (!def) return
            const name = def.name
            for (const f of def.fields || []) {
                if (f.kind !== 'ref' && f.kind !== 'ref[]') continue
                if (f.to === name) continue
                const tdef = this.typeDefs[f.to] || {}
                const item = {
                    field: f.name, to: f.to, label: f.label || f.name,
                    tree: !!this.selfRefField(tdef), nodes: [], options: [],
                    active: 0, activeLabel: '', loading: false, loaded: false,
                }
                this.filters.push(item)
                if (item.tree) this.loadFilterTree(item)
            }
        },
        // 树筛选的数据源（全量, 前端拼树 —— 分类量级小）
        async loadFilterTree(ft) {
            try {
                const res = await window.$api.get('/admin/tree', { type: ft.to })
                const pf = this.selfRefField(this.typeDefs[ft.to]) || 'parent'
                ft.nodes = this.buildTree(res.items || [], pf)
            } catch (_) {}
        },
        // 非树筛选：打开时先给一批（不然得先打字才看得到选项），之后走远程搜索。
        onFilterOpen(f) {
            if (!f.tree && !f.loaded) this.searchFilterRef(f, '')
        },
        async searchFilterRef(f, q) {
            f.loading = true
            try {
                const res = await window.$api.search({ q: q || '', type: f.to, page: 1, size: 50 })
                f.options = (res.items || []).map(n => ({ id: n.id, label: this.titleOf(n) + ' #' + n.id }))
                f.loaded = true
            } catch (_) {
                f.options = []
            } finally {
                f.loading = false
            }
        },
        // 选了一个节点（非树目标）; 清空时 id 为 undefined
        pickRef(f, id) {
            f.active = id || 0
            const hit = (f.options || []).find(o => o.id === id)
            f.activeLabel = hit ? hit.label : '#' + id
            f._ids = id ? [id] : null
            this.applyFilters()
        },
        // 选了分类树里的一个节点: 它和整棵子树都算命中
        pickTreeNode(ft, n) {
            ft.active = n.id
            ft.activeLabel = this.titleOf(n)
            ft._ids = this.collectSubtree(n)
            this.setTreeCurrent(ft, n.id)
            this.closeFilterPopover(ft)
            this.applyFilters()
        },
        clearFilter(ft) {
            ft.active = 0
            ft.activeLabel = ''
            ft._ids = null
            this.setTreeCurrent(ft, null)
            this.applyFilters()
        },
        applyFilters() {
            this.query.filter = this.combineFilters()
            this.query.page = 1
            this.refresh()
        },
        // el-tree 的 current-node-key 只在初始化时生效: 之后选中/清除都得显式 setCurrentKey,
        // 否则选过分类再点“全部”, 旧分类依然亮着。
        setTreeCurrent(ft, key) {
            const ref = this.$refs['tree-' + ft.field]
            const tree = Array.isArray(ref) ? ref[0] : ref
            if (tree && tree.setCurrentKey) tree.setCurrentKey(key)
        },
        closeFilterPopover(ft) {
            const ref = this.$refs['fp-' + ft.field]
            if (ref && ref[0]) ref[0].hide()
        },
        // 多字段 AND 组合: (and (in ->f1 [ids]) (in ->f2 [ids]))
        combineFilters() {
            const parts = []
            for (const f of this.filters) {
                if (f._ids && f._ids.length) {
                    parts.push('(in ->' + f.field + ' [' + f._ids.join(' ') + '])')
                }
            }
            if (parts.length === 0) return ''
            if (parts.length === 1) return parts[0]
            return '(and ' + parts.join(' ') + ')'
        },
        collectSubtree(n) {
            const ids = [n.id]
            const walk = (node) => {
                ;(node.children || []).forEach(c => { ids.push(c.id); walk(c) })
            }
            walk(n)
            return ids
        },
        // 平铺节点列表 → 树（无 parent / parent 缺失 = 根）
        buildTree(items, parentField) {
            const map = {}
            items.forEach(n => { map[n.id] = { ...n, children: [] } })
            const roots = []
            items.forEach(n => {
                const node = map[n.id]
                const pid = n.fields && n.fields[parentField]
                const parent = pid && map[pid]
                if (parent) parent.children.push(node)
                else roots.push(node)
            })
            return roots
        },
        async refresh() {
            if (!this.query.type) return
            this.loading = true
            try {
                const params = { type: this.query.type, page: this.query.page, size: this.query.size, sort: '-id' }
                if (this.query.q) params.q = this.query.q
                if (this.query.filter) params.filter = this.query.filter
                const res = await window.$api.nodes(params)
                this.rows = res.items || []
                this.total = res.total || 0
            } finally { this.loading = false }
        },
        onPageChange(p) { this.query.page = p; this.refresh() },
        rebuildSearch() {
            this.rebuilding = true
            window.$api.post('/admin/search/rebuild').then(() => {
                ElMessage.success('索引已重建')
                this.refresh()
            }).catch(() => {}).finally(() => { this.rebuilding = false })
        },
        fmt(s) { return s ? s.replace('T', ' ').slice(0, 16) : '' },
        // 列表标题: 统一走 $api.refLabel（title 列 → slug → 类型字段序兜底 → expand 合成）
        titleOf(r) {
            return window.$api.refLabel(r, this.typeDefs[r.type] || null)
        },
    },
}</script>

<style>
.nodes-page { display: flex; flex: 1; min-height: 0; }
.nodes-tree {
    width: 250px; flex-shrink: 0;
    padding: 16px 16px 16px 0;
    overflow: auto;
    border-right: 1px solid #eaeaee; /* 中间竖线分隔 */
    font-family: 'Segoe UI', 'Segoe UI Web (West European)', -apple-system, 'system-ui', Roboto, 'Helvetica Neue', sans-serif;
}
.nodes-tree-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px; font-size: 13px; color: #616161; }
.nodes-list {
    flex: 1; min-width: 0;
    padding: 16px 0 16px 16px;
}
.type-list { display: flex; flex-direction: column; gap: 0; }
/* 分组标题：文字后面接一条贯穿线（legend 的感觉），左对齐、不画方框 */
.type-group { display: flex; align-items: center; gap: 8px; margin: 14px 0 4px; font-size: 11px; color: #a19f9d; }
.type-group::after { content: ''; flex: 1; height: 1px; background: #edebe9; }
.type-group:first-child { margin-top: 2px; }
.type-item {
    display: flex; align-items: center; gap: 8px;
    padding: 7px 16px; border-radius: 0; cursor: pointer;
    font-size: 13px; color: #242424; border-left: 2px solid transparent;
}
.type-item:hover { background: #f3f2f1; }
.type-item.active { background: #edebe9; border-left: 2px solid #0277d4; color: #242424; font-weight: 600; }
.type-item.active:hover { background: #e1dfdd; }
.type-key { font-size: 11px; font-weight: 400; color: #a19f9d; }

/* 树过滤按钮: link 下划线样式（非按钮框 — 看着轻） */
.filter-link {
    color: #000000a6 !important;
    text-decoration: underline;
    &:hover { color: #000 !important; }
    &.active { color: #000 !important; font-weight: 600; }
}

/* el-tree 节点: 平时透明, hover 浅灰, current #edebe9 + 左 border */
.el-tree--highlight-current .el-tree-node.is-current > .el-tree-node__content {
    background: #edebe9 !important;
    color: #242424;
    border-left: 2px solid #0277d4;
    padding-left: 14px;
}
.el-tree-node__content:hover{ background:#f3f2f1; }
.el-tree-node__content{ color:#242424; font-size:13px; }
</style>
