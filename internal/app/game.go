package app

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"github.com/i41-eu/q41eu-pet/internal/pet"
	"github.com/i41-eu/q41eu-pet/internal/robotpic"
	"github.com/i41-eu/q41eu-raw/wire"
)

// Catch the ball: the robot's screen shows a ball in one of its four quarters
// and the kid taps it. pet.GameRounds balls; taps on the wrong quarter are
// simply ignored (no failing, just try again), and a ball not caught in time
// moves on. The head makes every ball harder:
//
//	ball 1    the head keeps still
//	ball 2    the head circles
//	ball 3    the head moves at random
//	ball 4, 5 the head dodges a hand that comes close
//
// A hand is seen by the robot's light sensor next to the screen (light_stream,
// binary 0x08, 20 samples/s): proximity rises, or the hand's shadow darkens
// the light. Each dodge gives the kid one more second. Without the stream
// (older firmware, proximity off) the head dodges on a timer instead.
// Everything here runs with a.mu held.

// timing is the game's pace (and the dream share); per App, so tests can speed it up.
type timing struct {
	round       time.Duration // to catch one ball
	moveExtra   time.Duration // more time for balls with a moving head
	gap         time.Duration // after a catch, before the next ball
	intro       time.Duration
	stars       time.Duration // the result, before the face says the score
	dodgeEvery  time.Duration
	dodgeBack   time.Duration // a dodge returns to the middle after this
	dreamShare  float64       // how often a touch while asleep shows a dream; otherwise a sleepy "Zzz"
	demoPicture time.Duration // the demo reset picture, before the face says it
	penalty     time.Duration // the color game: buttons greyed out after a wrong one

	photoAsk, photoCount, photoShot time.Duration // the leaderboard photo's steps (photos.go)
}

var defaultTiming = timing{
	round: 5 * time.Second, moveExtra: time.Second, gap: 700 * time.Millisecond, intro: 1500 * time.Millisecond,
	stars: 3 * time.Second, demoPicture: 3 * time.Second, penalty: 3 * time.Second,
	photoAsk: 4500 * time.Millisecond, photoCount: 9 * time.Second, photoShot: 11500 * time.Millisecond, dodgeEvery: 1200 * time.Millisecond, dodgeBack: 1100 * time.Millisecond, dreamShare: 0.4,
}

// Hand detection (raw sensor counts; see handNear).
const (
	proxRise   = 20  // proximity above its baseline: a hand close to the screen (games saw 0..30)
	shadowPart = 0.4 // light CH0 below this part of its baseline: a hand's shadow (0.6 fired on leaning over)
	shadowMin  = 20  // ... when there is enough light for a shadow to show
)

// Head positions in degrees: yaw left/right, pitch up from level (5..85 on the robot).
const (
	headMidPitch = 15
	circleYaw    = 25
	circlePitch  = 10
	wanderYaw    = 30
	dodgeYaw     = 35
)

type game struct {
	round    int       // 1..pet.GameRounds, 0 before the first ball
	hits     int       // balls caught
	spot     int       // where the ball is (robotpic.SpotAt), -1 before the first
	waiting  bool      // the current ball can still be caught
	deadline time.Time // the current ball flies on at this (real) time

	// Hand detection from the light stream.
	psBase, ch0Base float64 // "nobody near" levels, -1 before the first sample
	sawLight        bool    // light samples arrived this ball
	samples         int
	psMin, psMax    float64 // raw ranges over the game, logged to tune the thresholds
	ch0Min, ch0Max  float64
	allDodges       int
	dodges          int // this ball
	lastDodge       time.Time
	side            float64 // the last dodge direction, +1 or -1

	colors *colorGame // the color game (colorgame.go); nil for catch
}

// gameView is the game as the kid's page shows it.
type gameView struct {
	Round  int `json:"round"`
	Rounds int `json:"rounds"`
	Hits   int `json:"hits"`
}

// gameBrightness is the screen's brightness (%) during a game: fixed, as a kid's hand
// over the light sensor would dim the automatic one.
const gameBrightness = 70

