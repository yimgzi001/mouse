package engine

import (
	"math"
	"testing"
	"time"

	"gesturectl/internal/gesture"
	"gesturectl/internal/vision"
)

type inputEvent struct {
	kind  string
	code  uint16
	down  bool
	value int
}
type recordingInjector struct {
	x, y, moves int
	events      []inputEvent
}

func (r *recordingInjector) CursorPos() (int, int) { return r.x, r.y }
func (r *recordingInjector) MoveCursor(x, y int)   { r.x, r.y = x, y; r.moves++ }
func (*recordingInjector) Desktop() (int, int, int, int, int) {
	return -10000, -10000, 20000, 20000, 1000
}
func (r *recordingInjector) MouseButton(b string, d bool) {
	r.events = append(r.events, inputEvent{kind: b, down: d})
}
func (r *recordingInjector) Wheel(v int, _ bool) {
	r.events = append(r.events, inputEvent{kind: "wheel", value: v})
}
func (r *recordingInjector) Key(k uint16, d bool) {
	r.events = append(r.events, inputEvent{kind: "key", code: k, down: d})
}
func (*recordingInjector) Text(string)       {}
func (*recordingInjector) LockScreen()       {}
func (*recordingInjector) Open(string) error { return nil }
func (r *recordingInjector) count(kind string, code uint16) int {
	n := 0
	for _, v := range r.events {
		if v.kind == kind && v.code == code && v.down {
			n++
		}
	}
	return n
}

func hasPose(id string) bool {
	for _, p := range gesture.BuiltinPoses() {
		if p.ID == id {
			return true
		}
	}
	return false
}
func testEngine(t *testing.T, s Settings, bs ...Binding) (*Engine, *recordingInjector) {
	t.Helper()
	r := &recordingInjector{}
	e := New(r)
	if err := e.Configure(s, bs, hasPose); err != nil {
		t.Fatal(err)
	}
	return e, r
}
func bind(id, hand, pose, trigger, action string) Binding {
	return Binding{ID: id, Name: id, Enabled: true, Hand: hand, Pose: pose, Trigger: trigger, Action: Action{Type: action}}
}
func state(id int, side gesture.Side, pose string) gesture.HandState {
	return gesture.HandState{ID: id, Present: true, Side: side, Pose: pose, RawPose: pose, Features: gesture.Features{PalmSize: 100}}
}
func twoPoints(distance float32) []gesture.HandState {
	a, b := state(1, gesture.SideLeft, "point"), state(2, gesture.SideRight, "point")
	a.Landmarks[vision.IndexTip] = vision.Point{X: 300 - distance/2, Y: 200}
	b.Landmarks[vision.IndexTip] = vision.Point{X: 300 + distance/2, Y: 200}
	return []gesture.HandState{a, b}
}

func TestNormalizeRejectsNonfiniteActionGain(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		b := bind("move", "left", "point", "hold", "pointer_move")
		b.Action.Params = map[string]any{"sensitivity": v}
		if b.Normalize(hasPose) == nil {
			t.Fatalf("accepted nonfinite gain %v", v)
		}
	}
}

func TestLegacyPairFlagWithoutBindingDoesNotCapturePointer(t *testing.T) {
	s := DefaultSettings()
	s.PairZoom = PairZoomSettings{Enabled: true, Sensitivity: 1}
	e, r := testEngine(t, s, bind("move", "left", "point", "hold", "pointer_move"))
	now := time.Unix(100, 0)
	for i := 0; i < 8; i++ {
		st := twoPoints(200)
		st[0].Landmarks[vision.IndexTip].X += float32(i * 8)
		e.Process(st, now.Add(time.Duration(i)*33*time.Millisecond))
	}
	if r.moves == 0 {
		t.Fatal("legacy pair flag captured enabled single-hand pointer")
	}
	if r.count("key", vkCtrl) != 0 {
		t.Fatal("unbound pair zoom sent Ctrl")
	}
}

func TestSwipeDispatchesConfiguredKeyBeforePoseVote(t *testing.T) {
	b := bind("swipe", "right", "four", "swipe_left", "key_combo")
	b.Action.Params = map[string]any{"keys": "enter"}
	e, r := testEngine(t, DefaultSettings(), b)
	st := state(1, gesture.SideRight, "point")
	st.RawPose = "four"
	st.Swipe = "left"
	e.Process([]gesture.HandState{st}, time.Unix(100, 0))
	if r.count("key", 0x0d) != 1 {
		t.Fatalf("configured swipe did not send Enter once: %+v", r.events)
	}
}

func TestCompositeBindingCompatibility(t *testing.T) {
	for _, tc := range []struct {
		hand, pose, action, trigger string
		valid                       bool
	}{{"both", "two_index", "key_combo", "enter", true}, {"both", "two_index", "pair_zoom", "hold", true}, {"both", "two_index", "pointer_move", "hold", false}, {"both", "two_index", "drag", "hold", false}, {"both", "point", "key_combo", "enter", false}, {"left", "two_index", "key_combo", "enter", false}, {"left", "point", "pair_zoom", "hold", false}, {"both", "two_index", "key_combo", "swipe_left", false}} {
		b := bind("test", tc.hand, tc.pose, tc.trigger, tc.action)
		if err := b.Normalize(hasPose); (err == nil) != tc.valid {
			t.Errorf("%+v: %v", tc, err)
		}
	}
}

