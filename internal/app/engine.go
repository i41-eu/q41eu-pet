package app

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/i41-eu/q41eu-dev/sound"
	"github.com/i41-eu/q41eu-pet/internal/pet"
	"github.com/i41-eu/q41eu-pet/internal/robotpic"
	"github.com/i41-eu/q41eu-raw/wire"
)

// The engine turns robot events into pet actions, and the pet's mood and
// reactions into robot commands: face, speech bubble, stickers, LEDs, head
// gestures and short sounds. Everything here runs with a.mu held.

var moodEmotion = map[pet.Mood]string{
	pet.Happy: "happy", pet.OK: "neutral", pet.Hungry: "sad", pet.Bored: "doubt",
	pet.Tired: "sleepy", pet.Napping: "sleepy", pet.Sleeping: "sleepy",
}

// Idle LED colours per mood, dim: the LEDs sit right next to the kid's eyes.
var moodColor = map[pet.Mood]string{
	pet.Happy: "#402a00", pet.OK: "#161616", pet.Hungry: "#301000", pet.Bored: "#001a40",
	pet.Tired: "#1a0030", pet.Napping: "#0a0018",
}

const (
	cardQuiet = 2 * time.Second // head touches this soon after a card are the card

	hardPressZone   = 3 // all three head zones at full (0-3): the whole palm; one finger only dreams
	dreamEvery      = 8 * time.Second
	nightLightColor = "#180600"
	reactionTime    = 4 * time.Second  // how long a reaction shows before the mood comes back
	nagEvery        = 30 * time.Minute // "I'm hungry!" at most this often
	helloEvery      = 20 * time.Minute // greeting someone who comes close
)

// moodLEDs is the "leds" command for a mood.
func moodLEDs(p *pet.Pet, mood pet.Mood) map[string]any {
	switch mood {
	case pet.Sleeping:
		if p.Settings.NightLight {
			return map[string]any{"left": nightLightColor, "right": nightLightColor}
		}
		return map[string]any{"effect": "off"}
	case pet.Hungry: // slow orange breathing: "feed me"
		c := moodColor[mood]
		return map[string]any{"left": c, "right": c, "effect": "breathe", "color": "#ff6000", "speed": 0.3}
	}
	c := moodColor[mood]
	return map[string]any{"left": c, "right": c}
}

// idleSides are the mood colours to come back to after a timed LED effect.
func idleSides(p *pet.Pet, mood pet.Mood) map[string]any {
	c := moodColor[mood]
	if c == "" {
		c = "#000000"
	}
	return map[string]any{"left": c, "right": c}
}

// express shows the current mood: face, emotion, LEDs, screen on by day.
func (a *App) express(r *robot, now time.Time) {
	c := r.conn
	if c == nil {
		return
	}
	mood := r.pet.Mood(now)
	r.shownMood = mood
	r.busyUntil = time.Time{}
	a.clearSprites(r)
	if r.pictureOn {
		c.command("face", nil)
		r.pictureOn = false
	}
	if r.screenOff && mood != pet.Sleeping && r.pet.Phase(now) != pet.School {
		c.command("screensaver", map[string]any{"on": false})
		r.screenOff = false
	}
	c.command("emotion", map[string]any{"name": moodEmotion[mood]})
	a.drawnFace(r, moodFace[mood])
	if mood == pet.Napping || mood == pet.Sleeping {
		a.energyBar(r)
	}
	if mood == pet.Sleeping {
		c.command("leds", a.nightLEDs(r, now))
	} else {
		c.command("leds", moodLEDs(r.pet, mood))
	}
}

// later runs fn after d unless another reaction started meanwhile (or the robot left).
func (a *App) later(r *robot, d time.Duration, fn func()) {
	gen := r.gen
	time.AfterFunc(d, func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if r.gen == gen && r.conn != nil {
			fn()
		}
	})
}

// begin starts a reaction: the mood waits until it is over.
func (a *App) begin(r *robot, now time.Time, d time.Duration) {
	r.gen++
	r.busyUntil = now.Add(d)
	a.later(r, d, func() { a.express(r, a.now()) })
}

func (a *App) say(r *robot, key, arg string, seconds float64) {
	a.sayText(r, text(r.pet.Settings.Lang, key, arg), seconds)
}

// sayText shows a line in the speech bubble (folded: the robot's font has no Czech
// letters) and speaks it.
func (a *App) sayText(r *robot, t string, seconds float64) {
	a.sayLine(r, t, seconds, false)
}

func (a *App) sayLine(r *robot, t string, seconds float64, atNight bool) {
	if t == "" {
		return
	}
	// The drawn face stays: the robot shows its speech bubble above it (Embody Mode embody-v0.3.0).
	r.conn.command("say", map[string]any{"text": asciiOnly(t), "seconds": seconds})
	a.speak(r, t, atNight)
}

// play sends a sound when sounds are on; not at night unless atNight (the lullaby).
func (a *App) play(r *robot, name string, atNight bool) {
	s := r.pet.Settings
	if !s.Sounds || (!atNight && s.PhaseAt(a.now()) == pet.Night) {
		return
	}
	if time.Now().Before(r.speakingUntil) {
		return // the robot plays one queue: a sound during speech would chop both into noise
	}
	if asset := soundAsset(name); r.files[asset] && slices.Contains(r.commands, "play") {
		r.conn.command("play", map[string]any{"asset": asset}) // stored on the robot: nothing to send
		return
	}
	if pcm := sound.Chime(name); pcm != nil {
		r.conn.binary(wire.BinSpeakerPCM, sound.Message(pcm))
	}
}

