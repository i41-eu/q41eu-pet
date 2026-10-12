package app

// The browsers paired with a robot set up by a Stackchan manager, reported to the manager every
// 15 s (robot-auth "seen"); the answer names the pairings its owner removed there. The robot's apps
// are its manager's business: the robot talks to it on its own channel.

import "context"

func (a *App) reportSeen(ctx context.Context, c *robotConn) {
	a.mu.Lock()
	pairings := a.pairings(c.id)
	a.mu.Unlock()
	unpair, err := a.hub.Report(ctx, c.id, pairings) // a new owner comes through the hub's Owner
	if err != nil {
		return
	}
	a.unpair(c.id, unpair)
}

func (a *App) reportAllSeen(ctx context.Context) {
	a.mu.Lock()
	var conns []*robotConn
	for _, rb := range a.robots {
		if rb.conn != nil && rb.conn.Managed() {
			conns = append(conns, rb.conn)
		}
	}
	a.mu.Unlock()
	for _, c := range conns {
		a.reportSeen(ctx, c)
	}
}
