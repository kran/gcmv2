<!-- kind number：数字；列表里右对齐。
     文件名就是 kind 名（web/admin/widgets/number.vue），mode="edit" 编辑 / mode="cell" 只读。 -->
<template>
    <span v-if="mode === 'cell'" class="w-cell w-num">{{ modelValue === undefined || modelValue === null ? '' : modelValue }}</span>
    <el-input-number v-else :model-value="modelValue" @update:model-value="emitValue($event)" style="width:200px;" />
</template>
<script>
export default {
    name: 'WNumber',
    props: {
        modelValue: { default: undefined },
        mode: { type: String, default: 'edit' },      // edit | cell
        field: { type: Object, default: () => ({}) },
        defs: { type: Object, default: () => ({}) },  // 类型定义表（ref 显示名用）
        preset: { type: Array, default: () => [] },   // 引用已选值（编辑回显）
        expand: { default: null },                    // 列表接口批量展开的引用目标
    },
    emits: ['update:modelValue'],
    methods: {
        emitValue(v) { this.$emit('update:modelValue', v) },    },
}
</script>
<style>
/* 数字：列表里右对齐 */
.w-num { justify-content: flex-end; }
</style>