func TestCompositeEnterOwnsBothUntilStableRelease(t *testing.T) {
	b := bind("pair-enter", "both", "two_index", "enter", "key_combo")
	b.HoldMs = 100
	b.Action.Params = map[string]any{"keys": "enter"}
	move := bind("move", "left", "point", "hold", "pointer_move")
	e, r := testEngine(t, DefaultSettings(), b, move)
	now := time.Unix(100, 0)
	for n := 0; n < 10; n++ {
		st := twoPoints(float32(200 + n*10))
		e.Process(st, now.Add(time.Duration(n)*33*time.Millisecond))
	}
	if r.count("key", 0x0d) != 1 || r.moves != 0 {
		t.Fatalf("pair did not fire exclusively once: %+v moves=%d", r.events, r.moves)
	}
	if e.Running()[1] != "pair-enter" || e.Running()[2] != "pair-enter" {
		t.Fatalf("pair binding not represented in running: %v", e.Running())
	}
	unknown := twoPoints(500)
	unknown[1].Side = gesture.SideUnknown
	e.Process(unknown, now.Add(350*time.Millisecond))
	unknown[1].Present = false
	e.Process(unknown, now.Add(380*time.Millisecond))
	e.Process(twoPoints(600), now.Add(420*time.Millisecond))
	e.Process(twoPoints(650), now.Add(600*time.Millisecond))
	if r.count("key", 0x0d) != 1 || r.moves != 0 {
		t.Fatal("pair rearmed or moved after short uncertainty")
	}
	released := twoPoints(650)
	released[1].Pose = "two"
	released[1].RawPose = "two"
	e.Process(released, now.Add(650*time.Millisecond))
	e.Process(twoPoints(200), now.Add(700*time.Millisecond))
	e.Process(twoPoints(200), now.Add(850*time.Millisecond))
	if r.count("key", 0x0d) != 2 {
		t.Fatalf("release did not rearm pair: %+v", r.events)
	}
	for _, ev := range e.Events(0) {
		if ev.Hand == "both" && (ev.Binding != "pair-enter" || ev.Action != "key_combo") {
			t.Fatalf("pair event uses fixed action metadata: %+v", ev)
		}
	}
}

func TestCompositeDiscreteTriggers(t *testing.T) {
	for _, tc := range []struct {
		trigger string
		want    int
	}{{"enter", 1}, {"long_press", 1}, {"repeat", 3}, {"exit", 1}} {
		t.Run(tc.trigger, func(t *testing.T) {
			b := bind("pair", "both", "two_index", tc.trigger, "mouse_click")
			b.HoldMs = 100
			b.IntervalMs = 100
			e, r := testEngine(t, DefaultSettings(), b)
			now := time.Unix(100, 0)
			for n := 0; n < 4; n++ {
				e.Process(twoPoints(200), now.Add(time.Duration(n)*100*time.Millisecond))
			}
			end := twoPoints(200)
			end[0].Pose = "two"
			end[0].RawPose = "two"
			e.Process(end, now.Add(400*time.Millisecond))
			if r.count("left", 0) != tc.want {
				t.Fatalf("%s sent %d clicks, want %d: %+v", tc.trigger, r.count("left", 0), tc.want, r.events)
			}
		})
	}
}

func TestPairZoomBindingUsesDistanceAndConsumesReleaseClick(t *testing.T) {
	s := DefaultSettings()
	s.PairZoom.Sensitivity = 1
	b := bind("spread", "both", "two_index", "hold", "pair_zoom")
	b.HoldMs = 100
	click := bind("click", "left", "fist", "enter", "mouse_click")
	e, r := testEngine(t, s, b, click)
	now := time.Unix(100, 0)
	for n := 0; n < 20; n++ {
		e.Process(twoPoints(float32(200+n*12)), now.Add(time.Duration(n)*33*time.Millisecond))
	}
	sum := 0
	for _, ev := range r.events {
		if ev.kind == "wheel" {
			sum += ev.value
		}
	}
	if sum <= 0 || r.moves != 0 {
		t.Fatalf("distance did not zoom exclusively: %+v", r.events)
	}
	end := twoPoints(450)
	end[0].Pose = "point"
	end[0].RawPose = "fist"
	e.Process(end, now.Add(700*time.Millisecond))
	end[0].Pose = "fist"
	e.Process(end, now.Add(850*time.Millisecond))
	if r.count("left", 0) != 0 {
		t.Fatal("releasing pair clicked")
	}
}

func TestReleaseProtectionUsesConfiguredPoseAndSurvivesUncertainty(t *testing.T) {
	a := bind("a", "left", "middle", "enter", "key_combo")
	a.ReleaseTo = []string{"two"}
	b := bind("b", "left", "two", "enter", "mouse_click")
	c := bind("c", "left", "fist", "enter", "mouse_click")
	e, r := testEngine(t, DefaultSettings(), a, b, c)
	now := time.Unix(100, 0)
	send := func(st gesture.HandState) {
		e.Process([]gesture.HandState{st}, now)
		now = now.Add(50 * time.Millisecond)
	}
	send(state(1, gesture.SideLeft, "middle"))
	send(state(1, gesture.SideLeft, "two"))
	unknown := state(1, gesture.SideUnknown, "")
	send(unknown)
	missing := state(1, gesture.SideLeft, "two")
	missing.Present = false
	send(missing)
	send(state(1, gesture.SideLeft, "two"))
	noise := state(1, gesture.SideLeft, "two")
	noise.RawPose = "fist"
	send(noise)
	send(state(1, gesture.SideLeft, "two"))
	if r.count("left", 0) != 0 {
		t.Fatal("declared return pose rearmed after uncertainty or raw noise")
	}
	send(state(1, gesture.SideLeft, "fist"))
	if r.count("left", 0) != 1 {
		t.Fatal("unlisted pose was blocked")
	}
	send(state(1, gesture.SideLeft, "two"))
	if r.count("left", 0) != 2 {
		t.Fatal("stable unlisted pose did not end release protection")
	}
	// The unlisted pose may also come before any declared return pose.
	send(state(1, gesture.SideLeft, "middle"))
	send(state(1, gesture.SideLeft, "fist"))
	send(state(1, gesture.SideLeft, "two"))
	if r.count("left", 0) != 4 {
		t.Fatal("A→unlisted C→B kept the old release rule")
	}
}

