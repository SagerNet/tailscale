// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package magicsock

import (
	"errors"
	"net"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sagernet/tailscale/feature/buildfeatures"
	"github.com/sagernet/tailscale/types/nettype"
	"github.com/sagernet/wireguard-go/conn"
)

func TestBindRetryIPv4FailurePreservesHealthyIPv6(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := new(retryTestListener)
		l.fail4.Store(true)
		c := newRetryTestConn(t, l)
		defer c.Close()
		l.mu.Lock()
		v6 := l.conns[0]
		l.mu.Unlock()
		synctest.Wait()
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		if v6.wasClosed.Load() {
			t.Fatal("an IPv4-only recovery attempt closed the healthy IPv6 socket")
		}
	})
}

func TestBindRetryBindCloseStopsRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := new(retryTestListener)
		c := newRetryTestConn(t, l)
		defer c.Close()
		if _, _, err := c.bind.Open(0); err != nil {
			t.Fatal(err)
		}
		l.fail4.Store(true)
		c.Rebind()
		synctest.Wait()
		if err := c.bind.Close(); err != nil {
			t.Fatal(err)
		}
		before := len(l.attempts())
		// An ordinary rebind must also respect the closed Bind, otherwise
		// endpoint discovery could bypass the recovery worker's pause.
		if err := c.rebind(keepCurrentPort); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("rebind while Bind is closed: %v", err)
		}
		l.fail4.Store(false)
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		if after := len(l.attempts()); after != before {
			t.Fatalf("closed connBind was rebound: IPv4 attempts increased from %d to %d", before, after)
		}
	})
}

// A receiver may be descheduled after Close wakes its underlying read but
// before RebindingUDPConn compares the old and current socket pointers.
type retryGatedPacketConn struct {
	nettype.PacketConn
	entered chan struct{}
	release chan struct{}
}

func (p *retryGatedPacketConn) ReadFromUDPAddrPort(b []byte) (int, netip.AddrPort, error) {
	close(p.entered)
	n, ap, err := p.PacketConn.ReadFromUDPAddrPort(b)
	<-p.release
	return n, ap, err
}

func (p *retryGatedPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, ap, err := p.ReadFromUDPAddrPort(b)
	return n, net.UDPAddrFromAddrPort(ap), err
}

func TestBindRetryBindCloseUnblocksReceive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := new(retryTestListener)
		l.fail4.Store(true)
		c := newRetryTestConn(t, l)
		// Gate the actual failed IPv4 placeholder. Recovery would replace
		// this socket if it were allowed to continue after Bind Close.
		gate := &retryGatedPacketConn{
			PacketConn: c.pconn4.currentConn(),
			entered:    make(chan struct{}),
			release:    make(chan struct{}),
		}
		c.pconn4.mu.Lock()
		c.pconn4.setConnLocked(gate, "", c.bind.BatchSize(), c.controlKnobs)
		c.pconn4.mu.Unlock()
		fns, _, err := c.bind.Open(0)
		if err != nil {
			t.Fatal(err)
		}
		readDone := make(chan struct{})
		receiveErr := make(chan error, 1)
		go func() {
			// Use the same ReceiveFunc supplied to wireguard-go, including
			// magicsock's batch adapter and RebindingUDPConn switch-over.
			_, err := fns[0]([][]byte{make([]byte, 128)}, make([]int, 1), make([]conn.Endpoint, 1))
			receiveErr <- err
			close(readDone)
		}()
		<-gate.entered
		defer func() {
			select {
			case <-gate.release:
			default:
				close(gate.release)
			}
			c.Close()
			<-readDone
		}()
		if err := c.bind.Close(); err != nil {
			t.Fatal(err)
		}
		l.fail4.Store(false)
		time.Sleep(1100 * time.Millisecond)
		synctest.Wait()
		close(gate.release)
		synctest.Wait()
		select {
		case <-readDone:
			if err := <-receiveErr; !errors.Is(err, net.ErrClosed) {
				t.Errorf("closed receiver returned %v; want net.ErrClosed", err)
			}
		default:
			t.Error("connBind.Close receiver followed a socket reopened by recovery and remained blocked")
		}
	})
}

func TestBindRetryBindReopenRestoresSockets(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		name := "healthy"
		if unavailable {
			name = "transient-bind-failure"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				l := new(retryTestListener)
				c := newRetryTestConn(t, l)
				defer c.Close()
				if _, _, err := c.bind.Open(0); err != nil {
					t.Fatal(err)
				}
				if err := c.bind.Close(); err != nil {
					t.Fatal(err)
				}
				l.fail4.Store(unavailable)
				l.fail6.Store(unavailable)
				// Open must not acquire Conn.mu: WireGuard can call it while
				// holding locks later in the engine's lock order.
				c.mu.Lock()
				_, port, err := c.bind.Open(0)
				c.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				if unavailable && port != 0 || !unavailable && port != 41641 {
					t.Fatalf("reopened port = %d, unavailable = %v", port, unavailable)
				}
				synctest.Wait()
				l.fail4.Store(false)
				l.fail6.Store(false)
				time.Sleep(1100 * time.Millisecond)
				synctest.Wait()
				if c.pconn4.Port() != 41641 || c.pconn6.Port() != 41641 {
					t.Fatal("reopened Bind did not restore both UDP sockets")
				}
				before := len(l.attempts())
				time.Sleep(time.Minute)
				synctest.Wait()
				if len(l.attempts()) != before {
					t.Fatal("reopened healthy Bind kept retrying")
				}
			})
		})
	}
}

func TestBindRetryPartialIPv6Recovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := new(retryTestListener)
		l.fail4.Store(true)
		l.fail6.Store(true)
		c := newRetryTestConn(t, l)
		defer c.Close()
		synctest.Wait()
		before := metricReSTUNCalls.Value()
		l.fail6.Store(false)
		time.Sleep(1100 * time.Millisecond)
		synctest.Wait()
		if c.pconn6.Port() != 41641 || c.pconn4.Port() != 0 {
			t.Fatal("IPv6 did not recover independently of IPv4")
		}
		if buildfeatures.HasClientMetrics && metricReSTUNCalls.Value() <= before {
			t.Fatal("partial recovery did not request endpoint discovery")
		}
		v6 := c.pconn6.currentConn()
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		if c.pconn6.currentConn() != v6 {
			t.Fatal("continued IPv4 failures replaced the recovered IPv6 socket")
		}
		l.fail4.Store(false)
		time.Sleep(31 * time.Second)
		synctest.Wait()
		if c.pconn4.Port() != 41641 || c.pconn6.currentConn() != v6 {
			t.Fatal("IPv4 did not recover while preserving IPv6")
		}
	})
}

func TestBindRetryNewFailureResetsBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := new(retryTestListener)
		l.fail4.Store(true)
		c := newRetryTestConn(t, l)
		defer c.Close()
		time.Sleep(32 * time.Second)
		synctest.Wait()
		// Coalesce the success and new failure before the worker observes
		// either. The new episode must not inherit the old 30-second delay.
		l.fail4.Store(false)
		c.Rebind()
		l.fail4.Store(true)
		c.Rebind()
		l.fail4.Store(false)
		time.Sleep(1100 * time.Millisecond)
		synctest.Wait()
		if c.LocalPort() != 41641 {
			t.Fatal("new bind failure retained the previous episode's backoff")
		}
	})
}
