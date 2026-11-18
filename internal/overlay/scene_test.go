package overlay

import (
	"testing"
	"time"

	"gesturectl/internal/control"
	"gesturectl/internal/engine"
	"gesturectl/internal/gesture"
	"gesturectl/internal/winui"
)

func TestRowsShowEachHandInViewUntilTheFramesStop(t *testing.T) {
	now := time.Unix(100, 0)
	var s State
	s.Frame([]control.Hint{
		{ID: 1, Side: gesture.SideLeft, Fingers: [gesture.NumFingers]bool{false, true, true}, Text: "两指右键", Bound: true, Progress: .4},
		{ID: 2, Side: gesture.SideRight, Fingers: [gesture.NumFingers]bool{false, true}, Text: "右手移动", Bound: true, Active: true, Progress: 1},
		{ID: 3, Side: gesture.SideUnknown, Text: "握拳"},
		{ID: 4, Side: gesture.SideUnknown},
	}, nil, now)
	rows := s.Rows(now.Add(100 * time.Millisecond))
	if len(rows) != 3 {
		t.Fatalf("rows are %+v", rows)
	}
	if r := rows[0]; r.Look != Confirming || r.Text != "两指右键" || r.Progress != .4 || !r.HasHand || !r.Hand.Left || r.Hand.Fingers != [5]int{winui.Curled, winui.Raised, winui.Raised} {
		t.Errorf("a pose being confirmed shows %+v", r)
	}
	if r := rows[1]; r.Look != Running || r.Progress != 1 || r.Hand.Left {
		t.Errorf("a running action shows %+v", r)
	}
	if r := rows[2]; r.Look != Seen || r.Text != "握拳" {
		t.Errorf("a pose without a binding shows %+v", r)
	}
	if rows := s.Rows(now.Add(staleAfter + time.Millisecond)); len(rows) != 0 {
		t.Fatalf("hands of an old frame still show: %+v", rows)
	}
}

func TestEventsFlashTheirHandAndFade(t *testing.T) {
	now := time.Unix(100, 0)
	left := control.Hint{ID: 1, Side: gesture.SideLeft, Text: "两指右键", Bound: true, Progress: 1}
	var s State
	s.Frame([]control.Hint{left}, []engine.Event{
		{Hand: "left", Binding: "两指右键", What: "fire"},
		{Hand: "left", Binding: "两指右键", What: "fire"},
		{Hand: "both", Binding: "双食指缩放", What: "start"},
		{Hand: "right", Binding: "右手移动", What: "end"},
		{Hand: "right", Binding: "窗口切换", What: "cancel"},
	}, now)
	rows := s.Rows(now)
	if len(rows) != 2 || rows[0].Look != Fired || rows[0].Text != "两指右键" || rows[0].Glow != 1 || !rows[0].HasHand ||
		rows[1].Look != Fired || rows[1].Text != "双食指缩放" || rows[1].HasHand {
		t.Fatalf("rows after a click and a zoom are %+v", rows)
	}
	half := now.Add(flashFor / 2)
	s.Frame([]control.Hint{left}, nil, half)
	if rows := s.Rows(half); len(rows) != 2 || rows[0].Glow <= .4 || rows[0].Glow >= .6 {
		t.Fatalf("half way through the flash the rows are %+v", rows)
	}
	after := now.Add(flashFor)
	s.Frame([]control.Hint{left}, nil, after)
	if rows := s.Rows(after); len(rows) != 1 || rows[0].Look != Confirming {
		t.Fatalf("after the flash the rows are %+v", rows)
	}
}

func TestHiddenHintsStillShowThePauseNotice(t *testing.T) {
	now := time.Unix(100, 0)
	s := State{Hidden: true}
	s.Frame([]control.Hint{{Side: gesture.SideLeft, Text: "左手移动", Bound: true, Active: true}}, []engine.Event{{Hand: "left", Binding: "左手移动", What: "start"}}, now)
	if rows := s.Rows(now); len(rows) != 0 {
		t.Fatalf("hints turned off show %+v", rows)
	}
	s.Paused(true, now)
	if rows := s.Rows(now); len(rows) != 1 || rows[0].Look != Pause || rows[0].Text == "" {
		t.Fatalf("pausing shows %+v", rows)
	}
	later := now.Add(time.Second)
	s.Paused(false, later)
	if rows := s.Rows(later); len(rows) != 1 || rows[0].Look != Resume {
		t.Fatalf("resuming shows %+v", rows)
	}
	if rows := s.Rows(later.Add(noticeFor)); len(rows) != 0 {
		t.Fatalf("the notice outlasts its time: %+v", rows)
	}
}

func TestPlaceKeepsTheHintOnTheWorkArea(t *testing.T) {
	work := Rect{0, 0, 1920, 1040}
	if p := Place(Point{100, 100}, 120, 40, work, 96); p != (Point{116, 122}) {
		t.Errorf("by the cursor the hint is at %+v", p)
	}
	if p := Place(Point{100, 100}, 120, 40, work, 192); p != (Point{132, 144}) {
		t.Errorf("at 200%% the hint is at %+v", p)
	}
	if p := Place(Point{1900, 1030}, 120, 40, work, 96); p != (Point{1772, 982}) {
		t.Errorf("in the corner the hint is at %+v", p)
	}
	if p := Place(Point{-5, -5}, 120, 40, work, 96); p.X < 0 || p.Y < 0 {
		t.Errorf("off the work area the hint is at %+v", p)
	}
}
