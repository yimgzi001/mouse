package control

import (
	"testing"
	"time"

	"gesturectl/internal/gesture"
)

func TestKeyboardHandRolesAndOnceUntilRelease(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec string
		left float32
		vk   uint16
	}{{"left middle tab", "ccocc", .99, 0x09}, {"right middle enter", "ccocc", .01, 0x0D}, {"right OK right-alt", "ccooo", .01, 0xA5}} {
		t.Run(tc.name, func(t *testing.T) {
			inj := &recordingInput{}
			c, _ := New(inj, .5)
			pinch := 0
			if tc.spec == "ccooo" {
				pinch = 1
			}
			h := testHand(tc.spec, 0, pinch)
			h.LeftScore = tc.left
			now := feed(c, h, time.Unix(100, 0), 25)
			if countKey(inj, tc.vk, true) != 1 || countKey(inj, tc.vk, false) != 1 || len(inj.keys) != 2 {
				t.Fatalf("wrong key or repeated/held modifier: %v", inj.keys)
			}
			neutral := testHand("coooo", 0, 0)
			neutral.LeftScore = tc.left
			now = feed(c, neutral, now, 10)
			feed(c, h, now, 20)
			if countKey(inj, tc.vk, true) != 2 || len(inj.buttons) != 0 || len(inj.wheels) != 0 {
				t.Fatalf("release did not rearm only the intended key: %+v", inj)
			}
			c.Abort()
			assertReleased(t, inj)
		})
	}
}

func TestMiddleConfirmsInThreeFramesWithoutExtraHold(t *testing.T) {
	for _, left := range []float32{.99, .01} {
		inj := &recordingInput{}
		c, _ := New(inj, .5)
		h := testHand("coooo", 0, 0)
		h.LeftScore = left
		now := feed(c, h, time.Unix(100, 0), 10)
		h = testHand("ccocc", 0, 0)
		h.LeftScore = left
		now = feed(c, h, now, 2)
		if len(inj.keys) != 0 {
			t.Fatal("middle fired before consecutive confirmation")
		}
		feed(c, h, now, 1)
		if len(inj.keys) != 2 {
			t.Fatal("confirmed middle still waits for an extra long hold")
		}
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

func TestReturningLeftMiddleToFistDoesNotClick(t *testing.T) {
	inj := &recordingInput{}
	c, _ := New(inj, .5)
	now := feed(c, testHand("ccocc", 0, 0), time.Unix(100, 0), 20)
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
	for _, pose := range []string{"middle", "ok"} {
		for _, interruption := range []string{"unknown", "paused", "dropout", "pose-change"} {
			t.Run(pose+"/"+interruption, func(t *testing.T) {
				inj := &recordingInput{}
				c, _ := New(inj, .5)
				pinch, spec := 0, "ccocc"
				if pose == "ok" {
					pinch, spec = 1, "ccooo"
				}
				h := testHand(spec, 0, pinch)
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

func TestLeftMiddleHeldDuringSelectionDoesNotReplayTab(t *testing.T) {
	c, inj, owner, now := openedSwitcher(t)
	left := gesture.HandState{ID: 1, Present: true, Side: gesture.SideLeft, Pose: "point", RawPose: "middle"}
	tabs := countKey(inj, 0x09, true)
	for n := 0; n < 5; n++ {
		now = now.Add(33 * time.Millisecond)
		c.engine.Process([]gesture.HandState{left, owner}, now)
	}
	owner.RawPose = "fist"
	now = now.Add(33 * time.Millisecond)
	c.engine.Process([]gesture.HandState{left, owner}, now)
	left.Pose = "middle"
	for n := 0; n < 10; n++ {
		now = now.Add(33 * time.Millisecond)
		c.engine.Process([]gesture.HandState{left, owner}, now)
	}
	if countKey(inj, 0x09, true) != tabs {
		t.Fatal("left middle replayed Tab in the newly selected window")
	}
	c.Abort()
	assertReleased(t, inj)
}
