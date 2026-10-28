// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package magicsock

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sagernet/tailscale/net/netmon"
	"github.com/sagernet/tailscale/types/nettype"
	"github.com/sagernet/tailscale/util/eventbus"
	"github.com/sagernet/tailscale/util/usermetric"
)

// This listener models the sing-box hook returning ErrNoRoute until its
// default-interface monitor has caught up. No actual network is used.
type retryTestListener struct {
	fail4  atomic.Bool
	fail6  atomic.Bool
	mu     sync.Mutex
	times  []time.Time
	conns  []*retryTestPacketConn
	block4 <-chan struct{}
}

func (l *retryTestListener) ListenPacket(_ context.Context, network, address string) (net.PacketConn, error) {
	l.mu.Lock()
	if network == "udp4" {
		l.times = append(l.times, time.Now())
	}
	block := l.block4
	l.mu.Unlock()
	if network == "udp4" && block != nil {
		<-block
	}
	if network == "udp4" && l.fail4.Load() || network == "udp6" && l.fail6.Load() {
		return nil, errors.New("no route to internet")
	}
	ip := net.IPv4(127, 0, 0, 1)
	if network == "udp6" {
		ip = net.IPv6loopback
	}
	p := &retryTestPacketConn{
		blockForeverConn: newBlockForeverConn(),
		addr:             &net.UDPAddr{IP: ip, Port: 41641},
	}
	l.mu.Lock()
	l.conns = append(l.conns, p)
	l.mu.Unlock()
	return p, nil
}

func (l *retryTestListener) attempts() []time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.times)
}

type retryTestPacketConn struct {
	*blockForeverConn
	addr      *net.UDPAddr
	wasClosed atomic.Bool
}

func (p *retryTestPacketConn) LocalAddr() net.Addr { return p.addr }
func (p *retryTestPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, a, err := p.ReadFromUDPAddrPort(b)
	return n, net.UDPAddrFromAddrPort(a), err
}
func (p *retryTestPacketConn) WriteTo(b []byte, a net.Addr) (int, error) {
	return p.WriteToUDPAddrPort(b, a.(*net.UDPAddr).AddrPort())
}
func (p *retryTestPacketConn) Close() error {
	p.wasClosed.Store(true)
	return p.blockForeverConn.Close()
}

func newRetryTestConn(t *testing.T, l nettype.PacketListener) *Conn {
	t.Helper()
	bus := eventbus.New()
	t.Cleanup(bus.Close)
	c, err := NewConn(Options{
		Logf:                   t.Logf,
		EventBus:               bus,
		NetMon:                 netmon.NewStatic(),
		Metrics:                new(usermetric.Registry),
		Port:                   41641,
		DisablePortMapper:      true,
		TestOnlyPacketListener: l,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Keep ReSTUN on its stopped-client path: tests exercise socket recovery,
	// not external STUN servers or peers.
	c.mu.Lock()
	c.everHadKey = true
	c.mu.Unlock()
	return c
}

type retryRealListener struct{ unavailable atomic.Bool }

func (l *retryRealListener) ListenPacket(_ context.Context, network, _ string) (net.PacketConn, error) {
	if network != "udp4" || l.unavailable.Load() {
		return nil, errors.New("no route to internet")
	}
	return net.ListenPacket("udp4", "127.0.0.1:0")
}

func TestBindRetryRestoresRealUDP(t *testing.T) {
	l := new(retryRealListener)
	l.unavailable.Store(true)
	c := newRetryTestConn(t, l)
	defer c.Close()
	l.unavailable.Store(false)
	deadline := time.Now().Add(5 * time.Second)
	for c.LocalPort() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if c.LocalPort() == 0 {
		t.Fatal("real UDP listener did not recover")
	}
	receiver, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	if err := receiver.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.pconn4.WriteToUDPAddrPort([]byte("recovered"), receiver.LocalAddr().(*net.UDPAddr).AddrPort()); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 32)
	n, _, err := receiver.ReadFromUDP(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(b[:n]) != "recovered" {
		t.Fatalf("received %q", b[:n])
	}
}

func TestBindRetryRecoversWithoutNetworkEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := new(retryTestListener)
		l.fail4.Store(true)
		c := newRetryTestConn(t, l)
		defer c.Close()
		synctest.Wait()
		if c.LocalPort() != 0 {
			t.Fatal("failed bind must leave the dummy connection")
		}
		// First retry also fails; then the interface becomes usable without
		// any major/minor notification whatsoever.
		time.Sleep(1100 * time.Millisecond)
		l.fail4.Store(false)
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if c.LocalPort() != 41641 {
			t.Fatal("UDP socket was not restored")
		}
		before := len(l.attempts())
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		if len(l.attempts()) != before {
			t.Fatal("successful recovery kept rebinding")
		}
	})
}

func TestBindRetryAfterRunningRebindFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := new(retryTestListener)
		c := newRetryTestConn(t, l)
		defer c.Close()
		l.fail4.Store(true)
		c.Rebind()
		if c.LocalPort() != 0 {
			t.Fatal("expected failed runtime rebind")
		}
		// Match the incident: a minor event only runs ReSTUN and the default
		// interface becomes available just after the unsuccessful rebind.
		c.ReSTUN("link-change-minor")
		l.fail4.Store(false)
		time.Sleep(1100 * time.Millisecond)
		synctest.Wait()
		if c.LocalPort() != 41641 {
			t.Fatal("minor-only recovery did not rebind")
		}
	})
}

func TestBindRetryBackoffIsCapped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := new(retryTestListener)
		l.fail4.Store(true)
		c := newRetryTestConn(t, l)
		defer c.Close()
		start := time.Now()
		time.Sleep(95 * time.Second)
		synctest.Wait()
		var offsets []time.Duration
		for _, at := range l.attempts() {
			d := at.Sub(start)
			if len(offsets) == 0 || offsets[len(offsets)-1] != d {
				offsets = append(offsets, d)
			}
		}
		want := []time.Duration{0, time.Second, 3 * time.Second, 7 * time.Second, 15 * time.Second, 31 * time.Second, 61 * time.Second, 91 * time.Second}
		if !slices.Equal(offsets, want) {
			t.Fatalf("attempt offsets = %v; want %v", offsets, want)
		}
	})
}

func TestBindRetryNormalRebindCancelsPendingRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := new(retryTestListener)
		l.fail4.Store(true)
		c := newRetryTestConn(t, l)
		defer c.Close()
		synctest.Wait()
		l.fail4.Store(false)
		c.Rebind()
		synctest.Wait()
		before := len(l.attempts())
		time.Sleep(time.Minute)
		synctest.Wait()
		if len(l.attempts()) != before {
			t.Fatal("stale retry closed a healthy socket")
		}
	})
}

func TestBindRetryCloseStopsRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := new(retryTestListener)
		l.fail4.Store(true)
		c := newRetryTestConn(t, l)
		synctest.Wait()
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		before := len(l.attempts())
		l.fail4.Store(false)
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		if len(l.attempts()) != before {
			t.Fatal("recovery recreated a socket after Close")
		}
		if err := c.rebind(keepCurrentPort); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("rebind after close: %v", err)
		}
	})
}

func TestBindRetryIPv6FailureDoesNotRebindHealthyIPv4(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := new(retryTestListener)
		l.fail6.Store(true)
		c := newRetryTestConn(t, l)
		defer c.Close()
		before := len(l.attempts())
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		if c.LocalPort() != 41641 || len(l.attempts()) != before {
			t.Fatal("IPv6 failure disturbed healthy IPv4")
		}
	})
}

func TestBindRetryHonorsTCPOnlyMode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := new(retryTestListener)
		l.fail4.Store(true)
		c := newRetryTestConn(t, l)
		defer c.Close()
		c.onlyTCP443.Store(true)
		before := len(l.attempts())
		time.Sleep(time.Minute)
		synctest.Wait()
		if len(l.attempts()) != before {
			t.Fatal("recovery bound UDP in TCP-only mode")
		}
		l.fail4.Store(false)
		c.onlyTCP443.Store(false)
		time.Sleep(31 * time.Second)
		synctest.Wait()
		if c.LocalPort() != 41641 {
			t.Fatal("recovery did not resume after TCP-only mode ended")
		}
	})
}

func TestBindRetryCloseDuringBind(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := new(retryTestListener)
		l.fail4.Store(true)
		c := newRetryTestConn(t, l)
		synctest.Wait()
		release := make(chan struct{})
		l.mu.Lock()
		l.block4 = release
		l.mu.Unlock()
		l.fail4.Store(false)
		time.Sleep(1100 * time.Millisecond)
		synctest.Wait()
		closed := make(chan struct{})
		go func() { c.Close(); close(closed) }()
		// Mutex waits are not considered durably blocked by synctest.
		// Wait for Close's atomic marker, then release the held bind.
		for !c.closing.Load() {
			runtime.Gosched()
		}
		close(release)
		<-closed
		l.mu.Lock()
		defer l.mu.Unlock()
		for _, p := range l.conns {
			if !p.wasClosed.Load() {
				t.Fatal("socket leaked while closing during recovery")
			}
		}
	})
}

// Keep the PacketConn's netip method set explicit for the listener adapter.
var _ interface {
	WriteToUDPAddrPort([]byte, netip.AddrPort) (int, error)
} = (*retryTestPacketConn)(nil)