func (a *App) emotion(r *robot, name string) {
	r.conn.command("emotion", map[string]any{"name": name})
	if !r.faceHidden {
		a.drawnFace(r, emotionFace[name])
	}
}

func (a *App) sleepScreen(r *robot) {
	if r.pet.Settings.ScreenOffAtNight {
		r.conn.command("screensaver", map[string]any{"on": true})
		r.screenOff = true
	}
}

// At night the screen is dimmed and goes off nightScreenOn after it was last lit
// (the good night, a touch, a dream); the Tick turns it off.
const (
	nightScreenOn   = 5 * time.Minute
	nightBrightness = 8 // percent
)

// dimForNight dims the screen and starts its 5 minutes.
func (a *App) dimForNight(r *robot, now time.Time) {
	r.conn.command("brightness", map[string]any{"value": nightBrightness})
	r.dimmed = true
	r.screenOnAt = now
}

// brightAgain gives the screen back its automatic brightness (morning, woken at night).
func (a *App) brightAgain(r *robot) {
	if r.dimmed {
		r.conn.command("brightness", map[string]any{"auto": true})
		r.dimmed = false
	}
}

// react shows what an action did.
func (a *App) react(r *robot, re pet.Reaction, now time.Time) {
	c := r.conn
	if c == nil {
		return
	}
	a.log.Info("reaction", "robot", r.id, "kind", re.Kind, "changed", re.Changed, "touch", re.Touch, "food", re.Food)
	r.lastPlay = now
	if r.idleOff { // someone plays: the screen comes back
		r.idleOff = false
		if r.screenOff {
			c.command("screensaver", map[string]any{"on": false})
			r.screenOff = false
		}
	}
	if r.menu != "" { // e.g. a cuddle while the menu is open: the reaction replaces it
		a.closeMenu(r)
	}
	p := r.pet
	lang := p.Settings.Lang
	switch re.Kind {
	case pet.KindSchool:
		a.schoolRest(r, now)
		return
	case pet.KindAsleep: // a touch while asleep: mostly a sleepy "Zzz", sometimes a dream
		if rand.Float64() < a.timing.dreamShare {
			a.dream(r, now)
		} else {
			a.snore(r, now)
		}
		return
	case pet.KindEat, pet.KindEatAgain:
		a.begin(r, now, reactionTime)
		line := foodText(lang, re.Food)
		if re.Kind == pet.KindEatAgain { // the same food a third time: eaten after all
			line = text(lang, "eat_again", "")
		}
		if asset := assetDir + re.Food + ".png"; r.canSprite(asset) {
			a.eatSprite(r, asset, line)
			break
		}
		a.showPicture(r, robotpic.Food(re.Food))
		a.play(r, "munch", false)
		a.later(r, 2500*time.Millisecond, func() {
			c.command("face", nil)
			r.pictureOn = false
			a.emotion(r, "happy")
			a.sayText(r, line, 3)
			c.command("nod", nil)
		})
	case pet.KindDreamFood: // a food card while asleep
		a.dreamOf(r, now, re.Food)
		if re.Changed { // it helped: a murmur in its sleep (a bubble, no voice)
			c.command("say", map[string]any{"text": asciiOnly(text(lang, "dream_food", "")), "seconds": 3})
		}
		return
	case pet.KindPicky: // the same food again: "something else?"
		a.begin(r, now, reactionTime)
		a.emotion(r, "doubt")
		c.command("shake", nil)
		a.say(r, "picky", "", 3)
	case pet.KindFull:
		a.begin(r, now, reactionTime)
		a.emotion(r, "doubt")
		a.say(r, "full", "", 3)
		a.play(r, "no", false)
		c.command("shake", nil)
	case pet.KindCuddle:
		if !re.Changed { // stroking on: already reacting
			return
		}
		a.begin(r, now, reactionTime)
		a.emotion(r, "happy")
		switch re.Touch {
		case pet.TouchTickle: // giggling, blushing
			c.command("sticker", map[string]any{"name": "shy", "seconds": 2})
			a.say(r, "tickle", "", 3)
		case pet.TouchLong: // melting under the hand
			c.command("sticker", map[string]any{"name": "heart", "seconds": 3})
			a.say(r, "long_cuddle", "", 4)
		case pet.TouchScratch: // "right there!"
			c.command("sticker", map[string]any{"name": "heart", "seconds": 2})
			c.command("nod", nil)
			a.say(r, "scratch", "", 4)
		default:
			c.command("sticker", map[string]any{"name": "heart", "seconds": 2})
			a.say(r, "cuddle", "", 3)
		}
		a.play(r, "chirp", false)
	case pet.KindPlay:
		a.begin(r, now, 5*time.Second)
		a.emotion(r, "happy")
		leds := idleSides(p, p.Mood(now))
		leds["effect"], leds["seconds"], leds["speed"] = "rainbow", 4, 2
		c.command("leds", leds)
		a.say(r, "play", "", 3)
		a.play(r, "tada", false)
		// a little dance: look left, right, back to where the head was
		a.rememberHead(r)
		c.command("look", map[string]any{"yaw": -25, "pitch": 15})
		a.later(r, 700*time.Millisecond, func() { c.command("look", map[string]any{"yaw": 25, "pitch": 15}) })
		a.later(r, 1400*time.Millisecond, func() { a.restoreHead(r) })
	case pet.KindTooTired, pet.KindLimit:
		a.begin(r, now, reactionTime)
		a.emotion(r, "sleepy")
		a.say(r, re.Kind, "", 4)
		a.play(r, "yawn", false)
	case pet.KindNap:
		if !re.Changed {
			return
		}
		a.begin(r, now, reactionTime)
		a.emotion(r, "sleepy")
		a.say(r, "nap", "", 3)
		a.play(r, "yawn", false)
	case pet.KindNotTired:
		a.begin(r, now, reactionTime)
		a.emotion(r, "doubt")
		a.say(r, "not_tired", "", 3)
		a.play(r, "no", false)
	case pet.KindWake:
		if !re.Changed {
			return
		}
		a.begin(r, now, reactionTime)
		a.emotion(r, "happy")
		a.say(r, "wake", "", 3)
		a.play(r, "chirp", false)
	case pet.KindGame: // startGame shows it
		return
	case pet.KindShake:
		a.begin(r, now, 3*time.Second)
		c.command("sticker", map[string]any{"name": "dizzy", "seconds": 2})
		a.say(r, "shake", "", 2)
		a.play(r, "whee", false)
	}
}

