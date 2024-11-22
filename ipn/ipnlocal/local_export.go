package ipnlocal

import (
	"sync/atomic"

	"github.com/sagernet/tailscale/net/dns"
	"github.com/sagernet/tailscale/wgengine/filter"
	"github.com/sagernet/tailscale/wgengine/router"
	"github.com/sagernet/tailscale/wgengine/wgcfg"
)

func (b *LocalBackend) ExportFilter() *atomic.Pointer[filter.Filter] {
	return &b.filterAtomic
}

func (b *LocalBackend) ExportConfig() (*wgcfg.Config, *dns.Config, *router.Config) {
	return b.cfg, b.dcfg, b.rcfg
}
