// Command q41eu-pet is a Tamagotchi for Stackchan robots in Embody Mode.
//
// Robots connect to ws://<host>/api/devices/connect with a bearer token (by
// default the same token file as q41eu-raw, so that server can offer
// this one to its robots), or with a token of their own from a Stackchan
// manager (-manager-url, i41eu-manager), which this app checks with it. Kids open http://<host>/ after scanning the robot's
// QR code; parents open /parent and set a PIN.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/i41-eu/q41eu-pet/internal/app"
	"github.com/i41-eu/q41eu-pet/internal/voice"
	"github.com/i41-eu/q41eu-raw/robotauth"
	"github.com/i41-eu/q41eu-raw/sso"
	"github.com/i41-eu/q41eu-raw/statestore"
)

func main() {
	var (
		listen       = flag.String("listen", ":8770", "HTTP listen address for robots and browsers")
		publicURL    = flag.String("public-url", "", "base URL browsers use to reach this server (default: http://<LAN IP>:<port>)")
		tokenFile    = flag.String("token-file", defaultConfigFile("stackchan-server", "robot-token"), "file with the robot bearer token; generated if missing")
		stateFile    = flag.String("state-file", defaultStateFile(), "JSON file with pets and pairings (\"\" keeps them in memory only)")
		stateDB      = flag.String("state-database", os.Getenv("STATE_DATABASE_URL"), "keep pets, pairings and photos in Postgres instead (postgres://user@host/db; the password from PGPASSWORD); a -state-file that exists (and its photos) is imported once, into an empty database")
		tz           = flag.String("tz", "", "the family's time zone for the schedule, e.g. Europe/Prague (default: this machine's)")
		uiDir        = flag.String("ui-dir", "", "development: serve the pages from this directory (e.g. internal/app/ui), so edits need only a reload")
		debug        = flag.Bool("debug", false, "debug logging")
		ffmpeg       = flag.String("ffmpeg", lookPath("ffmpeg"), "ffmpeg for the Edge voice (\"\" = no Edge voice)")
		espeak       = flag.String("espeak", lookPath("espeak-ng"), "espeak-ng, the fallback voice (\"\" = none)")
		voiceDir     = flag.String("voice-dir", defaultCacheDir("voice"), "where the spoken lines are kept")
		noVoice      = flag.Bool("no-voice", false, "the pet does not speak")
		debugDir     = flag.String("debug-dir", defaultCacheDir("screens"), "where screen snapshots from the robot are saved (POST /api/debug/{id}/run)")
		mgrURL       = flag.String("manager-url", "", "the Stackchan manager that set robots up with tokens for this app, e.g. https://m.i41.eu (\"\" = none)")
		mgrSecret    = flag.String("manager-secret-file", "", "file with this app's secret at the manager")
		mgrSignIn    = flag.Bool("manager-sign-in", false, "sign in through the manager (-manager-url): the parent page is the robot owner's, no PIN")
		signInAlways = flag.Bool("sign-in-always", false, "with -manager-sign-in: try a silent sign-in at the manager also without its hint cookie (this app on another domain than the manager's -sign-in-hint-domain), at most once per 10 minutes per browser")
		admins       = flag.String("admin-emails", "", "with -manager-sign-in: comma-separated verified e-mails, the parents of robots with the shared token")
	)
	flag.Parse()

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	fail := func(msg string, err error) {
		log.Error(msg, "err", err)
		os.Exit(1)
	}

	token, err := loadOrCreateToken(*tokenFile, log)
	if err != nil {
		fail("robot token", err)
	}
	if *publicURL == "" {
		if *publicURL, err = defaultPublicURL(*listen); err != nil {
			fail("public URL", err)
		}
	}
	loc := time.Local
	if *tz != "" {
		if loc, err = time.LoadLocation(*tz); err != nil {
			fail("time zone", err)
		}
	}

	var synth *voice.Synth
	if !*noVoice && (*ffmpeg != "" || *espeak != "") {
		synth = &voice.Synth{FFmpeg: *ffmpeg, Espeak: *espeak, CacheDir: *voiceDir}
	}
	var manager *robotauth.Client
	var signIn *sso.Client
	if *mgrURL != "" {
		b, err := os.ReadFile(*mgrSecret)
		if err != nil || strings.TrimSpace(string(b)) == "" {
			fail("manager secret file", fmt.Errorf("%s: %v", *mgrSecret, err))
		}
		manager = robotauth.New(*mgrURL, strings.TrimSpace(string(b)))
		if *mgrSignIn {
			signIn = sso.New(*mgrURL, strings.TrimSpace(string(b)))
		}
	} else if *mgrSignIn {
		fail("-manager-sign-in", errors.New("needs -manager-url and -manager-secret-file"))
	}
	var state statestore.Store
	if *stateDB != "" {
		openCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute) // a database that is down for a moment (retried), or another copy's lock
		var from *statestore.File
		if *stateFile != "" {
			from = &statestore.File{Path: *stateFile, BlobDir: photoDir(*stateFile)}
		}
		st, err := statestore.Open(openCtx, *stateDB, "pet", from)
		cancel()
		if err != nil {
			fail("state database", err)
		}
		state = st
		log.Info("state in the database", "where", st.Where())
	}
	a := app.New(app.Config{
		RobotToken:   token,
		Manager:      manager,
		SignIn:       signIn,
		SignInAlways: *signInAlways,
		AdminEmails:  splitList(*admins),
		ManagerURL:   strings.TrimRight(*mgrURL, "/"),
		PublicURL:    strings.TrimRight(*publicURL, "/"),
		StateFile:    *stateFile,
		State:        state,
		UIDir:        *uiDir,
		Location:     loc,
		Log:          log,
		DebugDir:     *debugDir,
		PhotoDir:     photoDir(*stateFile),
		Voice:        synth,
	})
	srv := &http.Server{Addr: *listen, Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go a.Run(ctx)
	if state != nil {
		go func() { // the database's lock is gone: another copy may write now; stop here
			select {
			case <-state.Lost():
				log.Error("state database: connection (and lock) lost; exiting")
				os.Exit(1)
			case <-ctx.Done():
			}
		}()
	}
	go a.RunSignInCheck(ctx)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	log.Info("q41eu-pet listening", "listen", *listen, "public_url", *publicURL, "state_file", *stateFile, "tz", loc.String(), "manager", *mgrURL)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fail("server", err)
	}
	if err := a.Save(); err != nil {
		log.Warn("state not saved", "err", err)
	}
	if state != nil {
		state.Close() // releases the lock for the next copy
	}
}

