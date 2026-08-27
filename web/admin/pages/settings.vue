<template>
  <div>
    <div style="display:flex;gap:8px;align-items:center;margin-bottom:14px;">
      <span style="font-size:16px;font-weight:600;">站点配置</span>
      <el-select v-model="group" placeholder="分组" size="small" style="width:160px" clearable @change="refresh">
        <el-option v-for="g in groups" :key="g" :label="g || '(未分组)'" :value="g" />
      </el-select>
      <el-button size="small" @click="refresh">刷新</el-button>
      <el-button type="primary" size="small" @click="openEdit(null)">新建</el-button>
    </div>

    <el-table :data="rows" v-loading="loading" >
      <el-table-column prop="key" label="key" width="200">
        <template #default="{ row: r }"><code>{{ r.key }}</code></template>
      </el-table-column>
      <el-table-column prop="group" label="分组" width="110" />
      <el-table-column label="类型" width="100">
        <template #default="{ row: r }"><el-tag size="small">{{ r.type }}</el-tag></template>
      </el-table-column>
      <el-table-column label="value" min-width="240" show-overflow-tooltip>
        <template #default="{ row: r }"><code style="font-size:12px;">{{ valueOf(r) }}</code></template>
      </el-table-column>
      <el-table-column label="更新时间" width="165">
        <template #default="{ row: r }">{{ fmt(r.updated_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="130" fixed="right">
        <template #default="{ row: r }">
          <el-button link type="primary" size="small" @click="openEdit(r)">编辑</el-button>
          <el-button link type="danger" size="small" @click="doDelete(r)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog append-to-body v-model="dialog.visible" :title="dialog.isNew ? '新建配置' : '编辑 ' + dialog.key" width="60vw">
      <el-form label-width="60px">
        <el-form-item label="key">
          <el-input v-model="dialog.key" :disabled="!dialog.isNew" placeholder="footer / seo.default" />
        </el-form-item>
        <el-form-item label="分组">
          <el-input v-model="dialog.group" placeholder="site / seo / list" />
        </el-form-item>
        <el-form-item label="类型">
          <el-radio-group v-model="dialog.kind" class="kind-radios">
            <el-radio v-for="k in kinds" :key="k" :value="k" size="small">{{ k }}</el-radio>
          </el-radio-group>
        </el-form-item>

        <!-- 按类型渲染控件 -->
        <el-form-item v-if="dialog.kind === 'text'" label="value">
          <el-input v-model="dialog.scalar" placeholder="字符串值" />
        </el-form-item>
        <el-form-item v-else-if="dialog.kind === 'textarea'" label="value">
          <el-input v-model="dialog.scalar" type="textarea" :rows="4" />
        </el-form-item>
        <el-form-item v-else-if="dialog.kind === 'number'" label="value">
          <el-input-number v-model="dialog.scalar" style="width:200px;" />
        </el-form-item>
        <el-form-item v-else-if="dialog.kind === 'bool'" label="value">
          <el-switch v-model="dialog.scalar" />
        </el-form-item>

        <!-- object: 键值对列表（自由结构, 无需子定义） -->
        <el-form-item v-else-if="dialog.kind === 'object'" label="字段">
          <div style="width:100%;">
            <div v-for="(kv, i) in dialog.entries" :key="i" style="display:flex;gap:8px;margin-bottom:8px;">
              <el-input v-model="kv.name" placeholder="键" style="width:140px;" />
              <el-input v-model="kv.value" placeholder="值 (JSON 或字符串)" style="flex:1;" />
              <el-button link type="danger" @click="dialog.entries.splice(i, 1)">删</el-button>
            </div>
            <el-button size="small" @click="dialog.entries.push({ name: '', value: '' })">+ 添加字段</el-button>
          </div>
        </el-form-item>

        <el-form-item v-else-if="dialog.kind === 'upload-file'" label="value">
          <div style="width:100%;">
            <div style="display:flex;gap:8px;">
              <el-input v-model="dialog.scalar" placeholder="/uploads/xxx.pdf" style="flex:1;" />
              <el-button size="small" @click="pickFile">上传</el-button>
              <input ref="fileInput" type="file" style="display:none;" @change="onFile" />
            </div>
          </div>
        </el-form-item>
        <el-form-item v-else-if="dialog.kind === 'richtext'" label="value">
          <rich-editor :model-value="dialog.scalar || ''" style="width:100%;"
            @update:model-value="dialog.scalar = $event" />
        </el-form-item>

        <!-- array: 值列表（自由元素, JSON 或字符串） -->
        <el-form-item v-else-if="dialog.kind === 'array'" label="元素">
          <div style="width:100%;">
            <div v-for="(v, i) in dialog.array" :key="i" style="display:flex;gap:8px;margin-bottom:8px;">
              <el-input v-model="dialog.array[i]" placeholder="元素 (JSON 或字符串)" style="flex:1;" />
              <el-button link type="danger" @click="dialog.array.splice(i, 1)">删</el-button>
            </div>
            <el-button size="small" @click="dialog.array.push('')">+ 添加元素</el-button>
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog.visible = false">取消</el-button>
        <el-button type="primary" :loading="dialog.saving" @click="doSave">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>
