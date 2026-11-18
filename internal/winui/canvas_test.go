package winui

import "testing"

func pixel(c *Canvas, x, y int) [4]byte {
	i := (y*c.W + x) * 4
	return [4]byte{c.Pix[i], c.Pix[i+1], c.Pix[i+2], c.Pix[i+3]}
}

func TestCanvasBlendsPremultipliedColour(t *testing.T) {
	c := NewCanvas(8, 8)
	c.RoundRect(0, 0, 8, 8, 0, RGB{R: 255, G: 128}, .5)
	if got, want := pixel(c, 4, 4), [4]byte{0, 64, 128, 128}; got != want {
		t.Fatalf("half-covered pixel is %v, want %v", got, want)
	}
	c.RoundRect(0, 0, 8, 8, 0, RGB{B: 255}, 1)
	if got, want := pixel(c, 4, 4), [4]byte{255, 0, 0, 255}; got != want {
		t.Fatalf("covered pixel is %v, want %v", got, want)
	}
	if img := c.Image(); img.Pix[0] != 0 || img.Pix[2] != 255 || img.Pix[3] != 255 {
		t.Fatalf("image starts %v", img.Pix[:4])
	}
}

func TestRoundRectLeavesItsCornersClear(t *testing.T) {
	c := NewCanvas(20, 20)
	c.RoundRect(0, 0, 20, 20, 8, RGB{R: 255, G: 255, B: 255}, 1)
	if corner, edge, middle := pixel(c, 0, 0)[3], pixel(c, 0, 10)[3], pixel(c, 10, 10)[3]; corner != 0 || edge != 255 || middle != 255 {
		t.Fatalf("corner=%d edge=%d middle=%d", corner, edge, middle)
	}
	ring := NewCanvas(20, 20)
	ring.Outline(0, 0, 20, 20, 8, 2, RGB{R: 255}, 1)
	if edge, middle := pixel(ring, 0, 10)[3], pixel(ring, 10, 10)[3]; edge != 255 || middle != 0 {
		t.Fatalf("outline edge=%d middle=%d", edge, middle)
	}
}

func TestHandGlyphShowsWhichFingersAreUp(t *testing.T) {
	on, off := RGB{R: 255}, RGB{B: 255}
	lit, dim, clear := [4]byte{0, 0, 255, 255}, [4]byte{255, 0, 0, 255}, [4]byte{}
	point := Hand{Fingers: [5]int{Loose, Raised, Curled, Curled, Curled}}
	c := NewCanvas(64, 64)
	c.Hand(0, 0, 64, point, on, off)
	if index, middle, palm := pixel(c, 24, 14), pixel(c, 32, 14), pixel(c, 36, 46); index != lit || middle != clear || palm != dim {
		t.Fatalf("right hand: index=%v middle=%v palm=%v", index, middle, palm)
	}
	point.Left = true
	c = NewCanvas(64, 64)
	c.Hand(0, 0, 64, point, on, off)
	if index, mirrored := pixel(c, 39, 14), pixel(c, 24, 14); index != lit || mirrored != clear {
		t.Fatalf("left hand: index=%v at the right hand's place %v", index, mirrored)
	}
	c = NewCanvas(64, 64)
	c.Hand(0, 0, 64, Hand{Pinch: true}, on, off)
	if thumb, index := pixel(c, 17, 40), pixel(c, 16, 25); thumb != lit || index != lit {
		t.Fatalf("pinch: thumb=%v index=%v", thumb, index)
	}
	// A glyph drawn off the canvas, or with states out of range, is clipped.
	c.Hand(40, 40, 64, Hand{Fingers: [5]int{-3, 9, 0, 0, 0}}, on, off)
}

func TestMaskPaintsThroughCoverage(t *testing.T) {
	c := NewCanvas(4, 2)
	c.Mask([]byte{0, 255, 128, 0}, 2, 2, 1, 0, RGB{R: 255, G: 255, B: 255}, 1)
	if none, full, half := pixel(c, 1, 0)[3], pixel(c, 2, 0), pixel(c, 1, 1)[3]; none != 0 || full != [4]byte{255, 255, 255, 255} || half != 128 {
		t.Fatalf("none=%d full=%v half=%d", none, full, half)
	}
	c.Mask([]byte{255, 255, 255, 255}, 2, 2, 3, 1, RGB{}, 1)
	c.Mask([]byte{255, 255, 255, 255}, 2, 2, -1, -1, RGB{}, 1)
}
