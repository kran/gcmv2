<template>
  <div class="nodes-page">
    <!-- 左侧 type 列表（类型定义驱动, 动态） -->
    <div class="nodes-tree">
      <div class="nodes-tree-header">
        <span style="font-size:14px;">类型</span>
        <el-button link type="primary" size="small" @click="loadTypes">刷新</el-button>
      </div>
      <div class="type-list">
        <template v-for="g in typeGroups" :key="g.name || '__ungrouped'">
          <div v-if="g.name" class="type-group">{{ g.name }}</div>
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
        <!-- 树过滤（多个: 每个树引用字段一个 popover 下拉, 按目标类型名区分） -->
        <el-popover v-for="ft in filterTrees" :key="ft.field" trigger="click" placement="bottom-start"
                    :show-timeout="0" :hide-timeout="0"
                    :width="200" style="margin-left:8px;" :ref="'tp-' + ft.field">
          <template #reference>
            <el-button link size="small" class="filter-link" :class="{ active: ft.active }">
              {{ ft.active ? (ft.activeLabel + ' ✕') : ('按' + ft.label + '过滤') }}
            </el-button>
          </template>
          <div style="max-height:300px;overflow:auto;">
            <div class="type-item" :class="{ active: ft.active === 0 }" @click="clearTreeFilter(ft)">
              <span>全部</span>
            </div>
            <el-tree :ref="'tree-' + ft.field" :data="ft.nodes" node-key="id" default-expand-all
                     :expand-on-click-node="false" highlight-current
                     :current-node-key="ft.active" @node-click="(n) => selectTreeNode(ft, n)">
              <template #default="{ data }">
                <span class="tree-node-label" style="font-size:13px;">{{ titleOf(data) }}</span>
              </template>
            </el-tree>
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
            filterTrees: [],   // 多个树过滤: [{def, field, label, nodes, active, activeLabel}]
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
        // 分组只作用于左侧类型列表；没填 group 的排在最前
        buildTypeGroups() {
            const ungrouped = []
            const groups = []
            const byName = {}
            this.typeNames.forEach(name => {
                const admin = (this.typeDefs[name] || {}).admin || {}
                const item = { name, label: admin.label || name }
                if (!admin.group) {
                    ungrouped.push(item)
                    return
                }
                if (!byName[admin.group]) {
                    byName[admin.group] = { name: admin.group, items: [] }
                    groups.push(byName[admin.group])
                }
                byName[admin.group].items.push(item)
            })
            const byLabel = (a, b) => a.label.localeCompare(b.label, 'zh')
            ungrouped.sort(byLabel)
            groups.forEach(g => g.items.sort(byLabel))
            return ungrouped.length ? [{ name: '', items: ungrouped }].concat(groups) : groups
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
        fieldValue(node, name) {
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
            this.setupFilterTree(def)
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
        // 树过滤: 当前类型的全部"指向树结构"的 ref 字段, 每个一个过滤下拉。
        // 多树 AND 组合: (and (in ->f1 [ids]) (in ->f2 [ids]))
        setupFilterTree(def) {
            this.filterTrees = []
            if (!def) return
            const name = def.name
            for (const f of def.fields || []) {
                if (!(f.kind === 'ref' || f.kind === 'ref[]')) continue
                if (f.to === name) continue // 自身自引用: 列表即树（treeMode）, 过滤栏多余
                const tdef = this.typeDefs[f.to]
                if (tdef && this.selfRefField(tdef)) {
                    const ft = { def: tdef, field: f.name, label: f.to, nodes: [], active: 0, activeLabel: '' }
                    this.filterTrees.push(ft)
                    this.loadFilterTree(ft, f.to)
                }
            }
        },
        async loadFilterTree(ft, typeName) {
            try {
                const res = await window.$api.get('/admin/tree', { type: typeName })
                const pf = this.selfRefField(ft.def) || 'parent'
                ft.nodes = this.buildTree(res.items || [], pf)
            } catch (_) {}
        },
        // 点击树节点: 子树集合 → 该字段过滤; 多字段 AND 组合
        onTreeClick(ft, n) {
            ft.active = n.id
            ft.activeLabel = this.titleOf(n)
            ft._ids = this.collectSubtree(n)
            this.setTreeCurrent(ft, n.id)
            this.query.filter = this.combineTreeFilters()
            this.query.page = 1
            this.refresh()
        },
        clearTreeFilter(ft) {
            ft.active = 0
            ft.activeLabel = ''
            ft._ids = null
            this.setTreeCurrent(ft, null)
            this.query.filter = this.combineTreeFilters()
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
        selectTreeNode(ft, n) {
            this.onTreeClick(ft, n)
            this.closeTreePopover(ft)
        },
        closeTreePopover(ft) {
            const ref = this.$refs['tp-' + ft.field]
            if (ref && ref[0]) ref[0].hide()
        },
        combineTreeFilters() {
            const parts = []
            for (const ft of this.filterTrees) {
                if (ft._ids && ft._ids.length) {
                    parts.push('(in ->' + ft.field + ' [' + ft._ids.join(' ') + '])')
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
.type-group { margin-top: 10px; padding: 9px 16px 4px; font-size: 11px; color: #a19f9d; border-top: 1px solid #f3f2f1; }
.type-group:first-child { margin-top: 0; padding-top: 0; border-top: 0; }
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
