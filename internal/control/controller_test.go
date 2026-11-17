package control

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"gesturectl/internal/gesture"
	"gesturectl/internal/vision"
)

type recordingInput struct {
	x, y    int
	moves   int
	buttons []string
	wheels  []wheelEvent
	keys    []keyEvent
}

func (i *recordingInput) CursorPos() (int, int)            { return i.x, i.y }
func (i *recordingInput) MoveCursor(x, y int)              { i.x, i.y = x, y; i.moves++ }
func (*recordingInput) Desktop() (int, int, int, int, int) { return -1920, 0, 3840, 1080, 1080 }
func (i *recordingInput) MouseButton(b string, down bool) {
	if down {
		i.buttons = append(i.buttons, b+" down")
	} else {
		i.buttons = append(i.buttons, b+" up")
	}
}
func (i *recordingInput) Wheel(delta int, horizontal bool) {
	i.wheels = append(i.wheels, wheelEvent{delta, horizontal})
}
func (i *recordingInput) Key(vk uint16, down bool) { i.keys = append(i.keys, keyEvent{vk, down}) }
func (*recordingInput) Text(string)                {}
func (*recordingInput) LockScreen()                {}
func (*recordingInput) Open(string) error          { return nil }

// Recorded ONNX outputs include both image and world landmarks.
func referenceHand(t *testing.T, name string, left float32) vision.Hand {
	t.Helper()
	data, err := os.ReadFile("../../testdata/reference/control_hands.json")
	if err != nil {
		t.Fatal(err)
	}
	var refs map[string][]vision.Hand
	if err = json.Unmarshal(data, &refs); err != nil {
		t.Fatal(err)
	}
	h := refs[name][0]
	h.TrackID = 1
	h.LeftScore = left
	return h
}

func feed(c *Controller, h vision.Hand, start time.Time, frames int) time.Time {
	for n := 0; n < frames; n++ {
		c.Process([]vision.Hand{h}, start)
		start = start.Add(33 * time.Millisecond)
	}
	return start
}

