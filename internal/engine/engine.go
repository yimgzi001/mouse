// Package engine connects recognised gestures to actions. Which gesture does
// what is data (a list of bindings), so it can be changed without touching the
// program.
package engine

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"gesturectl/internal/gesture"
)

// Injector sends input to the operating system. Coordinates are pixels of the
// whole desktop.
type Injector interface {
	CursorPos() (x, y int)
	MoveCursor(x, y int)
	// Desktop returns the bounds of the whole desktop and the height of the
	// primary screen.
	Desktop() (x, y, w, h, primaryHeight int)
	// MouseButton presses or releases "left", "right", "middle", "x1" or "x2".
	MouseButton(button string, down bool)
	// Wheel turns the wheel; 120 is one notch, positive is up or right.
	Wheel(delta int, horizontal bool)
	Key(vk uint16, down bool)
	Text(text string)
	LockScreen()
	Open(target string) error
}

// PointerSettings tune how hand movement becomes cursor movement. Speeds are
// in palm lengths per second.
type PointerSettings struct {
	LeftSensitivity  float64 `json:"leftSensitivity"`
	RightSensitivity float64 `json:"rightSensitivity"`
	DragSensitivity  float64 `json:"dragSensitivity"`
	// GainLow and GainHigh say how far the cursor travels, in heights of the
	// primary screen, when the hand moves one palm length slowly or fast.
	GainLow  float64 `json:"gainLow"`
	GainHigh float64 `json:"gainHigh"`
	// Between SpeedLow and SpeedHigh the gain rises smoothly from low to high.
	SpeedLow  float64 `json:"speedLow"`
	SpeedHigh float64 `json:"speedHigh"`
	// MinCutoff, Beta and DCutoff are the One Euro Filter parameters.
	MinCutoff float64 `json:"minCutoff"`
	Beta      float64 `json:"beta"`
	DCutoff   float64 `json:"dCutoff"`
	// DeadZone is the cursor movement in pixels that a resting hand must exceed
	// before the cursor moves.
	DeadZone float64 `json:"deadZone"`
	// ClickRollbackMs: when the hand that moves the cursor clicks, the cursor
	// returns to where it was this long before the finger started to move.
	ClickRollbackMs int `json:"clickRollbackMs"`
}

// ScrollSettings tune the actions that follow the palm.
type ScrollSettings struct {
	// Speed is the number of wheel notches per palm length of movement.
	Speed float64 `json:"speed"`
	// DeadZone is how far the palm must move, in palm lengths, before scrolling starts.
	DeadZone float64 `json:"deadZone"`
	// MinSpeed is the palm speed below which movement is ignored.
	MinSpeed float64 `json:"minSpeed"`
	// Step is the size of one wheel event; 120 is a full notch.
	Step int `json:"step"`
	// MaxPerFrame limits the notches sent for one camera frame.
	MaxPerFrame float64 `json:"maxPerFrame"`
	// ZoomSpeed is the number of zoom steps per palm length.
	ZoomSpeed float64 `json:"zoomSpeed"`
	// VolumeSpeed is the number of volume steps per palm length.
	VolumeSpeed float64 `json:"volumeSpeed"`
}

// SwitcherSettings tune the window switcher.
type SwitcherSettings struct {
	// StepDistance is the sideways palm movement, in palm lengths, per window.
	StepDistance float64 `json:"stepDistance"`
	// MinStepMs is the least time between two steps.
	MinStepMs int `json:"minStepMs"`
	// TimeoutMs cancels the switcher when nothing was selected for this long.
	TimeoutMs int `json:"timeoutMs"`
	// InitialStep moves to the previous window as soon as the switcher opens,
	// as Alt+Tab does.
	InitialStep bool `json:"initialStep"`
}

// Settings are the engine's settings.
type Settings struct {
	Pointer  PointerSettings  `json:"pointer"`
	Scroll   ScrollSettings   `json:"scroll"`
	Switcher SwitcherSettings `json:"switcher"`
	PairZoom PairZoomSettings `json:"pairZoom"`
	// NewHandMs is how long a newly reported hand may only move the pointer.
	// Hands of people passing behind the user, or of a hand coming into view,
	// show a pose only for a moment.
	NewHandMs int `json:"newHandMs"`
	// AllowOpen permits the "open" action.
	AllowOpen bool `json:"-"`
}