// gameScreen fixes the brightness for a game (on), or gives it back to the light sensor.
func (a *App) gameScreen(r *robot, on bool) {
	if on {
		r.conn.command("brightness", map[string]any{"value": gameBrightness})
		return
	}
	r.conn.command("brightness", map[string]any{"auto": true})
}

// afterGame runs fn after d if g is still the robot's game.
func (a *App) afterGame(r *robot, g *game, d time.Duration, fn func()) {
	time.AfterFunc(d, func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if r.game == g && r.conn != nil {
			fn()
		}
	})
}

// inRound runs fn after d if the game is still at this ball and it was not caught yet.
func (a *App) inRound(r *robot, g *game, round int, d time.Duration, fn func()) {
	a.afterGame(r, g, d, func() {
		if g.round == round && g.waiting {
			fn()
		}
	})
}

// startGame begins a game on the robot (after pet.StartGame said yes).
func (a *App) startGame(r *robot, now time.Time) {
	r.gen++ // drop pending steps of earlier reactions
	g := &game{spot: -1, psBase: -1, ch0Base: -1, side: 1}
	r.game = g
	if r.menu != "" {
		a.closeMenu(r)
	}
	r.busyUntil = now.Add(time.Minute)
	if r.pictureOn {
		r.conn.command("face", nil)
		r.pictureOn = false
	}
	r.conn.command("light_stream", map[string]any{"on": true})
	a.gameScreen(r, true)
	a.rememberHead(r)
	a.emotion(r, "happy")
	a.say(r, "game", "", 2)
	a.play(r, "hello", false)
	a.publishState(r)
	a.afterGame(r, g, a.timing.intro, func() { a.nextRound(r, g) })
}

func (a *App) nextRound(r *robot, g *game) {
	if g.round >= pet.GameRounds {
		a.endGame(r, g)
		return
	}
	g.round++
	spot := rand.IntN(robotpic.Spots)
	if g.spot >= 0 { // never the same place twice in a row
		spot = rand.IntN(robotpic.Spots - 1)
		if spot >= g.spot {
			spot++
		}
	}
	g.spot, g.waiting = spot, true
	g.dodges, g.sawLight = 0, false
	if ball := assetDir + "ball.png"; r.canSprite(ball) {
		// The ball glides over the face to its next spot (the center of a quarter).
		x, y := 80+160*(spot%2), 60+120*(spot/2)
		if g.round == 1 {
			a.sprite(r, map[string]any{"id": "ball", "asset": ball, "x": x, "y": y, "scale": 0.6, "z": 1})
		} else {
			a.sprite(r, map[string]any{"id": "ball", "x": x, "y": y, "ms": 300})
		}
	} else {
		a.showPicture(r, robotpic.Ball(spot))
	}
	r.busyUntil = a.now().Add(time.Minute)
	a.publishState(r)

	settings := r.pet.Settings
	d := a.timing.round * time.Duration(settings.GameBallSeconds) / 5 // the parent's ball time (default 5 s)
	if g.round > 1 && settings.GameHeadMoves {
		d += a.timing.moveExtra
	}
	g.deadline = time.Now().Add(d)
	a.roundTimeout(r, g, g.round)
	a.moveHead(r, g, g.round)
}

// roundTimeout moves on when the ball's deadline passes (dodges push it later).
func (a *App) roundTimeout(r *robot, g *game, round int) {
	a.inRound(r, g, round, time.Until(g.deadline), func() {
		if left := time.Until(g.deadline); left > 0 {
			a.roundTimeout(r, g, round)
			return
		}
		g.waiting = false // not caught: the next ball
		a.nextRound(r, g)
	})
}

func (a *App) look(r *robot, yaw, pitch float64) {
	r.conn.command("look", map[string]any{"yaw": math.Round(yaw), "pitch": math.Round(pitch)})
}

// moveHead starts this ball's head movement.
func (a *App) moveHead(r *robot, g *game, round int) {
	switch {
	case round == 1 || !r.pet.Settings.GameHeadMoves:
		a.look(r, 0, headMidPitch)
	case round == 2:
		a.circle(r, g, round, 0)
	case round == 3:
		a.wander(r, g, round)
	default: // hold still, dodge when a hand comes (or on a timer without the light stream)
		a.look(r, 0, headMidPitch)
		for _, at := range []time.Duration{1500 * time.Millisecond, 3200 * time.Millisecond} {
			a.inRound(r, g, round, at, func() {
				if !g.sawLight {
					a.dodge(r, g, time.Now())
				}
			})
		}
	}
}

