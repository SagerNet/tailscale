// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package netstack

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	tun "github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	"github.com/sagernet/sing/common/canceler"
	E "github.com/sagernet/sing/common/exceptions"
	F "github.com/sagernet/sing/common/format"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/tailscale/envknob"
	"github.com/sagernet/tailscale/feature/buildfeatures"
	"github.com/sagernet/tailscale/ipn/ipnlocal"
	"github.com/sagernet/tailscale/net/dns"
	"github.com/sagernet/tailscale/net/ipset"
	"github.com/sagernet/tailscale/net/netx"
	"github.com/sagernet/tailscale/net/packet"
	"github.com/sagernet/tailscale/net/tsaddr"
	"github.com/sagernet/tailscale/net/tstun"
	"github.com/sagernet/tailscale/proxymap"
	"github.com/sagernet/tailscale/syncs"
	"github.com/sagernet/tailscale/tailcfg"
	"github.com/sagernet/tailscale/types/ipproto"
	"github.com/sagernet/tailscale/types/logger"
	"github.com/sagernet/tailscale/types/netmap"
	"github.com/sagernet/tailscale/types/nettype"
	"github.com/sagernet/tailscale/types/views"
	"github.com/sagernet/tailscale/util/set"
	"github.com/sagernet/tailscale/version"
	"github.com/sagernet/tailscale/wgengine/filter"
)

const debugPackets = false

var (
	debugNetstack             = envknob.RegisterBool("TS_DEBUG_NETSTACK")
	netstackKeepaliveIdle     = envknob.RegisterDuration("TS_NETSTACK_KEEPALIVE_IDLE")
	netstackKeepaliveInterval = envknob.RegisterDuration("TS_NETSTACK_KEEPALIVE_INTERVAL")
	serviceIP                 = tsaddr.TailscaleServiceIP()
	serviceIPv6               = tsaddr.TailscaleServiceIPv6()
	viaRange                  = tsaddr.TailscaleViaRange()
	ipv4Loopback              = netip.MustParseAddr("127.0.0.1")
	ipv6Loopback              = netip.IPv6Loopback()
)

type Impl struct {
	GetTCPHandlerForFlow         func(src, dst netip.AddrPort) (func(net.Conn), bool)
	GetUDPHandlerForFlow         func(src, dst netip.AddrPort) (func(nettype.ConnPacketConn), bool)
	HasListenerForDestination    func(protocol uint8, destination netip.AddrPort) bool
	Handler                      tun.Handler
	CheckLocalTransportEndpoints bool
	ProcessLocalIPs              bool
	ProcessSubnets               bool

	ipstack                  *tun.Go
	device                   *tun.MemoryTun
	tundev                   *tstun.Wrapper
	pm                       *proxymap.Mapper
	logf                     logger.Logf
	ctx                      context.Context
	ctxCancel                context.CancelFunc
	lb                       *ipnlocal.LocalBackend
	dns                      *dns.Manager
	ready                    atomic.Bool
	loopbackPort             *int
	peerapiPort4Atomic       atomic.Uint32
	peerapiPort6Atomic       atomic.Uint32
	atomicIsLocalIPFunc      syncs.AtomicValue[func(netip.Addr) bool]
	atomicIsVIPServiceIPFunc syncs.AtomicValue[func(netip.Addr) bool]
	atomicIPVIPServiceMap    syncs.AtomicValue[netmap.IPServiceMappings]
	atomicActiveVIPServices  syncs.AtomicValue[set.Set[tailcfg.ServiceName]]
	localAddresses           syncs.AtomicValue[[]netip.Addr]
	forwardDialFunc          netx.DialFunc
	access                   sync.Mutex
	inFlight                 int
	inFlightByClient         map[netip.Addr]int
}

