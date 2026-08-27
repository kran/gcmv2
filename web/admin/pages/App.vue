<template>
    <!-- 初始化加载 -->
    <div v-if="phase === 'loading'" class="panel-init-loading">
        <div class="panel-init-spinner"></div>
        <div class="panel-init-text">gcm Admin</div>
    </div>

    <!-- 登录 -->
    <div v-else-if="phase === 'login'" class="panel-login-wrapper">
        <div class="panel-login-card">
            <div class="panel-login-logo">
                <span>gcm</span>
            </div>
            <div class="panel-login-subtitle">{{ siteName }} · 内容管理</div>
            <el-form @submit.prevent="doLogin" class="panel-login-form">
                <el-input v-model="loginForm.username" placeholder="用户名" size="large">
                    <template #prefix><el-icon><User /></el-icon></template>
                </el-input>
                <el-input v-model="loginForm.password" type="password" placeholder="密码" size="large"
                    show-password @keyup.enter="doLogin">
                    <template #prefix><el-icon><Lock /></el-icon></template>
                </el-input>
                <el-alert v-if="loginForm.error" :title="loginForm.error" type="error"
                    :closable="false" show-icon style="margin-bottom:4px;" />
                <el-button type="primary" :loading="loginForm.loading" size="large" style="width:100%"
                    @click="doLogin">登 录</el-button>
            </el-form>
        </div>
    </div>

    <!-- 主布局: Jira 骨架 (顶部横栏贯穿 + 下方侧栏/内容) -->
    <div v-else class="panel-shell">
        <header class="panel-topbar">
            <span class="topbar-logo">gcm</span>
            <span class="topbar-title">{{ siteName }}{{ siteName ? ' · ' : '' }}{{ pageTitle }}</span>
            <div class="topbar-right">
                <el-dropdown>
                    <span class="topbar-user">
                        <el-icon><User /></el-icon>
                        {{ user?.username || 'admin' }}
                    </span>
                    <template #dropdown>
                        <el-dropdown-menu>
                            <el-dropdown-item @click="doLogout">退出登录</el-dropdown-item>
                        </el-dropdown-menu>
                    </template>
                </el-dropdown>
            </div>
        </header>

        <div class="panel-body">
            <!-- 左侧主菜单（默认展开: 图标 + 文字标签） -->
            <aside class="panel-sidebar expanded">
                <div class="panel-menu-scroll">
                    <div class="panel-menu expanded">
                        <!-- 动态菜单（tree 类型/面板）— 最上面 -->
                        <div v-for="item in extraMenu" :key="item.key" class="menu-item"
                            :class="{ active: route.name === item.route }"
                            @click="router.push({ name: item.route, params: item.params })">
                            <span class="menu-icon"><el-icon :size="18"><component :is="item.icon" /></el-icon></span>
                            <span>{{ item.label }}</span>
                        </div>
                        <!-- 固定菜单（内置: 内容管理/配置/账号）— 分隔线 -->
                        <div v-if="extraMenu.length" class="menu-divider" />
                        <div v-for="item in mainMenu" :key="item.key" class="menu-item"
                            :class="{ active: route.name === item.route }"
                            @click="router.push({ name: item.route, params: item.params })">
                            <span class="menu-icon"><el-icon :size="18"><component :is="item.icon" /></el-icon></span>
                            <span>{{ item.label }}</span>
                        </div>
                    </div>
                </div>
            </aside>

            <div class="panel-main">
                <div class="panel-content">
                    <div class="content-card">
                        <router-view />
                    </div>
                </div>
            </div>
        </div>
    </div>
</template>
<script>
import { ref, reactive, computed, onMounted, provide } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import $api from '$api'

