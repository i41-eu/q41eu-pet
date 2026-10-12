package app

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/i41-eu/q41eu-pet/internal/robotpic"
	"github.com/i41-eu/q41eu-raw/statestore"
	"github.com/i41-eu/q41eu-raw/wire"
)

// playColors starts the color game from the robot's menu and plays it through.
func playColors(t *testing.T, e *env, r *fakeRobot) {
	t.Helper()
	files := []string{"pet/menu-bg.png"}
	for _, items := range menus {
		for _, m := range items {
			files = append(files, "pet/"+m.asset)
		}
	}
	for _, c := range robotpic.Colors {
		files = append(files, "pet/color-"+c+".png")
	}
	haveFiles(t, r, files...)
	r.event("screen_tap", map[string]any{"x": 160, "y": 120}) // the main menu
	r.event("screen_tap", map[string]any{"x": 208, "y": 50, "sprite": "m:open:play"})
	time.Sleep(50 * time.Millisecond)
	r.event("screen_tap", map[string]any{"x": 160, "y": 100, "sprite": "m:play:colors"})

	ledColor := map[string]string{}
	for c, hex := range robotpic.ColorLED {
		ledColor[hex] = c
	}
	dark := func(px []any) bool { return !slices.ContainsFunc(px, func(p any) bool { return p != "#000000" }) }
	for level := 1; level <= colorLevels; level++ {
		r.binary(wire.BinShowJPEG) // the order picture
		for turn := 1; turn <= colorTurns; turn++ {
			var pixels []any // the colors (some lit)
			for pixels == nil {
				if args := r.command("leds"); args["pixels"] != nil && !dark(args["pixels"].([]any)) {
					pixels = args["pixels"].([]any)
				}
			}
			var parts int
			var order []int
			e.robotState("robot-1", func(rb *robot) { parts, order = rb.game.colors.parts, slices.Clone(rb.game.colors.order) })
			if want := map[int]int{1: 2, 2: 2, 3: 4, 4: 4, 5: 4}[level]; parts != want {
				t.Fatalf("level %d: %d parts", level, parts)
			}
			if level == 2 && !slices.Equal(order, []int{1, 0}) || level == 4 && !slices.Equal(order, []int{3, 2, 1, 0}) ||
				level == 5 && (slices.Equal(order, []int{0, 1, 2, 3}) || slices.Equal(order, []int{3, 2, 1, 0})) {
				t.Fatalf("level %d: order %v", level, order)
			}
			ledParts := colorParts(parts)
			if level == 1 && turn == 1 { // a wrong one first (the right strip's color, if it differs): only a buzz
				if c := ledColor[pixels[ledParts[1][0]].(string)]; c != ledColor[pixels[ledParts[0][0]].(string)] {
					r.event("screen_tap", map[string]any{"sprite": "c:" + c})
					if g := r.command("sprite"); g["opacity"] != colorGreyed {
						t.Fatalf("after a wrong button: %v", g)
					}
					time.Sleep(200 * time.Millisecond) // the penalty (shortened)
				}
			}
			for _, part := range order {
				c := ledColor[pixels[ledParts[part][0]].(string)]
				if c == "" {
					t.Fatalf("level %d: part %d dark: %v", level, part, pixels)
				}
				r.event("screen_tap", map[string]any{"sprite": "c:" + c})
			}
			for { // all pressed: every part dark
				if args := r.command("leds"); args["pixels"] != nil && dark(args["pixels"].([]any)) {
					break
				}
			}
		}
	}
}

func TestColorGame(t *testing.T) {
	e := newEnv(t, "")
	e.fastGame(300 * time.Millisecond)
	r := e.connectRobotWith("robot-1", []string{"sprite", "assets"})
	kid := e.browser()
	kid.pair(r)
	playColors(t, e, r)
	r.next("the time", func(m robotMsg) bool {
		var body wire.RobotCommandBody
		m.frame.Decode(&body)
		text, _ := body.Args["text"].(string)
		return body.Command == "say" && strings.Contains(text, "otovo za") && strings.Contains(text, "sekund")
	})
	e.robotState("robot-1", func(rb *robot) {
		if rb.game != nil || len(rb.pet.ColorsTop) == 0 {
			t.Fatalf("after the game: game %v, board %v", rb.game != nil, rb.pet.ColorsTop)
		}
	})
}

func TestSecondsText(t *testing.T) {
	for d, want := range map[time.Duration]string{time.Second: "1 sekundu", 3 * time.Second: "3 sekundy", 14 * time.Second: "14 sekund"} {
		if s := secondsText("cs", d); s != want {
			t.Errorf("%v: %q, want %q", d, s, want)
		}
	}
}

func TestLeaderboardPhoto(t *testing.T) {
	e := newEnv(t, "")
	e.fastGame(300 * time.Millisecond)
	dir := t.TempDir()
	e.app.cfg.State = &statestore.File{Path: filepath.Join(dir, "state.json"), BlobDir: filepath.Join(dir, "photos")}
	r := e.connectRobotWith("robot-1", []string{"sprite", "assets", "snapshot"})
	kid, stranger := e.browser(), e.browser()
	kid.pair(r)
	playColors(t, e, r)
	r.command("snapshot")           // first place: "smile", three, two, one
	photo := robotpic.Food("apple") // any JPEG
	r.ws.WriteMessage(websocket.BinaryMessage, append([]byte{wire.BinSnapshot}, photo...))
	r.binary(wire.BinShowJPEG) // the podium
	_, st := kid.get("/api/state")
	top := st["top"].([]any)
	if len(top) != 1 || top[0].(map[string]any)["photo"] == nil {
		t.Fatalf("the board: %v", top)
	}
	url := top[0].(map[string]any)["photo"].(string)
	if res, _ := kid.c.Get(e.srv.URL + url); res.StatusCode != 200 {
		t.Fatalf("the kid's photo: %d", res.StatusCode)
	}
	if res, _ := stranger.c.Get(e.srv.URL + url); res.StatusCode != 404 {
		t.Fatalf("a stranger got the photo: %d", res.StatusCode)
	}
}
