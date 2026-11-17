package engine

import (
	"math"
	"time"

	"gesturectl/internal/filter"
	"gesturectl/internal/gesture"
	"gesturectl/internal/vision"
)

const wheelNotch = 120

type stopReason int

const (
	stopPose    stopReason = iota // the pose ended
	stopLost                      // the hand was withdrawn
	stopCancel                    // stop gesture, pause, reconfiguration or abort
	stopTimeout                   // the action gave up by itself
	stopConfirm                   // explicit confirmation of a selected window
)

// controller is a continuous action in progress. update is called for every
// frame; live is false while tracking is uncertain or the action's required
// shape deviates. The window selector only requires its owner's palm after
// activation. An action must hold still whenever live is false.
type controller interface {
	start(e *Engine, st *gesture.HandState, now time.Time)
	update(e *Engine, st *gesture.HandState, now time.Time, live bool)
	stop(e *Engine, now time.Time, reason stopReason)
	expired(e *Engine, now time.Time) bool
}

func newController(b *binding, handID int) controller {
	switch b.Action.Type {
	case "pointer_move":
		return &pointerCtl{hand: handID, source: b.str("source"), sensitivity: b.num("sensitivity")}
	case "drag":
		return &pointerCtl{hand: handID, source: b.str("source"), button: b.str("button"), sensitivity: b.num("sensitivity")}
	case "scroll":
		return &followCtl{kind: followScroll, horizontal: b.str("axis") == "horizontal", natural: b.flag("natural"), sensitivity: b.num("sensitivity")}
	case "zoom":
		return &followCtl{kind: followZoom, natural: b.flag("natural"), sensitivity: b.num("sensitivity")}
	case "volume":
		return &followCtl{kind: followVolume}
	case "window_switcher":
		return &switcherCtl{sensitivity: b.num("sensitivity")}
	case "key_hold":
		c := &keyHoldCtl{}
		if len(b.keys) > 0 {
			c.keys = b.keys[0]
		}
		return c
	}
	return idleCtl{}
}

type idleCtl struct{}

func (idleCtl) start(*Engine, *gesture.HandState, time.Time)        {}
func (idleCtl) update(*Engine, *gesture.HandState, time.Time, bool) {}
func (idleCtl) stop(*Engine, time.Time, stopReason)                 {}
func (idleCtl) expired(*Engine, time.Time) bool                     { return false }

// motion measures how far a point of the hand moves between frames, in palm
// lengths, after smoothing. The palm length is fixed when tracking starts so
// that noise in it does not shift the measured position.
type motion struct {
	fx, fy filter.OneEuro
	have   bool
	palm   float64
	x, y   float64
	t      time.Time
	dt     float64
}

const maxFrameGap = 250 * time.Millisecond

func (m *motion) rebase() { m.have = false }

// step returns the movement since the previous frame and the current speed in
// palm lengths per second. ok is false for the first frame after a rebase.
func (m *motion) step(s *PointerSettings, px, py, palm float64, now time.Time) (dx, dy, speed float64, ok bool) {
	if m.have && now.Sub(m.t) > maxFrameGap {
		m.have = false
	}
	if !m.have {
		if palm <= 0 {
			return 0, 0, 0, false
		}
		m.palm = palm
		m.fx = filter.OneEuro{MinCutoff: s.MinCutoff, Beta: s.Beta, DCutoff: s.DCutoff}
		m.fy = m.fx
		m.x = m.fx.Filter(px/m.palm, 0)
		m.y = m.fy.Filter(py/m.palm, 0)
		m.t, m.have = now, true
		return 0, 0, 0, false
	}
	dt := now.Sub(m.t).Seconds()
	if dt <= 0 {
		return 0, 0, 0, false
	}
	x := m.fx.Filter(px/m.palm, dt)
	y := m.fy.Filter(py/m.palm, dt)
	dx, dy = x-m.x, y-m.y
	m.x, m.y, m.t, m.dt = x, y, now, dt
	return dx, dy, math.Hypot(dx, dy) / dt, true
}