// showNeeds covers the face with the needs picture for a few seconds.
func (a *App) showNeeds(r *robot, now time.Time) {
	if r.conn == nil {
		return
	}
	r.pet.Advance(now)
	if r.canMenu() { // with a button to close it
		a.openMenu(r, now, "needs")
		return
	}
	s := r.pet.Stats
	a.begin(r, now, 5*time.Second)
	a.showPicture(r, robotpic.Needs(s.Food, s.Fun, s.Energy))
}

// robotEvent handles something that happened on the robot.
func (a *App) robotEvent(id string, ev wire.RobotEventBody) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.robots[id]
	if r == nil {
		return
	}
	now := a.now()
	p := r.pet
	r.lastSeen = now
	if a.gameEvent(r, ev, now) {
		return
	}
	night := p.Phase(now) == pet.Night
	asleep := night || p.Napping(now) // the night, or a daytime nap
	if ev.Name == "screen_tap" || ev.Name == "screen_long_press" {
		a.log.Info("screen touch", "robot", id, "event", ev.Name, "x", ev.Data["x"], "y", ev.Data["y"],
			"sprite", ev.Data["sprite"], "menu", r.menu, "can_menu", r.canMenu(), "asleep", asleep, "game", r.game != nil)
	}
	var re pet.Reaction
	switch ev.Name {
	case "screen_long_press": // pressing the screen long: the screen is its face, "gently, please"
		switch {
		case asleep:
			re = pet.Reaction{Kind: pet.KindAsleep}
		case p.Phase(now) == pet.School:
			re = pet.Reaction{Kind: pet.KindSchool}
		default:
			if r.menu != "" {
				a.closeMenu(r)
			}
			a.gentle(r, now)
			return
		}
	case "head_press":
		zones := [3]float64{}
		for i, k := range []string{"z0", "z1", "z2"} {
			zones[i], _ = ev.Data[k].(float64)
		}
		if !asleep {
			a.touchStart(r, now, zones)
			return
		}
		// Asleep, a light touch (one finger) shows a dream; the whole palm (3,3,3) wakes the pet.
		a.log.Info("head press while asleep", "robot", id, "zones", zones, "night", night) // to tune "hard"
		switch {
		case min(zones[0], zones[1], zones[2]) < hardPressZone:
			re = pet.Reaction{Kind: pet.KindAsleep}
		case night:
			a.nightWake(r, now)
			return
		default:
			re = p.Wake(now)
		}
	case "head_release":
		if !asleep {
			ms, _ := ev.Data["ms"].(float64)
			a.touchEnd(r, now, ms)
		}
		return
	case "head_swipe_forward", "head_swipe_backward":
		if !asleep {
			a.stroke(r, now)
			return
		}
		re = pet.Reaction{Kind: pet.KindAsleep} // a stroke while asleep: a dream
	case "nfc_tag": // a food card
		r.lastCard = now
		r.cuddleGen++ // the card touched the head on its way: not a cuddle
		uid, _ := ev.Data["uid"].(string)
		food, ok := p.Settings.Foods[uid]
		if !ok {
			food = "apple" // works right away; the parent can pick the food later
			if uid != "" {
				r.unknownTags[uid] = now
				trimTags(r.unknownTags)
			}
		}
		if asleep { // asleep the pet dreams of the food; more cards help it sleep
			re = p.DreamFood(now, food)
		} else {
			re = p.Feed(now, food)
		}
		a.log.Info("food card", "robot", id, "uid", uid, "food", food, "reaction", re.Kind, "read_ms", ev.Data["read_ms"])
	case "screen_tap":
		if r.menu != "" {
			sprite, _ := ev.Data["sprite"].(string)
			a.menuTap(r, sprite, now)
			return
		}
		switch {
		case night:
			re = pet.Reaction{Kind: pet.KindAsleep}
		case p.Phase(now) == pet.School:
			re = pet.Reaction{Kind: pet.KindSchool}
		case p.Napping(now): // a tap does not wake a napping pet: a dream (the palm on the head does)
			re = pet.Reaction{Kind: pet.KindAsleep}
		case r.pictureOn: // tap again: back to the face
			a.express(r, now)
			return
		case r.canMenu(): // a tap opens the menu
			a.openMenu(r, now, "main")
			return
		default: // robots without sprites: the needs
			p.Advance(now)
			a.showNeeds(r, now)
			a.publishState(r)
			return
		}
	case "shake":
		if r.hold.shaken() {
			a.setHeld(r)
		}
		re = p.Shake(now)
	case "proximity_near":
		a.hello(r, now)
		return
	case "assets": // the robot's file store
		list, _ := ev.Data["list"].(string)
		free, _ := ev.Data["free"].(float64)
		total, _ := ev.Data["total"].(float64)
		a.log.Info("robot files", "robot", id, "free_kb", int(free/1024), "total_kb", int(total/1024))
		a.syncAssets(r, list)
		return
	case "asset_saved":
		name, _ := ev.Data["name"].(string)
		crc, _ := ev.Data["crc"].(float64)
		a.assetSaved(r, name, uint32(crc))
		return
	case "asset_error":
		a.log.Warn("robot file upload failed", "robot", id, "name", ev.Data["name"], "reason", ev.Data["reason"])
		return
	case "sound_error":
		a.log.Warn("robot could not play a sound", "robot", id, "asset", ev.Data["asset"], "reason", ev.Data["reason"])
		return
	case "sound_done":
		return
	case "screensaver_on":
		if r.menu != "" { // a quick double tap on the menu blanks the robot's screen: not now
			r.conn.command("screensaver", map[string]any{"on": false})
			return
		}
		r.screenOff = true
		r.screenManual = ev.Data["manual"] == 1.0
		return
	case "screensaver_off": // a touch lit the screen: at night it dims and has its 5 minutes again
		r.screenOff, r.screenManual, r.idleOff = false, false, false
		r.lastPlay = now
		if night {
			a.dimForNight(r, now)
		}
		return
	case "sprite_error":
		a.log.Warn("robot could not show a sprite", "robot", id, "id", ev.Data["id"], "reason", ev.Data["reason"])
		return
	default:
		return
	}
	a.dirty = true
	a.react(r, re, now)
	a.publishReaction(r, re)
	a.publishState(r)
}

