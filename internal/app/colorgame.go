package app

import (
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"github.com/i41-eu/q41eu-pet/internal/pet"
	"github.com/i41-eu/q41eu-pet/internal/robotpic"
)

// The color game: the screen shows six color buttons, three by two, in a new order
// every round; the robot's LED strips show colors, and the kid presses them in the
// level's order. Each level starts with a picture of the robot from above, its
// strips numbered in that order (robotpic.Order):
//
//	level 1  the left strip, then the right one
//	level 2  the right strip, then the left one
//	level 3  left near the screen, left far, right near, right far
//	level 4  right far, right near, left far, left near
//	level 5  the four halves in a random order (not level 3's or 4's)
//
// Each level has colorTurns rounds: new colors, the buttons shuffled. The same color
// twice in a row is a double tap. A part goes dark once pressed. A wrong button only
// buzzes and greys the buttons out for timing.penalty (the clock runs on); a round not
// done in colorRoundMax moves on and counts
// with that time. At the end the robot says the time over all rounds; the best time
// (every round done) is kept. It is a game like catch (r.game, with colors
// set). Everything here runs with a.mu held.

// colorRoundMax ends a round the kid does not finish; it counts with this time.
const colorRoundMax = 20 * time.Second

// A wrong button greys all of them out for timing.penalty (3 s; the clock runs on).
const colorGreyed = 0.2 // the buttons' opacity meanwhile

// The game is colorLevels levels of colorTurns rounds each.
const colorLevels, colorTurns = 5, 5

type colorGame struct {
	turn        int      // the round in this level, 1..colorTurns
	done        int      // rounds done over the game
	parts       int      // 2 (left, right) or 4 (halves)
	order       []int    // the parts in the order to press
	colors      []string // each part's color
	next        int      // how many are pressed
	started     time.Time
	lockedUntil time.Time     // greyed out after a wrong button until then
	total       time.Duration // the levels so far
	wrong       int
}

// pressNext is the color to press next.
func (cg *colorGame) pressNext() string { return cg.colors[cg.order[cg.next]] }

// The LED strips: 12 single LEDs, left 0-5, right 6-11 (wire "leds" pixels). The
// halves near the kid (the screen) and far from it: the left strip starts at the
// screen, the right one runs the other way (seen on the robot).
var (
	ledLeft      = []int{0, 1, 2, 3, 4, 5}
	ledRight     = []int{6, 7, 8, 9, 10, 11}
	ledLeftNear  = []int{0, 1, 2}
	ledLeftFar   = []int{3, 4, 5}
	ledRightNear = []int{9, 10, 11}
	ledRightFar  = []int{6, 7, 8}
)

// colorParts are the LEDs of each part: 2 the strips, 4 their halves.
func colorParts(n int) [][]int {
	if n == 2 {
		return [][]int{ledLeft, ledRight}
	}
	return [][]int{ledLeftNear, ledLeftFar, ledRightNear, ledRightFar}
}

// colorLevel is level's parts and order (1-based level).
func colorLevel(level int) (int, []int) {
	switch level {
	case 1:
		return 2, []int{0, 1}
	case 2:
		return 2, []int{1, 0}
	case 3:
		return 4, []int{0, 1, 2, 3}
	case 4:
		return 4, []int{3, 2, 1, 0}
	}
	for {
		order := rand.Perm(4)
		if !slices.Equal(order, []int{0, 1, 2, 3}) && !slices.Equal(order, []int{3, 2, 1, 0}) {
			return 4, order
		}
	}
}

// colorSpots are the button centers on the 320x240 screen, three by two.
var colorSpots = [6][2]int{{56, 62}, {160, 62}, {264, 62}, {56, 178}, {160, 178}, {264, 178}}

// canColors: the robot has the game's pictures.
func (r *robot) canColors() bool {
	if !r.canSprite(assetDir + "menu-bg.png") {
		return false
	}
	for _, c := range robotpic.Colors {
		if !r.canSprite(assetDir + "color-" + c + ".png") {
			return false
		}
	}
	return true
}

