package control

import (
	"math"
	"testing"
	"time"

	"gesturectl/internal/engine"
	"gesturectl/internal/gesture"
	"gesturectl/internal/vision"
)

type wheelEvent struct {
	delta      int
	horizontal bool
}
type keyEvent struct {
	vk   uint16
	down bool
}
type testPoint struct{ x, y, z float64 }

// A complete metric skeleton, independent of the recognition implementation.
// Straight and curled bones are shared across image and world coordinates.
func testHand(spec string, rotation float64, pinch int) vision.Hand {
	knuckles := [5]testPoint{{}, {.025, -.090, 0}, {.002, -.092, 0}, {-.020, -.088, 0}, {-.038, -.078, 0}}
	bones := [5][3]float64{{}, {.040, .025, .020}, {.045, .028, .022}, {.040, .026, .021}, {.032, .020, .018}}
	var points [21]testPoint
	points[1] = testPoint{.020, -.020, -.005}
	points[2] = testPoint{.045, -.045, -.010}
	if spec[0] == 'o' {
		points[3] = testPoint{.065, -.065, -.010}
		points[4] = testPoint{.090, -.085, -.010}
	} else {
		points[3] = testPoint{.040, -.060, -.030}
		points[4] = testPoint{.020, -.065, -.040}
	}
	for finger := 1; finger <= 4; finger++ {
		base := 5 + (finger-1)*4
		p := knuckles[finger]
		points[base] = p
		angles := [3]float64{}
		if finger == pinch {
			angles = [3]float64{50, 100, 140}
		} else if spec[finger] != 'o' {
			angles = [3]float64{80, 170, 250}
		}
		for j := 0; j < 3; j++ {
			a := angles[j] * math.Pi / 180
			p = testPoint{p.x, p.y - bones[finger][j]*math.Cos(a), p.z - bones[finger][j]*math.Sin(a)}
			points[base+j+1] = p
		}
	}
	if pinch > 0 {
		tip := points[5+(pinch-1)*4+3]
		tip = testPoint{tip.x + .006, tip.y + .003, tip.z + .004}
		points[4] = tip
		points[3] = testPoint{(points[2].x + tip.x) / 2, (points[2].y + tip.y) / 2, (points[2].z + tip.z) / 2}
	}
	sn, cs := math.Sincos(rotation * math.Pi / 180)
	h := vision.Hand{TrackID: 1, Presence: 1, LeftScore: .99}
	for n, p := range points {
		p = testPoint{p.x*cs - p.y*sn, p.x*sn + p.y*cs, p.z}
		h.World[n] = vision.Point{X: float32(p.x), Y: float32(p.y), Z: float32(p.z)}
		h.Landmarks[n] = vision.Point{X: 320 + float32(p.x*1100), Y: 300 + float32(p.y*1100), Z: float32(p.z * 1100)}
	}
	return h
}

func shift(h *vision.Hand, dx, dy float32) {
	for n := range h.Landmarks {
		h.Landmarks[n].X += dx
		h.Landmarks[n].Y += dy
	}
}
func countKey(i *recordingInput, vk uint16, down bool) int {
	n := 0
	for _, k := range i.keys {
		if k.vk == vk && k.down == down {
			n++
		}
	}
	return n
}
func assertReleased(t *testing.T, i *recordingInput) {
	t.Helper()
	held := map[uint16]int{}
	for _, k := range i.keys {
		if k.down {
			held[k.vk]++
		} else {
			held[k.vk]--
		}
	}
	for vk, n := range held {
		if n != 0 {
			t.Fatalf("key 0x%x left held: %d (%v)", vk, n, i.keys)
		}
	}
}

func TestThreeFingerScrollFollowsPalmAndStops(t *testing.T) {
	inj := &recordingInput{}
	c, _ := New(inj, .5)
	h := testHand("coooc", 0, 0)
	now := feed(c, h, time.Unix(100, 0), 20)
	if len(inj.wheels) != 0 {
		t.Fatal("stationary hand scrolled")
	}
	for n := 0; n < 20; n++ {
		shift(&h, 0, -5)
		now = feed(c, h, now, 1)
	}
	total := 0
	for _, w := range inj.wheels {
		if w.horizontal {
			t.Fatal("wrong scroll axis")
		}
		total += w.delta
	}
	if total <= 0 {
		t.Fatal("three-finger upward motion produced no upward scroll")
	}
	now = feed(c, h, now, 15)
	before := len(inj.wheels)
	now = feed(c, h, now, 15)
	if len(inj.wheels) != before {
		t.Fatal("stationary hand kept scrolling")
	}
	now = feed(c, testHand("ccccc", 0, 0), now, 15)
	before = len(inj.wheels)
	feed(c, testHand("ccccc", 0, 0), now, 10)
	if len(inj.wheels) != before {
		t.Fatal("fist scrolled")
	}
}

