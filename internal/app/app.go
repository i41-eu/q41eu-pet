// Package app is the pet server: robots connect over the Embody Mode protocol
// (github.com/i41-eu/q41eu-raw/wire), each robot has one pet, kids play
// on the robot and on a picture page, parents change settings behind a PIN.
//
// Pairing works like q41eu-raw: the robot shows <public-url>/pair?code=...
// as a QR code; the browser that opens it may see and play with that pet.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/i41-eu/q41eu-dev/robothub"
	"github.com/i41-eu/q41eu-pet/internal/pet"
	"github.com/i41-eu/q41eu-pet/internal/voice"
	"github.com/i41-eu/q41eu-raw/robotauth"
	"github.com/i41-eu/q41eu-raw/sso"
	"github.com/i41-eu/q41eu-raw/statestore"
	"github.com/i41-eu/q41eu-raw/wire"
)

// Config configures an App.
type Config struct {
	RobotToken   string            // the shared bearer token of the owner's robots (and of /api/debug)
	Manager      *robotauth.Client // robots set up by a Stackchan manager, with a token of their own; nil = none
	SignIn       *sso.Client       // sign-in through that manager (signin.go): the parent page is the owner's; nil = PIN
	SignInAlways bool              // try a silent sign-in without the manager's hint cookie (sso.SilentTry): this app on another domain than the manager's -sign-in-hint-domain
	AdminEmails  []string          // with SignIn: parents of the robots with the shared token
	ManagerURL   string            // the manager's page for people (the parent page links to it); "" = none
	PublicURL    string            // base URL browsers use, e.g. http://192.168.1.10:8770
	PairTTL      time.Duration     // lifetime of a pairing code
	StateFile    string            // JSON file with pets and pairings; "" keeps them in memory only
	UIDir        string            // development: serve the pages from this directory (e.g. internal/app/ui)
	Location     *time.Location    // the family's time zone for the schedule; default time.Local
	Log          *slog.Logger
	Now          func() time.Time // tests; default time.Now
	DebugDir     string           // where screen snapshots from the robot are saved (debug.go); "" = not saved
	PhotoDir     string           // the color game's leaderboard photos (photos.go) next to StateFile; "" = no photos
	// State: where pets, pairings and photos go instead of StateFile and PhotoDir (statestore: files,
	// or Postgres in the cluster); nil = StateFile and PhotoDir.
	State statestore.Store
	Voice *voice.Synth // the pet's voice (voice.go); nil = silent lines
}

// App holds all state. One mutex guards everything; robot sockets only queue messages.
type App struct {
	cfg Config
	log *slog.Logger

	mu       sync.Mutex
	robots   map[string]*robot
	hub      *robothub.Hub
	sessions map[string]*session
	subs     map[*subscriber]struct{}
	dirty    bool   // state changed since the last save
	timing   timing // the game's pace (game.go)
	saveMu   sync.Mutex
}

// session is one browser. Robots are its paired robots, the most recent first.
type session struct {
	Robots []string `json:"robots"`

	parentUntil  time.Time // the parent page is unlocked until then
	parentDevice bool      // a parent's phone: the parent page stays unlocked until locked
	locked       bool      // a parent's device locked with the button: the PIN again
	pinFails     int
	pinFailAt    time.Time

	account *sso.Account // signed in through the manager (signin.go)
	handle  string       // that sign-in's handle at the manager

	meta sessionMeta // device, last seen, paired since (pairings.go)
}

