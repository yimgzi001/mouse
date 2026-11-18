package gesture

import (
	"math"
	"sort"
	"time"

	"gesturectl/internal/vision"
)

// Side says which of the user's hands a hand is.
type Side int8

const (
	SideUnknown Side = iota
	SideLeft
	SideRight
)

func (s Side) String() string {
	switch s {
	case SideLeft:
		return "left"
	case SideRight:
		return "right"
	}
	return ""
}

// Config holds the recognizer's settings. Times are in milliseconds and
// distances in palm lengths (wrist to middle knuckle).
type Config struct {
	// VoteWindowMs is the span of recent frames that vote on the pose, and
	// VoteShare the share of those frames a pose needs to become the steady one.
	VoteWindowMs int     `json:"voteWindowMs"`
	VoteShare    float64 `json:"voteShare"`
	// PoseConfirmMs replaces majority voting for selected poses with consecutive
	// observations spanning this duration (and at least two frames). Other
	// poses keep the normal vote window. A dropout restarts confirmation.
	PoseConfirmMs map[string]int `json:"poseConfirmMs,omitempty"`
	// FromPoseConfirmMs lengthens PoseConfirmMs for a pose that directly
	// follows another steady pose, as when a hand relaxes out of a gesture
	// and briefly looks like an open hand.
	FromPoseConfirmMs map[string]int `json:"fromPoseConfirmMs,omitempty"`
	// PoseStillness makes a PoseConfirmMs pose wait for a hand at rest:
	// confirmation restarts whenever the palm has moved more than this many
	// palm lengths from where it began. A hand that passes through the pose
	// on its way into or out of view therefore does not confirm it.
	PoseStillness map[string]float64 `json:"poseStillness,omitempty"`
	// A finger counts as open once its openness rises above FingerOpenAt and as
	// closed once it falls below FingerCloseAt.
	FingerOpenAt  float64 `json:"fingerOpenAt"`
	FingerCloseAt float64 `json:"fingerCloseAt"`
	// PalmThumbOpenAt and PalmThumbCloseAt take the place of FingerOpenAt and
	// FingerCloseAt for the thumb while the other four fingers are open. The
	// thumb of a relaxed open hand rests beside the index finger and measures
	// far below FingerOpenAt, while a thumb folded onto the palm measures
	// close to zero. Zero keeps the normal thresholds.
	PalmThumbOpenAt  float64 `json:"palmThumbOpenAt"`
	PalmThumbCloseAt float64 `json:"palmThumbCloseAt"`
	// Two fingertips count as touching once they are closer than TouchEnter
	// and as apart once they are farther than TouchExit.
	TouchEnter float64 `json:"touchEnter"`
	TouchExit  float64 `json:"touchExit"`
	// TouchMinOpen is the least openness a finger needs for its tip to count as
	// touching the thumb.
	TouchMinOpen float64 `json:"touchMinOpen"`
	// DirToleranceDeg is how far a finger may deviate from a required direction.
	DirToleranceDeg float64 `json:"dirToleranceDeg"`
	// LostGraceMs is how long a hand may be out of sight before it counts as
	// withdrawn. A hand that reappears nearby within this time keeps its state.
	LostGraceMs int `json:"lostGraceMs"`
	// SideSmoothMs is the time constant for averaging the left/right estimate.
	SideSmoothMs int `json:"sideSmoothMs"`
	// A swipe is a palm movement of at least SwipeDistance within SwipeWindowMs.
	// After one, further swipes of that hand are ignored for SwipeCooldownMs.
	SwipeDistance   float64 `json:"swipeDistance"`
	SwipeWindowMs   int     `json:"swipeWindowMs"`
	SwipeCooldownMs int     `json:"swipeCooldownMs"`
	Tuning          Tuning  `json:"tuning"`
}

// DefaultConfig returns the default recognizer settings.
func DefaultConfig() Config {
	return Config{
		VoteWindowMs:     150,
		VoteShare:        0.6,
		FingerOpenAt:     0.6,
		FingerCloseAt:    0.4,
		PalmThumbOpenAt:  0.25,
		PalmThumbCloseAt: 0.15,
		TouchEnter:       0.22,
		TouchExit:        0.35,
		TouchMinOpen:     0.2,
		DirToleranceDeg:  40,
		LostGraceMs:      200,
		SideSmoothMs:     150,
		SwipeDistance:    1.5,
		SwipeWindowMs:    350,
		SwipeCooldownMs:  600,
		Tuning:           DefaultTuning(),
	}
}

