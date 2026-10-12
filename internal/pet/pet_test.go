package pet

import (
	"encoding/json"
	"math"
	"slices"
	"testing"
	"time"
)

var prague = mustLoc("Europe/Prague")

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

// at is a time on Wednesday 2026-09-30 (a weekday) in Prague, or dayOffset days later.
func at(dayOffset, h, m int) time.Time {
	return time.Date(2026, 9, 30+dayOffset, h, m, 0, 0, prague)
}

func TestPhase(t *testing.T) {
	s := DefaultSettings() // weekday 07:00-20:00, weekend 08:00-20:30
	s.School = true        // 08:00-15:00
	cases := []struct {
		t    time.Time
		want Phase
	}{
		{at(0, 6, 59), Night},
		{at(0, 7, 0), Awake},
		{at(0, 8, 0), School},
		{at(0, 14, 59), School},
		{at(0, 15, 0), Awake},
		{at(0, 19, 59), Awake},
		{at(0, 20, 0), Night},
		{at(3, 7, 30), Night},  // Saturday: wake at 08:00, no school
		{at(3, 9, 0), Awake},   // Saturday
		{at(3, 20, 15), Awake}, // Saturday: bed at 20:30
	}
	for _, c := range cases {
		if got := s.PhaseAt(c.t); got != c.want {
			t.Errorf("%s: phase %s, want %s", c.t.Format("Mon 15:04"), got, c.want)
		}
	}

	s.Weekday.Bed = "00:30" // after midnight
	if got := s.PhaseAt(at(0, 23, 0)); got != Awake {
		t.Errorf("late bedtime 23:00: %s, want awake", got)
	}
	if got := s.PhaseAt(at(0, 1, 0)); got != Night {
		t.Errorf("late bedtime 01:00: %s, want night", got)
	}
}

func TestAdvanceFollowsSchedule(t *testing.T) {
	s := DefaultSettings()
	s.School = true
	p := New(at(0, 7, 0), s)
	p.Stats = Stats{Food: 100, Fun: 100, Energy: 50}

	p.Advance(at(0, 8, 0)) // one awake hour
	if p.Stats.Food != 88 || p.Stats.Fun != 85 || p.Stats.Energy != 43 {
		t.Fatalf("after an awake hour: %+v", p.Stats)
	}
	p.Advance(at(0, 15, 0)) // school: paused
	if p.Stats.Food != 88 || p.Stats.Fun != 85 {
		t.Fatalf("school should pause needs: %+v", p.Stats)
	}
	p.Advance(at(1, 6, 0)) // 15:00-20:00 awake, then night: energy comes back, food and fun wait
	if p.Stats.Food != 28 || p.Stats.Fun != 10 || p.Stats.Energy != 100 {
		t.Fatalf("after an evening and a night: %+v", p.Stats)
	}
	if m := p.Mood(at(1, 6, 0)); m != Sleeping {
		t.Fatalf("mood at night: %s", m)
	}
	if m := p.Mood(at(1, 7, 0)); m != Bored {
		t.Fatalf("mood in the morning: %s", m)
	}
}

func TestAdvanceCatchUpIsLimited(t *testing.T) {
	p := New(at(0, 7, 0), DefaultSettings())
	p.Advance(at(30, 12, 0))
	if p.Stats.Food != 0 || p.Updated != at(30, 12, 0) {
		t.Fatalf("after a month: %+v updated %s", p.Stats, p.Updated)
	}
	if m := p.Mood(at(30, 12, 0)); m != Hungry {
		t.Fatalf("mood: %s", m) // sad, never dead
	}
}

func TestFeed(t *testing.T) {
	p := New(at(0, 9, 0), DefaultSettings())
	p.Stats.Food = 50
	if r := p.Feed(at(0, 9, 0), "cake"); r.Kind != KindEat || p.Stats.Food != 65 || p.Stats.Fun != 75 {
		t.Fatalf("cake: %+v %+v", r, p.Stats)
	}
	if r := p.Feed(at(0, 9, 0), "stone"); r.Food != "apple" || p.Stats.Food != 85 {
		t.Fatalf("unknown food: %+v %+v", r, p.Stats)
	}
	p.Stats.Food = 95
	if r := p.Feed(at(0, 9, 0), "bread"); r.Kind != KindFull || r.Changed {
		t.Fatalf("full: %+v", r)
	}
	if r := p.Feed(at(0, 22, 0), "bread"); r.Kind != KindAsleep {
		t.Fatalf("at night: %+v", r)
	}
}

