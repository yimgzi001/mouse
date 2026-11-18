package control

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gesturectl/internal/camera"
	"gesturectl/internal/gesture"
	"gesturectl/internal/vision"
)

type FrameSource interface {
	Next(*vision.Frame, uint64, time.Duration) (*vision.Frame, uint64, time.Time, error)
}
type Detector interface {
	Process(*vision.Frame, time.Time) ([]vision.Hand, vision.FrameStats, error)
}

// palmSizeFilter is implemented by detectors that can ignore small, distant
// hands; Run keeps it in step with the configuration.
type palmSizeFilter interface {
	SetMinPalm(px float32)
}

type FrameResult struct {
	// Frame is borrowed until OnFrame returns. A preview must copy its pixels.
	Frame    *vision.Frame
	Sequence uint64
	Latency  time.Duration
	States   []gesture.HandState
}
type RunOptions struct {
	ObserveWhilePaused bool
	// ObservePaused is checked for each frame so a hidden preview can save CPU.
	ObservePaused func() bool
	MaxFrameAge   time.Duration
	FrameTimeout  time.Duration
	// InputError takes the native API error, if any, that the engine's void
	// methods could not return since the last call. Windows refuses input while
	// the lock screen or a UAC prompt is up, so Run then lets go of all input,
	// tells OnInputError and carries on.
	InputError   func() error
	OnInputError func(error)
	OnFrame      func(FrameResult)
}

// Run processes only fresh frames, and always aborts input before returning.
// Source and detector lifetimes belong to the caller. Timeouts keep shutdown
// responsive while allowing the camera up to FrameTimeout to deliver a frame.
func Run(ctx context.Context, src FrameSource, det Detector, c *Controller, opt RunOptions) error {
	defer c.Abort()
	if opt.MaxFrameAge <= 0 {
		opt.MaxFrameAge = 200 * time.Millisecond
	}
	if opt.FrameTimeout <= 0 {
		opt.FrameTimeout = 2 * time.Second
	}
	lastFrame := time.Now()
	var frame *vision.Frame
	var seq uint64
	filter, _ := det.(palmSizeFilter)
	minPalm := float32(-1)
	for {
		if ctx.Err() != nil {
			return nil
		}
		f, s, stamp, err := src.Next(frame, seq, 100*time.Millisecond)
		frame = f
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, camera.ErrTimeout) {
			if time.Since(lastFrame) >= opt.FrameTimeout {
				return fmt.Errorf("camera stalled: %w", err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("capture: %w", err)
		}
		seq = s
		lastFrame = time.Now()
		if stamp.IsZero() || time.Since(stamp) > opt.MaxFrameAge {
			return fmt.Errorf("camera frame is older than %s; input stopped", opt.MaxFrameAge)
		}
		if c.Paused() && !opt.ObserveWhilePaused && (opt.ObservePaused == nil || !opt.ObservePaused()) && !c.NeedsPausedObservation() {
			continue
		}
		if want := c.MinPalmPx(); filter != nil && want != minPalm {
			filter.SetMinPalm(want)
			minPalm = want
		}
		hands, _, err := det.Process(frame, stamp)
		if err != nil {
			return fmt.Errorf("hand tracking: %w", err)
		}
		if ctx.Err() != nil {
			return nil
		}
		if time.Since(stamp) > opt.MaxFrameAge {
			return fmt.Errorf("inference result is older than %s; input stopped", opt.MaxFrameAge)
		}
		states := c.Process(hands, stamp)
		if opt.InputError != nil {
			if err := opt.InputError(); err != nil {
				c.Abort()
				if opt.OnInputError != nil {
					opt.OnInputError(err)
				}
			}
		}
		if opt.OnFrame != nil {
			opt.OnFrame(FrameResult{Frame: frame, Sequence: seq, Latency: time.Since(stamp), States: states})
		}
	}
}