// DefaultSettings returns the default engine settings.
func DefaultSettings() Settings {
	return Settings{
		Pointer: PointerSettings{
			LeftSensitivity: 1, RightSensitivity: 1, DragSensitivity: 1,
			GainLow: 0.5, GainHigh: 2.0, SpeedLow: 0.3, SpeedHigh: 2.0,
			MinCutoff: 1.0, Beta: 4.0, DCutoff: 1.0, DeadZone: 2, ClickRollbackMs: 80,
		},
		Scroll:   ScrollSettings{Speed: 8, DeadZone: 0.06, MinSpeed: 0.15, Step: 120, MaxPerFrame: 6, ZoomSpeed: 4, VolumeSpeed: 10},
		Switcher: SwitcherSettings{StepDistance: 0.45, MinStepMs: 120, TimeoutMs: 8000, InitialStep: true},
	}
}

// Event records something the engine did, for display.
type Event struct {
	Seq     uint64    `json:"seq"`
	Time    time.Time `json:"time"`
	Hand    string    `json:"hand"`
	Pose    string    `json:"pose"`
	Binding string    `json:"binding"`
	Action  string    `json:"action"`
	// What is "fire", "start", "end", "confirm" or "cancel".
	What string `json:"what"`
}

type binding struct {
	Binding
	spec *ActionSpec
	keys [][]uint16
	// shadowed[side] is true for an "any hand" binding when a binding for that
	// particular hand has the same pose and trigger.
	shadowed [3]bool
}

func (b *binding) str(key string) string {
	s, _ := b.Action.Params[key].(string)
	return s
}

func (b *binding) num(key string) float64 {
	n, _ := b.Action.Params[key].(float64)
	return n
}

func (b *binding) flag(key string) bool {
	v, _ := b.Action.Params[key].(bool)
	return v
}

type mode struct {
	b   *binding
	ctl controller
}

type hand struct {
	id          int
	side        gesture.Side
	pose        string
	since       time.Time
	born        time.Time
	fired       map[*binding]bool
	next        map[*binding]time.Time
	mode        *mode
	missing     bool
	blockedPose string
	releaseTo   []string
	releaseFrom string
	rawPose     string
	rawSince    time.Time
	consumed    map[string]bool
}

const maxEvents = 60

// Engine turns hand states into actions according to its bindings.
type Engine struct {
	mu          sync.Mutex
	inj         Injector
	settings    Settings
	bindings    []*binding
	hands       map[int]*hand
	paused      bool
	heldKeys    map[uint16]int
	heldButtons map[string]bool
	ptr         pointerMemory
	events      []Event
	seq         uint64
	onPause     func(paused bool)
	pair        *pairZoom
	// pausedPair retains only composite trigger latches, never held inputs.
	pausedPair     *pairZoom
	lastQuickCycle time.Time
	frameTime      time.Time
}

// New creates an engine without bindings.
func New(inj Injector) *Engine {
	return &Engine{
		inj:         inj,
		settings:    DefaultSettings(),
		hands:       map[int]*hand{},
		heldKeys:    map[uint16]int{},
		heldButtons: map[string]bool{},
	}
}

// OnPause sets a function that is called when pausing is switched on or off.
// It runs while the engine is busy and must return quickly.
func (e *Engine) OnPause(f func(paused bool)) {
	e.mu.Lock()
	e.onPause = f
	e.mu.Unlock()
}