// trimTags keeps the 20 most recently seen unknown tags.
func trimTags(tags map[string]time.Time) {
	for len(tags) > 20 {
		oldest := ""
		for uid, t := range tags {
			if oldest == "" || t.Before(tags[oldest]) {
				oldest = uid
			}
		}
		delete(tags, oldest)
	}
}

// hello greets someone who came close, now and then, by day.
func (a *App) hello(r *robot, now time.Time) {
	p := r.pet
	if r.conn == nil || p.Settings.PhaseAt(now) != pet.Awake || p.Napping(now) ||
		now.Sub(r.lastHello) < helloEvery || now.Before(r.busyUntil) {
		return
	}
	r.lastHello = now
	a.begin(r, now, 3*time.Second)
	a.emotion(r, "happy")
	a.say(r, "hello", "", 2)
	a.play(r, "hello", false)
}

// robotOnline sets the robot up after it connects.
func (a *App) robotOnline(r *robot) {
	now := a.now()
	p := r.pet
	p.Advance(now)
	r.phase = p.Phase(now)
	r.pictureOn, r.screenOff = false, false
	r.gen++
	if p.Settings.Sounds {
		r.conn.command("volume", map[string]any{"value": p.Settings.Volume})
	}
	a.express(r, now)
	if r.phase == pet.Night && a.viewers(r.id) > 0 { // not while the QR code waits for a scan
		a.dimForNight(r, now)
	}
	if r.phase == pet.School && a.viewers(r.id) > 0 {
		a.schoolScreen(r)
	}
}

// Tick advances every pet and plays the day: morning, bedtime, moods, nags.
func (a *App) Tick() {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for _, r := range a.robots {
		p := r.pet
		p.Advance(now)
		a.dirty = true
		phase := p.Phase(now) // a pet woken at night counts as awake until it falls asleep again
		prev := r.phase
		r.phase = phase
		if r.conn == nil {
			if reset := p.DemoReset(now); len(reset) > 0 { // demo mode without the robot: the page shows it
				a.publishReaction(r, pet.Reaction{Kind: pet.KindDemoReset, Need: reset[0], Changed: true})
			}
			continue
		}
		switch {
		case prev != "" && prev != pet.School && phase == pet.School:
			a.schoolStart(r, now)
		case prev == pet.School && phase != pet.School:
			a.express(r, now) // back from school: the face again
		case phase == pet.School: // resting; the watchdog keeps the screen dark
		case prev != "" && prev != pet.Night && phase == pet.Night:
			a.goodnight(r, now)
		case prev == pet.Night && phase != pet.Night:
			a.morning(r, now)
		case phase != pet.Night && now.After(r.busyUntil):
			if !a.demoCheck(r, now) {
				a.daytime(r, now)
			}
		case phase == pet.Night && now.After(r.busyUntil):
			a.nightFade(r, now)
		}
		a.publishState(r)
	}
}

