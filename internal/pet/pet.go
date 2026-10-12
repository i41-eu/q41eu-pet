// Package pet is the Tamagotchi model: three needs (food, fun, energy), the
// family's daily schedule and the kid's actions. It does no I/O: the caller
// passes the time and turns reactions into robot commands.
//
// The pet is kind: it never dies or gets ill. Neglected it only gets sad, and
// its needs pause while the kid sleeps or is at school.
package pet

import (
	"fmt"
	"math"
	"slices"
	"time"
)

// Stats are the needs, 0 (empty) to 100 (full).
type Stats struct {
	Food   float64 `json:"food"`
	Fun    float64 `json:"fun"`
	Energy float64 `json:"energy"`
}

// Phase is the part of the day the schedule says it is.
type Phase string

const (
	Awake  Phase = "awake"
	Night  Phase = "night"
	School Phase = "school" // needs paused; the pet rests and only says so (no school setting in holidays)
)

// Mood is what the pet shows, from the phase and its needs.
type Mood string

const (
	Happy    Mood = "happy"
	OK       Mood = "ok"
	Hungry   Mood = "hungry"
	Bored    Mood = "bored"
	Tired    Mood = "tired"
	Napping  Mood = "napping"
	Sleeping Mood = "sleeping" // night
)

// Entry is one line of the parent's activity log.
type Entry struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`             // feed, cuddle, play, nap, morning, night, ...
	Detail string    `json:"detail,omitempty"` // e.g. the food
}

const keepLog = 300

// Pet is one robot's pet. Stats are current as of Updated; call Advance first.
type Pet struct {
	Settings Settings  `json:"settings"`
	Stats    Stats     `json:"stats"`
	Born     time.Time `json:"born"`
	Updated  time.Time `json:"updated"`
	NapUntil time.Time `json:"nap_until,omitzero"`
	// A demo nap follows a curve from where it started (napCurve).
	NapFrom       time.Time `json:"nap_from,omitzero"`
	NapFromEnergy float64   `json:"nap_from_energy,omitempty"`
	// NightWakeUntil: woken at night by a hard press, awake until then (needs stay paused).
	NightWakeUntil time.Time `json:"night_wake_until,omitzero"`

	DreamCards  int       `json:"dream_cards,omitempty"` // food cards in this sleep (DreamFood)
	DreamCardAt time.Time `json:"dream_card_at,omitzero"`
	LastFood    string    `json:"last_food,omitempty"` // for picky eating (Feed)
	LastFoodAt  time.Time `json:"last_food_at,omitzero"`
	FoodRepeats int       `json:"food_repeats,omitempty"`

	PlayDay    string     `json:"play_day"` // the local date PlayMin counts, "2006-01-02"
	PlayMin    float64    `json:"play_min"` // play and cuddle time on PlayDay
	LastPlayAt time.Time  `json:"last_play_at,omitzero"`
	ColorsTop  []TopEntry `json:"colors_top,omitempty"` // the color game's 3 best full games, fastest first
	LastCuddle time.Time  `json:"last_cuddle,omitzero"`

	Log []Entry `json:"log"`
}

// New is a fresh pet: fed and rested, a bit bored.
func New(now time.Time, s Settings) *Pet {
	s.Normalize()
	return &Pet{
		Settings: s,
		Stats:    Stats{Food: 80, Fun: 60, Energy: 90},
		Born:     now,
		Updated:  now,
	}
}

// rates are points per hour.
type rates struct{ food, fun, energy, nightEnergy, napEnergy float64 }

func (s *Settings) rates() rates {
	r := rates{food: 12, fun: 15, energy: 7, nightEnergy: 25, napEnergy: 60}
	f := map[string]float64{"easy": 0.5, "normal": 1, "hard": 1.5}[s.Difficulty]
	if f == 0 {
		f = 1
	}
	r.food *= f
	r.fun *= f
	r.energy *= f
	if s.Demo { // a demo nap refills energy in a couple of minutes
		r.napEnergy *= 40
	}
	return r
}

// maxCatchUp limits how much time Advance simulates after a long pause (server down).
const maxCatchUp = 72 * time.Hour

// Advance moves the needs to now, minute by minute, following the schedule.
func (p *Pet) Advance(now time.Time) {
	if p.Updated.IsZero() || !now.After(p.Updated) {
		if p.Updated.IsZero() {
			p.Updated = now
		}
		return
	}
	from := p.Updated
	if now.Sub(from) > maxCatchUp {
		from = now.Add(-maxCatchUp)
	}
	r := p.Settings.rates()
	for t := from; t.Before(now); {
		step := min(time.Minute, now.Sub(t))
		h := step.Hours()
		switch p.Settings.PhaseAt(t) {
		case Night:
			p.Stats.Energy += r.nightEnergy * h
		case School:
			// waiting for the kid
		default:
			if t.Before(p.NapUntil) {
				p.Stats.Energy += r.napEnergy * h
				p.Stats.Food -= r.food * h / 2
			} else {
				p.Stats.Food -= r.food * h
				p.Stats.Fun -= r.fun * h
				p.Stats.Energy -= r.energy * h
			}
		}
		t = t.Add(step)
	}
	if p.Settings.Demo && !p.NapFrom.IsZero() { // a demo nap: quick at first, then up to 75%
		end := now
		if !p.NapUntil.IsZero() && p.NapUntil.Before(end) {
			end = p.NapUntil
		}
		p.Stats.Energy = max(p.Stats.Energy, napCurve(p.NapFromEnergy, end.Sub(p.NapFrom)))
	}
	p.clamp()
	p.Updated = now
	if !p.NapUntil.IsZero() && !now.Before(p.NapUntil) {
		p.NapUntil, p.NapFrom = time.Time{}, time.Time{}
	}
}

// Demo nap: +20% in the first 5 s (to see it work), then up to 75% at the end (1 minute),
// when it wakes up. The next nap passes 90% within seconds: the demo reset (DemoReset).
const (
	demoNapQuick     = 5 * time.Second
	demoNapQuickGain = 20
	demoNapTarget    = 75
)

// napCurve is the energy after a demo nap of length d that started at from.
func napCurve(from float64, d time.Duration) float64 {
	quick := from + demoNapQuickGain
	if d <= demoNapQuick {
		return from + demoNapQuickGain*d.Seconds()/demoNapQuick.Seconds()
	}
	target := max(float64(demoNapTarget), quick)
	rest := (DemoNapLength - demoNapQuick).Seconds()
	return quick + (target-quick)*min(1, (d-demoNapQuick).Seconds()/rest)
}

func (p *Pet) clamp() {
	c := func(v float64) float64 { return math.Round(max(0, min(100, v))*100) / 100 }
	p.Stats.Food, p.Stats.Fun, p.Stats.Energy = c(p.Stats.Food), c(p.Stats.Fun), c(p.Stats.Energy)
}

// Phase is the schedule's phase, except that a pet woken at night is awake for a while.
func (p *Pet) Phase(now time.Time) Phase {
	ph := p.Settings.PhaseAt(now)
	if ph == Night && now.Before(p.NightWakeUntil) {
		return Awake
	}
	return ph
}

// WakeAtNight wakes the sleeping pet for Settings.NightWakeMin minutes (a hard press).
func (p *Pet) WakeAtNight(now time.Time) Reaction {
	p.Advance(now)
	switch {
	case p.Settings.PhaseAt(now) != Night:
		return Reaction{Kind: KindWake}
	case p.Settings.NightWakeMin <= 0:
		return Reaction{Kind: KindAsleep}
	case now.Before(p.NightWakeUntil):
		return Reaction{Kind: KindNightWake}
	}
	p.NightWakeUntil = now.Add(time.Duration(p.Settings.NightWakeMin) * time.Minute)
	p.log(now, KindNightWake, "")
	return Reaction{Kind: KindNightWake, Changed: true}
}

// Napping reports whether the pet takes a daytime nap at now.
func (p *Pet) Napping(now time.Time) bool { return now.Before(p.NapUntil) }

// Mood at now (after Advance).
func (p *Pet) Mood(now time.Time) Mood {
	switch {
	case p.Phase(now) == Night:
		return Sleeping
	case p.Napping(now):
		return Napping
	case p.Stats.Food < 25:
		return Hungry
	case p.Stats.Energy < 20:
		return Tired
	case p.Stats.Fun < 25:
		return Bored
	case min(p.Stats.Food, p.Stats.Fun, p.Stats.Energy) >= 60:
		return Happy
	}
	return OK
}

// AgeDays counts the nights since the pet was born (local dates).
func (p *Pet) AgeDays(now time.Time) int {
	b := p.Born.In(now.Location())
	d0 := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, now.Location())
	d1 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return int(math.Round(d1.Sub(d0).Hours() / 24))
}

// PlayLeft is how many minutes of play are left today, or -1 without a limit.
func (p *Pet) PlayLeft(now time.Time) float64 {
	if p.Settings.PlayLimitMin <= 0 {
		return -1
	}
	used := p.PlayMin
	if p.PlayDay != now.Format(time.DateOnly) {
		used = 0
	}
	return max(0, float64(p.Settings.PlayLimitMin)-used)
}

// PlayedToday is the play and cuddle time today in minutes.
func (p *Pet) PlayedToday(now time.Time) float64 {
	if p.PlayDay != now.Format(time.DateOnly) {
		return 0
	}
	return p.PlayMin
}

// countPlay adds the time since the previous interaction, capped: separate
// taps a few seconds apart add up to how long the kid actually played.
func (p *Pet) countPlay(now time.Time) {
	if day := now.Format(time.DateOnly); p.PlayDay != day {
		p.PlayDay, p.PlayMin = day, 0
	}
	d := now.Sub(p.LastPlayAt)
	if p.LastPlayAt.IsZero() || d < 0 || d > time.Minute {
		d = 15 * time.Second
	}
	p.PlayMin += d.Minutes()
	p.LastPlayAt = now
}

func (p *Pet) log(now time.Time, kind, detail string) {
	p.Log = append(p.Log, Entry{At: now, Kind: kind, Detail: detail})
	if len(p.Log) > keepLog {
		p.Log = p.Log[len(p.Log)-keepLog:]
	}
}

// Note adds a line to the log (the caller's events: morning, night, ...).
func (p *Pet) Note(now time.Time, kind, detail string) { p.log(now, kind, detail) }

// Reaction is what an action did; the caller shows it on the robot and the page.
type Reaction struct {
	Kind    string `json:"kind"`             // see the Kind* constants
	Food    string `json:"food,omitempty"`   // for KindEat
	Hits    int    `json:"hits,omitempty"`   // for KindGameOver
	Ms      int64  `json:"ms,omitempty"`     // for KindColorsOver: the time over all rounds
	Record  bool   `json:"record,omitempty"` // for KindColorsOver: the best time so far
	Place   int    `json:"place,omitempty"`  // for KindColorsOver: 1..3 on the leaderboard, 0 = not on it
	Touch   string `json:"touch,omitempty"`  // for KindCuddle: tickle, cuddle, long, scratch
	Need    string `json:"need,omitempty"`   // for KindDemoReset: food, fun or energy
	Changed bool   `json:"changed"`          // the needs changed
}

const (
	KindEat        = "eat"
	KindFull       = "full"      // not hungry: refuses food
	KindCuddle     = "cuddle"    // head stroke or the cuddle button
	KindPlay       = "play"      // a game
	KindTooTired   = "too_tired" // no energy to play
	KindLimit      = "limit"     // today's play time is used up
	KindNap        = "nap"
	KindNotTired   = "not_tired"   // refuses a nap
	KindWake       = "wake"        // woken from a nap
	KindAsleep     = "asleep"      // it is night: only a sleepy answer (a dream on the robot)
	KindNightWake  = "night_wake"  // woken at night by a hard press
	KindDemoReset  = "demo_reset"  // demo mode set a full need back to 10%
	KindSchool     = "school"      // school hours: the pet rests and says only that
	KindColorsOver = "colors_over" // the color game ended; Hits rounds done, Ms the time
	KindPicky      = "picky"       // the same food again: "Nemáš něco jiného?"
	KindEatAgain   = "eat_again"   // the same food a third time: eaten after all, for 80%
	KindDreamFood  = "dream_food"  // a food card while asleep: a dream of it (Changed: a sleep boost)
	KindShake      = "shake"       // the robot was shaken
	KindGame       = "game"        // a game of catch starts on the robot
	KindGameOver   = "game_over"   // the game ended; Hits balls caught
)

// awake is the common start of an action: needs up to date, night answers
// sleepily, a nap ends.
func (p *Pet) awake(now time.Time) (Reaction, bool) {
	p.Advance(now)
	switch p.Phase(now) {
	case Night:
		return Reaction{Kind: KindAsleep}, false
	case School:
		return Reaction{Kind: KindSchool}, false
	}
	if p.Napping(now) {
		p.NapUntil = time.Time{}
	}
	return Reaction{}, true
}

// Feed gives the pet a food (see Foods); unknown foods count as an apple.
func (p *Pet) Feed(now time.Time, food string) Reaction {
	if r, ok := p.awake(now); !ok {
		return r
	}
	f, ok := Foods[food]
	if !ok {
		food, f = "apple", Foods["apple"]
	}
	if p.Stats.Food >= 90 {
		p.log(now, KindFull, food)
		return Reaction{Kind: KindFull, Food: food}
	}
	// The same food again soon: the second time it is refused ("something else?"),
	// the third time eaten after all, for 80% of its value.
	if food == p.LastFood && now.Sub(p.LastFoodAt) < pickyWindow {
		p.FoodRepeats++
	} else {
		p.FoodRepeats = 0
	}
	p.LastFood, p.LastFoodAt = food, now
	kind, share := KindEat, 1.0
	switch {
	case p.FoodRepeats == 1:
		p.log(now, KindPicky, food)
		return Reaction{Kind: KindPicky, Food: food}
	case p.FoodRepeats >= 2:
		kind, share = KindEatAgain, pickyShare
		p.FoodRepeats = 0 // the next one of it is refused again
	}
	p.Stats.Food += f.Food * share
	p.Stats.Fun += f.Fun * share
	p.Stats.Energy += f.Energy * share
	p.clamp()
	p.log(now, "feed", food)
	return Reaction{Kind: kind, Food: food, Changed: true}
}

const (
	pickyWindow = 30 * time.Minute // the same food within this counts as "again"
	pickyShare  = 0.8              // what the same food is worth the third time
)

const cuddleEvery = 4 * time.Second

// Cuddle is a head stroke. Stroking without a break raises fun only every few seconds.
func (p *Pet) Cuddle(now time.Time) Reaction {
	return p.Touch(now, TouchCuddle)
}

// Kinds of touch on the head (the robot's head sensor), each worth some fun.
const (
	TouchTickle  = "tickle"  // a tiny, light touch
	TouchCuddle  = "cuddle"  // a normal touch or a stroke
	TouchLong    = "long"    // a hand resting on the head
	TouchScratch = "scratch" // several strokes in a row
)

var touchFun = map[string]float64{TouchTickle: 3, TouchCuddle: 6, TouchLong: 8, TouchScratch: 10}

// Touch is a cuddle of some kind (see Touch*). Touching without a break raises fun only
// every few seconds.
func (p *Pet) Touch(now time.Time, kind string) Reaction {
	if r, ok := p.awake(now); !ok {
		return r
	}
	if p.PlayLeft(now) == 0 {
		return Reaction{Kind: KindLimit}
	}
	fun, ok := touchFun[kind]
	if !ok {
		kind, fun = TouchCuddle, touchFun[TouchCuddle]
	}
	p.countPlay(now)
	if now.Sub(p.LastCuddle) < cuddleEvery {
		return Reaction{Kind: KindCuddle, Touch: kind}
	}
	p.LastCuddle = now
	p.Stats.Fun += fun
	p.clamp()
	detail := ""
	if kind != TouchCuddle {
		detail = kind
	}
	p.log(now, "cuddle", detail)
	return Reaction{Kind: KindCuddle, Touch: kind, Changed: true}
}

// Play is a game: fun for energy and a little food.
func (p *Pet) Play(now time.Time) Reaction {
	if r, ok := p.awake(now); !ok {
		return r
	}
	if p.PlayLeft(now) == 0 {
		return Reaction{Kind: KindLimit}
	}
	if p.Stats.Energy < 15 {
		return Reaction{Kind: KindTooTired}
	}
	p.countPlay(now)
	p.Stats.Fun += 20
	p.Stats.Energy -= 8
	p.Stats.Food -= 4
	p.clamp()
	p.log(now, "play", "")
	return Reaction{Kind: KindPlay, Changed: true}
}

// GameRounds is the number of balls in one game of catch.
const GameRounds = 5

// StartGame checks the pet can play a game of catch (the robot runs it); the
// reward comes with FinishGame.
func (p *Pet) StartGame(now time.Time) Reaction {
	if r, ok := p.awake(now); !ok {
		return r
	}
	if p.PlayLeft(now) == 0 {
		return Reaction{Kind: KindLimit}
	}
	if p.Stats.Energy < 15 {
		return Reaction{Kind: KindTooTired}
	}
	p.countPlay(now)
	return Reaction{Kind: KindGame}
}

// Caught counts a caught ball as play time (a game takes about half a minute).
func (p *Pet) Caught(now time.Time) { p.countPlay(now) }

// FinishGame ends a game of catch: more fun for more catches, playing makes
// hungry and tired as Play does.
func (p *Pet) FinishGame(now time.Time, hits int) Reaction {
	p.Advance(now)
	hits = max(0, min(GameRounds, hits))
	p.countPlay(now)
	p.reward(hits)
	p.log(now, "play", fmt.Sprintf("%d/%d", hits, GameRounds))
	return Reaction{Kind: KindGameOver, Hits: hits, Changed: true}
}

// reward is a game's effect: more fun for more rounds won; playing makes hungry and tired.
func (p *Pet) reward(won int) {
	p.Stats.Fun += 8 + 4*float64(won)
	p.Stats.Energy -= 8
	p.Stats.Food -= 4
	p.clamp()
}

// TopEntry is a place on the color game's leaderboard.
type TopEntry struct {
	Ms    int64     `json:"ms"`
	At    time.Time `json:"at"`
	Photo string    `json:"photo,omitempty"` // the player's photo (a file the app keeps), "" = none
}

// TopPlaces is the leaderboard's length.
const TopPlaces = 3

// FinishColors ends the color game: done of rounds, in total time (the rounds not
// done count with their full time). A game with every round done can get on the
// leaderboard (Place); the fastest is a record. The fun is as for catch, by the
// share done.
func (p *Pet) FinishColors(now time.Time, done, rounds int, total time.Duration) Reaction {
	p.Advance(now)
	done = max(0, min(rounds, done))
	p.countPlay(now)
	p.reward(done * GameRounds / max(1, rounds))
	re := Reaction{Kind: KindColorsOver, Hits: done, Ms: total.Milliseconds(), Changed: true}
	if done == rounds {
		i := 0
		for i < len(p.ColorsTop) && p.ColorsTop[i].Ms <= re.Ms {
			i++
		}
		if i < TopPlaces {
			re.Place = i + 1
			re.Record = i == 0 && len(p.ColorsTop) > 0 // the first game is not a record yet
			p.ColorsTop = slices.Insert(p.ColorsTop, i, TopEntry{Ms: re.Ms, At: now})
			p.ColorsTop = p.ColorsTop[:min(len(p.ColorsTop), TopPlaces)]
		}
	}
	p.log(now, "colors", fmt.Sprintf("%d/%d %.1f s", done, rounds, total.Seconds()))
	return re
}

// NapLength is how long a daytime nap lasts unless the kid wakes the pet; DemoNapLength
// in demo mode.
const (
	NapLength     = 15 * time.Minute
	DemoNapLength = time.Minute
)

// Nap puts the pet to sleep for NapLength during the day.
func (p *Pet) Nap(now time.Time) Reaction {
	p.Advance(now)
	switch {
	case p.Phase(now) == Night:
		return Reaction{Kind: KindAsleep}
	case p.Phase(now) == School:
		return Reaction{Kind: KindSchool}
	case p.Napping(now):
		return Reaction{Kind: KindNap}
	case p.Stats.Energy >= DemoHigh: // napping is fine anywhere from 10 to 90
		return Reaction{Kind: KindNotTired}
	}
	length := NapLength
	if p.Settings.Demo {
		length = DemoNapLength
		p.NapFrom, p.NapFromEnergy = now, p.Stats.Energy
	}
	p.NapUntil = now.Add(length)
	p.log(now, "nap", "")
	return Reaction{Kind: KindNap, Changed: true}
}

// Wake ends a nap early.
func (p *Pet) Wake(now time.Time) Reaction {
	p.Advance(now)
	if !p.Napping(now) {
		return Reaction{Kind: KindWake}
	}
	p.NapUntil, p.NapFrom = time.Time{}, time.Time{}
	return Reaction{Kind: KindWake, Changed: true}
}

// Shake: the robot was shaken. A little fun, no play time.
func (p *Pet) Shake(now time.Time) Reaction {
	if r, ok := p.awake(now); !ok {
		return r
	}
	p.Stats.Fun += 2
	p.clamp()
	return Reaction{Kind: KindShake, Changed: true}
}

// Food is what one serving adds. Only sleep gives energy (Energy stays 0 for foods).
type Food struct {
	Food, Fun, Energy float64
}

// Foods by key; FoodOrder is the order on the kid's page.
var Foods = map[string]Food{
	"apple":  {Food: 20, Fun: 2},
	"carrot": {Food: 20, Fun: 2},
	"banana": {Food: 25},
	"bread":  {Food: 30},
	"milk":   {Food: 20},
	"cake":   {Food: 15, Fun: 15},
}

var FoodOrder = []string{"apple", "carrot", "banana", "bread", "milk", "cake"}

// Demo mode thresholds: a need this full drops back to demoLow, so the pet always needs something.
const (
	DemoHigh = 90
	DemoLow  = 10
)

// Needs are the names of the needs, in the order DemoReset checks them.
var Needs = []string{"food", "fun", "energy"}

// DemoReset (demo mode) sets every need at DemoHigh or more back to DemoLow and returns
// which ones it reset (nil when demo mode is off or none was that full).
func (p *Pet) DemoReset(now time.Time) []string {
	if !p.Settings.Demo {
		return nil
	}
	p.Advance(now)
	if p.Napping(now) {
		if p.Stats.Energy < DemoHigh {
			return nil // let it sleep on
		}
		p.NapUntil, p.NapFrom = time.Time{}, time.Time{} // full: the nap ends with the reset
	}
	var reset []string
	for _, need := range Needs {
		v := p.need(need)
		if *v >= DemoHigh {
			*v = DemoLow
			reset = append(reset, need)
			p.log(now, "demo_reset", need)
		}
	}
	return reset
}

func (p *Pet) need(name string) *float64 {
	switch name {
	case "food":
		return &p.Stats.Food
	case "fun":
		return &p.Stats.Fun
	}
	return &p.Stats.Energy
}

// Asleep reports whether the pet sleeps at now: the night or a nap.
func (p *Pet) Asleep(now time.Time) bool { return p.Phase(now) == Night || p.Napping(now) }

// dreamBoost is the energy a second (third, ...) food card in one sleep adds.
const dreamBoost = 15

// DreamFood is a food card while the pet sleeps: it dreams of the food instead of
// eating it. From the second card in the same sleep on, each one helps it sleep
// (+15% energy). Cards more than 30 minutes apart count as a new sleep.
func (p *Pet) DreamFood(now time.Time, food string) Reaction {
	p.Advance(now)
	if _, ok := Foods[food]; !ok {
		food = "apple"
	}
	if now.Sub(p.DreamCardAt) > 30*time.Minute {
		p.DreamCards = 0
	}
	p.DreamCards++
	p.DreamCardAt = now
	re := Reaction{Kind: KindDreamFood, Food: food}
	if p.DreamCards >= 2 {
		p.Stats.Energy += dreamBoost
		p.clamp()
		re.Changed = true
	}
	p.log(now, KindDreamFood, food)
	return re
}