// circle moves the head around a small circle, 8 steps per turn.
func (a *App) circle(r *robot, g *game, round, step int) {
	angle := float64(step) * math.Pi / 4
	a.look(r, circleYaw*math.Cos(angle), headMidPitch+circlePitch*math.Sin(angle))
	a.inRound(r, g, round, 350*time.Millisecond, func() { a.circle(r, g, round, step+1) })
}

// wander moves the head to random places.
func (a *App) wander(r *robot, g *game, round int) {
	a.look(r, (rand.Float64()*2-1)*wanderYaw, 5+rand.Float64()*25)
	next := time.Duration(600+rand.IntN(500)) * time.Millisecond
	a.inRound(r, g, round, next, func() { a.wander(r, g, round) })
}

// dodge turns the head away from a coming hand, then back; a few times per ball.
func (a *App) dodge(r *robot, g *game, now time.Time) {
	if g.round < 4 || !g.waiting || g.dodges >= g.round-2 || now.Sub(g.lastDodge) < a.timing.dodgeEvery ||
		!r.pet.Settings.GameHeadMoves {
		return
	}
	g.dodges++
	g.allDodges++
	g.lastDodge = now
	g.side = -g.side
	a.look(r, g.side*dodgeYaw, headMidPitch+5)
	g.deadline = g.deadline.Add(time.Second) // a fair chance after each dodge
	round := g.round
	a.inRound(r, g, round, a.timing.dodgeBack, func() { a.look(r, 0, headMidPitch) })
}

// handNear reads one light sample: true when a hand seems close. The baselines
// follow the room: proximity drops at once and rises slowly, light follows
// while no hand is near.
func (g *game) handNear(ps, ch0 float64) bool {
	g.sawLight = true
	g.samples++
	if g.samples == 1 {
		g.psMin, g.psMax, g.ch0Min, g.ch0Max = ps, ps, ch0, ch0
	}
	g.psMin, g.psMax = min(g.psMin, ps), max(g.psMax, ps)
	g.ch0Min, g.ch0Max = min(g.ch0Min, ch0), max(g.ch0Max, ch0)
	if g.psBase < 0 {
		g.psBase, g.ch0Base = ps, ch0
		return false
	}
	near := ps >= g.psBase+proxRise || (g.ch0Base >= shadowMin && ch0 < g.ch0Base*shadowPart)
	if ps < g.psBase {
		g.psBase = ps
	} else if !near {
		g.psBase += (ps - g.psBase) * 0.02
	}
	if !near {
		g.ch0Base += (ch0 - g.ch0Base) * 0.05
	}
	return near
}

// lightSamples handles a light stream message (payload after the type byte).
func (a *App) lightSamples(id string, payload []byte) {
	if len(payload) < 2 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.robots[id]
	if r == nil || r.game == nil || r.conn == nil {
		return
	}
	g := r.game
	n := int(binary.LittleEndian.Uint16(payload))
	for i := 0; i < n && 2+(i+1)*10 <= len(payload); i++ {
		s := payload[2+i*10:]
		ps := float64(binary.LittleEndian.Uint16(s[4:]))
		ch0 := float64(binary.LittleEndian.Uint16(s[6:]))
		if g.handNear(ps, ch0) {
			a.dodge(r, g, time.Now())
		}
	}
}

// gameTap is a tap on the robot's screen during the game.
func (a *App) gameTap(r *robot, g *game, x, y float64, now time.Time) {
	if !g.waiting || robotpic.SpotAt(x, y) != g.spot {
		return
	}
	g.waiting = false
	g.hits++
	r.pet.Caught(now)
	a.play(r, "chirp", false)
	leds := idleSides(r.pet, r.pet.Mood(now))
	leds["effect"], leds["color"], leds["speed"], leds["seconds"] = "blink", "#00ff40", 3, 1
	r.conn.command("leds", leds)
	a.publishState(r)
	round := g.round
	a.afterGame(r, g, a.timing.gap, func() {
		if g.round == round {
			a.nextRound(r, g)
		}
	})
}