// Configure replaces settings and bindings. Running actions are stopped.
// Disabled bindings are checked but not used.
func (e *Engine) Configure(settings Settings, bindings []Binding, hasPose func(string) bool) error {
	var compiled []*binding
	seen := map[string]bool{}
	for _, src := range bindings {
		b := &binding{Binding: src}
		if err := b.Normalize(hasPose); err != nil {
			return err
		}
		if seen[b.ID] {
			return fmt.Errorf("binding id %q is used twice", b.ID)
		}
		seen[b.ID] = true
		if !b.Enabled {
			continue
		}
		b.spec = findSpec(b.Action.Type)
		if keys := b.str("keys"); keys != "" {
			b.keys, _ = ParseKeys(keys)
		}
		compiled = append(compiled, b)
	}
	for _, b := range compiled {
		if b.Hand != "any" {
			continue
		}
		for _, other := range compiled {
			if other.Pose != b.Pose || other.Trigger != b.Trigger {
				continue
			}
			switch other.Hand {
			case "left":
				b.shadowed[gesture.SideLeft] = true
			case "right":
				b.shadowed[gesture.SideRight] = true
			}
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopAll(time.Now())
	for _, h := range e.hands {
		h.fired = map[*binding]bool{}
		h.next = map[*binding]time.Time{}
	}
	e.settings, e.bindings = settings, compiled
	return nil
}

// ActivePoses returns the ids of the poses that enabled bindings use.
func (e *Engine) ActivePoses() map[string]bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	active := map[string]bool{}
	for _, b := range e.bindings {
		active[b.Pose] = true
	}
	return active
}

// UsesSwipes reports whether any enabled binding has a swipe trigger.
func (e *Engine) UsesSwipes() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, b := range e.bindings {
		if len(b.Trigger) > 6 && b.Trigger[:6] == "swipe_" {
			return true
		}
	}
	return false
}

// Paused reports whether gesture control is paused.
func (e *Engine) Paused() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.paused
}

// SetPaused pauses or resumes gesture control. Pausing ends running actions
// and releases every key and button the engine holds.
func (e *Engine) SetPaused(paused bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.setPaused(paused, time.Now())
}

func (e *Engine) setPaused(paused bool, now time.Time) {
	if e.paused == paused {
		return
	}
	if paused {
		pair := e.pair
		e.stopAll(now)
		if pair != nil {
			pair.have, pair.acc, pair.since = false, 0, time.Time{}
			for i := range pair.bindings {
				if pair.bindings[i].b.spec.Continuous {
					pair.bindings[i].active, pair.bindings[i].fired = false, false
				}
			}
			e.pausedPair = pair
		}
	} else {
		e.pausedPair = nil
	}
	e.paused = paused
	if e.onPause != nil {
		e.onPause(paused)
	}
}

// Abort ends running actions, releases everything held and forgets all hands.
// It is for camera failures and shutdown.
func (e *Engine) Abort() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopAll(time.Now())
	e.hands = map[int]*hand{}
}

// Events returns the events with a sequence number above after.
func (e *Engine) Events(after uint64) []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Event
	for _, ev := range e.events {
		if ev.Seq > after {
			out = append(out, ev)
		}
	}
	return out
}

// Running returns, per hand id, the name of the binding whose continuous
// action is in effect.
func (e *Engine) Running() map[int]string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[int]string{}
	if e.pair != nil {
		name := e.pair.name()
		if e.pair.consumed {
			name += "（等待释放）"
		} else if !e.pair.active() {
			name += "（等待稳定）"
		}
		out[e.pair.left], out[e.pair.right] = name, name
	}
	for id, h := range e.hands {
		if h.mode != nil {
			out[id] = h.mode.b.Name
		}
	}
	return out
}

func (e *Engine) record(h *hand, b *binding, what string, now time.Time) {
	e.appendEvent(Event{Time: now, Hand: h.side.String(), Pose: b.Pose, Binding: b.Name, Action: b.Action.Type, What: what})
}

func (e *Engine) appendEvent(ev Event) {
	e.seq++
	ev.Seq = e.seq
	e.events = append(e.events, ev)
	if len(e.events) > maxEvents {
		e.events = append(e.events[:0], e.events[len(e.events)-maxEvents:]...)
	}
}