// robot is one robot and its pet; it stays after the robot disconnects.
type robot struct {
	id          string
	pet         *pet.Pet
	pinHash     string               // "salt:sha256hex" of the parent PIN, "" = not set yet
	owner       string               // the owner's account key, from the manager; "" = the shared token
	unknownTags map[string]time.Time // NFC tags without a chosen food: uid -> last seen

	conn     *robotConn // nil while offline
	lastSeen time.Time

	// What the robot shows (not saved): the engine compares and reacts on changes.
	shownMood     pet.Mood
	phase         pet.Phase
	lastNag       time.Time
	lastHello     time.Time
	bedWarned     string            // the date of the last "bedtime soon" warning
	lastAsleep    time.Time         // last sleepy answer at night, to not repeat it on every touch
	pictureOn     bool              // a picture covers the face
	screenOff     bool              // the pet turned the robot's screen off for the night
	idleOff       bool              // ... or because nobody played for a while (Settings.ScreenIdleMin)
	lastPlay      time.Time         // the last reaction: a touch, food, play (from the robot or a phone)
	busyUntil     time.Time         // a reaction shows until then; the mood waits
	gen           int               // bumped by each reaction; delayed steps of an older one are dropped
	game          *game             // a game of catch in progress (game.go)
	uploading     map[string]uint32 // pet files being uploaded -> their CRC-32 (assets.go)
	commands      []string          // what this robot's firmware accepts
	files         map[string]bool   // pet files on the robot, ready to show as sprites
	spriteIDs     map[string]bool   // the pet's sprites on screen (not the face): cleared with the mood
	faceShown     string            // the drawn face shown as the bottom sprite ("" = the robot's own face)
	faceHidden    bool              // the drawn face steps aside for speech or a full-screen picture
	lastCard      time.Time         // the last food card: head touches around it are not cuddles
	cuddleGen     int               // bumped by a card or a newer touch: a waiting touch is dropped
	touchAt       time.Time         // the head touch going on (engine.go), zero when none
	touchZone     float64           // its strongest zone (0-3) at the start
	touchSeen     bool              // it was already reacted to (a long touch, scratching)
	touchID       int
	strokes       []time.Time // recent head swipes
	lastScratch   time.Time
	menu          string    // the menu on the robot's screen ("" = none; menu.go)
	sleptAt       time.Time // the last good night: the night light fades from there
	ledLevel      float64   // the night light's level last sent
	dimmed        bool      // the pet dimmed the screen for the night
	screenOnAt    time.Time // the screen was last lit at night: off nightScreenOn later
	menuID        int
	menuBack      string             // where the menu's back button goes ("" = the face)
	voiceGen      int                // bumped by each spoken line: an older one stops
	speakingUntil time.Time          // a line is being spoken: effect sounds wait (one speaker queue)
	recording     *recording         // a debug recording of the robot's microphone (debug.go)
	head          *[2]float64        // yaw, pitch (degrees) from the robot's telemetry; nil until known
	telemetry     map[string]float64 // the robot's last telemetry (every 2 s)
	headWant      float64            // the pitch the pet last put the head at (0 = headRestPitch)
	lastLift      time.Time          // the last time it lifted a sunken head
	headBefore    *[2]float64        // where the head was before a game or dance: it goes back there
	screenManual  bool               // the screen was blanked on purpose (a double tap, the night): no keep-alive
	sentSeen      uint64             // the connection's sent count the watchdog saw last
	droppedSeen   uint64
	quietSince    time.Time // nothing was sent to the robot since then
	lastSchool    time.Time // the last "I rest during school" (engine.go)
	photoFor      time.Time // a leaderboard photo is awaited for the entry made then (photos.go)
	photoUntil    time.Time // ... until then
	barStep       int       // the energy bar shown while asleep (energyBar)
	hold          holdState // held in someone's hands (held.go)
}

func New(cfg Config) *App {
	if cfg.PairTTL <= 0 {
		cfg.PairTTL = 5 * time.Minute
	}
	if cfg.Location == nil {
		cfg.Location = time.Local
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	a := &App{
		cfg:      cfg,
		log:      cfg.Log,
		robots:   map[string]*robot{},
		sessions: map[string]*session{},
		subs:     map[*subscriber]struct{}{},
		timing:   defaultTiming,
	}
	a.hub = a.newHub()
	if a.cfg.State == nil && cfg.StateFile != "" {
		a.cfg.State = &statestore.File{Path: cfg.StateFile, BlobDir: cfg.PhotoDir}
	}
	if a.cfg.State != nil {
		if err := a.load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
			a.log.Warn("state not loaded, starting empty", "state", a.cfg.State.Where(), "err", err)
		}
	}
	return a
}

// now is the current time in the family's time zone.
func (a *App) now() time.Time { return a.cfg.Now().In(a.cfg.Location) }

