package wininput

import (
	"bytes"
	"errors"
	"testing"
	"unsafe"
)

func TestMouseButtonPacketsAndReleaseOnClose(t *testing.T) {
	i := New()
	var packets []input
	i.send = func(in []input) error { packets = append(packets, in...); return nil }
	i.MouseButton("left", true)
	i.MouseButton("left", false)
	i.MouseButton("right", true)
	if err := i.Close(); err != nil {
		t.Fatal(err)
	}
	if len(packets) != 4 {
		t.Fatalf("packets: %+v", packets)
	}
	for n, want := range []uint32{0x0002, 0x0004, 0x0008, 0x0010} {
		if packets[n].Kind != 0 || packets[n].Mouse.Flags != want {
			t.Fatalf("packet %d: %+v", n, packets[n])
		}
	}
	if unsafe.Sizeof(input{}) != 40 || unsafe.Offsetof(input{}.Mouse) != 8 {
		t.Fatal("incorrect Win64 INPUT ABI")
	}
}
func TestInputFailureIsReportedAndReleaseRetried(t *testing.T) {
	i := New()
	calls := 0
	failure := errors.New("blocked")
	i.send = func(in []input) error {
		calls++
		if calls == 2 {
			return failure
		}
		return nil
	}
	i.MouseButton("left", true)
	i.MouseButton("left", false)
	if !errors.Is(i.Err(), failure) {
		t.Fatalf("missing input error: %v", i.Err())
	}
	i.Close()
	if calls != 3 {
		t.Fatalf("failed release not retried: %d", calls)
	}
}
func TestTakeErrHandsOverEachFailureOnce(t *testing.T) {
	i := New()
	failure := errors.New("blocked")
	i.send = func([]input) error { return failure }
	i.Wheel(120, false)
	if err := i.TakeErr(); !errors.Is(err, failure) {
		t.Fatalf("failure not handed over: %v", err)
	}
	if err := i.TakeErr(); err != nil {
		t.Fatalf("failure handed over twice: %v", err)
	}
	i.send = func([]input) error { return nil }
	i.Wheel(120, false)
	if err := i.TakeErr(); err != nil {
		t.Fatalf("input that went through reported %v", err)
	}
}
func TestUnknownButtonDoesNotInject(t *testing.T) {
	i := New()
	calls := 0
	i.send = func([]input) error { calls++; return nil }
	i.MouseButton("invalid", true)
	if calls != 0 || i.Err() == nil {
		t.Fatal("unknown button accepted")
	}
}
func TestWheelAndUnicodePackets(t *testing.T) {
	i := New()
	var packets []input
	i.send = func(in []input) error { packets = append(packets, in...); return nil }
	i.Wheel(-120, false)
	i.Wheel(120, true)
	i.Text("影")
	if len(packets) != 4 || packets[0].Mouse.Data != 0xffffff88 || packets[0].Mouse.Flags != 0x800 || packets[1].Mouse.Flags != 0x1000 {
		t.Fatalf("wheel: %+v", packets)
	}
	k := (*keyboardInput)(unsafe.Pointer(&packets[2].Mouse))
	if packets[2].Kind != 1 || k.Scan != 0x5f71 || k.Flags != 4 {
		t.Fatalf("unicode: %+v", k)
	}
}
func TestDryRunDoesNotSendInput(t *testing.T) {
	var output bytes.Buffer
	i := NewDryRun(&output)
	i.send = func([]input) error { t.Fatal("dry-run called SendInput"); return nil }
	x, y := i.CursorPos()
	i.MoveCursor(x+5, y+3)
	i.MouseButton("left", true)
	i.MouseButton("left", false)
	a, b := i.CursorPos()
	if a != x+5 || b != y+3 || output.Len() == 0 {
		t.Fatalf("dry-run cursor: %d,%d %s", a, b, output.String())
	}
}
