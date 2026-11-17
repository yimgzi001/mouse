// Package gesture turns hand landmarks into named gestures.
//
// The steps are: geometric features per frame (how far each finger is
// stretched, which fingertips touch, where the hand points), matching those
// against a table of pose definitions, and a time filter that only reports a
// pose once it has been held steadily.
package gesture

import (
	"math"

	"gesturectl/internal/vision"
)

// Finger numbers, thumb first.
const (
	Thumb = iota
	Index
	Middle
	Ring
	Pinky
	NumFingers
)

// FingerNames are the identifiers used in configuration files.
var FingerNames = [NumFingers]string{"thumb", "index", "middle", "ring", "pinky"}

// Features are the per-frame measurements a pose is matched against.
type Features struct {
	// Openness says how far each finger is stretched: 0 fully curled, 1 straight.
	Openness [NumFingers]float32
	// TouchDist is the distance from the thumb tip to the tip of the index,
	// middle, ring and little finger, in palm lengths.
	TouchDist [NumFingers - 1]float32
	// PalmSize is the wrist-to-middle-knuckle length in image pixels, measured
	// in 3D so that tilting the hand changes it little.
	PalmSize float32
	// PalmX and PalmY are the centre of the palm in image pixels.
	PalmX, PalmY float32
	// HandAngle, ThumbAngle and IndexAngle say where the hand (wrist to middle
	// knuckle), the thumb and the index finger point in the image, in degrees:
	// 0 up, 90 right, 180 or -180 down, -90 left.
	HandAngle, ThumbAngle, IndexAngle float32
	// PalmNormalZ is the camera-facing component of the palm's normal vector,
	// from -1 to 1. Its sign tells palm from back of hand once the hand's side
	// is known.
	PalmNormalZ float32
	// ThumbBend (degrees) and ThumbSpread (thumb tip to index knuckle, in palm
	// lengths) are the two measurements the thumb's openness is made of. They
	// are reported for tuning.
	ThumbBend, ThumbSpread float32
}

// Tuning holds the thresholds that turn joint geometry into finger openness.
//
// A finger's bend is the sum of the bend angles at its two outer joints, in
// degrees. Its reach is the straight-line distance from knuckle to tip divided
// by the finger's length. The thumb's spread is the distance from its tip to
// the index knuckle in palm lengths: a thumb tucked against the hand is near
// the low value, a thumb stuck out is near the high one.
type Tuning struct {
	FingerBendOpen    float64 `json:"fingerBendOpen"`
	FingerBendClosed  float64 `json:"fingerBendClosed"`
	FingerReachClosed float64 `json:"fingerReachClosed"`
	FingerReachOpen   float64 `json:"fingerReachOpen"`
	ThumbBendOpen     float64 `json:"thumbBendOpen"`
	ThumbBendClosed   float64 `json:"thumbBendClosed"`
	ThumbSpreadClosed float64 `json:"thumbSpreadClosed"`
	ThumbSpreadOpen   float64 `json:"thumbSpreadOpen"`
}

// DefaultTuning returns thresholds chosen from MediaPipe's test photos (fist,
// pointing, victory, thumb up, open hands).
func DefaultTuning() Tuning {
	return Tuning{
		FingerBendOpen:    50,
		FingerBendClosed:  130,
		FingerReachClosed: 0.60,
		FingerReachOpen:   0.92,
		ThumbBendOpen:     50,
		ThumbBendClosed:   95,
		ThumbSpreadClosed: 0.45,
		ThumbSpreadOpen:   0.68,
	}
}

type vec3 struct{ x, y, z float64 }

func sub(a, b vec3) vec3 { return vec3{a.x - b.x, a.y - b.y, a.z - b.z} }
func (v vec3) len() float64 {
	return math.Sqrt(v.x*v.x + v.y*v.y + v.z*v.z)
}
func dot(a, b vec3) float64 { return a.x*b.x + a.y*b.y + a.z*b.z }
func cross(a, b vec3) vec3 {
	return vec3{a.y*b.z - a.z*b.y, a.z*b.x - a.x*b.z, a.x*b.y - a.y*b.x}
}

