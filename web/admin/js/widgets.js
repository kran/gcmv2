/* 组件解析：字段的 kind 名 → web/admin/widgets/<kind 名>.vue。
 *
 * kind 名**就是**界面标识（同一个名字用来取组件），不需要任何映射表：
 * 前端不列清单、不认识具体 kind，只按名字取文件 —— 站点自定义 kind
 * 只要有个同名组件就能显示，框架侧零改动。
 * 名字不能当文件名的情况在 Go 侧就被 RegisterKind 拒掉了。
 *
 * 取不到组件时渲染一个**明确的**错误块（不是空、不是原始值）：
 * 契约被破坏要当场看见。
 */
window.Widgets = (function () {
    var cache = {}

    // 没有 widgets/<kind>.vue 时的替身：把"缺哪个文件"直接写在页面上
    function missing(kind) {
        return {
            name: 'WidgetMissing',
            template: '<div class="w-missing">⚠ 没有 web/admin/widgets/' + kind +
                '.vue（kind &quot;' + kind + '&quot;）—— 加一个同名组件，或改这个 kind 名</div>',
        }
    }

    function component(kind) {
        if (!cache[kind]) {
            cache[kind] = Vue.defineAsyncComponent({
                loader: function () { return window.Panel.loadComponent('widgets/' + kind + '.vue') },
                errorComponent: missing(kind),
            })
        }
        return cache[kind]
    }

    // kind 名 → 组件（空名返回 null，调用方自己给出错误）
    function resolve(kind) { return kind ? component(kind) : null }

    // ── 显示用的纯函数（列表单元和编辑器共用，避免各写一遍）──

    // 截断（列表里一行放下）
    function truncate(value, max) {
        if (value === undefined || value === null) return ''
        var text = String(value)
        var limit = max || 60
        return text.length > limit ? text.slice(0, limit) + '…' : text
    }

    // 富文本 → 纯文本（去标签、压空白）
    function plain(html) {
        if (!html) return ''
        var div = document.createElement('div')
        div.innerHTML = String(html)
        return (div.textContent || '').replace(/\s+/g, ' ').trim()
    }

    // 时间：库/接口里的统一格式是 UTC（…Z），显示按当前设备本地时间
    function localTime(value, withTime) {
        if (!value) return ''
        var date = new Date(value)
        if (isNaN(date.getTime())) return String(value)
        var ymd = date.getFullYear() + '-' + pad(date.getMonth() + 1) + '-' + pad(date.getDate())
        return withTime === false ? ymd : ymd + ' ' + pad(date.getHours()) + ':' + pad(date.getMinutes())
    }

    // 只取日期（列表里往往只关心哪天）
    function localDate(value) { return localTime(value, false) }

    function pad(n) { return String(n).padStart(2, '0') }

    function fileName(path) {
        if (!path) return ''
        return String(path).split('/').pop()
    }

    function isImage(path) {
        return /\.(png|jpe?g|gif|webp|avif|bmp|svg)$/i.test(String(path || ''))
    }

    // refOption 引用目标的选项标签 —— 引用选择器/回显/列表单元格都走这里:
    // $api.refLabel 决定显示名, 后面永远缀 #id（同名节点靠它区分; 少了一个都认不出来）。
    function refOption(node, defs) {
        if (!node) return null
        return {
            id: node.id,
            type: node.type,
            label: window.$api.refLabel(node, (defs || {})[node.type] || null) + ' #' + node.id,
        }
    }

    return {
        resolve: resolve,
        refOption: refOption,
        truncate: truncate, plain: plain, localTime: localTime, localDate: localDate,
        fileName: fileName, isImage: isImage,
    }
})()
