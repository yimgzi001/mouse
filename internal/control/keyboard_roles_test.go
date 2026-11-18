package control

import (
	"testing"
	"time"

	"gesturectl/internal/gesture"
	"gesturectl/internal/vision"
)

// facingHand is testHand held up by the left or the right hand with the back
// of the hand or the palm towards the camera. testHand shows the palm of a
// left hand and the back of a right hand; its mirror image swaps the two.
func facingHand(spec string, left, back bool) vision.Hand {
	h := testHand(spec, 0, 0)
	h.LeftScore = .01
	if left {
		h.LeftScore = .99
	}
	if left == back {
		for n := range h.World {
			h.World[n].X = -h.World[n].X
			h.Landmarks[n].X = 640 - h.Landmarks[n].X
		}
	}
	return h
}

func TestKeyboardHandRolesAndOnceUntilRelease(t *testing.T) {
	rightAlt := testHand("ccooo", 0, 0)
	rightAlt.LeftScore = .01
	backspace := testHand("ccocc", -90, 0)
	backspace.LeftScore = .01
	for _, tc := range []struct {
		name string
		hand vision.Hand
		vk   uint16
	}{{"left pinky tab", facingHand("cccco", true, false), 0x09}, {"right pinky enter", facingHand("cccco", false, true), 0x0D}, {"right middle-ring-pinky right-alt", rightAlt, 0xA5}, {"right middle pointing left backspace", backspace, 0x08}} {
		t.Run(tc.name, func(t *testing.T) {
			inj := &recordingInput{}
			c, _ := New(inj, .5)
			now := feed(c, tc.hand, time.Unix(100, 0), 25)
			if countKey(inj, tc.vk, true) != 1 || countKey(inj, tc.vk, false) != 1 || len(inj.keys) != 2 {
				t.Fatalf("wrong key or repeated/held modifier: %v", inj.keys)
			}
			neutral := testHand("coooo", 0, 0)
			neutral.LeftScore = tc.hand.LeftScore
			now = feed(c, neutral, now, 10)
			feed(c, tc.hand, now, 20)
			if countKey(inj, tc.vk, true) != 2 || len(inj.buttons) != 0 || len(inj.wheels) != 0 {
				t.Fatalf("release did not rearm only the intended key: %+v", inj)
			}
			c.Abort()
			assertReleased(t, inj)
		})
	}
}

func TestPinkyConfirmsInFiveFramesWithoutExtraHold(t *testing.T) {
	for _, left := range []bool{true, false} {
		inj := &recordingInput{}
		c, _ := New(inj, .5)
		now := feed(c, facingHand("coooo", left, false), time.Unix(100, 0), 10)
		h := facingHand("cccco", left, false)
		now = feed(c, h, now, 4)
		if len(inj.keys) != 0 {
			t.Fatal("little finger fired before its confirmation time")
		}
		feed(c, h, now, 1)
		if len(inj.keys) != 2 {
			t.Fatal("confirmed little finger still waits for an extra long hold")
		}
	}
}

func TestBackspaceNeedsTheRightMiddleFingerPointingLeft(t *testing.T) {
	middle := func(rotation float64, left bool) vision.Hand {
		h := testHand("ccocc", rotation, 0)
		h.LeftScore = .01
		if left {
			h.LeftScore = .99
		}
		return h
	}
	for _, tc := range []struct {
		name string
		hand vision.Hand
	}{{"right middle up", middle(0, false)}, {"right middle pointing right", middle(90, false)}, {"left middle pointing left", middle(-90, true)}} {
		inj := &recordingInput{}
		c, _ := New(inj, .5)
		feed(c, tc.hand, time.Unix(100, 0), 25)
		if len(inj.keys) != 0 || len(inj.buttons) != 0 {
			t.Errorf("%s pressed keys %v, buttons %v", tc.name, inj.keys, inj.buttons)
		}
	}
	inj := &recordingInput{}
	c, _ := New(inj, .5)
	now := feed(c, middle(0, false), time.Unix(100, 0), 10)
	h := middle(-90, false)
	now = feed(c, h, now, 4)
	if len(inj.keys) != 0 {
		t.Fatal("Backspace fired before its confirmation time")
	}
	feed(c, h, now, 1)
	if countKey(inj, 0x08, true) != 1 || len(inj.keys) != 2 {
		t.Fatalf("keys %v, want one Backspace press and release", inj.keys)
	}
}

// Thumb and index out with the back of the hand to the camera only point, and
// the little finger presses its key once the hand holds still.
func TestPinkyNeedsAStillHand(t *testing.T) {
	for _, left := range []bool{true, false} {
		inj := &recordingInput{}
		c, _ := New(inj, .5)
		now := feed(c, facingHand("coooo", left, true), time.Unix(100, 0), 10)
		now = feed(c, facingHand("ooccc", left, true), now, 20)
		if len(inj.keys) != 0 {
			t.Fatalf("thumb and index with the back of the hand sent %v", inj.keys)
		}
		h := facingHand("cccco", left, true)
		for n := 0; n < 15; n++ {
			shift(&h, 12, 0)
			now = feed(c, h, now, 1)
		}
		if len(inj.keys) != 0 {
			t.Fatalf("a little finger on the move sent %v", inj.keys)
		}
		feed(c, h, now, 10)
		if len(inj.keys) != 2 {
			t.Fatalf("a little finger held still sent %v", inj.keys)
		}
	}
}