func TestReleaseProtectionIsOptInAndCompiledCopy(t *testing.T) {
	a := bind("a", "left", "ok", "enter", "key_combo")
	a.ReleaseTo = []string{"two"}
	a.Action.Params = map[string]any{"keys": "enter"}
	b := bind("b", "left", "two", "enter", "mouse_click")
	e, r := testEngine(t, DefaultSettings(), a, b)
	a.ReleaseTo[0] = "fist"
	a.Action.Params["keys"] = "tab"
	now := time.Unix(100, 0)
	e.Process([]gesture.HandState{state(1, gesture.SideLeft, "ok")}, now)
	e.Process([]gesture.HandState{state(1, gesture.SideLeft, "two")}, now.Add(time.Second))
	if r.count("left", 0) != 0 || r.count("key", 0x0d) != 1 {
		t.Fatal("caller mutated compiled bindings")
	}
	a.ReleaseTo = nil
	e, r = testEngine(t, DefaultSettings(), a, b)
	e.Process([]gesture.HandState{state(1, gesture.SideLeft, "ok")}, now)
	e.Process([]gesture.HandState{state(1, gesture.SideLeft, "two")}, now.Add(time.Second))
	if r.count("left", 0) != 1 {
		t.Fatal("undeclared return pose suppressed")
	}
}

func TestStickyConfirmationUsesActionRatherThanPoseName(t *testing.T) {
	for _, action := range []string{"confirm_action", "cancel_action", "stop_hand"} {
		t.Run(action, func(t *testing.T) {
			start := bind("select", "right", "five", "hold", "window_switcher")
			stop := bind("finish", "right", "two", "enter", action)
			stop.HoldMs = 50
			e, r := testEngine(t, DefaultSettings(), start, stop)
			now := time.Unix(100, 0)
			send := func(st gesture.HandState, dt time.Duration) { e.Process([]gesture.HandState{st}, now.Add(dt)) }
			send(state(1, gesture.SideRight, "five"), 0)
			s := state(1, gesture.SideRight, "five")
			s.RawPose = "fist"
			send(s, 100*time.Millisecond)
			if e.Running()[1] == "" {
				t.Fatal("raw fist ended selector without action")
			}
			s.Pose, s.RawPose = "two", "two"
			send(s, 150*time.Millisecond)
			send(s, 220*time.Millisecond)
			if e.Running()[1] != "" {
				t.Fatal("custom finish action did not stop selector")
			}
			esc := r.count("key", vkEsc)
			if action == "confirm_action" && esc != 0 || action != "confirm_action" && esc != 1 {
				t.Fatalf("wrong finish semantics: %+v", r.events)
			}
		})
	}
}

func TestPointerBaseGainMultipliesLocalOnceForActualHand(t *testing.T) {
	for _, tc := range []struct {
		name, action string
		side         gesture.Side
		base, local  float64
		want         int
	}{{"left", "pointer_move", gesture.SideLeft, .5, 2, 100}, {"right", "pointer_move", gesture.SideRight, .25, 2, 50}, {"drag", "drag", gesture.SideLeft, .5, 2, 100}, {"legacy", "pointer_move", gesture.SideLeft, 0, 1, 100}} {
		t.Run(tc.name, func(t *testing.T) {
			s := DefaultSettings()
			s.Pointer.LeftSensitivity = tc.base
			s.Pointer.RightSensitivity = tc.base
			s.Pointer.DragSensitivity = tc.base
			s.Pointer.GainLow = 1
			s.Pointer.GainHigh = 1
			s.Pointer.MinCutoff = 1e9
			s.Pointer.Beta = 0
			s.Pointer.DeadZone = 0
			b := bind("move", "any", "point", "hold", tc.action)
			b.Action.Params = map[string]any{"sensitivity": tc.local}
			e, r := testEngine(t, s, b)
			now := time.Unix(100, 0)
			st := state(1, tc.side, "point")
			e.Process([]gesture.HandState{st}, now)
			st.Landmarks[vision.IndexTip].X = 10
			e.Process([]gesture.HandState{st}, now.Add(100*time.Millisecond))
			if d := r.x - tc.want; d < -1 || d > 1 {
				t.Fatalf("cursor x=%d want %d", r.x, tc.want)
			}
			st.Side = gesture.SideUnknown
			st.Landmarks[vision.IndexTip].X = 100
			e.Process([]gesture.HandState{st}, now.Add(200*time.Millisecond))
			if r.moves != 1 {
				t.Fatal("unknown hand produced output")
			}
		})
	}
}

func TestNewHandOnlyMovesPointerUntilObserved(t *testing.T) {
	s := DefaultSettings()
	s.NewHandMs = 200
	s.Pointer.GainLow, s.Pointer.GainHigh = 1, 1
	s.Pointer.MinCutoff, s.Pointer.Beta, s.Pointer.DeadZone = 1e9, 0, 0
	e, r := testEngine(t, s, bind("move", "right", "point", "hold", "pointer_move"), bind("click", "left", "fist", "enter", "mouse_click"))
	now := time.Unix(100, 0)
	pointer := state(1, gesture.SideRight, "point")
	e.Process([]gesture.HandState{pointer}, now)
	pointer.Landmarks[vision.IndexTip].X = 10
	e.Process([]gesture.HandState{pointer}, now.Add(33*time.Millisecond))
	if r.moves == 0 {
		t.Fatal("a hand that had just appeared could not move the pointer")
	}
	fist := state(2, gesture.SideLeft, "fist")
	born := now.Add(66 * time.Millisecond)
	for at := time.Duration(0); at < 200*time.Millisecond; at += 33 * time.Millisecond {
		e.Process([]gesture.HandState{pointer, fist}, born.Add(at))
	}
	if r.count("left", 0) != 0 {
		t.Fatal("a hand clicked before it had been observed")
	}
	e.Process([]gesture.HandState{pointer, fist}, born.Add(200*time.Millisecond))
	if r.count("left", 0) != 1 {
		t.Fatal("an observed hand could not click")
	}
}

