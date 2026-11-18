//go:build windows

package winui

import (
	"math"
	"sync"
	"syscall"
	"unsafe"
)

var (
	kernel = syscall.NewLazyDLL("kernel32.dll")
	user   = syscall.NewLazyDLL("user32.dll")
	gdi    = syscall.NewLazyDLL("gdi32.dll")
	// comctl32 is first loaded after the activation context below exists, so
	// that this is version 6.
	comctl = syscall.NewLazyDLL("comctl32.dll")
)

type activationContext struct {
	Size, Flags            uint32
	Source                 *uint16
	Architecture, Language uint16
	AssemblyDirectory      *uint16
	ResourceName           uintptr
	ApplicationName        *uint16
	Module                 uintptr
}

var styles struct {
	once    sync.Once
	enabled bool
	// thread is the activation context to activate on each window thread when
	// it could not become the default of the process.
	thread uintptr
}

// EnableVisualStyles makes the controls created afterwards the themed ones of
// version 6 of the common controls. The program has no manifest to ask for
// them, so this borrows the manifest that shell32.dll carries for the purpose.
// Call it on each thread that creates windows, before the first one.
func EnableVisualStyles() bool {
	styles.once.Do(func() {
		directory := make([]uint16, 260)
		n, _, _ := kernel.NewProc("GetSystemDirectoryW").Call(uintptr(unsafe.Pointer(&directory[0])), uintptr(len(directory)))
		if n == 0 || int(n) >= len(directory) {
			return
		}
		source, err := syscall.UTF16PtrFromString(syscall.UTF16ToString(directory) + `\shell32.dll`)
		if err != nil {
			return
		}
		const assemblyDirectoryValid, resourceNameValid, setProcessDefault = 0x4, 0x8, 0x10
		context := activationContext{Flags: assemblyDirectoryValid | resourceNameValid | setProcessDefault, Source: source, AssemblyDirectory: &directory[0], ResourceName: 124}
		context.Size = uint32(unsafe.Sizeof(context))
		create := kernel.NewProc("CreateActCtxW")
		handle, _, _ := create.Call(uintptr(unsafe.Pointer(&context)))
		if handle == ^uintptr(0) {
			context.Flags &^= setProcessDefault
			if handle, _, _ = create.Call(uintptr(unsafe.Pointer(&context))); handle == ^uintptr(0) {
				return
			}
			styles.thread = handle
		}
		styles.enabled = true
	})
	if !styles.enabled {
		return false
	}
	if styles.thread != 0 {
		var cookie uintptr
		kernel.NewProc("ActivateActCtx").Call(styles.thread, uintptr(unsafe.Pointer(&cookie)))
	}
	classes := struct{ Size, Classes uint32 }{8, 0x40ff}
	comctl.NewProc("InitCommonControlsEx").Call(uintptr(unsafe.Pointer(&classes)))
	return true
}

// DPI returns the dots per inch a window is drawn at; 96 is 100% scaling.
func DPI(hwnd uintptr) int {
	if get := user.NewProc("GetDpiForWindow"); get.Find() == nil {
		if dpi, _, _ := get.Call(hwnd); dpi != 0 {
			return int(dpi)
		}
	}
	return 96
}

// Scale converts a length laid out at 96 DPI to pixels at dpi.
func Scale(v, dpi int) int { return int(math.Round(float64(v) * float64(dpi) / 96)) }

// Font qualities.
const (
	Grey      = 4
	ClearType = 5
)

// Font creates a font of a size in points for a window at dpi. The caller
// deletes it.
func Font(points float64, dpi, weight, quality int, face string) uintptr {
	name, _ := syscall.UTF16PtrFromString(face)
	height := -int(math.Round(points * float64(dpi) / 72))
	font, _, _ := gdi.NewProc("CreateFontW").Call(uintptr(height), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, uintptr(quality), 0, uintptr(unsafe.Pointer(name)))
	return font
}

type bitmapHeader struct {
	Size                   uint32
	Width, Height          int32
	Planes, Bits           uint16
	Compression, ImageSize uint32
	XPerMetre, YPerMetre   int32
	Used, Important        uint32
}

