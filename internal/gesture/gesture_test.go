package gesture

import (
	"math"
	"testing"
	"time"

	"gesturectl/internal/vision"
)

// synth describes an artificial hand: a flat palm with fingers that are either
// straight or curled towards the camera. By default it is a left hand held
// upright with the palm facing the camera, as seen in the mirrored view.
type synth struct {
	open  [NumFingers]bool
	cx    float32 // wrist position in pixels
	cy    float32
	rot   float64 // degrees, clockwise in the image; 0 means fingers up
	left  float32 // left-hand score reported by the model
	track int
	pinch int  // finger whose tip touches the thumb tip, 0 for none
	flip  bool // mirror the hand, which turns its back to the camera
}

const synthScale = 1100 // pixels per metre

func (s synth) hand() vision.Hand {
	knuckle := [NumFingers]vec3{{}, {0.025, -0.090, 0}, {0.002, -0.092, 0}, {-0.020, -0.088, 0}, {-0.038, -0.078, 0}}
	bones := [NumFingers][3]float64{{}, {0.040, 0.025, 0.020}, {0.045, 0.028, 0.022}, {0.040, 0.026, 0.021}, {0.032, 0.020, 0.018}}
	var w [vision.NumLandmarks]vec3
	w[vision.ThumbCMC] = vec3{0.020, -0.020, -0.005}
	w[vision.ThumbMCP] = vec3{0.045, -0.045, -0.010}
	if s.open[Thumb] {
		w[vision.ThumbIP] = vec3{0.065, -0.065, -0.010}
		w[vision.ThumbTip] = vec3{0.090, -0.085, -0.010}
	} else {
		w[vision.ThumbIP] = vec3{0.040, -0.060, -0.030}
		w[vision.ThumbTip] = vec3{0.020, -0.065, -0.040}
	}
	for finger := Index; finger <= Pinky; finger++ {
		base := vision.IndexMCP + (finger-Index)*4
		p := knuckle[finger]
		w[base] = p
		angles := [3]float64{0, 0, 0}
		switch {
		case finger == s.pinch:
			angles = [3]float64{50, 100, 140} // half bent, reaching for the thumb
		case !s.open[finger]:
			angles = [3]float64{80, 170, 250}
		}
		for j := 0; j < 3; j++ {
			a := angles[j] * math.Pi / 180
			p = vec3{p.x, p.y - bones[finger][j]*math.Cos(a), p.z - bones[finger][j]*math.Sin(a)}
			w[base+1+j] = p
		}
	}
	if s.pinch > 0 {
		tip := w[vision.IndexMCP+(s.pinch-Index)*4+3]
		tip = vec3{tip.x + 0.006, tip.y + 0.003, tip.z + 0.004}
		w[vision.ThumbTip] = tip
		knuckle := w[vision.ThumbMCP]
		w[vision.ThumbIP] = vec3{(knuckle.x + tip.x) / 2, (knuckle.y + tip.y) / 2, (knuckle.z + tip.z) / 2}
	}
	sin, cos := math.Sincos(s.rot * math.Pi / 180)
	var h vision.Hand
	h.TrackID = s.track
	h.Presence = 1
	h.LeftScore = s.left
	for i, p := range w {
		if s.flip {
			p.x = -p.x
		}
		p = vec3{p.x*cos - p.y*sin, p.x*sin + p.y*cos, p.z}
		h.World[i] = vision.Point{X: float32(p.x), Y: float32(p.y), Z: float32(p.z)}
		h.Landmarks[i] = vision.Point{X: s.cx + float32(p.x*synthScale), Y: s.cy + float32(p.y*synthScale), Z: float32(p.z * synthScale)}
	}
	return h
}

func fingers(spec string) [NumFingers]bool {
	var f [NumFingers]bool
	for i := range f {
		f[i] = spec[i] == 'o'
	}
	return f
}

func TestExtractOpenness(t *testing.T) {
	tuning := DefaultTuning()
	for mask := 0; mask < 32; mask++ {
		var s synth
		for i := range s.open {
			s.open[i] = mask&(1<<i) != 0
		}
		for _, rot := range []float64{0, 90, -135} {
			s.rot = rot
			h := s.hand()
			f := Extract(&h, &tuning)
			for i, open := range s.open {
				if open && f.Openness[i] < 0.75 {
					t.Errorf("mask %05b rot %v: %s openness %.2f, want open", mask, rot, FingerNames[i], f.Openness[i])
				}
				if !open && f.Openness[i] > 0.25 {
					t.Errorf("mask %05b rot %v: %s openness %.2f, want closed", mask, rot, FingerNames[i], f.Openness[i])
				}
			}
		}
	}
}

