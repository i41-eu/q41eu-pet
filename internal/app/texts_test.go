package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/i41-eu/q41eu-pet/internal/pet"
)

// The keys the code says (text(lang, key, ...)).
var lineKeys = []string{"eat", "full", "cuddle", "play", "too_tired", "limit", "nap", "not_tired", "wake", "shake",
	"hello", "hungry", "bored", "tired", "bedtime", "goodnight", "morning", "game", "night_wake", "game_over", "game_over_0", "school", "long_press",
	"color_game", "color_done", "color_record", "color_place", "photo_count",
	"tickle", "long_cuddle", "scratch", "game_over_all", "demo_food", "demo_fun", "demo_energy", "picky", "eat_again", "dream_food"}

func TestLinesFilesAreComplete(t *testing.T) {
	for _, lang := range []string{"cs", "en"} {
		for _, key := range lineKeys {
			if len(robotTexts[lang][key]) == 0 {
				t.Errorf("lines/%s.txt: no [%s]", lang, key)
			}
			for _, v := range robotTexts[lang][key] {
				filled := key == "game_over" || key == "color_done" || key == "color_record" || key == "color_place"
				if strings.Contains(v, "%s") != (key == "eat" && strings.Contains(v, "%s") || filled) {
					t.Errorf("lines/%s.txt [%s]: %%s where it is not filled in (or missing): %q", lang, key, v)
				}
			}
		}
		for key := range robotTexts[lang] {
			found := false
			for _, k := range lineKeys {
				found = found || k == key
			}
			if !found {
				t.Errorf("lines/%s.txt: unknown section [%s]", lang, key)
			}
		}
		for hits := 1; hits <= pet.GameRounds; hits++ {
			if scoreTexts[lang][fmt.Sprint(hits)] == "" {
				t.Errorf("lines/%s.txt [score]: no %d", lang, hits)
			}
		}
		for _, food := range pet.FoodOrder {
			if foodNames[lang][food] == "" {
				t.Errorf("lines/%s.txt [names]: no %s", lang, food)
			}
			if len(foodTexts[lang][food]) == 0 {
				t.Errorf("lines/%s.txt: no [food %s]", lang, food)
			}
		}
	}
}

func TestParseLines(t *testing.T) {
	if _, err := parseLines("Ahoj\n"); err == nil {
		t.Error("text before a section should be an error")
	}
	if _, err := parseLines("[names]\napple jablko\n"); err == nil {
		t.Error("a name without = should be an error")
	}
	l, err := parseLines("# x\n[hello]\nAhoj!\n[score]\n1 = jeden\n")
	if err != nil || l.texts["hello"][0] != "Ahoj!" || l.scores["1"] != "jeden" {
		t.Errorf("parsed: %+v %v", l, err)
	}
}
