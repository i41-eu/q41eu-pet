package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/i41-eu/q41eu-pet/internal/robotpic"
	"github.com/i41-eu/q41eu-raw/robotauth"
	"github.com/i41-eu/q41eu-raw/wire"
)

const testToken = "test-token"

var prague, _ = time.LoadLocation("Europe/Prague")

// clock is a settable time for the App.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

type env struct {
	t       *testing.T
	app     *App
	srv     *httptest.Server
	clock   *clock
	state   string
	handler http.Handler // the app's, swapped by useManager
}

func newEnv(t *testing.T, stateFile string) *env {
	t.Helper()
	c := &clock{t: time.Date(2026, 9, 30, 10, 0, 0, 0, prague)} // Wednesday morning
	e := &env{t: t, clock: c, state: stateFile}
	// The server first: the app (and its robot hub) gets its address at the start.
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { e.handler.ServeHTTP(w, r) }))
	e.app = New(Config{RobotToken: testToken, StateFile: stateFile, Location: prague, Now: c.now, PublicURL: e.srv.URL})
	e.handler = e.app.Handler()
	t.Cleanup(e.srv.Close)
	return e
}

// useManager makes the app check robot tokens (and sign-ins) with a Stackchan manager.
func (e *env) useManager(url string) {
	e.app.cfg.Manager = robotauth.New(url, "app-secret")
	e.app.hub = e.app.newHub()
	e.handler = e.app.Handler()
}

// fakeRobot is a websocket client speaking the robot side of the protocol.
type fakeRobot struct {
	t    *testing.T
	ws   *websocket.Conn
	msgs chan robotMsg

	mu       sync.Mutex
	pairCode wire.PairCodeBody // the latest, kept aside: waiting for other messages skips frames
}

type robotMsg struct {
	frame  wire.Frame
	binary []byte
}

func (e *env) connectRobot(id string) *fakeRobot {
	e.t.Helper()
	return e.connectRobotWith(id, nil)
}

// connectRobotWith registers a robot that accepts these commands.
func (e *env) connectRobotWith(id string, commands []string) *fakeRobot {
	e.t.Helper()
	return e.connectRobotAs(id, testToken, commands)
}

// connectRobotAs registers a robot with this token.
func (e *env) connectRobotAs(id, token string, commands []string) *fakeRobot {
	e.t.Helper()
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	h.Set(wire.DeviceIDHeader, id)
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(e.srv.URL, "http")+wire.ConnectPath, h)
	if err != nil {
		e.t.Fatalf("robot dial: %v", err)
	}
	e.t.Cleanup(func() { ws.Close() })
	f, _ := wire.Marshal(wire.KindRegister, wire.Meta{WorkerID: id}, wire.RegisterBody{Class: wire.ClassRobot, Capabilities: wire.RobotCapabilities{Commands: commands}})
	ws.WriteMessage(websocket.TextMessage, f)
	r := &fakeRobot{t: e.t, ws: ws, msgs: make(chan robotMsg, 256)}
	go func() {
		for {
			kind, data, err := ws.ReadMessage()
			if err != nil {
				close(r.msgs)
				return
			}
			if kind == websocket.BinaryMessage {
				r.msgs <- robotMsg{binary: data}
				continue
			}
			frames, _ := wire.Parse(data)
			for _, fr := range frames {
				if fr.Kind == wire.KindPairCode {
					r.mu.Lock()
					fr.Decode(&r.pairCode)
					r.mu.Unlock()
				}
				r.msgs <- robotMsg{frame: fr}
			}
		}
	}()
	return r
}

// next waits for a message matching ok, skipping others.
func (r *fakeRobot) next(what string, ok func(robotMsg) bool) robotMsg {
	r.t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case m, open := <-r.msgs:
			if !open {
				r.t.Fatalf("robot socket closed waiting for %s", what)
			}
			if ok(m) {
				return m
			}
		case <-timeout:
			r.t.Fatalf("timeout waiting for %s", what)
		}
	}
}

func (r *fakeRobot) frame(kind string) wire.Frame {
	r.t.Helper()
	return r.next(kind, func(m robotMsg) bool { return m.frame.Kind == kind }).frame
}

// command waits for a RobotCommand and returns its args.
func (r *fakeRobot) command(name string) map[string]any {
	r.t.Helper()
	var body wire.RobotCommandBody
	r.next("command "+name, func(m robotMsg) bool {
		if m.frame.Kind != wire.KindRobotCommand {
			return false
		}
		body = wire.RobotCommandBody{}
		m.frame.Decode(&body)
		return body.Command == name
	})
	return body.Args
}

func (r *fakeRobot) binary(kind byte) []byte {
	r.t.Helper()
	return r.next("binary", func(m robotMsg) bool { return len(m.binary) > 0 && m.binary[0] == kind }).binary
}

func (r *fakeRobot) event(name string, data map[string]any) {
	f, _ := wire.Marshal(wire.KindRobotEvent, wire.Meta{}, wire.RobotEventBody{Name: name, Data: data})
	r.ws.WriteMessage(websocket.TextMessage, f)
}

// browser is an HTTP client with its own cookie jar.
type browser struct {
	t *testing.T
	c *http.Client
	e *env
}

func (e *env) browser() *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: e.t, e: e, c: &http.Client{Jar: jar}}
}

func (b *browser) get(path string) (int, map[string]any) {
	b.t.Helper()
	res, err := b.c.Get(b.e.srv.URL + path)
	if err != nil {
		b.t.Fatal(err)
	}
	return decode(b.t, res)
}

func (b *browser) post(path string, body any) (int, map[string]any) {
	b.t.Helper()
	j, _ := json.Marshal(body)
	res, err := b.c.Post(b.e.srv.URL+path, "application/json", bytes.NewReader(j))
	if err != nil {
		b.t.Fatal(err)
	}
	return decode(b.t, res)
}

func decode(t *testing.T, res *http.Response) (int, map[string]any) {
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := map[string]any{}
	json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

// pair scans the robot's current code.
func (b *browser) pair(r *fakeRobot) {
	b.t.Helper()
	var pc wire.PairCodeBody
	for deadline := time.Now().Add(3 * time.Second); pc.URL == "" && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		r.mu.Lock()
		pc = r.pairCode
		r.mu.Unlock()
	}
	res, err := b.c.Get(pc.URL)
	if err != nil || res.StatusCode != http.StatusOK {
		b.t.Fatalf("pair: %v %v", err, res.Status)
	}
	res.Body.Close()
}

