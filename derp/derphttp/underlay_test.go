// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package derphttp

import (
	"context"
	"errors"
	"net"
	"strconv"
	"testing"

	"github.com/sagernet/tailscale/net/netmon"
	"github.com/sagernet/tailscale/tailcfg"
	"github.com/sagernet/tailscale/types/key"
	"github.com/sagernet/tailscale/types/nettype"
	"github.com/sagernet/tailscale/util/eventbus"
)

var errUnderlayDial = errors.New("underlay dial refused")

// refusingUnderlay records the DERP dial and refuses it, so the test can
// tell the underlay path from a direct dial.
type refusingUnderlay struct {
	network, address string
}

func (u *refusingUnderlay) DialContext(_ context.Context, network, address string) (net.Conn, error) {
	u.network, u.address = network, address
	return nil, errUnderlayDial
}

func (u *refusingUnderlay) ListenPacket(context.Context, string, string) (nettype.PacketConn, error) {
	return nil, errors.New("unexpected ListenPacket")
}

func newRegionClientWithUnderlay(t *testing.T, underlay netmon.Underlay) (*Client, *tailcfg.DERPNode) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	node := &tailcfg.DERPNode{
		Name:     "1a",
		RegionID: 1,
		HostName: "derp.example.invalid",
		IPv4:     "127.0.0.1",
		IPv6:     "none",
		DERPPort: ln.Addr().(*net.TCPAddr).Port,
	}
	bus := eventbus.New()
	t.Cleanup(bus.Close)
	netMon, err := netmon.New(bus, t.Logf, nil, underlay)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { netMon.Close() })
	c := NewRegionClient(key.NewNode(), t.Logf, netMon, func() *tailcfg.DERPRegion {
		return &tailcfg.DERPRegion{RegionID: 1, Nodes: []*tailcfg.DERPNode{node}}
	})
	t.Cleanup(func() { c.Close() })
	return c, node
}

func TestDialNodeUsesUnderlay(t *testing.T) {
	underlay := new(refusingUnderlay)
	c, node := newRegionClientWithUnderlay(t, underlay)

	conn, err := c.dialNode(context.Background(), node)
	if conn != nil {
		conn.Close()
		t.Fatal("dialNode connected directly, want underlay error")
	}
	if !errors.Is(err, errUnderlayDial) {
		t.Fatalf("dialNode error = %v, want %v", err, errUnderlayDial)
	}
	if want := net.JoinHostPort(node.IPv4, strconv.Itoa(node.DERPPort)); underlay.network != "tcp4" || underlay.address != want {
		t.Fatalf("underlay dialed %s %s, want tcp4 %s", underlay.network, underlay.address, want)
	}
}

func TestDialNodeNilUnderlayDialsDirectly(t *testing.T) {
	c, node := newRegionClientWithUnderlay(t, nil)

	conn, err := c.dialNode(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}
