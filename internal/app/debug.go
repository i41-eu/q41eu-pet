package app

import (
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/i41-eu/q41eu-pet/internal/pet"
)

// Debugging from the terminal (LAN development): with the robot token as a bearer,
//
//	POST /api/debug/{id}/run {"action": "feed", "food": "banana", "shots_ms": [200, 600, 1200]}
//
// runs a kid's action on the robot's pet (optional) and asks the robot for screen
// snapshots at those moments. The robot sends them as JPEGs (binary 0x07); they are
// saved in Config.DebugDir as <robot>-<time>.jpg (the newest 40 are kept).

const keepScreens = 40

// tokenOK: the shared robot token (the debug endpoint's key).
func (a *App) tokenOK(token string) bool {
	return a.cfg.RobotToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(a.cfg.RobotToken)) == 1
}

func (a *App) handleDebugRun(w http.ResponseWriter, r *http.Request) {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !a.tokenOK(token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Action   string `json:"action"`
		Food     string `json:"food"`
		Menu     string `json:"menu"`      // for "menu": which one (default main)
		RecordMs int    `json:"record_ms"` // record the robot's microphone (ch0 = what the speaker plays) to DebugDir
		ShotsMs  []int  `json:"shots_ms"`
		Commands []struct {
			Command string         `json:"command"`
			Args    map[string]any `json:"args"`
		} `json:"commands"` // raw robot commands, sent first
	}
	if !readJSON(w, r, &req) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	rb := a.robots[r.PathValue("id")]
	if rb == nil || rb.conn == nil {
		http.Error(w, "robot offline", http.StatusConflict)
		return
	}
	for _, c := range req.Commands {
		rb.conn.command(c.Command, c.Args)
	}
	now := a.now()
	var re pet.Reaction
	switch req.Action {
	case "":
	case "feed":
		re = rb.pet.Feed(now, req.Food)
	case "cuddle":
		re = rb.pet.Cuddle(now)
	case "nap":
		rb.pet.Stats.Energy = min(rb.pet.Stats.Energy, 30) // tired enough to nap
		re = rb.pet.Nap(now)
	case "play":
		re = a.playAction(rb, now)
	case "colors":
		re = a.colorsAction(rb, now)
	case "dream":
		rb.lastAsleep = time.Time{}
		a.dream(rb, now)
	case "express":
		a.express(rb, now)
	case "say": // a spoken line (even at night), e.g. to watch the robot's memory
		saved := rb.pet.Settings.Sounds
		rb.pet.Settings.Sounds = true
		a.sayLine(rb, text(rb.pet.Settings.Lang, "hungry", ""), 3, true)
		rb.pet.Settings.Sounds = saved
	case "menu":
		if _, ok := menus[req.Menu]; !ok {
			req.Menu = "main"
		}
		a.openMenu(rb, now, req.Menu)
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if re.Kind != "" {
		a.dirty = true
		a.react(rb, re, now)
		a.publishReaction(rb, re)
		a.publishState(rb)
	}
	if req.RecordMs > 0 {
		a.record(rb, time.Duration(min(req.RecordMs, 30000))*time.Millisecond)
	}
	for _, ms := range req.ShotsMs {
		conn := rb.conn
		time.AfterFunc(time.Duration(ms)*time.Millisecond, func() { conn.command("screen_snapshot", nil) })
	}
	files := 0
	for range rb.files {
		files++
	}
	writeJSON(w, http.StatusOK, map[string]any{"reaction": re, "shots": len(req.ShotsMs), "dir": a.cfg.DebugDir,
		"commands": rb.commands, "files": files, "sprites": rb.canSprite(assetDir + "ball.png"), "face": rb.faceShown, "menu": rb.canMenu(), "telemetry": rb.telemetry,
		"state": map[string]any{"stats": rb.pet.Stats, "mood": rb.pet.Mood(now), "phase": rb.pet.Phase(now), "demo": rb.pet.Settings.Demo,
			"napping": rb.pet.Napping(now), "nap_until": rb.pet.NapUntil, "shown_mood": rb.shownMood, "busy_until": rb.busyUntil,
			"menu": rb.menu, "game": rb.game != nil, "now": now}})
}

// saveScreen keeps a screen snapshot from the robot (binary 0x07) in DebugDir.
func (a *App) saveScreen(id string, jpeg []byte) {
	if a.cfg.DebugDir == "" {
		return
	}
	if err := os.MkdirAll(a.cfg.DebugDir, 0o755); err != nil {
		return
	}
	name := filepath.Join(a.cfg.DebugDir, fmt.Sprintf("%s-%s.jpg", id, time.Now().Format("150405.000")))
	if err := os.WriteFile(name, jpeg, 0o644); err != nil {
		return
	}
	files, _ := filepath.Glob(filepath.Join(a.cfg.DebugDir, "*.jpg"))
	sort.Strings(files)
	for len(files) > keepScreens {
		os.Remove(files[0])
		files = files[1:]
	}
}

// record turns the robot's microphone on for d and saves what arrives (binary 0x04:
// uint16 rate, uint8 channels, interleaved s16le; channel 0 is the speaker's own output)
// as a WAV in DebugDir. a.mu held.
func (a *App) record(r *robot, d time.Duration) {
	r.recording = &recording{}
	r.conn.command("mic", map[string]any{"on": true})
	conn := r.conn
	time.AfterFunc(d, func() {
		a.mu.Lock()
		rec := r.recording
		r.recording = nil
		a.mu.Unlock()
		conn.command("mic", map[string]any{"on": false})
		if rec == nil || rec.rate == 0 || a.cfg.DebugDir == "" {
			return
		}
		name := filepath.Join(a.cfg.DebugDir, fmt.Sprintf("%s-%s.wav", r.id, time.Now().Format("150405")))
		os.MkdirAll(a.cfg.DebugDir, 0o755)
		os.WriteFile(name, wavOf(rec.pcm, rec.rate, rec.channels), 0o644)
		a.log.Info("recording saved", "file", name, "seconds", float64(len(rec.pcm))/float64(2*rec.rate*rec.channels))
	})
}

type recording struct {
	rate, channels int
	pcm            []byte
}

// micAudio keeps a microphone message while recording.
func (a *App) micAudio(id string, payload []byte) {
	if len(payload) < 3 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.robots[id]
	if r == nil || r.recording == nil {
		return
	}
	r.recording.rate = int(payload[0]) | int(payload[1])<<8
	r.recording.channels = int(payload[2])
	r.recording.pcm = append(r.recording.pcm, payload[3:]...)
}

func wavOf(pcm []byte, rate, channels int) []byte {
	b := make([]byte, 44, 44+len(pcm))
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+len(pcm)))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], uint16(channels))
	binary.LittleEndian.PutUint32(b[24:], uint32(rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(rate*channels*2))
	binary.LittleEndian.PutUint16(b[32:], uint16(channels*2))
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(pcm)))
	return append(b, pcm...)
}
