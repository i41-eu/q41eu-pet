package voice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// Rate is the sample rate of the speech (the robot's speaker message).
const Rate = 16000

// Voices for Edge, per language: a young-sounding voice (pitch and rate up).
var Voices = map[string]EdgeVoice{
	"cs": {Name: "cs-CZ-AntoninNeural", Pitch: "+25Hz", Rate: "+8%"},
	"en": {Name: "en-US-AnaNeural", Pitch: "+10Hz", Rate: "+5%"},
}

// robotFilter makes the voice a bit robot: 40% of a robotized copy (the FFT phase
// dropped: a flat, buzzing pitch) mixed with the original, then even loudness with
// 6 dB of headroom: the robot's speaker path adds gain, louder speech clipped there.
const robotFilter = "aresample=22050,aformat=channel_layouts=mono,asplit[d][w];" +
	"[w]afftfilt=real='hypot(re,im)':imag='0':win_size=512:overlap=0.75[r];" +
	"[d][r]amix=inputs=2:weights=0.6 0.4:normalize=0,loudnorm=I=-21:TP=-6:LRA=11,aresample=16000"

// Synth makes speech: Edge + ffmpeg when both work (cached on disk: each line is made
// once), else espeak-ng (kept in memory only, so Edge is tried again later).
type Synth struct {
	FFmpeg   string // ffmpeg for Edge's MP3 and the robot filter; "" = Edge off
	Espeak   string // espeak-ng, the fallback; "" = none
	CacheDir string // where Edge speech is kept; "" = memory only

	mu     sync.Mutex
	memory map[string][]int16
}

// Speak returns the line as 16 kHz mono PCM.
func (s *Synth) Speak(ctx context.Context, text, lang string) ([]int16, error) {
	v, ok := Voices[lang]
	if !ok {
		v, lang = Voices["en"], "en"
	}
	sum := sha256.Sum256([]byte(v.Name + "|" + v.Pitch + "|" + v.Rate + "|" + robotFilter + "|" + text))
	key := hex.EncodeToString(sum[:12])

	s.mu.Lock()
	if s.memory == nil {
		s.memory = map[string][]int16{}
	}
	if pcm, ok := s.memory[key]; ok {
		s.mu.Unlock()
		return pcm, nil
	}
	s.mu.Unlock()

	file := ""
	if s.CacheDir != "" {
		file = filepath.Join(s.CacheDir, key+".pcm")
		if b, err := os.ReadFile(file); err == nil && len(b) > 0 {
			return s.keep(key, pcmOf(b)), nil
		}
	}
	if s.FFmpeg != "" {
		pcm, err := s.edge(ctx, v, text)
		if err == nil {
			if file != "" && os.MkdirAll(s.CacheDir, 0o755) == nil {
				os.WriteFile(file, bytesOf(pcm), 0o644)
			}
			return s.keep(key, pcm), nil
		}
		if s.Espeak == "" {
			return nil, err
		}
	}
	if s.Espeak == "" {
		return nil, errors.New("no voice: neither ffmpeg (Edge) nor espeak-ng")
	}
	pcm, err := espeak(ctx, s.Espeak, text, lang)
	if err != nil {
		return nil, err
	}
	return s.keep("espeak|"+key, pcm), nil // separate key: the Edge voice replaces it once it works
}

// Cached reports whether the line is ready (on disk or in memory).
func (s *Synth) Cached(text, lang string) bool {
	v, ok := Voices[lang]
	if !ok {
		v = Voices["en"]
	}
	sum := sha256.Sum256([]byte(v.Name + "|" + v.Pitch + "|" + v.Rate + "|" + robotFilter + "|" + text))
	key := hex.EncodeToString(sum[:12])
	s.mu.Lock()
	_, ok = s.memory[key]
	s.mu.Unlock()
	if ok || s.CacheDir == "" {
		return ok
	}
	_, err := os.Stat(filepath.Join(s.CacheDir, key+".pcm"))
	return err == nil
}

