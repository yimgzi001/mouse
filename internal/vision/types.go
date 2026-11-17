// Package vision turns camera frames into hand landmarks.
//
// It re-implements the MediaPipe Hands pipeline (palm detection -> rotated crop ->
// 21 landmarks -> tracking from the previous frame) on top of ONNX Runtime.
package vision

import "math"

// Frame is a packed 24-bit RGB image, row-major, 3 bytes per pixel.
type Frame struct {
	W, H int
	Pix  []byte
}

// Point is a landmark. X and Y are image pixels; Z is depth relative to the wrist
// in roughly the same scale as X (smaller means closer to the camera).
type Point struct{ X, Y, Z float32 }

// Rect is a rotated rectangle in image pixel coordinates. Rot is in radians,
// positive clockwise as seen on screen (the image Y axis points down).
type Rect struct {
	CX, CY, W, H float32
	Rot          float32
}

// NumLandmarks is the number of points the landmark model returns per hand.
const NumLandmarks = 21

// Landmark indices, following the MediaPipe hand model.
const (
	Wrist = iota
	ThumbCMC
	ThumbMCP
	ThumbIP
	ThumbTip
	IndexMCP
	IndexPIP
	IndexDIP
	IndexTip
	MiddleMCP
	MiddlePIP
	MiddleDIP
	MiddleTip
	RingMCP
	RingPIP
	RingDIP
	RingTip
	PinkyMCP
	PinkyPIP
	PinkyDIP
	PinkyTip
)

// Hand is one detected hand in one frame.
type Hand struct {
	// TrackID stays the same while the hand is followed from frame to frame.
	TrackID int
	// Landmarks are in image pixels.
	Landmarks [NumLandmarks]Point
	// World landmarks are in metres relative to the hand centre.
	World [NumLandmarks]Point
	// Presence is the model's confidence that the crop really contains a hand.
	Presence float32
	// LeftScore is the model's probability that this is the person's left hand,
	// assuming the image is mirrored like a selfie view. In an un-mirrored image
	// the meaning is reversed.
	LeftScore float32
	// ROI is the crop the landmarks were computed from.
	ROI Rect
}

func normalizeRadians(a float64) float64 {
	return a - 2*math.Pi*math.Floor((a+math.Pi)/(2*math.Pi))
}

// transformRect applies MediaPipe's RectTransformationCalculator with square_long:
// shift along the rect's own axes, make it square on the long side, then scale.
func transformRect(r Rect, shiftX, shiftY, scale float32) Rect {
	cos := float32(math.Cos(float64(r.Rot)))
	sin := float32(math.Sin(float64(r.Rot)))
	r.CX += r.W*shiftX*cos - r.H*shiftY*sin
	r.CY += r.W*shiftX*sin + r.H*shiftY*cos
	long := r.W
	if r.H > long {
		long = r.H
	}
	r.W = long * scale
	r.H = long * scale
	return r
}

// boundsIoU is the intersection-over-union of the axis-aligned boxes around the
// two rects, ignoring rotation, as MediaPipe's hand association does.
func boundsIoU(a, b Rect) float32 {
	ax0, ay0, ax1, ay1 := a.CX-a.W/2, a.CY-a.H/2, a.CX+a.W/2, a.CY+a.H/2
	bx0, by0, bx1, by1 := b.CX-b.W/2, b.CY-b.H/2, b.CX+b.W/2, b.CY+b.H/2
	iw := minf(ax1, bx1) - maxf(ax0, bx0)
	ih := minf(ay1, by1) - maxf(ay0, by0)
	if iw <= 0 || ih <= 0 {
		return 0
	}
	inter := iw * ih
	union := a.W*a.H + b.W*b.H - inter
	if union <= 0 {
		return 0
	}
	return inter / union
}

func minf(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func maxf(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
