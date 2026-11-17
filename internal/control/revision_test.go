package control

import (
	"gesturectl/internal/gesture"
	"gesturectl/internal/vision"
	"testing"
	"time"
)

func TestRevisedClickDragAndKeys(t *testing.T) {
	t.Run("right pinch left-click", func(t *testing.T) {
		inj := &recordingInput{}
		c, _ := New(inj, .5)
		h := testHand("ccccc", 0, 1)
		h.LeftScore = .01
		feed(c, h, time.Unix(100, 0), 35)
		if len(inj.buttons) != 2 || inj.buttons[0] != "left down" || inj.buttons[1] != "left up" {
			t.Fatalf("pinch: %v", inj.buttons)
		}
	})
	t.Run("left two right-click", func(t *testing.T) {
		inj := &recordingInput{}
		c, _ := New(inj, .5)
		feed(c, testHand("coocc", 0, 0), time.Unix(100, 0), 35)
		if len(inj.buttons) != 2 || inj.buttons[0] != "right down" || inj.buttons[1] != "right up" {
			t.Fatalf("left two: %v", inj.buttons)
		}
	})
	t.Run("right three drag", func(t *testing.T) {
		inj := &recordingInput{x: 500, y: 500}
		c, _ := New(inj, .5)
		h := testHand("coooc", 0, 0)
		h.LeftScore = .01
		now := feed(c, h, time.Unix(100, 0), 20)
		for n := 0; n < 10; n++ {
			shift(&h, 3, 0)
			now = feed(c, h, now, 1)
		}
		if len(inj.buttons) != 1 || inj.buttons[0] != "left down" || inj.x <= 500 {
			t.Fatalf("drag: %+v", inj)
		}
		feed(c, rightHand("ccccc"), now, 15)
		if len(inj.buttons) != 2 || inj.buttons[1] != "left up" {
			t.Fatal("drag did not release")
		}
	})
	for _, tc := range []struct {
		name, spec string
		left       float32
		vk         uint16
	}{{"left middle tab", "ccocc", .99, 0x09}, {"right middle enter", "ccocc", .01, 0x0D}} {
		t.Run(tc.name, func(t *testing.T) {
			inj := &recordingInput{}
			c, _ := New(inj, .5)
			h := testHand(tc.spec, 0, 0)
			h.LeftScore = tc.left
			now := feed(c, h, time.Unix(100, 0), 35)
			if countKey(inj, tc.vk, true) != 1 {
				t.Fatalf("keys: %v", inj.keys)
			}
			now = feed(c, testHand("ccccc", 0, 0), now, 15)
			feed(c, h, now, 25)
			if countKey(inj, tc.vk, true) != 2 {
				t.Fatal("key gesture did not rearm")
			}
			c.Abort()
			assertReleased(t, inj)
		})
	}
}
func TestRemovedGesturesDoNotTriggerLegacyActions(t *testing.T) {
	for _, h := range []vision.Hand{testHand("cocco", 0, 0), testHand("ccooo", 0, 1), testHand("occcc", 132, 0), testHand("occcc", -138, 0), testHand("occcc", 42, 0)} {
		inj := &recordingInput{}
		c, _ := New(inj, .5)
		now := feed(c, h, time.Unix(100, 0), 35)
		for n := 0; n < 10; n++ {
			shift(&h, 5, -5)
			now = feed(c, h, now, 1)
		}
		if len(inj.wheels) != 0 || len(inj.buttons) != 0 || len(inj.keys) != 0 {
			t.Fatalf("removed gesture produced input: %+v", inj)
		}
	}
}