// Process applies one frame of hand states.
func (e *Engine) Process(states []gesture.HandState, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.frameTime = now
	if e.paused {
		e.pair = e.pausedPair
		claimed := e.processPairZoom(states, now)
		if e.paused {
			e.pausedPair, e.pair = e.pair, nil
		}
		if claimed {
			return
		}
	}
	if !e.paused && e.exclusiveHand() == nil && e.processPairZoom(states, now) {
		return
	}
	if e.quickSwipe(states, now) {
		return
	}
	for i := range states {
		st := &states[i]
		h := e.hands[st.ID]
		if h == nil {
			if st.Lost || !st.Present {
				continue
			}
			h = &hand{id: st.ID, since: now, born: now, fired: map[*binding]bool{}, next: map[*binding]time.Time{}}
			e.hands[st.ID] = h
		}
		if st.Side != gesture.SideUnknown {
			if h.side != gesture.SideUnknown && h.side != st.Side {
				if h.mode != nil && !e.matches(h.mode.b, h.mode.b.Pose, st.Side) {
					e.stopMode(h, now, stopCancel)
				}
				// New side-specific bindings need a new baseline/confirmation;
				// keep click latches so an identity fluctuation cannot repeat a click.
				h.since = now
			}
			h.side = st.Side
		}
		switch {
		case st.Lost:
			e.leavePose(h, st, now, stopLost)
			delete(e.hands, st.ID)
		case !st.Present:
			h.missing = true
			if h.mode != nil {
				h.mode.ctl.update(e, st, now, false)
			}
		default:
			if h.missing {
				// Keep the one-click latch, but require a fresh confirmation
				// when a candidate hand reappears after a brief dropout.
				h.since, h.missing = now, false
			}
			if st.Side == gesture.SideUnknown || st.RawPose == "" {
				h.rawPose, h.rawSince = "", now
			} else if st.RawPose != h.rawPose {
				h.rawPose, h.rawSince = st.RawPose, now
			}
			if st.Pose != h.pose && st.Pose != "" && st.RawPose == st.Pose && st.Side != gesture.SideUnknown {
				e.leavePose(h, st, now, stopPose)
				h.pose, h.since = st.Pose, now
				clear(h.fired)
				clear(h.next)
			}
			h.promoteReturn(st)
			if st.Pose != "" && st.RawPose == st.Pose && st.Side != gesture.SideUnknown {
				for pose := range h.consumed {
					if pose != st.Pose {
						delete(h.consumed, pose)
					}
				}
			}
			e.evaluate(h, st, now)
		}
	}
	// Hands that are no longer reported at all, for instance after the
	// recognizer was reset.
	for id, h := range e.hands {
		reported := false
		for i := range states {
			if states[i].ID == id {
				reported = true
				break
			}
		}
		if !reported {
			if h.mode != nil {
				e.stopMode(h, now, stopCancel)
			}
			delete(e.hands, id)
		}
	}
}

// exclusiveHand returns the hand that runs an exclusive action, or nil.
func (e *Engine) exclusiveHand() *hand {
	for _, h := range e.hands {
		if h.mode != nil && h.mode.b.spec.Exclusive {
			return h
		}
	}
	return nil
}

func (e *Engine) matches(b *binding, pose string, side gesture.Side) bool {
	if b.Pose != pose {
		return false
	}
	switch b.Hand {
	case "both":
		return false
	case "left":
		return side == gesture.SideLeft
	case "right":
		return side == gesture.SideRight
	}
	return !b.shadowed[side]
}

// leavePose handles the end of the hand's current pose.
func (e *Engine) leavePose(h *hand, st *gesture.HandState, now time.Time, reason stopReason) {
	sticky := h.mode != nil && h.mode.b.spec.Sticky && reason == stopPose
	if h.mode != nil && !sticky {
		e.stopMode(h, now, reason)
	}
	if h.pose == "" || e.paused || h.blockedPose == h.pose || h.consumed[h.pose] {
		return
	}
	if owner := e.exclusiveHand(); owner != nil && owner != h {
		return
	}
	for _, b := range e.bindings {
		if sticky && !isFinishAction(b.Action.Type) {
			continue
		}
		if b.Trigger == "exit" && e.matches(b, h.pose, h.side) && !e.young(h, now) {
			e.fire(b, h, now)
		}
	}
}

