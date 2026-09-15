<!-- kind slug：URL 段；编辑 = 等宽输入 + 提示，列表 = code。
     文件名就是 kind 名（web/admin/widgets/slug.vue），mode="edit" 编辑 / mode="cell" 只读。 -->
<template>
    <span v-if="mode === 'cell'" class="w-cell w-slug">{{ modelValue }}</span>
    <el-input v-else :model-value="modelValue === undefined || modelValue === null ? '' : modelValue"
        placeholder="英文/数字/连字符，如 about-us" @update:model-value="emitValue($event)" />
</template>
<script>
export default {
    name: 'WSlug',
    props: {
        modelValue: { default: undefined },
        mode: { type: String, default: 'edit' },      // edit | cell
        field: { type: Object, default: () => ({}) },
        defs: { type: Object, default: () => ({}) },
        preset: { type: Array, default: () => [] },
        expand: { default: null },
    },
    emits: ['update:modelValue'],
    methods: {
        emitValue(v) { this.$emit('update:modelValue', v) },
    },
}
</script>
<style>
/* URL 段：列表里显示成等宽 code */
.w-slug { font-size: 11px; color: #444; }
</style>
