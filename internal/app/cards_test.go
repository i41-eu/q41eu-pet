package app

import (
	"encoding/json"
	"os"
	"path/filepath"
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
