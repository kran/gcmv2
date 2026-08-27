// Package highlight 搜索高亮模板函数插件。
//
// 安装后模板可用:
//
//	{{ .Display | highlight $.Query }}        // 标题高亮
//	{{ $n | excerpt 120 | highlight $.Query }} // 摘要高亮
//
// 大小写不敏感; 多关键词（空格分隔）逐个高亮; 输出已 HTML 转义（防 XSS —
// 文本来自节点数据, 关键词来自用户输入 — 先转义再包 <mark>）。
package highlight

import (
	"html"
	"html/template"
	"regexp"
	"strings"

	"github.com/kran/gcmv2/web"
)

// Highlight 高亮核心（纯函数 — 可单测）: 文本中出现的每个关键词包 <mark>。
// 文本先 HTML 转义（防 XSS — 节点数据/用户输入都不可信）再匹配。
func Highlight(text, q string) template.HTML {
	escaped := html.EscapeString(text)
	if q == "" {
		return template.HTML(escaped)
	}
	for _, term := range strings.Fields(q) {
		et := html.EscapeString(term)
		if et == "" {
			continue
		}
		re, err := regexp.Compile(`(?i)` + regexp.QuoteMeta(et))
		if err != nil {
			continue
		}
		escaped = re.ReplaceAllStringFunc(escaped, func(m string) string {
			return "<mark>" + m + "</mark>"
		})
	}
	return template.HTML(escaped)
}

// Mount 安装 highlight 模板函数。
// 管道用法 {{ .Display | highlight $.Query }} = highlight($.Query, .Display) —
// Go 模板管道把前置值追加为最后一个参数, 包装层调换顺序。
func Mount(s *web.Site) {
	s.Func("highlight", func(q, text string) template.HTML { return Highlight(text, q) })
}