// young reports whether h appeared too recently for actions other than moving
// the pointer. A nil h is a hand the engine has not seen before.
func (e *Engine) young(h *hand, now time.Time) bool {
	if e.settings.NewHandMs <= 0 {
		return false
	}
	return h == nil || now.Sub(h.born) < ms(e.settings.NewHandMs)
}

func (e *Engine) evaluate(h *hand, st *gesture.HandState, now time.Time) {
	if h.consumed[st.RawPose] || h.consumed[st.Pose] {
		return
	}
	if h.blockedPose != "" {
		if st.Side == gesture.SideUnknown || st.Pose == "" || st.RawPose != st.Pose || st.Pose == h.blockedPose {
			return
		}
		h.blockedPose = ""
		h.since = now
	}
	// Voting intentionally lags the current observation. A stale stable pose
	// must not finish a click candidate after the fingers have already changed.
	if st.RawPose != h.pose || st.Side == gesture.SideUnknown {
		h.since = now
	}
	held := now.Sub(h.since)
	if h.mode != nil && h.mode.b.spec.Sticky {
		for _, b := range e.bindings {
			if isFinishAction(b.Action.Type) && b.Trigger != "exit" && !isSwipe(b.Trigger) && !h.fired[b] && st.Side != gesture.SideUnknown && e.matches(b, st.RawPose, st.Side) && now.Sub(h.rawSince) >= ms(b.HoldMs) {
				h.fired[b] = true
				e.fire(b, h, now)
				return
			}
		}
		m := h.mode
		live := st.Side != gesture.SideUnknown && st.Pose == m.b.Pose && st.RawPose == st.Pose
		if m.b.Action.Type == "window_switcher" {
			// The activation binding summons the selector; thereafter its owner
			// steers by palm position despite finger classification changes.
			// Explicit finish actions run above; uncertain/lost hands still freeze.
			live = st.Side != gesture.SideUnknown
		}
		m.ctl.update(e, st, now, live)
		if m.ctl.expired(e, now) {
			e.stopMode(h, now, stopTimeout)
		}
		return
	}
	if owner := e.exclusiveHand(); owner != nil && owner != h {
		// An action held during selection must not land in the new window
		// when Alt is released. Require the other hand to release this raw gesture
		// even if its majority pose catches up after selection has closed.
		e.consumeExclusive(h, st.RawPose, st.Side)
		return
	}
	if st.Side == gesture.SideUnknown || st.RawPose != st.Pose || st.Pose == "" {
		if h.mode != nil {
			h.mode.ctl.update(e, st, now, false)
		}
		return
	}
	if h.pose != "" {
		young := e.young(h, now)
		for _, b := range e.bindings {
			if !e.matches(b, h.pose, st.Side) || young && b.Action.Type != "pointer_move" {
				continue
			}
			if e.paused && b.Action.Type != "toggle_pause" {
				continue
			}
			wasPaused := e.paused
			switch b.Trigger {
			case "enter", "long_press":
				if !h.fired[b] && held >= ms(b.HoldMs) && st.RawPose == st.Pose {
					h.fired[b] = true
					e.fire(b, h, now)
				}
			case "repeat":
				interval := ms(b.IntervalMs)
				if !h.fired[b] {
					if held >= ms(b.HoldMs) {
						h.fired[b] = true
						h.next[b] = now.Add(interval)
						e.fire(b, h, now)
					}
				} else if !now.Before(h.next[b]) {
					h.next[b] = h.next[b].Add(interval)
					if h.next[b].Before(now) {
						h.next[b] = now.Add(interval)
					}
					e.fire(b, h, now)
				}
			case "hold":
				if h.mode == nil && (!b.spec.Sticky || !h.fired[b]) && held >= ms(b.HoldMs) && st.Side != gesture.SideUnknown && st.RawPose == st.Pose && e.resourceFree(h, b) {
					e.startMode(h, b, st, now)
				}
			case "swipe_left", "swipe_right", "swipe_up", "swipe_down":
				if st.Side != gesture.SideUnknown && st.RawPose == st.Pose && st.Swipe != "" && b.Trigger == "swipe_"+st.Swipe && h.mode == nil {
					e.fire(b, h, now)
				}
			}
			if e.paused != wasPaused {
				// Pausing was switched by this binding; nothing else applies
				// to this frame.
				return
			}
		}
	}
	if h.mode != nil {
		h.mode.ctl.update(e, st, now, st.Side != gesture.SideUnknown && st.RawPose == st.Pose && e.matches(h.mode.b, st.Pose, st.Side))
		if h.mode.ctl.expired(e, now) {
			e.stopMode(h, now, stopTimeout)
		}
	}
}