// angleDeg is the angle between two vectors in degrees.
func angleDeg(a, b vec3) float64 {
	la, lb := a.len(), b.len()
	if la == 0 || lb == 0 {
		return 0
	}
	c := dot(a, b) / (la * lb)
	c = math.Max(-1, math.Min(1, c))
	return math.Acos(c) * 180 / math.Pi
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// ramp maps v linearly from [lo, hi] to [0, 1], clamped.
func ramp(v, lo, hi float64) float64 {
	if hi <= lo {
		if v >= hi {
			return 1
		}
		return 0
	}
	return clamp01((v - lo) / (hi - lo))
}

func pt(p vision.Point) vec3 { return vec3{float64(p.X), float64(p.Y), float64(p.Z)} }

// screenAngle converts an image-space direction (y pointing down) to degrees
// with 0 = up and 90 = right.
func screenAngle(dx, dy float64) float32 {
	return float32(math.Atan2(dx, -dy) * 180 / math.Pi)
}

// Extract computes the features of one hand.
func Extract(h *vision.Hand, t *Tuning) Features {
	var f Features
	var w [vision.NumLandmarks]vec3
	for i := range w {
		w[i] = pt(h.World[i])
	}
	palmLen := sub(w[vision.MiddleMCP], w[vision.Wrist]).len()
	if palmLen <= 0 {
		palmLen = 1
	}

	for finger := Index; finger <= Pinky; finger++ {
		base := vision.IndexMCP + (finger-Index)*4
		a, b, c, d := w[base], w[base+1], w[base+2], w[base+3]
		bend := angleDeg(sub(b, a), sub(c, b)) + angleDeg(sub(c, b), sub(d, c))
		length := sub(b, a).len() + sub(c, b).len() + sub(d, c).len()
		reach := 0.0
		if length > 0 {
			reach = sub(d, a).len() / length
		}
		byBend := 1 - ramp(bend, t.FingerBendOpen, t.FingerBendClosed)
		byReach := ramp(reach, t.FingerReachClosed, t.FingerReachOpen)
		f.Openness[finger] = float32(0.5*byBend + 0.5*byReach)
	}

	cmc, mcp, ip, tip := w[vision.ThumbCMC], w[vision.ThumbMCP], w[vision.ThumbIP], w[vision.ThumbTip]
	thumbBend := angleDeg(sub(mcp, cmc), sub(ip, mcp)) + angleDeg(sub(ip, mcp), sub(tip, ip))
	spread := sub(tip, w[vision.IndexMCP]).len() / palmLen
	f.Openness[Thumb] = float32((1 - ramp(thumbBend, t.ThumbBendOpen, t.ThumbBendClosed)) * ramp(spread, t.ThumbSpreadClosed, t.ThumbSpreadOpen))
	f.ThumbBend, f.ThumbSpread = float32(thumbBend), float32(spread)

	for i, tipIdx := range [...]int{vision.IndexTip, vision.MiddleTip, vision.RingTip, vision.PinkyTip} {
		f.TouchDist[i] = float32(sub(tip, w[tipIdx]).len() / palmLen)
	}

	normal := cross(sub(w[vision.IndexMCP], w[vision.Wrist]), sub(w[vision.PinkyMCP], w[vision.Wrist]))
	if n := normal.len(); n > 0 {
		f.PalmNormalZ = float32(normal.z / n)
	}

	lm := &h.Landmarks
	f.PalmSize = vision.PalmLength(lm)
	f.PalmX, f.PalmY = vision.PalmCentre(lm)
	f.HandAngle = screenAngle(float64(lm[vision.MiddleMCP].X-lm[vision.Wrist].X), float64(lm[vision.MiddleMCP].Y-lm[vision.Wrist].Y))
	f.ThumbAngle = screenAngle(float64(lm[vision.ThumbTip].X-lm[vision.ThumbMCP].X), float64(lm[vision.ThumbTip].Y-lm[vision.ThumbMCP].Y))
	f.IndexAngle = screenAngle(float64(lm[vision.IndexTip].X-lm[vision.IndexMCP].X), float64(lm[vision.IndexTip].Y-lm[vision.IndexMCP].Y))
	return f
}
