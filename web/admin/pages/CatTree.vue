<template>
    <ul class="ctree" :class="{ root: level === 0 }">
        <li v-for="n in nodes" :key="n.id" :class="{ 'has-children': hasKids(n) }">
            <div class="ctree-row" :class="{ active: n.id === activeId }" @click="$emit('select', n)">
                <el-icon class="ctree-icon" :class="{ clickable: hasKids(n) }" :size="15"
                         @click.stop="toggle(n.id)">
                    <template v-if="hasKids(n)"><FolderOpened v-if="expanded.has(n.id)" /><Folder v-else /></template>
                    <Document v-else />
                </el-icon>
                <span class="ctree-label">{{ n.label }}</span>
                <span v-if="n.count" class="ctree-count">{{ n.count }}</span>
            </div>
            <cat-tree v-if="hasKids(n) && expanded.has(n.id)"
                :nodes="n.children" :active-id="activeId" :level="level + 1"
                @select="$emit('select', $event)" />
        </li>
    </ul>
</template>
<script>
// CatTree 递归分类树: 自绘分支连接线 (├── └──), el-tree 画不出这个形态。
// 自引用经 name 实现; 展开状态各实例自管, 默认全展开。
export default {
    name: 'CatTree',
    props: {
        nodes: { type: Array, default: () => [] },
        activeId: { type: [Number, String], default: null },
        level: { type: Number, default: 0 },
    },
    emits: ['select'],
    data() { return { expanded: new Set() } },
    created() {
        // 默认收起（点击文件夹展开 — 用户主动操作; 不再默认全展开）
    },
    methods: {
        hasKids(n) { return !!(n.children && n.children.length) },
        toggle(id) {
            const s = new Set(this.expanded)
            s.has(id) ? s.delete(id) : s.add(id)
            this.expanded = s
        },
    },
}
</script>
<style>
.ctree { list-style: none; margin: 0; padding: 0; }
.ctree li { position: relative; }
.ctree li > ul { padding-left: 23px; } /* 23 = 三角中心 13 + 横枝 10 */

/* 分支连接线: 横枝 + 纵线, 末枝纵线止于拐点。
   对齐基准: 文件夹 icon 中心 = row padding 6px + icon 半宽 7.5px ≈ 13px;
   线 x = 子 ul padding 23px + 偏移 -10px = 13px ✓ */
.ctree li::before {
    content: '';
    position: absolute;
    left: -10px;
    top: 14px;
    width: 10px;
    border-top: 1px solid #DFE1E6;
}
.ctree li::after {
    content: '';
    position: absolute;
    left: -10px;
    top: 0;
    bottom: 0;
    border-left: 1px solid #DFE1E6;
}
.ctree li:last-child::after {
    height: 14px;
    bottom: auto;
}
/* 顶层不要线 */
.ctree.root > li::before, .ctree.root > li::after { display: none; }

.ctree-row {
    display: flex;
    align-items: center;
    gap: 5px;
    height: 28px;
    padding: 0 6px;
    border-radius: 3px;
    cursor: pointer;
    color: #42526E;
    font-size: 13.5px;
    transition: background .12s ease;
}
.ctree-row:hover { background: #F4F5F7; }
.ctree-row.active {
    background: #DEEBFF;
    color: #0052CC;
    font-weight: 500;
}
.ctree-row.active .ctree-icon { color: #0052CC; }

.ctree-icon { color: #6B778C; flex-shrink: 0; }
.ctree-icon.clickable { cursor: pointer; }
.ctree-icon.clickable:hover { color: #0052CC; }
.ctree-label {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}
.ctree-count {
    font-size: 11px;
    line-height: 16px;
    color: #6B778C;
    background: #F4F5F7;
    border-radius: 8px;
    padding: 0 6px;
    flex-shrink: 0;
}
.ctree-row.active .ctree-count { background: #fff; color: #0052CC; }
</style>
