<template>
    <div class="fr">
        <template v-for="f in fields" :key="f.name">
            <div class="fr-item">
                <div class="fr-label">
                    <span>{{ f.label || f.name }}</span>
                    <span class="fr-kind">{{ f.kind }}</span>
                    <span v-if="f.required" class="fr-req">*</span>
                </div>

                <!-- 标量 -->
                <el-input v-if="f.kind === 'text'" :model-value="get(f.name)"
                    @update:model-value="set(f.name, $event)" />
                <div v-else-if="f.kind === 'upload-image'" class="fr-image">
                    <el-input :model-value="get(f.name)" @update:model-value="set(f.name, $event)"
                        placeholder="/uploads/xxx.png" />
                    <input type="file" :ref="'file-' + f.name" style="display:none;" accept="image/*"
                        @change="uploadImage(f.name, $event)" />
                    <el-button size="small" @click="pickFile(f.name)">上传</el-button>
                    <img v-if="get(f.name)" :src="get(f.name)" class="fr-image-preview" />
                </div>
                <div v-else-if="f.kind === 'upload-file'" class="fr-image">
                    <el-input :model-value="get(f.name)" @update:model-value="set(f.name, $event)"
                        placeholder="/uploads/xxx.mp4" />
                    <input type="file" :ref="'file-' + f.name" style="display:none;"
                        @change="uploadImage(f.name, $event)" />
                    <el-button size="small" @click="pickFile(f.name)">上传</el-button>
                </div>
                <div v-else-if="f.kind === 'ref'" class="fr-ref">
                    <el-select :model-value="get(f.name)" filterable remote clearable
                               :remote-method="(q) => searchRef(f, q)"
                               :loading="refLoading[f.name]" placeholder="搜索并选择节点"
                               style="width:100%" @update:model-value="set(f.name, $event)">
                        <el-option v-for="o in allRefOptions[f.name] || []" :key="o.id"
                                   :label="o.label" :value="o.id" />
                    </el-select>
                </div>
                <div v-else-if="f.kind === 'ref[]'" class="fr-ref">
                    <el-select :model-value="get(f.name) || []" multiple filterable remote
                               :remote-method="(q) => searchRef(f, q)"
                               :loading="refLoading[f.name]" placeholder="搜索并选择多个节点"
                               style="width:100%" @update:model-value="set(f.name, $event)">
                        <el-option v-for="o in allRefOptions[f.name] || []" :key="o.id"
                                   :label="o.label" :value="o.id" />
                    </el-select>
                </div>
                <el-input v-else-if="f.kind === 'textarea'" type="textarea" :rows="4"
                    :model-value="get(f.name)" @update:model-value="set(f.name, $event)" />
                <rich-editor v-else-if="f.kind === 'richtext'" :model-value="get(f.name) || ''"
                    @update:model-value="set(f.name, $event)" />
                <el-select v-else-if="f.kind === 'select'" :model-value="get(f.name)"
                           placeholder="请选择" style="width:100%;"
                           @update:model-value="set(f.name, $event)">
                    <el-option v-for="o in f.options || []" :key="o" :label="o" :value="o" />
                </el-select>
                <el-input-number v-else-if="f.kind === 'number'" :model-value="get(f.name)"
                    @update:model-value="set(f.name, $event)" style="width:200px;" />
                <el-switch v-else-if="f.kind === 'bool'" :model-value="!!get(f.name)"
                    @update:model-value="set(f.name, $event)" />

                <!-- 数组 -->
                <div v-else-if="f.kind === 'array'" class="fr-array">
                    <!-- 字符串数组 → 标签输入 -->
                    <el-select v-if="f.item && f.item.kind === 'text'" multiple allow-create filterable
                        default-first-option :model-value="get(f.name) || []"
                        @update:model-value="set(f.name, $event)" placeholder="回车添加标签" style="width:100%;" />
                    <!-- 复合数组 → 卡片列表 (递归) -->
                    <template v-else>
                        <div v-for="(item, i) in (get(f.name) || [])" :key="i" class="fr-card">
                            <div class="fr-card-bar">
                                <span class="fr-card-idx">#{{ i + 1 }}</span>
                                <span>
                                    <el-button link size="small" @click="moveItem(f.name, i, -1)"
                                        :disabled="i === 0">上移</el-button>
                                    <el-button link size="small" @click="moveItem(f.name, i, 1)"
                                        :disabled="i === (get(f.name) || []).length - 1">下移</el-button>
                                    <el-button link type="danger" size="small"
                                        @click="removeItem(f.name, i)">删除</el-button>
                                </span>
                            </div>
                            <!-- object 元素: 子字段递归 -->
                            <field-renderer v-if="f.item && f.item.kind === 'object'" :fields="f.item.fields"
                                :model-value="item" @update:model-value="setItem(f.name, i, $event)" />
                            <!-- 非标量非标量元素 (array<array>/array<number>…): 包一层 {v: item} 复用渲染 -->
                            <field-renderer v-else :fields="[elemAsField(f)]" :model-value="{ v: item }"
                                @update:model-value="setItem(f.name, i, $event.v)" />
                        </div>
                        <el-button size="small" @click="addItem(f)">+ 添加一项</el-button>
                    </template>
                </div>

                <!-- 对象 (递归) -->
                <div v-else-if="f.kind === 'object'" class="fr-object">
                    <field-renderer :fields="f.fields || []" :model-value="get(f.name) || {}"
                        @update:model-value="set(f.name, $event)" />
                </div>

                <!-- 站点扩展控件: 未知 kind → /admin/ui-extras/{kind}.vue 动态加载 -->
                <div v-else-if="extraWidgets[f.kind] === false" class="fr-unknown">
                    ⚠ 未知控件类型 &quot;{{ f.kind }}&quot;（站点未挂载对应组件）
                </div>
                <component v-else :is="extraWidgets[f.kind]" :model-value="get(f.name)"
                    @update:model-value="set(f.name, $event)" />

            </div>
        </template>
    </div>
