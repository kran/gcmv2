<template>
  <div style="padding:20px;">
    <div style="display:flex;align-items:center;gap:12px;margin-bottom:16px;">
      <el-button type="primary" size="small" :loading="busy" @click="create">立即备份</el-button>
    </div>
    <el-table :data="items" v-loading="busy" style="width:100%;">
      <el-table-column prop="name" label="文件" min-width="220" />
      <el-table-column label="大小" width="100">
        <template #default="{ row }">{{ fmtSize(row.size) }}</template>
      </el-table-column>
      <el-table-column label="时间" width="170">
        <template #default="{ row }">{{ fmtTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="150">
        <template #default="{ row }">
          <el-button size="small" @click="download(row)">下载</el-button>
          <el-button size="small" type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
    <p style="color:#9aa3ad;font-size:12px;margin-top:12px;">
      备份为 SQLite 一致快照（VACUUM INTO）。恢复方式: 下载后停服替换数据库文件再启动。
    </p>
  </div>
</template>
<script>
export default {
  data() { return { items: [], busy: false } },
  mounted() { this.refresh() },
  methods: {
    refresh() {
      this.busy = true
      window.$api.get('/admin/backup').then((d) => { this.items = d.items || [] })
        .catch(() => {}).finally(() => { this.busy = false })
    },
    create() {
      this.busy = true
      window.$api.post('/admin/backup').then(() => {
        ElMessage.success('备份完成')
        this.refresh()
      }).catch(() => {}).finally(() => { this.busy = false })
    },
    download(row) {
      window.open('/admin/backup/download/' + row.name, '_blank')
    },
    remove(row) {
      ElMessageBox.confirm('删除备份 ' + row.name + '？', '确认', { type: 'warning' })
        .then(() => window.$api.del('/admin/backup/' + row.name))
        .then(() => { ElMessage.success('已删除'); this.refresh() })
        .catch(() => {})
    },
    fmtSize(n) {
      if (n > 1024 * 1024) return (n / 1024 / 1024).toFixed(1) + ' MB'
      if (n > 1024) return (n / 1024).toFixed(1) + ' KB'
      return n + ' B'
    },
    fmtTime(s) { return s ? s.replace('T', ' ').slice(0, 19) : '' },
  },
}
</script>
