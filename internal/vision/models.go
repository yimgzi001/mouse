package vision

import (
	"fmt"
	"sync"
	"time"

	ort "github.com/yalue/onnxruntime_go"
)

// ModelOptions configures the two neural networks.
type ModelOptions struct {
	// LibraryPath is the full path of onnxruntime.dll.
	LibraryPath string
	// PalmModel and LandmarkModel are the ONNX files' contents.
	PalmModel     []byte
	LandmarkModel []byte
	// Threads limits the CPU threads used for one inference. 0 means 2.
	Threads int
	// WorkerSpinning is "run" (or empty) to spin only during Run, "always"
	// for ONNX's original behavior, or "off" to disable spinning entirely.
	// Sleeping between camera frames saves CPU without sleeping between ops.
	WorkerSpinning string
}

// Models owns the ONNX Runtime sessions. It is not safe for concurrent use.
type Models struct {
	anchors [][2]float32

	palmIn     *ort.Tensor[float32]
	palmBoxes  *ort.Tensor[float32]
	palmScores *ort.Tensor[float32]
	palmSess   *ort.AdvancedSession

	lmIn       *ort.Tensor[float32]
	lmPoints   *ort.Tensor[float32]
	lmPresence *ort.Tensor[float32]
	lmHanded   *ort.Tensor[float32]
	lmWorld    *ort.Tensor[float32]
	lmSess     *ort.AdvancedSession

	// ReplicateBorder makes the landmark crop repeat edge pixels instead of
	// padding with black when the crop reaches outside the frame.
	ReplicateBorder bool
}

var (
	envOnce sync.Once
	envErr  error
)

func initRuntime(libraryPath string) error {
	envOnce.Do(func() {
		ort.SetSharedLibraryPath(libraryPath)
		envErr = ort.InitializeEnvironment()
		if envErr == nil {
			_ = ort.DisableTelemetry()
		}
	})
	return envErr
}

// RuntimeVersion reports the loaded ONNX Runtime version.
func RuntimeVersion() string { return ort.GetVersion() }

// NewModels loads ONNX Runtime and both networks.
func NewModels(opt ModelOptions) (*Models, error) {
	if opt.WorkerSpinning != "" && opt.WorkerSpinning != "run" && opt.WorkerSpinning != "always" && opt.WorkerSpinning != "off" {
		return nil, fmt.Errorf("worker spinning must be run, always or off")
	}
	if err := initRuntime(opt.LibraryPath); err != nil {
		return nil, fmt.Errorf("load ONNX Runtime from %q: %w", opt.LibraryPath, err)
	}
	threads := opt.Threads
	if threads <= 0 {
		threads = 2
	}
	so, err := ort.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("session options: %w", err)
	}
	defer so.Destroy()
	if err := so.SetIntraOpNumThreads(threads); err != nil {
		return nil, err
	}
	if err := so.SetInterOpNumThreads(1); err != nil {
		return nil, err
	}
	if opt.WorkerSpinning == "off" {
		for _, key := range []string{"session.intra_op.allow_spinning", "session.inter_op.allow_spinning"} {
			if err := so.AddSessionConfigEntry(key, "0"); err != nil {
				return nil, fmt.Errorf("disable worker spinning: %w", err)
			}
		}
	} else if opt.WorkerSpinning != "always" {
		if err := so.AddSessionConfigEntry("session.force_spinning_stop", "1"); err != nil {
			return nil, fmt.Errorf("stop worker spinning between runs: %w", err)
		}
	}

	m := &Models{anchors: palmAnchors(), ReplicateBorder: true}
	fail := func(what string, err error) (*Models, error) {
		m.Close()
		return nil, fmt.Errorf("%s: %w", what, err)
	}

	if m.palmIn, err = ort.NewEmptyTensor[float32](ort.NewShape(1, palmInputSize, palmInputSize, 3)); err != nil {
		return fail("palm input tensor", err)
	}
	if m.palmBoxes, err = ort.NewEmptyTensor[float32](ort.NewShape(1, palmNumAnchors, palmNumCoords)); err != nil {
		return fail("palm box tensor", err)
	}
	if m.palmScores, err = ort.NewEmptyTensor[float32](ort.NewShape(1, palmNumAnchors, 1)); err != nil {
		return fail("palm score tensor", err)
	}
	m.palmSess, err = ort.NewAdvancedSessionWithONNXData(opt.PalmModel,
		[]string{"input_1"}, []string{"Identity", "Identity_1"},
		[]ort.Value{m.palmIn}, []ort.Value{m.palmBoxes, m.palmScores}, so)
	if err != nil {
		return fail("palm detection model", err)
	}

	if m.lmIn, err = ort.NewEmptyTensor[float32](ort.NewShape(1, landmarkInputSize, landmarkInputSize, 3)); err != nil {
		return fail("landmark input tensor", err)
	}
	if m.lmPoints, err = ort.NewEmptyTensor[float32](ort.NewShape(1, NumLandmarks*3)); err != nil {
		return fail("landmark output tensor", err)
	}
	if m.lmPresence, err = ort.NewEmptyTensor[float32](ort.NewShape(1, 1)); err != nil {
		return fail("presence tensor", err)
	}
	if m.lmHanded, err = ort.NewEmptyTensor[float32](ort.NewShape(1, 1)); err != nil {
		return fail("handedness tensor", err)
	}
	if m.lmWorld, err = ort.NewEmptyTensor[float32](ort.NewShape(1, NumLandmarks*3)); err != nil {
		return fail("world landmark tensor", err)
	}
	m.lmSess, err = ort.NewAdvancedSessionWithONNXData(opt.LandmarkModel,
		[]string{"input_1"}, []string{"Identity", "Identity_1", "Identity_2", "Identity_3"},
		[]ort.Value{m.lmIn}, []ort.Value{m.lmPoints, m.lmPresence, m.lmHanded, m.lmWorld}, so)
	if err != nil {
		return fail("hand landmark model", err)
	}
	return m, nil
}