// Handler returns the HTTP routes.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET "+wire.ConnectPath, a.hub)
	mux.HandleFunc("GET /{$}", a.trySignIn(a.page("index.html")))
	mux.HandleFunc("GET /parent", a.trySignIn(a.page("parent.html")))
	mux.HandleFunc("GET /pair", a.trySignIn(a.handlePair))
	mux.HandleFunc("GET /auth/login", a.handleLogin)
	mux.HandleFunc("GET /auth/sso", a.handleSSOReturn)
	mux.HandleFunc("POST /auth/logout", a.handleLogout)
	mux.HandleFunc("GET /api/state", a.handleState)
	mux.HandleFunc("GET /api/events", a.handleEvents)
	mux.HandleFunc("POST /api/action", a.handleAction)
	mux.HandleFunc("GET /api/parent", a.handleParent)
	mux.HandleFunc("POST /api/parent/unlock", a.handleUnlock)
	mux.HandleFunc("POST /api/parent/lock", a.handleLock)
	mux.HandleFunc("POST /api/parent/device", a.handleDevice)
	mux.HandleFunc("POST /api/parent/settings", a.handleSettings)
	mux.HandleFunc("POST /api/parent/pin", a.handlePIN)
	mux.HandleFunc("POST /api/parent/reset", a.handleReset)
	mux.HandleFunc("POST /api/parent/try", a.handleTry)
	mux.HandleFunc("POST /api/parent/stats", a.handleStats)
	mux.HandleFunc("POST /api/parent/top", a.handleTop)
	mux.HandleFunc("GET /api/photos/{name}", a.handlePhoto)
	mux.HandleFunc("POST /api/debug/{id}/run", a.handleDebugRun)
	mux.Handle("GET /emoji/", http.StripPrefix("/emoji/", a.uiFiles("emoji")))
	mux.Handle("GET /chan/", http.StripPrefix("/chan/", a.uiFiles("chan")))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	return mux
}

// robotFor returns the robot, creating it and its pet on first sight. a.mu held.
func (a *App) robotFor(id string) *robot {
	r := a.robots[id]
	if r == nil {
		r = &robot{id: id, pet: pet.New(a.now(), pet.DefaultSettings()), unknownTags: map[string]time.Time{}}
		a.robots[id] = r
		a.dirty = true
	}
	return r
}

/* --------------------------------- pairing -------------------------------- */

// Unambiguous characters only (no 0/O, 1/I), easy to read off a screen.
// redeem pairs the session with the code's robot (first in its list). a.mu held.
func (a *App) redeem(sid, code string) (*robot, bool) {
	id, ok := a.hub.Redeem(code) // the hub gives the robot a fresh code for the next browser
	if !ok {
		return nil, false
	}
	s := a.sessionFor(sid)
	s.Robots = append([]string{id}, slices.DeleteFunc(s.Robots, func(x string) bool { return x == id })...)
	a.pairedNow(s, id)
	a.dirty = true
	return a.robotFor(id), true
}

// viewers is how many browsers are paired with the robot. a.mu held.
func (a *App) viewers(robotID string) int {
	n := 0
	for _, s := range a.sessions {
		if slices.Contains(s.Robots, robotID) {
			n++
		}
	}
	return n
}

// paired is what the robot shows: browsers paired, and of them with a page open now. a.mu held.
func (a *App) paired(robotID string) wire.PairedBody {
	seen := map[string]bool{}
	for sub := range a.subs {
		if s := a.sessions[sub.sid]; s != nil && slices.Contains(s.Robots, robotID) {
			seen[sub.sid] = true
		}
	}
	return wire.PairedBody{Viewers: a.viewers(robotID), Watching: len(seen)}
}

// tellWatching sends the session's robots how many watch now (a page opened or closed). a.mu held.
func (a *App) tellWatching(sid string) {
	if s := a.sessions[sid]; s != nil {
		for _, id := range s.Robots {
			if rb := a.robots[id]; rb != nil && rb.conn != nil {
				rb.conn.frame(wire.KindPaired, a.paired(id))
			}
		}
	}
}

/* -------------------------------- sessions -------------------------------- */

const sessionCookie = "stackchan_pet_session"

// sessionID returns the browser's session id, setting a new cookie if needed.
func (a *App) sessionID(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(sessionCookie); err == nil && validSessionID(c.Value) {
		a.seen(c.Value, r)
		return c.Value
	}
	b := make([]byte, 32)
	rand.Read(b)
	id := hex.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		MaxAge:   int((365 * 24 * time.Hour) / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || strings.HasPrefix(a.cfg.PublicURL, "https://"),
	})
	return id
}

func validSessionID(v string) bool {
	if len(v) != 64 {
		return false
	}
	_, err := hex.DecodeString(v)
	return err == nil
}

// sessionFor returns the session, creating it. a.mu held.
func (a *App) sessionFor(sid string) *session {
	s := a.sessions[sid]
	if s == nil {
		s = &session{}
		a.sessions[sid] = s
	}
	return s
}

// pairedRobot is the session's robot: ?robot= if paired, else the most recent. a.mu held.
func (a *App) pairedRobot(sid, want string) *robot {
	s := a.sessions[sid]
	if s == nil {
		return nil
	}
	for _, id := range s.Robots {
		if want == "" || id == want {
			return a.robots[id]
		}
	}
	return nil
}

/* ---------------------------------- state --------------------------------- */

