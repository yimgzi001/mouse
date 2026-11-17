package vision

import (
	"fmt"
	"os"
	"path/filepath"
)

// FindRuntimeLibrary locates onnxruntime.dll next to the executable or in the
// working directory, with or without a "runtime" sub-folder.
func FindRuntimeLibrary() (string, error) {
	const name = "onnxruntime.dll"
	var dirs []string
	if exe, err := os.Executable(); err == nil {
		d := filepath.Dir(exe)
		dirs = append(dirs, d, filepath.Join(d, "runtime"))
	}
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, wd, filepath.Join(wd, "runtime"))
	}
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s not found; looked in %v", name, dirs)
}
