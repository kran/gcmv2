<!-- kind richtext：富文本；编辑 = 富文本编辑器，列表 = 去标签截断。
     文件名就是 kind 名（web/admin/widgets/richtext.vue），mode="edit" 编辑 / mode="cell" 只读。 -->
<template>
    <span v-if="mode === 'cell'" class="w-cell">{{ Widgets.truncate(Widgets.plain(modelValue)) }}</span>
    <rich-editor v-else :model-value="modelValue || ''" @update:model-value="emitValue($event)" />
</template>
<script>
export default {
    name: 'WRichtext',
    props: {
        modelValue: { default: undefined },
        mode: { type: String, default: 'edit' },      // edit | cell
        field: { type: Object, default: () => ({}) },
        defs: { type: Object, default: () => ({}) },  // 类型定义表（ref 显示名用）
        preset: { type: Array, default: () => [] },   // 引用已选值（编辑回显）
        expand: { default: null },                    // 列表接口批量展开的引用目标
    },
    emits: ['update:modelValue'],
    components: { RichEditor: Vue.defineAsyncComponent(() => window.Panel.loadComponent('pages/RichEditor.vue')) },
    methods: {
        emitValue(v) { this.$emit('update:modelValue', v) },    },
}
</script>
