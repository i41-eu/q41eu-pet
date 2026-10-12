package app

import "math"

// Held: a kid holding the robot must not have its head move in their hands. The
// robot's telemetry (every 2 s) has the accelerometer (imu_a*_g) and the gyro
// (imu_gyro_dps). At rest the direction of gravity stays put; the pet learns it.
// Held is: tilted away from that rest pose, turning, or shaken; it ends after a few
// calm samples back at rest. While held, robotConn drops every head command.
const (
	heldTilt   = 12.0 // degrees away from the rest pose
	heldGyro   = 8.0  // degrees per second
	heldAccel  = 0.15 // g away from 1 g: moved
	calmTilt   = 6.0
	calmGyro   = 3.0
	calmToFree = 2 // calm samples in a row (2 s apart) before the head may move again
)

// headCommands move the head.
var headCommands = map[string]bool{"look": true, "nod": true, "shake": true, "home": true}

type holdState struct {
	rest [3]float64 // the rest direction of gravity (unit), zero until learned
	calm int        // calm samples in a row
	held bool
}

// imuSample updates the hold state from one telemetry sample; true when held changed.
func (h *holdState) imuSample(m map[string]float64) bool {
	ax, okX := m["imu_ax_g"]
	ay, okY := m["imu_ay_g"]
	az, okZ := m["imu_az_g"]
	if !okX || !okY || !okZ {
		return false
	}
	gyro := m["imu_gyro_dps"]
	g := math.Sqrt(ax*ax + ay*ay + az*az)
	if g < 0.3 {
		return false // not a real sample
	}
	v := [3]float64{ax / g, ay / g, az / g}
	if h.rest == [3]float64{} {
		if gyro < calmGyro && math.Abs(g-1) < heldAccel {
			h.rest = v
		}
		return false
	}
	dot := v[0]*h.rest[0] + v[1]*h.rest[1] + v[2]*h.rest[2]
	tilt := math.Acos(max(-1, min(1, dot))) * 180 / math.Pi
	was := h.held
	switch {
	case tilt > heldTilt || gyro > heldGyro || math.Abs(g-1) > heldAccel:
		h.held, h.calm = true, 0
	case tilt < calmTilt && gyro < calmGyro:
		h.calm++
		if !h.held { // at rest: the pose follows slow changes (a table moved a little)
			for i := range h.rest {
				h.rest[i] += (v[i] - h.rest[i]) * 0.1
			}
		}
		if h.calm >= calmToFree {
			h.held = false
		}
	default:
		h.calm = 0
	}
	return h.held != was
}

// shaken: the robot reported a shake, it is in someone's hands.
func (h *holdState) shaken() bool {
	was := h.held
	h.held, h.calm = true, 0
	return !was
}
