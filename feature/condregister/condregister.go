// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

// The condregister package registers all conditional features guarded
// by build tags. It is one central package that callers can empty import
// to ensure all conditional features are registered.
package condregister

import (
	// Portmapper is special in that the CLI also needs to link it in,
	// so it's pulled out into its own package, rather than using a maybe_*.go
	// file in condregister.
	_ "github.com/sagernet/tailscale/feature/condregister/portmapper"
	// HTTP proxy support is also needed by the CLI, and tsnet, so it's its
	// own package too.
	_ "github.com/sagernet/tailscale/feature/condregister/useproxy"
)
