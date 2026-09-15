<!-- kind refs：多引用；编辑 = 远程搜索多选，列表 = 展开的显示名列表。
     文件名就是 kind 名（web/admin/widgets/refs.vue），mode="edit" 编辑 / mode="cell" 只读。 -->
<template>
    <span v-if="mode === 'cell'" class="w-cell w-refs">
        <template v-for="(t, i) in targets" :key="t.id">
            <a class="w-ref-link" href="#" @click.prevent="open(t)">{{ t.label }}</a><span
                v-if="i < targets.length - 1">、</span>
        </template>
        <span v-if="!targets.length" class="w-empty">—</span>
    </span>
    <el-select v-else :model-value="modelValue || []" multiple filterable remote
        :remote-method="search" :loading="loading" placeholder="搜索并选择多个节点"
        style="width:100%" @visible-change="(open) => open && preload()"
        @update:model-value="emitValue($event)">
        <el-option v-for="o in options" :key="o.id" :label="o.label" :value="o.id" />
    </el-select>
</template>
<script>
export default {
    name: 'WRefs',
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
        // cell 用：每个引用 {id, type, label}
        targets() {
            let list = this.expand
            if (!list) return []
            if (!Array.isArray(list)) list = [list]
            return list.filter(Boolean).map(n => ({ id: n.id, type: n.type, label: n.display || '#' + n.id }))
        },
    },
    data() { return { found: [], loading: false, loaded: false } },
    methods: {
        emitValue(v) { this.$emit('update:modelValue', v) },
        open(t) { this.$emit('open-node', t) },
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
<style>
/* 多个引用：一行放不下就换行 */
.w-refs { flex-wrap: wrap; }
</style>