func Create(logf logger.Logf, tundev *tstun.Wrapper, dnsManager *dns.Manager, proxyMapper *proxymap.Mapper, memoryPressure func() tun.MemoryPressure) (*Impl, error) {
	if logf == nil || tundev == nil || dnsManager == nil || proxyMapper == nil {
		return nil, E.New("netstack: missing subsystem")
	}
	ns := &Impl{
		logf: logf, tundev: tundev, pm: proxyMapper, dns: dnsManager,
		inFlightByClient: make(map[netip.Addr]int),
	}
	ns.ctx, ns.ctxCancel = context.WithCancel(context.Background())
	ns.atomicIsLocalIPFunc.Store(ipset.FalseContainsIPFunc())
	ns.atomicIsVIPServiceIPFunc.Store(ipset.FalseContainsIPFunc())
	loopbackPort, found := envknob.LookupInt("TS_DEBUG_NETSTACK_LOOPBACK_PORT")
	if found && loopbackPort >= 0 && loopbackPort <= math.MaxUint16 {
		ns.loopbackPort = &loopbackPort
	}
	mtu := int(tstun.DefaultTUNMTU())
	ns.device = tun.NewMemoryTun(tun.MemoryTunOptions{
		MTU: mtu, Headroom: tstun.PacketStartOffset, Outbound: ns.inject,
	})
	var err error
	ns.ipstack, err = tun.NewGo(tun.StackOptions{
		Context: ns.ctx, Tun: ns.device, TunOptions: tun.Options{MTU: uint32(mtu)},
		Handler: ns, Logger: stackLogger{logf}, UDPTimeout: 2 * time.Minute, ICMPTimeout: time.Minute,
		UDPMapping:     tun.NATMappingAddressAndPortDependent,
		UDPFiltering:   tun.NATFilteringAddressAndPortDependent,
		MemoryPressure: memoryPressure,
	})
	if err != nil {
		ns.ctxCancel()
		ns.device.Close()
		return nil, err
	}
	tundev.PostFilterPacketInboundFromWireGuard = ns.injectInbound
	tundev.PreFilterPacketOutboundToWireGuardNetstackIntercept = ns.handleLocalPackets
	return ns, nil
}

func (ns *Impl) Close() error {
	ns.ctxCancel()
	return E.Errors(ns.ipstack.Close(), ns.device.Close())
}

type LocalBackend = any

func (ns *Impl) Start(backend LocalBackend) error {
	if backend != nil {
		ns.lb = backend.(*ipnlocal.LocalBackend)
	}
	err := ns.ipstack.Start()
	if err != nil {
		return err
	}
	ns.ready.Store(true)
	return nil
}

func (ns *Impl) UpdateNetstackIPs(networkMap *netmap.NetworkMap) {
	if networkMap == nil {
		ns.atomicIsLocalIPFunc.Store(ipset.FalseContainsIPFunc())
		ns.atomicIsVIPServiceIPFunc.Store(ipset.FalseContainsIPFunc())
		ns.localAddresses.Store(nil)
		return
	}
	ns.atomicIsLocalIPFunc.Store(ipset.NewContainsIPFunc(networkMap.GetAddresses()))
	var addresses []netip.Addr
	for _, prefix := range networkMap.GetAddresses().All() {
		addresses = append(addresses, prefix.Addr())
	}
	ns.localAddresses.Store(addresses)
	if buildfeatures.HasServe {
		services := make(set.Set[netip.Addr])
		for _, serviceAddresses := range networkMap.GetVIPServiceIPMap() {
			services.AddSlice(serviceAddresses)
		}
		ns.atomicIsVIPServiceIPFunc.Store(services.Contains)
	}
}

func (ns *Impl) UpdateIPServiceMappings(mappings netmap.IPServiceMappings) {
	ns.atomicIPVIPServiceMap.Store(mappings)
}

func (ns *Impl) UpdateActiveVIPServices(activeServices views.Slice[string]) {
	services := make(set.Set[tailcfg.ServiceName], activeServices.Len())
	for _, service := range activeServices.All() {
		services.Add(tailcfg.AsServiceName(service))
	}
	ns.atomicActiveVIPServices.Store(services)
}

func (ns *Impl) localAddress(remote netip.Addr) netip.Addr {
	for _, address := range ns.localAddresses.Load() {
		if address.Is4() == remote.Is4() {
			return address
		}
	}
	return netip.Addr{}
}

func (ns *Impl) DialContextTCP(ctx context.Context, remote netip.AddrPort) (*tun.GoConn, error) {
	return ns.ipstack.DialTCP(ctx, ns.localAddress(remote.Addr()), remote)
}

