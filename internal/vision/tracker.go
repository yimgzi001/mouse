package vision

import "time"

// TrackerConfig tunes how hands are found and followed in a video stream.
type TrackerConfig struct {
	// MaxHands is the most hands followed at once.
	MaxHands int
	// MinDetection is the palm detector's score threshold for a new hand.
	MinDetection float32
	// MinPresence is the landmark model's confidence below which a followed
	// hand is considered lost.
	MinPresence float32
	// DetectInterval is how often the palm detector looks for additional hands
	// while at least one hand is already followed.
	DetectInterval time.Duration
	// IdleDetectInterval is how often the palm detector runs while no hand is followed.
	IdleDetectInterval time.Duration
}

// DefaultTrackerConfig returns the starting values from the design document.
func DefaultTrackerConfig() TrackerConfig {
	return TrackerConfig{
		MaxHands:           2,
		MinDetection:       0.6,
		MinPresence:        0.6,
		DetectInterval:     200 * time.Millisecond,
		IdleDetectInterval: 120 * time.Millisecond,
	}
}

// Two crops overlapping more than this are taken to be the same hand.
const sameHandIoU = 0.5

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
		dets, err := t.models.detectPalms(f, t.cfg.MinDetection, t.cfg.MaxHands)
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
			roi := palmToROI(&dets[i])
			known := false
			for _, tr := range t.tracks {
				if boundsIoU(tr.roi, roi) > sameHandIoU {
					known = true
					break
				}
			}
			if !known {
				t.tracks = append(t.tracks, track{id: t.nextID, roi: roi})
				t.nextID++
			}
		}
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
		if h.Presence < t.cfg.MinPresence {
			continue
		}
		next := landmarksToROI(&h.Landmarks)
		if !plausibleROI(next, f) {
			continue
		}
		// Two tracks that drifted onto the same hand: keep the older one.
		duplicate := false
		for _, k := range kept {
			if boundsIoU(k.roi, next) > sameHandIoU {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		h.TrackID = tr.id
		hands = append(hands, h)
		kept = append(kept, track{id: tr.id, roi: next})
	}
	stats.LandmarkTime = time.Since(start)
	t.tracks = kept
	return hands, stats, nil
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