// daytime: bedtime warning, a finished nap, mood changes and nags.
func (a *App) daytime(r *robot, now time.Time) {
	p := r.pet
	mood := p.Mood(now)
	today := now.Format(time.DateOnly)
	wokenAtNight := p.Settings.PhaseAt(now) == pet.Night // quiet: no warnings, no nags
	if m := p.Settings.MinutesToBed(now); !wokenAtNight && m > 0 && m <= 10 && r.bedWarned != today {
		r.bedWarned = today
		a.begin(r, now, 5*time.Second)
		a.emotion(r, "sleepy")
		a.say(r, "bedtime", "", 4)
		a.play(r, "yawn", false)
		return
	}
	if r.shownMood == pet.Napping && mood != pet.Napping {
		r.shownMood = mood // woken: the Pulse must not see "napping" again
		a.react(r, pet.Reaction{Kind: pet.KindWake, Changed: true}, now)
		return
	}
	if r.phase == pet.Awake && !wokenAtNight && now.Sub(r.lastNag) >= nagEvery {
		key := map[pet.Mood]string{pet.Hungry: "hungry", pet.Bored: "bored", pet.Tired: "tired"}[mood]
		if key != "" {
			r.lastNag = now
			a.begin(r, now, reactionTime)
			a.emotion(r, moodEmotion[mood])
			if mood == pet.Hungry {
				r.conn.command("sticker", map[string]any{"name": "sweat", "seconds": 2})
			}
			a.say(r, key, "", 4)
			a.play(r, map[pet.Mood]string{pet.Hungry: "no", pet.Bored: "chirp", pet.Tired: "yawn"}[mood], false)
			return
		}
	}
	if mood != r.shownMood {
		a.express(r, now)
	}
}

// goodnight: lullaby, sleepy face, night light, then the screen goes dark.
func (a *App) goodnight(r *robot, now time.Time) {
	p := r.pet
	a.stopGame(r)
	p.Note(now, "night", "")
	r.gen++
	r.busyUntil = now.Add(15 * time.Second)
	r.shownMood = pet.Sleeping
	if r.pictureOn {
		r.conn.command("face", nil)
		r.pictureOn = false
	}
	a.emotion(r, "sleepy")
	a.sayLine(r, text(r.pet.Settings.Lang, "goodnight", ""), 6, true)
	a.play(r, "lullaby", true)
	r.sleptAt = now // the night light fades from now
	r.conn.command("leds", a.nightLEDs(r, now))
	a.dimForNight(r, now) // dark after nightScreenOn
}

// morning: screen on, a greeting and a sunrise on the LEDs.
func (a *App) morning(r *robot, now time.Time) {
	p := r.pet
	p.Note(now, "morning", "")
	a.begin(r, now, 7*time.Second)
	r.conn.command("screensaver", map[string]any{"on": false})
	r.screenOff = false
	a.brightAgain(r)
	a.emotion(r, "happy")
	a.say(r, "morning", "", 5)
	a.play(r, "morning", false)
	leds := idleSides(p, p.Mood(now))
	leds["effect"], leds["color"], leds["speed"], leds["seconds"] = "breathe", "#ffb000", 0.5, 6
	r.conn.command("leds", leds)
}

// dream shows what the sleeping pet dreams of: a light touch at night or during
// a nap. No sound; the sleepy face comes back (and at night the screen goes dark).
func (a *App) dream(r *robot, now time.Time) {
	if r.conn == nil || now.Sub(r.lastAsleep) < dreamEvery {
		return
	}
	a.dreamOf(r, now, robotpic.Dreams[rand.IntN(len(robotpic.Dreams))])
}

// dreamOf shows a dream of item (a food card while asleep: that food).
func (a *App) dreamOf(r *robot, now time.Time, item string) {
	if r.conn == nil {
		return
	}
	r.lastAsleep = now
	r.gen++
	r.busyUntil = now.Add(10 * time.Second)
	if cloud, pic := assetDir+"cloud.png", assetDir+item+".png"; r.canSprite(cloud) && r.canSprite(pic) {
		// A dream cloud next to the sleeping face, the dreamed thing bobbing in it.
		if r.screenOff {
			r.conn.command("screensaver", map[string]any{"on": false})
			r.screenOff = false
		}
		if r.pictureOn {
			r.conn.command("face", nil)
			r.pictureOn = false
		}
		a.emotion(r, "sleepy")
		a.sprite(r, map[string]any{"id": "cloud", "asset": cloud, "x": 232, "y": 72, "scale": 0.75, "opacity": 0.95, "z": 1})
		a.sprite(r, map[string]any{"id": "dream", "asset": pic, "x": 239, "y": 70, "scale": 0.4, "z": 2})
		for i, y := range []int{60, 72, 60, 72} {
			a.later(r, time.Duration(800+i*1200)*time.Millisecond, func() { a.sprite(r, map[string]any{"id": "dream", "y": y, "ms": 1100}) })
		}
	} else {
		a.showPicture(r, robotpic.Dream(item))
	}
	a.later(r, 6*time.Second, func() { a.express(r, a.now()) })
	if r.pet.Phase(now) == pet.Night { // a daytime nap keeps the screen on
		a.dimForNight(r, now)
	}
}

