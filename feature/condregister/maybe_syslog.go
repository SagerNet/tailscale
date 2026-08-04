// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build (linux || freebsd || openbsd) && !ts_omit_syslog

package condregister

import _ "github.com/sagernet/tailscale/feature/syslog"
