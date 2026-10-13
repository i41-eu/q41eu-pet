package app

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/i41-eu/q41eu-raw/wire"
)

// waitCardRead waits until the app has noted the robot's read of the card at want.
func (e *env) waitCardRead(id, uid string, want time.Time) {
	e.t.Helper()
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		e.app.mu.Lock()
		got := e.app.robots[id].cardSeen[uid]
		e.app.mu.Unlock()
		if got.Equal(want) {
			return
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("card %s read at %v, want %v", uid, got, want)
		}
	}
}

// connectWithCards connects a robot whose parent chose these cards' foods.
func (e *env) connectWithCards(id string, foods map[string]string) *fakeRobot {
	e.t.Helper()
	r := e.connectRobot(id)
	r.frame(wire.KindAccepted)
	r.command("emotion") // the robot and its pet are there
	e.app.mu.Lock()
	e.app.robots[id].pet.Settings.Foods = foods
	e.app.mu.Unlock()
	return r
}

func TestCardReadsSurviveRestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state.json")
	e := newEnv(t, file)
	r := e.connectWithCards("robot-1", map[string]string{"04AA": "milk", "04BB": "bread"})
	at := e.clock.now()
	r.event("nfc_tag", map[string]any{"uid": "04AA"})
	e.waitCardRead("robot-1", "04AA", at)
	e.clock.set(at.Add(time.Hour))
	r.event("nfc_tag", map[string]any{"uid": "04CC"}) // unknown: an apple, and among the new cards
	e.waitCardRead("robot-1", "04CC", at.Add(time.Hour))
	if err := e.app.Save(); err != nil {
		t.Fatal(err)
	}

	again := New(Config{RobotToken: testToken, StateFile: file, Location: prague, Now: e.clock.now})
	rb := again.robots["robot-1"]
	want := map[string]time.Time{"04AA": at, "04CC": at.Add(time.Hour)} // 04BB: never read
	if len(rb.cardSeen) != len(want) {
		t.Fatalf("after restart: %v, want %v", rb.cardSeen, want)
	}
	for uid, w := range want {
		if !rb.cardSeen[uid].Equal(w) {
			t.Fatalf("after restart: %s read at %v, want %v", uid, rb.cardSeen[uid], w)
		}
	}
	if _, ok := rb.unknownTags["04CC"]; !ok || len(rb.unknownTags) != 1 {
		t.Fatalf("unknown cards after restart: %v", rb.unknownTags)
	}

	// The running version reads the same document: unknown_tags as before, card_seen beside it.
	var doc struct {
		Robots map[string]map[string]json.RawMessage `json:"robots"`
	}
	b, _ := os.ReadFile(file)
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if saved := doc.Robots["robot-1"]; saved["unknown_tags"] == nil || saved["card_seen"] == nil {
		t.Fatalf("saved robot: %s", b)
	}
}

// A state saved by a version that did not keep the reads: the unknown cards' reads are still
// there, the parent's cards have none (never read).
func TestCardReadsFromAnOlderState(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state.json")
	e := newEnv(t, file)
	r := e.connectWithCards("robot-1", map[string]string{"04AA": "milk"})
	at := e.clock.now()
	r.event("nfc_tag", map[string]any{"uid": "04AA"})
	e.waitCardRead("robot-1", "04AA", at)
	r.event("nfc_tag", map[string]any{"uid": "04DD"})
	e.waitCardRead("robot-1", "04DD", at)
	if err := e.app.Save(); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	b, _ := os.ReadFile(file)
	json.Unmarshal(b, &doc)
	delete(doc["robots"].(map[string]any)["robot-1"].(map[string]any), "card_seen")
	b, _ = json.Marshal(doc)
	if err := os.WriteFile(file, b, 0o600); err != nil {
		t.Fatal(err)
	}

	again := New(Config{RobotToken: testToken, StateFile: file, Location: prague, Now: e.clock.now})
	rb := again.robots["robot-1"]
	if len(rb.cardSeen) != 1 || !rb.cardSeen["04DD"].Equal(at) || rb.pet.Settings.Foods["04AA"] != "milk" {
		t.Fatalf("from an older state: reads %v, foods %v", rb.cardSeen, rb.pet.Settings.Foods)
	}
}

// events follows the browser's event stream (/api/events): the names of the events, in order.
func (b *browser) events() <-chan string {
	b.t.Helper()
	res, err := b.c.Get(b.e.srv.URL + "/api/events")
	if err != nil {
		b.t.Fatal(err)
	}
	b.t.Cleanup(func() { res.Body.Close() })
	names := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			if name, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
				select {
				case names <- name:
				default:
				}
			}
		}
	}()
	return names
}

func waitEvent(t *testing.T, names <-chan string, want string) {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case name := <-names:
			if name == want {
				return
			}
		case <-timeout:
			t.Fatalf("no %q event", want)
		}
	}
}

