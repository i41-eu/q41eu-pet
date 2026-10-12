package app

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/i41-eu/q41eu-raw/statestore"

	"github.com/i41-eu/q41eu-pet/internal/pet"
	"github.com/i41-eu/q41eu-pet/internal/robotpic"
)

// The color game's leaderboard (pet.ColorsTop): the three best full games, each with
// the player's photo. A game fast enough for the board: the robot says the place,
// asks for a smile, counts down and takes a photo with its camera ("snapshot",
// arriving as BinSnapshot). The photos are files in Config.PhotoDir; the board shows
// on the robot (a podium picture) after every color game, and on the pages.
// Everything here runs with a.mu held, unless said otherwise.

// After the time is said: the place and "smile" (timing.photoAsk), "three, two,
// one" (photoCount), the photo (photoShot).
const (
	photoWait = 15 * time.Second // the photo must arrive by then
	boardShow = 8 * time.Second  // the podium on the robot
)

// canPhoto: photos are kept, the parent allows them, the robot has a camera.
func (a *App) canPhoto(r *robot) bool {
	return a.photos() && r.pet.Settings.ColorPhotos && slices.Contains(r.commands, "snapshot")
}

// boardAfterGame runs after the color game's time is said: the photo for a new place,
// then the podium; or the podium alone.
func (a *App) boardAfterGame(r *robot, re pet.Reaction, at time.Time) {
	if re.Place == 0 || !a.canPhoto(r) {
		if len(r.pet.ColorsTop) > 0 {
			a.later(r, a.timing.photoAsk, func() { a.showBoard(r) })
		}
		return
	}
	lang := r.pet.Settings.Lang
	a.later(r, a.timing.photoAsk, func() {
		a.emotion(r, "happy")
		a.say(r, "color_place", ordinalText(lang, re.Place), 4)
	})
	a.later(r, a.timing.photoCount, func() { a.say(r, "photo_count", "", 2) })
	a.later(r, a.timing.photoShot, func() {
		r.photoFor, r.photoUntil = at, time.Now().Add(photoWait)
		r.conn.command("snapshot", nil)
		r.conn.command("leds", map[string]any{"left": "#ffffff", "right": "#ffffff"}) // the flash
		gen := r.gen
		time.AfterFunc(photoWait, func() { // no photo came: the podium anyway
			a.mu.Lock()
			defer a.mu.Unlock()
			if r.gen == gen && r.conn != nil && !r.photoFor.IsZero() {
				r.photoFor = time.Time{}
				a.showBoard(r)
			}
		})
	})
}

// showBoard shows the podium on the robot for boardShow.
func (a *App) showBoard(r *robot) {
	var entries []robotpic.LeaderEntry
	for _, e := range r.pet.ColorsTop {
		var photo []byte
		if e.Photo != "" {
			photo, _ = a.cfg.State.Blob(context.Background(), e.Photo)
		}
		entries = append(entries, robotpic.LeaderEntry{Photo: photo, Seconds: int(math.Round(float64(e.Ms) / 1000))})
	}
	a.begin(r, a.now(), boardShow)
	a.clearSprites(r)
	a.showPicture(r, robotpic.Leaderboard(entries))
}

// robotSnapshot is a JPEG from the robot (BinSnapshot): the awaited leaderboard photo,
// else a screen snapshot for debugging. Takes a.mu.
func (a *App) robotSnapshot(id string, jpeg []byte) {
	a.mu.Lock()
	r := a.robots[id]
	if r == nil || r.photoFor.IsZero() || time.Now().After(r.photoUntil) {
		a.mu.Unlock()
		a.saveScreen(id, jpeg)
		return
	}
	defer a.mu.Unlock()
	at := r.photoFor
	r.photoFor = time.Time{}
	i := slices.IndexFunc(r.pet.ColorsTop, func(e pet.TopEntry) bool { return e.At.Equal(at) })
	if i < 0 {
		return // the entry left the board meanwhile (a parent cleared it)
	}
	name := fmt.Sprintf("%s-%d.jpg", safeName(r.id), at.UnixMilli())
	if err := a.cfg.State.PutBlob(context.Background(), name, jpeg); err != nil {
		a.log.Warn("photo not saved", "err", err)
		return
	}
	r.pet.ColorsTop[i].Photo = name
	a.dirty = true
	a.log.Info("leaderboard photo", "robot", r.id, "place", i+1, "file", name)
	if r.conn != nil {
		a.showBoard(r)
	}
	a.publishState(r)
}

// dropPhotos deletes the photos of entries no longer on the board.
func (a *App) dropPhotos(before, after []pet.TopEntry) {
	for _, e := range before {
		if e.Photo != "" && !slices.ContainsFunc(after, func(k pet.TopEntry) bool { return k.Photo == e.Photo }) {
			a.cfg.State.DeleteBlob(context.Background(), e.Photo)
		}
	}
}

// photos: there is somewhere to keep them (a photo directory at home, the database in the
// cluster).
func (a *App) photos() bool {
	if a.cfg.State == nil {
		return false
	}
	if f, ok := a.cfg.State.(*statestore.File); ok {
		return f.BlobDir != ""
	}
	return true
}

// safeName keeps letters, digits and dashes (a robot id in a file name).
func safeName(s string) string {
	return strings.Map(func(c rune) rune {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' {
			return c
		}
		return '_'
	}, s)
}

// ordinalText is a place after "na" / "in": "prvním", "first".
func ordinalText(lang string, place int) string {
	words := map[string][]string{"cs": {"prvním", "druhém", "třetím"}, "en": {"first", "second", "third"}}[lang]
	if words == nil {
		words = []string{"first", "second", "third"}
	}
	return words[max(1, min(len(words), place))-1]
}
