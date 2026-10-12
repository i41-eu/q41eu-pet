package app

import (
	"time"

	"github.com/i41-eu/q41eu-pet/internal/pet"
)

// The watchdog runs with every Pulse. The engine sends the robot commands when
// something changes, so a command that is lost or undone (the robot blanks its own
// screen after a minute without commands, a full send queue, a timer that did not
// fire) would leave the robot wrong until the next reaction. The watchdog repairs
// that: stuck states end, and an idle pet shows its whole face again now and then.
const (
	keepAlive = 30 * time.Second // the robot's screensaver starts after 60 s without a command
	maxBusy   = 2 * time.Minute  // longer than any reaction (a game ball keeps it busy for 1 min)
	staleWait = 5 * time.Second  // a menu or a picture this long past its time is stuck
)

func (a *App) watchdog(r *robot, now time.Time) {
	c := r.conn
	if c == nil {
		return
	}
	if n := c.sent.Load(); n != r.sentSeen {
		r.sentSeen, r.quietSince = n, now
	}
	if n := c.dropped.Load(); n != r.droppedSeen {
		a.log.Warn("watchdog: commands dropped, the robot's send queue was full", "robot", r.id, "dropped", n-r.droppedSeen)
		r.droppedSeen = n
		r.quietSince = time.Time{} // show everything again below
	}
	fixed := func(what string) { a.log.Warn("watchdog", "robot", r.id, "fixed", what) }
	idle := now.After(r.busyUntil)
	switch {
	case r.busyUntil.Sub(now) > maxBusy:
		fixed("busy for too long")
		a.stopGame(r)
		if r.menu != "" {
			a.closeMenu(r)
		}
	case r.game != nil && idle: // each ball keeps it busy for a minute
		fixed("game stuck")
		a.stopGame(r)
	case r.menu != "" && now.After(r.busyUntil.Add(staleWait)):
		fixed("menu left open")
		a.closeMenu(r)
	case r.pictureOn && r.game == nil && r.menu == "" && now.After(r.busyUntil.Add(staleWait)):
		fixed("picture left on")
	default:
		if r.pet.Phase(now) == pet.School { // resting: the screen stays dark
			if !r.screenOff && idle && !time.Now().Before(r.speakingUntil) {
				a.schoolScreen(r)
			}
			return
		}
		// Keep-alive: by day an idle pet shows its face again before the robot blanks it.
		if !idle || r.game != nil || r.menu != "" || r.screenManual || r.pet.Phase(now) != pet.Awake ||
			time.Now().Before(r.speakingUntil) || now.Sub(r.quietSince) < keepAlive {
			return
		}
	}
	r.faceShown = "" // forget what the robot shows: express sends the face again
	a.express(r, now)
}