func (ns *Impl) DialContextTCPWithBind(ctx context.Context, local netip.Addr, remote netip.AddrPort) (*tun.GoConn, error) {
	return ns.ipstack.DialTCP(ctx, local, remote)
}

func (ns *Impl) DialContextUDP(ctx context.Context, remote netip.AddrPort) (*tun.GoUDPConn, error) {
	return ns.DialContextUDPWithBind(ctx, ns.localAddress(remote.Addr()), remote)
}

func (ns *Impl) DialContextUDPWithBind(ctx context.Context, local netip.Addr, remote netip.AddrPort) (*tun.GoUDPConn, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return ns.ipstack.DialUDP(netip.AddrPortFrom(local, 0), remote)
}

func (ns *Impl) ListenPacket(network, address string) (net.PacketConn, error) {
	local, err := netip.ParseAddrPort(address)
	if err != nil {
		return nil, err
	}
	if network != "udp4" && network != "udp6" || (network == "udp4") != local.Addr().Is4() {
		return nil, E.New("netstack: incompatible network and address: ", network, " ", address)
	}
	conn, err := ns.ipstack.ListenUDP(local)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func (ns *Impl) inject(packets []*buf.Buffer) error {
	outbound := packets[:0]
	for index, buffer := range packets {
		var parsed packet.Parsed
		parsed.Decode(buffer.Bytes())
		var err error
		switch {
		case ns.shouldSendToHost(&parsed):
			err = ns.tundev.InjectInboundBuffer(buffer)
		case ns.isLocalIP(parsed.Dst.Addr()):
			_, err = ns.device.Write(buffer.Bytes())
			buffer.Release()
		default:
			outbound = append(outbound, buffer)
		}
		if err != nil {
			buf.ReleaseMulti(outbound)
			buf.ReleaseMulti(packets[index+1:])
			return err
		}
	}
	return ns.tundev.InjectOutboundBuffers(ns.ctx, outbound)
}

func (ns *Impl) shouldSendToHost(parsed *packet.Parsed) bool {
	source, destination := parsed.Src.Addr(), parsed.Dst.Addr()
	if source == serviceIP || source == serviceIPv6 {
		return true
	}
	if !ns.isLocalIP(destination) {
		return false
	}
	return ns.isVIPServiceIP(source) || viaRange.Contains(source) && ns.lb != nil && ns.lb.ShouldHandleViaIP(source)
}

func (ns *Impl) shouldProcessInbound(parsed *packet.Parsed) bool {
	destination := parsed.Dst.Addr()
	local := ns.isLocalIP(destination)
	if ns.lb != nil && parsed.IPProto == ipproto.TCP && local {
		var peerAPIPort uint16
		if parsed.TCPFlags&packet.TCPSynAck == packet.TCPSyn {
			port, found := ns.lb.GetPeerAPIPort(destination)
			if found {
				peerAPIPort = port
				ns.peerAPIPortAtomic(destination).Store(uint32(port))
			}
		} else {
			peerAPIPort = uint16(ns.peerAPIPortAtomic(destination).Load())
		}
		if peerAPIPort != 0 && parsed.Dst.Port() == peerAPIPort || ns.lb.ShouldInterceptTCPPort(parsed.Dst.Port()) {
			return true
		}
	}
	if buildfeatures.HasServe && ns.isVIPServiceIP(destination) {
		if parsed.IsEchoRequest() || ns.lb != nil && parsed.IPProto == ipproto.TCP && ns.lb.ShouldInterceptVIPServiceTCPPort(parsed.Dst) {
			return true
		}
		return ns.ipstack.HasEndpoint(uint8(parsed.IPProto), parsed.Dst, parsed.Src)
	}
	if parsed.IPVersion == 6 && !local && viaRange.Contains(destination) {
		return ns.lb != nil && ns.lb.ShouldHandleViaIP(destination)
	}
	if ns.ProcessLocalIPs && local || ns.ProcessSubnets && !local {
		return true
	}
	if ns.CheckLocalTransportEndpoints {
		if ns.HasListenerForDestination != nil && ns.HasListenerForDestination(uint8(parsed.IPProto), parsed.Dst) {
			return true
		}
		return local && ns.ipstack.HasEndpoint(uint8(parsed.IPProto), parsed.Dst, parsed.Src)
	}
	return false
}

func (ns *Impl) JudgeFlow(protocol uint8, source, destination netip.AddrPort, firstPacket []byte) tun.FlowVerdict {
	if ns.Handler == nil || ns.isLocalIP(destination.Addr()) || destination.Addr() == serviceIP || destination.Addr() == serviceIPv6 || ns.isVIPServiceIP(destination.Addr()) || viaRange.Contains(destination.Addr()) {
		return tun.FlowVerdict{Action: tun.ActionAccept}
	}
	if ns.HasListenerForDestination != nil && ns.HasListenerForDestination(protocol, destination) {
		return tun.FlowVerdict{Action: tun.ActionAccept}
	}
	return ns.Handler.JudgeFlow(protocol, source, destination, firstPacket)
}

func (ns *Impl) NewDNSPacket(payload []byte, source, destination M.Socksaddr, writer N.PacketWriter) {
	ns.Handler.NewDNSPacket(payload, source, destination, writer)
}

func (ns *Impl) NewConnectionEx(ctx context.Context, conn net.Conn, source, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	limit := 2048
	if version.IsMobile() {
		limit = 1024
	} else if version.OS() == "linux" {
		limit = 8192
	}
	ns.access.Lock()
	if ns.inFlight >= limit || ns.inFlightByClient[source.Addr] >= limit*2/3 {
		ns.access.Unlock()
		N.CloseOnHandshakeFailure(conn, onClose, E.New("netstack: too many connection attempts"))
		return
	}
	ns.inFlight++
	ns.inFlightByClient[source.Addr]++
	ns.access.Unlock()
	release := sync.OnceFunc(func() {
		ns.access.Lock()
		ns.inFlight--
		ns.inFlightByClient[source.Addr]--
		if ns.inFlightByClient[source.Addr] == 0 {
			delete(ns.inFlightByClient, source.Addr)
		}
		ns.access.Unlock()
	})
	defer release()
	keepalive := net.KeepAliveConfig{Enable: true, Idle: 2 * time.Hour, Interval: 75 * time.Second, Count: 9}
	var handler func(net.Conn)
	service := destination.Addr == serviceIP || destination.Addr == serviceIPv6
	if service && destination.Port == 53 {
		handler = func(client net.Conn) { ns.dns.HandleTCPConn(client, source.AddrPort()) }
	} else if ns.lb != nil {
		backendHandler, options := ns.lb.TCPHandlerForDst(source.AddrPort(), destination.AddrPort())
		if backendHandler != nil {
			if options.Idle != 0 {
				keepalive.Idle = options.Idle
			}
			handler = func(client net.Conn) {
				err := backendHandler(client)
				if err != nil && !E.IsClosedOrCanceled(err) {
					ns.logf("netstack: local service: %v", err)
				}
			}
		}
	}
	if idle := netstackKeepaliveIdle(); idle > 0 {
		keepalive.Idle = idle
	}
	if interval := netstackKeepaliveInterval(); interval > 0 {
		keepalive.Interval = interval
	}
	conn.(*tun.GoConn).SetKeepAliveConfig(keepalive)
	if handler != nil {
		err := N.ReportConnHandshakeSuccess(conn, nil)
		if err != nil {
			N.CloseOnHandshakeFailure(conn, onClose, err)
			return
		}
		release()
		handler(&serverConn{conn.(*tun.GoConn)})
		return
	}
	if ns.GetTCPHandlerForFlow != nil {
		flowHandler, intercept := ns.GetTCPHandlerForFlow(source.AddrPort(), destination.AddrPort())
		if flowHandler != nil && intercept {
			err := N.ReportConnHandshakeSuccess(conn, nil)
			if err != nil {
				N.CloseOnHandshakeFailure(conn, onClose, err)
				return
			}
			release()
			flowHandler(&serverConn{conn.(*tun.GoConn)})
			return
		}
		if intercept && ns.Handler == nil {
			N.CloseOnHandshakeFailure(conn, onClose, E.New("netstack: connection refused"))
			return
		}
	}
	if service && !ns.isLoopbackPort(destination.Port) || ns.isVIPServiceIP(destination.Addr) {
		N.CloseOnHandshakeFailure(conn, onClose, E.New("netstack: port is not served"))
		return
	}
	if ns.Handler != nil && !service {
		ns.Handler.NewConnectionEx(ctx, conn, source, destination, onClose)
		return
	}
	local := ns.isLocalIP(destination.Addr)
	dialAddress := tsaddr.UnmapVia(destination.Addr)
	if service && destination.Addr == serviceIPv6 {
		dialAddress = ipv6Loopback
	} else if service || tsaddr.IsTailscaleIP(dialAddress) {
		dialAddress = ipv4Loopback
	}
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	dial := ns.forwardDialFunc
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	backend, err := dial(dialCtx, "tcp", netip.AddrPortFrom(dialAddress, destination.Port).String())
	if err != nil {
		N.CloseOnHandshakeFailure(conn, onClose, err)
		return
	}
	defer backend.Close()
	if local {
		backendAddress := M.SocksaddrFromNet(backend.LocalAddr()).AddrPort()
		err = ns.pm.RegisterIPPortIdentity("tcp", backendAddress, source.Addr)
		if err != nil {
			N.CloseOnHandshakeFailure(conn, onClose, err)
			return
		}
		defer ns.pm.UnregisterIPPortIdentity("tcp", backendAddress)
	}
	err = N.ReportConnHandshakeSuccess(conn, backend)
	if err != nil {
		N.CloseOnHandshakeFailure(conn, onClose, err)
		return
	}
	release()
	err = bufio.CopyConn(ctx, conn, backend)
	if onClose != nil {
		onClose(err)
	}
}

func (ns *Impl) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, source, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	natConn := conn.(*tun.UDPNatConn)
	natConn.SetTimeout(0)
	conn = &handlerPacketConn{natConn}
	service := destination.Addr == serviceIP || destination.Addr == serviceIPv6
	if service && destination.Port == 53 {
		ns.handleMagicDNSUDP(ctx, conn, source)
		return
	}
	if service && !ns.isLoopbackPort(destination.Port) {
		conn.Close()
		return
	}
	if ns.GetUDPHandlerForFlow != nil {
		handler, intercept := ns.GetUDPHandlerForFlow(source.AddrPort(), destination.AddrPort())
		if intercept && handler != nil {
			packetConn := bufio.NewDestinationNATPacketConn(bufio.NewNetPacketConn(conn), destination, source)
			handler(&serverPacketConn{BindPacketConn: bufio.NewBindPacketConn(packetConn, source.UDPAddr()), local: destination})
			return
		}
		if intercept && ns.Handler == nil {
			conn.Close()
			return
		}
	}
	if ns.Handler != nil && !service {
		ns.Handler.NewPacketConnectionEx(ctx, conn, source, destination, onClose)
		return
	}
	defer conn.Close()
	local := ns.isLocalIP(destination.Addr)
	remote := destination
	remote.Addr = tsaddr.UnmapVia(remote.Addr)
	var bindAddress netip.Addr
	if local || service {
		remote.Addr = ipv4Loopback
		if service && destination.Addr == serviceIPv6 {
			remote.Addr = ipv6Loopback
		}
		bindAddress = remote.Addr
	} else if remote.IsIPv4() {
		bindAddress = netip.IPv4Unspecified()
	} else {
		bindAddress = netip.IPv6Unspecified()
	}
	listenAddress := net.UDPAddrFromAddrPort(netip.AddrPortFrom(bindAddress, source.Port))
	backend, err := net.ListenUDP("udp", listenAddress)
	if err != nil {
		listenAddress.Port = 0
		backend, err = net.ListenUDP("udp", listenAddress)
		if err != nil {
			ns.logf("netstack: listen UDP: %v", err)
			return
		}
	}
	defer backend.Close()
	if local {
		backendAddress := M.SocksaddrFromNet(backend.LocalAddr()).AddrPort()
		err = ns.pm.RegisterIPPortIdentity("udp", backendAddress, source.Addr)
		if err != nil {
			ns.logf("netstack: register UDP mapping: %v", err)
			return
		}
		defer ns.pm.UnregisterIPPortIdentity("udp", backendAddress)
	}
	var client N.PacketConn = conn
	if remote != destination {
		client = bufio.NewNATPacketConn(bufio.NewNetPacketConn(conn), destination, remote)
	}
	timeout := 2 * time.Minute
	if destination.Port == 53 {
		timeout = 30 * time.Second
	}
	ctx, client = canceler.NewPacketConn(ctx, client, timeout)
	err = bufio.CopyPacketConn(ctx, client, bufio.NewPacketConn(backend))
	if onClose != nil {
		onClose(err)
	}
}