// nightWake: a hard press at night wakes the pet for a few minutes (quietly);
// when the time is up the Tick sees night again and plays the lullaby.
func (a *App) nightWake(r *robot, now time.Time) {
	re := r.pet.WakeAtNight(now)
	a.publishReaction(r, re)
	if re.Kind != pet.KindNightWake {
		a.dream(r, now) // waking at night is off: dream on
		return
	}
	if !re.Changed || r.conn == nil {
		return
	}
	a.dirty = true
	r.phase = pet.Awake // woken, not morning: no morning greeting
	a.begin(r, now, reactionTime)
	r.conn.command("screensaver", map[string]any{"on": false})
	r.screenOff = false
	a.brightAgain(r)
	if r.pictureOn {
		r.conn.command("face", nil)
		r.pictureOn = false
	}
	a.emotion(r, "sleepy")
	a.say(r, "night_wake", "", 4)
	a.publishState(r)
}

// snore: the sleepy face murmurs "Zzz..." and sleeps on (a touch while asleep).
func (a *App) snore(r *robot, now time.Time) {
	if r.conn == nil || now.Sub(r.lastAsleep) < 3*time.Second {
		return
	}
	r.lastAsleep = now
	r.gen++
	r.busyUntil = now.Add(4 * time.Second)
	if r.screenOff {
		r.conn.command("screensaver", map[string]any{"on": false}) // the touch woke the screen anyway
		r.screenOff = false
	}
	if r.pictureOn {
		r.conn.command("face", nil)
		r.pictureOn = false
	}
	a.emotion(r, "sleepy")
	r.conn.command("say", map[string]any{"text": "Zzz...", "seconds": 2.5})
	a.later(r, 3*time.Second, func() { a.express(r, a.now()) })
	if r.pet.Phase(now) == pet.Night {
		a.dimForNight(r, now)
	}
}

// eatSprite: the food drops in from the top and glides to the mouth over the face,
// then shrinks bite by bite (munch, nod) and the pet says what it ate.
func (a *App) eatSprite(r *robot, asset, line string) {
	c := r.conn
	a.emotion(r, "happy")
	a.drawnFace(r, "yum")
	a.sprite(r, map[string]any{"id": "food", "asset": asset, "x": 160, "y": -60, "scale": 0.8, "z": 1})
	a.sprite(r, map[string]any{"id": "food", "x": 160, "y": 170, "ms": 700})
	a.later(r, 750*time.Millisecond, func() {
		a.play(r, "munch", false)
		c.command("nod", nil)
		a.sprite(r, map[string]any{"id": "food", "scale": 0.55})
	})
	a.later(r, 1150*time.Millisecond, func() { a.sprite(r, map[string]any{"id": "food", "scale": 0.3}) })
	a.later(r, 1500*time.Millisecond, func() {
		c.command("sprite_hide", map[string]any{"id": "food"})
		a.sayText(r, line, 3)
	})
}

// Head touches: a tiny light touch tickles, a hand resting on the head is a long
// cuddle, several strokes in a row are scratching, anything else a cuddle. The pet
// reacts a moment later: holding a food card to the robot touches its head too, and
// the card (read a moment later) must win.
const (
	tickleMs     = 300  // shorter and light (no zone above tickleZone): a tickle
	tickleZone   = 1    // head zone intensity (0-3)
	longTouchMs  = 1500 // held this long: a long cuddle, while the hand still rests
	scratchWin   = 2500 * time.Millisecond
	scratchCount = 2 // strokes within scratchWin
	touchWait    = 500 * time.Millisecond
)

func (a *App) touchStart(r *robot, now time.Time, zones [3]float64) {
	a.log.Info("head touch", "robot", r.id, "zones", zones)
	r.touchAt, r.touchZone, r.touchSeen = now, max(zones[0], zones[1], zones[2]), false
	r.touchID++
	id := r.touchID
	time.AfterFunc(longTouchMs*time.Millisecond, func() { // still touching: the hand rests
		a.mu.Lock()
		defer a.mu.Unlock()
		if r.touchID == id && !r.touchSeen && !r.touchAt.IsZero() {
			r.touchSeen = true
			a.touchSoon(r, a.now(), pet.TouchLong, 0)
		}
	})
}

func (a *App) touchEnd(r *robot, now time.Time, ms float64) {
	if r.touchAt.IsZero() || r.touchSeen {
		r.touchAt = time.Time{}
		return
	}
	r.touchSeen = true
	kind := pet.TouchCuddle
	switch {
	case ms < tickleMs && r.touchZone <= tickleZone:
		kind = pet.TouchTickle
	case ms >= longTouchMs:
		kind = pet.TouchLong
	}
	r.touchAt = time.Time{}
	a.touchSoon(r, now, kind, touchWait)
}

// stroke: a head swipe. Several in a row are scratching (it replaces the plain touch).
func (a *App) stroke(r *robot, now time.Time) {
	recent := r.strokes[:0]
	for _, t := range r.strokes {
		if now.Sub(t) < scratchWin {
			recent = append(recent, t)
		}
	}
	r.strokes = append(recent, now)
	if len(r.strokes) >= scratchCount {
		r.strokes = nil
		r.touchSeen = true // this touch is the scratching
		a.touchSoon(r, now, pet.TouchScratch, touchWait)
		return
	}
	a.touchSoon(r, now, pet.TouchCuddle, touchWait)
}

