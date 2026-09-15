<!-- kind timestamp：时间点；库里/接口是统一格式（UTC + 秒精度 + Z），界面按设备本地时间。
     文件名就是 kind 名（web/admin/widgets/timestamp.vue），mode="edit" 编辑 / mode="cell" 只读。 -->
<template>
    <span v-if="mode === 'cell'" class="w-cell">{{ modelValue ? Widgets.localTime(modelValue) : '' }}</span>
    <span v-else class="w-timestamp">
        <el-date-picker type="datetime" :model-value="toDate(modelValue)" :clearable="true"
            placeholder="选择时间" style="width:230px;"
            @update:model-value="emitValue($event)" />
        <!-- 库里的值不是统一格式（老库/纪元数字）→ 选择器读不出来：提示出来,
             不能静默清空（那是数据丢失, 也会把"没改"判成"改了"）。 -->
        <span v-if="!parseable" class="w-timestamp-warn">库里是旧格式：{{ modelValue }}（用 tools/legacy-time 迁移）</span>
    </span>
</template>
<script>
// 统一格式的判定：必须是 "YYYY-MM-DDTHH:MM:SS…Z" 这种字符串。
// JSON 数字（纪元秒）绝不能当毫秒解析 —— new Date(1790179200) 是 1970 年, 会静默写坏数据。
function parseable(v) {
    if (v === undefined || v === null || v === '') return true
    if (typeof v !== 'string') return false
    if (!/^\d{4}-\d{2}-\d{2}T/.test(v)) return false
    return !isNaN(new Date(v).getTime())
}

export default {
    name: 'WDatetime',
    props: {
        modelValue: { default: undefined },
        mode: { type: String, default: 'edit' },      // edit | cell
        field: { type: Object, default: () => ({}) },
        defs: { type: Object, default: () => ({}) },  // 类型定义表（ref 显示名用）
    },
    emits: ['update:modelValue'],
    computed: {
        parseable() { return parseable(this.modelValue) },
    },
    methods: {
        emitValue(raw) {
            const v = this.toCanonical(raw)
            if (v === null && !this.parseable) return
            // 选择器解析不了库里的值（emit null）时不要回写：留着原值, 提示用户迁移。
            if (v === this.modelValue) return
            this.$emit('update:modelValue', v)
        },
        toDate(v) { return parseable(v) && v ? new Date(v) : null },
        toCanonical(v) { return v ? new Date(v).toISOString().slice(0, 19) + 'Z' : null },
    },
}
</script>
<style>
/* 编辑态：选择器 + 旧值提示 */
.w-timestamp { display: inline-flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.w-timestamp-warn { font-size: 12px; color: var(--el-color-warning); }
</style>
