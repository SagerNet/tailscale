// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

// Code generated by tailscale.com/cmd/cloner; DO NOT EDIT.

package wgcfg

import (
	"net/netip"

	"github.com/sagernet/tailscale/tailcfg"
	"github.com/sagernet/tailscale/types/key"
	"github.com/sagernet/tailscale/types/logid"
	"github.com/sagernet/tailscale/types/ptr"
)

// Clone makes a deep copy of Config.
// The result aliases no memory with the original.
func (src *Config) Clone() *Config {
	if src == nil {
		return nil
	}
	dst := new(Config)
	*dst = *src
	dst.Addresses = append(src.Addresses[:0:0], src.Addresses...)
	dst.DNS = append(src.DNS[:0:0], src.DNS...)
	if src.Peers != nil {
		dst.Peers = make([]Peer, len(src.Peers))
		for i := range dst.Peers {
			dst.Peers[i] = *src.Peers[i].Clone()
		}
	}
	return dst
}

// A compilation failure here means this code must be regenerated, with the command at the top of this file.
var _ConfigCloneNeedsRegeneration = Config(struct {
	Name           string
	NodeID         tailcfg.StableNodeID
	PrivateKey     key.NodePrivate
	Addresses      []netip.Prefix
	MTU            uint16
	DNS            []netip.Addr
	Peers          []Peer
	NetworkLogging struct {
		NodeID             logid.PrivateID
		DomainID           logid.PrivateID
		LogExitFlowEnabled bool
	}
}{})

// Clone makes a deep copy of Peer.
// The result aliases no memory with the original.
func (src *Peer) Clone() *Peer {
	if src == nil {
		return nil
	}
	dst := new(Peer)
	*dst = *src
	dst.AllowedIPs = append(src.AllowedIPs[:0:0], src.AllowedIPs...)
	if dst.V4MasqAddr != nil {
		dst.V4MasqAddr = ptr.To(*src.V4MasqAddr)
	}
	if dst.V6MasqAddr != nil {
		dst.V6MasqAddr = ptr.To(*src.V6MasqAddr)
	}
	return dst
}

// A compilation failure here means this code must be regenerated, with the command at the top of this file.
var _PeerCloneNeedsRegeneration = Peer(struct {
	PublicKey           key.NodePublic
	DiscoKey            key.DiscoPublic
	AllowedIPs          []netip.Prefix
	V4MasqAddr          *netip.Addr
	V6MasqAddr          *netip.Addr
	IsJailed            bool
	PersistentKeepalive uint16
	WGEndpoint          key.NodePublic
}{})