func TestCompositeWindowCycleDoesNotRearmWhileHeld(t *testing.T) {
	b := bind("cycle", "both", "two_index", "enter", "window_cycle")
	e, r := testEngine(t, DefaultSettings(), b)
	now := time.Unix(100, 0)
	for n := 0; n < 5; n++ {
		e.Process(twoPoints(200), now.Add(time.Duration(n)*100*time.Millisecond))
	}
	if r.count("key", vkTab) != 1 || len(e.Running()) != 2 {
		t.Fatalf("held composite cycle rearmed or released capture: %+v running=%v", r.events, e.Running())
	}
}

func TestSwipeStopActionTargetsTrackedHand(t *testing.T) {
	drag := bind("drag", "right", "two", "hold", "drag")
	stop := bind("stop", "right", "four", "swipe_left", "stop_hand")
	e, r := testEngine(t, DefaultSettings(), drag, stop)
	now := time.Unix(100, 0)
	e.Process([]gesture.HandState{state(1, gesture.SideRight, "two")}, now)
	s := state(1, gesture.SideRight, "two")
	s.RawPose = "four"
	s.Swipe = "left"
	e.Process([]gesture.HandState{s}, now.Add(33*time.Millisecond))
	if len(e.Running()) != 0 || len(r.events) != 2 || r.events[1].kind != "left" || r.events[1].down {
		t.Fatalf("swipe stop did not release tracked drag: %+v running=%v", r.events, e.Running())
	}
}

func TestUnknownAndShortDropoutCannotRearmSingleClick(t *testing.T) {
	e, r := testEngine(t, DefaultSettings(), bind("click", "left", "two", "enter", "mouse_click"))
	now := time.Unix(100, 0)
	send := func(st gesture.HandState) {
		e.Process([]gesture.HandState{st}, now)
		now = now.Add(33 * time.Millisecond)
	}
	send(state(1, gesture.SideLeft, "two"))
	send(state(1, gesture.SideUnknown, ""))
	send(state(1, gesture.SideLeft, "two"))
	missing := state(1, gesture.SideLeft, "two")
	missing.Present = false
	send(missing)
	send(state(1, gesture.SideLeft, "two"))
	if r.count("left", 0) != 1 {
		t.Fatal("uncertain observation rearmed click")
	}
}

func TestConsumedReturnPoseDoesNotFireExitBinding(t *testing.T) {
	a := bind("a", "left", "middle", "enter", "key_combo")
	a.ReleaseTo = []string{"two"}
	b := bind("b", "left", "two", "exit", "mouse_click")
	e, r := testEngine(t, DefaultSettings(), a, b)
	now := time.Unix(100, 0)
	for n, pose := range []string{"middle", "two", "point"} {
		e.Process([]gesture.HandState{state(1, gesture.SideLeft, pose)}, now.Add(time.Duration(n)*100*time.Millisecond))
	}
	if r.count("left", 0) != 0 {
		t.Fatal("consumed return pose fired on exit")
	}
}

func TestCompositeLostEndsCaptureAndRearmsOnce(t *testing.T) {
	b := bind("pair", "both", "two_index", "enter", "key_combo")
	b.Action.Params = map[string]any{"keys": "enter"}
	b.HoldMs = 100
	e, r := testEngine(t, DefaultSettings(), b)
	now := time.Unix(100, 0)
	e.Process(twoPoints(200), now)
	unknown := twoPoints(200)
	unknown[1].Side = gesture.SideUnknown
	e.Process(unknown, now.Add(90*time.Millisecond))
	e.Process(twoPoints(200), now.Add(120*time.Millisecond))
	if r.count("key", 0x0d) != 0 {
		t.Fatal("interrupted confirmation fired")
	}
	e.Process(twoPoints(200), now.Add(230*time.Millisecond))
	lost := twoPoints(200)
	lost[1].Present = false
	lost[1].Lost = true
	e.Process(lost, now.Add(250*time.Millisecond))
	if len(e.Running()) != 0 {
		t.Fatal("lost hand retained composite capture")
	}
	e.Process(twoPoints(200), now.Add(300*time.Millisecond))
	e.Process(twoPoints(200), now.Add(410*time.Millisecond))
	if r.count("key", 0x0d) != 2 {
		t.Fatalf("lost composite did not rearm: %+v", r.events)
	}
}

func TestStickyConfirmationCanFireOnConfiguredPoseExit(t *testing.T) {
	start := bind("selector", "right", "five", "hold", "window_switcher")
	finish := bind("finish", "right", "two", "exit", "confirm_action")
	e, r := testEngine(t, DefaultSettings(), start, finish)
	now := time.Unix(100, 0)
	for n, pose := range []string{"five", "two", "point"} {
		e.Process([]gesture.HandState{state(1, gesture.SideRight, pose)}, now.Add(time.Duration(n)*100*time.Millisecond))
	}
	if len(e.Running()) != 0 || r.count("key", vkEsc) != 0 {
		t.Fatalf("configured exit did not confirm selector: running=%v events=%+v", e.Running(), r.events)
	}
}

