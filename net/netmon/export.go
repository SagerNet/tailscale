package netmon

import (
	"context"
	"net"

	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/tailscale/types/nettype"
)

// Underlay opens the sockets that carry Tailscale's own traffic to the
// network: DERP connections and the magicsock UDP sockets used for peer,
// disco and STUN traffic. A nil Underlay keeps the direct netns sockets.
type Underlay interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
	ListenPacket(ctx context.Context, network, address string) (nettype.PacketConn, error)
}

func (m *Monitor) Dialer() N.Dialer {
	return m.dialer
}

func (m *Monitor) Underlay() Underlay {
	return m.underlay
}
