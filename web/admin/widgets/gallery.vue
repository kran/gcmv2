<!-- kind gallery：多图；编辑 = 图集编辑器，列表 = 前 3 张缩略图 + 计数。
     文件名就是 kind 名（web/admin/widgets/gallery.vue），mode="edit" 编辑 / mode="cell" 只读。 -->
<template>
    <span v-if="mode === 'cell'" class="w-cell w-gallery">
        <img v-for="url in thumbs" :key="url" :src="url" class="w-thumb" />
        <span v-if="rest > 0" class="w-more">+{{ rest }}</span>
        <span v-if="!list.length" class="w-empty">—</span>
    </span>
    <gallery-editor v-else :model-value="modelValue || []" @update:model-value="emitValue($event)" />
</template>
<script>
export default {
    name: 'WGallery',
    props: {
        modelValue: { default: undefined },
        mode: { type: String, default: 'edit' },      // edit | cell
        field: { type: Object, default: () => ({}) },
        defs: { type: Object, default: () => ({}) },  // 类型定义表（ref 显示名用）
        preset: { type: Array, default: () => [] },   // 引用已选值（编辑回显）
        expand: { default: null },                    // 列表接口批量展开的引用目标
    },
    emits: ['update:modelValue'],
    components: { GalleryEditor: Vue.defineAsyncComponent(() => window.Panel.loadComponent('pages/GalleryEditor.vue')) },
    computed: {
        list() { return Array.isArray(this.modelValue) ? this.modelValue : [] },
        thumbs() { return this.list.slice(0, 3) },
        rest() { return Math.max(0, this.list.length - 3) },
    },
    methods: {
        emitValue(v) { this.$emit('update:modelValue', v) },    },
}
</script>
<style>
/* 多图：列表里最多 3 张缩略图 + 计数 */
.w-gallery { flex-wrap: wrap; }
.w-thumb { width: 30px; height: 30px; object-fit: cover; border-radius: 3px; border: 1px solid #eee; }
.w-more { color: #999; font-size: 12px; }
</style>
