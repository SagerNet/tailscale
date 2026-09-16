// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build !ts_omit_netstack

package ipnlocal

import (
	"net"
	"net/netip"
	"time"
)

// TCPHandlerForDst returns a TCP handler for connections to dst, or nil if
// no handler is needed.
// TCPHandlerForDst is called both for connections to our node's local IP
// as well as to the service IP (quad 100).
func (b *LocalBackend) TCPHandlerForDst(src, dst netip.AddrPort) (handler func(c net.Conn) error, options net.KeepAliveConfig) {
	// First handle internal connections to the service IP
	hittingServiceIP := dst.Addr() == magicDNSIP || dst.Addr() == magicDNSIPv6
	if hittingServiceIP {
		switch dst.Port() {
		case 80:
			// TODO(mpminardi): do we want to show an error message if the web client
			// has been disabled instead of the more "basic" web UI?
			if b.ShouldRunWebClient() {
				return b.handleWebClientConn, options
			}
			return b.HandleQuad100Port80Conn, options
		case DriveLocalPort:
			return b.handleDriveConn, options
		}
	}

	if f, ok := hookServeTCPHandlerForVIPService.GetOk(); ok {
		if handler := f(b, dst, src); handler != nil {
			return handler, options
		}
	}
	// Then handle external connections to the local IP.
	if !b.isLocalIP(dst.Addr()) {
		return nil, options
	}
	if dst.Port() == 22 && b.ShouldRunSSH() {
		// Use a higher keepalive idle time for SSH connections, as they are
		// typically long lived and idle connections are more likely to be
		// intentional. Ideally we would turn this off entirely, but we can't
		// tell the difference between a long lived connection that is idle
		// vs a connection that is dead because the peer has gone away.
		// We pick 72h as that is typically sufficient for a long weekend.
		options.Idle = 72 * time.Hour
		return b.handleSSHConn, options
	}
	// TODO(will,sonia): allow customizing web client port ?
	if dst.Port() == webClientPort && b.ShouldExposeRemoteWebClient() {
		return b.handleWebClientConn, options
	}
	if port, ok := b.GetPeerAPIPort(dst.Addr()); ok && dst.Port() == port {
		return func(c net.Conn) error {
			b.handlePeerAPIConn(src, dst, c)
			return nil
		}, options
	}
	if f, ok := hookTCPHandlerForServe.GetOk(); ok {
		if handler := f(b, dst.Port(), src, nil); handler != nil {
			return handler, options
		}
	}
	return nil, options
}