func pairStates(distance float32) []gesture.HandState {
	left := gesture.HandState{ID: 1, Present: true, Side: gesture.SideLeft, Pose: "point", RawPose: "point", Features: gesture.Features{PalmSize: 100}}
	right := left
	right.ID = 2
	right.Side = gesture.SideRight
	left.Landmarks[vision.IndexTip] = vision.Point{X: 300 - distance/2, Y: 200}
	right.Landmarks[vision.IndexTip] = vision.Point{X: 300 + distance/2, Y: 200}
	return []gesture.HandState{left, right}
}
func TestTwoIndexDistanceZoomsWithoutMovingCursor(t *testing.T) {
	inj := &recordingInput{x: 500, y: 500}
	c, _ := New(inj, .5)
	now := time.Unix(100, 0)
	for n := 0; n < 6; n++ {
		c.engine.Process(pairStates(200), now)
		now = now.Add(33 * time.Millisecond)
	}
	for n := 0; n < 12; n++ {
		c.engine.Process(pairStates(200+float32(n)*12), now)
		now = now.Add(33 * time.Millisecond)
	}
	up := 0
	for _, w := range inj.wheels {
		up += w.delta
	}
	if up <= 0 || inj.moves != 0 || countKey(inj, 0x11, true) == 0 {
		t.Fatalf("spread did not zoom exclusively: %+v", inj)
	}
	inj.wheels = nil
	for n := 0; n < 16; n++ {
		c.engine.Process(pairStates(332-float32(n)*12), now)
		now = now.Add(33 * time.Millisecond)
	}
	down := 0
	for _, w := range inj.wheels {
		down += w.delta
	}
	if down >= 0 {
		t.Fatalf("closing fingers did not zoom out: %v", inj.wheels)
	}
	c.Abort()
	assertReleased(t, inj)
}
func TestPairTranslationDropoutAndPauseDoNotZoom(t *testing.T) {
	inj := &recordingInput{x: 500, y: 500}
	c, _ := New(inj, .5)
	now := time.Unix(100, 0)
	for n := 0; n < 15; n++ {
		s := pairStates(200)
		for j := range s {
			s[j].Landmarks[vision.IndexTip].X += float32(n * 5)
		}
		c.engine.Process(s, now)
		now = now.Add(33 * time.Millisecond)
	}
	if len(inj.wheels) != 0 || inj.moves != 0 {
		t.Fatal("parallel movement changed zoom or cursor")
	}
	missing := pairStates(200)
	missing[1].Present = false
	c.engine.Process(missing, now)
	now = now.Add(66 * time.Millisecond)
	c.engine.Process(pairStates(400), now)
	if len(inj.wheels) != 0 {
		t.Fatal("reappearing hand caused a zoom jump")
	}
	c.SetPaused(true)
	now = now.Add(33 * time.Millisecond)
	c.engine.Process(pairStates(500), now)
	if len(inj.wheels) != 0 {
		t.Fatal("paused pair zoom sent input")
	}
	assertReleased(t, inj)
}
func TestIndependentPointerAndSwitcherPreferences(t *testing.T) {
	moves := func(side float32, p Preferences) int {
		inj := &recordingInput{x: 500, y: 500}
		c, err := NewWithPreferences(inj, p)
		if err != nil {
			t.Fatal(err)
		}
		h := testHand("coccc", 0, 0)
		h.LeftScore = side
		now := feed(c, h, time.Unix(100, 0), 12)
		for n := 0; n < 10; n++ {
			shift(&h, 3, 0)
			now = feed(c, h, now, 1)
		}
		return inj.x - 500
	}
	base := DefaultPreferences()
	fast := base
	fast.MoveRight = 1.5
	if moves(.99, base) != moves(.99, fast) || moves(.01, fast) <= moves(.01, base) {
		t.Fatal("right sensitivity changed left hand or had no effect")
	}
	steps := func(speed float64) int {
		p := base
		p.Switcher = speed
		inj := &recordingInput{}
		c, _ := NewWithPreferences(inj, p)
		h := rightHand("ooooo")
		now := feed(c, h, time.Unix(100, 0), 20)
		for n := 0; n < 12; n++ {
			shift(&h, 3, 0)
			now = feed(c, h, now, 1)
		}
		c.Abort()
		return countKey(inj, 0x09, true)
	}
	if steps(3) <= steps(1) {
		t.Fatal("window switch sensitivity had no effect")
	}
}

