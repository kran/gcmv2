/* gcm 业务端点 (数据层): 只做 URL 与参数映射, 请求细节在 Panel。 */
window.$api = {
    get: function (url, params) { return Panel.get(url, params) },
    post: function (url, body) { return Panel.post(url, body) },
    put: function (url, body) { return Panel.put(url, body) },
    del: function (url) { return Panel.del(url) },

    // 认证
    me:     function () { return Panel.get('/admin/me') },
    types:  function () { return Panel.get('/admin/types') },
    login:  function (username, password) { return Panel.post('/admin/login', { username: username, password: password }) },
    logout: function () { return Panel.post('/admin/logout') },
    changePassword: function (oldPwd, newPwd) { return Panel.post('/admin/password', { old_password: oldPwd, new_password: newPwd }) },

    // 节点 (按 type 管理; ref 字段经 fields 提交, 引擎落边)
    nodes:      function (query) { return Panel.get('/admin/nodes', query) },
    node:       function (id) { return Panel.get('/admin/nodes/' + id) },
    createNode: function (typ, n) { return Panel.post('/admin/nodes?type=' + typ, n) },
    updateNode: function (id, n) { return Panel.put('/admin/nodes/' + id, n) },
    deleteNode: function (id) { return Panel.del('/admin/nodes/' + id) },

    // 实体搜索 (引用编辑器)
    search: function (query) { return Panel.get('/admin/search', query) },

    // refLabel: 节点显示名（任何消费端统一）— 后台列表/引用选择器用。
    // 优先级: title 投影列 → slug → 类型定义字段序第一个非空字符串标量
    // （关系节点兜底, 如 employment 的 role）→ expand 引用合成 → #id。
    refLabel: function (n, def) {
        if (!n) { console.log('[refLabel] null node'); return '#?' }
        if (n.title) return n.title
        if (n.slug) return n.slug
        if (def) {
            for (const f of def.fields || []) {
                const v = (n.fields || {})[f.name]
                if (typeof v === 'string' && v.trim()) return v
            }
        }
        const parts = []
        for (const v of Object.values(n.expand || {})) {
            const arr = Array.isArray(v) ? v : (v ? [v] : [])
            for (const m of arr) if (m && m.title) parts.push(m.title)
        }
        if (parts.length) return parts.join('·')
        // 兜底: 打日志定位 — 为什么没走到 title/slug/def/expand
        console.log('[refLabel] 兜底 #id:', { id: n.id, type: n.type, title: n.title, slug: n.slug,
            hasDef: !!def, fields: n.fields, expandKeys: Object.keys(n.expand || {}) })
        return '#' + n.id
    },

    // 上传 (multipart; 不经 Panel 的 JSON 通道)
    upload: function (file) {
        var fd = new FormData()
        fd.append('file', file)
        return fetch('/admin/upload', { method: 'POST', body: fd, credentials: 'same-origin' })
            .then(async function (resp) {
                var body = {}
                try { body = await resp.json() } catch (_) {}
                if (!resp.ok) {
                    var err = new Error(body.error || ('HTTP ' + resp.status))
                    err.status = resp.status
                    if (resp.status !== 401 && window.ElementPlus) ElementPlus.ElMessage.error(err.message)
                    throw err
                }
                return body
            })
    },
}
