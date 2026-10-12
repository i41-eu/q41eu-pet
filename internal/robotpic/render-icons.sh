#!/usr/bin/env bash
# Render the robot-screen icons (png/*.png, 160 px) from the page's emoji SVGs.
# Needs inkscape. Run after adding a food or changing an icon.
set -euo pipefail
cd "$(dirname "$0")"
SVG=../app/ui/emoji
for f in apple carrot banana cake milk bread heart zzz ball star moon plate party refresh; do
    inkscape "$SVG/$f.svg" --export-type=png --export-width=160 --export-filename="png/$f.png" >/dev/null 2>&1
done
# Full-screen faces for the robot: faces/*.svg made from the page's Fluent Emoji (genfaces: the
# head's colour fills the 4:3 screen), then JPEG, no transparency needed. Needs ImageMagick too.
go run ./genfaces .
for f in faces/*.svg; do
    n=$(basename "$f" .svg)
    inkscape "$f" --export-type=png --export-width=320 --export-height=240 --export-filename="/tmp/face-$n.png" >/dev/null 2>&1
    magick "/tmp/face-$n.png" -quality 90 "png/face-$n.jpg" && rm "/tmp/face-$n.png"
done
