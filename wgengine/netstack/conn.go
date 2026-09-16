package netstack

import (
	"net"
	"time"

	tun "github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
)

type serverConn struct {
	*tun.GoConn
}

func (c *serverConn) LocalAddr() net.Addr {
	return c.GoConn.RemoteAddr()
}

func (c *serverConn) RemoteAddr() net.Addr {
	return c.GoConn.LocalAddr()
}

type handlerPacketConn struct {
	*tun.UDPNatConn
}

func (c *handlerPacketConn) SetTimeout(time.Duration) bool {
	return false
}

type serverPacketConn struct {
	bufio.BindPacketConn
	local M.Socksaddr
}

func (c *serverPacketConn) LocalAddr() net.Addr {
	return c.local.UDPAddr()
}
