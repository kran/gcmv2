<!-- kind ref：单引用；编辑 = 远程搜索选择，列表 = 接口展开的显示名（引用值存 edges 表，不在 fields 里）。
     文件名就是 kind 名（web/admin/widgets/ref.vue），mode="edit" 编辑 / mode="cell" 只读。 -->
<template>
    <span v-if="mode === 'cell'" class="w-cell">
        <a v-if="target" class="w-ref-link" href="#" @click.prevent="open">{{ target.label }}</a>
        <span v-else class="w-empty">—</span>
    </span>
    <el-select v-else :model-value="modelValue" filterable remote clearable
        :remote-method="search" :loading="loading" placeholder="搜索并选择节点"
        style="width:100%" @visible-change="(open) => open && preload()"
        @update:model-value="emitValue($event)">
        <el-option v-for="o in options" :key="o.id" :label="o.label" :value="o.id" />
    </el-select>
</template>
<script>
export default {
    name: 'WRef',
    props: {
        modelValue: { default: undefined },
        mode: { type: String, default: 'edit' },      // edit | cell
        field: { type: Object, default: () => ({}) },
        defs: { type: Object, default: () => ({}) },  // 类型定义表（ref 显示名用）
        preset: { type: Array, default: () => [] },   // 引用已选值（编辑回显）
        expand: { default: null },                    // 列表接口批量展开的引用目标
    },
    emits: ['update:modelValue', 'open-node'],
    computed: {
        options() { return [...(this.preset || []), ...this.found] },
        // cell 用：{id, type, label} —— 有 id 就能点开那个节点的编辑表单
        target() {
            const one = Array.isArray(this.expand) ? this.expand[0] : this.expand
            if (one) return { id: one.id, type: one.type, label: one.display || '#' + one.id }
            return this.modelValue ? { id: this.modelValue, label: '#' + this.modelValue } : null
        },
    },
    data() { return { found: [], loading: false, loaded: false } },
    methods: {
        emitValue(v) { this.$emit('update:modelValue', v) },
        open() { if (this.target) this.$emit('open-node', this.target) },
        async load(q, sort) {
            this.loaded = true
            this.loading = true
            const params = { q: q || '', type: this.field.to, page: 1, size: 50 }
            if (sort) params.sort = sort
            try {
                const res = await window.$api.search(params)
                this.found = (res.items || []).map(n => ({
                    id: n.id,
                    label: window.$api.refLabel(n, this.defs[n.type] || null) + ' #' + n.id,
                }))
            } catch (_) { this.found = [] }
            this.loading = false
        },
        search(q) { return this.load(q) },
        // 打开下拉先给一批候选（空查询 = 取一批，新的在前）——只在没搜过时预载
        preload() { if (!this.loaded) this.load('', '-id') },
    },
}
</script>
