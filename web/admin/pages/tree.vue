<template>
    <div>
        <div style="display:flex;gap:16px;align-items:flex-start;">
            <!-- 树: 该类型全量 -->
            <div class="nodes-tree" style="width:220px;">
                <div class="nodes-tree-header">
                    <span style="font-weight:600;font-size:14px;">{{ typeName }} tree</span>
                    <el-button link type="primary" size="small" @click="loadTree">刷新</el-button>
                </div>
                <div style="margin-top:8px;">
                    <cat-tree :nodes="treeNodes" :active-id="activeId" @select="onNodeClick" />
                    <div v-if="!treeNodes.length" style="color:#9ca3af;padding:20px;text-align:center;">空</div>
                </div>
            </div>

            <!-- in 方向列表: 选中分支的子树被哪些节点引用（无论类型 + 溯源） -->
            <div style="flex:1;min-width:0;">
                <div style="display:flex;align-items:center;gap:8px;margin-bottom:8px;">
                    <span style="font-weight:600;">引用列表</span>
                    <el-tag v-if="activeNode" size="small">{{ activeNode.title || '#' + activeNode.id }}</el-tag>
                    <el-checkbox v-model="subtree" :disabled="!activeId" @change="loadInbound">
                        含子树
                    </el-checkbox>
                    <span v-if="total" style="color:#9ca3af;font-size:12px;">共 {{ total }} 条</span>
                    <span v-if="refTypes.length" style="margin-left:auto;" class="split-btn">
                        <!-- 主按钮: 直接建默认类型（连体拆分按钮样式） -->
                        <el-button type="primary" size="small" class="split-main"
                                   @click="onCreateType(refTypes[0])">新建 {{ refTypes[0] }}</el-button><el-popover ref="typePopover" placement="bottom-end" trigger="click" width="120">
                            <template #reference>
                                <el-button type="primary" size="small" class="split-arrow">
                                    <el-icon><ArrowDown /></el-icon>
                                </el-button>
                            </template>
                            <div class="type-menu">
                                <div v-for="t in refTypes" :key="t" class="type-menu-item"
                                     @click="onCreateType(t); hidePopover()">{{ t }}</div>
                            </div>
                        </el-popover>
                    </span>
                </div>
                <el-table :data="rows" v-loading="loading" >
                    <el-table-column prop="id" label="ID" width="70" />
                    <el-table-column label="类型" width="110">
                        <template #default="{ row }">
                            <el-tag size="small">{{ row.type }}</el-tag>
                        </template>
                    </el-table-column>
                    <el-table-column label="标题" min-width="360" show-overflow-tooltip>
                        <template #default="{ row }">{{ row.title || row.slug || '#' + row.id }}</template>
                    </el-table-column>
                    <el-table-column label="溯源" min-width="160" show-overflow-tooltip>
                        <template #default="{ row }">
                            <code style="font-size:12px;">{{ row.via_field }}</code>
                        </template>
                    </el-table-column>
                    <el-table-column prop="slug" label="slug" min-width="160" show-overflow-tooltip />
                    <el-table-column label="操作" width="170" fixed="right">
                        <template #default="{ row }">
                            <node-ops :node="row" :defs="defsByType" @changed="loadInbound" />
                        </template>
                    </el-table-column>
                </el-table>
                <div style="display:flex;justify-content:flex-end;margin-top:12px;">
                    <el-pagination background layout="prev, pager, next, total" :total="total"
                        :page-size="size" :current-page="page" @current-change="onPage" />
                </div>
            </div>
        </div>
    </div>

    <!-- 新建（类型 = 引用本树的类型） -->
    <node-edit-dialog v-model:visible="createVisible" :type-name="createType" :defs="defsByType"
                      :preset-field="createField" :preset-value="activeId"
                      @changed="loadInbound" />
