package control

import (
	"testing"
	"time"

	"gesturectl/internal/gesture"
	"gesturectl/internal/vision"
)

func rightHand(spec string) vision.Hand {
	h := testHand(spec, 0, 0)
	h.LeftScore = .01
	return h
}

func TestLeftFistClicksOnceAndRearmsAfterRelease(t *testing.T) {
	inj := &recordingInput{x: 500, y: 500}
	c, _ := New(inj, .5)
	now := time.Unix(100, 0)
	right := rightHand("coccc")
	right.TrackID = 2
	left := testHand("ccccc", 0, 0)
	shift(&left, -150, 0)
	frame := func() {
		c.Process([]vision.Hand{left, right}, now)
		now = now.Add(33 * time.Millisecond)
	}
	for n := 0; n < 25; n++ {
		frame()
	}
	if len(inj.buttons) != 2 || inj.buttons[0] != "left down" || inj.buttons[1] != "left up" {
		t.Fatalf("held left fist did not click once: %v", inj.buttons)
	}
	if inj.x != 500 || inj.y != 500 || inj.moves != 0 {
		t.Fatal("left fist shifted the right hand's cursor")
	}
	// A short tracking interruption does not count as opening the fist.
	c.Process([]vision.Hand{right}, now)
	now = now.Add(66 * time.Millisecond)
	for n := 0; n < 15; n++ {
		frame()
	}
	if len(inj.buttons) != 2 {
		t.Fatal("tracking dropout repeated click")
	}
	left = testHand("coooo", 0, 0)
	shift(&left, -150, 0)
	for n := 0; n < 12; n++ {
		frame()
	}
	left = testHand("ccccc", 0, 0)
	shift(&left, -150, 0)
	for n := 0; n < 20; n++ {
		frame()
	}
	if len(inj.buttons) != 4 {
		t.Fatalf("release and fist did not click again: %v", inj.buttons)
	}
	c.Abort()
	assertReleased(t, inj)
}

func TestLeftHandCannotOpenOrQuickCycleWindows(t *testing.T) {
	for _, spec := range []string{"ooooo", "coooo"} {
		for _, dx := range []float32{-24, 24} {
			inj := &recordingInput{}
			c, _ := New(inj, .5)
			h := testHand(spec, 0, 0)
			now := feed(c, h, time.Unix(100, 0), 10)
			for n := 0; n < 8; n++ {
				shift(&h, dx, 0)
				now = feed(c, h, now, 1)
			}
			if countKey(inj, 0x12, true) != 0 {
				t.Fatalf("left %s switched windows: %v", spec, inj.keys)
			}
			c.Abort()
		}
	}
}

func TestLeftFistRejectsUnknownPausedAndTransientInput(t *testing.T) {
	for _, condition := range []string{"unknown", "paused", "transient"} {
		t.Run(condition, func(t *testing.T) {
			inj := &recordingInput{}
			c, _ := New(inj, .5)
			h := testHand("ccccc", 0, 0)
			now := time.Unix(100, 0)
			switch condition {
			case "unknown":
				h.LeftScore = .5
				feed(c, h, now, 30)
			case "paused":
				c.SetPaused(true)
				feed(c, h, now, 30)
			case "transient":
				now = feed(c, testHand("coooo", 0, 0), now, 10)
				now = feed(c, h, now, 1)
				feed(c, testHand("coooo", 0, 0), now, 10)
			}
			if len(inj.buttons) != 0 {
				t.Fatalf("unexpected fist click: %v", inj.buttons)
			}
			c.Abort()
		})
	}
}

func TestLeftFistDuringSwitcherCannotClickAfterConfirmation(t *testing.T) {
	c, inj, owner, now := openedSwitcher(t)
	left := gesture.HandState{ID: 1, Present: true, Side: gesture.SideLeft, Pose: "point", RawPose: "fist"}
	for n := 0; n < 6; n++ {
		now = now.Add(33 * time.Millisecond)
		c.engine.Process([]gesture.HandState{left, owner}, now)
	}
	if len(inj.buttons) != 0 || countKey(inj, 0x12, false) != 0 {
		t.Fatal("left fist interrupted right-hand selection")
	}
	owner.RawPose = "fist"
	now = now.Add(33 * time.Millisecond)
	c.engine.Process([]gesture.HandState{left, owner}, now)
	left.Pose = "fist" // majority pose may catch up only after selection closes
	for n := 0; n < 10; n++ {
		now = now.Add(33 * time.Millisecond)
		c.engine.Process([]gesture.HandState{left, owner}, now)
	}
	if len(inj.buttons) != 0 {
		t.Fatalf("held left fist clicked in newly selected window: %v", inj.buttons)
	}
	left.Pose, left.RawPose = "four", "four"
	now = now.Add(33 * time.Millisecond)
	c.engine.Process([]gesture.HandState{left, owner}, now)
	left.Pose, left.RawPose = "fist", "fist"
	for n := 0; n < 10; n++ {
		now = now.Add(33 * time.Millisecond)
		c.engine.Process([]gesture.HandState{left, owner}, now)
	}
	if len(inj.buttons) != 2 || countKey(inj, 0x1B, true) != 0 {
		t.Fatalf("fist release did not rearm normal click: %v", inj.buttons)
	}
	c.Abort()
	assertReleased(t, inj)
}
