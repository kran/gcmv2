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
	gquery "github.com/kran/gcmv2/query"
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

// Mount 安装 sitemap 插件: GET /sitemap.xml — 全部已发布节点（address 优先）。
// 站点绝对地址读 site.Config()["base_url"]（协议要求绝对 URL — 缺失 panic）。
// Options sitemap 插件配置（站点侧负责）。
type Options struct {
	// BaseURL 站点绝对地址（协议要求绝对 URL）。
	BaseURL string
}

// Mount 安装 sitemap 插件。
func Mount(s *web.Site, opts Options) {
	baseURL := opts.BaseURL
	if baseURL == "" {
		panic("sitemap: config base_url required")
	}
	s.Hook(web.HookBeforeMount, func(site *web.Site) error {
		site.Router().Get("/sitemap.xml", func(ctx *web.CmsCtx) {
			list := make([]core.Node, 0)
			for _, typeName := range s.Engine().Types().Names() {
				_, publicationEnabled := s.Engine().Types().Publication(typeName)
				if !publicationEnabled && !s.Exposes(typeName, web.ReadExport) {
					continue
				}
				scope, err := s.ReadScope(ctx, web.ReadExport, typeName)
				if err != nil {
					ctx.Error(http.StatusInternalServerError, "sitemap policy: "+err.Error())
					return
				}
				items, err := s.Engine().Query(ctx.R.Context(), core.ListQuery{
					Type:  typeName,
					Scope: scope,
					Sort:  []gquery.SortField{gquery.Asc(gquery.System("id"))},
					Page:  gquery.Page{Size: 10000},
				})
				if err != nil {
					ctx.Error(http.StatusInternalServerError, "sitemap: "+err.Error())
					return
				}
				list = append(list, items...)
			}
			set := urlset{
				Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9",
				URLs:  make([]urlEntry, 0, len(list)+1),
			}
			for i := range list {
				n := &list[i]
				loc := baseURL + "/node/"
				address := s.Engine().Types().Address(n.Type, n.Fields)
				if address != "" {
					loc += address
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
		return nil
	})
}
