package engine

import (
	"math"
	"time"

	"gesturectl/internal/filter"
	"gesturectl/internal/gesture"
)

type PairZoomSettings struct {
	// Enabled and ConfirmMs are retained for reading legacy settings. Enabled
	// does not create a binding; each binding supplies its own confirmation.
	Enabled     bool    `json:"enabled"`
	Sensitivity float64 `json:"sensitivity"`
	ConfirmMs   int     `json:"confirmMs"`
}

type pairBinding struct {
	b             *binding
	active, fired bool
	next          time.Time
}
type pairZoom struct {
	left, right   int
	since, last   time.Time
	bindings      []pairBinding
	have          bool
	distance, acc float64
	filter        filter.OneEuro
	consumed      bool
}

func (p *pairZoom) active() bool {
	for _, slot := range p.bindings {
		if slot.active {
			return true
		}
	}
	return false
}
func (p *pairZoom) name() string {
	for _, slot := range p.bindings {
		if slot.active {
			return slot.b.Name
		}
	}
	return p.bindings[0].b.Name
}
func (e *Engine) recordPair(b *binding, what string, now time.Time) {
	e.appendEvent(Event{Time: now, Hand: "both", Pose: b.Pose, Binding: b.Name, Action: b.Action.Type, What: what})
}
func (e *Engine) endPairZoom(now time.Time) {
	p := e.pair
	if p == nil {
		return
	}
	e.pair = nil
	for _, slot := range p.bindings {
		if slot.active && slot.b.spec.Continuous {
			e.recordPair(slot.b, "end", now)
		}
	}
}

// finishPair allows exit bindings only on a recognized release/lost event,
// never on pause, reconfiguration or abort.
func (e *Engine) finishPair(states []gesture.HandState, now time.Time) {
	p := e.pair
	e.endPairZoom(now)
	for _, slot := range p.bindings {
		if slot.active && slot.b.Trigger == "exit" && (!e.paused || slot.b.Action.Type == "toggle_pause") {
			e.firePair(slot.b, now)
		}
	}
	// Single-hand motion starts with a new baseline. Consume a releasing
	// discrete pose so ending a composite cannot also click the old target.
	e.hands = map[int]*hand{}
	for _, st := range states {
		if !st.Present || st.Lost || (st.ID != p.left && st.ID != p.right) {
			continue
		}
		h := &hand{id: st.ID, side: st.Side, pose: st.Pose, since: now, rawPose: st.RawPose, rawSince: now, fired: map[*binding]bool{}, next: map[*binding]time.Time{}}
		for _, b := range e.bindings {
			if !b.spec.Continuous && !isSwipe(b.Trigger) && e.matches(b, st.RawPose, st.Side) {
				h.blockedPose = st.RawPose
				break
			}
		}
		e.hands[st.ID] = h
	}
}

func (e *Engine) firePair(b *binding, now time.Time) {
	// A virtual owner has no pointer history or single-hand controller.
	before := e.seq
	e.fire(b, &hand{id: 0, side: gesture.SideUnknown}, now)
	for i := range e.events {
		if e.events[i].Seq > before && e.events[i].Binding == b.Name && e.events[i].What == "fire" {
			e.events[i].Hand = "both"
		}
	}
}

// processPairZoom routes recognized composite payloads through enabled
// bindings. Every matching binding reserves both hands while confirming and
// while held, even after a once-only action has already fired.
func (e *Engine) processPairZoom(states []gesture.HandState, now time.Time) bool {
	bound := false
	for _, b := range e.bindings {
		if b.Hand == "both" && (!e.paused || b.Action.Type == "toggle_pause") {
			bound = true
			break
		}
	}
	if !bound {
		e.pair = nil
		return false
	}
	data, candidate := gesture.TwoIndex(states)
	if p := e.pair; p != nil {
		var a, b *gesture.HandState
		for i := range states {
			if states[i].ID == p.left {
				a = &states[i]
			}
			if states[i].ID == p.right {
				b = &states[i]
			}
		}
		definiteRelease := func(st *gesture.HandState) bool {
			return st == nil || st.Lost || st.Present && st.Side != gesture.SideUnknown && st.Pose != "" && st.Pose == st.RawPose && st.Pose != "point"
		}
		if definiteRelease(a) || definiteRelease(b) || candidate && (data.LeftID != p.left || data.RightID != p.right) {
			e.finishPair(states, now)
			return false
		}
		if !candidate || !data.Stable {
			p.since = time.Time{}
			p.have = false
			p.acc = 0
			return true
		}
	}
	if !candidate {
		return false
	}
	if e.pair == nil {
		e.stopAll(now)
		e.hands = map[int]*hand{}
		p := &pairZoom{left: data.LeftID, right: data.RightID}
		for _, b := range e.bindings {
			if b.Hand == "both" && (!e.paused || b.Action.Type == "toggle_pause") {
				p.bindings = append(p.bindings, pairBinding{b: b})
			}
		}
		e.pair = p
	}
	p := e.pair
	if p.consumed {
		return true
	}
	if !data.Stable {
		p.since = time.Time{}
		p.have = false
		p.acc = 0
		return true
	}
	if p.since.IsZero() {
		p.since = now
	}
	for i := range p.bindings {
		slot := &p.bindings[i]
		b := slot.b
		if e.paused && b.Action.Type != "toggle_pause" {
			continue
		}
		if now.Sub(p.since) < ms(b.HoldMs) {
			continue
		}
		slot.active = true
		switch b.Trigger {
		case "hold":
			if !slot.fired {
				slot.fired = true
				e.recordPair(b, "start", now)
			}
			if b.Action.Type == "pair_zoom" {
				e.updatePairDistance(p, b, data.Distance, now)
			}
		case "enter", "long_press":
			if !slot.fired {
				slot.fired = true
				e.firePair(b, now)
			}
		case "repeat":
			if !slot.fired || !now.Before(slot.next) {
				slot.fired = true
				slot.next = now.Add(ms(b.IntervalMs))
				e.firePair(b, now)
			}
		}
		if e.pair != p || e.paused {
			return true
		}
	}
	return true
}

// Only the action controller turns distance data into input. Each Ctrl is
// released in the same frame; landmark jumps never accumulate excess output.
func (e *Engine) updatePairDistance(p *pairZoom, b *binding, distance float64, now time.Time) {
	if distance < 20 || math.IsNaN(distance) || math.IsInf(distance, 0) {
		p.have = false
		p.acc = 0
		return
	}
	value := math.Log(distance)
	if !p.have || now.Sub(p.last) > maxFrameGap {
		p.filter = filter.OneEuro{MinCutoff: 2, Beta: 2, DCutoff: 1}
		p.distance = p.filter.Filter(value, 0)
		p.last = now
		p.have = true
		p.acc = 0
		return
	}
	dt := now.Sub(p.last).Seconds()
	if dt <= 0 {
		return
	}
	value = p.filter.Filter(value, dt)
	p.acc += (value - p.distance) * 8 * legacyGain(e.settings.PairZoom.Sensitivity) * b.num("sensitivity") / math.Ln2
	p.distance, p.last = value, now
	p.acc = math.Max(-2, math.Min(2, p.acc))
	if steps := int(p.acc); steps != 0 {
		p.acc -= float64(steps)
		e.keyDown(vkCtrl)
		e.inj.Wheel(steps*wheelNotch, false)
		e.keyUp(vkCtrl)
	}
}
