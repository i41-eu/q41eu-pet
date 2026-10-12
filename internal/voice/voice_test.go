package voice

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestSecMSGEC(t *testing.T) {
	// The same as edge-tts computes for this moment.
	if got := secMSGEC(time.Unix(1790000000, 0)); got != "CA99F0B37F2EAC6F5D719AE4BA7C98978842B3F335D9F42979070A8DB149F0A5" {
		t.Fatalf("Sec-MS-GEC %s", got)
	}
}

func TestResample(t *testing.T) {
	if out := Resample(make([]int16, 22050), 22050, Rate); len(out) != Rate {
		t.Fatalf("one second at 22050 Hz -> %d samples", len(out))
	}
}

func TestEspeakFallback(t *testing.T) {
	path, err := exec.LookPath("espeak-ng")
	if err != nil {
		t.Skip("espeak-ng not installed")
	}
	s := &Synth{Espeak: path}
	pcm, err := s.Speak(context.Background(), "Mám hlad!", "cs")
	if err != nil || len(pcm) < Rate/3 {
		t.Fatalf("espeak: %v, %d samples", err, len(pcm))
	}
}

// Online: STACKCHAN_EDGE_TEST=1 go test ./internal/voice/ (EDGE_OUT=x.raw keeps the PCM).
func TestEdgeLive(t *testing.T) {
	if os.Getenv("STACKCHAN_EDGE_TEST") == "" {
		t.Skip("online test: set STACKCHAN_EDGE_TEST=1")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	s := &Synth{FFmpeg: ffmpeg, CacheDir: t.TempDir()}
	start := time.Now()
	pcm, err := s.Speak(context.Background(), "Mám hlad!", "cs")
	if err != nil {
		t.Fatal(err)
	}
	secs := float64(len(pcm)) / Rate
	t.Logf("Edge: %.2f s of speech in %v", secs, time.Since(start))
	if secs < 0.4 || secs > 4 {
		t.Fatalf("\"Mám hlad!\" took %.2f s", secs)
	}
	if !s.Cached("Mám hlad!", "cs") {
		t.Fatal("the line should be cached on disk")
	}
	if out := os.Getenv("EDGE_OUT"); out != "" {
		os.WriteFile(out, bytesOf(pcm), 0o644)
	}
}