// endGame shows the stars, then the face says how it went.
func (a *App) endGame(r *robot, g *game) {
	now := a.now()
	r.game = nil
	r.conn.command("light_stream", map[string]any{"on": false})
	a.gameScreen(r, false)
	a.restoreHead(r)
	a.log.Info("game over", "robot", r.id, "hits", g.hits, "dodges", g.allDodges, "light_samples", g.samples,
		"proximity", fmt.Sprintf("%.0f..%.0f", g.psMin, g.psMax), "light_ch0", fmt.Sprintf("%.0f..%.0f", g.ch0Min, g.ch0Max))
	re := r.pet.FinishGame(now, g.hits)
	a.dirty = true
	a.begin(r, now, 7*time.Second)
	if star := assetDir + "star.png"; r.canSprite(star) {
		// One star per ball over the face: bright for a catch, faint for a miss.
		a.clearSprites(r)
		a.emotion(r, "happy")
		for i := range pet.GameRounds {
			opacity := 1.0
			if i >= g.hits {
				opacity = 0.25
			}
			a.sprite(r, map[string]any{"id": fmt.Sprintf("star%d", i), "asset": star, "x": 32 + 64*i, "y": 120, "scale": 0.35, "opacity": opacity, "z": 3})
		}
	} else {
		a.showPicture(r, robotpic.Stars(g.hits, pet.GameRounds))
	}
	a.play(r, "tada", false)
	leds := idleSides(r.pet, r.pet.Mood(now))
	leds["effect"], leds["seconds"], leds["speed"] = "rainbow", 3, 2
	r.conn.command("leds", leds)
	a.later(r, a.timing.stars, func() { // the speech bubble is under pictures and sprites: face first
		r.conn.command("face", nil)
		a.clearSprites(r)
		r.pictureOn = false
		a.emotion(r, "happy")
		key, score := "game_over", scoreText(r.pet.Settings.Lang, g.hits, pet.GameRounds)
		switch g.hits {
		case 0:
			key = "game_over_0"
		case pet.GameRounds:
			key = "game_over_all"
		}
		a.say(r, key, score, 4)
		r.conn.command("nod", nil)
	})
	a.publishReaction(r, re)
	a.publishState(r)
}

// stopGame drops a running game (bedtime, the robot left).
func (a *App) stopGame(r *robot) {
	if r.game == nil {
		return
	}
	r.game = nil
	if r.conn != nil {
		r.conn.command("light_stream", map[string]any{"on": false})
		a.gameScreen(r, false)
		a.restoreHead(r)
		a.clearSprites(r)
	}
	a.publishState(r)
}

// gameEvent handles a robot event during a game; false if the game does not care.
func (a *App) gameEvent(r *robot, ev wire.RobotEventBody, now time.Time) bool {
	g := r.game
	if g == nil {
		return false
	}
	switch ev.Name {
	case "screen_tap":
		if g.colors != nil {
			sprite, _ := ev.Data["sprite"].(string)
			a.colorTap(r, g, sprite)
			break
		}
		x, _ := ev.Data["x"].(float64)
		y, _ := ev.Data["y"].(float64)
		a.gameTap(r, g, x, y, now)
	case "screensaver_on": // a quick double tap blanks the robot's screen: not now
		r.conn.command("screensaver", map[string]any{"on": false})
	}
	return true // other events wait until the game is over
}

// playAction starts a game on the robot, or plays without it when the robot is away.
func (a *App) playAction(r *robot, now time.Time) pet.Reaction {
	if r.game != nil {
		return pet.Reaction{Kind: pet.KindGame}
	}
	if r.conn == nil {
		return r.pet.Play(now)
	}
	re := r.pet.StartGame(now)
	if re.Kind == pet.KindGame {
		a.startGame(r, now)
	}
	return re
}

func (g *game) view() *gameView {
	if g == nil {
		return nil
	}
	return &gameView{Round: g.round, Rounds: pet.GameRounds, Hits: g.hits}
}
