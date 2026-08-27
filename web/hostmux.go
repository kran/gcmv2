package web

import (
	"net/http"
	"strings"
)

// HostMux 多站域名分发（Site 的 Handler 按 Host 头路由）。
type HostMux struct {
	routes   map[string]*Site // host → site（不含端口）
	fallback *Site
}

// NewHostMux 建多站分发器。
func NewHostMux() *HostMux {
	return &HostMux{routes: map[string]*Site{}}
}

// Add 注册站点 — hosts 匹配 Host 头（"example.com" 或 "example.com:8080"）。
// 内部 NewSite 装配（迁移 → 引擎 → 路由）, 失败返回 error（不吞）;
// 返回的 *Site 供站点业务直接挂函数/路由。
func (m *HostMux) Add(hosts []string, spec SiteSpec) (*Site, error) {
	site, err := NewSite(spec)
	if err != nil {
		return nil, err
	}
	for _, h := range hosts {
		m.routes[normalizeHost(h)] = site
	}
	return site, nil
}

// SetFallback 兜底站点（未匹配 Host 时; 可选）。
func (m *HostMux) SetFallback(site *Site) { m.fallback = site }

// ServeHTTP 按 Host 分发。
func (m *HostMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	site := m.routes[normalizeHost(r.Host)]
	if site == nil {
		site = m.fallback
	}
	if site == nil {
		http.NotFound(w, r)
		return
	}
	site.Handler().ServeHTTP(w, r)
}

// normalizeHost 去端口 + 小写。
func normalizeHost(host string) string {
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return strings.ToLower(host)
}
