package app

import (
	"encoding/json"
	"fmt"
	"hash/crc32"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/i41-eu/q41eu-dev/robothub"
	"github.com/i41-eu/q41eu-dev/sound"
	"github.com/i41-eu/q41eu-pet/internal/pet"
	"github.com/i41-eu/q41eu-pet/internal/robotpic"
	"github.com/i41-eu/q41eu-raw/wire"
)

// The pet keeps its pictures in the robot's file store (folder "pet/"), so it
// can later show them without sending them again. At connect it asks for the
// robot's file list ("assets") and uploads what is missing or changed (CRC-32).

const assetDir = "pet/"

// petAssets are the files the pet wants on the robot: its pictures and sounds (WAV).
// petSounds are the pet's sounds (the dev kit's chimes), stored on the robot as WAV files.
var petSounds = []string{"chirp", "munch", "tada", "no", "yawn", "morning", "lullaby", "hello", "whee"}

func petAssets() map[string][]byte {
	out := map[string][]byte{}
	for name, b := range robotpic.Files() {
		out[assetDir+name] = b
	}
	for _, name := range petSounds {
		out[soundAsset(name)] = sound.WAV(sound.Chime(name))
	}
	return out
}

func soundAsset(name string) string { return assetDir + "snd/" + name + ".wav" }