func (ns *Impl) handleMagicDNSUDP(ctx context.Context, conn N.PacketConn, source M.Socksaddr) {
	defer conn.Close()
	writer := bufio.NewNetPacketWriter(conn)
	buffer := buf.NewSize(tstun.MaxPacketSize)
	defer buffer.Release()
	for {
		buffer.Reset()
		conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
		destination, err := conn.ReadPacket(buffer)
		if err != nil {
			return
		}
		response, err := ns.dns.Query(ctx, buffer.Bytes(), "udp", source.AddrPort())
		if err != nil {
			ns.logf("netstack: DNS query: %v", err)
			return
		}
		_, err = writer.WriteTo(response, destination)
		if err != nil {
			return
		}
	}
}

type stackLogger struct{ logf logger.Logf }

func (l stackLogger) Trace(args ...any) { l.logf("[v2] netstack: %s", F.ToString(args...)) }
func (l stackLogger) Debug(args ...any) { l.logf("[v2] netstack: %s", F.ToString(args...)) }
func (l stackLogger) Info(args ...any)  { l.logf("netstack: %s", F.ToString(args...)) }
func (l stackLogger) Warn(args ...any)  { l.logf("netstack: %s", F.ToString(args...)) }
func (l stackLogger) Error(args ...any) { l.logf("netstack: %s", F.ToString(args...)) }
func (l stackLogger) Fatal(args ...any) { panic(F.ToString(args...)) }
func (l stackLogger) Panic(args ...any) { panic(F.ToString(args...)) }