// colorsAction starts the color game (from the robot's play menu).
func (a *App) colorsAction(r *robot, now time.Time) pet.Reaction {
	if r.game != nil {
		return pet.Reaction{Kind: pet.KindGame}
	}
	if r.conn == nil || !r.canColors() {
		return r.pet.Play(now)
	}
	re := r.pet.StartGame(now)
	if re.Kind == pet.KindGame {
		a.startColors(r, now)
	}
	return re
}

func (a *App) startColors(r *robot, now time.Time) {
	r.gen++ // drop pending steps of earlier reactions
	g := &game{colors: &colorGame{}}
	r.game = g
	r.menu = "" // the game replaces a menu (its sprites are cleared below)
	r.busyUntil = now.Add(time.Minute)
	if r.pictureOn {
		r.conn.command("face", nil)
		r.pictureOn = false
	}
	a.rememberHead(r)
	a.gameScreen(r, true)
	a.clearSprites(r)
	a.emotion(r, "happy")
	a.sprite(r, map[string]any{"id": "c:bg", "asset": assetDir + "menu-bg.png", "x": 160, "y": 120, "z": 20})
	a.say(r, "color_game", "", 2)
	a.play(r, "hello", false)
	a.publishState(r)
	a.afterGame(r, g, a.timing.intro, func() { a.colorLevelStart(r, g) })
}

// colorLevelStart starts the next level with its order picture (colorShowOrder).
func (a *App) colorLevelStart(r *robot, g *game) {
	if g.round >= colorLevels {
		a.endColors(r, g)
		return
	}
	g.round++
	cg := g.colors
	cg.parts, cg.order = colorLevel(g.round)
	cg.turn = 0
	r.conn.command("leds", map[string]any{"pixels": slices.Repeat([]string{"#000000"}, 12)})
	for _, id := range append([]string{"c:bg"}, colorIDs()...) { // the picture is under the sprites
		r.conn.command("sprite", map[string]any{"id": id, "hidden": true})
	}
	a.showPicture(r, robotpic.Order(cg.parts, cg.order))
	r.busyUntil = a.now().Add(time.Minute)
	a.publishState(r)
	level := g.round
	a.afterGame(r, g, 4*a.timing.intro, func() { // 6 s: time to learn the order
		if g.round != level {
			return
		}
		r.conn.command("face", nil)
		r.pictureOn = false
		r.conn.command("sprite", map[string]any{"id": "c:bg", "hidden": false})
		a.colorTurn(r, g)
	})
}

// colorTurn is the next round of the level: new colors, the buttons shuffled.
func (a *App) colorTurn(r *robot, g *game) {
	cg := g.colors
	if cg.turn >= colorTurns {
		a.colorLevelStart(r, g)
		return
	}
	cg.turn++
	cg.colors, cg.next = make([]string, cg.parts), 0
	for i := range cg.colors {
		cg.colors[i] = robotpic.Colors[rand.IntN(len(robotpic.Colors))]
	}
	for i, spot := range rand.Perm(len(colorSpots)) {
		c := robotpic.Colors[i]
		a.sprite(r, map[string]any{"id": "c:" + c, "asset": assetDir + "color-" + c + ".png",
			"x": colorSpots[spot][0], "y": colorSpots[spot][1], "z": 21, "tap": true, "hidden": false, "opacity": 1.0})
	}
	cg.lockedUntil = time.Time{}
	a.colorLEDs(r, cg)
	cg.started = time.Now()
	r.busyUntil = a.now().Add(time.Minute)
	level, turn := g.round, cg.turn
	a.afterGame(r, g, colorRoundMax, func() {
		if g.round == level && cg.turn == turn && cg.next < cg.parts { // not finished: on to the next round
			cg.total += colorRoundMax
			cg.started = time.Time{}
			a.colorTurn(r, g)
		}
	})
}