func isFinishAction(action string) bool {
	return action == "stop_hand" || action == "confirm_action" || action == "cancel_action"
}

func (h *hand) pendingReturn(pose string) bool {
	if pose == h.releaseFrom {
		return false
	}
	for _, target := range h.releaseTo {
		if target == pose {
			return true
		}
	}
	return false
}

// Only a known stable pose can finish a pending return rule. Raw return
// candidates still suppress fast events without consuming or clearing it.
func (h *hand) promoteReturn(st *gesture.HandState) {
	if st.Side == gesture.SideUnknown || st.Pose == "" || st.RawPose != st.Pose || st.Pose == h.releaseFrom || len(h.releaseTo) == 0 {
		return
	}
	if h.pendingReturn(st.Pose) {
		h.blockedPose = st.Pose
	}
	h.releaseTo = nil
}

func (e *Engine) consumeExclusive(h *hand, pose string, side gesture.Side) {
	for _, b := range e.bindings {
		if requiresFreshActivation(b) && e.matches(b, pose, side) {
			h.blockedPose = pose
			return
		}
	}
}

// resourceFree reports whether no other hand runs an action that would fight
// with b, such as two hands moving the cursor.
func (e *Engine) resourceFree(h *hand, b *binding) bool {
	if b.spec.resource == "" {
		return true
	}
	for _, other := range e.hands {
		if other != h && other.mode != nil && other.mode.b.spec.resource == b.spec.resource {
			return false
		}
	}
	return true
}

func (e *Engine) startMode(h *hand, b *binding, st *gesture.HandState, now time.Time) {
	if b.spec.Exclusive {
		for _, other := range e.hands {
			if other != h {
				if other.mode != nil {
					e.stopMode(other, now, stopCancel)
				}
				pose := other.rawPose
				if pose == "" {
					pose = other.pose
				}
				e.consumeExclusive(other, pose, other.side)
			}
		}
	}
	h.mode = &mode{b: b, ctl: newController(b, h.id)}
	if b.spec.Sticky {
		// A timeout/cancel must stay closed until the activation pose is released.
		h.fired[b] = true
	}
	e.record(h, b, "start", now)
	h.mode.ctl.start(e, st, now)
}

func (e *Engine) stopMode(h *hand, now time.Time, reason stopReason) {
	m := h.mode
	if m == nil {
		return
	}
	h.mode = nil
	if reason == stopCancel && m.b.Action.Type == "drag" {
		h.blockedPose = m.b.Pose
	}
	m.ctl.stop(e, now, reason)
	what := "end"
	if m.b.spec.Sticky {
		h.fired[m.b] = true
		what = "cancel"
		if reason == stopLost || reason == stopConfirm {
			what = "confirm"
		}
	}
	e.record(h, m.b, what, now)
}

// stopAll ends every running action and releases everything held.
func (e *Engine) stopAll(now time.Time) {
	e.endPairZoom(now)
	e.pausedPair = nil
	for _, h := range e.hands {
		e.stopMode(h, now, stopCancel)
	}
	e.releaseAll()
}