var userPingSem = syncs.NewSemaphore(20)

type userPingDirection int

const (
	userPingDirectionOutbound userPingDirection = iota
	userPingDirectionInbound
)

func (ns *Impl) isLoopbackPort(port uint16) bool {
	if ns.loopbackPort != nil && int(port) == *ns.loopbackPort {
		return true
	}
	return false
}

func (ns *Impl) isLocalIP(ip netip.Addr) bool {
	return ns.atomicIsLocalIPFunc.Load()(ip)
}

func (ns *Impl) isVIPServiceIP(ip netip.Addr) bool {
	if !buildfeatures.HasServe {
		return false
	}
	return ns.atomicIsVIPServiceIPFunc.Load()(ip)
}

func (ns *Impl) peerAPIPortAtomic(ip netip.Addr) *atomic.Uint32 {
	if ip.Is4() {
		return &ns.peerapiPort4Atomic
	} else {
		return &ns.peerapiPort6Atomic
	}
}

func (ns *Impl) handleLocalPackets(p *packet.Parsed, t *tstun.Wrapper) filter.Response {
	if !ns.ready.Load() || ns.ctx.Err() != nil {
		return filter.DropSilently
	}

	dst := p.Dst.Addr()
	serviceName, isVIPServiceIP := ns.atomicIPVIPServiceMap.Load()[dst]
	switch {
	case dst == serviceIP || dst == serviceIPv6:
	case isVIPServiceIP:
		activeServices := ns.atomicActiveVIPServices.Load()
		if !activeServices.Contains(serviceName) {
			return filter.Accept
		}
		if p.IPProto != ipproto.TCP {
			return filter.DropSilently
		}
		if debugNetstack() {
			ns.logf("netstack: intercepting local VIP service packet: proto=%v dst=%v src=%v",
				p.IPProto, p.Dst, p.Src)
		}
	case viaRange.Contains(dst):
		var shouldHandle bool
		if p.IPVersion == 6 && !ns.isLocalIP(dst) {
			shouldHandle = ns.lb != nil && ns.lb.ShouldHandleViaIP(dst)
		}
		if !shouldHandle {
			return filter.Accept
		}

		if debugNetstack() {
			ns.logf("netstack: handling local 4via6 packet: version=%d proto=%v dst=%v src=%v",
				p.IPVersion, p.IPProto, p.Dst, p.Src)
		}

		pingIP, handlePing := ns.shouldHandlePing(p)
		if handlePing {
			ns.logf("netstack: handling local 4via6 ping: dst=%v pingIP=%v", dst, pingIP)

			var pong []byte // the reply to the ping, if our relayed ping works
			if dst.Is4() {
				h := p.ICMP4Header()
				h.ToResponse()
				pong = packet.Generate(&h, p.Payload())
			} else if dst.Is6() {
				h := p.ICMP6Header()
				h.ToResponse()
				pong = packet.Generate(&h, p.Payload())
			}

			go ns.userPing(pingIP, pong, userPingDirectionInbound)
			return filter.DropSilently
		}

	default:
		return filter.Accept
	}
	if debugPackets {
		ns.logf("[v2] service packet in (from %v): % x", p.Src, p.Buffer())
	}

	ns.device.Write(p.Buffer())
	return filter.DropSilently
}

