/* Panel 前端框架 (gcm): SFC 加载器 + Vue 装配 + 通用 http 工具。
 * 业务端点见 api.js, 壳组件见 pages/App.vue。
 * 与 vox 版差异: 裸 JSON 协议 (非 {code,msg,data} 封套), 支持 GET/POST/PUT/DELETE。 */
window.Panel = (function () {

    // ElementPlus 命令式 API 全局暴露
    if (window.ElementPlus) {
        window.ElMessage = window.ElementPlus.ElMessage
        window.ElMessageBox = window.ElementPlus.ElMessageBox
    }

    var _app = null
    var _router = null

    // ═══════════════════════════════════════════════════
    //  SFC 加载: vue3-sfc-loader 浏览器运行时编译
    // ═══════════════════════════════════════════════════

    var _loadModule = window['vue3-sfc-loader'] && window['vue3-sfc-loader'].loadModule
    if (!_loadModule) throw new Error('[panel] vue3-sfc-loader missing in vendor')

    var _sfcOptions = {
        moduleCache: { vue: Vue, 'vue-router': VueRouter, '$api': window.$api },
        async getFile(url) {
            // 缓存破坏: 开发期改 .vue 即生效（fetch 不受强缓存影响）
            var bustUrl = url + (url.indexOf('?') >= 0 ? '&' : '?') + '_t=' + Date.now()
            var resp = await fetch(bustUrl, { cache: 'no-store' })
            if (!resp.ok) {
                console.error('[panel] fetch failed:', resp.status, url)
                throw new Error('[panel] fetch failed: ' + url)
            }
            return {
                getContentData: function (asBinary) {
                    return asBinary ? resp.arrayBuffer() : resp.text()
                },
            }
        },
        addStyle: function (textContent) {
            var style = document.createElement('style')
            style.textContent = textContent
            document.head.appendChild(style)
        },
        log: function (type, scope, message) {
            (type === 'error' ? console.error : console.log)('[' + scope + ']', message)
        },
    }

    function loadComponent(url) {
        console.log('[panel] loadComponent:', url)
        var p = _loadModule(url, _sfcOptions)
        p.then(function () { console.log('[panel] loaded:', url) })
         .catch(function (err) { console.error('[panel] load FAILED:', url, err) })
        return p
    }

    // ═══════════════════════════════════════════════════
    //  http 工具: cmx 裸 JSON 协议
    //    成功: 2xx + 任意 JSON 直接返回
    //    失败: 非 2xx + {"error": msg} → throw (status 挂在 err 上)
    // ═══════════════════════════════════════════════════

    var _onError = null

    // 注册全局错误回调 (如 err.status === 401 → 登出)
    function onError(fn) {
        _onError = fn
    }

    function _toast(msg) {
        if (window.ElementPlus) window.ElementPlus.ElMessage.error(msg)
    }

    async function request(method, url, params) {
        var fullUrl = url
        var opts = { method: method, headers: {}, credentials: 'same-origin' }

        if (method === 'GET' && params) {
            var qs = new URLSearchParams()
            for (var k in params) {
                var v = params[k]
                if (v !== undefined && v !== null && v !== '') qs.append(k, v)
            }
            var s = qs.toString()
            if (s) fullUrl += (url.indexOf('?') !== -1 ? '&' : '?') + s
        }
        if (method !== 'GET' && params !== undefined) {
            opts.headers['Content-Type'] = 'application/json'
            opts.body = JSON.stringify(params)
        }

        var err = null
        var body = null
        try {
            var resp = await fetch(fullUrl, opts)
            var text = await resp.text()
            try { body = text ? JSON.parse(text) : {} } catch (_) { body = {} }

            if (!resp.ok) {
                err = new Error(body.error || ('HTTP ' + resp.status))
                err.status = resp.status
                if (resp.status !== 401) _toast(err.message)
            }
        } catch (e) {
            if (!err) {
                err = new Error('network error')
                _toast('网络请求失败')
            }
        }

        if (err) {
            if (_onError) _onError(err)
            throw err
        }
        return body
    }

    function get(url, params) { return request('GET', url, params) }
    function post(url, body) { return request('POST', url, body) }
    function put(url, body) { return request('PUT', url, body) }
    function del(url) { return request('DELETE', url) }

    // ═══════════════════════════════════════════════════
    //  装配
    // ═══════════════════════════════════════════════════

    var _loadingComp = {
        template: '<div style="padding:40px;text-align:center;color:#999;">加载中...</div>'
    }

    // createApp(options)
    //   root:   根组件 .vue 路径 (壳组件, 业务侧提供)
    //   routes: [{name, path, url}]
    async function createApp(options) {
        var vueRoutes = (options.routes || []).map(function (r) {
            return {
                name: r.name,
                path: r.path,
                component: Vue.defineAsyncComponent({
                    loader: function () { return loadComponent(r.url) },
                    loadingComponent: _loadingComp,
                    delay: 100,
                }),
            }
        })

        _router = VueRouter.createRouter({
            history: VueRouter.createWebHashHistory(),
            routes: vueRoutes,
        })

        var root = await loadComponent(options.root)
        _app = Vue.createApp(root)

        _app.use(_router)
        _app.use(ElementPlus, { size: 'small', locale: window.ElementPlusLocaleZhCn })

        var EPIcons = window.ElementPlusIconsVue || {}
        for (var key in EPIcons) {
            if (EPIcons.hasOwnProperty(key)) _app.component(key, EPIcons[key])
        }

        _app.mount('#panel-app')
        return _app
    }

    return {
        get: get,
        post: post,
        put: put,
        del: del,
        onError: onError,
        loadComponent: loadComponent,
        createApp: createApp,
        get app() { return _app },
        get router() { return _router },
    }
})()
