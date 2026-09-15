<!-- kind upload-image：单图；编辑 = 路径 + 上传，列表 = 缩略图。
     文件名就是 kind 名（web/admin/widgets/upload-image.vue），mode="edit" 编辑 / mode="cell" 只读。 -->
<template>
    <span v-if="mode === 'cell'" class="w-cell">
        <img v-if="modelValue" :src="modelValue" class="w-thumb" />
        <span v-else class="w-empty">—</span>
    </span>
    <div v-else class="w-image">
        <el-input :model-value="modelValue" @update:model-value="emitValue($event)" placeholder="/uploads/xxx.png" />
        <input type="file" style="display:none;" accept="image/*" ref="file" @change="upload" />
        <el-button size="small" @click="pick">上传</el-button>
        <img v-if="modelValue" :src="modelValue" class="w-image-preview" />
    </div>
</template>
<script>
export default {
    name: 'WImage',
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
        emitValue(v) { this.$emit('update:modelValue', v) },
        pick() { if (this.$refs.file) this.$refs.file.click() },
        async upload(ev) {
            const file = ev.target.files && ev.target.files[0]
            if (!file) return
            try {
                const res = await window.$api.upload(file)
                this.emitValue(res.path)
                ElMessage.success('已上传')
            } catch (_) {}
            ev.target.value = ''
        },
    },
}
</script>
<style>
.w-image { display: flex; gap: 8px; align-items: center; width: 100%; }
.w-image-preview { max-height: 56px; border-radius: 3px; border: 1px solid #eee; }
.w-thumb { width: 30px; height: 30px; object-fit: cover; border-radius: 3px; border: 1px solid #eee; }
</style>