// HandState is what the recognizer reports about one hand in one frame.
type HandState struct {
	// ID stays the same while the hand is followed, including short dropouts.
	ID int
	// Side is SideUnknown while the estimate is unsure, or when two hands in
	// view claim the same side and this is the less certain one.
	Side Side
	// Present is false while the hand is out of sight but still within the
	// grace time. Lost is true in the single last report after that time.
	Present bool
	Lost    bool
	// Pose is the steady pose among the active ones, "" for none. Changed is
	// true in the frame it changes; PrevPose is what it was before.
	Pose      string
	PrevPose  string
	Changed   bool
	PoseSince time.Time
	// RawPose is this frame's match among the active poses, before voting.
	// RawSince is when the hand began to show it frame after frame.
	RawPose  string
	RawSince time.Time
	// AnyPose is this frame's match among all poses, for display.
	AnyPose string
	// Swipe is "left", "right", "up" or "down" in the frame a swipe completes.
	Swipe string
	// Fingers and Touch are the finger states after hysteresis.
	Fingers   [NumFingers]bool
	Touch     [NumFingers - 1]bool
	LeftScore float32
	// Facing is 1 while the palm faces the camera, -1 for the back of the
	// hand and 0 when the side or the angle of the hand leaves it unclear.
	Facing    int8
	Features  Features
	Landmarks [vision.NumLandmarks]vision.Point
}

type vote struct {
	t    time.Time
	pose string
}

type motionSample struct {
	t    time.Time
	x, y float32
}

type slot struct {
	id        int
	trackID   int
	seen      bool
	init      bool
	lastSeen  time.Time
	open      [NumFingers]bool
	touch     [NumFingers - 1]bool
	leftScore float32
	side      Side
	// sideSince is when side was last set and flipSince when the estimate
	// started to point at the other side, zero while it does not.
	sideSince time.Time
	flipSince time.Time
	votes     []vote
	motion    []motionSample
	swipeHold time.Time
	swipePose string
	// rawSince is when the current run of the same raw pose began, rawFrames
	// its length and rawX, rawY where the palm was at its start.
	rawSince   time.Time
	rawFrames  int
	rawX, rawY float32
	last       HandState
}

// Recognizer follows hands over time and reports their steady poses.
type Recognizer struct {
	cfg    Config
	poses  *PoseSet
	active map[string]bool
	slots  []*slot
	nextID int
	out    []HandState
	// inverted is set while the caller inverts the detector's handedness to
	// correct the camera. Palm and back are judged in the detector's view.
	inverted bool
}

// NewRecognizer creates a recognizer in which every pose of the set is active.
func NewRecognizer(cfg Config, poses *PoseSet) *Recognizer {
	return &Recognizer{cfg: cfg, poses: poses, nextID: 1}
}

// Configure replaces the settings and the poses while keeping the hands that
// are being followed. Only poses in active take part in recognition; nil
// means all of them.
func (r *Recognizer) Configure(cfg Config, poses *PoseSet, active map[string]bool) {
	r.cfg, r.poses, r.active = cfg, poses, active
}

// Reset forgets all hands.
func (r *Recognizer) Reset() { r.slots = r.slots[:0] }

// InvertSides applies a camera handedness correction without replacing tracked
// IDs or losing gesture release state during a configuration update.
func (r *Recognizer) InvertSides() {
	r.inverted = !r.inverted
	flip := func(side Side) Side {
		if side == SideLeft {
			return SideRight
		}
		if side == SideRight {
			return SideLeft
		}
		return side
	}
	for _, s := range r.slots {
		s.leftScore = 1 - s.leftScore
		s.side = flip(s.side)
		s.last.Side = flip(s.last.Side)
		s.last.LeftScore = 1 - s.last.LeftScore
	}
	for n := range r.out {
		r.out[n].Side = flip(r.out[n].Side)
		r.out[n].LeftScore = 1 - r.out[n].LeftScore
	}
}

