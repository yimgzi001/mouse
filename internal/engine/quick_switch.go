package engine

import (
	"strings"
	"time"

	"gesturectl/internal/gesture"
)

func isSwipe(trigger string) bool { return strings.HasPrefix(trigger, "swipe_") }

// A fast swipe can finish before majority voting catches up to its palm pose.
// The recognizer supplies motion made only during that raw pose. Consume the
// whole frame so the stable-pose route cannot fire it twice. Window cycling
// additionally releases held inputs before its atomic Alt+Tab chord.
func (e *Engine) quickSwipe(states []gesture.HandState, now time.Time) bool {
	if e.paused {
		return false
	}
	owner := e.exclusiveHand()
	for _, st := range states {
		if !st.Present || st.Lost || st.Side == gesture.SideUnknown || st.Swipe == "" {
			continue
		}
		if owner != nil && owner.id != st.ID {
			continue
		}
		if h := e.hands[st.ID]; h != nil {
			h.promoteReturn(&st)
			if h.blockedPose == st.RawPose || h.consumed[st.RawPose] || h.pendingReturn(st.RawPose) {
				continue
			}
		}
		for _, b := range e.bindings {
			if owner != nil && !isFinishAction(b.Action.Type) {
				continue
			}
			if b.spec.Continuous || b.Trigger != "swipe_"+st.Swipe || !e.matches(b, st.RawPose, st.Side) {
				continue
			}
			if b.Action.Type == "window_cycle" && !e.lastQuickCycle.IsZero() && now.Sub(e.lastQuickCycle) < 250*time.Millisecond {
				return true
			}
			if b.Action.Type == "window_cycle" {
				e.lastQuickCycle = now
			}
			h := e.hands[st.ID]
			if h == nil {
				h = &hand{id: st.ID, side: st.Side, pose: st.Pose, since: now, fired: map[*binding]bool{}, next: map[*binding]time.Time{}}
				e.hands[st.ID] = h
			}
			h.side = st.Side
			e.fire(b, h, now)
			return true
		}
	}
	return false
}
