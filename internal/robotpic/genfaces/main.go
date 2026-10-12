// Command genfaces writes the robot's full-screen faces (../faces/*.svg) from the kid page's
// Fluent Emoji (../../app/ui/emoji, MIT, © Microsoft): the round head is dropped, its colour
// fills the whole 4:3 screen, and the eyes and mouth are cropped to fill it. render-icons.sh
// runs it before rendering the faces to JPEG.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// face: the emoji it comes from and the part of its 32x32 box shown (4:3, x y width).
type face struct {
	emoji   string
	x, y, w float64
}

var faces = map[string]face{
	"happy":    {"happy", 2, 5.5, 28},
	"neutral":  {"neutral", 2, 4.5, 28},
	"sad":      {"sad", 0.5, 4, 31}, // the tear on the left
	"grumpy":   {"grumpy", 2, 6.5, 28},
	"sleeping": {"sleeping", -0.5, 1.5, 33}, // the Zzz at the top right
	"yawn":     {"yawn", 2, 7.5, 28},
	"yum":      {"yum", 2, 4.5, 28},
}

var (
	firstPath = regexp.MustCompile(`(?s)<path [^>]*fill="(#[0-9A-Fa-f]{6})"\s*/>`)
	svgOpen   = regexp.MustCompile(`(?s)<svg[^>]*>`)
)

func main() {
	dir := filepath.Dir(os.Args[0])
	if len(os.Args) > 1 {
		dir = os.Args[1] // the robotpic directory
	}
	for name, f := range faces {
		src, err := os.ReadFile(filepath.Join(dir, "..", "app", "ui", "emoji", f.emoji+".svg"))
		if err != nil {
			fail(err)
		}
		s := string(src)
		head := firstPath.FindStringSubmatchIndex(s)
		if head == nil {
			fail(fmt.Errorf("%s: no head path", f.emoji))
		}
		skin := s[head[2]:head[3]]
		body := s[head[1]:] // everything after the head: eyes, mouth, extras
		body = strings.TrimSuffix(strings.TrimSpace(body), "</svg>")
		defs := ""
		if m := svgOpen.FindStringIndex(s); m != nil {
			defs = strings.TrimSpace(s[m[1]:head[0]]) // gradients before the head, if any
		}
		h := f.w * 3 / 4
		inner := fmt.Sprintf(`<defs><radialGradient id="skin" cx="50%%" cy="45%%" r="75%%"><stop offset="0" stop-color="%s" stop-opacity="0"/><stop offset="1" stop-color="#000" stop-opacity=".18"/></radialGradient></defs>
%s
<rect x="%g" y="%g" width="%g" height="%g" fill="%s"/>
<rect x="%g" y="%g" width="%g" height="%g" fill="url(#skin)"/>
%s`, skin, defs, f.x, f.y, f.w, h, skin, f.x, f.y, f.w, h, strings.TrimSpace(body))
		note := fmt.Sprintf("<!-- From Fluent Emoji Flat (MIT, © Microsoft), %s.svg: made by genfaces, do not edit. -->", f.emoji)
		screen := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="320" height="240" viewBox="%g %g %g %g">
%s
%s
</svg>
`, f.x, f.y, f.w, h, note, inner)
		write(filepath.Join(dir, "faces", name+".svg"), screen)
		// The kid page's tiny Stackchan with the same face on its screen.
		chanSVG := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 128">
%s
%s
<g clip-path="url(#screen-shape)"><svg x="21" y="23" width="86" height="62" viewBox="%g %g %g %g" preserveAspectRatio="xMidYMid slice">
%s
</svg></g>
%s
</svg>
`, note, chanBody, f.x, f.y, f.w, h, inner, chanGlare)
		write(filepath.Join(dir, "..", "app", "ui", "chan", name+".svg"), chanSVG)
	}
}

func write(path, text string) {
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		fail(err)
	}
}

// chanBody is the tiny Stackchan of the kid page (ui/chan/NOTICE) without its face; the face goes
// on its screen (x 21, y 23, 86x62, rounded), chanGlare over it.
const chanBody = `<defs>
<linearGradient id="shell" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#a8e2ff"/><stop offset="1" stop-color="#3f95ee"/></linearGradient>
<linearGradient id="base" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#ffcb5c"/><stop offset="1" stop-color="#ff8a3d"/></linearGradient>
<filter id="glow" x="-1" y="-1" width="3" height="3"><feGaussianBlur stdDeviation="2.5"/></filter>
<clipPath id="screen-shape" clipPathUnits="userSpaceOnUse"><rect x="21" y="23" width="86" height="62" rx="11"/></clipPath>
</defs>
<ellipse cx="64" cy="121" rx="38" ry="5" fill="#1b2340" opacity=".15"/>
<rect x="30" y="97" width="68" height="21" rx="9" fill="url(#base)"/>
<rect x="37" y="100" width="54" height="5" rx="2.5" fill="#fff" opacity=".4"/>
<rect x="55" y="88" width="18" height="11" rx="3" fill="#5c6f94"/>
<rect x="3" y="36" width="9" height="36" rx="4.5" fill="#ffd24a" filter="url(#glow)"/>
<rect x="116" y="36" width="9" height="36" rx="4.5" fill="#ffd24a" filter="url(#glow)"/>
<rect x="5" y="39" width="5" height="30" rx="2.5" fill="#ffd24a"/>
<rect x="118" y="39" width="5" height="30" rx="2.5" fill="#ffd24a"/>
<rect x="10" y="10" width="108" height="84" rx="20" fill="url(#shell)"/>
<rect x="24" y="14" width="44" height="5" rx="2.5" fill="#fff" opacity=".55"/>
<rect x="20" y="22" width="88" height="64" rx="12" fill="#1b2340"/>`

const chanGlare = `<path d="M27 29 H48 L33 80 H27 Z" fill="#fff" opacity=".12"/>`

func fail(err error) {
	fmt.Fprintln(os.Stderr, "genfaces:", err)
	os.Exit(1)
}