func TestActionMultipliersComposeForScrollZoomAndSwitcher(t *testing.T) {
	for _, action := range []string{"scroll", "zoom", "window_switcher"} {
		t.Run(action, func(t *testing.T) {
			measure := func(base, local float64) []inputEvent {
				s := DefaultSettings()
				s.Pointer.MinCutoff = 1e9
				s.Pointer.Beta = 0
				s.Scroll.DeadZone = 0
				s.Scroll.MinSpeed = 0
				s.Scroll.Speed = base
				s.Scroll.ZoomSpeed = base
				s.Scroll.MaxPerFrame = 100
				s.Switcher.StepDistance = .4 / base
				s.Switcher.MinStepMs = 0
				s.Switcher.InitialStep = false
				b := bind("follow", "right", "three", "hold", action)
				b.Action.Params = map[string]any{"sensitivity": local}
				e, r := testEngine(t, s, b)
				now := time.Unix(100, 0)
				for n := 0; n < 18; n++ {
					st := state(1, gesture.SideRight, "three")
					st.Features.PalmX = float32(n * 10)
					st.Features.PalmY = float32(-n * 10)
					e.Process([]gesture.HandState{st}, now.Add(time.Duration(n)*33*time.Millisecond))
				}
				return r.events
			}
			baseline, composed := measure(1, 1), measure(.5, 2)
			if len(baseline) == 0 || len(baseline) != len(composed) {
				t.Fatalf("base/local changed output: baseline=%+v composed=%+v", baseline, composed)
			}
			for i := range baseline {
				if baseline[i] != composed[i] {
					t.Fatalf("base/local not multiplied once: baseline=%+v composed=%+v", baseline, composed)
				}
			}
		})
	}
}

func TestPairZoomLocalAndBaseMultiplyOnce(t *testing.T) {
	measure := func(base, local float64) []inputEvent {
		s := DefaultSettings()
		s.PairZoom.Sensitivity = base
		b := bind("spread", "both", "two_index", "hold", "pair_zoom")
		b.Action.Params = map[string]any{"sensitivity": local}
		e, r := testEngine(t, s, b)
		now := time.Unix(100, 0)
		for n := 0; n < 18; n++ {
			e.Process(twoPoints(float32(200+n*7)), now.Add(time.Duration(n)*33*time.Millisecond))
		}
		return r.events
	}
	a, b := measure(1, 1), measure(.5, 2)
	if len(a) == 0 || len(a) != len(b) {
		t.Fatalf("distance gain mismatch: %+v / %+v", a, b)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("distance gain applied more than once: %+v / %+v", a, b)
		}
	}
}

func TestExitActionReturnProtectionAppliesInReleaseFrame(t *testing.T) {
	a := bind("a", "left", "middle", "exit", "key_combo")
	a.ReleaseTo = []string{"two"}
	b := bind("b", "left", "two", "enter", "mouse_click")
	e, r := testEngine(t, DefaultSettings(), a, b)
	now := time.Unix(100, 0)
	e.Process([]gesture.HandState{state(1, gesture.SideLeft, "middle")}, now)
	e.Process([]gesture.HandState{state(1, gesture.SideLeft, "two")}, now.Add(100*time.Millisecond))
	if r.count("left", 0) != 0 || r.count("key", 0x43) != 1 {
		t.Fatalf("exit return protection did not apply atomically: %+v", r.events)
	}
}

func TestSwipeReturnRuleEndsWhenStableUnlistedPoseContinues(t *testing.T) {
	a := bind("swipe", "left", "four", "swipe_left", "key_combo")
	a.ReleaseTo = []string{"two"}
	b := bind("click", "left", "two", "enter", "mouse_click")
	e, r := testEngine(t, DefaultSettings(), a, b)
	now := time.Unix(100, 0)
	e.Process([]gesture.HandState{state(1, gesture.SideLeft, "point")}, now)
	s := state(1, gesture.SideLeft, "point")
	s.RawPose = "four"
	s.Swipe = "left"
	e.Process([]gesture.HandState{s}, now.Add(33*time.Millisecond))
	e.Process([]gesture.HandState{state(1, gesture.SideLeft, "point")}, now.Add(66*time.Millisecond))
	e.Process([]gesture.HandState{state(1, gesture.SideLeft, "two")}, now.Add(100*time.Millisecond))
	if r.count("left", 0) != 1 {
		t.Fatal("stable unlisted pose failed to end swipe return rule")
	}
}

func TestCompositePauseBindingResumesAfterReleaseWithoutHeldRetrigger(t *testing.T) {
	b := bind("pause", "both", "two_index", "enter", "toggle_pause")
	e, r := testEngine(t, DefaultSettings(), b)
	now := time.Unix(100, 0)
	e.Process(twoPoints(200), now)
	if !e.Paused() {
		t.Fatal("pair did not pause")
	}
	for n := 1; n < 4; n++ {
		e.Process(twoPoints(200), now.Add(time.Duration(n)*100*time.Millisecond))
	}
	if !e.Paused() || len(r.events) != 0 || len(e.Running()) != 0 {
		t.Fatal("held pause pair resumed or retained active input")
	}
	end := twoPoints(200)
	end[1].Pose = "two"
	end[1].RawPose = "two"
	e.Process(end, now.Add(400*time.Millisecond))
	e.Process(twoPoints(200), now.Add(500*time.Millisecond))
	if e.Paused() {
		t.Fatal("released pair pause binding could not resume")
	}
	e.Process(twoPoints(200), now.Add(600*time.Millisecond))
	if e.Paused() {
		t.Fatal("held resume pair paused again")
	}
}

