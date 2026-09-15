<!-- FieldRenderer：字段表单调度器。
     叶值交给 Widgets.resolve(field.kind) —— 字段的 kind 名就是组件文件名
     （web/admin/widgets/<kind>.vue），前端不认识任何具体 kind，只按名字取。
     array / object 是**结构**（不是 kind，没有组件），由这里递归渲染。 -->
<template>
    <div class="fr">
        <template v-for="f in fields" :key="f.name">
            <div class="fr-item" :class="{ 'fr-readonly': editing && f.immutable }">
                <div class="fr-label">
                    <span>{{ f.label || f.name }}</span>
                    <span class="fr-kind">{{ f.kind }}</span>
                    <span v-if="f.required" class="fr-req">*</span>
                    <span v-if="editing && f.immutable" class="fr-kind">只读</span>
                </div>

                <!-- 结构：数组（元素为 object 时子字段递归，其它包一层复用） -->
                <div v-if="f.kind === 'array'" class="fr-array">
                    <template>
                        <div v-for="(item, i) in (get(f.name) || [])" :key="i" class="fr-card">
                            <div class="fr-card-bar">
                                <span class="fr-card-idx">#{{ i + 1 }}</span>
                                <span>
                                    <el-button link size="small" @click="moveItem(f.name, i, -1)"
                                        :disabled="i === 0">上移</el-button>
                                    <el-button link size="small" @click="moveItem(f.name, i, 1)"
                                        :disabled="i === (get(f.name) || []).length - 1">下移</el-button>
                                    <el-button link size="small"
                                        @click="removeItem(f.name, i)">删除</el-button>
                                </span>
                            </div>
                            <!-- object 元素: 子字段递归 -->
                            <field-renderer v-if="f.item && f.item.kind === 'object'" :fields="f.item.fields"
                                :model-value="item" :defs="defs" :editing="editing"
                                @update:model-value="setItem(f.name, i, $event)" />
                            <!-- 其它元素: 包一层 {v: item} 复用渲染 -->
                            <field-renderer v-else :fields="[elemAsField(f)]" :model-value="{ v: item }"
                                :defs="defs" :editing="editing"
                                @update:model-value="setItem(f.name, i, $event.v)" />
                        </div>
                        <el-button size="small" @click="addItem(f)">+ 添加一项</el-button>
                    </template>
                </div>

                <!-- 结构：对象（递归） -->
                <div v-else-if="f.kind === 'object'" class="fr-object">
                    <field-renderer :fields="f.fields || []" :model-value="get(f.name) || {}"
                        :defs="defs" :editing="editing" :node="node"
                        @update:model-value="set(f.name, $event)" />
                </div>

                <!-- 叶值：kind 名对应的组件（编辑模式） -->
                <component v-else-if="widget(f)" :is="widget(f)" mode="edit" :model-value="get(f.name)"
                    :field="f" :defs="defs" :node="node"
                    @update:model-value="set(f.name, $event)" />
                <!-- 字段没有 kind（类型定义坏了）：组件缺失由 Widgets 那边的错误组件显示 -->
                <div v-else class="fr-error">
                    ⚠ 字段 &quot;{{ f.name }}&quot; 没有 kind，画不出界面 —— 检查类型定义
                </div>
            </div>
        </template>
    </div>
</template>
<script>

// FieldRenderer 按类型定义递归渲染字段表单（design §9 复合字段）。
// 自引用经 name: 'FieldRenderer' 实现（SFC 运行时编译无法自 import）。
export default {
    name: 'FieldRenderer',
    props: {
        fields: { type: Array, default: () => [] },
        modelValue: { type: Object, default: () => ({}) },
        // 当前节点上下文：组件从它取引用目标（node.expand）
        node: { type: Object, default: () => ({}) },
        // 类型定义表（refLabel 显示兜底用）: {typeName: TypeDef}
        defs: { type: Object, default: () => ({}) },
        editing: { type: Boolean, default: false },
    },
    emits: ['update:modelValue'],
    methods: {
        // kind 名 → 组件（取不到文件时 Widgets 渲染"缺哪个文件"的错误块）
        widget(f) { return Widgets.resolve(f && f.kind) },
        get(name) { return this.modelValue ? this.modelValue[name] : undefined },
        set(name, v) {
            const field = (this.fields || []).find(f => f.name === name)
            if (this.editing && field && field.immutable) return
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
.fr-readonly { opacity: .7; pointer-events: none; }
.fr-item .el-input, .fr-item .el-textarea, .fr-item .el-select,
.fr-item .el-input-number, .fr-item .el-color-picker { width: 100%; }
.fr-label { font-size: 13px; font-weight: 600; color: #444; margin-bottom: 4px; }
.fr-kind { font-weight: 400; color: #aaa; font-size: 11px; margin-left: 6px; }
.fr-req { color: #e60012; margin-left: 2px; }
.fr-object, .fr-array { border-left: 2px solid #eee; padding-left: 12px; }
.fr-card { border: 1px solid #eee; border-radius: 6px; padding: 10px; margin-bottom: 8px; }
.fr-card-bar { display: flex; justify-content: space-between; align-items: center; margin-bottom: 6px; }
.fr-card-idx { color: #aaa; font-size: 12px; }
.fr-error { color: #c45656; font-size: 12px; background: #fef0f0; padding: 6px 8px; border-radius: 4px; }
</style>