// touchSoon reacts to a touch after wait, unless a food card came (or a newer touch).
func (a *App) touchSoon(r *robot, now time.Time, kind string, wait time.Duration) {
	if now.Sub(r.lastCard) < cardQuiet {
		return
	}
	if kind != pet.TouchScratch && now.Sub(r.lastScratch) < scratchWin {
		return // the release after scratching is not a new cuddle
	}
	if kind == pet.TouchScratch {
		r.lastScratch = now
	}
	r.cuddleGen++
	gen := r.cuddleGen
	time.AfterFunc(wait, func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		now := a.now()
		if r.cuddleGen != gen || r.conn == nil || r.game != nil || now.Sub(r.lastCard) < cardQuiet {
			return
		}
		re := r.pet.Touch(now, kind)
		a.dirty = true
		a.react(r, re, now)
		a.publishReaction(r, re)
		a.publishState(r)
	})
}

// telemetry keeps the robot's last telemetry (sent every 2 s) and its head angles.
func (a *App) telemetry(id string, m map[string]float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.robots[id]
	if r == nil {
		return
	}
	r.telemetry = m
	if r.hold.imuSample(m) {
		a.setHeld(r)
	}
	yaw, okYaw := m["head_yaw_deg"]
	pitch, okPitch := m["head_pitch_deg"]
	if okYaw && okPitch {
		r.head = &[2]float64{yaw, pitch}
		a.liftHead(r, a.now(), yaw, pitch)
	}
}

// The firmware lets go of the servos when the head rests (no torque), so the head
// slowly sinks under its weight. By day the pet lifts it back to where it last put it.
const (
	headRestPitch = 25               // where the head rests by day, when the pet set nothing else
	headSag       = 8                // degrees below that count as sunk
	headYawDrift  = 30               // turned further than this (degrees): back to the middle
	headLiftEvery = 20 * time.Second // at most this often (a hand may hold it down)
)

func (a *App) liftHead(r *robot, now time.Time, yaw, pitch float64) {
	want := r.headWant
	if want == 0 {
		want = headRestPitch
	}
	p := r.pet
	if r.conn == nil || r.game != nil || r.menu != "" || now.Before(r.busyUntil) || p.Phase(now) == pet.Night ||
		p.Napping(now) || (pitch >= want-headSag && math.Abs(yaw) <= headYawDrift) || now.Sub(r.lastLift) < headLiftEvery {
		return // asleep the head may droop: that is the sleeping pose
	}
	r.lastLift = now
	toYaw := math.Round(yaw)
	if math.Abs(yaw) > headYawDrift {
		toYaw = 0
	}
	r.conn.command("look", map[string]any{"yaw": toYaw, "pitch": want})
	a.log.Info("head back to its rest", "robot", r.id, "pitch", pitch, "yaw", yaw, "to_pitch", want, "to_yaw", toYaw)
}

// rememberHead notes where the head is before the pet moves it (a game, a dance).
func (a *App) rememberHead(r *robot) {
	if r.head != nil {
		h := *r.head
		r.headBefore = &h
	}
}

// restoreHead turns the head back to where it was; unknown: straight ahead, a little up.
// (Not "home": that is pitch 0, the head looking down.)
func (a *App) restoreHead(r *robot) {
	yaw, pitch := 0.0, float64(headMidPitch)
	if h := r.headBefore; h != nil {
		yaw, pitch = h[0], max(h[1], 5) // "look" takes pitch 5..85
	}
	r.headBefore = nil
	r.headWant = math.Round(pitch)
	r.conn.command("look", map[string]any{"yaw": math.Round(yaw), "pitch": math.Round(pitch)})
}

// The night light fades out: full at bedtime (or the last good night), off after
// nightLightFade, in steps of a tenth.
const nightLightFade = 10 * time.Minute

// nightLevel is the night light's brightness now, 0..1.
func (a *App) nightLevel(r *robot, now time.Time) float64 {
	s := r.pet.Settings
	if !s.NightLight {
		return 0
	}
	since := time.Duration((1440-s.MinutesToBed(now))%1440) * time.Minute // since the scheduled bedtime
	if !r.sleptAt.IsZero() && now.Sub(r.sleptAt) < since {
		since = now.Sub(r.sleptAt)
	}
	level := 1 - float64(since)/float64(nightLightFade)
	return max(0, math.Ceil(level*10)/10)
}

// nightLEDs: the night light at its level now, or off.
func (a *App) nightLEDs(r *robot, now time.Time) map[string]any {
	level := a.nightLevel(r, now)
	r.ledLevel = level
	if level <= 0 {
		return map[string]any{"effect": "off"}
	}
	c := fmt.Sprintf("#%02x%02x00", int(math.Round(0x18*level)), int(math.Round(0x06*level)))
	return map[string]any{"left": c, "right": c}
}

// nightFade updates the night light when its level changed (from the Tick, at night).
func (a *App) nightFade(r *robot, now time.Time) {
	if level := a.nightLevel(r, now); level != r.ledLevel {
		r.conn.command("leds", a.nightLEDs(r, now))
	}
	if !r.screenOff && !r.screenOnAt.IsZero() && now.Sub(r.screenOnAt) >= nightScreenOn {
		a.sleepScreen(r)
	}
}