</template>
<script>


// FieldRenderer 按 types 定义递归渲染字段表单 (design §9 复合字段)。
// 自引用经 name: 'FieldRenderer' 实现 (SFC 运行时编译无法自 import)。
export default {
    name: 'FieldRenderer',
    components: { RichEditor: Vue.defineAsyncComponent(() => window.Panel.loadComponent('pages/RichEditor.vue')) },
    props: {
        fields: { type: Array, default: () => [] },
        modelValue: { type: Object, default: () => ({}) },
        // 引用预置: {fieldName: [{id, label}]} — 编辑回显已选值（expand 结果）
        refPreset: { type: Object, default: () => ({}) },
        // 类型定义表（refLabel 显示兜底用）: {typeName: TypeDef}
        defs: { type: Object, default: () => ({}) },
    },
    emits: ['update:modelValue'],
    data() {
        return { refOptions: {}, refLoading: {}, extraWidgets: {} }
    },
    mounted() { this.resolveExtraWidgets() },
    watch: {
        fields: { deep: true, handler() { this.resolveExtraWidgets() } },
    },
    computed: {
        // 合并: preset（编辑回显已选值）+ options（搜索新值, 覆盖 preset 同 id）
        allRefOptions() {
            return { ...(this.refPreset || {}), ...(this.refOptions || {}) }
        },
    },
    methods: {
        // 内置控件分支集合（其余 kind → 站点扩展组件动态加载）
        builtinWidgets() {
            return ['text', 'textarea', 'richtext', 'number', 'bool',
                'upload-image', 'upload-file', 'ref', 'ref[]', 'array', 'object']
        },
        resolveExtraWidgets() {
            const builtin = this.builtinWidgets()
            ;(this.fields || []).forEach(f => {
                if (f.kind && builtin.indexOf(f.kind) < 0 && this.extraWidgets[f.kind] === undefined) {
                    this.resolveWidget(f.kind)
                }
            })
        },
        async resolveWidget(name) {
            this.extraWidgets[name] = null
            try {
                const comp = await Panel.loadComponent('/admin/ui-extras/' + name + '.vue')
                this.extraWidgets[name] = comp || false
            } catch (_) {
                this.extraWidgets[name] = false // 加载失败 → 模板显示警告块（fail-loud）
            }
        },
        get(name) { return this.modelValue ? this.modelValue[name] : undefined },
        set(name, v) {
            this.$emit('update:modelValue', { ...(this.modelValue || {}), [name]: v })
        },
        setItem(name, i, v) {
            const arr = [...(this.get(name) || [])]
            arr[i] = v
            this.set(name, arr)
        },
        addItem(f) {
            const arr = [...(this.get(f.name) || []), defaultItem(f.item)]
            this.set(f.name, arr)
        },
        removeItem(name, i) {
            const arr = (this.get(name) || []).filter((_, idx) => idx !== i)
            this.set(name, arr)
        },
        moveItem(name, i, delta) {
            const arr = [...(this.get(name) || [])]
            const j = i + delta
            if (j < 0 || j >= arr.length) return
            const t = arr[i]; arr[i] = arr[j]; arr[j] = t
            this.set(name, arr)
        },
        // 非 object 数组元素包成单字段表单复用渲染
        elemAsField(f) {
            const item = f.item || { kind: 'text' }
            return { name: 'v', kind: item.kind, item: item.item, fields: item.fields }
        },
        // v-for 里的 ref 是数组 (Vue 3): $refs['file-x'] 是 [el]
        pickFile(name) {
            const els = this.$refs['file-' + name]
            if (els && els[0]) els[0].click()
        },
        // 引用编辑: 实体搜索（$api.search, 按 f.to 类型过滤）
        async searchRef(f, q) {
            if (!q) return
            this.refLoading = { ...(this.refLoading || {}), [f.name]: true }
            try {
                const res = await window.$api.search({ q: q, type: f.to, page: 1, size: 50 })
                this.refOptions = { ...(this.refOptions || {}), [f.name]:
                    (res.items || []).map(n => ({ id: n.id, label: window.$api.refLabel(n, this.defs[n.type] || null) + ' #' + n.id })) }
            } catch (_) {
                this.refOptions = { ...(this.refOptions || {}), [f.name]: [] }
            }
            this.refLoading = { ...(this.refLoading || {}), [f.name]: false }
        },
        async uploadImage(name, ev) {
            const file = ev.target.files && ev.target.files[0]
            if (!file) return
            try {
                const res = await window.$api.upload(file)
                this.set(name, res.path)
                ElMessage.success('已上传')
            } catch (_) {}
            ev.target.value = ''
        },
    },
}

