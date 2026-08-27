// Package sitemap sitemap.xml 插件（第一个插件 — 定义插件约定）。
//
// 插件约定: 独立包 + Mount(s *web.Site) 安装函数 — 内部只用 site 公开
// API 组装（路由/模板函数/hook）— 无插件框架, 站点按需安装:
//
//	sitemap.Mount(site, "https://viicn.org.cn")   // 安装后 GET /sitemap.xml
package sitemap

import (
	"encoding/xml"
	"net/http"
	"sort"
	"strconv"

	"github.com/kran/gcmv2/core"
	"github.com/kran/gcmv2/web"
)

// urlset sitemap.xml 根（sitemaps.org 协议）。
type urlset struct {
	XMLName xml.Name   `xml:"urlset"`
	Xmlns   string     `xml:"xmlns,attr"`
	URLs    []urlEntry `xml:"url"`
}

type urlEntry struct {
	Loc      string `xml:"loc"`
	LastMod  string `xml:"lastmod,omitempty"`
	Priority string `xml:"priority,omitempty"`
}

// Mount 安装 sitemap 插件: GET /sitemap.xml — 全部已发布节点（slug 优先）。
// 站点绝对地址读 site.Config()["base_url"]（协议要求绝对 URL — 缺失 panic）。
func Mount(s *web.Site) {
	baseURL, _ := s.Config()["base_url"].(string)
	if baseURL == "" {
		panic("sitemap: config base_url required")
	}
	s.Get("/sitemap.xml", func(ctx *web.CmsCtx) {
		list, _, err := s.Engine().QueryPage(core.ListQuery{
			Filter: `(= status 1)`, Page: 1, Size: 10000, Sort: `id ASC`,
		})
		if err != nil {
			ctx.Error(http.StatusInternalServerError, "sitemap: "+err.Error())
			return
		}
		set := urlset{Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9"}
		set.URLs = make([]urlEntry, 0, len(list)+1)
		for i := range list {
			n := &list[i]
			loc := baseURL + "/node/"
			if n.Slug != "" {
				loc += n.Slug
			} else {
				loc += strconv.FormatInt(n.ID, 10)
			}
			u := urlEntry{Loc: loc}
			if !n.UpdatedAt.IsZero() {
				u.LastMod = n.UpdatedAt.Format("2006-01-02")
			}
			set.URLs = append(set.URLs, u)
		}
		// 首页
		set.URLs = append(set.URLs, urlEntry{Loc: baseURL + "/", Priority: "1.0"})
		sort.Slice(set.URLs, func(i, j int) bool { return set.URLs[i].Loc < set.URLs[j].Loc })

		out, err := xml.MarshalIndent(set, "", "  ")
		if err != nil {
			ctx.Error(http.StatusInternalServerError, "sitemap: "+err.Error())
			return
		}
		ctx.SetHeader("Content-Type", "application/xml; charset=utf-8")
		ctx.W.WriteHeader(http.StatusOK)
		_, _ = ctx.W.Write([]byte(xml.Header))
		_, _ = ctx.W.Write(out)
	})
}
