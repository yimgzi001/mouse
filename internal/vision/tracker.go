package vision

import (
	"math"
	"time"
)

// TrackerConfig tunes how hands are found and followed in a video stream.
type TrackerConfig struct {
	// MaxHands is the most hands followed at once.
	MaxHands int
	// MinDetection is the palm detector's score threshold for a new hand.
	MinDetection float32
	// MinPresence is the landmark model's confidence a newly found hand needs.
	MinPresence float32
	// KeepPresence is the confidence below which a followed hand is considered
	// lost. It is lower than MinPresence so that a hand that is briefly harder
	// to see, such as one turning or moving fast, stays followed. Zero means
	// MinPresence.
	KeepPresence float32
	// MinPalmPx is the smallest palm, wrist to middle knuckle in frame pixels,
	// a newly found hand may have. Smaller hands are farther from the camera
	// than the user, usually people behind them. Zero accepts any size.
	MinPalmPx float32
	// DetectInterval is how often the palm detector looks for additional hands
	// while at least one hand is already followed.
	DetectInterval time.Duration
	// IdleDetectInterval is how often the palm detector runs while no hand is followed.
	IdleDetectInterval time.Duration
}

// DefaultTrackerConfig returns the starting values from the design document.
func DefaultTrackerConfig() TrackerConfig {
	return TrackerConfig{
		MaxHands:     2,
		MinDetection: 0.6,
		MinPresence:  0.6,
		KeepPresence: 0.5,
		// Palm detection is the costliest step. Longer intervals save CPU but
		// let a hand coming into view wait up to that long to be followed.
		DetectInterval:     100 * time.Millisecond,
		IdleDetectInterval: 200 * time.Millisecond,
	}
}

// Two crops overlapping more than this are taken to be the same hand.
const sameHandIoU = 0.5

// A detection or hand whose palm centre lies within this many palm lengths of
// a followed hand's is that hand. The detector's crop of a followed hand can
// differ from the hand's own crop enough for their overlap to stay below
// sameHandIoU, which used to start a second track on one hand.
const sameHandPalms = 0.6

// The palm detector's keypoints give a rougher, flat palm length than the
// landmarks, so its candidates are only dropped when clearly too small.
const detectionPalmSlack = 0.7

// FrameStats reports what the tracker did for one frame.
type FrameStats struct {
	RanDetection  bool
	DetectionTime time.Duration
	LandmarkTime  time.Duration
	LandmarkRuns  int
}

type track struct {
	id  int
	roi Rect
	// palmX, palmY and palmLen locate the palm, from the latest landmarks or,
	// for a fresh track, from the palm detection.
	palmX, palmY, palmLen float32
	// fresh is true until the landmark model has confirmed the hand.
	fresh bool
}

// Tracker follows hands from frame to frame. The palm detector only runs when
// fewer than MaxHands hands are followed; otherwise each hand's crop for the
// next frame is derived from its landmarks in the current frame.
type Tracker struct {
	models     *Models
	cfg        TrackerConfig
	tracks     []track
	nextID     int
	lastDetect time.Time
}

// NewTracker creates a tracker using the given models.
func NewTracker(models *Models, cfg TrackerConfig) *Tracker {
	return &Tracker{models: models, cfg: cfg, nextID: 1}
}

// SetConfig changes the tuning while running.
func (t *Tracker) SetConfig(cfg TrackerConfig) { t.cfg = cfg }

// SetMinPalm changes MinPalmPx while running. Hands already followed stay.
func (t *Tracker) SetMinPalm(px float32) { t.cfg.MinPalmPx = px }

// Reset forgets all followed hands.
func (t *Tracker) Reset() {
	t.tracks = t.tracks[:0]
	t.lastDetect = time.Time{}
}

// Tracking reports how many hands are currently followed.
func (t *Tracker) Tracking() int { return len(t.tracks) }

// DetectionDue reports whether Process would run the palm detector at time now.
func (t *Tracker) DetectionDue(now time.Time) bool {
	if len(t.tracks) >= t.cfg.MaxHands {
		return false
	}
	interval := t.cfg.DetectInterval
	if len(t.tracks) == 0 {
		interval = t.cfg.IdleDetectInterval
	}
	return t.lastDetect.IsZero() || now.Sub(t.lastDetect) >= interval
}

