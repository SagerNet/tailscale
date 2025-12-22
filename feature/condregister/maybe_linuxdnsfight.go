// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

//go:build linux && !android && !ts_omit_linuxdnsfight

package condregister

import _ "github.com/sagernet/tailscale/feature/linuxdnsfight"
