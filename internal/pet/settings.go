package pet

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// DaySchedule is when the kid wakes up and goes to bed, "HH:MM" local time.
// A bedtime after midnight (earlier than Wake) works too.
type DaySchedule struct {
	Wake string `json:"wake"`
	Bed  string `json:"bed"`
}

// Settings are what a parent sets on the parent page.
type Settings struct {
	Name       string      `json:"name"`
	Lang       string      `json:"lang"`       // "cs" or "en"
	Difficulty string      `json:"difficulty"` // how fast needs grow: "easy", "normal", "hard"
	Weekday    DaySchedule `json:"weekday"`    // Monday to Friday
	Weekend    DaySchedule `json:"weekend"`

	School     bool   `json:"school"` // on weekdays the needs pause during school
	SchoolFrom string `json:"school_from"`
	SchoolTo   string `json:"school_to"`

	PlayLimitMin int `json:"play_limit_min"` // play and cuddle minutes per day, 0 = no limit

	Sounds           bool `json:"sounds"`
	Voice            bool `json:"voice"`               // the pet speaks its lines (with sounds on)
	Volume           int  `json:"volume"`              // robot speaker, 0..100
	NightLight       bool `json:"night_light"`         // dim warm LEDs at night
	ScreenOffAtNight bool `json:"screen_off_at_night"` // the robot's screen sleeps at night
	ScreenIdleMin    int  `json:"screen_idle_min"`     // by day the screen goes off after this many minutes nobody plays, 0 = never
	NightWakeMin     int  `json:"night_wake_min"`      // a hard press at night wakes it this long, 0 = never
	DrawnFace        bool `json:"drawn_face"`          // the pet's drawn faces on the robot instead of its own
	Demo             bool `json:"demo"`                // demo mode: a need at 90% drops back to 10% (DemoReset)
	DemoNoSchool     bool `json:"demo_no_school"`      // in demo mode school hours are ignored (the pet does not rest)

	GameBallSeconds int  `json:"game_ball_seconds"` // time to catch one ball (3..10)
	GameHeadMoves   bool `json:"game_head_moves"`   // the head circles, wanders and dodges in the game
	ColorPhotos     bool `json:"color_photos"`      // the color game's top three get a photo from the robot's camera

	Foods map[string]string `json:"foods"` // NFC tag uid -> food key; unknown tags feed an apple
}

// UnmarshalJSON reads settings over the defaults, so a setting added later
// gets its default in a saved pet, not a zero value.
func (s *Settings) UnmarshalJSON(b []byte) error {
	type plain Settings // without this method
	v := plain(DefaultSettings())
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*s = Settings(v)
	return nil
}

// DefaultSettings for a new pet.
func DefaultSettings() Settings {
	return Settings{
		Name:             "Čenda",
		Lang:             "cs",
		Difficulty:       "normal",
		Weekday:          DaySchedule{Wake: "07:00", Bed: "20:00"},
		Weekend:          DaySchedule{Wake: "08:00", Bed: "20:30"},
		School:           false,
		DemoNoSchool:     true,
		SchoolFrom:       "08:00",
		SchoolTo:         "15:00",
		Sounds:           true,
		Voice:            true,
		Volume:           40,
		NightLight:       true,
		ScreenOffAtNight: true,
		ScreenIdleMin:    5,
		NightWakeMin:     5,
		GameBallSeconds:  5,
		GameHeadMoves:    true,
		ColorPhotos:      true,
		DrawnFace:        true, // the Fluent Emoji faces, not the robot's own
		Foods:            map[string]string{},
	}
}

// Normalize fills in defaults and clamps values; the parent page sends whole settings.
func (s *Settings) Normalize() {
	d := DefaultSettings()
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		s.Name = d.Name
	}
	if len([]rune(s.Name)) > 24 {
		s.Name = string([]rune(s.Name)[:24])
	}
	if s.Lang != "en" {
		s.Lang = "cs"
	}
	if _, ok := map[string]bool{"easy": true, "normal": true, "hard": true}[s.Difficulty]; !ok {
		s.Difficulty = d.Difficulty
	}
	fix := func(v *string, def string) {
		if minutesOf(*v) < 0 {
			*v = def
		}
		*v = clockString(minutesOf(*v))
	}
	fix(&s.Weekday.Wake, d.Weekday.Wake)
	fix(&s.Weekday.Bed, d.Weekday.Bed)
	fix(&s.Weekend.Wake, d.Weekend.Wake)
	fix(&s.Weekend.Bed, d.Weekend.Bed)
	fix(&s.SchoolFrom, d.SchoolFrom)
	fix(&s.SchoolTo, d.SchoolTo)
	s.PlayLimitMin = max(0, min(600, s.PlayLimitMin))
	s.Volume = max(0, min(100, s.Volume))
	s.NightWakeMin = max(0, min(60, s.NightWakeMin))
	s.ScreenIdleMin = max(0, min(120, s.ScreenIdleMin))
	s.GameBallSeconds = max(3, min(10, s.GameBallSeconds))
	if s.Foods == nil {
		s.Foods = map[string]string{}
	}
	for uid, food := range s.Foods {
		if _, ok := Foods[food]; !ok || uid == "" {
			delete(s.Foods, uid)
		}
	}
}

// minutesOf parses "HH:MM" into minutes after midnight, -1 if invalid.
func minutesOf(s string) int {
	var h, m int
	if n, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil || n != 2 || h < 0 || h > 23 || m < 0 || m > 59 {
		return -1
	}
	return h*60 + m
}

func clockString(m int) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }

func weekend(t time.Time) bool { return t.Weekday() == time.Saturday || t.Weekday() == time.Sunday }

// Day is the schedule for t's date.
func (s *Settings) Day(t time.Time) DaySchedule {
	if weekend(t) {
		return s.Weekend
	}
	return s.Weekday
}

// PhaseAt says whether the pet is awake, asleep for the night, or waiting for school to end.
func (s *Settings) PhaseAt(t time.Time) Phase {
	d := s.Day(t)
	m := t.Hour()*60 + t.Minute()
	wake, bed := minutesOf(d.Wake), minutesOf(d.Bed)
	if wake >= 0 && bed >= 0 && wake != bed {
		night := m < wake || m >= bed
		if bed < wake { // bedtime after midnight
			night = m >= bed && m < wake
		}
		if night {
			return Night
		}
	}
	if s.School && !weekend(t) && !(s.Demo && s.DemoNoSchool) {
		from, to := minutesOf(s.SchoolFrom), minutesOf(s.SchoolTo)
		if from >= 0 && to > from && m >= from && m < to {
			return School
		}
	}
	return Awake
}

// MinutesToBed is how long until today's bedtime (0..1439).
func (s *Settings) MinutesToBed(t time.Time) int {
	bed := minutesOf(s.Day(t).Bed)
	return ((bed-(t.Hour()*60+t.Minute()))%1440 + 1440) % 1440
}

// NextWake is the wake-up time the kid waits for at t: today's before it,
// tomorrow's once today's has passed (the evening).
func (s *Settings) NextWake(t time.Time) string {
	if t.Hour()*60+t.Minute() >= minutesOf(s.Day(t).Wake) {
		return s.Day(t.AddDate(0, 0, 1)).Wake
	}
	return s.Day(t).Wake
}
