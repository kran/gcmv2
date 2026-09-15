<!-- kind select：枚举；列表里标签。
     文件名就是 kind 名（web/admin/widgets/select.vue），mode="edit" 编辑 / mode="cell" 只读。 -->
<template>
    <el-tag v-if="mode === 'cell' && modelValue" size="small">{{ modelValue }}</el-tag>
    <el-select v-else-if="mode === 'edit'" :model-value="modelValue" placeholder="请选择"
        style="width:100%;" @update:model-value="emitValue($event)">
        <el-option v-for="o in field.options || []" :key="o" :label="o" :value="o" />
    </el-select>
</template>
<script>
export default {
    name: 'WSelect',
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