func (e *Engine) fire(b *binding, h *hand, now time.Time) {
	// A click's up event must not release a button owned by another hand's
	// continuous drag. The gesture stays consumed until it is released.
	if b.Action.Type == "mouse_click" && e.heldButtons[b.str("button")] {
		return
	}
	if b.Action.Type == "window_cycle" {
		// The atomic cycle releases held inputs, but a composite once-only
		// binding keeps ownership until its input is actually released.
		pair := e.pair
		if h.id == 0 {
			e.pair = nil
		}
		e.stopAll(now)
		if h.id == 0 {
			e.pair = pair
		}
	}
	if len(b.ReleaseTo) != 0 {
		h.releaseTo = append([]string(nil), b.ReleaseTo...)
		h.releaseFrom = b.Pose
	}
	e.record(h, b, "fire", now)
	switch b.Action.Type {
	case "window_cycle":
		if b.str("direction") == "previous" {
			e.tap(vkAlt, vkShift, vkTab)
		} else {
			e.tap(vkAlt, vkTab)
		}
	case "mouse_click":
		if x, y, ok := e.ptr.rollback(e.inj, h.id, now, ms(e.settings.Pointer.ClickRollbackMs)); ok {
			e.inj.MoveCursor(x, y)
		}
		button := b.str("button")
		count, _ := strconv.Atoi(b.str("count"))
		for i := 0; i < count; i++ {
			e.inj.MouseButton(button, true)
			e.inj.MouseButton(button, false)
		}
	case "mouse_down":
		e.buttonDown(b.str("button"))
	case "mouse_up":
		button := b.str("button")
		delete(e.heldButtons, button)
		e.inj.MouseButton(button, false)
	case "scroll_step":
		delta := int(b.num("amount")) * wheelNotch
		switch b.str("direction") {
		case "up":
			e.inj.Wheel(delta, false)
		case "down":
			e.inj.Wheel(-delta, false)
		case "left":
			e.inj.Wheel(-delta, true)
		case "right":
			e.inj.Wheel(delta, true)
		}
	case "zoom_step":
		delta := int(b.num("amount")) * wheelNotch
		if b.str("direction") == "out" {
			delta = -delta
		}
		e.keyDown(vkCtrl)
		e.inj.Wheel(delta, false)
		e.keyUp(vkCtrl)
	case "key_combo":
		for _, combo := range b.keys {
			e.tap(combo...)
		}
	case "type_text":
		if text := b.str("text"); text != "" {
			e.inj.Text(text)
		}
	case "media_key":
		if code, ok := keyCodes[b.str("key")]; ok {
			e.tap(code)
		}
	case "toggle_pause":
		e.setPaused(!e.paused, now)
	case "stop_hand", "cancel_action", "confirm_action":
		reason := stopCancel
		if b.Action.Type == "confirm_action" {
			reason = stopConfirm
		}
		if h.id == 0 {
			for _, other := range e.hands {
				e.stopMode(other, now, reason)
			}
		} else {
			e.stopMode(h, now, reason)
		}
	case "lock_screen":
		e.inj.LockScreen()
	case "open":
		if target := b.str("target"); e.settings.AllowOpen && target != "" {
			_ = e.inj.Open(target)
		}
	}
}

func (e *Engine) keyDown(vk uint16) {
	e.heldKeys[vk]++
	if e.heldKeys[vk] == 1 {
		e.inj.Key(vk, true)
	}
}

func (e *Engine) keyUp(vk uint16) {
	switch n := e.heldKeys[vk]; {
	case n > 1:
		e.heldKeys[vk] = n - 1
	case n == 1:
		delete(e.heldKeys, vk)
		e.inj.Key(vk, false)
	}
}

// tap presses the keys in order and releases them in reverse order.
func (e *Engine) tap(keys ...uint16) {
	for _, k := range keys {
		e.keyDown(k)
	}
	for i := len(keys) - 1; i >= 0; i-- {
		e.keyUp(keys[i])
	}
}

func (e *Engine) buttonDown(button string) {
	if !e.heldButtons[button] {
		e.heldButtons[button] = true
		e.inj.MouseButton(button, true)
	}
}

func (e *Engine) buttonUp(button string) {
	if e.heldButtons[button] {
		delete(e.heldButtons, button)
		e.inj.MouseButton(button, false)
	}
}

// releaseAll lets go of every key and button the engine holds.
func (e *Engine) releaseAll() {
	for vk := range e.heldKeys {
		e.inj.Key(vk, false)
	}
	clear(e.heldKeys)
	for button := range e.heldButtons {
		e.inj.MouseButton(button, false)
	}
	clear(e.heldButtons)
}

func ms(v int) time.Duration { return time.Duration(v) * time.Millisecond }