func stats(t *testing.T, state map[string]any) map[string]any {
	t.Helper()
	s, ok := state["stats"].(map[string]any)
	if !ok {
		t.Fatalf("no stats in %v", state)
	}
	return s
}

func TestRobotNeedsToken(t *testing.T) {
	e := newEnv(t, "")
	if code := e.dialRobot("robot-1", "nope"); code != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d, want 401", code)
	}
}

// dialRobot opens a robot connection with this token: the HTTP status of the upgrade.
func (e *env) dialRobot(id, token string) int {
	e.t.Helper()
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	h.Set(wire.DeviceIDHeader, id)
	ws, res, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(e.srv.URL, "http")+wire.ConnectPath, h)
	if err == nil {
		ws.Close()
	}
	if res == nil {
		e.t.Fatalf("robot dial: %v", err)
	}
	return res.StatusCode
}

// A robot set up by a Stackchan manager connects with its own token, which the manager confirms
// for that robot only; the debug API still needs the shared token.
func TestRobotSetUpByManager(t *testing.T) {
	mgr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Robot, Token string }
		json.NewDecoder(r.Body).Decode(&req)
		ok := r.Header.Get("Authorization") == "Bearer app-secret" && req.Robot == "robot-2" && req.Token == "own-token"
		json.NewEncoder(w).Encode(map[string]any{"ok": ok, "owner": "someone", "cache_s": 60})
	}))
	defer mgr.Close()
	e := newEnv(t, "")
	e.useManager(mgr.URL)

	if code := e.dialRobot("robot-2", "own-token"); code != http.StatusSwitchingProtocols {
		t.Errorf("its own token: %d, want 101", code)
	}
	if code := e.dialRobot("robot-3", "own-token"); code != http.StatusUnauthorized {
		t.Errorf("another robot with that token: %d, want 401", code)
	}
	if code := e.dialRobot("robot-2", testToken); code != http.StatusSwitchingProtocols {
		t.Errorf("the shared token: %d, want 101", code)
	}
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/debug/robot-2/run", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer own-token")
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != http.StatusUnauthorized {
		t.Errorf("debug with a manager token: %v %v, want 401", err, res.Status)
	}
}

func TestPairAndPlay(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectRobot("robot-1")
	r.frame(wire.KindAccepted)
	r.command("emotion") // the pet's face right away

	kid := e.browser()
	if code, out := kid.get("/api/state"); code != http.StatusNotFound || out["error"] != "not_paired" {
		t.Fatalf("unpaired state: %d %v", code, out)
	}
	kid.pair(r)
	r.frame(wire.KindPaired)

	code, st := kid.get("/api/state")
	if code != 200 || st["robot"] != "robot-1" || st["online"] != true || st["phase"] != "awake" {
		t.Fatalf("state: %d %v", code, st)
	}

	// Feeding from the page: food picture and a munch on the robot.
	code, out := kid.post("/api/action", map[string]any{"action": "feed", "food": "cake"})
	if code != 200 || out["reaction"].(map[string]any)["kind"] != "eat" {
		t.Fatalf("feed: %d %v", code, out)
	}
	if pic := r.binary(wire.BinShowJPEG); len(pic) < 1000 {
		t.Fatalf("food picture: %d bytes", len(pic))
	}
	if pcm := r.binary(wire.BinSpeakerPCM); len(pcm) < 1000 {
		t.Fatalf("munch: %d bytes", len(pcm))
	}
	r.command("face") // after the picture
	if say := r.command("say"); !isFoodLine("cake", say["text"]) {
		t.Fatalf("eat bubble: %v", say)
	}

	// Stroking the head is a cuddle.
	fun := stats(t, out["state"].(map[string]any))["fun"].(float64)
	r.event("head_press", map[string]any{"z0": 2})
	r.event("head_release", map[string]any{"ms": 600}) // a normal cuddle
	r.command("sticker")
	_, st = kid.get("/api/state")
	if got := stats(t, st)["fun"].(float64); got != fun+6 {
		t.Fatalf("fun after cuddle %.1f, want %.1f", got, fun+6)
	}

	// Another browser without pairing sees nothing and cannot act.
	stranger := e.browser()
	if code, _ := stranger.post("/api/action", map[string]any{"action": "play"}); code != http.StatusNotFound {
		t.Fatalf("stranger action: %d", code)
	}
}

func TestFoodCardsAndParent(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectRobot("robot-1")
	parent := e.browser()
	parent.pair(r)
	other := e.browser() // for the PIN lockout below; codes expire, so pair before the clock moves
	other.pair(r)

	// An unknown card feeds an apple and is remembered for the parent.
	r.event("nfc_tag", map[string]any{"uid": "04A1B2C3"})
	r.binary(wire.BinShowJPEG)

	if code, out := parent.get("/api/parent"); code != 200 || out["unlocked"] != false || out["has_pin"] != false {
		t.Fatalf("locked parent page: %d %v", code, out)
	}
	if code, _ := parent.post("/api/parent/settings", map[string]any{}); code != http.StatusForbidden {
		t.Fatalf("settings while locked: %d", code)
	}
	if code, _ := parent.post("/api/parent/unlock", map[string]any{"pin": "12"}); code != http.StatusBadRequest {
		t.Fatalf("short PIN: %d", code)
	}
	if code, _ := parent.post("/api/parent/unlock", map[string]any{"pin": "4321"}); code != 200 {
		t.Fatalf("set PIN: %d", code)
	}
	code, out := parent.get("/api/parent")
	cards := out["cards"].([]any)
	if code != 200 || len(cards) != 1 || cards[0].(map[string]any)["uid"] != "04A1B2C3" || cards[0].(map[string]any)["food"] != "" {
		t.Fatalf("unknown card: %d %v", code, out["cards"])
	}
	log := out["log"].([]any)
	if len(log) == 0 || log[0].(map[string]any)["detail"] != "apple" {
		t.Fatalf("log: %v", log)
	}

	// Assign the card to milk; bedtime and language too.
	s := out["settings"].(map[string]any)
	s["foods"] = map[string]any{"04A1B2C3": "milk"}
	s["lang"] = "en"
	s["weekday"] = map[string]any{"wake": "07:00", "bed": "21:00"}
	if code, _ := parent.post("/api/parent/settings", s); code != 200 {
		t.Fatalf("save settings: %d", code)
	}
	_, out = parent.get("/api/parent")
	cards = out["cards"].([]any)
	if len(cards) != 1 || cards[0].(map[string]any)["food"] != "milk" || out["settings"].(map[string]any)["lang"] != "en" {
		t.Fatalf("after saving: %v", out)
	}
	e.clock.set(e.clock.now().Add(time.Hour)) // hungry enough to eat again
	r.event("nfc_tag", map[string]any{"uid": "04A1B2C3"})
	if say := r.command("say"); !isFoodLine("milk", say["text"]) {
		t.Fatalf("milk bubble: %v", say)
	}

	// Wrong PINs lock out after a few tries, in another browser too.
	for i := 0; i < pinTries; i++ {
		if code, _ := other.post("/api/parent/unlock", map[string]any{"pin": "0000"}); code != http.StatusForbidden {
			t.Fatalf("wrong PIN %d: %d", i, code)
		}
	}
	if code, _ := other.post("/api/parent/unlock", map[string]any{"pin": "4321"}); code != http.StatusTooManyRequests {
		t.Fatalf("after lockout: %d", code)
	}
}

