package netstack

import tun "github.com/sagernet/sing-tun"

func (ns *Impl) ExportIPStack() *tun.Go {
	return ns.ipstack
}