function defaultItem(item) {
    const kind = item && item.kind
    switch (kind) {
        case 'object': return {}
        case 'array': return []
        case 'number': return 0
        case 'bool': return false
        default: return ''
    }
}
</script>
<style>
.fr-item { margin-bottom: 12px; width: 100%; min-width: 0; }
.fr-item .el-input, .fr-item .el-textarea, .fr-item .el-select,
.fr-item .el-input-number, .fr-item .el-color-picker { width: 100%; }
.fr-label { font-size: 13px; font-weight: 600; color: #444; margin-bottom: 4px; }
.fr-kind { font-weight: 400; color: #aaa; font-size: 11px; margin-left: 6px; }
.fr-req { color: #e60012; margin-left: 2px; }
.fr-object, .fr-array { border-left: 2px solid #eee; padding-left: 12px; }
.fr-card { border: 1px solid #eee; border-radius: 6px; padding: 10px; margin-bottom: 8px; }
.fr-card-bar { display: flex; justify-content: space-between; align-items: center; margin-bottom: 6px; }
.fr-card-idx { color: #aaa; font-size: 12px; }
.fr-image { display: flex; gap: 8px; align-items: center; width: 100%; }
.fr-image-preview { max-height: 56px; border-radius: 3px; border: 1px solid #eee; }
</style>
