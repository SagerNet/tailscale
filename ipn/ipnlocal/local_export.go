package ipnlocal

import (
	"sync/atomic"

	"github.com/sagernet/tailscale/wgengine"
	"github.com/sagernet/tailscale/wgengine/filter"
)

func (b *LocalBackend) ExportFilter() *atomic.Pointer[filter.Filter] {
	return &b.currentNode().filterAtomic
}

func (b *LocalBackend) ExportEngine() wgengine.Engine {
	return b.e
}

func (b *LocalBackend) SetExternalSSHHostKeys(keys []string) {
	b.mu.Lock()
	b.externalSSHHostKeys = keys
	if b.hostinfo != nil {
		b.hostinfo.SSH_HostKeys = keys
	}
	b.mu.Unlock()
	b.doSetHostinfoFilterServices()
}