const (
	minVotes = 3
	// A hand that shows up within this many palm lengths of where a hand just
	// vanished is taken to be the same hand.
	reattachDistance = 1.5
	sideEnter        = 0.15 // distance from 0.5 at which a side is first accepted
	sideFlip         = 0.25 // distance on the other side of 0.5 at which it flips
	palmFacingMin    = 0.35
	// Turning a hand can fool the handedness estimate for a moment, and a
	// flip hands it the other hand's bindings. Once a hand has kept its side
	// for sideLockAfter, the estimate must point at the other side for
	// sideLockHold before the side flips.
	sideLockAfter = 500 * time.Millisecond
	sideLockHold  = 700 * time.Millisecond
)

func ms(v int) time.Duration { return time.Duration(v) * time.Millisecond }

// Update takes the hands found in one frame and returns the state of every
// hand being followed, ordered by ID. The returned slice is reused by the next
// call.
func (r *Recognizer) Update(hands []vision.Hand, now time.Time) []HandState {
	for _, s := range r.slots {
		s.seen = false
	}
	assigned := make([]*slot, len(hands))
	for i := range hands {
		for _, s := range r.slots {
			if !s.seen && s.trackID == hands[i].TrackID {
				assigned[i], s.seen = s, true
				break
			}
		}
	}
	features := make([]Features, len(hands))
	for i := range hands {
		features[i] = Extract(&hands[i], &r.cfg.Tuning)
		if assigned[i] != nil {
			continue
		}
		f := &features[i]
		var best *slot
		bestDist := float32(math.Inf(1))
		for _, s := range r.slots {
			if s.seen {
				continue
			}
			lf := &s.last.Features
			d := float32(math.Hypot(float64(f.PalmX-lf.PalmX), float64(f.PalmY-lf.PalmY)))
			size := f.PalmSize
			if lf.PalmSize > size {
				size = lf.PalmSize
			}
			if d <= reattachDistance*size && d < bestDist {
				best, bestDist = s, d
			}
		}
		if best == nil {
			best = &slot{id: r.nextID}
			r.nextID++
			r.slots = append(r.slots, best)
		}
		best.trackID, best.seen = hands[i].TrackID, true
		assigned[i] = best
	}
	for i := range hands {
		r.observe(assigned[i], &hands[i], &features[i], now)
	}

	r.out = r.out[:0]
	kept := r.slots[:0]
	grace := ms(r.cfg.LostGraceMs)
	for _, s := range r.slots {
		if !s.seen {
			s.last.Present = false
			s.last.Changed = false
			s.last.Swipe = ""
			s.last.RawPose = ""
			s.last.AnyPose = ""
			if now.Sub(s.lastSeen) > grace {
				s.last.Lost = true
				r.out = append(r.out, s.last)
				continue
			}
		}
		r.out = append(r.out, s.last)
		kept = append(kept, s)
	}
	for i := len(kept); i < len(r.slots); i++ {
		r.slots[i] = nil
	}
	r.slots = kept
	r.resolveSides()
	sort.Slice(r.out, func(i, j int) bool { return r.out[i].ID < r.out[j].ID })
	return r.out
}

// resolveSides withholds the side of the less certain hand when two visible
// hands claim the same one.
func (r *Recognizer) resolveSides() {
	for i := range r.out {
		for j := i + 1; j < len(r.out); j++ {
			a, b := &r.out[i], &r.out[j]
			if !a.Present || !b.Present || a.Side == SideUnknown || a.Side != b.Side {
				continue
			}
			ca := math.Abs(float64(a.LeftScore) - 0.5)
			cb := math.Abs(float64(b.LeftScore) - 0.5)
			if ca < cb || (ca == cb && a.ID > b.ID) {
				a.Side = SideUnknown
			} else {
				b.Side = SideUnknown
			}
		}
	}
}

