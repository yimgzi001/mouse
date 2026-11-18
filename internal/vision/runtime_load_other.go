//go:build !windows

package vision

func preloadRuntime(string) error { return nil }