func section(dc uintptr, width, height int) (bitmap, bits uintptr) {
	header := bitmapHeader{Size: 40, Width: int32(width), Height: -int32(height), Planes: 1, Bits: 32}
	bitmap, _, _ = gdi.NewProc("CreateDIBSection").Call(dc, uintptr(unsafe.Pointer(&header)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	return bitmap, bits
}

// Bitmap copies a canvas into a new 32-bit bitmap. The caller deletes it.
func Bitmap(c *Canvas) uintptr {
	if c.W == 0 || c.H == 0 {
		return 0
	}
	bitmap, bits := section(0, c.W, c.H)
	if bitmap != 0 && bits != 0 {
		kernel.NewProc("RtlMoveMemory").Call(bits, uintptr(unsafe.Pointer(&c.Pix[0])), uintptr(len(c.Pix)))
	}
	return bitmap
}

// TextMask draws one line of text in a font and returns, row by row, how much
// of each pixel the text covers, from 0 to 255. Canvas.Mask paints it. The
// font should have the quality Grey, so that every channel carries coverage.
func TextMask(font uintptr, text string) (mask []byte, width, height int) {
	chars, err := syscall.UTF16FromString(text)
	if err != nil || len(chars) < 2 {
		return nil, 0, 0
	}
	count := uintptr(len(chars) - 1)
	dc, _, _ := gdi.NewProc("CreateCompatibleDC").Call(0)
	if dc == 0 {
		return nil, 0, 0
	}
	defer gdi.NewProc("DeleteDC").Call(dc)
	selectObject := gdi.NewProc("SelectObject")
	previous, _, _ := selectObject.Call(dc, font)
	defer selectObject.Call(dc, previous)
	var size struct{ X, Y int32 }
	gdi.NewProc("GetTextExtentPoint32W").Call(dc, uintptr(unsafe.Pointer(&chars[0])), count, uintptr(unsafe.Pointer(&size)))
	width, height = int(size.X), int(size.Y)
	if width <= 0 || height <= 0 {
		return nil, 0, 0
	}
	bitmap, bits := section(dc, width, height)
	if bitmap == 0 || bits == 0 {
		return nil, 0, 0
	}
	defer gdi.NewProc("DeleteObject").Call(bitmap)
	blank, _, _ := selectObject.Call(dc, bitmap)
	defer selectObject.Call(dc, blank)
	gdi.NewProc("SetBkMode").Call(dc, 1)
	gdi.NewProc("SetTextColor").Call(dc, 0xffffff)
	gdi.NewProc("TextOutW").Call(dc, 0, 0, uintptr(unsafe.Pointer(&chars[0])), count)
	gdi.NewProc("GdiFlush").Call()
	pixels := make([]byte, width*height*4)
	kernel.NewProc("RtlMoveMemory").Call(uintptr(unsafe.Pointer(&pixels[0])), bits, uintptr(len(pixels)))
	mask = make([]byte, width*height)
	for i := range mask {
		mask[i] = pixels[i*4+1]
	}
	return mask, width, height
}

// Capture renders a window, frame included, into an opaque canvas. Tests use
// it to look at what a window draws.
func Capture(hwnd uintptr) *Canvas {
	var r struct{ Left, Top, Right, Bottom int32 }
	user.NewProc("GetWindowRect").Call(hwnd, uintptr(unsafe.Pointer(&r)))
	c := NewCanvas(int(r.Right-r.Left), int(r.Bottom-r.Top))
	dc, _, _ := gdi.NewProc("CreateCompatibleDC").Call(0)
	if dc == 0 || len(c.Pix) == 0 {
		return c
	}
	defer gdi.NewProc("DeleteDC").Call(dc)
	bitmap, bits := section(dc, c.W, c.H)
	if bitmap == 0 || bits == 0 {
		return c
	}
	defer gdi.NewProc("DeleteObject").Call(bitmap)
	blank, _, _ := gdi.NewProc("SelectObject").Call(dc, bitmap)
	defer gdi.NewProc("SelectObject").Call(dc, blank)
	user.NewProc("PrintWindow").Call(hwnd, dc, 2)
	gdi.NewProc("GdiFlush").Call()
	kernel.NewProc("RtlMoveMemory").Call(uintptr(unsafe.Pointer(&c.Pix[0])), bits, uintptr(len(c.Pix)))
	for i := 3; i < len(c.Pix); i += 4 {
		c.Pix[i] = 255
	}
	return c
}
