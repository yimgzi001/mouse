package vision

import "math"

const (
	landmarkInputSize = 224
	// The model's z output is divided by this to match x/y scale (MediaPipe's normalize_z).
	landmarkNormalizeZ = 0.4
)

// projectLandmarks maps the landmark model's output (pixels of the 224x224 crop)
// back into image pixels using the crop rectangle.
func projectLandmarks(raw []float32, roi Rect, out *[NumLandmarks]Point) {
	cos := float32(math.Cos(float64(roi.Rot)))
	sin := float32(math.Sin(float64(roi.Rot)))
	for k := 0; k < NumLandmarks; k++ {
		x := (raw[3*k]/landmarkInputSize - 0.5) * roi.W
		y := (raw[3*k+1]/landmarkInputSize - 0.5) * roi.H
		out[k] = Point{
			X: roi.CX + x*cos - y*sin,
			Y: roi.CY + x*sin + y*cos,
			Z: raw[3*k+2] / landmarkInputSize / landmarkNormalizeZ * roi.W,
		}
	}
}

// The landmarks MediaPipe uses to follow a hand into the next frame: the wrist,
// the thumb base and the two lowest joints of each finger. Fingertips are left
// out so that curling the fingers does not move the crop.
var trackingLandmarks = [...]int{
	Wrist, ThumbCMC, ThumbMCP, ThumbIP,
	IndexMCP, IndexPIP, MiddleMCP, MiddlePIP,
	RingMCP, RingPIP, PinkyMCP, PinkyPIP,
}

// landmarksToROI computes the crop for the next frame from this frame's landmarks.
func landmarksToROI(lm *[NumLandmarks]Point) Rect {
	x0, y0 := float64(lm[Wrist].X), float64(lm[Wrist].Y)
	x1 := (float64(lm[IndexMCP].X)+float64(lm[RingMCP].X))/2 + float64(lm[MiddleMCP].X)
	y1 := (float64(lm[IndexMCP].Y)+float64(lm[RingMCP].Y))/2 + float64(lm[MiddleMCP].Y)
	x1 /= 2
	y1 /= 2
	rot := normalizeRadians(math.Pi/2 - math.Atan2(-(y1 - y0), x1-x0))

	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, i := range trackingLandmarks {
		minX = math.Min(minX, float64(lm[i].X))
		maxX = math.Max(maxX, float64(lm[i].X))
		minY = math.Min(minY, float64(lm[i].Y))
		maxY = math.Max(maxY, float64(lm[i].Y))
	}
	acx, acy := (minX+maxX)/2, (minY+maxY)/2

	// Bounds of the same points in the hand's own (un-rotated) frame.
	cosR, sinR := math.Cos(-rot), math.Sin(-rot)
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	for _, i := range trackingLandmarks {
		ox, oy := float64(lm[i].X)-acx, float64(lm[i].Y)-acy
		px := ox*cosR - oy*sinR
		py := ox*sinR + oy*cosR
		minX = math.Min(minX, px)
		maxX = math.Max(maxX, px)
		minY = math.Min(minY, py)
		maxY = math.Max(maxY, py)
	}
	pcx, pcy := (minX+maxX)/2, (minY+maxY)/2
	cos, sin := math.Cos(rot), math.Sin(rot)

	r := Rect{
		CX:  float32(pcx*cos - pcy*sin + acx),
		CY:  float32(pcx*sin + pcy*cos + acy),
		W:   float32(maxX - minX),
		H:   float32(maxY - minY),
		Rot: float32(rot),
	}
	return transformRect(r, 0, -0.1, 2.0)
}
