package control

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"
)

var errBusy = errors.New("driver still reading")

type testCamera struct {
	testSource
	closes int
	// busy is how many attempts to close fail before the driver lets go.
	busy int
}

func (c *testCamera) CloseContext(context.Context) error {
	if c.closes++; c.closes <= c.busy {
		return errBusy
	}
	return nil
}

func TestRunRecoveringOpensTheCameraAgain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lost := errors.New("device invalidated")
	failed := &testCamera{testSource: testSource{err: lost}, busy: 2}
	again := &testCamera{}
	again.next = func() { again.stamp = time.Now() }
	opens, frames, recovered := 0, 0, 0
	var reported error
	var retries []string
	cam, err := RunRecovering(ctx, failed, &testDetector{}, newSettled(t, &recordingInput{}), RunOptions{OnFrame: func(FrameResult) {
		if frames++; frames == 2 {
			cancel()
		}
	}}, CameraRecovery{
		Delays: []time.Duration{time.Millisecond},
		Open: func() (Camera, error) {
			if opens++; opens == 1 {
				return nil, errors.New("not plugged in")
			}
			return again, nil
		},
		OnLost: func(err error) { reported = err },
		OnRetry: func(attempt int, err error) {
			retries = append(retries, fmt.Sprintf("%d %v after %d opens", attempt, err, opens))
		},
		OnRecovered: func(c Camera) {
			if c == again {
				recovered++
			}
		},
	})
	// The device is not opened again while the failed camera still holds it.
	want := []string{"1 driver still reading after 0 opens", "2 not plugged in after 1 opens"}
	if err != nil || cam != again || !errors.Is(reported, lost) || failed.closes != 3 || !slices.Equal(retries, want) || recovered != 1 || frames != 2 {
		t.Fatalf("returned %v, %v; lost %v; closes %d; retries %q; recovered %d; frames %d", cam, err, reported, failed.closes, retries, recovered, frames)
	}
}

func TestRunRecoveringStopsOnTrackingFailure(t *testing.T) {
	cam := &testCamera{testSource: testSource{stamp: time.Now()}}
	opens := 0
	got, err := RunRecovering(context.Background(), cam, &testDetector{err: errors.New("model failed")}, newSettled(t, &recordingInput{}), RunOptions{}, CameraRecovery{
		Delays: []time.Duration{time.Millisecond},
		Open:   func() (Camera, error) { opens++; return cam, nil },
	})
	if !errors.Is(err, ErrTracking) || got != cam || opens != 0 || cam.closes != 0 {
		t.Fatalf("tracking failure: %v, camera %v, opens %d, closes %d", err, got, opens, cam.closes)
	}
}

func TestRunRecoveringStopsWaitingWhenCancelled(t *testing.T) {
	for _, busy := range []int{0, 5} {
		ctx, cancel := context.WithCancel(context.Background())
		failed := &testCamera{testSource: testSource{err: errors.New("unplugged")}, busy: busy}
		opens := 0
		started := time.Now()
		got, err := RunRecovering(ctx, failed, &testDetector{}, newSettled(t, &recordingInput{}), RunOptions{}, CameraRecovery{
			Delays: []time.Duration{time.Hour},
			Open:   func() (Camera, error) { opens++; return nil, errors.New("opened after cancel") },
			OnLost: func(error) { cancel() },
		})
		cancel()
		// A camera that has not let go goes back to the caller to close.
		var want Camera
		if busy > 0 {
			want = failed
		}
		if err != nil || got != want || opens != 0 || time.Since(started) > time.Second {
			t.Errorf("busy %d: returned %v, %v after %s with %d opens", busy, got, err, time.Since(started), opens)
		}
	}
}
