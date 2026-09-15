<!-- kind text：单行文本；列表 = 截断。
     文件名就是 kind 名（web/admin/widgets/text.vue），mode="edit" 编辑 / mode="cell" 只读。 -->
<template>
    <span v-if="mode === 'cell'" class="w-cell">{{ Widgets.truncate(modelValue) }}</span>
    <el-input v-else :model-value="modelValue === undefined || modelValue === null ? '' : modelValue"
        @update:model-value="emitValue($event)" />
</template>
<script>
export default {
    name: 'WText',
    props: {
        modelValue: { default: undefined },
        mode: { type: String, default: 'edit' },      // edit | cell
        field: { type: Object, default: () => ({}) },
        defs: { type: Object, default: () => ({}) },  // 类型定义表（ref 显示名用）
    },
    emits: ['update:modelValue'],
    methods: {
        emitValue(v) { this.$emit('update:modelValue', v) },    },
}
</script>
