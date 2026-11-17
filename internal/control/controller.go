// Package control connects landmarks, gesture recognition and desktop actions.
package control

import (
	"fmt"
	"math"
	"sync"
	"time"

	"gesturectl/internal/engine"
	"gesturectl/internal/gesture"
	"gesturectl/internal/vision"
)

// Controller serializes input processing with pause and shutdown requests.
type Controller struct {
	mu            sync.Mutex
	recognizer    *gesture.Recognizer
	engine        *engine.Engine
	swapHands     bool
	lastEvent     uint64
	preferences   Preferences
	poses         *gesture.PoseSet
	injector      engine.Injector
	configuration Configuration
	lastStates    []gesture.HandState
}

// New enables the default desktop gestures. Sensitivity scales pointer gain.
func New(inj engine.Injector, sensitivity float64) (*Controller, error) {
	if sensitivity <= 0 || math.IsNaN(sensitivity) || math.IsInf(sensitivity, 0) {
		return nil, fmt.Errorf("sensitivity must be finite and greater than zero")
	}
	p := DefaultPreferences()
	p.MoveLeft, p.MoveRight = sensitivity, sensitivity
	return NewWithPreferences(inj, p)
}

func NewWithPreferences(inj engine.Injector, p Preferences) (*Controller, error) {
	return NewWithConfiguration(inj, DefaultConfiguration(p))
}

func NewWithConfiguration(inj engine.Injector, cfg Configuration) (*Controller, error) {
	cfg = cfg.Clone()
	if err := cfg.Normalize(); err != nil {
		return nil, err
	}
	p := cfg.Preferences
	poses, err := gesture.NewPoseSet(gesture.BuiltinPoses())
	if err != nil {
		return nil, err
	}
	e := engine.New(inj)
	settings := p.engineSettings()
	bindings := cfg.Bindings
	if err = e.Configure(settings, bindings, poses.Has); err != nil {
		return nil, err
	}
	recognition := cfg.Recognition.settings(p)
	r := gesture.NewRecognizer(recognition, poses)
	active := make(map[string]bool, len(cfg.Gestures))
	for _, id := range cfg.Gestures {
		active[id] = true
	}
	r.Configure(recognition, poses, active)
	return &Controller{recognizer: r, engine: e, preferences: p, poses: poses, swapHands: p.SwapHands, injector: inj, configuration: cfg}, nil
}

func (c *Controller) Configuration() Configuration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.configuration.Clone()
}

// applyPrepared cannot fail: preparation and persistence have already succeeded.
// The processing lock is held only while releasing and swapping, never for disk IO.
func (c *Controller) applyPrepared(next *Controller) {
	c.mu.Lock()
	defer c.mu.Unlock()
	paused := c.engine.Paused()
	flipped := c.swapHands != next.swapHands
	next.engine.AdoptState(c.engine, c.lastStates, flipped)
	next.engine.SetPaused(paused)
	if flipped {
		c.recognizer.InvertSides()
		for n := range c.lastStates {
			if c.lastStates[n].Side == gesture.SideLeft {
				c.lastStates[n].Side = gesture.SideRight
			} else if c.lastStates[n].Side == gesture.SideRight {
				c.lastStates[n].Side = gesture.SideLeft
			}
			c.lastStates[n].LeftScore = 1 - c.lastStates[n].LeftScore
		}
	}
	active := make(map[string]bool, len(next.configuration.Gestures))
	for _, id := range next.configuration.Gestures {
		active[id] = true
	}
	c.recognizer.Configure(next.configuration.Recognition.settings(next.preferences), next.poses, active)
	c.engine, c.poses = next.engine, next.poses
	c.preferences, c.configuration, c.swapHands = next.preferences, next.configuration, next.swapHands
	c.lastEvent = 0
}

func (c *Controller) SetConfiguration(cfg Configuration) error {
	next, err := NewWithConfiguration(c.injector, cfg)
	if err != nil {
		return err
	}
	c.applyPrepared(next)
	return nil
}

// Process consumes one fresh result. Returned states can be used for logging.
func (c *Controller) Process(hands []vision.Hand, now time.Time) []gesture.HandState {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.swapHands {
		// Preserve the detector's results: callers may use them for diagnostics.
		hands = append([]vision.Hand(nil), hands...)
		for i := range hands {
			hands[i].LeftScore = 1 - hands[i].LeftScore
		}
	}
	states := c.recognizer.Update(hands, now)
	c.lastStates = append(c.lastStates[:0], states...)
	c.engine.Process(states, now)
	return append([]gesture.HandState(nil), states...)
}

// SetSwapHands corrects cameras whose image orientation reverses handedness.
// Old candidates and movement baselines must not survive a mapping change.
func (c *Controller) SetSwapHands(swap bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.swapHands == swap {
		return
	}
	c.engine.Abort()
	c.recognizer.Reset()
	c.lastStates = nil
	c.swapHands = swap
	c.preferences.SwapHands = swap
	c.configuration.Preferences.SwapHands = swap
}

func (c *Controller) Preferences() Preferences {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.preferences
}
func (c *Controller) SetPreferences(p Preferences) error {
	cfg := c.Configuration()
	cfg.Preferences = p
	return c.SetConfiguration(cfg)
}

func (c *Controller) SwapHands() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.swapHands
}

// Feedback returns active actions and new events since the previous call.
func (c *Controller) Feedback() (map[int]string, []engine.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	events := c.engine.Events(c.lastEvent)
	if len(events) > 0 {
		c.lastEvent = events[len(events)-1].Seq
	}
	return c.engine.Running(), events
}

func (c *Controller) Paused() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.engine.Paused()
}

func (c *Controller) NeedsPausedObservation() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, b := range c.configuration.Bindings {
		if b.Enabled && b.Action.Type == "toggle_pause" {
			return true
		}
	}
	return false
}

// SetPaused forgets old baselines and candidates on both transitions.
func (c *Controller) SetPaused(paused bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.engine.Paused() == paused {
		return
	}
	c.engine.SetPaused(paused)
	c.engine.Abort()
	c.recognizer.Reset()
}

// Abort releases held input and discards all tracking state.
func (c *Controller) Abort() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.engine.Abort()
	c.recognizer.Reset()
}