func (ns *Impl) injectInbound(p *packet.Parsed, t *tstun.Wrapper) filter.Response {
	if !ns.ready.Load() || ns.ctx.Err() != nil {
		return filter.DropSilently
	}

	if !ns.shouldProcessInbound(p) {
		if ns.CheckLocalTransportEndpoints && (p.IPProto == ipproto.Fragment || p.IsError()) {
			ns.device.Write(p.Buffer())
		}
		return filter.Accept
	}

	destIP := p.Dst.Addr()

	pingIP, handlePing := ns.shouldHandlePing(p)
	if handlePing {
		var pong []byte // the reply to the ping, if our relayed ping works
		if destIP.Is4() {
			h := p.ICMP4Header()
			h.ToResponse()
			pong = packet.Generate(&h, p.Payload())
		} else if destIP.Is6() {
			h := p.ICMP6Header()
			h.ToResponse()
			pong = packet.Generate(&h, p.Payload())
		}
		go ns.userPing(pingIP, pong, userPingDirectionOutbound)
		return filter.DropSilently
	}

	if debugPackets {
		ns.logf("[v2] packet in (from %v): % x", p.Src, p.Buffer())
	}
	ns.device.Write(p.Buffer())

	return filter.DropSilently
}

func (ns *Impl) shouldHandlePing(p *packet.Parsed) (_ netip.Addr, ok bool) {
	if !p.IsEchoRequest() {
		return netip.Addr{}, false
	}

	destIP := p.Dst.Addr()

	if viaRange.Contains(destIP) {
		return tsaddr.UnmapVia(destIP), true
	}

	if !ns.ProcessSubnets || ns.Handler != nil {
		return netip.Addr{}, false
	}

	if tsaddr.IsTailscaleIP(destIP) {
		return netip.Addr{}, false
	}

	return destIP, true
}

