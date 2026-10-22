package netmon

import (
	"context"
	"syscall"

	"github.com/sagernet/tailscale/types/nettype"

	N "github.com/sagernet/sing/common/network"
)

// Hooks are the sing-box integration points consulted by the monitor,
// netns and magicsock on behalf of this monitor's owner.
type Hooks struct {
	// Dialer, if non-nil, replaces the platform dialer for TCP
	// connections that are not forced direct.
	Dialer N.Dialer

	// Control, if non-nil, replaces the platform-specific socket
	// control (SO_MARK, SO_BINDTODEVICE, ...) for listeners and dialers.
	Control func(network, address string, conn syscall.RawConn) error

	// ListenPacket, if non-nil, creates the UDP sockets of magicsock and
	// the peer relay server.
	ListenPacket func(ctx context.Context, network, address string) (nettype.PacketConn, error)

	// DefaultRoute, if non-nil, replaces the platform default route
	// lookup for this monitor's interface state.
	DefaultRoute func() (DefaultRouteDetails, error)
}

func (m *Monitor) Hooks() Hooks {
	return m.hooks
}

func (m *Monitor) Dialer() N.Dialer {
	return m.hooks.Dialer
}

func (m *Monitor) ControlFunc() func(network, address string, conn syscall.RawConn) error {
	return m.hooks.Control
}

func (m *Monitor) ListenPacketFunc() func(ctx context.Context, network, address string) (nettype.PacketConn, error) {
	return m.hooks.ListenPacket
}