func smoothstep(t float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	return t * t * (3 - 2*t)
}

// pointerMemory remembers where the engine last put the cursor, so that a
// click by the hand that moves the cursor can undo the small shift caused by
// forming the click gesture.
type pointerMemory struct {
	hand     int
	hist     []cursorSample
	frozenAt time.Time
}

type cursorSample struct {
	t    time.Time
	x, y int
}

const (
	pointerHistory   = 600 * time.Millisecond
	rollbackValidFor = 700 * time.Millisecond
	// A cursor farther than this from where the engine left it was moved by
	// something else, such as the real mouse, and is left alone.
	rollbackMaxJump = 80
)

func (p *pointerMemory) record(now time.Time, x, y int) {
	drop := 0
	for drop < len(p.hist) && now.Sub(p.hist[drop].t) > pointerHistory {
		drop++
	}
	p.hist = append(p.hist[:0], p.hist[drop:]...)
	p.hist = append(p.hist, cursorSample{now, x, y})
}

func (p *pointerMemory) freeze(now time.Time) {
	if p.frozenAt.IsZero() {
		p.frozenAt = now
	}
}

func (p *pointerMemory) rollback(inj Injector, hand int, now time.Time, back time.Duration) (x, y int, ok bool) {
	if back <= 0 || p.hand != hand || p.frozenAt.IsZero() || now.Sub(p.frozenAt) > rollbackValidFor || len(p.hist) == 0 {
		return 0, 0, false
	}
	last := p.hist[len(p.hist)-1]
	cx, cy := inj.CursorPos()
	if math.Abs(float64(cx-last.x)) > rollbackMaxJump || math.Abs(float64(cy-last.y)) > rollbackMaxJump {
		return 0, 0, false
	}
	target := p.frozenAt.Add(-back)
	pick := p.hist[0]
	for _, s := range p.hist {
		if s.t.After(target) {
			break
		}
		pick = s
	}
	if pick.x == cx && pick.y == cy {
		return 0, 0, false
	}
	return pick.x, pick.y, true
}

// pointerCtl moves the cursor with the hand, optionally holding a mouse
// button for dragging.
type pointerCtl struct {
	m           motion
	hand        int
	source      string
	button      string
	accX, accY  float64
	sensitivity float64
}

func (c *pointerCtl) start(e *Engine, st *gesture.HandState, now time.Time) {
	x, y := e.inj.CursorPos()
	e.ptr = pointerMemory{hand: c.hand}
	e.ptr.record(now, x, y)
	if c.button != "" {
		e.buttonDown(c.button)
	}
}

func (c *pointerCtl) update(e *Engine, st *gesture.HandState, now time.Time, live bool) {
	if !live {
		c.m.rebase()
		c.accX, c.accY = 0, 0
		e.ptr.freeze(now)
		return
	}
	e.ptr.frozenAt = time.Time{}
	s := &e.settings.Pointer
	px, py := float64(st.Features.PalmX), float64(st.Features.PalmY)
	if c.source != "palm" {
		tip := st.Landmarks[vision.IndexTip]
		px, py = float64(tip.X), float64(tip.Y)
	}
	dx, dy, speed, ok := c.m.step(s, px, py, float64(st.Features.PalmSize), now)
	if !ok {
		return
	}
	_, _, _, _, primary := e.inj.Desktop()
	gain := s.GainLow
	if s.SpeedHigh > s.SpeedLow {
		gain += (s.GainHigh - s.GainLow) * smoothstep((speed-s.SpeedLow)/(s.SpeedHigh-s.SpeedLow))
	}
	base := s.LeftSensitivity
	if st.Side == gesture.SideRight {
		base = s.RightSensitivity
	}
	if c.button != "" {
		base = s.DragSensitivity
	}
	gain *= float64(primary) * c.sensitivity * legacyGain(base)
	c.accX += dx * gain
	c.accY += dy * gain
	dead := s.DeadZone
	if s.SpeedLow > 0 {
		dead *= 1 - math.Min(1, speed/s.SpeedLow)
	}
	if math.Hypot(c.accX, c.accY) < dead {
		// Let the remainder fade, so that jitter cannot add up to a move.
		fade := math.Exp(-c.m.dt / 0.1)
		c.accX *= fade
		c.accY *= fade
		return
	}
	mx, my := math.Trunc(c.accX), math.Trunc(c.accY)
	if mx == 0 && my == 0 {
		return
	}
	c.accX -= mx
	c.accY -= my
	cx, cy := e.inj.CursorPos()
	bx, by, bw, bh, _ := e.inj.Desktop()
	nx := clampInt(cx+int(mx), bx, bx+bw-1)
	ny := clampInt(cy+int(my), by, by+bh-1)
	if nx != cx || ny != cy {
		e.inj.MoveCursor(nx, ny)
	}
	e.ptr.record(now, nx, ny)
}

