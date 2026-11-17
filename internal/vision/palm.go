package vision

import (
	"math"
	"sort"
)

const (
	palmInputSize   = 192
	palmNumAnchors  = 2016
	palmNumCoords   = 18
	palmNumKeypoint = 7
	palmNMSOverlap  = 0.3
)

// palmDetection is one palm found by the detector, in image pixels.
type palmDetection struct {
	Score                  float32
	XMin, YMin, XMax, YMax float32
	// Keypoints: 0 wrist, 1 index MCP, 2 middle MCP, 3 ring MCP, 4 pinky MCP,
	// 5 thumb CMC, 6 thumb MCP.
	Keypoints [palmNumKeypoint][2]float32
}

// palmAnchors returns the centres of the SSD anchors used by the palm model:
// a 24x24 grid with 2 anchors per cell, then a 12x12 grid with 6 per cell.
func palmAnchors() [][2]float32 {
	layers := []struct{ stride, perCell int }{{8, 2}, {16, 6}}
	anchors := make([][2]float32, 0, palmNumAnchors)
	for _, l := range layers {
		grid := palmInputSize / l.stride
		for y := 0; y < grid; y++ {
			for x := 0; x < grid; x++ {
				cx := (float32(x) + 0.5) / float32(grid)
				cy := (float32(y) + 0.5) / float32(grid)
				for k := 0; k < l.perCell; k++ {
					anchors = append(anchors, [2]float32{cx, cy})
				}
			}
		}
	}
	return anchors
}

// letterboxRect is the square region, centred on the frame, that the palm model sees.
func letterboxRect(w, h int) Rect {
	side := float32(w)
	if h > w {
		side = float32(h)
	}
	return Rect{CX: float32(w) / 2, CY: float32(h) / 2, W: side, H: side}
}

// decodePalms converts raw model output into detections in image pixels.
// boxes holds palmNumAnchors*palmNumCoords values, scores holds palmNumAnchors.
func decodePalms(boxes, scores []float32, anchors [][2]float32, frameW, frameH int, minScore float32) []palmDetection {
	lb := letterboxRect(frameW, frameH)
	offX := lb.CX - lb.W/2
	offY := lb.CY - lb.H/2
	side := lb.W
	// Only anchors whose raw score clears the threshold need decoding.
	rawMin := float32(math.Log(float64(minScore) / (1 - float64(minScore))))

	var out []palmDetection
	for i := 0; i < palmNumAnchors; i++ {
		raw := scores[i]
		if raw < rawMin {
			continue
		}
		if raw > 100 {
			raw = 100
		}
		score := float32(1 / (1 + math.Exp(-float64(raw))))
		b := boxes[i*palmNumCoords : (i+1)*palmNumCoords]
		ax, ay := anchors[i][0], anchors[i][1]
		cx := b[0]/palmInputSize + ax
		cy := b[1]/palmInputSize + ay
		bw := b[2] / palmInputSize
		bh := b[3] / palmInputSize
		d := palmDetection{
			Score: score,
			XMin:  (cx-bw/2)*side + offX,
			YMin:  (cy-bh/2)*side + offY,
			XMax:  (cx+bw/2)*side + offX,
			YMax:  (cy+bh/2)*side + offY,
		}
		for k := 0; k < palmNumKeypoint; k++ {
			d.Keypoints[k][0] = (b[4+2*k]/palmInputSize+ax)*side + offX
			d.Keypoints[k][1] = (b[5+2*k]/palmInputSize+ay)*side + offY
		}
		out = append(out, d)
	}
	return out
}

func detIoU(a, b *palmDetection) float32 {
	iw := minf(a.XMax, b.XMax) - maxf(a.XMin, b.XMin)
	ih := minf(a.YMax, b.YMax) - maxf(a.YMin, b.YMin)
	if iw <= 0 || ih <= 0 {
		return 0
	}
	inter := iw * ih
	union := (a.XMax-a.XMin)*(a.YMax-a.YMin) + (b.XMax-b.XMin)*(b.YMax-b.YMin) - inter
	if union <= 0 {
		return 0
	}
	return inter / union
}

// weightedNMS merges overlapping detections the way MediaPipe does: each group
// of boxes overlapping the best remaining one is replaced by their
// score-weighted average, keeping the best score.
func weightedNMS(dets []palmDetection) []palmDetection {
	sort.SliceStable(dets, func(i, j int) bool { return dets[i].Score > dets[j].Score })
	remaining := dets
	var out []palmDetection
	for len(remaining) > 0 {
		best := remaining[0]
		var rest []palmDetection
		var total float32
		var acc palmDetection
		for i := range remaining {
			c := &remaining[i]
			if detIoU(&best, c) <= palmNMSOverlap {
				rest = append(rest, *c)
				continue
			}
			s := c.Score
			total += s
			acc.XMin += c.XMin * s
			acc.YMin += c.YMin * s
			acc.XMax += c.XMax * s
			acc.YMax += c.YMax * s
			for k := range c.Keypoints {
				acc.Keypoints[k][0] += c.Keypoints[k][0] * s
				acc.Keypoints[k][1] += c.Keypoints[k][1] * s
			}
		}
		merged := best
		if total > 0 {
			merged.XMin = acc.XMin / total
			merged.YMin = acc.YMin / total
			merged.XMax = acc.XMax / total
			merged.YMax = acc.YMax / total
			for k := range merged.Keypoints {
				merged.Keypoints[k][0] = acc.Keypoints[k][0] / total
				merged.Keypoints[k][1] = acc.Keypoints[k][1] / total
			}
		}
		out = append(out, merged)
		remaining = rest
	}
	return out
}

// palmToROI turns a palm detection into the crop for the landmark model: the box
// rotated so that wrist -> middle finger points up, shifted towards the fingers
// and enlarged to cover the whole hand.
func palmToROI(d *palmDetection) Rect {
	x0, y0 := d.Keypoints[0][0], d.Keypoints[0][1]
	x1, y1 := d.Keypoints[2][0], d.Keypoints[2][1]
	rot := normalizeRadians(math.Pi/2 - math.Atan2(-float64(y1-y0), float64(x1-x0)))
	r := Rect{
		CX:  (d.XMin + d.XMax) / 2,
		CY:  (d.YMin + d.YMax) / 2,
		W:   d.XMax - d.XMin,
		H:   d.YMax - d.YMin,
		Rot: float32(rot),
	}
	return transformRect(r, 0, -0.5, 2.6)
}