func TestFiveFingerSwitcherSelectsAndWithdrawsToConfirm(t *testing.T) {
	inj := &recordingInput{}
	c, _ := New(inj, .5)
	h := rightHand("ooooo")
	now := feed(c, h, time.Unix(100, 0), 25)
	if countKey(inj, 0x12, true) != 1 || countKey(inj, 0x09, true) != 1 {
		t.Fatalf("five did not open Alt+Tab: %v", inj.keys)
	}
	for n := 0; n < 20; n++ {
		shift(&h, 6, 0)
		now = feed(c, h, now, 1)
	}
	if countKey(inj, 0x09, true) < 2 {
		t.Fatal("horizontal palm motion did not choose another window")
	}
	before := countKey(inj, 0x09, true)
	four := rightHand("coooo")
	shift(&four, 120, 0) // change shape without moving the palm back to the origin
	now = feed(c, four, now, 15)
	if countKey(inj, 0x12, false) != 0 || countKey(inj, 0x09, true) != before {
		t.Fatal("four fingers confirmed or changed window")
	}
	c.Process(nil, now)
	if countKey(inj, 0x12, false) != 0 {
		t.Fatal("brief dropout confirmed window")
	}
	c.Process(nil, now.Add(time.Duration(DefaultRecognition().LostGraceMs+50)*time.Millisecond))
	if countKey(inj, 0x12, false) != 1 || countKey(inj, 0x1B, true) != 0 {
		t.Fatalf("withdrawal failed to confirm: %v", inj.keys)
	}
	assertReleased(t, inj)
}

func backendController(t *testing.T, inj *recordingInput) *Controller {
	t.Helper()
	c, err := New(inj, .5)
	if err != nil {
		t.Fatal(err)
	}
	bindings := []engine.Binding{
		{ID: "drag", Enabled: true, Hand: "any", Pose: "rock", Trigger: "hold", HoldMs: 180, Action: engine.Action{Type: "drag"}},
		{ID: "click", Enabled: true, Hand: "left", Pose: "two", Trigger: "enter", HoldMs: 100, Action: engine.Action{Type: "mouse_click"}},
		{ID: "zoom", Enabled: true, Hand: "any", Pose: "ok", Trigger: "hold", HoldMs: 180, Action: engine.Action{Type: "zoom"}},
		{ID: "volume", Enabled: true, Hand: "any", Pose: "thumb_up", Trigger: "hold", HoldMs: 250, Action: engine.Action{Type: "volume"}},
		{ID: "media", Enabled: true, Hand: "any", Pose: "thumb_down", Trigger: "enter", HoldMs: 600, Action: engine.Action{Type: "media_key"}},
		{ID: "back", Enabled: true, Hand: "any", Pose: "thumb_left", Trigger: "enter", HoldMs: 500, Action: engine.Action{Type: "key_combo", Params: map[string]any{"keys": "alt+left"}}},
		{ID: "forward", Enabled: true, Hand: "any", Pose: "thumb_right", Trigger: "enter", HoldMs: 500, Action: engine.Action{Type: "key_combo", Params: map[string]any{"keys": "alt+right"}}},
		{ID: "stop", Enabled: true, Hand: "any", Pose: "fist", Trigger: "enter", Action: engine.Action{Type: "stop_hand"}},
	}
	if err = c.engine.Configure(engine.DefaultSettings(), bindings, c.poses.Has); err != nil {
		t.Fatal(err)
	}
	c.recognizer.Configure(gesture.DefaultConfig(), c.poses, c.engine.ActivePoses())
	return c
}

func TestBackendRockDragReleasesOnFistAndAbort(t *testing.T) {
	for _, abort := range []bool{false, true} {
		inj := &recordingInput{x: 500, y: 500}
		c := backendController(t, inj)
		h := testHand("cocco", 0, 0)
		now := feed(c, h, time.Unix(100, 0), 20)
		if len(inj.buttons) != 1 || inj.buttons[0] != "left down" {
			t.Fatalf("drag did not press left: %v", inj.buttons)
		}
		for n := 0; n < 10; n++ {
			shift(&h, 3, 0)
			now = feed(c, h, now, 1)
		}
		if inj.x <= 500 {
			t.Fatal("drag did not move cursor")
		}
		if abort {
			c.Abort()
		} else {
			feed(c, testHand("ccccc", 0, 0), now, 15)
		}
		if len(inj.buttons) != 2 || inj.buttons[1] != "left up" {
			t.Fatalf("drag left mouse held: %v", inj.buttons)
		}
	}
}

