<template>
    <el-drawer append-to-body v-model="visibleModel" :title="isEdit ? '编辑 #' + node.id : '新建 ' + (typeName || '')"
               size="60%" :close-on-click-modal="false" :close-on-press-escape="false">
        <el-form>
            <!-- display 也走构建器: 字段行结构（label/kind/必填）与下面字段完全一致 -->
            <field-renderer v-if="def" :fields="def.fields" v-model="form.fields"
                            :display="form.display" show-display
                            @update:display="form.display = $event"
                            :ref-preset="form.refPreset || {}" :defs="defs" :editing="isEdit" />
        </el-form>
        <template #footer>
            <div style="display:flex;justify-content:flex-end;gap:8px;">
                <el-button @click="visibleModel = false">取消</el-button>
                <el-button type="primary" :loading="saving" @click="save"><el-icon><Check /></el-icon>保存</el-button>
            </div>
        </template>
    </el-drawer>
</template>
<script>
// NodeEditDialog: 节点新建/编辑共用表单。
// v-model:visible 控制显隐; @changed 保存成功后通知。
export default {
    name: 'NodeEditDialog',
    components: { FieldRenderer: Vue.defineAsyncComponent(() => window.Panel.loadComponent('pages/FieldRenderer.vue')) },
    props: {
        visible: { type: Boolean, required: true },
        node: { type: Object, default: () => ({}) },   // 编辑: 行数据（含 id/type）
        defs: { type: Object, default: () => ({}) },
        typeName: { type: String, default: '' },       // 新建类型（编辑用 node.type）
        presetField: { type: String, default: '' },    // 新建预置字段（如 parent/categories）
        presetValue: { type: Number, default: 0 },     // 预置字段值（如选中的树节点 id）
        presetLabel: { type: String, default: '' },    // 预置字段的显示名（ref 回显 label）
        isEdit: { type: Boolean, default: false },
    },
    emits: ['update:visible', 'changed'],
    data() {
        return { form: { display: '', revision: 0, fields: {}, refPreset: {} }, saving: false, def: null }
    },
    computed: {
        // v-model:visible 代理 — prop 只读, 内部写走 emit
        visibleModel: {
            get() { return this.visible },
            set(v) { this.$emit('update:visible', v) },
        },
    },
    watch: {
        visible(v) {
            if (!v) return
            this.saving = false
            if (this.isEdit) this.loadEdit()
            else this.loadCreate()
        },
    },
    methods: {
        loadCreate() {
            this.def = this.defs[this.typeName] || null
            var defaults = {}
            ;((this.def && this.def.fields) || []).forEach(function (f) {
                if (f.default !== undefined && f.default !== null) defaults[f.name] = structuredClone(f.default)
            })
            this.form = { display: '', revision: 0, fields: defaults, refPreset: {} }
            if (this.presetField && this.presetValue) {
                this.form.fields[this.presetField] = this.presetValue
                // ref 字段显示名（否则只显示裸 id）
                if (this.presetLabel) {
                    this.form.refPreset[this.presetField] = [{ id: this.presetValue, label: this.presetLabel }]
                }
            }
        },
        loadEdit() {
            var r = this.node
            var type = r.type || this.typeName
            this.def = this.defs[type] || null
            window.$api.node(r.id).then((full) => {
                this.form = {
                    display: full.display || '',
                    revision: full.revision,
                    fields: full.fields || {},
                    refPreset: {},
                }
                // 引用回显: expand * → refPreset（已选值显示标题, 非裸 id）
                window.$api.get('/admin/expand', { node: r.id, expr: '*' }).then((ex) => {
                    var expand = (ex.node && ex.node.expand) || {}
                    var preset = {}
                    Object.keys(expand).forEach((f) => {
                        var v = expand[f]
                        var items = Array.isArray(v) ? v : (v ? [v] : [])
                        preset[f] = items.map((n) => ({ id: n.id,
                            label: window.$api.refLabel(n, this.defs[n.type] || null) + ' #' + n.id }))
                    })
                    this.form.refPreset = preset
                }).catch(() => {})
            }).catch(() => {})
        },
        save() {
            this.saving = true
            // 只提交类型声明的字段: 存量数据可能带未声明的历史键（v0.8 允许任意字段），
            // 回传它们会被 v0.9 的 Schema 校验拒绝（422 unknown field）；不回传 = 不改动它们。
            var declared = {}
            var self = this
            ;((this.def && this.def.fields) || []).forEach(function (f) {
                if (self.isEdit && f.immutable) return
                declared[f.name] = (self.form.fields || {})[f.name]
            })
            var body = { display: this.form.display, fields: declared }
            if (this.isEdit) body.revision = this.form.revision
            var p = this.isEdit
                ? window.$api.updateNode(this.node.id, body)
                : window.$api.createNode(this.typeName, body)
            p.then(() => {
                ElMessage.success('已保存')
                this.$emit('changed')
                this.visibleModel = false
            }).catch(() => {}).finally(() => { this.saving = false })
        },
    },
}
</script>
