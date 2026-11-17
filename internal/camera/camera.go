// Package camera captures webcam frames through Windows Media Foundation.
//
// Frames are never written to disk. Only the most recent frame is kept; older
// ones are dropped so that a slow consumer never works on stale images.
package camera

/*
#cgo LDFLAGS: -lmfplat -lmf -lmfreadwrite -lmfuuid -lole32 -luuid
#include <stdlib.h>
#include "mfcam.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"gesturectl/internal/vision"
)

// Config selects the device and the wanted capture mode.
type Config struct {
	Index  int
	Width  int
	Height int
	FPS    int
	// Mirror flips the image horizontally, like a mirror, so that moving a hand
	// to the right moves it to the right in the image.
	Mirror bool
}

// Info describes the mode the camera actually runs in.
type Info struct {
	Name   string
	Width  int
	Height int
	FPS    float64
	Format string
}

// ErrClosed is returned by Next after Close.
var ErrClosed = errors.New("camera closed")

// ErrTimeout is returned by Next when no new frame arrived in time.
var ErrTimeout = errors.New("no camera frame in time")

func hresult(what string, hr C.long) error {
	return fmt.Errorf("%s failed (HRESULT 0x%08X)", what, uint32(hr))
}

func listOnThisThread() ([]string, error) {
	buf := make([]byte, 8192)
	n := C.mfcam_list((*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf)))
	if n < 0 {
		return nil, hresult("listing cameras", n)
	}
	text := C.GoString((*C.char)(unsafe.Pointer(&buf[0])))
	names := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if n == 0 {
		return nil, nil
	}
	return names, nil
}

// List returns the names of the available cameras, in device-index order.
func List() ([]string, error) {
	type result struct {
		names []string
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if hr := C.mfcam_startup(); hr != C.MFCAM_OK {
			ch <- result{nil, hresult("starting Media Foundation", hr)}
			return
		}
		defer C.mfcam_shutdown()
		names, err := listOnThisThread()
		ch <- result{names, err}
	}()
	r := <-ch
	return r.names, r.err
}

// Capture is an open camera delivering frames on its own OS thread.
type Capture struct {
	info Info

	mu      sync.Mutex
	latest  *vision.Frame
	seq     uint64
	stamp   time.Time
	fps     float64
	err     error
	fresh   chan struct{}
	stop    chan struct{}
	done    chan struct{}
	stopped sync.Once
}

// Open starts capturing. It returns once the camera delivered its mode, or failed.
func Open(cfg Config) (*Capture, error) {
	c := &Capture{
		fresh: make(chan struct{}, 1),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	ready := make(chan error, 1)
	go c.run(cfg, ready)
	if err := <-ready; err != nil {
		<-c.done
		return nil, err
	}
	return c, nil
}

func (c *Capture) run(cfg Config, ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(c.done)

	if hr := C.mfcam_startup(); hr != C.MFCAM_OK {
		ready <- hresult("starting Media Foundation", hr)
		return
	}
	defer C.mfcam_shutdown()

	names, _ := listOnThisThread()
	if cfg.Index < 0 || cfg.Index >= len(names) {
		ready <- fmt.Errorf("camera %d not found (%d camera(s) connected)", cfg.Index, len(names))
		return
	}

	var cam *C.mfcam
	if hr := C.mfcam_open(C.int(cfg.Index), C.int(cfg.Width), C.int(cfg.Height), C.int(cfg.FPS), &cam); hr != C.MFCAM_OK {
		ready <- hresult(fmt.Sprintf("opening camera %q", names[cfg.Index]), hr)
		return
	}
	defer C.mfcam_close(cam)

	var w, h, num, den C.int
	format := make([]byte, 16)
	C.mfcam_info(cam, &w, &h, &num, &den, (*C.char)(unsafe.Pointer(&format[0])))
	c.info = Info{
		Name:   names[cfg.Index],
		Width:  int(w),
		Height: int(h),
		Format: C.GoString((*C.char)(unsafe.Pointer(&format[0]))),
	}
	if den > 0 {
		c.info.FPS = float64(num) / float64(den)
	}
	size := int(w) * int(h) * 3
	newFrame := func() *vision.Frame {
		return &vision.Frame{W: int(w), H: int(h), Pix: make([]byte, size)}
	}
	back := newFrame()
	c.latest = newFrame()
	ready <- nil

	mirror := C.int(0)
	if cfg.Mirror {
		mirror = 1
	}
	var last time.Time
	for {
		select {
		case <-c.stop:
			return
		default:
		}
		hr := C.mfcam_read(cam, (*C.uchar)(unsafe.Pointer(&back.Pix[0])), C.int(len(back.Pix)), mirror)
		if hr == C.MFCAM_NO_FRAME {
			continue
		}
		if hr != C.MFCAM_OK {
			c.mu.Lock()
			c.err = hresult("reading from the camera", hr)
			c.mu.Unlock()
			return
		}
		now := time.Now()
		c.mu.Lock()
		back, c.latest = c.latest, back
		if len(back.Pix) != size {
			// The consumer handed in a buffer of the wrong size; replace it.
			back = newFrame()
		}
		c.seq++
		c.stamp = now
		if !last.IsZero() {
			if dt := now.Sub(last).Seconds(); dt > 0 {
				inst := 1 / dt
				if c.fps == 0 {
					c.fps = inst
				} else {
					c.fps += 0.1 * (inst - c.fps)
				}
			}
		}
		last = now
		c.mu.Unlock()
		select {
		case c.fresh <- struct{}{}:
		default:
		}
	}
}

// Info returns the negotiated capture mode.
func (c *Capture) Info() Info { return c.info }

// MeasuredFPS is the frame rate actually delivered by the camera.
func (c *Capture) MeasuredFPS() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fps
}

// Next waits for a frame newer than lastSeq and returns it together with its
// sequence number and arrival time. The caller passes in a frame it no longer
// needs (or nil); its buffer is reused, so the returned frame is only valid
// until it is passed to Next again.
func (c *Capture) Next(spare *vision.Frame, lastSeq uint64, timeout time.Duration) (*vision.Frame, uint64, time.Time, error) {
	var timer <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		timer = t.C
	}
	for {
		c.mu.Lock()
		if c.seq != lastSeq && c.seq != 0 {
			frame := c.latest
			if spare == nil {
				spare = &vision.Frame{W: frame.W, H: frame.H, Pix: make([]byte, len(frame.Pix))}
			}
			c.latest = spare
			seq, stamp := c.seq, c.stamp
			c.mu.Unlock()
			return frame, seq, stamp, nil
		}
		err := c.err
		c.mu.Unlock()

		select {
		case <-c.fresh:
		case <-c.done:
			if err == nil {
				c.mu.Lock()
				err = c.err
				c.mu.Unlock()
			}
			if err == nil {
				err = ErrClosed
			}
			return spare, lastSeq, time.Time{}, err
		case <-timer:
			return spare, lastSeq, time.Time{}, ErrTimeout
		}
	}
}

// Close stops capturing and releases the camera.
func (c *Capture) Close() {
	_ = c.CloseContext(context.Background())
}

// CloseContext requests shutdown and waits for native capture cleanup. A broken
// device can leave synchronous Media Foundation ReadSample blocked indefinitely.
// On deadline the read still owns its resources; a CLI can then exit the process
// without waiting forever. Do not reopen that device until cleanup or process exit.
func (c *Capture) CloseContext(ctx context.Context) error {
	c.stopped.Do(func() { close(c.stop) })
	// Prefer successful completion if both done and the deadline are ready.
	select {
	case <-c.done:
		return nil
	default:
	}
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("camera cleanup is still waiting for its native driver: %w", ctx.Err())
	}
}