func (r *Recognizer) observe(s *slot, h *vision.Hand, f *Features, now time.Time) {
	cfg := &r.cfg
	setOpen := func(i int, openAt, closeAt float64) {
		switch o := f.Openness[i]; {
		case !s.init:
			s.open[i] = o >= float32(openAt+closeAt)/2
		case s.open[i]:
			if o < float32(closeAt) {
				s.open[i] = false
			}
		case o > float32(openAt):
			s.open[i] = true
		}
	}
	palm := cfg.PalmThumbOpenAt > 0
	for i := Index; i < NumFingers; i++ {
		setOpen(i, cfg.FingerOpenAt, cfg.FingerCloseAt)
		palm = palm && s.open[i]
	}
	if palm {
		setOpen(Thumb, cfg.PalmThumbOpenAt, cfg.PalmThumbCloseAt)
	} else {
		setOpen(Thumb, cfg.FingerOpenAt, cfg.FingerCloseAt)
	}
	for i, d := range f.TouchDist {
		// In a fist the thumb lies on the curled fingers, close to their tips.
		// That is not a pinch, so a fully curled finger cannot be touched.
		openness := f.Openness[Index+i]
		if s.touch[i] {
			if d > float32(cfg.TouchExit) || openness < float32(cfg.TouchMinOpen)/2 {
				s.touch[i] = false
			}
		} else if d < float32(cfg.TouchEnter) && openness >= float32(cfg.TouchMinOpen) {
			s.touch[i] = true
		}
	}

	if !s.init {
		s.leftScore = h.LeftScore
	} else {
		alpha := float32(1)
		if cfg.SideSmoothMs > 0 {
			alpha = float32(1 - math.Exp(-float64(now.Sub(s.lastSeen))/float64(ms(cfg.SideSmoothMs))))
		}
		s.leftScore += alpha * (h.LeftScore - s.leftScore)
	}
	s.updateSide(now)
	s.init = true

	ob := observation{open: s.open, touch: s.touch, thumbAngle: f.ThumbAngle, indexAngle: f.IndexAngle}
	if s.side != SideUnknown && math.Abs(float64(f.PalmNormalZ)) >= palmFacingMin {
		// In the mirrored view a left hand shows its palm when the palm normal
		// points towards the camera (negative z); for a right hand it is the
		// other way round. The side must be the detector's: the landmarks are
		// in its view whether or not the caller corrects its handedness.
		if (f.PalmNormalZ < 0) == ((s.side == SideLeft) != r.inverted) {
			ob.palm = 1
		} else {
			ob.palm = -1
		}
	}
	tolerance := float32(cfg.DirToleranceDeg)
	raw := r.poses.match(&ob, r.active, tolerance)
	anyPose := raw
	if r.active != nil {
		anyPose = r.poses.match(&ob, nil, tolerance)
	}

	window := ms(cfg.VoteWindowMs)
	s.votes = append(s.votes, vote{now, raw})
	drop := 0
	for drop < len(s.votes)-minVotes && now.Sub(s.votes[drop].t) > 2*window {
		drop++
	}
	s.votes = append(s.votes[:0], s.votes[drop:]...)

	prev := s.last
	if raw != prev.RawPose || !prev.Present || now.Sub(s.lastSeen) > max(window, 150*time.Millisecond) {
		s.rawSince, s.rawFrames, s.rawX, s.rawY = now, 0, f.PalmX, f.PalmY
	}
	s.rawFrames++
	st := HandState{
		ID:        s.id,
		Side:      s.side,
		Present:   true,
		Pose:      prev.Pose,
		PrevPose:  prev.PrevPose,
		PoseSince: prev.PoseSince,
		RawPose:   raw,
		AnyPose:   anyPose,
		Fingers:   s.open,
		Touch:     s.touch,
		LeftScore: s.leftScore,
		Facing:    ob.palm,
		Features:  *f,
		Landmarks: h.Landmarks,
	}
	if st.PoseSince.IsZero() {
		st.PoseSince = now
	}
	winner, ok := s.tally(now, window, cfg.VoteShare)
	if confirmMs := cfg.PoseConfirmMs[raw]; confirmMs > 0 {
		if prev.Pose != "" && prev.Pose != raw {
			confirmMs = max(confirmMs, cfg.FromPoseConfirmMs[raw])
		}
		if still := cfg.PoseStillness[raw]; still > 0 && prev.Pose != raw &&
			math.Hypot(float64(f.PalmX-s.rawX), float64(f.PalmY-s.rawY)) > still*float64(f.PalmSize) {
			s.rawSince, s.rawFrames, s.rawX, s.rawY = now, 1, f.PalmX, f.PalmY
		}
		winner, ok = raw, s.rawFrames >= 2 && now.Sub(s.rawSince) >= ms(confirmMs)
	} else if cfg.PoseConfirmMs[winner] > 0 {
		// Old votes cannot activate a fast-confirmed pose after it disappeared.
		ok = false
	}
	if ok && winner != st.Pose {
		st.PrevPose, st.Pose, st.PoseSince, st.Changed = st.Pose, winner, now, true
	}
	// A new palm shape or reacquired hand must build its own swipe motion.
	// Movement made while pointing, or while out of view, is not a palm swipe.
	if raw != s.swipePose || !s.last.Present {
		s.motion = s.motion[:0]
		if raw != s.swipePose {
			s.swipeHold = time.Time{}
		}
		s.swipePose = raw
	}
	st.Swipe = s.swipe(now, f, cfg)
	st.RawSince = s.rawSince
	s.last = st
	s.lastSeen = now
}

