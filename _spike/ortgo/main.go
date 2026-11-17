// Throwaway spike: checks that Go + cgo (MinGW gcc) can load onnxruntime.dll and run a model.
package main

import (
	"fmt"
	"os"
	"time"

	ort "github.com/yalue/onnxruntime_go"
)

func fail(stage string, err error) {
	fmt.Println(stage, "error:", err)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: ortgo <onnxruntime.dll> <model.onnx>")
		os.Exit(2)
	}
	ort.SetSharedLibraryPath(os.Args[1])
	if err := ort.InitializeEnvironment(); err != nil {
		fail("init", err)
	}
	defer ort.DestroyEnvironment()
	fmt.Println("onnxruntime version:", ort.GetVersion())

	ins, outs, err := ort.GetInputOutputInfo(os.Args[2])
	if err != nil {
		fail("model info", err)
	}
	fmt.Println("input:", ins[0].Name, ins[0].Dimensions, "output:", outs[0].Name, outs[0].Dimensions)

	in, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 1, 28, 28))
	if err != nil {
		fail("input tensor", err)
	}
	defer in.Destroy()
	out, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 10))
	if err != nil {
		fail("output tensor", err)
	}
	defer out.Destroy()

	sess, err := ort.NewAdvancedSession(os.Args[2],
		[]string{ins[0].Name}, []string{outs[0].Name},
		[]ort.Value{in}, []ort.Value{out}, nil)
	if err != nil {
		fail("session", err)
	}
	defer sess.Destroy()

	data := in.GetData()
	for i := range data {
		data[i] = float32(i%28) / 28
	}

	const n = 2000
	t0 := time.Now()
	for i := 0; i < n; i++ {
		if err := sess.Run(); err != nil {
			fail("run", err)
		}
	}
	el := time.Since(t0)
	fmt.Printf("runs=%d avg=%.1fus\n", n, float64(el.Microseconds())/n)
	fmt.Println("output:", out.GetData())
}
