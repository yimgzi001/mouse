package control

import (
	"sort"
	"strings"
	"time"

	"gesturectl/internal/engine"
	"gesturectl/internal/gesture"
)

// Hint tells, for one hand in view, what the program sees and what that
// does. The on-screen hint next to the cursor draws it.
type Hint struct {
	ID   int
	Side gesture.Side
	// Fingers and Pinch are the finger states as the camera reads them now.
	Fingers [gesture.NumFingers]bool
	Pinch   bool
	// Text names the action in effect, else the binding that the pose calls,
	// else the pose; it is empty when the hand shows no known pose.
	Text string
	// Progress runs from 0 to 1 while a pose is confirmed and held for its
	// binding. At 1 the binding has what it waits for.
	Progress float64
	// Active says a continuous action of this hand is in effect.
	Active bool
	// Bound is false for a pose that no enabled binding uses.
	Bound bool
}

// Hinter works out hints for one configuration.
type Hinter struct {
	bindings []engine.Binding
	names    map[string]string
	confirm  map[string]int
	vote     int
}

func NewHinter(cfg Configuration) *Hinter {
	h := &Hinter{names: map[string]string{}}
	for _, b := range cfg.Bindings {
		// A hint follows one hand that makes a pose. Bindings of both hands
		// show once they run, and those that fire as the pose ends, or on a
		// swipe, are not what holding the pose leads to.
		if b.Enabled && b.Hand != "both" && b.Trigger != "exit" && !strings.HasPrefix(b.Trigger, "swipe_") {
			h.bindings = append(h.bindings, b)
		}
	}
	for _, pose := range GestureCatalog() {
		name, _, _ := strings.Cut(pose.Name, "（")
		h.names[pose.ID] = name
	}
	settings := cfg.Recognition.settings()
	h.confirm, h.vote = settings.PoseConfirmMs, settings.VoteWindowMs
	return h
}

// Hints returns a hint for each hand in view, the left hand first. running
// holds the continuous actions in effect, as Controller.Feedback reports them.
func (h *Hinter) Hints(states []gesture.HandState, running map[int]string, now time.Time) []Hint {
	var hints []Hint
	for i := range states {
		st := &states[i]
		if !st.Present {
			continue
		}
		hint := Hint{ID: st.ID, Side: st.Side, Fingers: st.Fingers, Pinch: st.Touch[0]}
		if name := running[st.ID]; name != "" {
			hint.Text, hint.Progress, hint.Active, hint.Bound = name, 1, true, true
		} else if st.RawPose != "" {
			hint.Text = h.names[st.RawPose]
			if hint.Text == "" {
				hint.Text = st.RawPose
			}
			for j := range h.bindings {
				b := &h.bindings[j]
				if b.Pose == st.RawPose && st.Side != gesture.SideUnknown && (b.Hand == "any" || b.Hand == st.Side.String()) {
					hint.Text, hint.Bound, hint.Progress = b.Name, true, h.progress(st, b, now)
					break
				}
			}
		}
		hints = append(hints, hint)
	}
	sort.SliceStable(hints, func(i, j int) bool { return order(hints[i].Side) < order(hints[j].Side) })
	return hints
}

func order(side gesture.Side) int {
	if side == gesture.SideUnknown {
		return int(gesture.SideRight) + 1
	}
	return int(side)
}

// progress estimates how far a pose is on its way to its binding: first the
// recognizer confirms the pose, then the binding waits its own time.
func (h *Hinter) progress(st *gesture.HandState, b *engine.Binding, now time.Time) float64 {
	confirm := float64(h.confirm[b.Pose])
	if confirm <= 0 {
		// Other poses win a vote, which takes about half the vote window.
		confirm = float64(h.vote) / 2
	}
	hold := float64(b.HoldMs)
	if confirm+hold <= 0 {
		return 1
	}
	if st.Pose == b.Pose {
		held := float64(now.Sub(st.PoseSince).Milliseconds())
		return (confirm + max(0, min(held, hold))) / (confirm + hold)
	}
	seen := float64(now.Sub(st.RawSince).Milliseconds())
	return min(.95, max(0, min(seen, confirm))/(confirm+hold))
}