<script>

export default {
    name: 'SettingsPage',
    components: { RichEditor: Vue.defineAsyncComponent(() => window.Panel.loadComponent('pages/RichEditor.vue')) },
    data() {
        return {
            rows: [], groups: [], group: '', loading: false,
            // 编辑形态枚举（cmx piece 同款 — 前端按形态渲染控件）
            kinds: ['text', 'textarea', 'number', 'bool', 'object', 'array', 'upload-file', 'richtext'],
            dialog: { visible: false, saving: false, isNew: true, key: '', group: '',
                kind: 'textarea', scalar: null, entries: [], array: [] },
        }
    },
    async mounted() { await this.refresh() },
    methods: {
        async refresh() {
            this.loading = true
            try {
                const res = await window.$api.get('/admin/settings', { group: this.group })
                this.rows = res.items || []
                const gs = {}
                this.rows.forEach(r => { if (!gs[r.group]) gs[r.group] = true })
                this.groups = Object.keys(gs).sort()
            } finally { this.loading = false }
        },
        valueOf(r) {
            if (r.value === null || r.value === undefined) return '—'
            const s = JSON.stringify(r.value)
            return s.length > 40 ? s.slice(0, 40) + '…' : s
        },
        fmt(s) { return s ? s.replace('T', ' ').slice(0, 16) : '' },
        openEdit(r) {
            this.dialog.isNew = !r
            this.dialog.key = r ? r.key : ''
            this.dialog.group = r ? r.group : ''
            // gcm 后端已解码 Value（cmx 是 JSON 字符串需 parse — 移植时删）
            let v = r ? r.value : {}

            // 类型以存库为准（string/text/file/richtext 值都是字符串, 反推不可靠）
            const ed = this.toEditable(v)
            ed.kind = r ? r.type : 'object' // 新建默认 object
            this.dialog.kind = ed.kind
            this.dialog.scalar = ed.scalar
            this.dialog.entries = ed.entries
            this.dialog.array = ed.array

            this.dialog.visible = true
        },
        async doSave() {
            this.dialog.saving = true
            try {
                await window.$api.post('/admin/settings', {
                    key: this.dialog.key,
                    group: this.dialog.group,
                    type: this.dialog.kind,
                    value: this.fromEditable(this.dialog.kind, this.dialog),
                })
                ElMessage.success('已保存')
                this.dialog.visible = false
                this.refresh()
            } catch (_) { /* api.js 已提示 */ }
            this.dialog.saving = false
        },
        async doDelete(r) {
            await ElMessageBox.confirm('删除配置 ' + r.key + ' ?', '确认', { type: 'warning' })
            await window.$api.del('/admin/settings/' + r.key)
            ElMessage.success('已删除')
            this.refresh()
        },
        pickFile() { this.$refs.fileInput && this.$refs.fileInput.click() },
        async onFile(ev) {
            const file = ev.target.files && ev.target.files[0]
            ev.target.value = ''
            if (!file) return
            try {
                const res = await window.$api.upload(file)
                this.dialog.scalar = res.path
                ElMessage.success('已上传')
            } catch (_) {}
        },
        // JSON 值 → 编辑中间形态（typeOf 推断; 打开时以存库 type 覆盖）
        toEditable(v) {
            let kind = 'text' // 字符串默认（kind 改名后: string→text）
            if (v !== null && v !== undefined && v !== '') {
                const t = typeof v
                if (t === 'object') kind = Array.isArray(v) ? 'array' : 'object'
                else if (t === 'number' || t === 'boolean') kind = t
            }
            const out = { kind, scalar: null, entries: [], array: [] }
            if (kind === 'number') out.scalar = typeof v === 'number' ? v : Number(v) || 0
            else if (kind === 'bool') out.scalar = !!v
            else if (kind === 'text' || kind === 'textarea' || kind === 'richtext' || kind === 'upload-file') out.scalar = v
            else if (kind === 'object') {
                out.entries = Object.entries(v || {}).map(kv => ({ name: kv[0], value: typeof kv[1] === 'string' ? kv[1] : JSON.stringify(kv[1]) }))
            } else if (kind === 'array') {
                out.array = (v || []).map(x => typeof x === 'string' ? x : JSON.stringify(x))
            }
            return out
        },
        // 编辑中间形态 → JSON 值
        fromEditable(kind, e) {
            if (kind === 'number') return e.scalar == null || e.scalar === '' ? 0 : Number(e.scalar)
            if (kind === 'bool') return !!e.scalar
            if (kind === 'text' || kind === 'textarea' || kind === 'upload-file' || kind === 'richtext') {
                return e.scalar == null ? '' : String(e.scalar)
            }
            if (kind === 'object') {
                const out = {}
                e.entries.forEach(kv => { if (kv.name) out[kv.name] = this.parseVal(kv.value) })
                return out
            }
            if (kind === 'array') return e.array.map(this.parseVal).filter(x => x !== '')
            return null
        },
        parseVal(s) {
            const t = String(s).trim()
            if (t === '') return ''
            try { return JSON.parse(t) } catch (_) { return t }
        },
    },
}
</script>