func TestClickOnceUntilGestureReleased(t *testing.T) {
	for _, tc := range []struct {
		side float32
		want string
	}{{.99, "right"}, {.01, "left"}} {
		t.Run(tc.want, func(t *testing.T) {
			inj := &recordingInput{x: 500, y: 500}
			c, err := New(inj, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Abort()
			click := referenceHand(t, "victory.jpg", tc.side)
			if tc.side < .5 {
				click = testHand("ccccc", 0, 1)
				click.LeftScore = tc.side
			}
			now := feed(c, click, time.Unix(100, 0), 35)
			if len(inj.buttons) != 2 || inj.buttons[0] != tc.want+" down" || inj.buttons[1] != tc.want+" up" {
				t.Fatalf("held gesture: %v", inj.buttons)
			}
			released := testHand("coooo", 0, 0)
			released.LeftScore = tc.side
			now = feed(c, released, now, 12)
			feed(c, click, now, 15)
			if len(inj.buttons) != 4 {
				t.Fatalf("release and repeat: %v", inj.buttons)
			}
		})
	}
}

func TestPointerMovesAndStopsOnFistAndPause(t *testing.T) {
	inj := &recordingInput{x: 500, y: 500}
	c, err := New(inj, 1)
	if err != nil {
		t.Fatal(err)
	}
	h := referenceHand(t, "pointing_up.jpg", .99)
	now := feed(c, h, time.Unix(100, 0), 12)
	for n := 0; n < 12; n++ {
		for j := range h.Landmarks {
			h.Landmarks[j].X += 3
		}
		now = feed(c, h, now, 1)
	}
	if inj.x <= 500 || inj.moves == 0 {
		t.Fatalf("pointer did not move right: %+v", inj)
	}
	now = feed(c, referenceHand(t, "fist.jpg", .99), now, 12)
	moves := inj.moves
	c.SetPaused(true)
	now = feed(c, h, now, 15)
	if inj.moves != moves {
		t.Fatal("moved while paused")
	}
	c.SetPaused(false)
	now = feed(c, h, now, 12)
	if inj.moves != moves {
		t.Fatal("pointer jumped on resume")
	}
	c.Abort()
	feed(c, referenceHand(t, "fist.jpg", .99), now, 12)
	if inj.moves != moves {
		t.Fatal("fist produced movement")
	}
}

func TestUnknownSideAndShortClickDoNothing(t *testing.T) {
	inj := &recordingInput{}
	c, err := New(inj, 1)
	if err != nil {
		t.Fatal(err)
	}
	now := feed(c, referenceHand(t, "victory.jpg", .5), time.Unix(100, 0), 20)
	if len(inj.buttons) != 0 {
		t.Fatal("uncertain hand clicked")
	}
	c.Abort()
	now = feed(c, referenceHand(t, "pointing_up.jpg", .99), now, 10)
	now = feed(c, referenceHand(t, "victory.jpg", .99), now, 2)
	feed(c, testHand("coooo", 0, 0), now, 12)
	if len(inj.buttons) != 0 {
		t.Fatalf("transient click: %v", inj.buttons)
	}
}

func TestDropoutDoesNotRepeatHeldClick(t *testing.T) {
	inj := &recordingInput{}
	c, err := New(inj, 1)
	if err != nil {
		t.Fatal(err)
	}
	h := referenceHand(t, "victory.jpg", .99)
	now := feed(c, h, time.Unix(100, 0), 15)
	c.Process(nil, now)
	now = now.Add(66 * time.Millisecond)
	feed(c, h, now, 15)
	if len(inj.buttons) != 2 {
		t.Fatalf("dropout repeated click: %v", inj.buttons)
	}
}

func TestClickCancelledWhenRawPoseChangesBeforeConfirmation(t *testing.T) {
	inj := &recordingInput{}
	c, _ := New(inj, 1)
	now := time.Unix(100, 0)
	st := gesture.HandState{ID: 1, Present: true, Side: gesture.SideLeft, Pose: "two", RawPose: "two"}
	c.engine.Process([]gesture.HandState{st}, now)
	st.RawPose = "fist"
	c.engine.Process([]gesture.HandState{st}, now.Add(120*time.Millisecond))
	if len(inj.buttons) != 0 {
		t.Fatalf("stale vote fired click: %v", inj.buttons)
	}
}

func TestPointerFreezesWhenSideBecomesUnknown(t *testing.T) {
	inj := &recordingInput{x: 500, y: 500}
	c, _ := New(inj, 1)
	now := time.Unix(100, 0)
	st := gesture.HandState{ID: 1, Present: true, Side: gesture.SideLeft, Pose: "point", RawPose: "point", Features: gesture.Features{PalmSize: 100}}
	c.engine.Process([]gesture.HandState{st}, now)
	st.Landmarks[vision.IndexTip].X = 10
	c.engine.Process([]gesture.HandState{st}, now.Add(33*time.Millisecond))
	moves := inj.moves
	st.Side = gesture.SideUnknown
	st.Landmarks[vision.IndexTip].X = 30
	c.engine.Process([]gesture.HandState{st}, now.Add(66*time.Millisecond))
	if inj.moves != moves {
		t.Fatal("uncertain hand moved pointer")
	}
}

func TestDropoutCancelsPendingClickConfirmation(t *testing.T) {
	inj := &recordingInput{}
	c, _ := New(inj, 1)
	now := time.Unix(100, 0)
	st := gesture.HandState{ID: 1, Present: true, Side: gesture.SideLeft, Pose: "two", RawPose: "two"}
	c.engine.Process([]gesture.HandState{st}, now)
	st.Present = false
	c.engine.Process([]gesture.HandState{st}, now.Add(66*time.Millisecond))
	st.Present = true
	c.engine.Process([]gesture.HandState{st}, now.Add(150*time.Millisecond))
	if len(inj.buttons) != 0 {
		t.Fatal("click confirmed using time spent out of sight")
	}
	c.engine.Process([]gesture.HandState{st}, now.Add(270*time.Millisecond))
	if len(inj.buttons) != 2 {
		t.Fatal("fresh confirmation did not click")
	}
}

func TestEitherHandCanMovePointer(t *testing.T) {
	for _, side := range []float32{.99, .01} {
		t.Run(fmt.Sprint(side), func(t *testing.T) {
			inj := &recordingInput{x: 500, y: 500}
			c, _ := New(inj, 1)
			h := referenceHand(t, "pointing_up.jpg", side)
			now := feed(c, h, time.Unix(100, 0), 12)
			for n := 0; n < 10; n++ {
				for j := range h.Landmarks {
					h.Landmarks[j].X += 3
				}
				now = feed(c, h, now, 1)
			}
			if inj.x <= 500 {
				t.Fatalf("hand leftScore=%.2f did not move cursor", side)
			}
		})
	}
}

func TestTwoPointingHandsZoomInsteadOfFightingForPointer(t *testing.T) {
	inj := &recordingInput{x: 500, y: 500}
	c, _ := New(inj, 1)
	left := referenceHand(t, "pointing_up.jpg", .99)
	right := referenceHand(t, "pointing_up.jpg", .01)
	right.TrackID = 2
	for j := range right.Landmarks {
		right.Landmarks[j].X += 300
	}
	now := feed(c, left, time.Unix(100, 0), 12)
	for n := 0; n < 20; n++ {
		for j := range left.Landmarks {
			left.Landmarks[j].X += 2
			right.Landmarks[j].X -= 4
		}
		c.Process([]vision.Hand{left, right}, now)
		now = now.Add(33 * time.Millisecond)
	}
	if inj.moves != 0 || len(inj.wheels) == 0 {
		t.Fatal("two pointing hands did not exclusively zoom")
	}
}

func TestUnknownPointCannotBlockKnownHand(t *testing.T) {
	inj := &recordingInput{x: 500, y: 500}
	c, _ := New(inj, 1)
	now := time.Unix(100, 0)
	unknown := gesture.HandState{ID: 1, Present: true, Pose: "point", RawPose: "point", Features: gesture.Features{PalmSize: 100}}
	known := unknown
	known.ID = 2
	known.Side = gesture.SideLeft
	c.engine.Process([]gesture.HandState{unknown, known}, now)
	known.Landmarks[vision.IndexTip].X = 10
	c.engine.Process([]gesture.HandState{unknown, known}, now.Add(33*time.Millisecond))
	if inj.x <= 500 {
		t.Fatal("uncertain hand reserved pointer and blocked known hand")
	}
}

func TestSwappedCameraHandednessControlsMovementAndClick(t *testing.T) {
	inj := &recordingInput{x: 500, y: 500}
	c, _ := New(inj, 1)
	c.SetSwapHands(true)
	// This camera reports the user's physical left hand as model-right.
	h := referenceHand(t, "pointing_up.jpg", .01)
	now := feed(c, h, time.Unix(100, 0), 12)
	for n := 0; n < 10; n++ {
		for j := range h.Landmarks {
			h.Landmarks[j].X += 3
		}
		now = feed(c, h, now, 1)
	}
	if inj.x <= 500 {
		t.Fatal("corrected left hand did not move cursor")
	}
	now = feed(c, referenceHand(t, "victory.jpg", .01), now, 15)
	if len(inj.buttons) != 2 || inj.buttons[0] != "right down" || inj.buttons[1] != "right up" {
		t.Fatalf("physical left hand clicked wrong button: %v", inj.buttons)
	}
	c.Abort()
	pinch := testHand("ccccc", 0, 1)
	pinch.LeftScore = .99
	feed(c, pinch, now, 15)
	if len(inj.buttons) != 4 || inj.buttons[2] != "left down" || inj.buttons[3] != "left up" {
		t.Fatalf("physical right hand clicked wrong button: %v", inj.buttons)
	}
}
