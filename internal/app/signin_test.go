package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/i41-eu/q41eu-raw/sso"
)

// signInManager is a fake Stackchan manager: robot-auth (robot-2 with own-token, owned by ema)
// and the sign-on, signing in whoever is in next.
type signInManager struct {
	ts      *httptest.Server
	mu      sync.Mutex
	next    *sso.Account
	handles map[string]sso.Account
}

func newSignInManager(t *testing.T) *signInManager {
	m := &signInManager{handles: map[string]sso.Account{}}
	codes := map[string]sso.Account{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sso", func(w http.ResponseWriter, r *http.Request) {
		ret := r.URL.Query().Get("return")
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.next == nil {
			http.Redirect(w, r, ret+"&error=login_required", http.StatusFound)
			return
		}
		code := "c" + m.next.Key
		codes[code] = *m.next
		m.next = nil
		http.Redirect(w, r, ret+"&code="+code, http.StatusFound)
	})
	mux.HandleFunc("POST /api/", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Robot, Token, Code, Handle string }
		json.NewDecoder(r.Body).Decode(&req)
		m.mu.Lock()
		defer m.mu.Unlock()
		switch r.URL.Path {
		case "/api/robot-auth":
			ok := req.Robot == "robot-2" && req.Token == "own-token"
			json.NewEncoder(w).Encode(map[string]any{"ok": ok, "owner": "ema", "cache_s": 60})
		case "/api/sso/token":
			a, ok := codes[req.Code]
			delete(codes, req.Code)
			if ok {
				m.handles["h"+a.Key] = a
			}
			json.NewEncoder(w).Encode(sso.Answer{OK: ok, Handle: "h" + a.Key, Account: &a, CacheS: 60})
		case "/api/sso/check":
			a, ok := m.handles[req.Handle]
			json.NewEncoder(w).Encode(sso.Answer{OK: ok, Account: &a})
		case "/api/sso/logout":
			delete(m.handles, req.Handle)
			w.WriteHeader(http.StatusNoContent)
		}
	})
	m.ts = httptest.NewServer(mux)
	t.Cleanup(m.ts.Close)
	return m
}

func (m *signInManager) signInNext(key, name string) {
	m.mu.Lock()
	m.next = &sso.Account{Key: key, Name: name, Provider: "github"}
	m.mu.Unlock()
}

// With sign-in, the parent page is the robot owner's (from the manager), without a PIN; the
// owner gets the robot without the QR code; a kid pairs by the QR code and gets no parent page.
func TestParentSignsIn(t *testing.T) {
	m := newSignInManager(t)
	e := newEnv(t, "")
	e.useManager(m.ts.URL)
	e.app.cfg.SignIn = sso.New(m.ts.URL, "app-secret")
	r := e.connectRobotAs("robot-2", "own-token", nil)

	kid := e.browser()
	kid.pair(r) // no hint from the manager: no trip there
	if code, out := kid.get("/api/parent"); code != http.StatusOK || out["unlocked"] != false || out["sign_in"] != true || out["signed_in"] != false {
		t.Fatalf("kid: %d %v", code, out)
	}
	if code, out := kid.post("/api/parent/unlock", map[string]string{"pin": "1234"}); code != http.StatusConflict || out["error"] != "sign_in" {
		t.Fatalf("a PIN with sign-in: %d %v", code, out)
	}
	if code, _ := kid.post("/api/parent/settings", map[string]any{}); code != http.StatusForbidden {
		t.Fatalf("kid changes settings: %d", code)
	}

	// Someone else, signed in and paired by the code: still not a parent of this robot.
	jan := e.browser()
	m.signInNext("jan", "Jan")
	jan.get("/auth/login?next=/parent")
	jan.pair(r)
	if code, out := jan.get("/api/parent"); code != http.StatusOK || out["unlocked"] != false || out["signed_in"] != true {
		t.Fatalf("another account: %d %v", code, out)
	}

	// The owner signs in: the robot is theirs without the QR code, and the parent page is open.
	ema := e.browser()
	m.signInNext("ema", "Ema")
	if code, _ := ema.get("/auth/login?next=/parent"); code != http.StatusOK {
		t.Fatalf("owner's sign-in: %d", code)
	}
	if code, out := ema.get("/api/parent"); code != http.StatusOK || out["unlocked"] != true || out["name"] != "Ema" {
		t.Fatalf("owner: %d %v", code, out)
	}

	// Signed out at the manager (or in another app): locked again after the check.
	m.mu.Lock()
	m.handles = map[string]sso.Account{}
	m.mu.Unlock()
	e.app.cfg.SignIn = sso.New(m.ts.URL, "app-secret") // no cached answer
	e.app.checkSignIns(t.Context())
	if _, out := ema.get("/api/parent"); out["unlocked"] != false || out["signed_in"] != false {
		t.Fatalf("owner after signing out everywhere: %v", out)
	}
}

// The manager joins a person's sign-ins into one user: the session's account becomes the user's at
// the next check, and with it the robot that user owns (its parent page), without signing in again.
func TestParentJoinedAtTheManager(t *testing.T) {
	m := newSignInManager(t)
	e := newEnv(t, "")
	e.useManager(m.ts.URL)
	e.app.cfg.SignIn = sso.New(m.ts.URL, "app-secret")
	e.connectRobotAs("robot-2", "own-token", nil) // owned by "ema"

	ema := e.browser()
	m.signInNext("ema-google", "Ema") // another sign-in of hers, not yet joined
	ema.get("/auth/login?next=/parent")
	if _, out := ema.get("/api/parent"); out["unlocked"] == true || out["signed_in"] != true {
		t.Fatalf("before the join: %v", out)
	}
	m.mu.Lock()
	for h, a := range m.handles {
		a.Key = "ema"
		m.handles[h] = a
	}
	m.mu.Unlock()
	e.app.cfg.SignIn = sso.New(m.ts.URL, "app-secret") // no cached answer
	e.app.checkSignIns(t.Context())
	if _, out := ema.get("/api/parent"); out["unlocked"] != true {
		t.Fatalf("after the join: %v", out)
	}
}
