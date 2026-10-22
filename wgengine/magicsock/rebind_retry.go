// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package magicsock

import "time"

const (
	bindRetryInitialDelay = time.Second
	bindRetryMaxDelay     = 30 * time.Second
)

// retryFailedBind restores a real UDP socket after all IPv4 bind candidates
// failed. A minor link change (or no subsequent event at all) is insufficient
// to repair blockForeverConn, whose writes deliberately succeed without
// sending anything. Do not use netcheck failure as a trigger: an otherwise
// healthy socket can simply be behind a firewall that drops STUN.
func (c *Conn) retryFailedBind() {
	defer close(c.bindRetryDone)
	delay := bindRetryInitialDelay
	for {
		c.rebindMu.Lock()
		needed := c.bindRetryNeeded && !c.bindRetryPaused
		postBind := c.bindRetryPostBind && !c.bindRetryPaused
		c.bindRetryPostBind = false
		c.rebindMu.Unlock()
		if postBind && !c.closing.Load() {
			c.updateAfterBind()
			c.afterRebind()
			c.ReSTUN("bind-updated")
		}
		if !needed {
			delay = bindRetryInitialDelay
			select {
			case <-c.donec:
				return
			case <-c.bindRetryWake:
				continue
			}
		}

		timer := time.NewTimer(delay)
		select {
		case <-c.donec:
			timer.Stop()
			return
		case <-c.bindRetryWake:
			timer.Stop()
			// A completed bind, a fresh failure, or a Close/Open transition
			// starts a new recovery episode, even if wakeups were coalesced.
			delay = bindRetryInitialDelay
			continue
		case <-timer.C:
		}

		attempted, err := c.rebindIfNeeded(keepCurrentPort, true)
		if c.closing.Load() {
			return
		}
		if !attempted {
			// For example, TCP-only mode is active. Stay idle at the capped
			// interval instead of binding sockets or spinning.
			delay = min(delay*2, bindRetryMaxDelay)
			continue
		}
		if err != nil {
			delay = min(delay*2, bindRetryMaxDelay)
			c.logf("magicsock: UDP bind recovery failed; retrying in %v: %v", delay, err)
			continue
		}

		c.logf("magicsock: UDP bind recovered; restarting endpoint discovery")
		c.afterRebind()
		c.ReSTUN("bind-recovered")
		delay = bindRetryInitialDelay
	}
}
