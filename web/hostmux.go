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
// New（define hooks + 建 router）→ Setup（mount 路由 — 幂等带锁）; 返回 setup 后 site。
// 站点业务（Hook/UseCtx）应在新之前/Add 前完成 — Add 即 setup, 之后不可再改配置期。
func (m *HostMux) Add(hosts []string, basedir string) *Site {
	site := New(basedir)
	site.Setup()
	for _, h := range hosts {
		m.routes[normalizeHost(h)] = site
	}
	return site
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
