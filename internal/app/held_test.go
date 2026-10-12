package app

import "testing"

func TestHeldInHands(t *testing.T) {
	var h holdState
	rest := map[string]float64{"imu_ax_g": 0.01, "imu_ay_g": 0.98, "imu_az_g": 0.18, "imu_gyro_dps": 0.1}
	h.imuSample(rest) // learns the rest pose
	h.imuSample(rest)
	if h.held {
		t.Fatal("held at rest")
	}
	tilted := map[string]float64{"imu_ax_g": 0.4, "imu_ay_g": 0.9, "imu_az_g": 0.1, "imu_gyro_dps": 1}
	if !h.imuSample(tilted) || !h.held {
		t.Fatal("tilted in hands: not held")
	}
	h.imuSample(rest)
	if !h.held {
		t.Fatal("free after one calm sample")
	}
	h.imuSample(rest)
	if h.held {
		t.Fatal("still held after calm samples at rest")
	}
	turning := map[string]float64{"imu_ax_g": 0.01, "imu_ay_g": 0.98, "imu_az_g": 0.18, "imu_gyro_dps": 30}
	if h.imuSample(turning); !h.held {
		t.Fatal("turning: not held")
	}
}

func TestNoHeadCommandsWhileHeld(t *testing.T) {
	c := &robotConn{hubRobot: &fakeHubRobot{done: make(chan struct{})}}
	c.held.Store(true)
	if c.command("look", map[string]any{"yaw": 10}) || c.command("nod", nil) {
		t.Fatal("a head command went out while held")
	}
	if !c.command("emotion", map[string]any{"name": "happy"}) {
		t.Fatal("other commands must still go")
	}
}

// fakeHubRobot stands in for a connected robot: it takes everything.
type fakeHubRobot struct{ done chan struct{} }

func (f *fakeHubRobot) Command(string, map[string]any) bool { return true }
func (f *fakeHubRobot) Frame(string, any) bool              { return true }
func (f *fakeHubRobot) Binary(byte, []byte) bool            { return true }
func (f *fakeHubRobot) Done() <-chan struct{}               { return f.done }
func (f *fakeHubRobot) Queued() int                         { return 0 }
func (f *fakeHubRobot) Managed() bool                       { return false }
func (f *fakeHubRobot) Close()                              {}