func (s *Synth) keep(key string, pcm []int16) []int16 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.memory) > 256 {
		clear(s.memory)
	}
	s.memory[key] = pcm
	return pcm
}

// edge: Edge's MP3 through ffmpeg's robot filter to 16 kHz PCM.
func (s *Synth) edge(ctx context.Context, v EdgeVoice, text string) ([]int16, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	mp3, err := Edge(ctx, v, text)
	if err != nil {
		return nil, err
	}
	return runFFmpeg(ctx, s.FFmpeg, mp3, robotFilter)
}

func runFFmpeg(ctx context.Context, ffmpeg string, in []byte, filter string) ([]int16, error) {
	cmd := exec.CommandContext(ctx, ffmpeg, "-loglevel", "error", "-i", "pipe:0",
		"-filter_complex", filter, "-ac", "1", "-ar", "16000", "-f", "s16le", "pipe:1")
	cmd.Stdin = bytes.NewReader(in)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, errors.New("ffmpeg: " + err.Error() + ": " + stderr.String())
	}
	return pcmOf(out.Bytes()), nil
}

// espeakArgs: voice, pitch (0-99), words per minute.
var espeakArgs = map[string][]string{
	"cs": {"-v", "cs", "-p", "75", "-s", "150"},
	"en": {"-v", "en-us", "-p", "75", "-s", "160"},
}

// espeak: espeak-ng's WAV (22 kHz) resampled to Rate.
func espeak(ctx context.Context, espeakPath, text, lang string) ([]int16, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	wav, err := exec.CommandContext(ctx, espeakPath, append(append([]string{}, espeakArgs[lang]...), "--stdout", text)...).Output()
	if err != nil {
		return nil, err
	}
	pcm, rate, err := wavPCM(wav)
	if err != nil {
		return nil, err
	}
	return Resample(pcm, rate, Rate), nil
}

func pcmOf(b []byte) []int16 {
	pcm := make([]int16, len(b)/2)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(b[2*i:]))
	}
	return pcm
}

func bytesOf(pcm []int16) []byte {
	b := make([]byte, 2*len(pcm))
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(s))
	}
	return b
}

// wavPCM reads 16-bit mono PCM from a WAV (espeak-ng's streamed WAV may carry
// bogus sizes: the data runs to the end).
func wavPCM(b []byte) ([]int16, int, error) {
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, 0, errors.New("not a WAV")
	}
	rate := 0
	for p := 12; p+8 <= len(b); {
		id, size := string(b[p:p+4]), int(binary.LittleEndian.Uint32(b[p+4:]))
		p += 8
		switch id {
		case "fmt ":
			if p+16 > len(b) || binary.LittleEndian.Uint16(b[p:]) != 1 || binary.LittleEndian.Uint16(b[p+2:]) != 1 ||
				binary.LittleEndian.Uint16(b[p+14:]) != 16 {
				return nil, 0, errors.New("want 16-bit mono PCM")
			}
			rate = int(binary.LittleEndian.Uint32(b[p+4:]))
		case "data":
			data := b[p:]
			if size >= 0 && size < len(data) {
				data = data[:size]
			}
			if rate == 0 {
				return nil, 0, errors.New("no fmt chunk")
			}
			return pcmOf(data), rate, nil
		}
		p += size + size&1
	}
	return nil, 0, errors.New("no data chunk")
}

// Resample converts PCM between rates (linear interpolation; fine for speech).
func Resample(in []int16, from, to int) []int16 {
	if from == to || len(in) == 0 {
		return in
	}
	n := len(in) * to / from
	out := make([]int16, n)
	for i := range out {
		x := float64(i) * float64(from) / float64(to)
		j := int(x)
		t := x - float64(j)
		a, b := float64(in[min(j, len(in)-1)]), float64(in[min(j+1, len(in)-1)])
		out[i] = int16(a + (b-a)*t)
	}
	return out
}
