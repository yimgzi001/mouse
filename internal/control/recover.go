package control

import (
	"context"
	"errors"
	"time"
)

// ErrTracking marks a failure of the hand tracker, which opening the camera
// again does not mend.
var ErrTracking = errors.New("hand tracking")

// Camera is a frame source that can be closed. A camera whose native read is
// stuck takes a while to close and must not be opened again until it has.
type Camera interface {
	FrameSource
	CloseContext(context.Context) error
}

// CameraRecovery says how RunRecovering brings a failed camera back.
type CameraRecovery struct {
	Open func() (Camera, error)
	// Delays are the pauses before successive attempts to bring the camera
	// back; the last one repeats. The default is 1, 2 and then 5 seconds.
	Delays []time.Duration
	// CloseTimeout bounds each wait for a failed camera to let go of the
	// device; the default is 2 seconds.
	CloseTimeout time.Duration
	// OnLost is told why the camera stopped. Input has been released by then.
	OnLost func(error)
	// OnRetry is told of each attempt that did not bring the camera back.
	OnRetry func(attempt int, err error)
	// OnRecovered is called with the camera that was opened again.
	OnRecovered func(Camera)
}

// RunRecovering runs Run and, whenever the camera fails, closes it and opens
// it again until ctx ends. A failure of the hand tracker still ends it. It
// returns the camera it holds, nil if none, for the caller to close.
func RunRecovering(ctx context.Context, cam Camera, det Detector, c *Controller, opt RunOptions, rec CameraRecovery) (Camera, error) {
	if len(rec.Delays) == 0 {
		rec.Delays = []time.Duration{time.Second, 2 * time.Second, 5 * time.Second}
	}
	if rec.CloseTimeout <= 0 {
		rec.CloseTimeout = 2 * time.Second
	}
	for {
		err := Run(ctx, cam, det, c, opt)
		if err == nil || ctx.Err() != nil || errors.Is(err, ErrTracking) || rec.Open == nil {
			return cam, err
		}
		if rec.OnLost != nil {
			rec.OnLost(err)
		}
		failed := cam
		closeFailed := func() error {
			closing, stop := context.WithTimeout(ctx, rec.CloseTimeout)
			defer stop()
			err := failed.CloseContext(closing)
			if err == nil {
				failed = nil
			}
			return err
		}
		closeFailed()
		cam = nil
		for attempt := 1; cam == nil; attempt++ {
			wait := time.NewTimer(rec.Delays[min(attempt, len(rec.Delays))-1])
			select {
			case <-ctx.Done():
				wait.Stop()
				return failed, nil
			case <-wait.C:
			}
			if failed != nil {
				err = closeFailed()
			}
			if failed == nil {
				var next Camera
				if next, err = rec.Open(); err == nil {
					cam = next
					break
				}
			}
			if rec.OnRetry != nil {
				rec.OnRetry(attempt, err)
			}
		}
		if rec.OnRecovered != nil {
			rec.OnRecovered(cam)
		}
	}
}
