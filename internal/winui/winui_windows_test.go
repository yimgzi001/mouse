//go:build windows

package winui

import (
	"testing"
	"unsafe"
)

func TestScaleRoundsToPixels(t *testing.T) {
	for _, c := range []struct{ v, dpi, want int }{{24, 96, 24}, {24, 192, 48}, {9, 144, 14}, {-2, 192, -4}, {0, 240, 0}} {
		if got := Scale(c.v, c.dpi); got != c.want {
			t.Errorf("Scale(%d, %d) = %d, want %d", c.v, c.dpi, got, c.want)
		}
	}
}

func TestVisualStylesCanBeEnabledMoreThanOnce(t *testing.T) {
	if !EnableVisualStyles() || !EnableVisualStyles() {
		t.Fatal("the themed common controls are not available")
	}
}

func TestTextMaskCoversTheInk(t *testing.T) {
	font := Font(12, 96, 700, Grey, "Microsoft YaHei UI")
	if font == 0 {
		t.Fatal("no font")
	}
	defer gdi.NewProc("DeleteObject").Call(font)
	mask, width, height := TextMask(font, "手势 Tab")
	if width <= 0 || height <= 0 || len(mask) != width*height {
		t.Fatalf("mask of %d bytes for %dx%d", len(mask), width, height)
	}
	ink, strongest := 0, byte(0)
	for _, v := range mask {
		if v > 0 {
			ink++
		}
		strongest = max(strongest, v)
	}
	if ink == 0 || ink == len(mask) || strongest < 200 {
		t.Fatalf("%d of %d pixels carry ink, the strongest %d", ink, len(mask), strongest)
	}
	if _, longer, _ := TextMask(font, "手势 Tab 手势 Tab"); longer <= width {
		t.Fatalf("longer text measures %d, shorter %d", longer, width)
	}
	if mask, width, height := TextMask(font, ""); mask != nil || width != 0 || height != 0 {
		t.Fatal("empty text has a mask")
	}
}

func TestBitmapHasTheSizeOfTheCanvas(t *testing.T) {
	c := NewCanvas(7, 5)
	c.RoundRect(0, 0, 7, 5, 0, RGB{R: 1, G: 2, B: 3}, 1)
	bitmap := Bitmap(c)
	if bitmap == 0 {
		t.Fatal("no bitmap")
	}
	defer gdi.NewProc("DeleteObject").Call(bitmap)
	var info struct {
		Type, Width, Height, WidthBytes int32
		Planes, BitsPixel               uint16
		Bits                            uintptr
	}
	gdi.NewProc("GetObjectW").Call(bitmap, unsafe.Sizeof(info), uintptr(unsafe.Pointer(&info)))
	if info.Width != 7 || info.Height != 5 || info.BitsPixel != 32 {
		t.Fatalf("bitmap is %dx%d at %d bits", info.Width, info.Height, info.BitsPixel)
	}
	if Bitmap(NewCanvas(0, 3)) != 0 {
		t.Fatal("an empty canvas made a bitmap")
	}
}
