package control

import (
	"math"
	"testing"
	"time"

	"gesturectl/internal/engine"
	"gesturectl/internal/gesture"
)

func TestHintsNameTheBindingAndHowFarItIs(t *testing.T) {
	cfg := DefaultConfiguration(DefaultPreferences())
	var click engine.Binding
	for _, b := range cfg.Bindings {
		if b.ID == "right-click" {
			click = b
		}
	}
	confirm := float64(cfg.Recognition.VoteWindowMs) / 2
	total := confirm + float64(click.HoldMs)
	if click.Pose != "two" || click.Hand != "left" || click.HoldMs <= 0 {
		t.Fatalf("the default right click changed: %+v", click)
	}
	now := time.Unix(100, 0)
	hand := gesture.HandState{ID: 7, Side: gesture.SideLeft, Present: true, RawPose: "two", RawSince: now.Add(-30 * time.Millisecond)}
	hint := func(h *Hinter, running map[int]string) Hint {
		t.Helper()
		hints := h.Hints([]gesture.HandState{hand}, running, now)
		if len(hints) != 1 {
			t.Fatalf("%d hints for one hand", len(hints))
		}
		return hints[0]
	}
	near := func(got, want float64) bool { return math.Abs(got-want) < 1e-9 }
	h := NewHinter(cfg)
	if got := hint(h, nil); got.Text != click.Name || !got.Bound || got.Active || !near(got.Progress, 30/total) || got.ID != 7 || got.Side != gesture.SideLeft {
		t.Fatalf("a pose being confirmed gives %+v", got)
	}
	hand.RawSince = now.Add(-time.Minute)
	if got := hint(h, nil); got.Progress >= 1 {
		t.Fatalf("a pose the recognizer has not confirmed shows progress %v", got.Progress)
	}
	hand.Pose, hand.PoseSince = "two", now.Add(-40*time.Millisecond)
	if got := hint(h, nil); !near(got.Progress, (confirm+40)/total) {
		t.Fatalf("a confirmed pose held for 40 ms shows progress %v", got.Progress)
	}
	hand.PoseSince = now.Add(-time.Second)
	if got := hint(h, nil); got.Progress != 1 {
		t.Fatalf("a pose held long enough shows progress %v", got.Progress)
	}
	if got := hint(h, map[int]string{7: "正在拖拽"}); got.Text != "正在拖拽" || !got.Active || got.Progress != 1 || !got.Bound {
		t.Fatalf("a running action gives %+v", got)
	}
	hand.Side = gesture.SideUnknown
	if got := hint(h, nil); got.Bound || got.Text != "二指" || got.Progress != 0 {
		t.Fatalf("a hand of unknown side gives %+v", got)
	}
	hand.Side = gesture.SideLeft
	for i := range cfg.Bindings {
		if cfg.Bindings[i].ID == "right-click" {
			cfg.Bindings[i].Enabled = false
		}
	}
	if got := hint(NewHinter(cfg), nil); got.Bound || got.Text != "二指" {
		t.Fatalf("a disabled binding still hints: %+v", got)
	}
	hand.RawPose, hand.Fingers, hand.Touch = "", [gesture.NumFingers]bool{true, true}, [gesture.NumFingers - 1]bool{true}
	if got := hint(h, nil); got.Text != "" || got.Bound || !got.Pinch || got.Fingers != hand.Fingers {
		t.Fatalf("a hand without a pose gives %+v", got)
	}
}

func TestHintsSkipAbsentHandsAndPutTheLeftFirst(t *testing.T) {
	h := NewHinter(DefaultConfiguration(DefaultPreferences()))
	states := []gesture.HandState{
		{ID: 1, Side: gesture.SideUnknown, Present: true},
		{ID: 2, Side: gesture.SideRight, Present: true},
		{ID: 3, Side: gesture.SideLeft},
		{ID: 4, Side: gesture.SideLeft, Present: true},
	}
	hints := h.Hints(states, nil, time.Unix(100, 0))
	if len(hints) != 3 || hints[0].ID != 4 || hints[1].ID != 2 || hints[2].ID != 1 {
		t.Fatalf("hints are %+v", hints)
	}
	if len(h.Hints(nil, nil, time.Unix(100, 0))) != 0 {
		t.Fatal("hints without hands")
	}
}
