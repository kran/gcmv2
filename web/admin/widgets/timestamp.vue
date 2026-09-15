<!-- kind timestamp：时间点；库里/接口是统一格式（UTC + 秒精度 + Z），界面按设备本地时间。
     文件名就是 kind 名（web/admin/widgets/timestamp.vue），mode="edit" 编辑 / mode="cell" 只读。 -->
<template>
    <span v-if="mode === 'cell'" class="w-cell">{{ modelValue ? Widgets.localTime(modelValue) : '' }}</span>
    <el-date-picker v-else type="datetime" :model-value="toDate(modelValue)" :clearable="true"
        placeholder="选择时间" style="width:230px;"
        @update:model-value="emitValue(toCanonical($event))" />
</template>
<script>
export default {
    name: 'WDatetime',
    props: {
        modelValue: { default: undefined },
        mode: { type: String, default: 'edit' },      // edit | cell
        field: { type: Object, default: () => ({}) },
        defs: { type: Object, default: () => ({}) },  // 类型定义表（ref 显示名用）
    },
    emits: ['update:modelValue'],
    methods: {
        emitValue(v) { this.$emit('update:modelValue', v) },
        toDate(v) { return v ? new Date(v) : null },
        toCanonical(v) { return v ? new Date(v).toISOString().slice(0, 19) + 'Z' : null },
    },
}
</script>
