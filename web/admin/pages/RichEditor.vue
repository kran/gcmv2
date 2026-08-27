<template>
    <div class="rich-editor">
        <div ref="el"></div>
        <input type="file" ref="fileInput" style="display:none;" accept="image/*" @change="onFile" />
        <input type="file" ref="videoInput" style="display:none;" accept="video/*" @change="onVideo" />
        <input type="file" ref="audioInput" style="display:none;" accept="audio/*" @change="onAudio" />
    </div>
</template>
<script>
// Quill 富文本包装 (无构建集成; 存 HTML 字符串)。
// 换编辑器 (wangEditor/TinyMCE) 只需改这个文件, FieldRenderer 不动。
//
// 注意: 全量回填一律 convert+setContents, 不用 dangerouslyPasteHTML —
// 后者内部操作选区, 无焦点时会 null.offset 报错 (Quill 2 已知坑)。
//
// **Quill 实例绝不进 Vue 响应式** (data/ref/reactive): Vue 的 reactivity
// Proxy 包装 Quill 实例会导致内部方法 this 错位 → null.offset 崩溃
// (Quill 2 GitHub issue #4375/#4293 官方已知场景)。用普通实例属性。
// 模块注册幂等 (组件多次挂载只注册一次; 模块级变量多实例共享)
let quillResizeRegistered = false

