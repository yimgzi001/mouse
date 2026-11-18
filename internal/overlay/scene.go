// Package overlay draws the hint next to the cursor: the gesture of each hand
// in view, how far it is confirmed, a flash when it acts, and a short notice
// when the control is paused or resumed.
package overlay

import (
	"time"

	"gesturectl/internal/control"
	"gesturectl/internal/engine"
	"gesturectl/internal/gesture"
	"gesturectl/internal/winui"
)

// Look says how a row of the hint is drawn.
type Look int

const (
	// Seen is a pose that no enabled binding of the hand uses.
	Seen Look = iota
	// Confirming is a pose on its way to its binding; the bar shows how far.
	Confirming
	// Running is an action in effect, such as moving the cursor.
	Running
	// Fired is a binding that has just acted; the row flashes.
	Fired
	// Pause and Resume are the notices for pausing and resuming the control.
	Pause
	Resume
)

// Row is one line of the hint.
type Row struct {
	Look Look
	Text string
	// Hand is the glyph of a hand in view; notices have none.
	Hand    winui.Hand
	HasHand bool
	// Progress fills the bar of Confirming, Running and Fired rows, from 0 to 1.
	Progress float64
	// Glow fades the flash of a Fired row from 1 to 0.
	Glow float64
}

func (r Row) hasBar() bool { return r.Look == Confirming || r.Look == Running || r.Look == Fired }

const (
	// staleAfter drops the hands of a frame that no newer frame followed, as
	// when the control is paused or the camera stalls.
	staleAfter = 300 * time.Millisecond
	flashFor   = 450 * time.Millisecond
	noticeFor  = 1500 * time.Millisecond
)

type flash struct {
	hand, text string
	until      time.Time
}

// State is what the hint shows between frames. It is not safe for concurrent
// use.
type State struct {
	// Hidden leaves out the rows about hands; notices show anyway.
	Hidden  bool
	hints   []control.Hint
	seen    time.Time
	flashes []flash
	notice  Row
	until   time.Time
}

// Frame takes the hints and the engine events of a frame.
func (s *State) Frame(hints []control.Hint, events []engine.Event, now time.Time) {
	s.hints, s.seen = hints, now
	for _, e := range events {
		if e.What == "fire" || e.What == "start" || e.What == "confirm" {
			s.flashes = append(s.flashes, flash{e.Hand, e.Binding, now.Add(flashFor)})
		}
	}
}

// Paused shows for a moment that the control was paused or resumed.
func (s *State) Paused(paused bool, now time.Time) {
	s.notice = Row{Look: Resume, Text: "已恢复手势控制"}
	if paused {
		s.notice = Row{Look: Pause, Text: "已暂停，按 F8 恢复"}
	}
	s.until = now.Add(noticeFor)
}

// Rows returns the rows to draw at now: the notice, then a row for each hand
// in view, then flashes of hands out of view. No rows hide the hint.
func (s *State) Rows(now time.Time) []Row {
	var rows []Row
	if now.Before(s.until) {
		rows = append(rows, s.notice)
	}
	live := s.flashes[:0]
	for _, f := range s.flashes {
		if now.Before(f.until) {
			live = append(live, f)
		}
	}
	s.flashes = live
	if s.Hidden {
		return rows
	}
	flashed := map[string]bool{}
	if now.Sub(s.seen) <= staleAfter {
		for _, h := range s.hints {
			row := Row{Look: Seen, Text: h.Text, Hand: glyph(h), HasHand: true}
			switch {
			case h.Active:
				row.Look, row.Progress = Running, 1
			case h.Bound:
				row.Look, row.Progress = Confirming, h.Progress
			}
			side := h.Side.String()
			if f, ok := s.latest(side); ok && !flashed[side] {
				row.Look, row.Text, row.Progress, row.Glow = Fired, f.text, 1, glow(f, now)
				flashed[side] = true
			}
			if row.Text != "" {
				rows = append(rows, row)
			}
		}
	}
	for i := len(s.flashes) - 1; i >= 0; i-- {
		if f := s.flashes[i]; !flashed[f.hand] {
			flashed[f.hand] = true
			rows = append(rows, Row{Look: Fired, Text: f.text, Progress: 1, Glow: glow(f, now)})
		}
	}
	return rows
}

// latest returns the newest flash of a hand.
func (s *State) latest(hand string) (flash, bool) {
	for i := len(s.flashes) - 1; i >= 0; i-- {
		if s.flashes[i].hand == hand {
			return s.flashes[i], true
		}
	}
	return flash{}, false
}

func glow(f flash, now time.Time) float64 {
	return max(0, min(1, float64(f.until.Sub(now))/float64(flashFor)))
}

func glyph(h control.Hint) winui.Hand {
	g := winui.Hand{Pinch: h.Pinch, Left: h.Side == gesture.SideLeft}
	for i, up := range h.Fingers {
		if up {
			g.Fingers[i] = winui.Raised
		}
	}
	return g
}