// demoCheck: in demo mode a need at 90% drops back to 10%. The robot shows a reset
// picture (the need's icon, a refresh badge, its bar at 10%), then says so.
func (a *App) demoCheck(r *robot, now time.Time) bool {
	reset := r.pet.DemoReset(now)
	if len(reset) == 0 {
		return false
	}
	a.dirty = true
	need := reset[0]
	a.begin(r, now, 7*time.Second)
	a.showPicture(r, robotpic.DemoReset(need))
	a.later(r, a.timing.demoPicture, func() {
		r.conn.command("face", nil)
		r.pictureOn = false
		a.emotion(r, "doubt")
		a.say(r, "demo_"+need, "", 4)
	})
	a.publishReaction(r, pet.Reaction{Kind: pet.KindDemoReset, Need: need, Changed: true})
	a.publishState(r)
	return true
}

// Pulse (every 2 s) keeps open pages live (the bars move, e.g. during a nap) and
// wakes a pet whose nap just ended, without waiting for the Tick.
func (a *App) Pulse() {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for _, r := range a.robots {
		p := r.pet
		p.Advance(now)
		if r.conn != nil && r.shownMood == pet.Napping && !p.Napping(now) && now.After(r.busyUntil) &&
			p.Phase(now) != pet.Night {
			r.shownMood = p.Mood(now) // woken: no second wake before the mood is shown again
			re := pet.Reaction{Kind: pet.KindWake, Changed: true}
			a.react(r, re, now)
			a.publishReaction(r, re)
		} else if r.conn != nil && now.After(r.busyUntil) && r.game == nil && r.menu == "" && p.Phase(now) == pet.Awake {
			a.demoCheck(r, now) // demo mode: a full need drops back within seconds
		}
		a.watchdog(r, now)
		a.idleScreen(r, now)
		if (r.shownMood == pet.Napping || r.shownMood == pet.Sleeping) && r.conn != nil && now.After(r.busyUntil) &&
			r.menu == "" && r.game == nil {
			a.energyBar(r) // energy comes back while it sleeps
		}
		if a.watched(r.id) {
			a.publishState(r)
		}
	}
}

// watched: a page paired with the robot is open (its event stream).
func (a *App) watched(robotID string) bool {
	for sub := range a.subs {
		if s := a.sessions[sub.sid]; s != nil && slices.Contains(s.Robots, robotID) {
			return true
		}
	}
	return false
}

// School hours: the pet rests with the screen off, and whatever the kid does, it only
// says so (the screen lights for the line, then goes dark again).
const (
	schoolLine  = 6 * time.Second // the screen stays lit this long for the line
	schoolQuiet = 4 * time.Second // touches this soon after the line get no new one
)

func (a *App) schoolStart(r *robot, now time.Time) {
	a.stopGame(r)
	if r.menu != "" {
		a.closeMenu(r)
	}
	r.gen++
	r.busyUntil = time.Time{}
	a.schoolScreen(r)
}

// schoolScreen: no pictures, LEDs off, the screen off.
func (a *App) schoolScreen(r *robot) {
	a.clearSprites(r)
	if r.pictureOn {
		r.conn.command("face", nil)
		r.pictureOn = false
	}
	a.emotion(r, "sleepy")
	r.conn.command("leds", map[string]any{"left": "#000000", "right": "#000000"})
	r.conn.command("screensaver", map[string]any{"on": true})
	r.screenOff = true
	r.shownMood = r.pet.Mood(a.now())
}

func (a *App) schoolRest(r *robot, now time.Time) {
	if now.Sub(r.lastSchool) < schoolQuiet {
		return
	}
	r.lastSchool = now
	r.gen++
	r.busyUntil = now.Add(schoolLine)
	if r.screenOff {
		r.conn.command("screensaver", map[string]any{"on": false})
		r.screenOff = false
	}
	a.emotion(r, "sleepy")
	a.say(r, "school", "", 4)
	a.later(r, schoolLine, func() { a.schoolScreen(r) })
}

// gentle: a long press on the screen, which is the pet's face.
func (a *App) gentle(r *robot, now time.Time) {
	a.begin(r, now, reactionTime)
	a.emotion(r, "doubt")
	a.say(r, "long_press", "", 4)
}

// setHeld passes the hold state to the connection (which drops head commands while held).
func (a *App) setHeld(r *robot) {
	a.log.Info("robot held", "robot", r.id, "held", r.hold.held)
	if r.conn != nil {
		r.conn.held.Store(r.hold.held)
	}
}

// idleScreen turns the robot's screen off by day when nobody has played for ScreenIdleMin
// minutes (the robot's own screensaver never starts: the pet keeps sending it commands). A touch
// on the robot lights it again (the robot does that itself), and so does play from a phone.
func (a *App) idleScreen(r *robot, now time.Time) {
	mins := r.pet.Settings.ScreenIdleMin
	if mins <= 0 || r.conn == nil || r.screenOff || r.game != nil || r.menu != "" || now.Before(r.busyUntil) ||
		r.pet.Phase(now) != pet.Awake {
		return
	}
	if r.lastPlay.IsZero() {
		r.lastPlay = now
	}
	if now.Sub(r.lastPlay) < time.Duration(mins)*time.Minute {
		return
	}
	r.conn.command("screensaver", map[string]any{"on": true})
	r.screenOff, r.idleOff = true, true
	a.log.Info("screen off: nobody played", "robot", r.id, "minutes", mins)
}