func TestLivePreferencesReleaseDragAndKeepPause(t *testing.T) {
	inj := &recordingInput{}
	c, _ := New(inj, .5)
	h := testHand("coooc", 0, 0)
	h.LeftScore = .01
	now := feed(c, h, time.Unix(100, 0), 20)
	p := c.Preferences()
	p.Drag = 1.2
	if err := c.SetPreferences(p); err != nil {
		t.Fatal(err)
	}
	if len(inj.buttons) != 2 || inj.buttons[1] != "left up" {
		t.Fatal("changing settings left drag held")
	}
	c.SetPaused(true)
	p.Scroll = 1.8
	if err := c.SetPreferences(p); err != nil {
		t.Fatal(err)
	}
	feed(c, h, now, 20)
	if !c.Paused() || len(inj.buttons) != 2 {
		t.Fatal("changing preferences resumed input")
	}
}
func TestThumbBackFacingDoesNotSendTab(t *testing.T) {
	inj := &recordingInput{}
	c, _ := New(inj, .5)
	h := testHand("occcc", -48, 0)
	for n := range h.World {
		h.World[n].X = -h.World[n].X
		h.Landmarks[n].X = 640 - h.Landmarks[n].X
	}
	feed(c, h, time.Unix(100, 0), 30)
	if len(inj.keys) != 0 || len(inj.buttons) != 0 {
		t.Fatalf("removed back-facing thumb sent input: %v", inj.keys)
	}
}
func TestWindowSwitchHasPriorityOverTwoIndexZoom(t *testing.T) {
	c, inj, owner, now := openedSwitcher(t)
	pair := pairStates(300)
	pair[1].ID = owner.ID
	for n := 0; n < 10; n++ {
		pair[0].Landmarks[vision.IndexTip].X -= 10
		pair[1].Landmarks[vision.IndexTip].X += 10
		c.engine.Process(pair, now.Add(time.Duration(n+1)*33*time.Millisecond))
	}
	if len(inj.wheels) != 0 || inj.moves != 0 || countKey(inj, 0x12, false) != 0 {
		t.Fatal("pair zoom interrupted switcher")
	}
	c.Abort()
	assertReleased(t, inj)
}

func TestKnownHandSideChangeReleasesOldMode(t *testing.T) {
	inj := &recordingInput{x: 500, y: 500}
	c, _ := New(inj, .5)
	now := time.Unix(100, 0)
	st := gesture.HandState{ID: 1, Present: true, Side: gesture.SideLeft, Pose: "point", RawPose: "point", Features: gesture.Features{PalmSize: 100}}
	c.engine.Process([]gesture.HandState{st}, now)
	st.Landmarks[vision.IndexTip].X = 10
	c.engine.Process([]gesture.HandState{st}, now.Add(33*time.Millisecond))
	st.Side = gesture.SideRight
	st.Landmarks[vision.IndexTip].X = 20
	c.engine.Process([]gesture.HandState{st}, now.Add(66*time.Millisecond))
	before := inj.x
	st.Landmarks[vision.IndexTip].X = 30
	c.engine.Process([]gesture.HandState{st}, now.Add(99*time.Millisecond))
	if inj.x <= before {
		t.Fatal("known side change froze pointer permanently")
	}
	c.Abort()
	inj.buttons = nil
	st.Pose, st.RawPose = "three", "three"
	st.Side = gesture.SideRight
	c.engine.Process([]gesture.HandState{st}, now.Add(time.Second))
	c.engine.Process([]gesture.HandState{st}, now.Add(1200*time.Millisecond))
	st.Side = gesture.SideLeft
	c.engine.Process([]gesture.HandState{st}, now.Add(1233*time.Millisecond))
	if len(inj.buttons) != 2 || inj.buttons[0] != "left down" || inj.buttons[1] != "left up" {
		t.Fatalf("side change left drag held or fired immediately: %v", inj.buttons)
	}
}
