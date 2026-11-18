package overlay

import (
	"math"

	"gesturectl/internal/winui"
)

// TextFunc turns a line of text into a coverage mask, as winui.TextMask does.
type TextFunc func(text string) (mask []byte, width, height int)

var (
	backColour   = winui.RGB{R: 32, G: 32, B: 32}
	textColour   = winui.RGB{R: 255, G: 255, B: 255}
	dimColour    = winui.RGB{R: 185, G: 185, B: 185}
	palmColour   = winui.RGB{R: 110, G: 110, B: 110}
	accentColour = winui.RGB{R: 76, G: 194, B: 255}
	runColour    = winui.RGB{R: 108, G: 203, B: 95}
	flashColour  = winui.RGB{R: 16, G: 150, B: 16}
	pauseColour  = winui.RGB{R: 255, G: 185, B: 0}
)

// scaled converts a length laid out at 96 DPI to pixels at dpi.
func scaled(v, dpi int) int { return int(math.Round(float64(v) * float64(dpi) / 96)) }

// Draw lays the rows out at dpi and paints them on a canvas of their size, or
// returns nil for no rows.
func Draw(rows []Row, dpi int, text TextFunc) *winui.Canvas {
	if len(rows) == 0 {
		return nil
	}
	px := func(v int) float64 { return float64(scaled(v, dpi)) }
	pad, gap, icon, iconGap := px(5), px(3), px(14), px(5)
	bar, barGap, minText := max(2, px(2)), px(2), px(48)
	type line struct {
		mask          []byte
		w, h          int
		block, height float64
	}
	lines := make([]line, len(rows))
	width, height := 0.0, pad*2+gap*float64(len(rows)-1)
	for i, r := range rows {
		mask, w, h := text(r.Text)
		block := float64(h)
		if r.hasBar() {
			block += barGap + bar
		}
		lines[i] = line{mask, w, h, block, max(icon, block)}
		width = max(width, pad*2+icon+iconGap+max(float64(w), minText))
		height += lines[i].height
	}
	c := winui.NewCanvas(int(width), int(height))
	c.RoundRect(0, 0, width, height, px(6), backColour, .9)
	c.Outline(0, 0, width, height, px(6), max(1, px(1)), textColour, .12)
	y := pad
	for i, r := range rows {
		l := lines[i]
		if r.Look == Fired {
			c.RoundRect(px(2), y-px(2), width-px(4), l.height+px(4), px(4), flashColour, .3+.6*r.Glow)
		}
		x, iy := pad, y+(l.height-icon)/2
		switch {
		case r.HasHand:
			on := textColour
			if r.Look == Seen {
				on = dimColour
			}
			c.Hand(x, iy, icon, r.Hand, on, palmColour)
		case r.Look == Pause:
			for _, u := range []float64{.36, .64} {
				c.Capsule(x+icon*u, iy+icon*.24, x+icon*u, iy+icon*.76, icon*.09, pauseColour, 1)
			}
		case r.Look == Resume:
			c.RoundRect(x+icon*.2, iy+icon*.2, icon*.6, icon*.6, icon*.3, runColour, 1)
		case r.Look == Fired:
			c.Capsule(x+icon*.2, iy+icon*.52, x+icon*.42, iy+icon*.74, icon*.08, textColour, 1)
			c.Capsule(x+icon*.42, iy+icon*.74, x+icon*.82, iy+icon*.3, icon*.08, textColour, 1)
		}
		tx, ty := x+icon+iconGap, math.Round(y+(l.height-l.block)/2)
		colour := textColour
		if r.Look == Seen {
			colour = dimColour
		}
		c.Mask(l.mask, l.w, l.h, int(tx), int(ty), colour, 1)
		if r.hasBar() {
			bw, by := width-pad-tx, ty+float64(l.h)+barGap
			c.RoundRect(tx, by, bw, bar, bar/2, textColour, .22)
			fill := accentColour
			switch r.Look {
			case Running:
				fill = runColour
			case Fired:
				fill = textColour
			}
			if p := max(0, min(1, r.Progress)); p > 0 {
				c.RoundRect(tx, by, max(bar, bw*p), bar, bar/2, fill, 1)
			}
		}
		y += l.height + gap
	}
	return c
}

// Point and Rect are screen coordinates in pixels.
type Point struct{ X, Y int }
type Rect struct{ Left, Top, Right, Bottom int }

// Place puts a hint of w by h pixels below and to the right of the cursor, or
// on the other side of it by the edges of the work area.
func Place(cursor Point, w, h int, work Rect, dpi int) Point {
	x, y := cursor.X+scaled(16, dpi), cursor.Y+scaled(22, dpi)
	if x+w > work.Right {
		x = cursor.X - scaled(8, dpi) - w
	}
	if y+h > work.Bottom {
		y = cursor.Y - scaled(8, dpi) - h
	}
	return Point{max(work.Left, min(x, work.Right-w)), max(work.Top, min(y, work.Bottom-h))}
}