func TestOtherHandsClickCannotReleaseActiveDrag(t *testing.T) {
	inj := &recordingInput{x: 500, y: 500}
	c := backendController(t, inj)
	drag := testHand("cocco", 0, 0)
	drag.TrackID = 2
	drag.LeftScore = .01
	now := feed(c, drag, time.Unix(100, 0), 20)
	click := testHand("coocc", 0, 0)
	click.TrackID = 1
	for n := 0; n < 25; n++ {
		c.Process([]vision.Hand{drag, click}, now)
		now = now.Add(33 * time.Millisecond)
	}
	if len(inj.buttons) != 1 || inj.buttons[0] != "left down" {
		t.Fatalf("other hand interrupted drag: %v", inj.buttons)
	}
	c.Abort()
	if len(inj.buttons) != 2 || inj.buttons[1] != "left up" {
		t.Fatalf("drag did not release on abort: %v", inj.buttons)
	}
}

func TestBackendSingleHandZoomAndVolume(t *testing.T) {
	t.Run("zoom", func(t *testing.T) {
		inj := &recordingInput{}
		c := backendController(t, inj)
		h := testHand("ccooo", 0, 1)
		now := feed(c, h, time.Unix(100, 0), 20)
		for n := 0; n < 20; n++ {
			shift(&h, 0, -5)
			now = feed(c, h, now, 1)
		}
		if len(inj.wheels) == 0 || countKey(inj, 0x11, true) == 0 || countKey(inj, 0x12, true) != 0 {
			t.Fatalf("OK did not zoom: wheels=%v keys=%v", inj.wheels, inj.keys)
		}
		c.Abort()
		assertReleased(t, inj)
	})
	t.Run("volume", func(t *testing.T) {
		inj := &recordingInput{}
		c := backendController(t, inj)
		h := testHand("occcc", -48, 0)
		now := feed(c, h, time.Unix(100, 0), 25)
		for n := 0; n < 20; n++ {
			shift(&h, 0, -5)
			now = feed(c, h, now, 1)
		}
		if countKey(inj, 0xAF, true) == 0 || len(inj.wheels) != 0 {
			t.Fatalf("thumb up did not raise volume: %v", inj.keys)
		}
		for n := 0; n < 30; n++ {
			shift(&h, 0, 5)
			now = feed(c, h, now, 1)
		}
		if countKey(inj, 0xAE, true) == 0 {
			t.Fatal("downward palm motion did not lower volume")
		}
		c.Abort()
		assertReleased(t, inj)
	})
}

func TestBackendShortcutsAndMediaFireOncePerGesture(t *testing.T) {
	for _, tc := range []struct {
		name string
		rot  float64
		key  uint16
	}{{"back", -138, 0x25}, {"forward", 42, 0x27}, {"media", 132, 0xB3}} {
		t.Run(tc.name, func(t *testing.T) {
			inj := &recordingInput{}
			c := backendController(t, inj)
			h := testHand("occcc", tc.rot, 0)
			now := feed(c, h, time.Unix(100, 0), 60)
			if countKey(inj, tc.key, true) != 1 {
				t.Fatalf("held gesture did not fire exactly once: %v", inj.keys)
			}
			now = feed(c, testHand("ccccc", 0, 0), now, 15)
			feed(c, h, now, 40)
			if countKey(inj, tc.key, true) != 2 {
				t.Fatal("shortcut did not rearm after release")
			}
			c.Abort()
			assertReleased(t, inj)
		})
	}
}

func openedSwitcher(t *testing.T) (*Controller, *recordingInput, gesture.HandState, time.Time) {
	t.Helper()
	inj := &recordingInput{x: 500, y: 500}
	c, _ := New(inj, .5)
	now := time.Unix(100, 0)
	st := gesture.HandState{ID: 2, Present: true, Side: gesture.SideRight, Pose: "five", RawPose: "five", Features: gesture.Features{PalmSize: 100}}
	c.engine.Process([]gesture.HandState{st}, now)
	now = now.Add(300 * time.Millisecond)
	c.engine.Process([]gesture.HandState{st}, now)
	if countKey(inj, 0x12, true) != 1 {
		t.Fatalf("switcher not opened: %v", inj.keys)
	}
	return c, inj, st, now
}