func TestPlayAndLimit(t *testing.T) {
	s := DefaultSettings()
	s.PlayLimitMin = 1
	p := New(at(0, 9, 0), s)
	p.Stats = Stats{Food: 80, Fun: 10, Energy: 80}

	if r := p.Play(at(0, 9, 0)); r.Kind != KindPlay || p.Stats.Fun != 30 || p.Stats.Energy != 72 {
		t.Fatalf("play: %+v %+v", r, p.Stats)
	}
	// Cuddles a few seconds apart count as continuous play time.
	now := at(0, 9, 0)
	for i := 0; i < 20; i++ {
		now = now.Add(3 * time.Second)
		p.Cuddle(now)
	}
	if r := p.Cuddle(now.Add(time.Second)); r.Kind != KindLimit {
		t.Fatalf("after the limit: %+v, played %.2f min", r, p.PlayMin)
	}
	if r := p.Feed(now, "apple"); r.Kind != KindEat {
		t.Fatalf("feeding is never limited: %+v", r)
	}
	if left := p.PlayLeft(at(1, 9, 0)); left != 1 {
		t.Fatalf("next day play left %.2f", left)
	}

	p.Advance(at(1, 9, 0)) // the night refills energy, so drain it afterwards
	p.Stats.Energy = 10
	if r := p.Play(at(1, 9, 0)); r.Kind != KindTooTired {
		t.Fatalf("tired: %+v", r)
	}
}

func TestCuddleCooldown(t *testing.T) {
	p := New(at(0, 9, 0), DefaultSettings())
	p.Stats.Fun = 50
	p.Cuddle(at(0, 9, 0))
	p.Cuddle(at(0, 9, 0).Add(time.Second))
	if p.Stats.Fun != 56 {
		t.Fatalf("held stroke should count once: fun %.1f", p.Stats.Fun)
	}
}

func TestNap(t *testing.T) {
	p := New(at(0, 13, 0), DefaultSettings())
	p.Stats = Stats{Food: 80, Fun: 80, Energy: 90}
	if r := p.Nap(at(0, 13, 0)); r.Kind != KindNotTired {
		t.Fatalf("rested pet: %+v", r)
	}
	p.Stats.Energy = 30
	if r := p.Nap(at(0, 13, 0)); r.Kind != KindNap || p.Mood(at(0, 13, 1)) != Napping {
		t.Fatalf("nap: %+v", r)
	}
	p.Advance(at(0, 13, 30))
	if p.Napping(at(0, 13, 30)) || p.Stats.Energy < 40 {
		t.Fatalf("after the nap: napping %v, %+v", p.Napping(at(0, 13, 30)), p.Stats)
	}
	p.Nap(at(0, 14, 0))
	if r := p.Cuddle(at(0, 14, 1)); r.Kind != KindCuddle || p.Napping(at(0, 14, 2)) {
		t.Fatalf("a cuddle wakes it: %+v", r)
	}
}

func TestSettingsNormalize(t *testing.T) {
	s := Settings{Name: "  ", Lang: "de", Weekday: DaySchedule{Wake: "7:5", Bed: "25:00"},
		Volume: 300, Foods: map[string]string{"04AABB": "cake", "x": "stone"}}
	s.Normalize()
	if s.Name != "Čenda" || s.Lang != "cs" || s.Weekday.Wake != "07:05" || s.Weekday.Bed != "20:00" ||
		s.Volume != 100 || len(s.Foods) != 1 || s.Difficulty != "normal" {
		t.Fatalf("normalized: %+v", s)
	}
}

func TestMinutesToBed(t *testing.T) {
	s := DefaultSettings()
	if m := s.MinutesToBed(at(0, 19, 50)); m != 10 {
		t.Fatalf("minutes to bed: %d", m)
	}
}

func TestNextWake(t *testing.T) {
	s := DefaultSettings()
	if w := s.NextWake(at(0, 6, 0)); w != "07:00" { // Wednesday early morning
		t.Errorf("before wake: %s", w)
	}
	if w := s.NextWake(at(2, 21, 0)); w != "08:00" { // Friday evening: Saturday's
		t.Errorf("Friday evening: %s", w)
	}
}

func TestGame(t *testing.T) {
	p := New(at(0, 9, 0), DefaultSettings())
	p.Stats = Stats{Food: 80, Fun: 20, Energy: 80}
	if r := p.StartGame(at(0, 9, 0)); r.Kind != KindGame || p.Stats.Fun != 20 {
		t.Fatalf("start: %+v %+v", r, p.Stats)
	}
	r := p.FinishGame(at(0, 9, 0), 4)
	if r.Kind != KindGameOver || r.Hits != 4 || p.Stats.Fun != 44 || p.Stats.Energy != 72 {
		t.Fatalf("finish: %+v %+v", r, p.Stats)
	}
	if e := p.Log[len(p.Log)-1]; e.Kind != "play" || e.Detail != "4/5" {
		t.Fatalf("log: %+v", e)
	}
	p.Stats.Energy = 5
	if r := p.StartGame(at(0, 10, 0)); r.Kind != KindTooTired {
		t.Fatalf("tired: %+v", r)
	}
	if r := p.StartGame(at(0, 22, 0)); r.Kind != KindAsleep {
		t.Fatalf("night: %+v", r)
	}
}

