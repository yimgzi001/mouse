package vision

import "testing"

func TestSameHandMatchesOverlappingCropOrNearbyPalm(t *testing.T) {
	held := []track{{roi: Rect{CX: 300, CY: 300, W: 200, H: 200}, palmX: 300, palmY: 320, palmLen: 80}}
	for _, tc := range []struct {
		name string
		c    track
		want bool
	}{
		{"overlapping crop", track{roi: Rect{CX: 320, CY: 300, W: 200, H: 200}, palmX: 600, palmY: 600, palmLen: 80}, true},
		{"palm within 0.6 palm lengths", track{roi: Rect{CX: 300, CY: 420, W: 120, H: 120}, palmX: 330, palmY: 350, palmLen: 60}, true},
		{"palm just beyond 0.6 palm lengths", track{roi: Rect{CX: 300, CY: 520, W: 120, H: 120}, palmX: 300, palmY: 370, palmLen: 80}, false},
		{"separate hand", track{roi: Rect{CX: 560, CY: 300, W: 200, H: 200}, palmX: 560, palmY: 320, palmLen: 80}, false},
	} {
		if got := sameHand(held, &tc.c); got != tc.want {
			t.Errorf("%s: sameHand=%t, want %t", tc.name, got, tc.want)
		}
	}
}

func TestDetectionPalmUsesWristAndKnuckles(t *testing.T) {
	var d palmDetection
	d.Keypoints[0] = [2]float32{100, 200}
	d.Keypoints[1] = [2]float32{80, 130}
	d.Keypoints[2] = [2]float32{100, 120}
	d.Keypoints[3] = [2]float32{120, 130}
	d.Keypoints[4] = [2]float32{140, 140}
	d.Keypoints[5] = [2]float32{900, 900}
	x, y, length := detectionPalm(&d)
	if x != 108 || y != 144 || length != 80 {
		t.Fatalf("palm at (%v, %v) length %v, want (108, 144) length 80", x, y, length)
	}
}
