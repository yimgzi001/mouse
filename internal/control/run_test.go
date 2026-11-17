package control

import (
	"context"
	"errors"
	"testing"
	"time"

	"gesturectl/internal/camera"
	"gesturectl/internal/engine"
	"gesturectl/internal/vision"
)

type testSource struct {
	stamp time.Time
	err   error
	next  func()
}

func (s *testSource) Next(*vision.Frame, uint64, time.Duration) (*vision.Frame, uint64, time.Time, error) {
	if s.next != nil {
		s.next()
	}
	return &vision.Frame{W: 1, H: 1, Pix: make([]byte, 3)}, 1, s.stamp, s.err
}

type testDetector struct {
	calls  int
	err    error
	during func()
	hands  []vision.Hand
}

func (d *testDetector) Process(*vision.Frame, time.Time) ([]vision.Hand, vision.FrameStats, error) {
	d.calls++
	if d.during != nil {
		d.during()
	}
	return d.hands, vision.FrameStats{}, d.err
}

func TestRunStopsOnCameraFailure(t *testing.T) {
	c, _ := New(&recordingInput{}, 1)
	want := errors.New("disconnected")
	err := Run(context.Background(), &testSource{err: want}, &testDetector{}, c, RunOptions{})
	if !errors.Is(err, want) {
		t.Fatalf("got %v", err)
	}
}
func TestRunDropsStaleCaptureBeforeInference(t *testing.T) {
	c, _ := New(&recordingInput{}, 1)
	det := &testDetector{}
	err := Run(context.Background(), &testSource{stamp: time.Now().Add(-time.Second)}, det, c, RunOptions{})
	if err == nil || det.calls != 0 {
		t.Fatalf("err %v, inference calls %d", err, det.calls)
	}
}
func TestRunRejectsLateInference(t *testing.T) {
	inj := &recordingInput{}
	c, _ := New(inj, 1)
	det := &testDetector{during: func() { time.Sleep(25 * time.Millisecond) }}
	err := Run(context.Background(), &testSource{stamp: time.Now()}, det, c, RunOptions{MaxFrameAge: 10 * time.Millisecond})
	if err == nil || det.calls != 1 || inj.moves != 0 || len(inj.buttons) != 0 {
		t.Fatalf("late inference: %v %+v", err, inj)
	}
}
func TestRunCancelledDuringInferenceProducesNoInput(t *testing.T) {
	inj := &recordingInput{}
	c, _ := New(inj, 1)
	ctx, cancel := context.WithCancel(context.Background())
	det := &testDetector{during: cancel, hands: []vision.Hand{referenceHand(t, "victory.jpg", .99)}}
	if err := Run(ctx, &testSource{stamp: time.Now()}, det, c, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if inj.moves != 0 || len(inj.buttons) != 0 {
		t.Fatal("input after cancellation")
	}
}
func TestRunCameraTimeoutAndDetectorFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  *testSource
		det  *testDetector
	}{
		{"timeout", &testSource{err: camera.ErrTimeout}, &testDetector{}},
		{"inference", &testSource{stamp: time.Now()}, &testDetector{err: errors.New("model failed")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := New(&recordingInput{}, 1)
			if err := Run(context.Background(), tc.src, tc.det, c, RunOptions{FrameTimeout: time.Nanosecond}); err == nil {
				t.Fatal("failure ignored")
			}
		})
	}
}

func TestRunStopsOnNativeInputFailure(t *testing.T) {
	c, _ := New(&recordingInput{}, 1)
	want := errors.New("SendInput blocked")
	err := Run(context.Background(), &testSource{stamp: time.Now()}, &testDetector{}, c, RunOptions{InputError: func() error { return want }})
	if !errors.Is(err, want) {
		t.Fatalf("native error lost: %v", err)
	}
}

func TestPausedPreviewReceivesFramesWithoutSendingInput(t *testing.T) {
	inj := &recordingInput{}
	c, _ := New(inj, 1)
	c.SetPaused(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	det := &testDetector{hands: []vision.Hand{referenceHand(t, "pointing_up.jpg", .99)}}
	seen := false
	err := Run(ctx, &testSource{stamp: time.Now()}, det, c, RunOptions{ObserveWhilePaused: true, OnFrame: func(result FrameResult) {
		seen = result.Frame != nil && len(result.States) == 1
		cancel()
	}})
	if err != nil || !seen || det.calls != 1 || inj.moves != 0 || len(inj.buttons) != 0 {
		t.Fatalf("paused preview: seen=%t calls=%d err=%v input=%+v", seen, det.calls, err, inj)
	}
}

func TestPausedHiddenPreviewSkipsInferenceUntilShown(t *testing.T) {
	c, _ := New(&recordingInput{}, 1)
	c.SetPaused(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &testSource{stamp: time.Now()}
	captures, visible := 0, false
	source.next = func() {
		captures++
		if captures == 3 {
			visible = true
		}
	}
	det := &testDetector{}
	err := Run(ctx, source, det, c, RunOptions{ObservePaused: func() bool { return visible }, OnFrame: func(FrameResult) { cancel() }})
	if err != nil || captures != 3 || det.calls != 1 {
		t.Fatalf("hidden paused inference: captures=%d inference=%d err=%v", captures, det.calls, err)
	}
}

func TestPausedHiddenPreviewStillObservesConfiguredResumeGesture(t *testing.T) {
	cfg := DefaultConfiguration(DefaultPreferences())
	cfg.Bindings = append(cfg.Bindings, engine.Binding{ID: "resume", Name: "手势暂停恢复", Enabled: true, Hand: "left", Pose: "thumb_out", Trigger: "enter", Action: engine.Action{Type: "toggle_pause"}})
	c, err := NewWithConfiguration(&recordingInput{}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.SetPaused(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &testSource{stamp: time.Now()}
	captures := 0
	source.next = func() {
		captures++
		source.stamp = time.Now()
		if captures > 12 {
			cancel()
		}
	}
	det := &testDetector{hands: []vision.Hand{testHand("occcc", 0, 0)}}
	err = Run(ctx, source, det, c, RunOptions{OnFrame: func(FrameResult) {
		if !c.Paused() {
			cancel()
		}
	}})
	if err != nil || c.Paused() || det.calls < 3 {
		t.Fatalf("configured gesture could not resume hidden app: inference=%d paused=%t err=%v", det.calls, c.Paused(), err)
	}
}
