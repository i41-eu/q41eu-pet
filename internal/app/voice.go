package app

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/i41-eu/q41eu-dev/sound"
	"github.com/i41-eu/q41eu-pet/internal/pet"
	"github.com/i41-eu/q41eu-raw/wire"
)

// The pet speaks its lines (Config.Voice: Edge's Antonín, a bit robot and kid, cached;
// espeak-ng as the fallback). The PCM is streamed to the robot's speaker at speaking
// pace (the robot buffers only about 3 s); a new line cuts the previous one off.

const (
	voiceChunk = sound.Rate / 2 // samples per message: half a second
	voiceAhead = 2              // messages sent at once before pacing (about 1 s)
)

// speak says a line on the robot (if sounds and the voice are on; at night only when
// atNight, e.g. the good night). The speech is made outside the lock.
func (a *App) speak(r *robot, line string, atNight bool) {
	s := r.pet.Settings
	if a.cfg.Voice == nil || r.conn == nil || !s.Sounds || !s.Voice || (!atNight && s.PhaseAt(a.now()) == pet.Night) {
		return
	}
	if time.Now().Before(r.speakingUntil) && slices.Contains(r.commands, "speaker_flush") {
		r.conn.command("speaker_flush", nil) // the unfinished line fades out instead of mixing in
	}
	r.voiceGen++
	r.speakingUntil = time.Now().Add(3 * time.Second) // until the speech's length is known
	gen, c, lang := r.voiceGen, r.conn, s.Lang
	go func() {
		pcm, err := a.cfg.Voice.Speak(context.Background(), line, lang)
		if err != nil {
			a.log.Warn("voice", "err", err)
			return
		}
		a.mu.Lock()
		if r.voiceGen == gen {
			r.speakingUntil = time.Now().Add(time.Second * time.Duration(len(pcm)) / sound.Rate)
		}
		a.mu.Unlock()
		for off, n := 0, 0; off < len(pcm); off, n = off+voiceChunk, n+1 {
			if n >= voiceAhead {
				time.Sleep(time.Second * voiceChunk / sound.Rate)
			}
			a.mu.Lock()
			current := r.voiceGen == gen && r.conn == c
			a.mu.Unlock()
			if !current {
				return // a newer line, or the robot left
			}
			c.binary(wire.BinSpeakerPCM, sound.Message(pcm[off:min(off+voiceChunk, len(pcm))]))
		}
	}()
}

// allLines are the pet's fixed lines in a language (to make the speech ahead of time).
func allLines(lang string) []string {
	var out []string
	for key, variants := range robotTexts[lang] {
		for _, v := range variants {
			switch {
			case !strings.Contains(v, "%s"):
				out = append(out, v)
			case key == "eat":
				for _, food := range pet.FoodOrder {
					out = append(out, strings.ReplaceAll(v, "%s", foodNames[lang][food]))
				}
			case key == "color_place":
				for place := 1; place <= pet.TopPlaces; place++ {
					out = append(out, strings.ReplaceAll(v, "%s", ordinalText(lang, place)))
				}
			case key == "game_over":
				for hits := 1; hits <= pet.GameRounds; hits++ {
					out = append(out, strings.ReplaceAll(v, "%s", scoreText(lang, hits, pet.GameRounds)))
				}
			}
		}
	}
	for _, variants := range foodTexts[lang] {
		out = append(out, variants...)
	}
	return out
}

// warmVoice makes the speech of every fixed line in the pets' languages ahead of time,
// one after another (Edge is quick; lines already cached are skipped).
func (a *App) warmVoice(ctx context.Context) {
	if a.cfg.Voice == nil {
		return
	}
	a.mu.Lock()
	langs := map[string]bool{}
	for _, r := range a.robots {
		langs[r.pet.Settings.Lang] = true
	}
	a.mu.Unlock()
	made := 0
	for lang := range langs {
		for _, line := range allLines(lang) {
			if ctx.Err() != nil {
				return
			}
			if a.cfg.Voice.Cached(line, lang) {
				continue
			}
			if _, err := a.cfg.Voice.Speak(ctx, line, lang); err != nil {
				a.log.Warn("voice warm-up stopped", "err", err)
				return
			}
			made++
		}
	}
	if made > 0 {
		a.log.Info("voice ready", "new_lines", made)
	}
}