func TestExclusiveSelectionConsumesOtherHandAcrossConfiguredTriggers(t *testing.T) {
	for _, action := range []string{"key_combo", "mouse_click", "mouse_down", "key_hold"} {
		triggers := []string{"long_press", "repeat", "exit", "swipe_left"}
		if action == "key_hold" {
			triggers = []string{"hold"}
		}
		for _, trigger := range triggers {
			t.Run(action+"/"+trigger, func(t *testing.T) {
				start := bind("selector", "right", "five", "hold", "window_switcher")
				finish := bind("finish", "right", "four", "enter", "confirm_action")
				other := bind("other", "left", "two", trigger, action)
				other.HoldMs = 100
				other.IntervalMs = 100
				e, r := testEngine(t, DefaultSettings(), start, finish, other)
				now := time.Unix(100, 0)
				owner := state(2, gesture.SideRight, "five")
				e.Process([]gesture.HandState{owner}, now)
				left := state(1, gesture.SideLeft, "point")
				left.RawPose = "two"
				left.Swipe = "left"
				e.Process([]gesture.HandState{owner, left}, now.Add(33*time.Millisecond))
				left.Pose = "two"
				left.Swipe = ""
				e.Process([]gesture.HandState{owner, left}, now.Add(66*time.Millisecond))
				owner.RawPose = "four"
				e.Process([]gesture.HandState{owner, left}, now.Add(100*time.Millisecond))
				r.events = nil
				for n := 0; n < 12; n++ {
					left.Swipe = "left"
					e.Process([]gesture.HandState{owner, left}, now.Add(200*time.Millisecond+time.Duration(n)*100*time.Millisecond))
				}
				left.Pose, left.RawPose, left.Swipe = "point", "point", ""
				e.Process([]gesture.HandState{owner, left}, now.Add(1500*time.Millisecond))
				if len(r.events) != 0 {
					t.Fatalf("consumed hand replayed after selection: %+v", r.events)
				}
				left.Pose, left.RawPose = "two", "two"
				left.Swipe = "left"
				e.Process([]gesture.HandState{owner, left}, now.Add(1600*time.Millisecond))
				left.Swipe = ""
				e.Process([]gesture.HandState{owner, left}, now.Add(1750*time.Millisecond))
				left.Pose, left.RawPose = "point", "point"
				e.Process([]gesture.HandState{owner, left}, now.Add(1800*time.Millisecond))
				if len(r.events) == 0 {
					t.Fatal("stable release did not rearm configured action")
				}
			})
		}
	}
}

func TestUnknownHandCannotDispatchZeroHoldAnyBinding(t *testing.T) {
	for _, action := range []string{"key_combo", "mouse_click", "toggle_pause"} {
		t.Run(action, func(t *testing.T) {
			b := bind("any", "any", "two", "enter", action)
			e, r := testEngine(t, DefaultSettings(), b)
			now := time.Unix(100, 0)
			st := state(1, gesture.SideUnknown, "two")
			e.Process([]gesture.HandState{st}, now)
			if len(r.events) != 0 || e.Paused() || len(e.Events(0)) != 0 {
				t.Fatal("unknown hand dispatched zero-hold action")
			}
			st.Side = gesture.SideLeft
			e.Process([]gesture.HandState{st}, now.Add(33*time.Millisecond))
			if len(e.Events(0)) == 0 {
				t.Fatal("known hand did not dispatch zero-hold action")
			}
		})
	}
}

func TestExclusiveOwnerCanFinishWithConfiguredSwipe(t *testing.T) {
	for _, action := range []string{"confirm_action", "cancel_action", "stop_hand"} {
		t.Run(action, func(t *testing.T) {
			start := bind("selector", "right", "five", "hold", "window_switcher")
			finish := bind("finish", "right", "two", "swipe_left", action)
			e, r := testEngine(t, DefaultSettings(), start, finish)
			now := time.Unix(100, 0)
			owner := state(1, gesture.SideRight, "five")
			e.Process([]gesture.HandState{owner}, now)
			owner.RawPose = "two"
			owner.Swipe = "left"
			e.Process([]gesture.HandState{owner}, now.Add(33*time.Millisecond))
			if len(e.Running()) != 0 {
				t.Fatal("exclusive owner's finish swipe was ignored")
			}
			esc := r.count("key", vkEsc)
			if action == "confirm_action" && esc != 0 || action != "confirm_action" && esc != 1 {
				t.Fatalf("finish swipe produced wrong selector result: %+v", r.events)
			}
		})
	}
}

func TestCompositeCannotBindSingleHandFinishActions(t *testing.T) {
	for _, action := range []string{"confirm_action", "cancel_action", "stop_hand"} {
		if SupportsInput(action, true) {
			t.Errorf("composite accepted %s", action)
		}
		b := bind("finish", "both", "two_index", "enter", action)
		if b.Normalize(hasPose) == nil {
			t.Errorf("normalized unreachable composite %s", action)
		}
	}
}

func TestAdoptStateConsumesHeldActionsAcrossBindingIDs(t *testing.T) {
	for _, tc := range []struct{ action, trigger string }{{"mouse_click", "enter"}, {"key_combo", "enter"}, {"key_combo", "repeat"}, {"mouse_click", "exit"}, {"drag", "hold"}, {"key_hold", "hold"}, {"window_switcher", "hold"}} {
		t.Run(tc.action+"/"+tc.trigger, func(t *testing.T) {
			b := bind("before", "right", "two", tc.trigger, tc.action)
			b.IntervalMs = 100
			old, r := testEngine(t, DefaultSettings(), b)
			now := time.Unix(100, 0)
			st := state(1, gesture.SideRight, "two")
			old.Process([]gesture.HandState{st}, now)
			b.ID, b.Name = "after", "after"
			next := New(r)
			if err := next.Configure(DefaultSettings(), []Binding{b}, hasPose); err != nil {
				t.Fatal(err)
			}
			next.AdoptState(old, []gesture.HandState{st}, false)
			if len(old.Running()) != 0 {
				t.Fatal("adoption left old controller running")
			}
			r.events = nil
			for n := 1; n < 8; n++ {
				next.Process([]gesture.HandState{st}, now.Add(time.Duration(n)*150*time.Millisecond))
			}
			st.Pose, st.RawPose = "point", "point"
			next.Process([]gesture.HandState{st}, now.Add(1200*time.Millisecond))
			if len(r.events) != 0 || len(next.Running()) != 0 {
				t.Fatalf("new binding replayed consumed input: %+v running=%v", r.events, next.Running())
			}
			st.Pose, st.RawPose = "two", "two"
			next.Process([]gesture.HandState{st}, now.Add(1400*time.Millisecond))
			st.Pose, st.RawPose = "point", "point"
			next.Process([]gesture.HandState{st}, now.Add(1600*time.Millisecond))
			if len(r.events) == 0 {
				t.Fatal("stable release did not rearm adopted binding")
			}
		})
	}
}