// Close releases the sessions and tensors.
func (m *Models) Close() {
	if m.palmSess != nil {
		m.palmSess.Destroy()
		m.palmSess = nil
	}
	if m.lmSess != nil {
		m.lmSess.Destroy()
		m.lmSess = nil
	}
	for _, t := range []*ort.Tensor[float32]{m.palmIn, m.palmBoxes, m.palmScores, m.lmIn, m.lmPoints, m.lmPresence, m.lmHanded, m.lmWorld} {
		if t != nil {
			t.Destroy()
		}
	}
	m.palmIn, m.palmBoxes, m.palmScores = nil, nil, nil
	m.lmIn, m.lmPoints, m.lmPresence, m.lmHanded, m.lmWorld = nil, nil, nil, nil, nil
}

// detectPalms runs the palm detector on the whole frame and returns up to
// maxHands detections, best first.
func (m *Models) detectPalms(f *Frame, minScore float32, maxHands int) ([]palmDetection, error) {
	sampleRect(f, letterboxRect(f.W, f.H), palmInputSize, palmInputSize, m.palmIn.GetData(), false)
	if err := m.palmSess.Run(); err != nil {
		return nil, fmt.Errorf("palm detection: %w", err)
	}
	dets := decodePalms(m.palmBoxes.GetData(), m.palmScores.GetData(), m.anchors, f.W, f.H, minScore)
	dets = weightedNMS(dets)
	if len(dets) > maxHands {
		dets = dets[:maxHands]
	}
	return dets, nil
}

// landmarks runs the landmark model on one crop of the frame.
func (m *Models) landmarks(f *Frame, roi Rect) (Hand, error) {
	sampleRect(f, roi, landmarkInputSize, landmarkInputSize, m.lmIn.GetData(), m.ReplicateBorder)
	if err := m.lmSess.Run(); err != nil {
		return Hand{}, fmt.Errorf("hand landmarks: %w", err)
	}
	h := Hand{
		Presence:  m.lmPresence.GetData()[0],
		LeftScore: m.lmHanded.GetData()[0],
		ROI:       roi,
	}
	projectLandmarks(m.lmPoints.GetData(), roi, &h.Landmarks)
	w := m.lmWorld.GetData()
	for k := 0; k < NumLandmarks; k++ {
		h.World[k] = Point{X: w[3*k], Y: w[3*k+1], Z: w[3*k+2]}
	}
	return h, nil
}

// DetectStill finds hands in a single unrelated image: palm detection followed
// by one landmark pass per palm, with no tracking.
func (m *Models) DetectStill(f *Frame, minDetection, minPresence float32, maxHands int) ([]Hand, error) {
	dets, err := m.detectPalms(f, minDetection, maxHands)
	if err != nil {
		return nil, err
	}
	var hands []Hand
	for i := range dets {
		h, err := m.landmarks(f, palmToROI(&dets[i]))
		if err != nil {
			return nil, err
		}
		if h.Presence < minPresence {
			continue
		}
		h.TrackID = i
		hands = append(hands, h)
	}
	return hands, nil
}

// Benchmark runs each model n times on its current input and returns the average
// time of one palm detection and of one landmark inference.
func (m *Models) Benchmark(n int) (palm, landmark time.Duration, err error) {
	t0 := time.Now()
	for i := 0; i < n; i++ {
		if err = m.palmSess.Run(); err != nil {
			return
		}
	}
	palm = time.Since(t0) / time.Duration(n)
	t0 = time.Now()
	for i := 0; i < n; i++ {
		if err = m.lmSess.Run(); err != nil {
			return
		}
	}
	landmark = time.Since(t0) / time.Duration(n)
	return
}