// updateSide applies the smoothed left/right estimate with hysteresis, and
// with the lock described at sideLockAfter.
func (s *slot) updateSide(now time.Time) {
	switch {
	case s.side == SideUnknown:
		if s.leftScore > 0.5+sideEnter {
			s.side, s.sideSince = SideLeft, now
		} else if s.leftScore < 0.5-sideEnter {
			s.side, s.sideSince = SideRight, now
		}
		s.flipSince = time.Time{}
		return
	case s.side == SideLeft && s.leftScore < 0.5-sideFlip,
		s.side == SideRight && s.leftScore > 0.5+sideFlip:
	default:
		s.flipSince = time.Time{}
		return
	}
	if s.flipSince.IsZero() {
		s.flipSince = now
	}
	if now.Sub(s.sideSince) >= sideLockAfter && now.Sub(s.flipSince) < sideLockHold {
		return
	}
	if s.side == SideLeft {
		s.side = SideRight
	} else {
		s.side = SideLeft
	}
	s.sideSince, s.flipSince = now, time.Time{}
}

// tally returns the pose that holds the required share of the recent votes.
// ok is false when there are too few votes or no pose has that share.
func (s *slot) tally(now time.Time, window time.Duration, share float64) (winner string, ok bool) {
	n := len(s.votes)
	start := n
	for start > 0 && now.Sub(s.votes[start-1].t) <= window {
		start--
	}
	if n-start < minVotes {
		// At low frame rates the window holds too few frames: fall back to the
		// last few, unless they are too old to say anything about now.
		start = n - minVotes
		if start < 0 || now.Sub(s.votes[start].t) > 2*window {
			return "", false
		}
	}
	best, bestCount := "", 0
	for i := start; i < n; i++ {
		count := 0
		for j := start; j < n; j++ {
			if s.votes[j].pose == s.votes[i].pose {
				count++
			}
		}
		if count > bestCount {
			best, bestCount = s.votes[i].pose, count
		}
	}
	if float64(bestCount) < share*float64(n-start) {
		return "", false
	}
	return best, true
}

// swipe reports a fast palm movement along one axis.
func (s *slot) swipe(now time.Time, f *Features, cfg *Config) string {
	if now.Before(s.swipeHold) {
		s.motion = s.motion[:0]
		return ""
	}
	window := ms(cfg.SwipeWindowMs)
	drop := 0
	for drop < len(s.motion) && now.Sub(s.motion[drop].t) > window {
		drop++
	}
	s.motion = append(s.motion[:0], s.motion[drop:]...)
	dir := ""
	if f.PalmSize > 0 && cfg.SwipeDistance > 0 {
		need := float32(cfg.SwipeDistance) * f.PalmSize
		for _, m := range s.motion {
			dx, dy := f.PalmX-m.x, f.PalmY-m.y
			ax, ay := float32(math.Abs(float64(dx))), float32(math.Abs(float64(dy)))
			switch {
			case ax >= need && ax >= 2*ay:
				if dx > 0 {
					dir = "right"
				} else {
					dir = "left"
				}
			case ay >= need && ay >= 2*ax:
				if dy > 0 {
					dir = "down"
				} else {
					dir = "up"
				}
			}
			if dir != "" {
				break
			}
		}
	}
	if dir != "" {
		s.swipeHold = now.Add(ms(cfg.SwipeCooldownMs))
		s.motion = s.motion[:0]
		return dir
	}
	s.motion = append(s.motion, motionSample{now, f.PalmX, f.PalmY})
	return ""
}
