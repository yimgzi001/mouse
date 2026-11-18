// Package winui holds what the configuration panel and the on-screen hint
// share: drawing with smooth edges, and a few Win32 helpers.
package winui

import (
	"image"
	"math"
)

// RGB is an opaque colour.
type RGB struct{ R, G, B uint8 }

// Canvas is an image of premultiplied BGRA pixels, top row first: the layout
// that layered windows and 32-bit image lists take.
type Canvas struct {
	W, H int
	Pix  []byte
}

func NewCanvas(w, h int) *Canvas {
	w, h = max(w, 0), max(h, 0)
	return &Canvas{W: w, H: h, Pix: make([]byte, w*h*4)}
}

func (c *Canvas) blend(x, y int, col RGB, a float64) {
	if a <= 0 {
		return
	}
	a = min(a, 1)
	i := (y*c.W + x) * 4
	for k, v := range [4]float64{float64(col.B), float64(col.G), float64(col.R), 255} {
		c.Pix[i+k] = uint8(v*a + float64(c.Pix[i+k])*(1-a) + .5)
	}
}

// paint covers the pixels of a box with a colour. distance says how far a
// pixel centre lies outside the shape, in pixels; the edge pixel is covered in
// part, which smooths it.
func (c *Canvas) paint(x0, y0, x1, y1 float64, col RGB, alpha float64, distance func(x, y float64) float64) {
	left, top := max(0, int(math.Floor(x0))), max(0, int(math.Floor(y0)))
	right, bottom := min(c.W, int(math.Ceil(x1))), min(c.H, int(math.Ceil(y1)))
	for y := top; y < bottom; y++ {
		for x := left; x < right; x++ {
			c.blend(x, y, col, alpha*min(1, .5-distance(float64(x)+.5, float64(y)+.5)))
		}
	}
}

func roundRect(px, py, cx, cy, hw, hh, r float64) float64 {
	r = min(r, hw, hh)
	qx, qy := math.Abs(px-cx)-hw+r, math.Abs(py-cy)-hh+r
	return min(max(qx, qy), 0) + math.Hypot(max(qx, 0), max(qy, 0)) - r
}

func capsule(px, py, ax, ay, bx, by, r float64) float64 {
	dx, dy := bx-ax, by-ay
	t := 0.0
	if length := dx*dx + dy*dy; length > 0 {
		t = max(0, min(1, ((px-ax)*dx+(py-ay)*dy)/length))
	}
	return math.Hypot(px-ax-t*dx, py-ay-t*dy) - r
}

// RoundRect fills a rectangle with corners of radius r.
func (c *Canvas) RoundRect(x, y, w, h, r float64, col RGB, alpha float64) {
	c.paint(x, y, x+w, y+h, col, alpha, func(px, py float64) float64 {
		return roundRect(px, py, x+w/2, y+h/2, w/2, h/2, r)
	})
}

// Outline draws the border of a rounded rectangle, width pixels wide, inside it.
func (c *Canvas) Outline(x, y, w, h, r, width float64, col RGB, alpha float64) {
	c.paint(x, y, x+w, y+h, col, alpha, func(px, py float64) float64 {
		return math.Abs(roundRect(px, py, x+w/2, y+h/2, w/2, h/2, r)+width/2) - width/2
	})
}

// Capsule fills a line from a to b with round ends of radius r.
func (c *Canvas) Capsule(ax, ay, bx, by, r float64, col RGB, alpha float64) {
	c.paint(min(ax, bx)-r-1, min(ay, by)-r-1, max(ax, bx)+r+1, max(ay, by)+r+1, col, alpha, func(px, py float64) float64 {
		return capsule(px, py, ax, ay, bx, by, r)
	})
}

// Mask paints a colour through a coverage mask of mw by mh bytes, as
// TextMask returns for a line of text, with its top left corner at x, y.
func (c *Canvas) Mask(mask []byte, mw, mh, x, y int, col RGB, alpha float64) {
	for row := 0; row < mh; row++ {
		if y+row < 0 || y+row >= c.H {
			continue
		}
		for column := 0; column < mw; column++ {
			if x+column < 0 || x+column >= c.W {
				continue
			}
			c.blend(x+column, y+row, col, alpha*float64(mask[row*mw+column])/255)
		}
	}
}

// The states of a finger in a hand glyph.
const (
	Curled = iota
	Loose
	Raised
)

// Hand describes a hand glyph: the state of each finger from the thumb to the
// little finger. Pinch joins the tips of the thumb and the index finger. Left
// draws a left hand, with the thumb on the right as its owner sees it.
type Hand struct {
	Fingers [5]int
	Pinch   bool
	Left    bool
}

// The glyph is laid out in a unit square: a palm, four fingers above it and
// the thumb on the left.
var (
	fingerX   = [4]float64{.388, .506, .624, .742}
	fingerTip = [3][4]float64{
		Curled: {.505, .505, .505, .505},
		Loose:  {.36, .33, .36, .42},
		Raised: {.17, .10, .16, .28},
	}
	thumbTip = [3][2]float64{Curled: {.285, .70}, Loose: {.19, .64}, Raised: {.115, .565}}
)

// Hand draws a hand glyph in the square of the given size at x, y. Raised
// fingers take the colour on, the palm and the other fingers the colour off.
func (c *Canvas) Hand(x, y, size float64, hand Hand, on, off RGB) {
	at := func(u, v float64) (float64, float64) {
		if hand.Left {
			u = 1 - u
		}
		return x + u*size, y + v*size
	}
	type stroke struct {
		ax, ay, bx, by, r float64
		lit               bool
	}
	var strokes []stroke
	finger := func(au, av, bu, bv, r float64, lit bool) {
		ax, ay := at(au, av)
		bx, by := at(bu, bv)
		strokes = append(strokes, stroke{ax, ay, bx, by, r * size, lit})
	}
	for i, state := range hand.Fingers[1:] {
		if i == 0 && hand.Pinch {
			finger(fingerX[0], .58, .25, .385, .052, true)
			continue
		}
		state = max(Curled, min(Raised, state))
		finger(fingerX[i], .58, fingerX[i], fingerTip[state][i], .052, state == Raised)
	}
	if hand.Pinch {
		finger(.37, .80, .185, .47, .056, true)
	} else {
		state := max(Curled, min(Raised, hand.Fingers[0]))
		finger(.37, .80, thumbTip[state][0], thumbTip[state][1], .056, state == Raised)
	}
	for _, s := range strokes {
		if !s.lit {
			c.Capsule(s.ax, s.ay, s.bx, s.by, s.r, off, 1)
		}
	}
	px, py := at(.565, .725)
	c.RoundRect(px-.255*size, py-.195*size, .51*size, .39*size, .11*size, off, 1)
	for _, s := range strokes {
		if s.lit {
			c.Capsule(s.ax, s.ay, s.bx, s.by, s.r, on, 1)
		}
	}
}

// Image converts the canvas to a standard image, for saving and for tests.
func (c *Canvas) Image() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, c.W, c.H))
	for i := 0; i+3 < len(c.Pix); i += 4 {
		if a := int(c.Pix[i+3]); a != 0 {
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = uint8(min(255, int(c.Pix[i+2])*255/a)), uint8(min(255, int(c.Pix[i+1])*255/a)), uint8(min(255, int(c.Pix[i])*255/a)), uint8(a)
		}
	}
	return img
}