// Swapping the hands swaps Tab and Enter whichever side of the hand faces the
// camera.
func TestSwappedHandsSwapTheLittleFingerKeys(t *testing.T) {
	for _, tc := range []struct {
		name       string
		left, back bool
		vk         uint16
	}{
		{"detected left back is the right hand", true, true, 0x0D},
		{"detected right back is the left hand", false, true, 0x09},
		{"detected left palm is the right hand", true, false, 0x0D},
		{"detected right palm is the left hand", false, false, 0x09},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, later := range []bool{false, true} {
				inj := &recordingInput{}
				p := DefaultPreferences()
				p.SwapHands = !later
				c, err := NewWithPreferences(inj, p)
				if err != nil {
					t.Fatal(err)
				}
				if later {
					c.SetSwapHands(true)
				}
				feed(c, facingHand("cccco", tc.left, tc.back), time.Unix(100, 0), 25)
				if len(inj.keys) != 2 || countKey(inj, tc.vk, true) != 1 {
					t.Fatalf("swapped later %v: keys %v", later, inj.keys)
				}
			}
		})
	}
}

func TestThumbAndLeftOKDoNotSendKeys(t *testing.T) {
	for _, spec := range []string{"occcc", "ccooo"} {
		for _, left := range []float32{.99, .01} {
			if spec == "ccooo" && left < .5 {
				continue
			}
			inj := &recordingInput{}
			c, _ := New(inj, .5)
			pinch := 0
			if spec == "ccooo" {
				pinch = 1
			}
			h := testHand(spec, -48, pinch)
			h.LeftScore = left
			feed(c, h, time.Unix(100, 0), 25)
			if len(inj.keys) != 0 || len(inj.buttons) != 0 || len(inj.wheels) != 0 {
				t.Fatalf("unbound thumb/left OK produced input: %+v", inj)
			}
		}
	}
}

func TestReturningLeftPinkyToFistDoesNotClick(t *testing.T) {
	inj := &recordingInput{}
	c, _ := New(inj, .5)
	now := feed(c, facingHand("cccco", true, false), time.Unix(100, 0), 20)
	if countKey(inj, 0x09, true) != 1 {
		t.Fatalf("left little finger did not press Tab: %v", inj.keys)
	}
	now = feed(c, testHand("ccccc", 0, 0), now, 20)
	if len(inj.buttons) != 0 {
		t.Fatalf("releasing Tab to a fist clicked the old cursor target: %v", inj.buttons)
	}
	now = feed(c, testHand("coooo", 0, 0), now, 12)
	feed(c, testHand("ccccc", 0, 0), now, 20)
	if len(inj.buttons) != 2 {
		t.Fatal("deliberate release and fist did not restore clicking")
	}
}

func TestKeyboardGesturesRejectUnknownPausedAndInterruptedCandidates(t *testing.T) {
	for _, pose := range []string{"pinky", "middle_ring_pinky"} {
		for _, interruption := range []string{"unknown", "paused", "dropout", "pose-change"} {
			t.Run(pose+"/"+interruption, func(t *testing.T) {
				inj := &recordingInput{}
				c := newSettled(t, inj)
				spec := "cccco"
				if pose == "middle_ring_pinky" {
					spec = "ccooo"
				}
				h := testHand(spec, 0, 0)
				h.LeftScore = .01
				now := time.Unix(100, 0)
				switch interruption {
				case "unknown":
					h.LeftScore = .5
					feed(c, h, now, 20)
				case "paused":
					c.SetPaused(true)
					feed(c, h, now, 20)
				default:
					now = feed(c, h, now, 1)
					if interruption == "dropout" {
						c.Process(nil, now)
						now = now.Add(66 * time.Millisecond)
					} else {
						now = feed(c, rightHand("ccccc"), now, 1)
					}
					feed(c, h, now, 1)
				}
				if len(inj.keys) != 0 {
					t.Fatalf("invalid candidate sent a key: %v", inj.keys)
				}
				c.Abort()
				assertReleased(t, inj)
			})
		}
	}
}

func TestLeftPinkyHeldDuringSelectionDoesNotReplayTab(t *testing.T) {
	c, inj, owner, now := openedSwitcher(t)
	left := gesture.HandState{ID: 1, Present: true, Side: gesture.SideLeft, Pose: "point", RawPose: "pinky"}
	tabs := countKey(inj, 0x09, true)
	for n := 0; n < 5; n++ {
		now = now.Add(33 * time.Millisecond)
		c.engine.Process([]gesture.HandState{left, owner}, now)
	}
	owner.RawPose = "fist"
	now = now.Add(33 * time.Millisecond)
	c.engine.Process([]gesture.HandState{left, owner}, now)
	left.Pose = "pinky"
	for n := 0; n < 10; n++ {
		now = now.Add(33 * time.Millisecond)
		c.engine.Process([]gesture.HandState{left, owner}, now)
	}
	if countKey(inj, 0x09, true) != tabs {
		t.Fatal("left little finger replayed Tab in the newly selected window")
	}
	c.Abort()
	assertReleased(t, inj)
}