func TestSwitcherAbortPauseCancelAndRawFistConfirms(t *testing.T) {
	for _, how := range []string{"abort", "pause", "raw-fist"} {
		t.Run(how, func(t *testing.T) {
			c, inj, st, now := openedSwitcher(t)
			switch how {
			case "abort":
				c.Abort()
			case "pause":
				c.SetPaused(true)
			case "raw-fist":
				st.RawPose = "fist"
				c.engine.Process([]gesture.HandState{st}, now.Add(33*time.Millisecond))
			}
			wantEsc := 1
			if how == "raw-fist" {
				wantEsc = 0
			}
			if countKey(inj, 0x1B, true) != wantEsc || countKey(inj, 0x12, false) != 1 {
				t.Fatalf("wrong cancel/confirm behavior: %v", inj.keys)
			}
			assertReleased(t, inj)
		})
	}
}

func TestSwitcherTimeoutDoesNotReopenWhileFiveHeld(t *testing.T) {
	c, inj, st, now := openedSwitcher(t)
	c.engine.Process([]gesture.HandState{st}, now.Add(9*time.Second))
	c.engine.Process([]gesture.HandState{st}, now.Add(9100*time.Millisecond))
	if countKey(inj, 0x12, true) != 1 || countKey(inj, 0x12, false) != 1 {
		t.Fatalf("held five reopened timed-out switcher: %v", inj.keys)
	}
	assertReleased(t, inj)
}

func TestSwitcherTimeoutAfterPoseFluctuationStillStaysClosed(t *testing.T) {
	c, inj, st, now := openedSwitcher(t)
	st.Pose, st.RawPose = "", ""
	c.engine.Process([]gesture.HandState{st}, now.Add(100*time.Millisecond))
	st.Pose, st.RawPose = "five", "five"
	c.engine.Process([]gesture.HandState{st}, now.Add(200*time.Millisecond))
	c.engine.Process([]gesture.HandState{st}, now.Add(9*time.Second))
	c.engine.Process([]gesture.HandState{st}, now.Add(9100*time.Millisecond))
	if countKey(inj, 0x12, true) != 1 || countKey(inj, 0x12, false) != 1 {
		t.Fatalf("switcher reopened after timeout: %v", inj.keys)
	}
}

func TestBackendZoomAndVolumeLimitAbnormalDisplacement(t *testing.T) {
	for _, pose := range []string{"ok", "thumb_up"} {
		t.Run(pose, func(t *testing.T) {
			inj := &recordingInput{}
			c := backendController(t, inj)
			now := time.Unix(100, 0)
			st := gesture.HandState{ID: 1, Present: true, Side: gesture.SideLeft, Pose: pose, RawPose: pose, Features: gesture.Features{PalmSize: 100}}
			c.engine.Process([]gesture.HandState{st}, now)
			c.engine.Process([]gesture.HandState{st}, now.Add(300*time.Millisecond))
			st.Features.PalmY = -10000
			c.engine.Process([]gesture.HandState{st}, now.Add(333*time.Millisecond))
			if pose == "ok" {
				if len(inj.wheels) != 1 || inj.wheels[0].delta > 240 {
					t.Fatalf("unbounded zoom: %v", inj.wheels)
				}
			} else {
				if n := countKey(inj, 0xAF, true); n < 1 || n > 2 {
					t.Fatalf("unbounded volume steps: %d", n)
				}
			}
			c.Abort()
			assertReleased(t, inj)
		})
	}
}

func TestSwitcherExclusiveAndUnknownSideFreeze(t *testing.T) {
	c, inj, st, now := openedSwitcher(t)
	other := gesture.HandState{ID: 1, Present: true, Side: gesture.SideLeft, Pose: "two", RawPose: "two", Features: gesture.Features{PalmSize: 100}}
	for n := 0; n < 20; n++ {
		now = now.Add(33 * time.Millisecond)
		c.engine.Process([]gesture.HandState{other, st}, now)
	}
	if len(inj.buttons) != 0 || len(inj.wheels) != 0 || inj.moves != 0 {
		t.Fatal("another hand acted during switcher")
	}
	before := countKey(inj, 0x09, true)
	st.Side = gesture.SideUnknown
	st.Features.PalmX = 300
	c.engine.Process([]gesture.HandState{st}, now.Add(33*time.Millisecond))
	if countKey(inj, 0x09, true) != before {
		t.Fatal("uncertain switcher hand selected a window")
	}
	c.Abort()
	assertReleased(t, inj)
}