// syncAssets compares the robot's list with the pet's files and uploads the difference. a.mu held.
func (a *App) syncAssets(r *robot, listJSON string) {
	var list struct {
		Files []struct {
			Name  string `json:"name"`
			Bytes int    `json:"bytes"`
			CRC   uint32 `json:"crc"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(listJSON), &list); err != nil || r.conn == nil {
		return
	}
	have := map[string]uint32{}
	for _, f := range list.Files {
		have[f.Name] = f.CRC
	}
	want := petAssets()
	for name, b := range want {
		if have[name] == crc32.ChecksumIEEE(b) {
			r.files[name] = true
		}
	}
	for name := range have { // the pet's old files (e.g. a picture it no longer uses)
		if _, ok := want[name]; !ok && strings.HasPrefix(name, assetDir) {
			r.conn.command("asset_delete", map[string]any{"name": name})
			a.log.Info("deleting an old pet file on the robot", "robot", r.id, "name", name)
		}
	}
	names := make([]string, 0, len(want))
	for name, b := range want {
		if crc, ok := have[name]; !ok || crc != crc32.ChecksumIEEE(b) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		a.log.Info("robot files up to date", "robot", r.id, "files", len(want))
		a.refreshFace(r)
		return
	}
	r.uploading = map[string]uint32{}
	var chunks [][]byte
	for _, name := range names {
		r.uploading[name] = crc32.ChecksumIEEE(want[name])
		chunks = append(chunks, wire.AssetChunks(name, want[name])...)
	}
	a.log.Info("uploading pet files", "robot", r.id, "files", len(names), "chunks", len(chunks))
	go a.sendChunks(r.conn, chunks)
}

// sendChunks queues upload messages without crowding out commands.
func (a *App) sendChunks(c *robotConn, chunks [][]byte) {
	for _, msg := range chunks {
		for c.Queued() > robothub.QueueSize/2 {
			select {
			case <-c.Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
		if !c.binary(msg[0], msg[1:]) { // AssetChunks gives the type byte first
			return
		}
	}
}

// assetSaved checks an uploaded file's CRC. a.mu held.
func (a *App) assetSaved(r *robot, name string, crc uint32) {
	want, ok := r.uploading[name]
	if !ok {
		return
	}
	delete(r.uploading, name)
	if crc != want {
		a.log.Warn("robot file differs after upload", "robot", r.id, "name", name, "crc", crc, "want", want)
		return
	}
	r.files[name] = true
	if len(r.uploading) == 0 {
		a.log.Info("pet files uploaded", "robot", r.id)
		a.refreshFace(r)
	}
}

// canSprite: the robot shows stored pictures and has this one.
func (r *robot) canSprite(asset string) bool {
	return r.files[asset] && slices.Contains(r.commands, "sprite")
}

// sprite sends a "sprite" command (see the firmware's SpriteLayer); the ids are
// remembered so clearSprites can remove them and keep the drawn face.
func (a *App) sprite(r *robot, args map[string]any) {
	if id, _ := args["id"].(string); id != "" && id != faceSprite {
		if r.spriteIDs == nil {
			r.spriteIDs = map[string]bool{}
		}
		r.spriteIDs[id] = true
	}
	r.conn.command("sprite", args)
}

// clearSprites removes the pet's sprites, except the drawn face.
func (a *App) clearSprites(r *robot) {
	for id := range r.spriteIDs {
		r.conn.command("sprite_hide", map[string]any{"id": id})
	}
	r.spriteIDs = nil
}

// The drawn face (parent setting): a full-screen picture per mood as the bottom sprite,
// under food, dreams and the ball. It steps aside for speech bubbles and pictures.
const faceSprite = "face"

var (
	emotionFace = map[string]string{"happy": "happy", "neutral": "neutral", "sad": "sad", "doubt": "grumpy", "angry": "grumpy", "sleepy": "sleeping"}
	moodFace    = map[pet.Mood]string{pet.Happy: "happy", pet.OK: "neutral", pet.Hungry: "sad", pet.Bored: "grumpy",
		pet.Tired: "yawn", pet.Napping: "sleeping", pet.Sleeping: "sleeping"}
)

// drawnFace shows face-<name>.jpg when the parent chose drawn faces (and the robot has it); else
// the robot's own face.
func (a *App) drawnFace(r *robot, name string) {
	asset := assetDir + "face-" + name + ".jpg"
	if name == "" || !r.pet.Settings.DrawnFace || !r.canSprite(asset) {
		if r.faceShown != "" {
			r.conn.command("sprite_hide", map[string]any{"id": faceSprite})
			r.faceShown, r.faceHidden = "", false
		}
		return
	}
	if r.faceShown == name && !r.faceHidden {
		return
	}
	r.conn.command("sprite", map[string]any{"id": faceSprite, "asset": asset, "x": 160, "y": 120, "z": -10, "hidden": false})
	r.faceShown, r.faceHidden = name, false
}

// hideFace lets the robot's own face (with its speech bubble) or a picture show for a moment.
func (a *App) hideFace(r *robot) {
	if r.faceShown != "" && !r.faceHidden {
		r.conn.command("sprite", map[string]any{"id": faceSprite, "hidden": true})
		r.faceHidden = true
	}
}

// showPicture covers the face with a full-screen JPEG (the older way, without sprites).
func (a *App) showPicture(r *robot, jpeg []byte) {
	a.hideFace(r)
	r.conn.binary(wire.BinShowJPEG, jpeg)
	r.pictureOn = true
}

// refreshFace shows the mood again once the robot's files are known (the drawn face needs them).
func (a *App) refreshFace(r *robot) {
	if r.conn != nil && r.game == nil && a.now().After(r.busyUntil) {
		a.express(r, a.now())
	}
}

// energyBar: while the pet sleeps (a nap, or the screen lit at night), a tiny faint
// bar in the bottom left corner shows its energy, in steps of 10%. Only sent when it
// changes.
func (a *App) energyBar(r *robot) {
	step := int(math.Round(r.pet.Stats.Energy/10)) * 10
	asset := fmt.Sprintf("%szbar-%d.png", assetDir, step)
	if !r.canSprite(asset) || (r.spriteIDs[energyBarSprite] && r.barStep == step) {
		return
	}
	r.barStep = step
	a.sprite(r, map[string]any{"id": energyBarSprite, "asset": asset, // bottom left, faint
		"x": 12 + robotpic.EnergyBarW/2, "y": 240 - 12 - robotpic.EnergyBarH/2, "z": 5, "opacity": 0.45})
}

const energyBarSprite = "zbar"
