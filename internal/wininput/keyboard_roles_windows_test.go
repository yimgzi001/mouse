//go:build windows && amd64

package wininput

import (
	"testing"
	"time"
	"unsafe"

	"gesturectl/internal/control"
	"gesturectl/internal/engine"
	"gesturectl/internal/gesture"
)

// Exercise the configured gesture-to-Windows-packet path without sending keys
// to the user's focused application. Right Alt must be distinct and extended.
func TestKeyboardRolesEncodeWindowsKeyPackets(t *testing.T) {
	for _, tc := range []struct {
		name, pose string
		side       gesture.Side
		vk         uint16
		flags      uint32
	}{{"left eight Tab", "eight_back", gesture.SideLeft, 0x09, 0}, {"right eight Enter", "eight_back", gesture.SideRight, 0x0D, 0}, {"right middle-ring-pinky RightAlt", "middle_ring_pinky", gesture.SideRight, 0xA5, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			inj := New()
			defer inj.Close()
			var packets []input
			inj.send = func(p []input) error { packets = append(packets, p...); return nil }
			poses, err := gesture.NewPoseSet(gesture.BuiltinPoses())
			if err != nil {
				t.Fatal(err)
			}
			e := engine.New(inj)
			if err = e.Configure(engine.DefaultSettings(), control.DefaultBindings(), poses.Has); err != nil {
				t.Fatal(err)
			}
			st := gesture.HandState{ID: 1, Present: true, Side: tc.side, Pose: tc.pose, RawPose: tc.pose}
			for n := 0; n < 20; n++ {
				e.Process([]gesture.HandState{st}, time.Unix(100, 0).Add(time.Duration(n)*33*time.Millisecond))
			}
			e.Abort()
			if len(packets) != 2 {
				t.Fatalf("expected a single key down/up without extra modifiers: %+v", packets)
			}
			for n := range packets {
				k := (*keyboardInput)(unsafe.Pointer(&packets[n].Mouse))
				if packets[n].Kind != 1 || k.VK != tc.vk || k.Scan != 0 || k.Flags != tc.flags|uint32(n*2) {
					t.Fatalf("wrong Windows key packet %d: %+v", n, k)
				}
			}
			if len(inj.heldKeys) != 0 {
				t.Fatal("key remained pressed")
			}
		})
	}
}