func (ns *Impl) userPing(dstIP netip.Addr, pingResPkt []byte, direction userPingDirection) {
	if !userPingSem.TryAcquire() {
		return
	}
	defer userPingSem.Release()

	t0 := time.Now()
	err := ns.sendOutboundUserPing(dstIP, 3*time.Second)
	d := time.Since(t0)
	if err != nil {
		if d < time.Second/2 {
			ns.logf("exec ping of %v failed in %v: %v", dstIP, d, err)
		}
		return
	}
	if debugNetstack() {
		ns.logf("exec pinged %v in %v", dstIP, time.Since(t0))
	}
	if direction == userPingDirectionOutbound {
		if err := ns.tundev.InjectOutbound(pingResPkt); err != nil {
			ns.logf("InjectOutbound ping response: %v", err)
		}
	} else if direction == userPingDirectionInbound {
		if err := ns.tundev.InjectInboundCopy(pingResPkt); err != nil {
			ns.logf("InjectInboundCopy ping response: %v", err)
		}
	}
}

func windowsPingOutputIsSuccess(ip netip.Addr, b []byte) bool {
	sub := fmt.Appendf(nil, " %s: ", ip)

	eqSigns := func(bb []byte) (n int) {
		for _, b := range bb {
			if b == '=' || (b == '<' && n == 1) {
				n++
			}
		}
		return
	}

	for len(b) > 0 {
		var line []byte
		line, b, _ = bytes.Cut(b, []byte("\n"))
		if _, rest, ok := bytes.Cut(line, sub); ok && eqSigns(rest) == 3 {
			return true
		}
	}
	return false
}