export default {
    setup() {
        var route = useRoute()
        var router = useRouter()
        var Panel = window.Panel
        var menuData = ref(window.AppConfig.menu) // ref: 插件面板动态 push 后 UI 刷新
        var defaultPage = window.AppConfig.defaultPage
        // 固定菜单（内置）与动态菜单（tree/面板）— 动态项由 section 标记
        var mainMenu = computed(function () { return menuData.value.filter(function (m) { return !m.section }) })
        var extraMenu = computed(function () { return menuData.value.filter(function (m) { return m.section }) })

        var phase = ref('loading')
        var user = ref(null)
        provide('user', user)

        var loginForm = reactive({ username: '', password: '', loading: false, error: '' })

        var siteName = computed(function () { return user.value?.site || '' })

        var pageTitle = computed(function () {
            var name = route.name
            var list = menuData.value
            for (var i = 0; i < list.length; i++) {
                if (list[i].route === name) return list[i].label
            }
            return name || '首页'
        })

        // 防止 401 触发登出时, 登出请求自身再 401 造成递归
        var _loggingOut = false
        function doLogout() {
            if (_loggingOut) return
            _loggingOut = true
            $api.logout().catch(function () {}).finally(function () { _loggingOut = false })
            user.value = null
            phase.value = 'login'
            router.push('/')
        }

        async function checkAuth() {
            try {
                user.value = await $api.me()
                phase.value = 'app'
                console.log('[app] checkAuth ok, phase=app, route=', route.name, route.path)
                loadPanels()   // 站点面板: 动态注册路由 + 菜单
                loadTreeMenus() // tree 类型独立菜单（view: tree）
            } catch (_) {
                console.log('[app] checkAuth failed → login')
                phase.value = 'login'
            }
        }

        // tree 类型独立菜单: 每个 view=tree 的类型一个树管理页（静态路由 /tree/:type — index.html 注册）
        async function loadTreeMenus() {
            try {
                var res = await $api.types()
                var defs = res.types || {}
                Object.keys(defs).forEach(function (t) {
                    if ((defs[t].view || '') !== 'tree') return
                    var key = 'tree-' + t
                    var exists = menuData.value.some(function (m) { return m.key === key })
                    if (exists) return // 重复执行（checkAuth + doLogin 都可能调）不重复 push
                    menuData.value.push({ key: key, label: t, section: 'tree',
                        icon: 'Share', route: 'tree', params: { type: t } })
                })
            } catch (e) { console.error('[panel] loadTreeMenus failed:', e) }
        }

        // 站点面板: /admin/panels 返回 [{path, title, vue}] — 动态 addRoute + 菜单
        async function loadPanels() {
            try {
                var res = await $api.get('/admin/panels')
                ;(res.items || []).forEach(function (p) {
                    if (!p.vue || !p.path) return
                    var name = 'panel' + p.path.replace(/[^a-zA-Z0-9]/g, '')
                    var exists = menuData.value.some(function (m) { return m.key === name })
                    if (exists) return // 去重（与 loadTreeMenus 同理）
                    router.addRoute({ name: name, path: p.path, component: Vue.defineAsyncComponent({
                        loader: function () { return Panel.loadComponent(p.vue) },
                        loadingComponent: { template: '<div style="padding:40px;text-align:center;color:#999;">加载中...</div>' },
                        delay: 100,
                    }) })
                    menuData.value.push({ key: name, label: p.title, icon: 'Grid', route: name, section: 'panel' })
                })
                // 刷新后 hash 残留动态路由页: 初次渲染时未注册导致失配 — 重新匹配
                if (route.name === undefined && route.path !== '/') {
                    router.replace(route.fullPath)
                }
            } catch (e) { console.error('[panel] loadPanels failed:', e) }
        }

        async function doLogin() {
            loginForm.loading = true
            loginForm.error = ''
            try {
                await $api.login(loginForm.username, loginForm.password)
                user.value = await $api.me()
                phase.value = 'app'
                loadPanels()   // 站点面板: 动态注册路由 + 菜单
                loadTreeMenus() // tree 类型独立菜单（view: tree）
                if (defaultPage) router.push({ name: defaultPage })
            } catch (_) {
                loginForm.error = '用户名或密码错误'
            }
            loginForm.loading = false
        }

        onMounted(function () {
            console.log('[app] mounted, 初始路由:', route.name, route.path, '| hash:', location.hash)
            Panel.onError(function (err) { if (err.status === 401) doLogout() })
            checkAuth()
        })
        // 路由变化日志（router-view 渲染谁）
        router.afterEach(function (to) {
            console.log('[app] route →', to.name, to.path)
        })

        return {
            phase: phase, user: user, siteName: siteName, pageTitle: pageTitle,
            menuData: menuData, mainMenu: mainMenu, extraMenu: extraMenu, loginForm: loginForm,
            route: route, router: router,
            doLogin: doLogin, doLogout: doLogout,
        }
    },
}
</script>