func TestParentSeesCardsByLastRead(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectWithCards("robot-1", map[string]string{
		"04AA": "milk", "04BB": "bread", // read below
		"04F1": "cake", "04F2": "apple", "04F0": "apple", "04F3": "carrot", // never read
	})
	parent := e.browser()
	parent.pair(r)
	if code, _ := parent.post("/api/parent/unlock", map[string]any{"pin": "4321", "remember": true}); code != 200 {
		t.Fatalf("unlock: %d", code)
	}
	events := parent.events()
	t0 := e.clock.now()
	read := func(uid string, at time.Time) {
		t.Helper()
		e.clock.set(at)
		r.event("nfc_tag", map[string]any{"uid": uid})
		e.waitCardRead("robot-1", uid, at)
		waitEvent(t, events, "card") // an open parent page asks for the cards again
	}
	read("04BB", t0)
	read("04ZZ", t0.Add(20*time.Minute)) // unknown: no food chosen
	last := t0.Add(30 * time.Minute)
	read("04AA", last)

	cardsAt := func(at time.Time) (uids []string, cards map[string]map[string]any) {
		t.Helper()
		e.clock.set(at)
		code, out := parent.get("/api/parent")
		if code != 200 || out["card_recent_s"] != cardRecent.Seconds() {
			t.Fatalf("parent page: %d %v", code, out)
		}
		if now, _ := time.Parse(time.RFC3339Nano, out["now"].(string)); !now.Equal(at) {
			t.Fatalf("now %v, want %v", out["now"], at)
		}
		cards = map[string]map[string]any{}
		for _, c := range out["cards"].([]any) {
			c := c.(map[string]any)
			uids = append(uids, c["uid"].(string))
			cards[c["uid"].(string)] = c
		}
		return uids, cards
	}
	seen := func(c map[string]any) time.Time {
		s, _ := c["seen"].(string)
		at, _ := time.Parse(time.RFC3339Nano, s)
		return at
	}

	// The most recently read first; never read at the end, by food in the menu's order, then by uid.
	uids, cards := cardsAt(last.Add(cardRecent - time.Second))
	if want := []string{"04AA", "04ZZ", "04BB", "04F0", "04F2", "04F3", "04F1"}; !slices.Equal(uids, want) {
		t.Fatalf("cards %v, want %v", uids, want)
	}
	if !seen(cards["04AA"]).Equal(last) || !seen(cards["04ZZ"]).Equal(t0.Add(20*time.Minute)) || !seen(cards["04BB"]).Equal(t0) {
		t.Fatalf("last reads: %v", cards)
	}
	if _, ok := cards["04F0"]["seen"]; ok || cards["04ZZ"]["food"] != "" || cards["04AA"]["food"] != "milk" {
		t.Fatalf("cards: %v", cards)
	}
	// Read in the last 5 minutes: just under them it still blinks, at 5 minutes no more.
	if cards["04AA"]["recent"] != true || cards["04ZZ"]["recent"] != nil {
		t.Fatalf("recent at 4:59: %v", cards)
	}
	if _, cards = cardsAt(last.Add(cardRecent)); cards["04AA"]["recent"] != nil {
		t.Fatalf("recent at 5:00: %v", cards["04AA"])
	}

	// A card the parent removes takes its last read along.
	_, out := parent.get("/api/parent")
	s := out["settings"].(map[string]any)
	delete(s["foods"].(map[string]any), "04BB")
	if code, _ := parent.post("/api/parent/settings", s); code != 200 {
		t.Fatalf("save settings: %d", code)
	}
	if uids, _ = cardsAt(e.clock.now()); slices.Contains(uids, "04BB") || len(uids) != 6 {
		t.Fatalf("after removing 04BB: %v", uids)
	}
	e.app.mu.Lock()
	_, kept := e.app.robots["robot-1"].cardSeen["04BB"]
	e.app.mu.Unlock()
	if kept {
		t.Fatal("the removed card's read is still kept")
	}
}

// A game takes the robot's events, but a card held to it then still counts as read.
func TestCardReadDuringAGame(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectWithCards("robot-1", map[string]string{})
	kid := e.browser()
	kid.pair(r)
	if code, out := kid.post("/api/action", map[string]any{"action": "play"}); code != 200 || out["reaction"].(map[string]any)["kind"] != "game" {
		t.Fatalf("play: %d %v", code, out)
	}
	_, before := kid.get("/api/state")
	r.event("nfc_tag", map[string]any{"uid": "04AA"})
	e.waitCardRead("robot-1", "04AA", e.clock.now())
	if _, after := kid.get("/api/state"); stats(t, after)["food"] != stats(t, before)["food"] {
		t.Fatalf("the card fed the pet during the game: %v -> %v", stats(t, before), stats(t, after))
	}
}