// defaultStateFile follows the XDG base directory spec: $XDG_STATE_HOME or ~/.local/state.
func defaultStateFile() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "stackchan-pet", "state.json")
}

// lookPath is the program's path, or "" when it is not installed.
func lookPath(name string) string {
	p, _ := exec.LookPath(name)
	return p
}

// defaultCacheDir is name in $XDG_CACHE_HOME/stackchan-pet (or ~/.cache).
func defaultCacheDir(name string) string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "stackchan-pet", name)
}

func defaultConfigFile(app, name string) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return name
	}
	return filepath.Join(dir, app, name)
}

// loadOrCreateToken reads the robot token, generating a random one on first run.
func loadOrCreateToken(path string, log *slog.Logger) (string, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		token := strings.TrimSpace(string(b))
		if token == "" {
			return "", fmt.Errorf("%s is empty", path)
		}
		return token, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	raw := make([]byte, 32)
	rand.Read(raw)
	token := hex.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	log.Info("generated new robot token", "path", path)
	return token, nil
}

// defaultPublicURL builds http://<LAN IP>:<port> so a phone on the same network can open the QR link.
func defaultPublicURL(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("parse -listen %q: %w", listen, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = lanIP()
	}
	return "http://" + net.JoinHostPort(host, port), nil
}

// lanIP returns the address of the interface used for outbound traffic (no packets are sent).
func lanIP() string {
	if c, err := net.Dial("udp4", "192.0.2.1:80"); err == nil {
		defer c.Close()
		return c.LocalAddr().(*net.UDPAddr).IP.String()
	}
	return "127.0.0.1"
}

// photoDir keeps the leaderboard photos next to the state file ("" without one).
func photoDir(stateFile string) string {
	if stateFile == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(stateFile), "photos")
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
