package netmon

import (
	"sync/atomic"

	N "github.com/sagernet/sing/common/network"
)

var defaultRouteOverride atomic.Pointer[func() (DefaultRouteDetails, error)]

// SetDefaultRouteFunc sets a custom function that overrides the
// platform-specific default route lookup.
// Pass nil to restore the default platform behavior.
func SetDefaultRouteFunc(f func() (DefaultRouteDetails, error)) {
	if f != nil {
		defaultRouteOverride.Store(&f)
	} else {
		defaultRouteOverride.Store(nil)
	}
}

func (m *Monitor) Dialer() N.Dialer {
	return m.dialer
}