func TestBedtimeAndMorning(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectRobot("robot-1")
	kid := e.browser()
	kid.pair(r)

	e.clock.set(time.Date(2026, 9, 30, 19, 55, 0, 0, prague)) // 5 minutes before bed
	e.app.Tick()
	if say := r.command("say"); !strings.Contains(say["text"].(string), "spat") {
		t.Fatalf("bedtime warning: %v", say)
	}
	e.clock.set(time.Date(2026, 9, 30, 20, 0, 30, 0, prague))
	e.app.Tick()
	if say := r.command("say"); !strings.Contains(say["text"].(string), "Dobrou noc") {
		t.Fatalf("good night: %v", say)
	}
	r.binary(wire.BinSpeakerPCM) // the lullaby

	_, st := kid.get("/api/state")
	if st["phase"] != "night" || st["mood"] != "sleeping" {
		t.Fatalf("night state: %v", st)
	}
	if code, out := kid.post("/api/action", map[string]any{"action": "play"}); code != 200 || out["reaction"].(map[string]any)["kind"] != "asleep" {
		t.Fatalf("play at night: %v", out)
	}

	e.clock.set(time.Date(2026, 10, 1, 7, 0, 10, 0, prague))
	e.app.Tick()
	if on := r.command("screensaver"); on["on"] != false {
		t.Fatalf("morning screen: %v", on)
	}
	if say := r.command("say"); !strings.Contains(say["text"].(string), "Dobre rano") {
		t.Fatalf("morning: %v", say)
	}
}

func TestStateSurvivesRestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state.json")
	e := newEnv(t, file)
	r := e.connectRobot("robot-1")
	kid := e.browser()
	kid.pair(r)
	kid.post("/api/parent/unlock", map[string]any{"pin": "1234"})
	kid.post("/api/action", map[string]any{"action": "feed", "food": "bread"})
	if err := e.app.Save(); err != nil {
		t.Fatal(err)
	}

	again := New(Config{RobotToken: testToken, StateFile: file, Location: prague, Now: e.clock.now})
	again.mu.Lock()
	defer again.mu.Unlock()
	rb := again.robots["robot-1"]
	if rb == nil || rb.pinHash == "" || !pinOK(rb.pinHash, "1234") || len(rb.pet.Log) == 0 || len(again.sessions) != 1 {
		t.Fatalf("after restart: %+v sessions %d", rb, len(again.sessions))
	}
}

func TestCatchTheBall(t *testing.T) {
	e := newEnv(t, "")
	e.fastGame(300 * time.Millisecond)
	r := e.connectRobot("robot-1")
	kid := e.browser()
	kid.pair(r)

	code, out := kid.post("/api/action", map[string]any{"action": "play"})
	if code != 200 || out["reaction"].(map[string]any)["kind"] != "game" {
		t.Fatalf("play: %d %v", code, out)
	}
	r.next("light stream on", func(m robotMsg) bool { // (connecting turned it off first)
		var body wire.RobotCommandBody
		m.frame.Decode(&body)
		return body.Command == "light_stream" && body.Args["on"] == true
	})
	if say := r.command("say"); !strings.Contains(say["text"].(string), "micek") {
		t.Fatalf("game intro: %v", say)
	}
	r.binary(wire.BinShowJPEG) // the first ball

	// Tap the wrong quarter (ignored), then the ball.
	e.app.mu.Lock()
	spot := e.app.robots["robot-1"].game.spot
	e.app.mu.Unlock()
	centers := [][2]float64{{80, 60}, {240, 60}, {80, 180}, {240, 180}}
	wrong := centers[(spot+1)%4]
	r.event("screen_tap", map[string]any{"x": wrong[0], "y": wrong[1]})
	r.event("screen_tap", map[string]any{"x": centers[spot][0], "y": centers[spot][1]})
	r.binary(wire.BinSpeakerPCM) // the catch chirp (the intro hello came before the ball)

	// The other balls fly away unnoticed; then the stars and the score.
	r.next("game over", func(m robotMsg) bool {
		var body wire.RobotCommandBody
		m.frame.Decode(&body)
		text, _ := body.Args["text"].(string)
		return body.Command == "say" && strings.Contains(text, "jeden z peti")
	})
	_, st := kid.get("/api/state")
	if st["game"] != nil {
		t.Fatalf("game still on: %v", st["game"])
	}
	e.app.mu.Lock()
	last := e.app.robots["robot-1"].pet.Log
	e.app.mu.Unlock()
	if l := last[len(last)-1]; l.Kind != "play" || l.Detail != "1/5" {
		t.Fatalf("log: %+v", l)
	}
}

// fastGame shortens the game's timing for a test (before the game starts).
func (e *env) fastGame(round time.Duration) {
	e.app.mu.Lock()
	defer e.app.mu.Unlock()
	e.app.timing.round, e.app.timing.moveExtra = round, 0
	e.app.timing.gap, e.app.timing.intro, e.app.timing.stars = 50*time.Millisecond, 50*time.Millisecond, 100*time.Millisecond
	e.app.timing.penalty = 100 * time.Millisecond
	e.app.timing.photoAsk, e.app.timing.photoCount, e.app.timing.photoShot = 50*time.Millisecond, 100*time.Millisecond, 150*time.Millisecond
}

