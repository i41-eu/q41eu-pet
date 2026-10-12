package pet

import (
	"testing"
	"time"
)

func TestDemoIgnoresSchool(t *testing.T) {
	s := DefaultSettings()
	s.School = true
	wed := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	if s.PhaseAt(wed) != School {
		t.Fatal("school hours")
	}
	s.Demo = true
	if s.PhaseAt(wed) != Awake {
		t.Fatal("demo mode should ignore school")
	}
	s.DemoNoSchool = false
	if s.PhaseAt(wed) != School {
		t.Fatal("demo mode that keeps school")
	}
}