// colorButtons sets the buttons' opacity (greyed out after a wrong one, or back).
func (a *App) colorButtons(r *robot, opacity float64) {
	for _, id := range colorIDs() {
		r.conn.command("sprite", map[string]any{"id": id, "opacity": opacity})
	}
}

// colorIDs are the buttons' sprite ids.
func colorIDs() []string {
	ids := make([]string, len(robotpic.Colors))
	for i, c := range robotpic.Colors {
		ids[i] = "c:" + c
	}
	return ids
}

// colorLEDs shows on the LED strips the colors still to press; the pressed ones go dark.
func (a *App) colorLEDs(r *robot, cg *colorGame) {
	pixels := slices.Repeat([]string{"#000000"}, 12)
	parts := colorParts(cg.parts)
	for i, part := range parts {
		if slices.Contains(cg.order[:cg.next], i) {
			continue // pressed
		}
		for _, p := range part {
			pixels[p] = robotpic.ColorLED[cg.colors[i]]
		}
	}
	r.conn.command("leds", map[string]any{"pixels": pixels})
}

// colorTap is a tap on a button (sprite id "c:<color>") during the color game.
func (a *App) colorTap(r *robot, g *game, sprite string) {
	cg := g.colors
	c, ok := strings.CutPrefix(sprite, "c:")
	if !ok || !slices.Contains(robotpic.Colors, c) || cg.started.IsZero() || cg.next >= cg.parts {
		return
	}
	if time.Now().Before(cg.lockedUntil) {
		return // greyed out after a wrong button
	}
	if c != cg.pressNext() { // the penalty: the buttons grey out for a while, the clock runs on
		cg.wrong++
		cg.lockedUntil = time.Now().Add(a.timing.penalty)
		a.play(r, "no", false)
		a.colorButtons(r, colorGreyed)
		level, turn := g.round, cg.turn
		a.afterGame(r, g, a.timing.penalty, func() {
			if g.round == level && cg.turn == turn {
				a.colorButtons(r, 1)
			}
		})
		return
	}
	cg.next++
	a.play(r, "chirp", false)
	a.colorLEDs(r, cg)
	if cg.next < cg.parts {
		return
	}
	cg.total += time.Since(cg.started) // the level is done
	cg.started = time.Time{}           // no presses until the next level's buttons
	cg.done++
	g.hits = cg.done / colorTurns // the page counts levels
	r.pet.Caught(a.now())
	a.publishState(r)
	level, turn := g.round, cg.turn
	a.afterGame(r, g, a.timing.gap, func() {
		if g.round == level && cg.turn == turn {
			a.colorTurn(r, g)
		}
	})
}

// endColors: the face says how long it took (or the new record).
func (a *App) endColors(r *robot, g *game) {
	now := a.now()
	cg := g.colors
	r.game = nil
	a.gameScreen(r, false)
	a.restoreHead(r)
	a.clearSprites(r)
	before := slices.Clone(r.pet.ColorsTop)
	re := r.pet.FinishColors(now, cg.done, colorLevels*colorTurns, cg.total)
	a.dropPhotos(before, r.pet.ColorsTop)
	a.log.Info("color game over", "robot", r.id, "rounds", cg.done, "seconds", cg.total.Seconds(), "wrong", cg.wrong, "record", re.Record)
	a.dirty = true
	busy := 6 * time.Second
	if re.Place > 0 && a.canPhoto(r) {
		busy = a.timing.photoShot + 2*time.Second // the countdown and the photo
	}
	a.begin(r, now, busy)
	a.emotion(r, "happy")
	a.play(r, "tada", false)
	leds := idleSides(r.pet, r.pet.Mood(now))
	leds["effect"], leds["seconds"], leds["speed"] = "rainbow", 3, 2
	r.conn.command("leds", leds)
	key := "color_done"
	if re.Record {
		key = "color_record"
	}
	a.say(r, key, secondsText(r.pet.Settings.Lang, cg.total), 4)
	r.conn.command("nod", nil)
	a.boardAfterGame(r, re, now)
	a.publishReaction(r, re)
	a.publishState(r)
}
