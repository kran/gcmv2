<template>
    <div class="gallery-editor">
        <!-- 缩略网格（多图紧凑 — 不逐项长表单） -->
        <div class="gallery-grid">
            <div v-for="(p, i) in (value || [])" :key="p" class="gallery-item">
                <img :src="p" class="gallery-thumb" @click="openPreview(p)" />
                <div class="gallery-overlay">
                    <el-button link size="small" @click="move(i, -1)" :disabled="i === 0">‹</el-button>
                    <el-button link size="small" @click="move(i, 1)"
                        :disabled="i === (value || []).length - 1">›</el-button>
                    <el-button link size="small" class="gallery-del" @click="remove(i)">×</el-button>
                </div>
            </div>
            <!-- 上传按钮（多选批量） -->
            <div class="gallery-item gallery-add" @click="pick">
                <el-icon :size="24"><Plus /></el-icon>
                <span>添加图片</span>
            </div>
        </div>
        <input type="file" ref="file" style="display:none;" multiple accept="image/*" @change="uploadBatch" />
        <p class="gallery-hint">点击 + 批量上传（缩略图由处理参数生成 — 前端留原路径）</p>
    </div>
</template>
<script>
export default {
    name: 'GalleryEditor',
    props: {
        modelValue: { type: Array, default: () => [] },
    },
    emits: ['update:modelValue'],
    computed: {
        value() { return this.modelValue || [] },
    },
    methods: {
        pick() { this.$refs.file && this.$refs.file.click() },
        async uploadBatch(ev) {
            const files = Array.from(ev.target.files || [])
            if (!files.length) return
            ElMessage.info('上传中 ' + files.length + ' 张…')
            this.uploading = true
            try {
                const paths = []
                for (const f of files) {
                    const res = await window.$api.upload(f)
                    paths.push(res.path)
                }
                this.$emit('update:modelValue', [...this.value, ...paths])
                ElMessage.success('已上传 ' + files.length + ' 张')
            } catch (e) {
                ElMessage.error('上传失败')
            }
            this.uploading = false
            ev.target.value = ''
        },
        remove(i) {
            const arr = [...this.value]; arr.splice(i, 1)
            this.$emit('update:modelValue', arr)
        },
        move(i, delta) {
            const j = i + delta
            const arr = [...this.value]
            if (j < 0 || j >= arr.length) return
            const t = arr[i]; arr[i] = arr[j]; arr[j] = t
            this.$emit('update:modelValue', arr)
        },
        openPreview(p) { window.open(p, '_blank') },
    },
}
</script>
<style>
.gallery-editor { width: 100%; }
.gallery-grid { display: flex; flex-wrap: wrap; gap: 8px; }
.gallery-item {
    position: relative; width: 100px; height: 100px;
    border-radius: 6px; overflow: hidden; border: 1px solid #eaeaee;
    background: #fafafa; flex-shrink: 0;
}
.gallery-thumb { width: 100%; height: 100%; object-fit: cover; cursor: zoom-in; display: block; }
.gallery-overlay {
    position: absolute; inset: 0; display: flex; align-items: center; justify-content: center;
    gap: 2px; background: rgba(0,0,0,0.4); opacity: 0; transition: opacity .15s;
}
.gallery-item:hover .gallery-overlay { opacity: 1; }
.gallery-overlay .el-button { color: #fff; padding: 2px; }
.gallery-del { color: #ff7875 !important; }
.gallery-add {
    display: flex; flex-direction: column; align-items: center; justify-content: center;
    border: 1px dashed #ccc; cursor: pointer; color: #999; transition: all .15s;
}
.gallery-add:hover { border-color: #409eff; color: #409eff; }
.gallery-hint { font-size: 12px; color: #aaa; margin-top: 6px; }
</style>
