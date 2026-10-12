package robotpic

import (
	"bytes"
	"image/jpeg"
	"image/png"
	"os"
	"testing"

	"github.com/i41-eu/q41eu-pet/internal/pet"
)

func TestPictures(t *testing.T) {
	pics := map[string][]byte{"needs": Needs(80, 45, 10), "ball3": Ball(3), "stars": Stars(3, 5), "dream": Dream("cake")}
	for _, d := range Dreams {
		if icon(d) == nil {
			t.Errorf("no icon for dream %s", d)
		}
	}
	for _, f := range pet.FoodOrder {
		pics[f] = Food(f)
		if icon(f) == nil {
			t.Errorf("no icon for food %s", f)
		}
	}
	for name, b := range pics {
		img, err := jpeg.Decode(bytes.NewReader(b))
		if err != nil || img.Bounds().Dx() != W || img.Bounds().Dy() != H {
			t.Errorf("%s: not a %dx%d JPEG: %v", name, W, H, err)
		}
		if len(b) > 60<<10 {
			t.Errorf("%s: %d bytes, too big for the robot", name, len(b))
		}
	}
	// PICDIR=/tmp/x go test ./internal/robotpic/ saves them to look at.
	if dir := os.Getenv("PICDIR"); dir != "" {
		for name, b := range pics {
			os.WriteFile(dir+"/"+name+".jpg", b, 0o644)
		}
	}
}

func TestSpotAt(t *testing.T) {
	for _, c := range []struct {
		x, y float64
		want int
	}{{10, 10, 0}, {300, 10, 1}, {10, 200, 2}, {300, 200, 3}, {160, 120, 3}} {
		if got := SpotAt(c.x, c.y); got != c.want {
			t.Errorf("SpotAt(%v, %v) = %d, want %d", c.x, c.y, got, c.want)
		}
	}
}

func TestEnergyBarPNG(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(EnergyBar(50)))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 { // the rounded end
		t.Fatalf("corner alpha %d", a)
	}
}
