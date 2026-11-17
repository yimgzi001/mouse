package engine

import (
	"time"

	"gesturectl/internal/gesture"
)

// AdoptState replaces an old engine without treating a held gesture as a new
// activation. Callers serialize camera processing and pass the last actual
// recognition states. Compiled bindings and running controllers are never
// copied; all held input is released before the new engine takes ownership.
func (e *Engine) AdoptState(previous *Engine, states []gesture.HandState, swapSides bool) {
	if previous == nil || previous == e {
		return
	}
	previous.mu.Lock()
	defer previous.mu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	now := previous.frameTime
	if now.IsZero() {
		now = time.Now()
	}
	pair := previous.pair
	if pair == nil {
		pair = previous.pausedPair
	}
	previous.stopAll(now)
	e.stopAll(now)
	e.ptr = pointerMemory{}
	e.frameTime = now
	e.lastQuickCycle = previous.lastQuickCycle
	e.hands = map[int]*hand{}
	flip := func(side gesture.Side) gesture.Side {
		if swapSides {
			if side == gesture.SideLeft {
				return gesture.SideRight
			}
			if side == gesture.SideRight {
				return gesture.SideLeft
			}
		}
		return side
	}
	for id, src := range previous.hands {
		h := &hand{id: id, side: flip(src.side), pose: src.pose, since: now, born: src.born, rawPose: src.rawPose, rawSince: now, missing: src.missing,
			blockedPose: src.blockedPose, releaseFrom: src.releaseFrom, releaseTo: append([]string(nil), src.releaseTo...),
			fired: map[*binding]bool{}, next: map[*binding]time.Time{}, consumed: map[string]bool{}}
		for pose, value := range src.consumed {
			h.consumed[pose] = value
		}
		e.hands[id] = h
	}
	observed := make([]gesture.HandState, len(states))
	copy(observed, states)
	for i := range observed {
		st := &observed[i]
		st.Side = flip(st.Side)
		if st.Lost {
			delete(e.hands, st.ID)
			continue
		}
		h := e.hands[st.ID]
		if h == nil {
			h = &hand{id: st.ID, since: now, rawSince: now, fired: map[*binding]bool{}, next: map[*binding]time.Time{}, consumed: map[string]bool{}}
			e.hands[st.ID] = h
		}
		if st.Side != gesture.SideUnknown {
			h.side = st.Side
		}
		if st.Pose != "" {
			h.pose = st.Pose
		}
		if st.Present && st.RawPose != "" {
			h.rawPose = st.RawPose
		}
		h.missing = !st.Present
	}
	for _, h := range e.hands {
		for _, pose := range []string{h.pose, h.rawPose} {
			if pose == "" {
				continue
			}
			for _, b := range e.bindings {
				if requiresFreshActivation(b) && b.Pose == pose && (h.side == gesture.SideUnknown || e.matches(b, pose, h.side)) {
					h.consumed[pose] = true
					break
				}
			}
		}
	}
	data, candidate := gesture.TwoIndex(observed)
	if pair != nil {
		// Removing the composite binding releases pointer ownership immediately,
		// while the first return gesture still only releases the old composite.
		for _, id := range []int{pair.left, pair.right} {
			if h := e.hands[id]; h != nil {
				h.releaseFrom = "point"
				for _, b := range e.bindings {
					if requiresFreshActivation(b) && b.Hand != "both" && (h.side == gesture.SideUnknown || e.matches(b, b.Pose, h.side)) {
						h.releaseTo = append(h.releaseTo, b.Pose)
					}
				}
			}
		}
	}
	if pair != nil || candidate {
		p := &pairZoom{consumed: true}
		if pair != nil {
			p.left, p.right = pair.left, pair.right
			if swapSides {
				p.left, p.right = p.right, p.left
			}
		}
		if candidate {
			p.left, p.right = data.LeftID, data.RightID
		}
		for _, b := range e.bindings {
			if b.Hand == "both" {
				p.bindings = append(p.bindings, pairBinding{b: b})
			}
		}
		if len(p.bindings) != 0 {
			e.pair = p
		}
	}
}

func requiresFreshActivation(b *binding) bool {
	return !b.spec.Continuous || b.spec.Sticky || b.Action.Type == "drag" || b.Action.Type == "key_hold"
}
