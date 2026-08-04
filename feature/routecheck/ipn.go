// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package routecheck

import (
	"github.com/sagernet/tailscale/ipn/ipnext"
	"github.com/sagernet/tailscale/ipn/ipnlocal"
	"github.com/sagernet/tailscale/net/routecheck"
)

// ClientFor returns the [routecheck.Client] for a given backend,
// or nil if route checking is not available for that backend.
func ClientFor(b *ipnlocal.LocalBackend) *routecheck.Client {
	e, ok := ipnlocal.GetExt[*Extension](b)
	if e == nil || !ok {
		return nil
	}
	return e.Client
}

// Report contains the result of a single routecheck.
type Report = routecheck.Report

// NodeBackender is a shim between [ipnext.Host] and [routecheck.NodeBackender].
type nodeBackender struct{ ipnext.Host }

var _ routecheck.NodeBackender = nodeBackender{}

func (nb nodeBackender) NodeBackend() routecheck.NodeBackend {
	return nb.Host.NodeBackend()
}
