package wgengine

import (
	"github.com/sagernet/tailscale/net/dns"
	"github.com/sagernet/tailscale/net/tstun"
	"github.com/sagernet/tailscale/wgengine/router"
	"github.com/sagernet/tailscale/wgengine/wgcfg"
)

type ExportedUserspaceEngine interface {
	SetOnReconfigListener(listener ReconfigListener)
	InputPackets(packets [][]byte) ([][]byte, error)
	SetReturnPath(returnPath tstun.ReturnPath) error
}

type ReconfigListener = func(cfg *wgcfg.Config, routerCfg *router.Config, dnsCfg *dns.Config)

func (e *userspaceEngine) SetOnReconfigListener(listener ReconfigListener) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onReconfig = listener
}

func (e *userspaceEngine) InputPackets(packets [][]byte) ([][]byte, error) {
	return e.tundev.InputPackets(packets)
}

func (e *userspaceEngine) SetReturnPath(returnPath tstun.ReturnPath) error {
	return e.tundev.SetReturnPath(returnPath)
}
