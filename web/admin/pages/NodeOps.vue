<template>
    <div style="display:inline-flex;gap:4px;">
        <el-button v-if="showCreate" size="small" link class="op-btn" @click="openCreate()">新建子级</el-button>
        <el-button size="small" link class="op-btn" @click="openEdit">编辑</el-button>
        <el-button size="small" link class="op-btn" @click="openExpand">引用</el-button>
        <el-button size="small" link class="op-btn" @click="doDelete">永久删除</el-button>
    </div>
    <node-edit-dialog v-model:visible="editVisible" :node="node" :type-name="typeName"
                      :preset-field="parentField" :preset-value="parentId"
                      :preset-label="parentLabel"
                      :is-edit="isEdit" :defs="defs" @changed="$emit('changed')" />

    <!-- 引用展开预览 -->
    <el-dialog append-to-body v-model="expandDialog.visible" title="引用展开" width="60vw">
        <div v-loading="expandDialog.loading" style="min-height:120px;">
            <template v-if="expandDialog.node">
                <div v-if="!expandDialog.fields.length" style="color:#9ca3af;padding:20px;text-align:center;">
                    该类型没有引用字段
                </div>
                <div v-for="field in expandDialog.fields" :key="field" style="margin-bottom:16px;">
                    <div style="font-weight:600;font-size:14px;color:#409eff;margin-bottom:6px;">{{ field }}</div>
                    <div v-for="n in expandItems(expandDialog.node.expand[field])" :key="n.id"
                         style="padding:8px 12px;background:#f9fafb;border-radius:6px;margin-bottom:4px;display:flex;align-items:center;gap:8px;">
                        <el-tag size="small">{{ n.type }}</el-tag>
                        <span style="font-weight:600;">{{ titleOf(n) }}</span>
                        <code style="color:#9ca3af;font-size:12px;">#{{ n.id }}</code>
                    </div>
                </div>
            </template>
            <div v-else-if="!expandDialog.loading" style="color:#9ca3af;padding:20px;text-align:center;">
                展开失败（字段可能不在该类型上）
            </div>
        </div>
    </el-dialog>
</template>
<script>
// NodeOps: 通用节点操作（编辑/永久删除/引用展开）— nodes.vue / tree.vue 共用。
export default {
    name: 'NodeOps',
    components: { NodeEditDialog: Vue.defineAsyncComponent(() => window.Panel.loadComponent('pages/NodeEditDialog.vue')) },
    computed: {
        parentLabel() {
            return window.$api.refLabel(this.node, this.defs[this.node.type] || null) + ' #' + this.node.id
        },
        parentField() {
            const def = this.defs[this.typeName || this.node.type] || {}
            const tree = def.capabilities && def.capabilities.tree
            return tree ? tree.parent : ''
        },
    },
    props: {
        node: { type: Object, required: true },      // 行数据（含 id/type）
        defs: { type: Object, default: () => ({}) },
        typeName: { type: String, default: '' },     // 新建时的类型（编辑用 node.type）
        showCreate: { type: Boolean, default: false }, // 树场景: 新建子
        parentId: { type: Number, default: 0 },        // 新建子的父 id（= node.id）
    },
    emits: ['changed'],
    data() {
        return {
            editVisible: false,
            isEdit: false,
            expandDialog: { visible: false, loading: false, node: null, fields: [] },
        }
    },
    methods: {
        titleOf(n) {
            return window.$api.refLabel(n, this.defs[n.type] || null)
        },
        openCreate() {
            this.isEdit = false
            this.editVisible = true
        },
        openEdit() {
            this.isEdit = true
            this.editVisible = true
        },
        doDelete() {
            var r = this.node
            // 这里是永久删除（engine.DeleteNode；被任何引用指向都会拒绝）——
            // 和"归档"不是一回事：归档节点连后台列表都查不到，所以措辞必须写明永久，别让人当软删点。
            ElMessageBox.confirm('永久删除 #' + r.id + ' ？不可撤销，且被引用时可能被拒绝。', '确认',
                { type: 'warning', confirmButtonText: '永久删除' })
                .then(() => {
                    window.$api.deleteNode(r.id).then(() => {
                        ElMessage.success('已永久删除')
                        this.$emit('changed')
                    }).catch(() => {})
                }).catch(() => {})
        },
        // 引用展开预览: ExpandPath 全 ref 字段一层全景
        openExpand() {
            var r = this.node
            this.expandDialog.visible = true
            this.expandDialog.loading = true
            this.expandDialog.node = null
            // 详情接口自带 auto expand（与列表/编辑器同形）
            window.$api.get('/admin/nodes/' + r.id).then((res) => {
                this.expandDialog.node = res || null
                this.expandDialog.fields = this.expandDialog.node ? Object.keys(this.expandDialog.node.expand || {}) : []
            }).catch(() => { this.expandDialog.node = null })
                .finally(() => { this.expandDialog.loading = false })
        },
        expandItems(v) { return Array.isArray(v) ? v : [v] },
    },
}
</script>