</template>
<script>
import { useRoute } from 'vue-router'
export default {
    components: {
        NodeOps: Vue.defineAsyncComponent(() => window.Panel.loadComponent('pages/NodeOps.vue')),
        NodeEditDialog: Vue.defineAsyncComponent(() => window.Panel.loadComponent('pages/NodeEditDialog.vue')),
        CatTree: Vue.defineAsyncComponent(() => window.Panel.loadComponent('pages/CatTree.vue')),
    },
    setup() {
        var route = useRoute()
        return { typeName: route.params.type }
    },
    data() {
        return {
            def: null,
            defsByType: {},
            treeNodes: [],
            activeId: 0,
            activeNode: null,
            subtree: true,
            rows: [],
            total: 0,
            page: 1,
            size: 25,
            loading: false,
            refTypes: [],          // 引用该 tree 类型的类型（新建选项）
            createType: '',        // 当前新建的类型
            createField: '',       // 新建预置字段（指向本树的 ref 字段）
            createVisible: false,
        }
    },
    mounted() { this.loadTypes() },
    methods: {
        titleOf(n) {
            return n.title || n.slug || '#' + n.id
        },
        async loadTypes() {
            var res = await $api.types()
            this.defsByType = res.types || {}
            this.def = this.defsByType[this.typeName] || null
            // 引用该 tree 类型的类型（含自己 — 新建子分类）: ref/ref[] 字段 to === typeName
            var me = this
            this.refTypes = []
            Object.keys(this.defsByType).forEach(function (t) {
                var def = me.defsByType[t]
                var hits = (def.fields || []).filter(function (f) {
                    return (f.kind === 'ref' || f.kind === 'ref[]') && f.to === me.typeName
                })
                if (hits.length) me.refTypes.push(t)
            })
            this.loadTree()
        },
        async loadTree() {
            try {
                var res = await $api.get('/admin/tree', { type: this.typeName })
                this.treeNodes = this.buildTree(res.items || [], 'parent')
                console.log('[tree] nodes:', this.treeNodes.length, JSON.parse(JSON.stringify(this.treeNodes.slice(0, 3))))
            } catch (e) { console.error('[tree] loadTree failed:', e) }
        },
        buildTree(items, parentField) {
            var map = {}
            var me = this
            items.forEach(function (n) { map[n.id] = { ...n, label: me.titleOf(n), children: [] } })
            var roots = []
            items.forEach(function (n) {
                var node = map[n.id]
                var pid = n.fields && n.fields[parentField]
                var parent = pid && map[pid]
                if (parent) parent.children.push(node)
                else roots.push(node)
            })
            return roots
        },
        collectSubtree(n) {
            var ids = [n.id]
            var walk = (node) => { (node.children || []).forEach(function (c) { ids.push(c.id); walk(c) }) }
            walk(n)
            return ids
        },
        hidePopover() { this.$refs.typePopover && this.$refs.typePopover.hide() },
        onCreateType(t) {
            this.createType = t
            // 预置字段: 该类型指向本 tree 类型的第一个 ref 字段
            var def = this.defsByType[t] || {}
            var me = this
            var f = (def.fields || []).find(function (x) {
                return (x.kind === 'ref' || x.kind === 'ref[]') && x.to === me.typeName
            })
            this.createField = f ? f.name : ''
            this.createVisible = true
        },
        onNodeClick(n) {
            this.activeId = n.id
            this.activeNode = n
            this.page = 1
            this.loadInbound()
        },
        async loadInbound() {
            if (!this.activeId) return
            this.loading = true
            try {
                var params = { node: this.activeId, page: this.page, size: this.size }
                if (this.subtree) params.subtree = 1
                var res = await $api.get('/admin/inbound', params)
                this.rows = res.items || []
                this.total = res.total || 0
            } catch (_) { this.rows = []; this.total = 0 }
            this.loading = false
        },
        onPage(p) { this.page = p; this.loadInbound() },
    },
}
</script>

<style>
/* tree 页: 连体拆分按钮（全局 — popover teleport 到 body） */
.split-btn .el-button.split-main { border-top-right-radius: 0; border-bottom-right-radius: 0; }
.split-btn .el-button.split-arrow {
    border-top-left-radius: 0; border-bottom-left-radius: 0;
    margin-left: 0 !important;                     /* el-button 相邻默认 margin 清除 */
    border-left: 1px solid rgba(255,255,255,.35);
    padding: 8px 10px;
}
.split-btn .el-button + .el-button { margin-left: 0 !important; }
/* 类型下拉菜单 */
.type-menu-item {
    padding: 5px 10px; font-size: 13px; color: #606266; cursor: pointer;
    border-radius: 4px; white-space: nowrap; line-height: 1.4;
}
.type-menu-item:hover { background: #ecf5ff; color: #409eff; }
</style>