func TestWakeAtNight(t *testing.T) {
	p := New(at(0, 21, 0), DefaultSettings())
	if r := p.Play(at(0, 21, 0)); r.Kind != KindAsleep {
		t.Fatalf("night play: %+v", r)
	}
	if r := p.WakeAtNight(at(0, 21, 0)); r.Kind != KindNightWake || !r.Changed {
		t.Fatalf("wake: %+v", r)
	}
	if ph, m := p.Phase(at(0, 21, 3)), p.Mood(at(0, 21, 3)); ph != Awake || m == Sleeping {
		t.Fatalf("woken: %s %s", ph, m)
	}
	if r := p.Cuddle(at(0, 21, 3)); r.Kind != KindCuddle {
		t.Fatalf("cuddle while woken: %+v", r)
	}
	if ph := p.Phase(at(0, 21, 6)); ph != Night {
		t.Fatalf("after 5 minutes: %s", ph)
	}
	p.Settings.NightWakeMin = 0
	if r := p.WakeAtNight(at(0, 22, 0)); r.Kind != KindAsleep {
		t.Fatalf("waking disabled: %+v", r)
	}
}

func TestSettingsLoadOverDefaults(t *testing.T) {
	var s Settings
	if err := json.Unmarshal([]byte(`{"name":"Robík","volume":10}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.Name != "Robík" || s.Volume != 10 || s.NightWakeMin != 5 || !s.Sounds || s.Weekday.Bed != "20:00" {
		t.Fatalf("loaded: %+v", s)
	}
}

func TestTouchKinds(t *testing.T) {
	p := New(at(0, 9, 0), DefaultSettings())
	p.Stats.Fun = 20
	now := at(0, 9, 0)
	for _, c := range []struct {
		kind string
		fun  float64
	}{{TouchTickle, 23}, {TouchScratch, 33}, {TouchLong, 41}, {"??", 47}} {
		now = now.Add(5 * time.Second)
		if r := p.Touch(now, c.kind); r.Kind != KindCuddle || !r.Changed || math.Abs(p.Stats.Fun-c.fun) > 0.2 {
			t.Fatalf("%s: %+v, fun %.0f want %.0f", c.kind, r, p.Stats.Fun, c.fun)
		}
	}
	if l := p.Log[0]; l.Detail != "tickle" {
		t.Fatalf("log: %+v", p.Log)
	}
}

func TestDemoReset(t *testing.T) {
	p := New(at(0, 9, 0), DefaultSettings())
	p.Stats = Stats{Food: 95, Fun: 50, Energy: 92}
	if r := p.DemoReset(at(0, 9, 0)); r != nil {
		t.Fatalf("demo off: %v", r)
	}
	p.Settings.Demo = true
	r := p.DemoReset(at(0, 9, 0))
	if len(r) != 2 || r[0] != "food" || r[1] != "energy" || p.Stats.Food != DemoLow || p.Stats.Energy != DemoLow || p.Stats.Fun != 50 {
		t.Fatalf("reset %v, stats %+v", r, p.Stats)
	}
	if l := p.Log[len(p.Log)-1]; l.Kind != "demo_reset" || l.Detail != "energy" {
		t.Fatalf("log: %+v", l)
	}
}

func TestDemoNap(t *testing.T) {
	s := DefaultSettings()
	s.Demo = true
	t0 := at(0, 13, 0)
	p := New(t0, s)
	p.Stats = Stats{Food: 50, Fun: 50, Energy: 10}
	if r := p.Nap(t0); r.Kind != KindNap || !p.NapUntil.Equal(t0.Add(time.Minute)) {
		t.Fatalf("demo nap: %+v until %v", r, p.NapUntil)
	}
	for _, c := range []struct {
		after  time.Duration
		energy float64
	}{{5 * time.Second, 30}, {time.Minute, 75}} {
		p.Advance(t0.Add(c.after))
		if reset := p.DemoReset(t0.Add(c.after)); reset != nil {
			t.Fatalf("after %v: a reset below 90%%: %v", c.after, reset)
		}
		if math.Abs(p.Stats.Energy-c.energy) > 1 {
			t.Fatalf("after %v: energy %.1f, want about %.0f", c.after, p.Stats.Energy, c.energy)
		}
	}
	if p.Napping(t0.Add(time.Minute)) {
		t.Fatal("a demo nap ends by itself after a minute (at 75%)")
	}

	// The next nap starts at 75%: past 90% within seconds, then back to 10% and awake.
	t1 := t0.Add(2 * time.Minute)
	p.Stats.Energy = 75
	p.Updated = t1
	if r := p.Nap(t1); r.Kind != KindNap {
		t.Fatalf("second nap: %+v", r)
	}
	if reset := p.DemoReset(t1.Add(5 * time.Second)); len(reset) != 1 || reset[0] != "energy" || p.Stats.Energy != DemoLow {
		t.Fatalf("the second nap: reset %v, energy %.0f", reset, p.Stats.Energy)
	}
	if p.Napping(t1.Add(6 * time.Second)) {
		t.Fatal("the reset ends the nap")
	}
}

func TestPickyEating(t *testing.T) {
	p := New(at(0, 9, 0), DefaultSettings())
	p.Stats.Food = 20
	now := at(0, 9, 0)
	if r := p.Feed(now, "apple"); r.Kind != KindEat || math.Abs(p.Stats.Food-40) > 0.1 {
		t.Fatalf("first apple: %+v, food %.1f", r, p.Stats.Food)
	}
	if r := p.Feed(now, "apple"); r.Kind != KindPicky || r.Changed || math.Abs(p.Stats.Food-40) > 0.1 {
		t.Fatalf("second apple: %+v, food %.1f", r, p.Stats.Food)
	}
	if r := p.Feed(now, "apple"); r.Kind != KindEatAgain || math.Abs(p.Stats.Food-56) > 0.1 {
		t.Fatalf("third apple: %+v, food %.1f (80%% of 20 is 16)", r, p.Stats.Food)
	}
	if r := p.Feed(now, "apple"); r.Kind != KindPicky {
		t.Fatalf("fourth apple: %+v", r)
	}
	if r := p.Feed(now, "carrot"); r.Kind != KindEat {
		t.Fatalf("something else: %+v", r)
	}
	if r := p.Feed(now.Add(time.Hour), "carrot"); r.Kind != KindEat {
		t.Fatalf("the same food an hour later: %+v", r)
	}
}

func TestFoodGivesNoEnergy(t *testing.T) {
	for name, f := range Foods {
		if f.Energy != 0 {
			t.Errorf("%s gives energy %.0f: only sleep gives energy", name, f.Energy)
		}
	}
}

func TestDreamFood(t *testing.T) {
	p := New(at(0, 22, 0), DefaultSettings()) // night
	p.Stats.Energy = 40
	if !p.Asleep(at(0, 22, 0)) {
		t.Fatal("asleep at night")
	}
	if r := p.DreamFood(at(0, 22, 0), "cake"); r.Kind != KindDreamFood || r.Food != "cake" || r.Changed || p.Stats.Energy != 40 {
		t.Fatalf("first card: %+v, energy %.0f", r, p.Stats.Energy)
	}
	if r := p.DreamFood(at(0, 22, 0), "milk"); !r.Changed || p.Stats.Energy != 55 {
		t.Fatalf("second card: %+v, energy %.0f", r, p.Stats.Energy)
	}
	if p.Stats.Food != 80 { // as New made it: the night pauses hunger
		t.Fatalf("dreaming of food does not feed: %.0f", p.Stats.Food)
	}
}

func TestNapUpTo90(t *testing.T) {
	p := New(at(0, 13, 0), DefaultSettings())
	p.Stats = Stats{Food: 80, Fun: 80, Energy: 85}
	if r := p.Nap(at(0, 13, 0)); r.Kind != KindNap {
		t.Fatalf("energy 85 should nap: %+v", r)
	}
}

func TestColorsLeaderboard(t *testing.T) {
	p := New(at(0, 13, 0), DefaultSettings())
	game := func(m, done int, secs float64) Reaction {
		return p.FinishColors(at(0, 13, m), done, 25, time.Duration(secs*float64(time.Second)))
	}
	if re := game(1, 25, 80); re.Place != 1 || re.Record {
		t.Fatalf("first game: %+v", re)
	}
	if re := game(2, 25, 90); re.Place != 2 {
		t.Fatalf("slower: %+v", re)
	}
	if re := game(3, 24, 50); re.Place != 0 {
		t.Fatalf("a round not done is not on the board: %+v", re)
	}
	if re := game(4, 25, 85); re.Place != 2 {
		t.Fatalf("between: %+v", re)
	}
	if re := game(5, 25, 70); re.Place != 1 || !re.Record {
		t.Fatalf("faster: %+v", re)
	}
	if re := game(6, 25, 95); re.Place != 0 {
		t.Fatalf("slower than all three: %+v", re)
	}
	var ms []int64
	for _, e := range p.ColorsTop {
		ms = append(ms, e.Ms)
	}
	if !slices.Equal(ms, []int64{70000, 80000, 85000}) {
		t.Fatalf("board: %v", ms)
	}
}