// lightMsg is a light stream message with n samples of the same values.
func lightMsg(n int, ps, ch0 uint16) []byte {
	b := []byte{wire.BinLight, byte(n), 0}
	for i := 0; i < n; i++ {
		b = append(b, 0, 0, 0, 0, byte(ps), byte(ps>>8), byte(ch0), byte(ch0>>8), 10, 0)
	}
	return b
}

func TestHeadDodgesAHand(t *testing.T) {
	e := newEnv(t, "")
	e.fastGame(2 * time.Second)
	r := e.connectRobot("robot-1")
	kid := e.browser()
	kid.pair(r)
	kid.post("/api/action", map[string]any{"action": "play"})

	round := func() int {
		e.app.mu.Lock()
		defer e.app.mu.Unlock()
		if g := e.app.robots["robot-1"].game; g != nil {
			return g.round
		}
		return -1
	}
	// Catch balls 1-3 quickly (the head keeps still, circles, wanders), up to ball 4.
	for round() < 4 {
		e.app.mu.Lock()
		g := e.app.robots["robot-1"].game
		spot, waiting := g.spot, g.waiting
		e.app.mu.Unlock()
		if waiting {
			x, y := 80+160*float64(spot%2), 60+120*float64(spot/2)
			r.event("screen_tap", map[string]any{"x": x, "y": y})
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Nobody near: no dodge. Then a hand: proximity jumps.
	r.ws.WriteMessage(websocket.BinaryMessage, lightMsg(6, 20, 300))
	time.Sleep(100 * time.Millisecond)
	e.app.mu.Lock()
	dodges := e.app.robots["robot-1"].game.dodges
	e.app.mu.Unlock()
	if dodges != 0 {
		t.Fatalf("dodged with nobody near: %d", dodges)
	}
	r.ws.WriteMessage(websocket.BinaryMessage, lightMsg(2, 400, 300))
	look := r.next("dodge", func(m robotMsg) bool {
		var body wire.RobotCommandBody
		m.frame.Decode(&body)
		yaw, _ := body.Args["yaw"].(float64)
		return body.Command == "look" && (yaw == dodgeYaw || yaw == -dodgeYaw)
	})
	_ = look
	e.app.mu.Lock()
	dodges = e.app.robots["robot-1"].game.dodges
	e.app.mu.Unlock()
	if dodges != 1 {
		t.Fatalf("dodges: %d", dodges)
	}
}

func TestNightDreamsAndWake(t *testing.T) {
	e := newEnv(t, "")
	e.alwaysDream()
	e.clock.set(time.Date(2026, 9, 30, 21, 0, 0, 0, prague)) // Wednesday night
	r := e.connectRobot("robot-1")
	kid := e.browser()
	kid.pair(r)

	// A light touch: a dream picture, no waking.
	r.event("head_press", map[string]any{"z0": 1, "z1": 0, "z2": 0})
	r.binary(wire.BinShowJPEG)
	if _, st := kid.get("/api/state"); st["phase"] != "night" {
		t.Fatalf("after a light touch: %v", st["phase"])
	}

	// The whole palm: awake for a few minutes, quietly.
	e.clock.set(e.clock.now().Add(10 * time.Second))                 // dreams come at most every 8 s
	r.event("head_press", map[string]any{"z0": 3, "z1": 2, "z2": 3}) // not quite the whole palm: a dream
	r.binary(wire.BinShowJPEG)
	e.clock.set(e.clock.now().Add(10 * time.Second)) // dreams come at most every 8 s
	r.event("head_press", map[string]any{"z0": 3, "z1": 3, "z2": 3})
	if on := r.command("screensaver"); on["on"] != false {
		t.Fatalf("wake screen: %v", on)
	}
	if say := r.command("say"); !strings.Contains(say["text"].(string), "vzhuru") && !strings.Contains(say["text"].(string), "spal") {
		t.Fatalf("night wake: %v", say)
	}
	if _, st := kid.get("/api/state"); st["phase"] != "awake" {
		t.Fatalf("woken: %v", st["phase"])
	}
	if code, out := kid.post("/api/action", map[string]any{"action": "cuddle"}); code != 200 || out["reaction"].(map[string]any)["kind"] != "cuddle" {
		t.Fatalf("cuddle while woken: %v", out)
	}

	// Time is up: back to sleep with a good night.
	e.clock.set(time.Date(2026, 9, 30, 21, 6, 0, 0, prague))
	e.app.Tick()
	r.next("good night", func(m robotMsg) bool {
		var body wire.RobotCommandBody
		m.frame.Decode(&body)
		text, _ := body.Args["text"].(string)
		return body.Command == "say" && strings.Contains(text, "Dobrou noc")
	})

	// A long one-finger touch is not a hard press.
	e.clock.set(time.Date(2026, 9, 30, 22, 0, 0, 0, prague))
	r.event("head_press", map[string]any{"z0": 0, "z1": 3, "z2": 0})
	r.event("head_release", map[string]any{"ms": 3000})
	time.Sleep(100 * time.Millisecond)
	if _, st := kid.get("/api/state"); st["phase"] != "night" {
		t.Fatalf("one finger held: %v", st["phase"])
	}
}

func TestNapDreamsUntilThePalm(t *testing.T) {
	e := newEnv(t, "")
	e.alwaysDream()
	r := e.connectRobot("robot-1")
	kid := e.browser()
	kid.pair(r)
	e.app.mu.Lock()
	e.app.robots["robot-1"].pet.Stats.Energy = 30
	e.app.mu.Unlock()
	if _, out := kid.post("/api/action", map[string]any{"action": "nap"}); out["reaction"].(map[string]any)["kind"] != "nap" {
		t.Fatalf("nap: %v", out)
	}
	for _, z := range []map[string]any{{"z0": 0, "z1": 2, "z2": 0}, {"z0": 3, "z1": 3, "z2": 1}} {
		e.clock.set(e.clock.now().Add(10 * time.Second))
		r.event("head_press", z)
		r.binary(wire.BinShowJPEG) // a dream
		if _, st := kid.get("/api/state"); st["mood"] != "napping" {
			t.Fatalf("after %v: %v", z, st["mood"])
		}
	}
	r.event("head_swipe_forward", nil)
	e.clock.set(e.clock.now().Add(10 * time.Second))
	r.event("head_press", map[string]any{"z0": 3, "z1": 3, "z2": 3})
	if say := r.command("say"); !strings.Contains(say["text"].(string), "vzhuru") && !strings.Contains(say["text"].(string), "vyspal") {
		t.Fatalf("palm wakes: %v", say)
	}
	if _, st := kid.get("/api/state"); st["mood"] == "napping" {
		t.Fatalf("still napping")
	}
}

// alwaysDream makes every touch while asleep a dream (not the random "Zzz").
func (e *env) alwaysDream() {
	e.app.mu.Lock()
	e.app.timing.dreamShare = 1
	e.app.mu.Unlock()
}

func TestEatWithSprites(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectRobotWith("robot-1", []string{"sprite", "assets"})
	kid := e.browser()
	kid.pair(r)

	// The robot already has the cake picture (same CRC): no upload, sprites instead of a picture.
	cake := robotpic.Files()["cake.png"]
	list := fmt.Sprintf(`{"files":[{"name":"pet/cake.png","bytes":%d,"crc":%d}]}`, len(cake), crc32.ChecksumIEEE(cake))
	r.event("assets", map[string]any{"list": list, "free": 1e6, "total": 2e6})
	time.Sleep(100 * time.Millisecond)

	kid.post("/api/action", map[string]any{"action": "feed", "food": "cake"})
	first := r.command("sprite")
	if first["asset"] != "pet/cake.png" || first["id"] != "food" {
		t.Fatalf("first sprite: %v", first)
	}
	if move := r.command("sprite"); move["ms"] == nil {
		t.Fatalf("the food should glide: %v", move)
	}
	r.command("sprite_hide")
	if say := r.command("say"); !isFoodLine("cake", say["text"]) {
		t.Fatalf("eat bubble: %v", say)
	}
}

// haveFiles answers the pet's file list request: the robot already has these pet files.
func haveFiles(t *testing.T, r *fakeRobot, names ...string) {
	t.Helper()
	all := petAssets()
	var files []string
	for _, n := range names {
		b, ok := all[n]
		if !ok {
			t.Fatalf("no pet file %s", n)
		}
		files = append(files, fmt.Sprintf(`{"name":%q,"bytes":%d,"crc":%d}`, n, len(b), crc32.ChecksumIEEE(b)))
	}
	r.event("assets", map[string]any{"list": `{"files":[` + strings.Join(files, ",") + `]}`, "free": 1e6, "total": 2e6})
	time.Sleep(100 * time.Millisecond)
}

func TestGameWithSprites(t *testing.T) {
	e := newEnv(t, "")
	e.fastGame(300 * time.Millisecond)
	r := e.connectRobotWith("robot-1", []string{"sprite", "assets"})
	kid := e.browser()
	kid.pair(r)
	haveFiles(t, r, "pet/ball.png", "pet/star.png")

	kid.post("/api/action", map[string]any{"action": "play"})
	if ball := r.command("sprite"); ball["asset"] != "pet/ball.png" {
		t.Fatalf("ball sprite: %v", ball)
	}
	if glide := r.command("sprite"); glide["id"] != "ball" || glide["ms"] == nil {
		t.Fatalf("the ball should glide to the next spot: %v", glide)
	}
	r.next("stars", func(m robotMsg) bool {
		var body wire.RobotCommandBody
		m.frame.Decode(&body)
		return body.Command == "sprite" && body.Args["asset"] == "pet/star.png"
	})
}

func TestParentSetsNeeds(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectRobot("robot-1")
	parent := e.browser()
	parent.pair(r)
	if code, _ := parent.post("/api/parent/stats", map[string]any{"food": 10}); code != http.StatusForbidden {
		t.Fatalf("locked: %d", code)
	}
	parent.post("/api/parent/unlock", map[string]any{"pin": "1234"})
	if code, out := parent.post("/api/parent/stats", map[string]any{"food": 10, "energy": 150}); code != 200 {
		t.Fatalf("set: %d %v", code, out)
	}
	_, st := parent.get("/api/state")
	s := stats(t, st)
	if s["food"] != 10.0 || s["energy"] != 100.0 || s["fun"] != 60.0 || st["mood"] != "hungry" {
		t.Fatalf("needs after setting: %v, mood %v", s, st["mood"])
	}
}

// isFoodLine: the text is one of the pet's lines about that food.
func isFoodLine(food string, text any) bool {
	for _, lang := range []string{"cs", "en"} {
		for _, v := range foodTexts[lang][food] {
			if asciiOnly(v) == text {
				return true
			}
		}
	}
	return false
}

func TestFoodCardIsNotACuddle(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectRobot("robot-1")
	kid := e.browser()
	kid.pair(r)
	e.app.mu.Lock()
	e.app.robots["robot-1"].pet.Stats.Food = 30
	e.app.mu.Unlock()

	r.event("head_press", map[string]any{"z0": 2, "z1": 1, "z2": 0}) // the card touches the head first
	r.event("nfc_tag", map[string]any{"uid": "04AABBCC"})
	r.event("head_press", map[string]any{"z0": 1, "z1": 0, "z2": 0}) // and again while lifted
	time.Sleep(touchWait + 300*time.Millisecond)
	e.app.mu.Lock()
	log := e.app.robots["robot-1"].pet.Log
	e.app.mu.Unlock()
	for _, l := range log {
		if l.Kind == "cuddle" {
			t.Fatalf("a food card counted as a cuddle: %+v", log)
		}
	}
	if l := log[len(log)-1]; l.Kind != "feed" {
		t.Fatalf("log: %+v", log)
	}
}

func TestHeadGoesBackAfterTheGame(t *testing.T) {
	e := newEnv(t, "")
	e.fastGame(200 * time.Millisecond)
	r := e.connectRobot("robot-1")
	kid := e.browser()
	kid.pair(r)
	f, _ := wire.Marshal(wire.KindRobotTelemetry, wire.Meta{}, wire.RobotTelemetryBody{
		Measurements: map[string]float64{"head_yaw_deg": 10, "head_pitch_deg": 30}})
	r.ws.WriteMessage(websocket.TextMessage, f)
	time.Sleep(100 * time.Millisecond)

	kid.post("/api/action", map[string]any{"action": "play"})
	r.next("head back where it was", func(m robotMsg) bool {
		var body wire.RobotCommandBody
		m.frame.Decode(&body)
		return body.Command == "look" && body.Args["yaw"] == 10.0 && body.Args["pitch"] == 30.0
	})
}

func TestTouchKindsOnTheRobot(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectRobot("robot-1")
	kid := e.browser()
	kid.pair(r)
	lastTouch := func() string {
		e.app.mu.Lock()
		defer e.app.mu.Unlock()
		log := e.app.robots["robot-1"].pet.Log
		if len(log) == 0 || log[len(log)-1].Kind != "cuddle" {
			return "none"
		}
		if d := log[len(log)-1].Detail; d != "" {
			return d
		}
		return "cuddle"
	}
	pause := func() { // past the pet's cuddle cooldown
		e.clock.set(e.clock.now().Add(5 * time.Second))
	}

	r.event("head_press", map[string]any{"z0": 1, "z1": 0, "z2": 0})
	r.event("head_release", map[string]any{"ms": 150})
	r.command("sticker") // shy
	if k := lastTouch(); k != "tickle" {
		t.Fatalf("a tiny light touch: %s", k)
	}

	pause()
	r.event("head_press", map[string]any{"z0": 3, "z1": 3, "z2": 2})
	time.Sleep(longTouchMs*time.Millisecond + 200*time.Millisecond) // the hand rests
	if k := lastTouch(); k != "long" {
		t.Fatalf("a resting hand: %s", k)
	}
	r.event("head_release", map[string]any{"ms": 2400}) // no second reaction

	pause()
	r.event("head_swipe_forward", nil)
	r.event("head_swipe_backward", nil)
	time.Sleep(touchWait + 200*time.Millisecond)
	if k := lastTouch(); k != "scratch" {
		t.Fatalf("two strokes: %s", k)
	}
}

func TestMenuOnTheRobot(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectRobotWith("robot-1", []string{"sprite", "assets"})
	kid := e.browser()
	kid.pair(r)
	files := []string{"pet/menu-bg.png"}
	for _, items := range menus {
		for _, m := range items {
			files = append(files, "pet/"+m.asset)
		}
	}
	haveFiles(t, r, files...)
	e.app.mu.Lock()
	e.app.robots["robot-1"].pet.Stats.Food = 30
	e.app.mu.Unlock()
	tiles := func(n int) {
		t.Helper()
		for taps := 0; taps < n; {
			if args := r.command("sprite"); args["tap"] == true {
				taps++
			}
		}
	}

	menuNow := func() string {
		time.Sleep(50 * time.Millisecond)
		e.app.mu.Lock()
		defer e.app.mu.Unlock()
		return e.app.robots["robot-1"].menu
	}
	r.event("screen_tap", map[string]any{"x": 160, "y": 120}) // a tap opens the main menu
	tiles(len(menus["main"]))
	// The needs, and back: to the main menu, then to the face.
	r.event("screen_tap", map[string]any{"x": 208, "y": 140, "sprite": "m:open:needs"})
	r.binary(wire.BinShowJPEG)
	tiles(len(menus["needs"]))
	r.event("screen_tap", map[string]any{"x": 160, "y": 206, "sprite": "m:back"})
	tiles(len(menus["main"]))
	if m := menuNow(); m != "main" {
		t.Fatalf("back from the needs: menu %q", m)
	}
	r.event("screen_tap", map[string]any{"x": 160, "y": 206, "sprite": "m:back"})
	if m := menuNow(); m != "" {
		t.Fatalf("back from the main menu: menu %q", m)
	}
	r.event("screen_tap", map[string]any{"x": 160, "y": 120})
	tiles(len(menus["main"]))
	r.event("screen_tap", map[string]any{"x": 112, "y": 50, "sprite": "m:open:food"})
	tiles(len(menus["food"]))
	r.event("screen_tap", map[string]any{"x": 264, "y": 48, "sprite": "m:feed:banana"})
	r.next("the banana", func(m robotMsg) bool {
		var body wire.RobotCommandBody
		m.frame.Decode(&body)
		return body.Command == "say" && isFoodLine("banana", body.Args["text"])
	})
	e.app.mu.Lock()
	menu, log := e.app.robots["robot-1"].menu, e.app.robots["robot-1"].pet.Log
	e.app.mu.Unlock()
	if menu != "" || log[len(log)-1].Detail != "banana" {
		t.Fatalf("after feeding from the menu: menu %q, log %+v", menu, log[len(log)-1])
	}
}

func TestNightLightFades(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectRobot("robot-1")
	kid := e.browser()
	kid.pair(r)
	expectLEDs := func(h, m int, what string, ok func(map[string]any) bool) {
		t.Helper()
		e.clock.set(time.Date(2026, 9, 30, h, m, 0, 0, prague))
		e.app.Tick()
		r.next(what, func(msg robotMsg) bool {
			var body wire.RobotCommandBody
			msg.frame.Decode(&body)
			return body.Command == "leds" && ok(body.Args)
		})
	}
	expectLEDs(20, 0, "full night light at bedtime", func(a map[string]any) bool { return a["left"] == "#180600" })
	expectLEDs(20, 5, "half after 5 minutes", func(a map[string]any) bool { return a["left"] == "#0c0300" })
	expectLEDs(20, 10, "off after 10 minutes", func(a map[string]any) bool { return a["effect"] == "off" })
}

func TestNightScreenDimsThenSleeps(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectRobot("robot-1")
	kid := e.browser()
	kid.pair(r)
	e.clock.set(time.Date(2026, 9, 30, 19, 59, 0, 0, prague))
	e.app.Tick()
	e.clock.set(time.Date(2026, 9, 30, 20, 0, 10, 0, prague))
	e.app.Tick()
	if b := r.command("brightness"); b["value"] != float64(nightBrightness) {
		t.Fatalf("good night dims: %v", b)
	}
	e.clock.set(time.Date(2026, 9, 30, 20, 3, 0, 0, prague))
	e.app.Tick()
	e.app.mu.Lock()
	off := e.app.robots["robot-1"].screenOff
	e.app.mu.Unlock()
	if off {
		t.Fatal("the screen went off before 5 minutes")
	}
	e.clock.set(time.Date(2026, 9, 30, 20, 5, 30, 0, prague))
	e.app.Tick()
	if s := r.command("screensaver"); s["on"] != true {
		t.Fatalf("after 5 minutes: %v", s)
	}
	e.clock.set(time.Date(2026, 10, 1, 7, 0, 10, 0, prague))
	e.app.Tick()
	if b := r.command("brightness"); b["auto"] != true {
		t.Fatalf("morning brightness: %v", b)
	}
}

func TestDemoModeOnTheRobot(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectRobot("robot-1")
	parent := e.browser()
	parent.pair(r)
	parent.post("/api/parent/unlock", map[string]any{"pin": "1234"})
	_, out := parent.get("/api/parent")
	s := out["settings"].(map[string]any)
	s["demo"] = true
	parent.post("/api/parent/settings", s)
	parent.post("/api/parent/stats", map[string]any{"food": 95})
	e.app.mu.Lock()
	e.app.timing.demoPicture = 100 * time.Millisecond
	e.app.mu.Unlock()

	e.clock.set(e.clock.now().Add(20 * time.Second))
	e.app.Tick()
	r.binary(wire.BinShowJPEG) // the reset picture
	r.next("the demo line", func(m robotMsg) bool {
		var body wire.RobotCommandBody
		m.frame.Decode(&body)
		for _, v := range robotTexts["cs"]["demo_food"] {
			if body.Command == "say" && body.Args["text"] == asciiOnly(v) {
				return true
			}
		}
		return false
	})
	_, st := parent.get("/api/state")
	if food := stats(t, st)["food"].(float64); food > 11 {
		t.Fatalf("food after the demo reset: %.1f", food)
	}
}

func TestSunkenHeadIsLifted(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectRobot("robot-1")
	kid := e.browser()
	kid.pair(r)
	tele := func(pitch float64) {
		f, _ := wire.Marshal(wire.KindRobotTelemetry, wire.Meta{}, wire.RobotTelemetryBody{
			Measurements: map[string]float64{"head_yaw_deg": 4, "head_pitch_deg": pitch}})
		r.ws.WriteMessage(websocket.TextMessage, f)
	}
	tele(22) // a little low: fine
	tele(12) // sunk: lifted back to the rest pitch
	r.next("the head lifted", func(m robotMsg) bool {
		var body wire.RobotCommandBody
		m.frame.Decode(&body)
		return body.Command == "look" && body.Args["pitch"] == float64(headRestPitch) && body.Args["yaw"] == 4.0
	})
	e.clock.set(time.Date(2026, 9, 30, 22, 0, 0, 0, prague)) // at night it may droop
	tele(5)
	time.Sleep(200 * time.Millisecond)
	e.app.mu.Lock()
	last := e.app.robots["robot-1"].lastLift
	e.app.mu.Unlock()
	if last.After(time.Date(2026, 9, 30, 21, 0, 0, 0, prague)) {
		t.Fatal("the head was lifted at night")
	}
}

func TestOldPetFilesAreDeleted(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectRobotWith("robot-1", []string{"sprite", "assets"})
	r.event("assets", map[string]any{"list": `{"files":[{"name":"pet/battery.png","bytes":10,"crc":1},{"name":"mine/song.wav","bytes":10,"crc":2}]}`})
	if del := r.command("asset_delete"); del["name"] != "pet/battery.png" {
		t.Fatalf("delete: %v", del)
	}
}

func TestPulseKeepsPagesLive(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectRobot("robot-1")
	kid := e.browser()
	kid.pair(r)
	res, err := kid.c.Get(e.srv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	buf := make([]byte, 4096)
	res.Body.Read(buf) // the first state
	e.clock.set(e.clock.now().Add(30 * time.Minute))
	e.app.Pulse()
	n, _ := res.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "event: state") {
		t.Fatalf("no state from the pulse: %q", buf[:n])
	}
}

func TestFoodCardWhileAsleepIsADream(t *testing.T) {
	e := newEnv(t, "")
	e.clock.set(time.Date(2026, 9, 30, 22, 0, 0, 0, prague)) // night
	r := e.connectRobot("robot-1")
	kid := e.browser()
	kid.pair(r)
	e.app.mu.Lock()
	e.app.robots["robot-1"].pet.Stats.Energy = 40
	e.app.mu.Unlock()
	r.event("nfc_tag", map[string]any{"uid": "04AA"})
	r.binary(wire.BinShowJPEG) // the dream (no sprites on this robot: the picture)
	e.clock.set(e.clock.now().Add(20 * time.Second))
	r.event("nfc_tag", map[string]any{"uid": "04BB"})
	r.next("the sleep murmur", func(m robotMsg) bool {
		var body wire.RobotCommandBody
		m.frame.Decode(&body)
		return body.Command == "say"
	})
	_, st := kid.get("/api/state")
	if s := stats(t, st); s["energy"].(float64) < 54 || s["food"].(float64) != 80 {
		t.Fatalf("after two cards asleep: %v", s)
	}
}

func TestParentDeviceStaysUnlocked(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state.json")
	e := newEnv(t, file)
	r := e.connectRobot("robot-1")
	parent, other := e.browser(), e.browser()
	parent.pair(r)
	other.pair(r)
	parent.post("/api/parent/unlock", map[string]any{"pin": "1234", "remember": true})
	other.post("/api/parent/unlock", map[string]any{"pin": "1234"})
	unlocked := func(b *browser) bool {
		t.Helper()
		_, out := b.get("/api/parent")
		return out["unlocked"] == true
	}
	e.clock.set(e.clock.now().Add(2 * time.Hour))
	if !unlocked(parent) || unlocked(other) {
		t.Fatalf("after 2 hours: parent's device %v, other %v", unlocked(parent), unlocked(other))
	}

	// Locked on purpose: the PIN again, then unlocked for good again.
	parent.post("/api/parent/lock", nil)
	if unlocked(parent) {
		t.Fatal("still unlocked after Lock")
	}
	if err := e.app.Save(); err != nil {
		t.Fatal(err)
	}
	again := New(Config{RobotToken: testToken, StateFile: file, Location: prague, Now: e.clock.now})
	var devices, locked int
	for _, s := range again.sessions {
		if s.parentDevice {
			devices++
			if s.locked {
				locked++
			}
		}
	}
	if devices != 1 || locked != 1 {
		t.Fatalf("after restart: %d parent devices, %d locked", devices, locked)
	}
	parent.post("/api/parent/unlock", map[string]any{"pin": "1234"})
	e.clock.set(e.clock.now().Add(2 * time.Hour))
	if !unlocked(parent) {
		t.Fatal("the PIN did not unlock the parent's device for good")
	}

	// No longer a parent's device: it locks after the usual time.
	parent.post("/api/parent/device", map[string]any{"parent": false})
	e.clock.set(e.clock.now().Add(parentUnlockTime + time.Minute))
	if unlocked(parent) {
		t.Fatal("unmarked device still unlocked")
	}
}

func TestSchoolHoursRest(t *testing.T) {
	e := newEnv(t, "") // Wednesday 10:00
	r := e.connectRobot("robot-1")
	kid := e.browser()
	kid.pair(r)
	e.clock.set(time.Date(2026, 9, 30, 7, 30, 0, 0, prague)) // before school
	e.app.Tick()
	e.robotState("robot-1", func(rb *robot) {
		rb.pet.Settings.School = true
		rb.pet.Stats.Food = 40
	})
	e.clock.set(time.Date(2026, 9, 30, 8, 0, 30, 0, prague))
	e.app.Tick()
	if s := r.command("screensaver"); s["on"] != true {
		t.Fatalf("school start: %v", s)
	}

	// Food at school: not eaten, only the line.
	_, out := kid.post("/api/action", map[string]any{"action": "feed", "food": "apple"})
	if re := out["reaction"].(map[string]any); re["kind"] != "school" {
		t.Fatalf("feeding at school: %v", re)
	}
	r.next("the school line", func(m robotMsg) bool {
		var body wire.RobotCommandBody
		m.frame.Decode(&body)
		return body.Command == "say" && body.Args["text"] == asciiOnly(robotTexts["cs"]["school"][0])
	})
	e.robotState("robot-1", func(rb *robot) {
		if rb.pet.Stats.Food != 40 {
			t.Fatalf("food changed at school: %.1f", rb.pet.Stats.Food)
		}
		if rb.screenOff {
			t.Fatal("the screen stayed dark for the line")
		}
	})

	// The line said, the screen goes dark again.
	e.clock.set(e.clock.now().Add(schoolLine + time.Second))
	e.app.Pulse()
	if s := r.command("screensaver"); s["on"] != true {
		t.Fatalf("after the line: %v", s)
	}

	// After school the face is back.
	e.clock.set(time.Date(2026, 9, 30, 15, 0, 30, 0, prague))
	e.app.Tick()
	if s := r.command("screensaver"); s["on"] != false {
		t.Fatalf("after school: %v", s)
	}
}

func TestLongPressAsksForGentleness(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectRobot("robot-1")
	kid := e.browser()
	kid.pair(r)
	r.event("screen_long_press", map[string]any{"x": 160, "y": 120})
	r.next("the gentle line", func(m robotMsg) bool {
		var body wire.RobotCommandBody
		m.frame.Decode(&body)
		for _, v := range robotTexts["cs"]["long_press"] {
			if body.Command == "say" && body.Args["text"] == asciiOnly(v) {
				return true
			}
		}
		return false
	})
	e.robotState("robot-1", func(rb *robot) {
		if rb.game != nil || rb.menu != "" {
			t.Fatalf("a long press started a game or menu: game %v, menu %q", rb.game != nil, rb.menu)
		}
	})
}

// By day the screen goes off when nobody plays for ScreenIdleMin minutes; a touch (the robot
// lights it itself: screensaver_off) or play from a phone brings it back.
func TestScreenOffWhenNobodyPlays(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectRobot("robot-1")
	kid := e.browser()
	kid.pair(r)
	screen := func() (off, idle bool) {
		e.app.mu.Lock()
		defer e.app.mu.Unlock()
		rb := e.app.robots["robot-1"]
		return rb.screenOff, rb.idleOff
	}
	e.clock.set(e.clock.now().Add(4 * time.Minute))
	e.app.Pulse()
	if off, _ := screen(); off {
		t.Fatal("off after 4 minutes (the setting is 5)")
	}
	e.clock.set(e.clock.now().Add(2 * time.Minute))
	e.app.Pulse()
	if on := r.command("screensaver")["on"]; on != true {
		t.Fatalf("after 6 minutes: screensaver %v", on)
	}
	if off, idle := screen(); !off || !idle {
		t.Fatal("not marked off")
	}
	r.event("screensaver_off", nil) // a touch lit it
	time.Sleep(50 * time.Millisecond)
	if off, _ := screen(); off {
		t.Fatal("still off after the touch")
	}
	// Off again, then food from the phone brings it back.
	e.clock.set(e.clock.now().Add(6 * time.Minute))
	e.app.Pulse()
	r.command("screensaver")
	kid.post("/api/action", map[string]string{"action": "feed", "food": "apple"})
	if on := r.command("screensaver")["on"]; on != false {
		t.Fatalf("play from the phone: screensaver %v", on)
	}
}

// The manager learns who is paired (device, since) and may remove a pairing.
func TestPairingsListedAndRemoved(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectRobot("robot-1")
	r.frame(wire.KindAccepted)
	kid := e.browser()
	kid.pair(r)
	r.frame(wire.KindPaired)
	kid.get("/api/state")
	e.app.mu.Lock()
	p := e.app.pairings("robot-1")
	e.app.mu.Unlock()
	if len(p) != 1 || p[0].Device == "" || p[0].Paired.IsZero() || p[0].LastSeen.IsZero() || len(p[0].ID) != 12 {
		t.Fatalf("pairings %+v", p)
	}
	e.app.unpair("robot-1", []string{"000000000000"})
	if code, _ := kid.get("/api/state"); code != 200 {
		t.Fatalf("another id removed this one: %d", code)
	}
	e.app.unpair("robot-1", []string{p[0].ID})
	r.frame(wire.KindPaired) // the robot is told
	if code, out := kid.get("/api/state"); code != http.StatusNotFound || out["error"] != "not_paired" {
		t.Fatalf("after removal: %d %v", code, out)
	}
}