// stateFile is what is saved: pets and pairings, not live connections.
type stateFile struct {
	Robots   map[string]savedRobot `json:"robots"`
	Sessions map[string][]string   `json:"sessions"`
	// Parent devices (session id -> locked): their parent page stays unlocked.
	ParentDevices map[string]bool `json:"parent_devices,omitempty"`
	// Sessions signed in through the manager (signin.go).
	SignIns map[string]savedSignIn `json:"sign_ins,omitempty"`
	// Device, last seen and paired since of the paired sessions (pairings.go).
	Meta map[string]sessionMeta `json:"sessions_meta,omitempty"`
}

type savedSignIn struct {
	Account sso.Account `json:"account"`
	Handle  string      `json:"handle"`
}

type savedRobot struct {
	Pet         *pet.Pet             `json:"pet"`
	PINHash     string               `json:"pin_hash,omitempty"`
	Owner       string               `json:"owner,omitempty"`
	UnknownTags map[string]time.Time `json:"unknown_tags,omitempty"`
}

func (a *App) load() error {
	b, err := a.cfg.State.Load(context.Background())
	if err != nil {
		return err
	}
	if b == nil {
		return fs.ErrNotExist
	}
	var st stateFile
	if err := json.Unmarshal(b, &st); err != nil {
		return err
	}
	for id, sr := range st.Robots {
		if sr.Pet == nil {
			continue
		}
		sr.Pet.Settings.Normalize()
		if sr.UnknownTags == nil {
			sr.UnknownTags = map[string]time.Time{}
		}
		a.robots[id] = &robot{id: id, pet: sr.Pet, pinHash: sr.PINHash, owner: sr.Owner, unknownTags: sr.UnknownTags}
	}
	for sid, ids := range st.Sessions {
		if validSessionID(sid) {
			a.sessions[sid] = &session{Robots: ids, meta: st.Meta[sid]}
		}
	}
	for sid, si := range st.SignIns {
		if validSessionID(sid) && si.Handle != "" && a.cfg.SignIn != nil {
			s := a.sessionFor(sid)
			acct := si.Account
			s.account, s.handle = &acct, si.Handle
		}
	}
	for sid, locked := range st.ParentDevices {
		if s := a.sessions[sid]; s != nil {
			s.parentDevice, s.locked = true, locked
		}
	}
	a.log.Info("state loaded", "state", a.cfg.State.Where(), "pets", len(a.robots), "sessions", len(a.sessions))
	return nil
}

// Save saves the state (cfg.State) if anything changed.
func (a *App) Save() error {
	if a.cfg.State == nil {
		return nil
	}
	a.saveMu.Lock()
	defer a.saveMu.Unlock()
	a.mu.Lock()
	if !a.dirty {
		a.mu.Unlock()
		return nil
	}
	st := stateFile{Robots: map[string]savedRobot{}, Sessions: map[string][]string{}, ParentDevices: map[string]bool{},
		SignIns: map[string]savedSignIn{}, Meta: map[string]sessionMeta{}}
	a.expirePairings()
	for id, r := range a.robots {
		r.pet.Advance(a.now())
		st.Robots[id] = savedRobot{Pet: r.pet, PINHash: r.pinHash, Owner: r.owner, UnknownTags: r.unknownTags}
	}
	for sid, s := range a.sessions {
		if s.account != nil {
			st.SignIns[sid] = savedSignIn{Account: *s.account, Handle: s.handle}
		}
		if len(s.Robots) > 0 {
			st.Sessions[sid] = s.Robots
			st.Meta[sid] = s.meta
			if s.parentDevice {
				st.ParentDevices[sid] = s.locked
			}
		}
	}
	b, err := json.MarshalIndent(st, "", "  ")
	a.dirty = false
	a.mu.Unlock()
	if err != nil {
		return err
	}
	if err := a.cfg.State.Save(context.Background(), b); err != nil {
		a.mu.Lock()
		a.dirty = true // try again next time
		a.mu.Unlock()
		return err
	}
	return nil
}

// Run drives the pets and passes on the manager's app lists (every 15 s: a switch the owner
// asked for on the manager's page comes soon) and saves the state (every minute) until ctx ends.
func (a *App) Run(ctx context.Context) {
	go a.warmVoice(ctx)
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	pulse := time.NewTicker(2 * time.Second)
	defer pulse.Stop()
	save := time.NewTicker(time.Minute)
	defer save.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			a.Tick()
			go a.reportAllSeen(ctx)
		case <-pulse.C:
			a.Pulse()
		case <-save.C:
			if err := a.Save(); err != nil {
				a.log.Warn("state not saved", "err", err)
			}
		}
	}
}
