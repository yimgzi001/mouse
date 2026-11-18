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
	for _, tc := range []struct {
		name string
		hand vision.Hand
		vk   uint16
	}{{"left eight tab", facingHand("ooccc", true, true), 0x09}, {"right eight enter", facingHand("ooccc", false, true), 0x0D}, {"right middle-ring-pinky right-alt", rightAlt, 0xA5}} {
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

func TestEightConfirmsInFiveFramesWithoutExtraHold(t *testing.T) {
	for _, left := range []bool{true, false} {
		inj := &recordingInput{}
		c, _ := New(inj, .5)
		now := feed(c, facingHand("coooo", left, true), time.Unix(100, 0), 10)
		h := facingHand("ooccc", left, true)
		now = feed(c, h, now, 4)
		if len(inj.keys) != 0 {
			t.Fatal("eight fired before its confirmation time")
		}
		feed(c, h, now, 1)
		if len(inj.keys) != 2 {
			t.Fatal("confirmed eight still waits for an extra long hold")
		}
	}
}

func TestEightNeedsTheBackOfAStillHand(t *testing.T) {
	for _, left := range []bool{true, false} {
		inj := &recordingInput{}
		c, _ := New(inj, .5)
		now := feed(c, facingHand("coooo", left, true), time.Unix(100, 0), 10)
		now = feed(c, facingHand("ooccc", left, false), now, 20)
		if len(inj.keys) != 0 {
			t.Fatalf("thumb and index with the palm towards the camera sent %v", inj.keys)
		}
		h := facingHand("ooccc", left, true)
		for n := 0; n < 15; n++ {
			shift(&h, 12, 0)
			now = feed(c, h, now, 1)
		}
		if len(inj.keys) != 0 {
			t.Fatalf("an eight on the move sent %v", inj.keys)
		}
		feed(c, h, now, 10)
		if len(inj.keys) != 2 {
			t.Fatalf("an eight held still sent %v", inj.keys)
		}
	}
}

// A camera that reverses handedness mirrors the landmarks too, so swapping the
// hands must not swap the palm and the back of the hand.
func TestSwappedHandsStillNeedTheBackOfTheHand(t *testing.T) {
	for _, tc := range []struct {
		name       string
		left, back bool
		vk         uint16
	}{
		{"detected left back is the right hand", true, true, 0x0D},
		{"detected right back is the left hand", false, true, 0x09},
		{"detected left palm", true, false, 0},
		{"detected right palm", false, false, 0},
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
				feed(c, facingHand("ooccc", tc.left, tc.back), time.Unix(100, 0), 25)
				want := 0
				if tc.vk != 0 {
					want = 2
				}
				if len(inj.keys) != want || (want > 0 && countKey(inj, tc.vk, true) != 1) {
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

func TestReturningLeftEightToFistDoesNotClick(t *testing.T) {
	inj := &recordingInput{}
	c, _ := New(inj, .5)
	now := feed(c, facingHand("ooccc", true, true), time.Unix(100, 0), 20)
	if countKey(inj, 0x09, true) != 1 {
		t.Fatalf("left eight did not press Tab: %v", inj.keys)
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
	for _, pose := range []string{"eight_back", "middle_ring_pinky"} {
		for _, interruption := range []string{"unknown", "paused", "dropout", "pose-change"} {
			t.Run(pose+"/"+interruption, func(t *testing.T) {
				inj := &recordingInput{}
				c := newSettled(t, inj)
				spec := "ooccc"
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

func TestLeftEightHeldDuringSelectionDoesNotReplayTab(t *testing.T) {
	c, inj, owner, now := openedSwitcher(t)
	left := gesture.HandState{ID: 1, Present: true, Side: gesture.SideLeft, Pose: "point", RawPose: "eight_back"}
	tabs := countKey(inj, 0x09, true)
	for n := 0; n < 5; n++ {
		now = now.Add(33 * time.Millisecond)
		c.engine.Process([]gesture.HandState{left, owner}, now)
	}
	owner.RawPose = "fist"
	now = now.Add(33 * time.Millisecond)
	c.engine.Process([]gesture.HandState{left, owner}, now)
	left.Pose = "eight_back"
	for n := 0; n < 10; n++ {
		now = now.Add(33 * time.Millisecond)
		c.engine.Process([]gesture.HandState{left, owner}, now)
	}
	if countKey(inj, 0x09, true) != tabs {
		t.Fatal("left eight replayed Tab in the newly selected window")
	}
	c.Abort()
	assertReleased(t, inj)
}
