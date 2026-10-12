package app

import (
	"context"
	"sync/atomic"

	"github.com/i41-eu/q41eu-dev/robothub"
	"github.com/i41-eu/q41eu-raw/wire"
)

// robotConn is one robot's connection, from the dev kit's hub (github.com/i41-eu/q41eu-dev/robothub:
// the token check, Register, pings, pairing codes, the manager's reports), with the pet's own
// rules on top: no head commands while the robot is in someone's hands, and the counts the
// watchdog reads.
type robotConn struct {
	hubRobot
	id string

	sent, dropped atomic.Uint64 // messages queued, and dropped because the queue was full (watchdog.go)
	held          atomic.Bool   // the robot is in someone's hands: no head commands (held.go)
}

// hubRobot is what the pet uses of a robothub.Robot (an interface, so tests can stand in for it).
type hubRobot interface {
	Command(name string, args map[string]any) bool
	Frame(kind string, body any) bool
	Binary(kind byte, payload []byte) bool
	Done() <-chan struct{}
	Queued() int
	Managed() bool
	Close()
}

func (c *robotConn) close() { c.Close() }

// count notes a message queued, or dropped (a full queue; not a connection that has gone).
func (c *robotConn) count(ok bool) bool {
	if ok {
		c.sent.Add(1)
		return true
	}
	select {
	case <-c.Done():
	default:
		c.dropped.Add(1)
	}
	return false
}

// frame queues a JSON frame.
func (c *robotConn) frame(kind string, body any) bool { return c.count(c.Frame(kind, body)) }

// command queues a robot command.
func (c *robotConn) command(name string, args map[string]any) bool {
	if headCommands[name] && c.held.Load() {
		return false // the head stays still in a kid's hands
	}
	return c.count(c.Command(name, args))
}

// binary queues a binary message: type byte, then payload.
func (c *robotConn) binary(kind byte, payload []byte) bool { return c.count(c.Binary(kind, payload)) }

// newHub: robots connect through the hub; the pet's state stays here.
func (a *App) newHub() *robothub.Hub {
	return robothub.New(robothub.Config{
		Token: a.cfg.RobotToken, Manager: a.cfg.Manager, PublicURL: a.cfg.PublicURL, PairTTL: a.cfg.PairTTL, Log: a.log,
		Connected: func(r *robothub.Robot) { a.attach(&robotConn{hubRobot: r, id: r.ID()}, r.Capabilities().Commands) },
		Disconnected: func(r *robothub.Robot) {
			a.mu.Lock()
			c := a.robots[r.ID()]
			a.mu.Unlock()
			if c != nil && c.conn != nil && c.conn.hubRobot == hubRobot(r) {
				a.detach(c.conn)
			}
		},
		Event:     func(r *robothub.Robot, ev wire.RobotEventBody) { a.robotEvent(r.ID(), ev) },
		Telemetry: func(r *robothub.Robot, m map[string]float64) { a.telemetry(r.ID(), m) },
		Binary: func(r *robothub.Robot, kind byte, payload []byte) {
			switch kind {
			case wire.BinLight: // the game's hand detection
				a.lightSamples(r.ID(), payload)
			case wire.BinSnapshot: // a leaderboard photo, or a screen snapshot (photos.go)
				a.robotSnapshot(r.ID(), payload)
			case wire.BinAudioMulti: // a debug recording (debug.go)
				a.micAudio(r.ID(), payload)
			}
		},
		// Browsers paired before: the robot starts with its face, not the QR screen.
		Paired: func(id string) wire.PairedBody {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.paired(id)
		},
		Owner: func(id, owner string) {
			a.mu.Lock()
			defer a.mu.Unlock()
			if rb := a.robots[id]; owner == "" || rb == nil || rb.owner != owner {
				a.setOwnerLocked(id, owner)
			}
		},
	})
}

func (a *App) attach(c *robotConn, commands []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.robotFor(c.id)
	r.conn = c // the hub closed any older connection
	c.held.Store(r.hold.held)
	r.commands = commands
	r.files = map[string]bool{} // filled from the robot's "assets" answer
	r.spriteIDs, r.faceShown, r.faceHidden = nil, "", false
	r.lastPlay, r.idleOff = a.cfg.Now(), false // just connected: the full time before the screen goes off
	r.lastSeen = a.cfg.Now()
	c.command("light_stream", map[string]any{"on": false}) // in case a game was cut short
	c.command("assets", nil)                               // what the robot's file store holds (logged; used later for the pet's pictures)
	a.robotOnline(r)
	go a.reportSeen(context.Background(), c)
	a.publishState(r)
}

func (a *App) detach(c *robotConn) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.robots[c.id]
	if r == nil || r.conn != c {
		return
	}
	r.conn = nil
	r.game = nil // the robot drops its streams when it switches servers or reconnects
	a.publishState(r)
}