// Process finds the hands in frame f, captured at time now.
func (t *Tracker) Process(f *Frame, now time.Time) ([]Hand, FrameStats, error) {
	var stats FrameStats

	if t.DetectionDue(now) {
		start := time.Now()
		// Spare candidates keep a hand that is followed already, or too small,
		// from hiding another one among the detector's best few.
		dets, err := t.models.detectPalms(f, t.cfg.MinDetection, t.cfg.MaxHands+2)
		stats.RanDetection = true
		stats.DetectionTime = time.Since(start)
		t.lastDetect = now
		if err != nil {
			return nil, stats, err
		}
		for i := range dets {
			if len(t.tracks) >= t.cfg.MaxHands {
				break
			}
			candidate := track{roi: palmToROI(&dets[i]), fresh: true}
			candidate.palmX, candidate.palmY, candidate.palmLen = detectionPalm(&dets[i])
			if candidate.palmLen < t.cfg.MinPalmPx*detectionPalmSlack || sameHand(t.tracks, &candidate) {
				continue
			}
			candidate.id = t.nextID
			t.nextID++
			t.tracks = append(t.tracks, candidate)
		}
	}

	keep := t.cfg.KeepPresence
	if keep <= 0 || keep > t.cfg.MinPresence {
		keep = t.cfg.MinPresence
	}
	hands := make([]Hand, 0, len(t.tracks))
	kept := t.tracks[:0]
	start := time.Now()
	for _, tr := range t.tracks {
		h, err := t.models.landmarks(f, tr.roi)
		stats.LandmarkRuns++
		if err != nil {
			t.tracks = t.tracks[:0]
			return nil, stats, err
		}
		if h.Presence < keep || tr.fresh && h.Presence < t.cfg.MinPresence {
			continue
		}
		next := track{id: tr.id, roi: landmarksToROI(&h.Landmarks)}
		if !plausibleROI(next.roi, f) {
			continue
		}
		next.palmX, next.palmY = PalmCentre(&h.Landmarks)
		next.palmLen = PalmLength(&h.Landmarks)
		if tr.fresh && next.palmLen < t.cfg.MinPalmPx {
			continue
		}
		// Two tracks on the same hand: keep the older one.
		if sameHand(kept, &next) {
			continue
		}
		h.TrackID = tr.id
		hands = append(hands, h)
		kept = append(kept, next)
	}
	stats.LandmarkTime = time.Since(start)
	t.tracks = kept
	return hands, stats, nil
}

// sameHand reports whether c is one of the hands in tracks.
func sameHand(tracks []track, c *track) bool {
	for i := range tracks {
		t := &tracks[i]
		if boundsIoU(t.roi, c.roi) > sameHandIoU {
			return true
		}
		size := maxf(t.palmLen, c.palmLen)
		dist := math.Hypot(float64(t.palmX-c.palmX), float64(t.palmY-c.palmY))
		if size > 0 && dist < sameHandPalms*float64(size) {
			return true
		}
	}
	return false
}

// detectionPalm locates the palm from the detector's wrist and knuckle
// keypoints, in the same way as PalmCentre and, flat, PalmLength.
func detectionPalm(d *palmDetection) (x, y, length float32) {
	for k := 0; k < 5; k++ {
		x += d.Keypoints[k][0]
		y += d.Keypoints[k][1]
	}
	dx := float64(d.Keypoints[2][0] - d.Keypoints[0][0])
	dy := float64(d.Keypoints[2][1] - d.Keypoints[0][1])
	return x / 5, y / 5, float32(math.Hypot(dx, dy))
}

// plausibleROI rejects crops that are degenerate or have left the frame.
func plausibleROI(r Rect, f *Frame) bool {
	if r.W < 16 || r.H < 16 {
		return false
	}
	limit := 4 * float32(max(f.W, f.H))
	if r.W > limit {
		return false
	}
	return r.CX > -r.W/4 && r.CY > -r.H/4 && r.CX < float32(f.W)+r.W/4 && r.CY < float32(f.H)+r.H/4
}