func TestExtractGeometry(t *testing.T) {
	tuning := DefaultTuning()
	h := synth{open: fingers("ooooo"), cx: 300, cy: 400, left: 1}.hand()
	f := Extract(&h, &tuning)
	if math.Abs(float64(f.PalmSize)-0.092*synthScale) > 1 {
		t.Errorf("palm size %.1f px", f.PalmSize)
	}
	if math.Abs(float64(f.HandAngle)) > 3 || math.Abs(float64(f.IndexAngle)) > 3 {
		t.Errorf("upright hand: hand angle %.1f, index angle %.1f", f.HandAngle, f.IndexAngle)
	}
	if f.PalmNormalZ > -0.9 {
		t.Errorf("palm normal z %.2f, want about -1", f.PalmNormalZ)
	}
	h = synth{open: fingers("ooooo"), rot: 90}.hand()
	if f = Extract(&h, &tuning); math.Abs(float64(f.IndexAngle)-90) > 3 {
		t.Errorf("hand turned right: index angle %.1f", f.IndexAngle)
	}
}

func builtinSet(t *testing.T) *PoseSet {
	t.Helper()
	set, err := NewPoseSet(BuiltinPoses())
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// settle feeds the same hand for several frames and returns its last state.
func settle(r *Recognizer, s synth, start time.Time, frames int) (HandState, time.Time) {
	now := start
	var st HandState
	for i := 0; i < frames; i++ {
		now = now.Add(33 * time.Millisecond)
		out := r.Update([]vision.Hand{s.hand()}, now)
		st = out[0]
	}
	return st, now
}

func TestBuiltinPoses(t *testing.T) {
	set := builtinSet(t)
	cases := []struct {
		name string
		hand synth
		want string
	}{
		{"fist", synth{open: fingers("ccccc")}, "fist"},
		{"index", synth{open: fingers("coccc")}, "point_up"},
		{"index sideways", synth{open: fingers("coccc"), rot: 90}, "point_right"},
		{"index down", synth{open: fingers("coccc"), rot: 180}, "point_down"},
		{"index left", synth{open: fingers("coccc"), rot: -90}, "point_left"},
		{"index diagonal", synth{open: fingers("coccc"), rot: 135}, "point"},
		{"two", synth{open: fingers("coocc")}, "two"},
		{"three", synth{open: fingers("coooc")}, "three"},
		{"four", synth{open: fingers("coooo")}, "four"},
		{"five", synth{open: fingers("ooooo")}, "five"},
		{"five from behind", synth{open: fingers("ooooo"), flip: true}, "five_back"},
		{"thumb up", synth{open: fingers("occcc"), rot: -48}, "thumb_up"},
		{"thumb right", synth{open: fingers("occcc"), rot: 42}, "thumb_right"},
		{"thumb down", synth{open: fingers("occcc"), rot: 132}, "thumb_down"},
		{"thumb left", synth{open: fingers("occcc"), rot: -138}, "thumb_left"},
		{"gun", synth{open: fingers("ooccc"), rot: 135}, "gun"},
		{"thumb index middle", synth{open: fingers("ooocc")}, "three_thumb"},
		{"rock", synth{open: fingers("cocco")}, "rock"},
		{"love", synth{open: fingers("oocco")}, "love"},
		{"six", synth{open: fingers("occco")}, "six"},
		{"pinky", synth{open: fingers("cccco")}, "pinky"},
		{"ok", synth{open: fingers("ccooo"), pinch: Index}, "ok"},
		{"pinch", synth{open: fingers("ccccc"), pinch: Index}, "click_pinch"},
		{"middle pinch", synth{open: fingers("coccc"), pinch: Middle}, "pinch_middle"},
		{"middle and ring", synth{open: fingers("ccooc")}, ""},
	}
	for _, c := range cases {
		r := NewRecognizer(DefaultConfig(), set)
		c.hand.left = 0.97
		c.hand.cx, c.hand.cy = 320, 300
		st, _ := settle(r, c.hand, time.Unix(100, 0), 5)
		if st.Pose != c.want {
			t.Errorf("%s: pose %q, want %q (fingers %v, thumb %.0f, index %.0f)", c.name, st.Pose, c.want, st.Fingers, st.Features.ThumbAngle, st.Features.IndexAngle)
		}
		if st.Side != SideLeft {
			t.Errorf("%s: side %v, want left", c.name, st.Side)
		}
	}
}

func TestActivePosesOnly(t *testing.T) {
	set := builtinSet(t)
	r := NewRecognizer(DefaultConfig(), set)
	r.Configure(DefaultConfig(), set, map[string]bool{"point": true, "two": true})
	st, _ := settle(r, synth{open: fingers("ooccc"), left: 0.9}, time.Unix(100, 0), 5)
	if st.Pose != "point" || st.AnyPose != "gun" {
		t.Errorf("pose %q (any %q), want point (any gun)", st.Pose, st.AnyPose)
	}
}

func TestVotingIgnoresGlitches(t *testing.T) {
	set := builtinSet(t)
	r := NewRecognizer(DefaultConfig(), set)
	r.Configure(DefaultConfig(), set, map[string]bool{"fist": true, "five": true, "point": true})
	now := time.Unix(100, 0)
	step := func(spec string) HandState {
		now = now.Add(33 * time.Millisecond)
		return r.Update([]vision.Hand{synth{open: fingers(spec), left: 0.9}.hand()}, now)[0]
	}
	if st := step("ccccc"); st.Pose != "" {
		t.Fatalf("pose %q after one frame, want none yet", st.Pose)
	}
	step("ccccc")
	st := step("ccccc")
	if st.Pose != "fist" || !st.Changed {
		t.Fatalf("after three frames: pose %q changed %v, want fist", st.Pose, st.Changed)
	}
	step("ccccc")
	step("ccccc")
	if st = step("ooooo"); st.Pose != "fist" || st.RawPose != "five" {
		t.Fatalf("single odd frame: pose %q raw %q, want fist/five", st.Pose, st.RawPose)
	}
	for i := 0; i < 4; i++ {
		if st = step("ccccc"); st.Pose != "fist" || st.Changed {
			t.Fatalf("pose %q changed %v after the odd frame", st.Pose, st.Changed)
		}
	}
	changes := 0
	for i := 0; i < 6; i++ {
		st = step("coccc")
		if st.Changed {
			changes++
			if i != 2 {
				t.Errorf("pose changed on frame %d of the new pose, want frame 2", i)
			}
			if st.PrevPose != "fist" {
				t.Errorf("previous pose %q", st.PrevPose)
			}
		}
	}
	if st.Pose != "point" || changes != 1 {
		t.Fatalf("pose %q after %d changes, want point after 1", st.Pose, changes)
	}
}

func TestDropoutAndWithdrawal(t *testing.T) {
	set := builtinSet(t)
	r := NewRecognizer(DefaultConfig(), set)
	hand := synth{open: fingers("ooooo"), left: 0.9, cx: 300, cy: 300, track: 1}
	st, now := settle(r, hand, time.Unix(100, 0), 5)
	id := st.ID
	if st.Pose != "five" {
		t.Fatalf("pose %q", st.Pose)
	}
	for i := 0; i < 3; i++ {
		now = now.Add(33 * time.Millisecond)
		out := r.Update(nil, now)
		if len(out) != 1 || out[0].Present || out[0].Lost || out[0].Pose != "five" {
			t.Fatalf("during dropout: %+v", out)
		}
	}
	// The tracker finds the hand again under a new track number, a little moved.
	hand.track, hand.cx = 2, 330
	now = now.Add(33 * time.Millisecond)
	out := r.Update([]vision.Hand{hand.hand()}, now)
	if len(out) != 1 || out[0].ID != id || !out[0].Present || out[0].Pose != "five" || out[0].Changed {
		t.Fatalf("after dropout: %+v", out)
	}
	lost := 0
	for i := 0; i < 10; i++ {
		now = now.Add(33 * time.Millisecond)
		for _, st := range r.Update(nil, now) {
			if st.Lost {
				lost++
				if gone := time.Duration(i+1) * 33 * time.Millisecond; gone < 200*time.Millisecond || gone > 240*time.Millisecond {
					t.Errorf("reported lost after %v", gone)
				}
			}
		}
	}
	if lost != 1 {
		t.Fatalf("lost reported %d times, want once", lost)
	}
	// A hand that shows up far away is a new hand.
	hand.track, hand.cx = 3, 330
	out = r.Update([]vision.Hand{hand.hand()}, now.Add(33*time.Millisecond))
	if len(out) != 1 || out[0].ID == id {
		t.Fatalf("new hand: %+v", out)
	}
}

func TestSides(t *testing.T) {
	set := builtinSet(t)
	r := NewRecognizer(DefaultConfig(), set)
	now := time.Unix(100, 0)
	frame := func(a, b float32) []HandState {
		now = now.Add(33 * time.Millisecond)
		return r.Update([]vision.Hand{
			synth{open: fingers("ooooo"), left: a, cx: 150, cy: 300, track: 1}.hand(),
			synth{open: fingers("ooooo"), left: b, cx: 480, cy: 300, track: 2}.hand(),
		}, now)
	}
	out := frame(0.95, 0.04)
	if out[0].Side != SideLeft || out[1].Side != SideRight {
		t.Fatalf("sides %v %v", out[0].Side, out[1].Side)
	}
	// One wrong frame must not flip a hand.
	out = frame(0.95, 0.9)
	if out[0].Side != SideLeft || out[1].Side != SideRight {
		t.Fatalf("after one odd frame: sides %v %v", out[0].Side, out[1].Side)
	}
	// When the model keeps calling both hands left, the less certain one is
	// reported without a side.
	for i := 0; i < 30; i++ {
		out = frame(0.95, 0.85)
	}
	if out[0].Side != SideLeft || out[1].Side != SideUnknown {
		t.Fatalf("both left: sides %v %v", out[0].Side, out[1].Side)
	}
	r = NewRecognizer(DefaultConfig(), set)
	if out = frame(0.55, 0.5); out[0].Side != SideUnknown || out[1].Side != SideUnknown {
		t.Fatalf("unsure hands: sides %v %v", out[0].Side, out[1].Side)
	}
}

func TestSwipe(t *testing.T) {
	set := builtinSet(t)
	r := NewRecognizer(DefaultConfig(), set)
	hand := synth{open: fingers("ooooo"), left: 0.9, cx: 200, cy: 300, track: 1}
	_, now := settle(r, hand, time.Unix(100, 0), 5)
	var seen []string
	move := func(dx, dy float32, frames int) {
		for i := 0; i < frames; i++ {
			hand.cx += dx
			hand.cy += dy
			now = now.Add(33 * time.Millisecond)
			if st := r.Update([]vision.Hand{hand.hand()}, now)[0]; st.Swipe != "" {
				seen = append(seen, st.Swipe)
			}
		}
	}
	move(40, 2, 6)   // fast to the right
	move(-40, -2, 6) // and straight back, within the cooldown
	if len(seen) != 1 || seen[0] != "right" {
		t.Fatalf("swipes %v, want one to the right", seen)
	}
	move(0, 0, 20)
	seen = nil
	move(3, 0, 30) // slow drift over the same distance
	if len(seen) != 0 {
		t.Fatalf("slow movement gave swipes %v", seen)
	}
	move(0, 0, 20)
	move(2, -45, 5)
	if len(seen) != 1 || seen[0] != "up" {
		t.Fatalf("swipes %v, want one upwards", seen)
	}
}

func TestPoseValidation(t *testing.T) {
	bad := []PoseDef{
		{ID: "Bad Id", Fingers: [5]string{Open, Any, Any, Any, Any}},
		{ID: "x", Fingers: [5]string{"bent", Any, Any, Any, Any}},
		{ID: "x"},
		{ID: "x", Touch: "thumb"},
		{ID: "x", Fingers: [5]string{Open}, ThumbDir: "north"},
	}
	for _, p := range bad {
		if _, err := NewPoseSet([]PoseDef{p}); err == nil {
			t.Errorf("%+v accepted", p)
		}
	}
	dup := PoseDef{ID: "x", Fingers: [5]string{Open}}
	if _, err := NewPoseSet([]PoseDef{dup, dup}); err == nil {
		t.Error("duplicate id accepted")
	}
}