func TestAdoptStatePreservesReleaseRuleAndCopiesGuards(t *testing.T) {
	a := bind("tab", "left", "middle", "enter", "key_combo")
	a.ReleaseTo = []string{"fist"}
	b := bind("click", "left", "fist", "enter", "mouse_click")
	old, r := testEngine(t, DefaultSettings(), a, b)
	now := time.Unix(100, 0)
	st := state(1, gesture.SideLeft, "middle")
	old.Process([]gesture.HandState{st}, now)
	next := New(r)
	if err := next.Configure(DefaultSettings(), []Binding{a, b}, hasPose); err != nil {
		t.Fatal(err)
	}
	next.AdoptState(old, []gesture.HandState{st}, false)
	old.hands[1].releaseTo[0] = "two"
	r.events = nil
	st.Side = gesture.SideUnknown
	next.Process([]gesture.HandState{st}, now.Add(50*time.Millisecond))
	st.Side = gesture.SideLeft
	st.Present = false
	next.Process([]gesture.HandState{st}, now.Add(100*time.Millisecond))
	st.Present = true
	st.Pose, st.RawPose = "fist", "fist"
	next.Process([]gesture.HandState{st}, now.Add(150*time.Millisecond))
	if len(r.events) != 0 {
		t.Fatalf("return guard lost or aliased during adoption: %+v", r.events)
	}
	st.Pose, st.RawPose = "point", "point"
	next.Process([]gesture.HandState{st}, now.Add(200*time.Millisecond))
	st.Pose, st.RawPose = "fist", "fist"
	next.Process([]gesture.HandState{st}, now.Add(250*time.Millisecond))
	if r.count("left", 0) != 1 {
		t.Fatal("adopted release guard never rearmed")
	}
}

func TestAdoptStateConsumesRawCandidateNewlyMappedToKey(t *testing.T) {
	old, r := testEngine(t, DefaultSettings())
	st := state(1, gesture.SideLeft, "point")
	st.RawPose = "two"
	now := time.Unix(100, 0)
	old.Process([]gesture.HandState{st}, now)
	b := bind("new", "right", "two", "enter", "key_combo")
	next := New(r)
	if err := next.Configure(DefaultSettings(), []Binding{b}, hasPose); err != nil {
		t.Fatal(err)
	}
	next.AdoptState(old, []gesture.HandState{st}, true)
	st.Side = gesture.SideRight
	st.Pose = "two"
	next.Process([]gesture.HandState{st}, now.Add(33*time.Millisecond))
	next.Process([]gesture.HandState{st}, now.Add(150*time.Millisecond))
	if len(r.events) != 0 {
		t.Fatal("swapped raw candidate fired newly enabled binding")
	}
	st.Pose, st.RawPose = "point", "point"
	next.Process([]gesture.HandState{st}, now.Add(200*time.Millisecond))
	st.Pose, st.RawPose = "two", "two"
	next.Process([]gesture.HandState{st}, now.Add(250*time.Millisecond))
	if r.count("key", 0x43) != 1 {
		t.Fatal("swapped side failed to rearm new binding")
	}
}

func TestAdoptStateConsumesHeldPairAcrossActionChange(t *testing.T) {
	zoom := bind("zoom", "both", "two_index", "hold", "pair_zoom")
	old, r := testEngine(t, DefaultSettings(), zoom)
	now := time.Unix(100, 0)
	st := twoPoints(200)
	old.Process(st, now)
	key := bind("key", "both", "two_index", "enter", "key_combo")
	next := New(r)
	if err := next.Configure(DefaultSettings(), []Binding{key}, hasPose); err != nil {
		t.Fatal(err)
	}
	next.AdoptState(old, st, false)
	r.events = nil
	for n := 1; n < 5; n++ {
		next.Process(twoPoints(float32(200+n*10)), now.Add(time.Duration(n)*100*time.Millisecond))
	}
	if len(r.events) != 0 {
		t.Fatalf("held pair fired remapped key: %+v", r.events)
	}
	end := twoPoints(200)
	end[0].Pose, end[0].RawPose = "two", "two"
	next.Process(end, now.Add(500*time.Millisecond))
	next.Process(twoPoints(200), now.Add(600*time.Millisecond))
	if r.count("key", 0x43) != 1 {
		t.Fatal("pair release did not rearm remapped key")
	}
}

