<!-- kind upload-file：单文件；编辑 = 路径 + 上传，列表 = 文件名链接。
     文件名就是 kind 名（web/admin/widgets/upload-file.vue），mode="edit" 编辑 / mode="cell" 只读。 -->
<template>
    <span v-if="mode === 'cell'" class="w-cell">
        <a v-if="modelValue" :href="modelValue" target="_blank">{{ Widgets.fileName(modelValue) }}</a>
        <span v-else class="w-empty">—</span>
    </span>
    <div v-else class="w-image">
        <el-input :model-value="modelValue" @update:model-value="emitValue($event)" placeholder="/uploads/xxx.mp4" />
        <input type="file" style="display:none;" ref="file" @change="upload" />
        <el-button size="small" @click="pick">上传</el-button>
        <a v-if="modelValue" :href="modelValue" target="_blank" class="w-file-name">{{ Widgets.fileName(modelValue) }}</a>
    </div>
</template>
<script>
export default {
    name: 'WFile',
    props: {
        modelValue: { default: undefined },
        mode: { type: String, default: 'edit' },      // edit | cell
        field: { type: Object, default: () => ({}) },
        defs: { type: Object, default: () => ({}) },  // 类型定义表（ref 显示名用）
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
/* 单文件：路径输入 + 上传按钮 + 文件名一行 */
.w-image { display: flex; gap: 8px; align-items: center; width: 100%; }
.w-file-name { font-size: 12px; color: #666; }
</style>