func (c *pointerCtl) stop(e *Engine, now time.Time, reason stopReason) {
	if c.button != "" {
		e.buttonUp(c.button)
	}
	e.ptr.freeze(now)
}

func (c *pointerCtl) expired(*Engine, time.Time) bool { return false }

func clampInt(v, lo, hi int) int {
	if hi < lo {
		return v
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

type followKind int

const (
	followScroll followKind = iota
	followZoom
	followVolume
)

// followCtl turns palm movement into wheel turns, zoom steps or volume steps.
// Output stops as soon as the hand stops: nothing is carried on by inertia.
type followCtl struct {
	sensitivity float64
	m           motion
	kind        followKind
	horizontal  bool
	natural     bool
	travel      float64 // palm lengths moved since tracking (re)started
	engaged     bool
	acc         float64 // output not yet sent: wheel units, or steps
}

func (c *followCtl) start(*Engine, *gesture.HandState, time.Time) {}

func (c *followCtl) update(e *Engine, st *gesture.HandState, now time.Time, live bool) {
	if !live {
		c.m.rebase()
		c.travel, c.engaged, c.acc = 0, false, 0
		return
	}
	s := &e.settings.Scroll
	dx, dy, speed, ok := c.m.step(&e.settings.Pointer, float64(st.Features.PalmX), float64(st.Features.PalmY), float64(st.Features.PalmSize), now)
	if !ok {
		return
	}
	// Moving the hand up or to the right counts as positive.
	amount := -dy
	if c.horizontal {
		amount = dx
	}
	if !c.engaged {
		c.travel += amount
		if math.Abs(c.travel) < s.DeadZone {
			return
		}
		c.engaged = true
	}
	if speed < s.MinSpeed {
		return
	}
	if c.natural {
		amount = -amount
	}
	switch c.kind {
	case followScroll:
		step := s.Step
		if step <= 0 {
			step = wheelNotch
		}
		c.acc += amount * s.Speed * wheelNotch * legacyGain(c.sensitivity)
		if limit := s.MaxPerFrame * wheelNotch; limit > 0 {
			c.acc = math.Max(-limit, math.Min(limit, c.acc))
		}
		if n := int(c.acc / float64(step)); n != 0 {
			c.acc -= float64(n * step)
			e.inj.Wheel(n*step, c.horizontal)
		}
	case followZoom:
		c.acc += amount * s.ZoomSpeed * legacyGain(c.sensitivity)
		// An implausible landmark jump must not produce hundreds of zoom or
		// volume events. Drop excess rather than carrying it into later frames.
		c.acc = math.Max(-2, math.Min(2, c.acc))
		if n := int(c.acc); n != 0 {
			c.acc -= float64(n)
			e.keyDown(vkCtrl)
			e.inj.Wheel(n*wheelNotch, false)
			e.keyUp(vkCtrl)
		}
	case followVolume:
		c.acc += amount * s.VolumeSpeed
		c.acc = math.Max(-2, math.Min(2, c.acc))
		for ; c.acc >= 1; c.acc-- {
			e.tap(vkVolumeUp)
		}
		for ; c.acc <= -1; c.acc++ {
			e.tap(vkVolumeDown)
		}
	}
}

func (c *followCtl) stop(*Engine, time.Time, stopReason) {}
func (c *followCtl) expired(*Engine, time.Time) bool     { return false }

// switcherCtl drives the Alt+Tab window switcher: Alt stays down while the
// palm's sideways movement steps through the windows. Withdrawing the hand
// confirms the choice; finish bindings confirm or cancel explicitly, while
// pause, abort and timeout cancel.
type switcherCtl struct {
	sensitivity float64
	m           motion
	acc         float64 // palm lengths moved towards the next step
	back        float64 // movement against acc since it last grew
	lastStep    time.Time
	done        bool
}

// A movement against the current direction of at least this share of a step
// counts as a change of direction and restarts the count.
const switcherReverse = 0.2

func (c *switcherCtl) start(e *Engine, st *gesture.HandState, now time.Time) {
	e.keyDown(vkAlt)
	if e.settings.Switcher.InitialStep {
		e.tap(vkTab)
	}
	c.lastStep = now
}

func (c *switcherCtl) update(e *Engine, st *gesture.HandState, now time.Time, live bool) {
	if !live {
		c.m.rebase()
		return
	}
	s := &e.settings.Switcher
	dx, _, _, ok := c.m.step(&e.settings.Pointer, float64(st.Features.PalmX), float64(st.Features.PalmY), float64(st.Features.PalmSize), now)
	if !ok || s.StepDistance <= 0 {
		return
	}
	dx *= legacyGain(c.sensitivity)
	if c.acc != 0 && (dx > 0) != (c.acc > 0) {
		c.back += math.Abs(dx)
		if c.back >= switcherReverse*s.StepDistance {
			c.acc = math.Copysign(c.back, dx)
			c.back = 0
		}
		return
	}
	c.back = 0
	c.acc += dx
	if math.Abs(c.acc) < s.StepDistance || now.Sub(c.lastStep) < ms(s.MinStepMs) {
		return
	}
	if c.acc > 0 {
		e.tap(vkTab)
		c.acc -= s.StepDistance
	} else {
		e.tap(vkShift, vkTab)
		c.acc += s.StepDistance
	}
	c.lastStep = now
}

// Zero was the implicit value in settings written before base multipliers.
func legacyGain(value float64) float64 {
	if value == 0 {
		return 1
	}
	return value
}

func (c *switcherCtl) stop(e *Engine, now time.Time, reason stopReason) {
	if c.done {
		return
	}
	c.done = true
	if reason != stopLost && reason != stopConfirm {
		// Esc while Alt is down closes the switcher without changing window.
		e.tap(vkEsc)
	}
	e.keyUp(vkAlt)
}

func (c *switcherCtl) expired(e *Engine, now time.Time) bool {
	timeout := e.settings.Switcher.TimeoutMs
	return timeout > 0 && now.Sub(c.lastStep) > ms(timeout)
}

// keyHoldCtl keeps keys down while the pose is held.
type keyHoldCtl struct {
	keys []uint16
	down bool
}

func (c *keyHoldCtl) start(e *Engine, st *gesture.HandState, now time.Time) {
	for _, k := range c.keys {
		e.keyDown(k)
	}
	c.down = true
}

func (c *keyHoldCtl) update(*Engine, *gesture.HandState, time.Time, bool) {}

func (c *keyHoldCtl) stop(e *Engine, now time.Time, reason stopReason) {
	if !c.down {
		return
	}
	c.down = false
	for i := len(c.keys) - 1; i >= 0; i-- {
		e.keyUp(c.keys[i])
	}
}

func (c *keyHoldCtl) expired(*Engine, time.Time) bool { return false }