func TestExclusiveStartConsumesAlreadyObservedOtherHandBeforeNextFrame(t *testing.T) {
	for _, tc := range []struct {
		action, trigger string
		wait            int
	}{{"key_combo", "enter", 100}, {"key_hold", "hold", 0}, {"window_switcher", "hold", 100}} {
		t.Run(tc.action, func(t *testing.T) {
			start := bind("selector", "right", "five", "hold", "window_switcher")
			finish := bind("finish", "right", "four", "enter", "confirm_action")
			other := bind("other", "left", "two", tc.trigger, tc.action)
			other.HoldMs = tc.wait
			e, r := testEngine(t, DefaultSettings(), start, finish, other)
			now := time.Unix(100, 0)
			left, owner := state(1, gesture.SideLeft, "two"), state(2, gesture.SideRight, "five")
			e.Process([]gesture.HandState{left, owner}, now)
			r.events = nil
			owner.RawPose = "four"
			e.Process([]gesture.HandState{owner, left}, now.Add(200*time.Millisecond))
			for _, ev := range r.events {
				if ev.down && ev.code != vkAlt {
					t.Fatalf("other hand replayed before first exclusive frame: %+v", r.events)
				}
			}
			if len(e.Running()) != 0 {
				t.Fatal("held other selector reopened after exclusive selection")
			}
		})
	}
}

func TestAdoptStateDisabledPairReleasesPointerWithoutClickingOnReturn(t *testing.T) {
	zoom := bind("zoom", "both", "two_index", "hold", "pair_zoom")
	old, r := testEngine(t, DefaultSettings(), zoom)
	now := time.Unix(100, 0)
	st := twoPoints(200)
	old.Process(st, now)
	move := bind("move", "left", "point", "hold", "pointer_move")
	click := bind("click", "left", "fist", "enter", "mouse_click")
	next := New(r)
	if err := next.Configure(DefaultSettings(), []Binding{move, click}, hasPose); err != nil {
		t.Fatal(err)
	}
	next.AdoptState(old, st, false)
	r.events = nil
	next.Process(st, now.Add(33*time.Millisecond))
	st[0].Landmarks[vision.IndexTip].X += 20
	next.Process(st, now.Add(66*time.Millisecond))
	if r.moves == 0 {
		t.Fatal("disabled pair kept capturing single-hand pointer")
	}
	st[0].Pose, st[0].RawPose = "fist", "fist"
	next.Process(st, now.Add(100*time.Millisecond))
	if r.count("left", 0) != 0 {
		t.Fatal("config-switched pair release clicked")
	}
	st[0].Pose, st[0].RawPose = "point", "point"
	next.Process(st, now.Add(150*time.Millisecond))
	st[0].Pose, st[0].RawPose = "fist", "fist"
	next.Process(st, now.Add(200*time.Millisecond))
	if r.count("left", 0) != 1 {
		t.Fatal("pair return guard never rearmed")
	}
}

func TestPendingReleaseRuleProtectsEverySwipeDirectionAndDiscreteAction(t *testing.T) {
	actions := []string{"mouse_click", "mouse_down", "mouse_up", "scroll_step", "zoom_step", "key_combo", "type_text", "window_cycle", "media_key", "toggle_pause", "stop_hand", "confirm_action", "cancel_action", "lock_screen", "open"}
	for _, direction := range []string{"left", "right", "up", "down"} {
		for _, action := range actions {
			t.Run(direction+"/"+action, func(t *testing.T) {
				a := bind("tab", "left", "middle", "enter", "key_combo")
				a.Action.Params = map[string]any{"keys": "tab"}
				a.ReleaseTo = []string{"five"}
				b := bind("return", "left", "five", "swipe_"+direction, action)
				e, _ := testEngine(t, DefaultSettings(), a, b)
				now := time.Unix(100, 0)
				e.Process([]gesture.HandState{state(1, gesture.SideLeft, "middle")}, now)
				st := state(1, gesture.SideLeft, "five")
				st.Swipe = direction
				e.Process([]gesture.HandState{st}, now.Add(33*time.Millisecond))
				st.Side = gesture.SideUnknown
				e.Process([]gesture.HandState{st}, now.Add(66*time.Millisecond))
				st.Side = gesture.SideLeft
				e.Process([]gesture.HandState{st}, now.Add(100*time.Millisecond))
				for _, ev := range e.Events(0) {
					if ev.Binding == "return" {
						t.Fatalf("declared return swipe dispatched %s: %+v", action, ev)
					}
				}
				st.Pose, st.RawPose, st.Swipe = "point", "point", ""
				e.Process([]gesture.HandState{st}, now.Add(150*time.Millisecond))
				st.Pose, st.RawPose, st.Swipe = "five", "five", direction
				e.Process([]gesture.HandState{st}, now.Add(450*time.Millisecond))
				fired := 0
				for _, ev := range e.Events(0) {
					if ev.Binding == "return" && ev.What == "fire" {
						fired++
					}
				}
				if fired != 1 {
					t.Fatalf("stable release failed to rearm %s swipe, count=%d", action, fired)
				}
			})
		}
	}
}

func TestPendingReturnSwipeCannotUsePoseVotingLagToFire(t *testing.T) {
	a := bind("tab", "left", "middle", "enter", "key_combo")
	a.ReleaseTo = []string{"five"}
	b := bind("return", "left", "five", "swipe_left", "mouse_click")
	e, r := testEngine(t, DefaultSettings(), a, b)
	now := time.Unix(100, 0)
	e.Process([]gesture.HandState{state(1, gesture.SideLeft, "middle")}, now)
	st := state(1, gesture.SideLeft, "middle")
	st.RawPose = "five"
	st.Swipe = "left"
	e.Process([]gesture.HandState{st}, now.Add(33*time.Millisecond))
	if r.count("left", 0) != 0 {
		t.Fatal("raw return swipe bypassed pending guard before stable pose caught up")
	}
	st.RawPose, st.Swipe = "middle", ""
	e.Process([]gesture.HandState{st}, now.Add(66*time.Millisecond))
	st.Pose, st.RawPose, st.Swipe = "five", "five", "left"
	e.Process([]gesture.HandState{st}, now.Add(100*time.Millisecond))
	if r.count("left", 0) != 0 {
		t.Fatal("raw noise cleared pending return guard")
	}
}
