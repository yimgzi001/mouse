package control

import (
	"gesturectl/internal/engine"
	"gesturectl/internal/gesture"
	"testing"
	"time"
)

// newQuickCycle is New(inj, .5) plus right-hand four-finger swipes that cycle
// windows. They were default bindings until configuration version 4 and are
// still available as custom ones. settled drops the new-hand observation time.
func newQuickCycle(t *testing.T, inj *recordingInput, settled bool) *Controller {
	t.Helper()
	p := DefaultPreferences()
	p.MoveLeft, p.MoveRight = .5, .5
	cfg := DefaultConfiguration(p)
	if settled {
		cfg.Recognition.NewHandMs = 0
	}
	for _, swipe := range [][2]string{{"swipe_right", "next"}, {"swipe_left", "previous"}} {
		cfg.Bindings = append(cfg.Bindings, engine.Binding{ID: "quick-" + swipe[1], Name: "quick " + swipe[1], Enabled: true, Hand: "right", Pose: "four", Trigger: swipe[0],
			Action: engine.Action{Type: "window_cycle", Params: map[string]any{"direction": swipe[1]}}})
	}
	c, err := NewWithConfiguration(inj, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFiveOpensOnceSteadyAndFistConfirms(t *testing.T) {
	inj := &recordingInput{}
	c := newSettled(t, inj)
	now := feed(c, rightHand("ooooo"), time.Unix(100, 0), 4)
	if countKey(inj, 0x12, true) != 0 {
		t.Fatal("five opened the switcher before it was steady")
	}
	now = feed(c, rightHand("ooooo"), now, 2)
	if countKey(inj, 0x12, true) != 1 {
		t.Fatal("steady five did not open the switcher")
	}
	tabs := countKey(inj, 0x09, true)
	feed(c, rightHand("ccccc"), now, 10)
	if countKey(inj, 0x12, false) != 1 || countKey(inj, 0x1B, true) != 0 || countKey(inj, 0x09, true) != tabs {
		t.Fatalf("fist did not confirm highlighted window: %v", inj.keys)
	}
	_, events := c.Feedback()
	confirmed := false
	for _, ev := range events {
		if ev.Action == "window_switcher" && ev.What == "confirm" {
			confirmed = true
		}
	}
	if !confirmed {
		t.Fatal("fist was not reported as confirmation")
	}
	assertReleased(t, inj)
}
func TestTransientFiveDoesNotOpenSwitcher(t *testing.T) {
	inj := &recordingInput{}
	c, _ := New(inj, .5)
	now := feed(c, rightHand("ccccc"), time.Unix(100, 0), 10)
	now = feed(c, rightHand("ooooo"), now, 1)
	feed(c, rightHand("ccccc"), now, 10)
	if countKey(inj, 0x12, true) != 0 {
		t.Fatal("single-frame five opened switcher")
	}
}

func TestRightFiveStraightAfterAnotherPoseWaitsLonger(t *testing.T) {
	for _, spec := range []string{"coccc", "coooo"} {
		inj := &recordingInput{}
		c, _ := New(inj, .5)
		h := rightHand(spec)
		now := feed(c, h, time.Unix(100, 0), 15)
		h = rightHand("ooooo")
		now = feed(c, h, now, 7)
		if countKey(inj, 0x12, true) != 0 {
			t.Fatalf("five straight after %s opened the switcher within 0.2 s", spec)
		}
		feed(c, h, now, 2)
		if countKey(inj, 0x12, true) != 1 {
			t.Fatalf("old %s votes delayed five", spec)
		}
		c.Abort()
		assertReleased(t, inj)
	}
}

func TestFiveConfirmationRestartsAfterDropoutPoseChangeOrLongGap(t *testing.T) {
	for _, interruption := range []string{"dropout", "pose-change", "gap"} {
		t.Run(interruption, func(t *testing.T) {
			inj := &recordingInput{}
			c := newSettled(t, inj)
			now := time.Unix(100, 0)
			h := rightHand("ooooo")
			now = feed(c, h, now, 4)
			switch interruption {
			case "dropout":
				c.Process(nil, now)
				now = now.Add(33 * time.Millisecond)
			case "pose-change":
				now = feed(c, rightHand("ccccc"), now, 1)
			case "gap":
				now = now.Add(160 * time.Millisecond)
			}
			now = feed(c, h, now, 4)
			if countKey(inj, 0x12, true) != 0 {
				t.Fatal("stale five observations opened selector")
			}
			feed(c, h, now, 8)
			if countKey(inj, 0x12, true) != 1 {
				t.Fatal("fresh steady five did not open selector")
			}
			c.Abort()
			assertReleased(t, inj)
		})
	}
}

func TestSwitcherFollowsPalmDespiteFingerClassificationChanges(t *testing.T) {
	c, inj, st, now := openedSwitcher(t)
	for direction, dx := range []float32{8, -8} {
		before := countKey(inj, 0x09, true)
		shiftBefore := countKey(inj, 0x10, true)
		for n := 0; n < 20; n++ {
			// The real right-hand log alternated between five and four as
			// the thumb estimate changed while all four fingers stayed open.
			st.Pose, st.RawPose = "four", "four"
			if n%4 == 0 {
				st.Pose, st.RawPose = "five", "five"
			}
			st.Features.PalmX += dx
			now = now.Add(33 * time.Millisecond)
			c.engine.Process([]gesture.HandState{st}, now)
		}
		if countKey(inj, 0x09, true)-before < 2 {
			t.Fatalf("right palm direction %v stopped following: %v", dx, inj.keys)
		}
		if direction == 1 && countKey(inj, 0x10, true)-shiftBefore < 2 {
			t.Fatal("return swing did not reverse selection")
		}
	}
	if len(inj.buttons) != 0 || len(inj.wheels) != 0 || inj.moves != 0 {
		t.Fatal("finger fluctuation triggered another action during selection")
	}
	st.RawPose = "fist"
	c.engine.Process([]gesture.HandState{st}, now.Add(33*time.Millisecond))
	if countKey(inj, 0x1B, true) != 0 {
		t.Fatal("fist cancelled instead of confirming")
	}
	assertReleased(t, inj)
}
func TestFourPalmQuickCycleDirectionsAndReturnCooldown(t *testing.T) {
	for _, dx := range []float32{-24, 24} {
		inj := &recordingInput{}
		c := newQuickCycle(t, inj, false)
		h := rightHand("coooo")
		now := feed(c, h, time.Unix(100, 0), 10)
		for n := 0; n < 4; n++ {
			shift(&h, dx, 0)
			now = feed(c, h, now, 1)
		}
		for n := 0; n < 4; n++ {
			shift(&h, -dx, 0)
			now = feed(c, h, now, 1)
		}
		if countKey(inj, 0x12, true) != 1 || countKey(inj, 0x12, false) != 1 || countKey(inj, 0x09, true) != 1 {
			t.Fatalf("quick palm did not cycle once: %v", inj.keys)
		}
		wantShift := 0
		if dx < 0 {
			wantShift = 1
		}
		if countKey(inj, 0x10, true) != wantShift {
			t.Fatalf("wrong direction: %v", inj.keys)
		}
		assertReleased(t, inj)
	}
}
func TestFourPalmSlowAndVerticalMovementDoNotCycle(t *testing.T) {
	for _, delta := range [][2]float32{{2, 0}, {0, 24}} {
		inj := &recordingInput{}
		c := newQuickCycle(t, inj, false)
		h := rightHand("coooo")
		now := feed(c, h, time.Unix(100, 0), 10)
		for n := 0; n < 30; n++ {
			shift(&h, delta[0], delta[1])
			now = feed(c, h, now, 1)
		}
		if countKey(inj, 0x12, true) != 0 {
			t.Fatal("non-horizontal-fast motion switched windows")
		}
	}
}
func TestFourPalmDoesNotInheritAnotherPoseOrDropoutMotion(t *testing.T) {
	for _, dropout := range []bool{false, true} {
		inj := &recordingInput{}
		c := newQuickCycle(t, inj, false)
		h := rightHand("coccc")
		if dropout {
			h = rightHand("coooo")
		}
		now := feed(c, h, time.Unix(100, 0), 10)
		for n := 0; n < 3; n++ {
			shift(&h, 15, 0)
			now = feed(c, h, now, 1)
		}
		if dropout {
			c.Process(nil, now)
			now = now.Add(66 * time.Millisecond)
		}
		four := rightHand("coooo")
		shift(&four, 60, 0)
		if dropout {
			shift(&four, 140, 0)
		}
		now = feed(c, four, now, 1)
		shift(&four, 15, 0)
		feed(c, four, now, 1)
		if countKey(inj, 0x12, true) != 0 {
			t.Fatalf("inherited motion triggered cycle (dropout=%t): %v", dropout, inj.keys)
		}
	}
}

func TestUnusedPointSwipeDoesNotDelayFourPalmQuickSwitch(t *testing.T) {
	inj := &recordingInput{}
	c := newQuickCycle(t, inj, false)
	h := rightHand("coccc")
	now := feed(c, h, time.Unix(100, 0), 10)
	for n := 0; n < 4; n++ {
		shift(&h, 30, 0)
		now = feed(c, h, now, 1)
	}
	four := rightHand("coooo")
	shift(&four, 120, 0)
	now = feed(c, four, now, 1)
	for n := 0; n < 4; n++ {
		shift(&four, 24, 0)
		now = feed(c, four, now, 1)
	}
	if countKey(inj, 0x12, true) != 1 {
		t.Fatalf("unused pointing swipe suppressed quick palm: %v", inj.keys)
	}
	assertReleased(t, inj)
}
func TestQuickPalmBeforePoseVoteReleasesDragAndAllKeys(t *testing.T) {
	inj := &recordingInput{}
	c := newQuickCycle(t, inj, false)
	now := time.Unix(100, 0)
	st := gesture.HandState{ID: 1, Present: true, Side: gesture.SideRight, Pose: "three", RawPose: "three", Features: gesture.Features{PalmSize: 100}}
	c.engine.Process([]gesture.HandState{st}, now)
	c.engine.Process([]gesture.HandState{st}, now.Add(250*time.Millisecond))
	st.RawPose = "four"
	st.Swipe = "right"
	c.engine.Process([]gesture.HandState{st}, now.Add(283*time.Millisecond))
	if len(inj.buttons) != 2 || inj.buttons[1] != "left up" || countKey(inj, 0x12, true) != 1 {
		t.Fatalf("quick cycle did not preempt drag: %+v", inj)
	}
	assertReleased(t, inj)
}

func TestLeftQuickSwipeDoesNotInterruptRightDrag(t *testing.T) {
	inj := &recordingInput{}
	c := newQuickCycle(t, inj, true)
	now := time.Unix(100, 0)
	drag := gesture.HandState{ID: 1, Present: true, Side: gesture.SideRight, Pose: "three", RawPose: "three", Features: gesture.Features{PalmSize: 100}}
	c.engine.Process([]gesture.HandState{drag}, now)
	c.engine.Process([]gesture.HandState{drag}, now.Add(150*time.Millisecond))
	palm := gesture.HandState{ID: 2, Present: true, Side: gesture.SideLeft, Pose: "four", RawPose: "four", Swipe: "right"}
	c.engine.Process([]gesture.HandState{drag, palm}, now.Add(183*time.Millisecond))
	palm.Swipe = ""
	c.engine.Process([]gesture.HandState{drag, palm}, now.Add(216*time.Millisecond))
	if len(inj.buttons) != 1 || countKey(inj, 0x12, true) != 0 {
		t.Fatalf("left swipe interrupted right drag: %v", inj.buttons)
	}
	c.Abort()
	assertReleased(t, inj)
}
func TestQuickPalmFromHandThatJustAppearedIsIgnored(t *testing.T) {
	inj := &recordingInput{}
	c := newQuickCycle(t, inj, false)
	now := time.Unix(100, 0)
	st := gesture.HandState{ID: 1, Present: true, Side: gesture.SideRight, Pose: "four", RawPose: "four", Swipe: "right", Features: gesture.Features{PalmSize: 100}}
	c.engine.Process([]gesture.HandState{st}, now)
	if len(inj.keys) != 0 {
		t.Fatalf("a passing palm switched windows: %v", inj.keys)
	}
	st.Swipe = ""
	c.engine.Process([]gesture.HandState{st}, now.Add(100*time.Millisecond))
	st.Swipe = "right"
	c.engine.Process([]gesture.HandState{st}, now.Add(233*time.Millisecond))
	if countKey(inj, 0x12, true) != 1 {
		t.Fatalf("an observed palm could not switch windows: %v", inj.keys)
	}
	c.Abort()
	assertReleased(t, inj)
}

func TestQuickPalmIgnoredWhilePausedUnknownOrVisualSwitcher(t *testing.T) {
	inj := &recordingInput{}
	c := newQuickCycle(t, inj, false)
	now := time.Unix(100, 0)
	st := gesture.HandState{ID: 2, Present: true, Side: gesture.SideRight, Pose: "five", RawPose: "five", Features: gesture.Features{PalmSize: 100}}
	c.engine.Process([]gesture.HandState{st}, now)
	now = now.Add(300 * time.Millisecond)
	c.engine.Process([]gesture.HandState{st}, now)
	if countKey(inj, 0x12, true) != 1 {
		t.Fatalf("switcher not opened: %v", inj.keys)
	}
	before := countKey(inj, 0x09, true)
	st.Pose, st.RawPose, st.Swipe = "four", "four", "right"
	c.engine.Process([]gesture.HandState{st}, now.Add(33*time.Millisecond))
	if countKey(inj, 0x09, true) != before || countKey(inj, 0x12, false) != 0 {
		t.Fatal("quick cycle interfered with visual switcher")
	}
	c.Abort()
	for _, paused := range []bool{false, true} {
		inj := &recordingInput{}
		c := newQuickCycle(t, inj, true)
		c.SetPaused(paused)
		side := gesture.SideUnknown
		if paused {
			side = gesture.SideLeft
		}
		st := gesture.HandState{ID: 1, Present: true, Side: side, RawPose: "four", Pose: "four", Swipe: "right"}
		c.engine.Process([]gesture.HandState{st}, now)
		if len(inj.keys) != 0 {
			t.Fatal("paused/unknown hand switched windows")
		}
	}
}