export default {
    props: { modelValue: { type: String, default: '' } },
    emits: ['update:modelValue'],
    mounted() {
        // quill-resize-module (支持 Quill 2): 图片/视频选中后可拖拽调整大小。
        // UMD 也是 esbuild 命名空间 — 类在 .default (与 TableUp 同构)。
        // window flag 幂等: 组件多次挂载 (对话框反复打开) 只注册一次,
        // 避免 Quill 的 "Overwriting" 警告。
        const resizeMod = window.QuillResize && (window.QuillResize.default || window.QuillResize)
        if (resizeMod && !quillResizeRegistered) {
            Quill.register('modules/resize', resizeMod)
            quillResizeRegistered = true
        }
        // 自定义 audio blot: <audio controls> 播放器（前台直接播, 无需转换）
        // Quill embed 边界: 第一个位置的 embed 删除不掉 — 插入时后加空文本 + selection 跳过
        if (!window.__qlAudioRegistered) {
            const BlockEmbed = Quill.import('blots/block/embed')
            class AudioBlot extends BlockEmbed {
                static create(value) {
                    const node = super.create()   // <audio>
                    node.classList.add('ql-audio-blot')
                    node.setAttribute('controls', '')
                    node.setAttribute('preload', 'metadata')
                    const src = document.createElement('source')
                    src.setAttribute('src', value)
                    src.setAttribute('type', 'audio/mpeg')
                    node.appendChild(src)
                    return node
                }
                static value(node) {
                    const src = node.querySelector('source')
                    return src ? src.getAttribute('src') : ''
                }
            }
            AudioBlot.blotName = 'audio'
            AudioBlot.tagName = 'audio'
            Quill.register(AudioBlot)
            window.__qlAudioRegistered = true
        }
        this.quill = new Quill(this.$refs.el, {
            theme: 'snow',
            placeholder: '正文…',
            modules: {
                toolbar: {
                    container: [
                        [{ header: [1, 2, 3, 4, false] }],
                        [{ size: ['small', false, 'large', 'huge'] }],
                        ['bold', 'italic', 'underline', 'strike'],
                        [{ color: [] }, { background: [] }],
                        [{ list: 'ordered' }, { list: 'bullet' }, { align: [] }],
                        ['blockquote', 'code-block', 'link', 'image', 'video', 'audio'],
                        ['clean'],
                    ],
                    handlers: { image: this.pickImage, video: this.pickVideo, audio: this.pickAudio },
                },
                // 拖拽调整大小 (默认: image 宽, video 宽高; minWidth 100/200)
                resize: {},
            },
        })
        // matcher: <audio> 回显 → audio blot + 后补空格
        // （Quill embed 边界: 后面无字符时 backspace 删不掉 — 空格保证删除路径）
        const Delta = Quill.import('delta')
        this.quill.clipboard.addMatcher('AUDIO', function (node, delta) {
            const srcNode = node.querySelector && node.querySelector('source')
            const src = srcNode ? srcNode.getAttribute('src') : ''
            const d = new Delta()
            if (src) {
                d.insert({ audio: src })
                d.insert(' ')   // 后补空格 — 可删字符 → 删除路径正常
            }
            return d
        })
        this.setHTML(this.modelValue)
        this.quill.on('text-change', () => {
            const html = this.quill.root.innerHTML
            // 空编辑器的占位 HTML 归一为空串
            this.$emit('update:modelValue', html === '<p><br></p>' ? '' : html)
        })
    },
    watch: {
        // 外部赋值 (编辑对话框回填): 内容不同才写入, 防光标跳动
        modelValue(v) {
            if (this.quill && v !== this.quill.root.innerHTML) {
                this.setHTML(v)
            }
        },
    },
    methods: {
        setHTML(v) {
            const delta = this.quill.clipboard.convert({ html: v || '' })
            this.quill.setContents(delta)
        },
        // 图片按钮 → 选择文件 → 上传 → 插入当前选区
        pickImage() {
            this.$refs.fileInput.click()
        },
        pickVideo() {
            this.$refs.videoInput.click()
        },
        pickAudio() {
            this.$refs.audioInput.click()
        },
        async onFile(ev) {
            const file = ev.target.files && ev.target.files[0]
            ev.target.value = ''
            if (!file) return
            try {
                const res = await window.$api.upload(file)
                if (!res.path) throw new Error('上传响应缺少 path: ' + JSON.stringify(res))
                let index = this.quill.getLength()
                const range = this.quill.getSelection()
                if (range) index = range.index
                this.quill.insertEmbed(index, 'image', res.path)
                try { this.quill.setSelection(index + 1) } catch (_) {}
                ElMessage.success('图片已插入')
            } catch (err) {
                console.error('[rich-editor] upload/insert failed:', err)
                ElMessage.error('图片插入失败: ' + (err.message || err))
            }
        },
        async onAudio(ev) {
            const file = ev.target.files && ev.target.files[0]
            ev.target.value = ''
            if (!file) return
            try {
                const res = await window.$api.upload(file)
                if (!res.path) throw new Error('上传响应缺少 path')
                let index = this.quill.getLength()
                const range = this.quill.getSelection()
                if (range) index = range.index
                // Quill embed 边界: 后面无字符时删不掉 — 插入后补空格分隔
                this.quill.insertEmbed(index, 'audio', res.path)
                this.quill.insertText(index + 1, ' ', 'silent')
                try { this.quill.setSelection(index + 2) } catch (_) {}
                ElMessage.success('音频已插入')
            } catch (err) {
                console.error('[rich-editor] audio insert failed:', err)
                ElMessage.error('音频插入失败: ' + (err.message || err))
            }
        },
        async onVideo(ev) {
            const file = ev.target.files && ev.target.files[0]
            ev.target.value = ''
            if (!file) return
            try {
                const res = await window.$api.upload(file)
                if (!res.path) throw new Error('上传响应缺少 path: ' + JSON.stringify(res))
                let index = this.quill.getLength()
                const range = this.quill.getSelection()
                if (range) index = range.index
                this.quill.insertEmbed(index, 'video', res.path)
                try { this.quill.setSelection(index + 1) } catch (_) {}
                ElMessage.success('视频已插入')
            } catch (err) {
                console.error('[rich-editor] upload/insert failed:', err)
                ElMessage.error('图片插入失败: ' + (err.message || err))
            }
        },
    },
}
</script>
<style>
.rich-editor .ql-editor { min-height: 180px; font-size: 14px; }
.rich-editor .ql-container { border-radius: 0; }
.rich-editor .ql-toolbar { border-radius: 0; }
/* audio 工具栏图标 */
.rich-editor .ql-toolbar .ql-audio::after {
    content: "♪";
    font-size: 16px;
    font-weight: bold;
    color: #444;
}
/* 编辑区 audio: 不响应鼠标（控件不抢焦点 — blot 可删除/输入） */
.rich-editor .ql-editor .ql-audio-blot audio { pointer-events: none; }
.rich-editor .ql-editor .ql-audio-blot {
    display: block;
    margin: 8px 0;
    background: #f9fafb;
    border: 1px solid #e5e7eb;
    border-radius: 4px;
    padding: 8px;
    user-select: none;
}
</style>
