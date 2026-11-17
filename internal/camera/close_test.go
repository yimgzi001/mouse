package camera

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A stuck native ReadSample never closes done. Shutdown must still return by
// the caller's deadline so the CLI can exit after releasing desktop input.
func TestCloseContextReturnsWhenNativeReadStalls(t *testing.T) {
	c := &Capture{stop: make(chan struct{}), done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := c.CloseContext(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	select {
	case <-c.stop:
	default:
		t.Fatal("capture stop was not signalled")
	}
	// Native completion after the timeout still permits normal repeated Close.
	close(c.done)
	if err = c.CloseContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestCloseContextReturnsImmediatelyForCompletedCapture(t *testing.T) {
	c := &Capture{stop: make(chan struct{}), done: make(chan struct{})}
	close(c.done)
	if err := c.CloseContext(context.Background()); err != nil {
		t.Fatal(err)
	}
}
