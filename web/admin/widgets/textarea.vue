<!-- kind textarea：多行文本；列表 = 压成一行截断。
     文件名就是 kind 名（web/admin/widgets/textarea.vue），mode="edit" 编辑 / mode="cell" 只读。 -->
<template>
    <span v-if="mode === 'cell'" class="w-cell">{{ Widgets.truncate(oneLine) }}</span>
    <el-input v-else type="textarea" :rows="4"
        :model-value="modelValue === undefined || modelValue === null ? '' : modelValue"
        @update:model-value="emitValue($event)" />
</template>
<script>
export default {
    name: 'WTextarea',
    props: {
        modelValue: { default: undefined },
        mode: { type: String, default: 'edit' },      // edit | cell
        field: { type: Object, default: () => ({}) },
        defs: { type: Object, default: () => ({}) },  // 类型定义表（ref 显示名用）
    },
    emits: ['update:modelValue'],
    computed: {
        oneLine() { return String(this.modelValue || "").replace(/\s+/g, " ") },
    },
    methods: {
        emitValue(v) { this.$emit('update:modelValue', v) },    },
}
</script>
