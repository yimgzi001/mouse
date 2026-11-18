package overlay

import (
	"testing"

	"gesturectl/internal/winui"
)

// blocks stands in for a font at dpi: each character is a solid block 8 by 16
// pixels at 96 DPI.
func blocks(dpi int) TextFunc {
	return func(text string) ([]byte, int, int) {
		w, h := len([]rune(text))*scaled(8, dpi), scaled(16, dpi)
		mask := make([]byte, w*h)
		for i := range mask {
			mask[i] = 255
		}
		return mask, w, h
	}
}

func colourAt(c *winui.Canvas, x, y int) (r, g, b int) {
	i := (y*c.W + x) * 4
	return int(c.Pix[i+2]), int(c.Pix[i+1]), int(c.Pix[i])
}

func TestDrawScalesWithTheMonitor(t *testing.T) {
	if Draw(nil, 96, blocks(96)) != nil {
		t.Fatal("no rows draw a canvas")
	}
	rows := []Row{{Look: Confirming, Text: "两指右键", HasHand: true, Progress: .5}, {Look: Pause, Text: "已暂停"}}
	small, large := Draw(rows, 96, blocks(96)), Draw(rows, 192, blocks(192))
	if small.W <= 0 || large.W != 2*small.W || large.H != 2*small.H {
		t.Fatalf("%dx%d at 96 DPI but %dx%d at 192 DPI", small.W, small.H, large.W, large.H)
	}
	if corner := small.Pix[3]; corner != 0 {
		t.Errorf("the rounded corner is covered: alpha %d", corner)
	}
}

func TestDrawFillsTheBarAndFlashesFiredRows(t *testing.T) {
	// At 96 DPI the text of a row starts 24 pixels in, after the padding and
	// the hand, and the bar lies 2 pixels under the 16 pixels of text.
	const pad, barLeft, barY = 5, 24, 5 + 16 + 2 + 1
	accent := func(c *winui.Canvas) int {
		n := 0
		for x := 0; x < c.W; x++ {
			if r, _, b := colourAt(c, x, barY); b > r+60 {
				n++
			}
		}
		return n
	}
	half := Draw([]Row{{Look: Confirming, Text: "两指右键", HasHand: true, Progress: .5}}, 96, blocks(96))
	quarter := Draw([]Row{{Look: Confirming, Text: "两指右键", HasHand: true, Progress: .25}}, 96, blocks(96))
	width := half.W - pad - barLeft
	if n := accent(half); n < width/2-2 || n > width/2+2 {
		t.Errorf("half the bar of %d pixels is %d pixels", width, n)
	}
	if n := accent(quarter); n < width/4-2 || n > width/4+2 {
		t.Errorf("a quarter of the bar of %d pixels is %d pixels", width, n)
	}
	fired := Draw([]Row{{Look: Fired, Text: "两指右键", HasHand: true, Progress: 1, Glow: 1}}, 96, blocks(96))
	if r, g, b := colourAt(fired, 3, fired.H/2); g < r+40 || g < b+40 {
		t.Errorf("a fired row is not green: %d %d %d", r, g, b)
	}
	if r, g, b := colourAt(half, 3, half.H/2); g > r+10 || g > b+10 {
		t.Errorf("a row being confirmed is not grey: %d %d %d", r, g, b)
	}
}
