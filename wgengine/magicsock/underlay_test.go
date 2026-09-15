// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package magicsock

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/sagernet/tailscale/net/netmon"
	"github.com/sagernet/tailscale/types/nettype"
	"github.com/sagernet/tailscale/util/eventbus"
	"github.com/sagernet/tailscale/util/usermetric"
)

// loopbackUnderlay records ListenPacket calls and binds them to loopback,
// which is distinguishable from the unspecified address the direct path uses.
type loopbackUnderlay struct {
	mu    sync.Mutex
	calls []string
}

func (u *loopbackUnderlay) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("unexpected DialContext")
}

func (u *loopbackUnderlay) ListenPacket(ctx context.Context, network, address string) (nettype.PacketConn, error) {
	u.mu.Lock()
	u.calls = append(u.calls, network+" "+address)
	u.mu.Unlock()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	host := "127.0.0.1"
	if network == "udp6" {
		host = "::1"
	}
	var lc net.ListenConfig
	pc, err := lc.ListenPacket(ctx, network, net.JoinHostPort(host, port))
	if err != nil {
		return nil, err
	}
	return pc.(*net.UDPConn), nil
}

func (u *loopbackUnderlay) snapshot() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.calls)
}

func newUnderlayConn(t *testing.T, underlay netmon.Underlay) *Conn {
	t.Helper()
	bus := eventbus.New()
	t.Cleanup(bus.Close)
	netMon, err := netmon.New(bus, t.Logf, nil, underlay)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { netMon.Close() })
	c, err := NewConn(Options{
		Logf:              t.Logf,
		NetMon:            netMon,
		EventBus:          bus,
		Metrics:           new(usermetric.Registry),
		DisablePortMapper: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func localAddr(t *testing.T, c *Conn) netip.AddrPort {
	t.Helper()
	ap, err := netip.ParseAddrPort(c.pconn4.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	return ap
}

func TestListenPacketUnderlay(t *testing.T) {
	underlay := new(loopbackUnderlay)
	c := newUnderlayConn(t, underlay)

	if got := localAddr(t, c); !got.Addr().IsLoopback() {
		t.Fatalf("IPv4 socket bound to %v, want loopback from underlay", got)
	}
	if got, want := underlay.snapshot(), []string{"udp6 :0", "udp4 :0"}; !slices.Equal(got, want) {
		t.Fatalf("underlay calls = %q, want %q", got, want)
	}
}

func TestRebindKeepsPortThroughUnderlay(t *testing.T) {
	underlay := new(loopbackUnderlay)
	c := newUnderlayConn(t, underlay)
	before := localAddr(t, c)

	c.Rebind()

	after := localAddr(t, c)
	if after.Port() != before.Port() {
		t.Fatalf("port changed across rebind: %v -> %v", before, after)
	}
	want := "udp4 :" + strconv.Itoa(int(before.Port()))
	if got := underlay.snapshot(); !slices.Contains(got, want) {
		t.Fatalf("underlay calls = %q, want %q for rebind", got, want)
	}
}

func TestUnderlayIsPerConn(t *testing.T) {
	a, b := new(loopbackUnderlay), new(loopbackUnderlay)
	ca := newUnderlayConn(t, a)
	cb := newUnderlayConn(t, b)
	beforeA, beforeB := len(a.snapshot()), len(b.snapshot())

	ca.Rebind()

	if got := len(a.snapshot()); got == beforeA {
		t.Fatal("rebind of first conn did not reach its underlay")
	}
	if got := len(b.snapshot()); got != beforeB {
		t.Fatalf("rebind of first conn reached second underlay: %d calls, want %d", got, beforeB)
	}
	if localAddr(t, cb).Port() == localAddr(t, ca).Port() {
		t.Fatal("conns share a port")
	}
}

func TestNilUnderlayBindsDirectly(t *testing.T) {
	c := newUnderlayConn(t, nil)
	if got := localAddr(t, c); !got.Addr().IsUnspecified() {
		t.Fatalf("IPv4 socket bound to %v, want unspecified direct bind", got)
	}
}
