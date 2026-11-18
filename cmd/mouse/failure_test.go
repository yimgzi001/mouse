package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestFailureTextAddsTheHint(t *testing.T) {
	cause := errors.New("camera busy")
	err := fmt.Errorf("start: %w", withHint(cause, cameraHint))
	if text := failureText(err); !strings.Contains(text, "start: camera busy") || !strings.HasSuffix(text, cameraHint) {
		t.Fatalf("text = %q", text)
	}
	if !errors.Is(err, cause) {
		t.Fatal("the hint hides the cause")
	}
	if text := failureText(cause); strings.Contains(text, cameraHint) {
		t.Fatalf("a failure without a hint got one: %q", text)
	}
}
