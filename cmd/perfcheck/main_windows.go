//go:build windows

// Command perfcheck measures paced tracking on fixed test images. It never
// opens the camera, saves camera frames, or injects desktop input.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"syscall"
	"time"
	"unsafe"

	"gesturectl/assets"
	"gesturectl/internal/vision"
)

var getProcessMemoryInfo = syscall.NewLazyDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

// processMemoryCounters is PROCESS_MEMORY_COUNTERS_EX.
type processMemoryCounters struct {
	cb, pageFaults                                 uint32
	peakWorkingSet, workingSet                     uintptr
	quotaPeakPagedPool, quotaPagedPool             uintptr
	quotaPeakNonPagedPool, quotaNonPagedPool       uintptr
	pagefileUsage, peakPagefileUsage, privateUsage uintptr
}

func loadFrame(path string) (*vision.Frame, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	bounds := img.Bounds()
	fr := &vision.Frame{W: bounds.Dx(), H: bounds.Dy(), Pix: make([]byte, bounds.Dx()*bounds.Dy()*3)}
	for y := 0; y < fr.H; y++ {
		for x := 0; x < fr.W; x++ {
			r, g, b, _ := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			i := (y*fr.W + x) * 3
			fr.Pix[i], fr.Pix[i+1], fr.Pix[i+2] = byte(r>>8), byte(g>>8), byte(b>>8)
		}
	}
	return fr, nil
}

func processCPU() (time.Duration, error) {
	h, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0, err
	}
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return 0, err
	}
	ticks := (uint64(kernel.HighDateTime)<<32 | uint64(kernel.LowDateTime)) + (uint64(user.HighDateTime)<<32 | uint64(user.LowDateTime))
	return time.Duration(ticks * 100), nil
}

func processMemory() (processMemoryCounters, error) {
	c := processMemoryCounters{cb: uint32(unsafe.Sizeof(processMemoryCounters{}))}
	h, err := syscall.GetCurrentProcess()
	if err != nil {
		return c, err
	}
	if r, _, err := getProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&c)), uintptr(c.cb)); r == 0 {
		return c, fmt.Errorf("GetProcessMemoryInfo: %w", err)
	}
	return c, nil
}

// addMemory adds the process working set and private bytes and the Go heap in
// use, in MiB, to report.
func addMemory(report map[string]any) error {
	c, err := processMemory()
	if err != nil {
		return err
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	const mib = 1 << 20
	report["workingSetMB"] = float64(c.workingSet) / mib
	report["privateMB"] = float64(c.privateUsage) / mib
	report["goHeapMB"] = float64(ms.HeapInuse) / mib
	return nil
}

func quantile(v []time.Duration, share float64) float64 {
	return float64(v[max(0, int(math.Ceil(float64(len(v))*share))-1)]) / float64(time.Millisecond)
}

func run() error {
	frames := flag.Int("frames", 120, "measured frames per scenario")
	fps := flag.Int("fps", 30, "pace frames at this rate; not an unbounded throughput test")
	threads := flag.Int("threads", 2, "inference threads")
	spin := flag.String("spin", "run", "worker spinning: run (only during inference), always (old), off")
	images := flag.String("images", "testdata/images", "fixed test images")
	detect := flag.Duration("detect", vision.DefaultTrackerConfig().DetectInterval, "how often to look for another hand while one is followed")
	idle := flag.Duration("idle", vision.DefaultTrackerConfig().IdleDetectInterval, "how often to look for a hand while none is followed")
	cpuProfile := flag.String("cpuprofile", "", "write a CPU profile of all scenarios to this file")
	flag.Parse()
	if *frames < 1 || *frames > 100000 || *fps < 1 || *fps > 240 || *threads < 1 || *threads > 64 {
		return fmt.Errorf("frames must be 1..100000, fps 1..240, threads 1..64")
	}
	lib, err := vision.FindRuntimeLibrary()
	if err != nil {
		return err
	}
	m, err := vision.NewModels(vision.ModelOptions{LibraryPath: lib, PalmModel: assets.PalmModel, LandmarkModel: assets.LandmarkModel,
		Threads: *threads, WorkerSpinning: *spin})
	if err != nil {
		return err
	}
	defer m.Close()
	loaded := map[string]any{"scenario": "loaded"}
	if err := addMemory(loaded); err != nil {
		return err
	}
	if err := json.NewEncoder(os.Stdout).Encode(loaded); err != nil {
		return err
	}
	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			return err
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			return err
		}
		defer pprof.StopCPUProfile()
	}
	interval := time.Second / time.Duration(*fps)
	for _, scenario := range []struct{ name, file string }{{"idle", ""}, {"one", "pointing_up.jpg"}, {"two", "right_hands.jpg"}} {
		frame := &vision.Frame{W: 640, H: 480, Pix: make([]byte, 640*480*3)}
		if scenario.file != "" {
			frame, err = loadFrame(filepath.Join(*images, scenario.file))
			if err != nil {
				return err
			}
		}
		cfg := vision.DefaultTrackerConfig()
		cfg.DetectInterval, cfg.IdleDetectInterval = *detect, *idle
		tracker := vision.NewTracker(m, cfg)
		warmStart := time.Now().Add(-20 * interval)
		for n := 0; n < 20; n++ {
			if _, _, err := tracker.Process(frame, warmStart.Add(time.Duration(n)*interval)); err != nil {
				return err
			}
		}
		samples := make([]time.Duration, 0, *frames)
		handFrames := map[int]int{}
		var total, detectTime, landmarkTime time.Duration
		var detections, landmarkRuns, overBudget int
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		faultsBefore, err := processMemory()
		if err != nil {
			return err
		}
		cpuStart, err := processCPU()
		if err != nil {
			return err
		}
		start := time.Now()
		for n := 0; n < *frames; n++ {
			time.Sleep(time.Until(start.Add(time.Duration(n) * interval)))
			t0 := time.Now()
			hands, stats, err := tracker.Process(frame, t0)
			elapsed := time.Since(t0)
			if err != nil {
				return err
			}
			samples = append(samples, elapsed)
			total += elapsed
			detectTime += stats.DetectionTime
			landmarkTime += stats.LandmarkTime
			landmarkRuns += stats.LandmarkRuns
			handFrames[len(hands)]++
			if stats.RanDetection {
				detections++
			}
			if elapsed > interval {
				overBudget++
			}
		}
		time.Sleep(time.Until(start.Add(time.Duration(*frames) * interval)))
		wall := time.Since(start)
		cpuEnd, err := processCPU()
		if err != nil {
			return err
		}
		runtime.ReadMemStats(&after)
		faultsAfter, err := processMemory()
		if err != nil {
			return err
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		report := map[string]any{
			"scenario": scenario.name, "image": scenario.file, "width": frame.W, "height": frame.H,
			"frames": *frames, "targetFPS": *fps, "effectiveFPS": float64(*frames) / wall.Seconds(),
			"threads": *threads, "spinMode": *spin,
			"detectMs": detect.Milliseconds(), "idleMs": idle.Milliseconds(),
			"logicalProcessors": runtime.NumCPU(), "runtime": vision.RuntimeVersion(),
			"cpuTotalPct": 100 * float64(cpuEnd-cpuStart) / float64(wall) / float64(runtime.NumCPU()),
			"meanMs":      float64(total) / float64(*frames) / float64(time.Millisecond),
			"p50Ms":       quantile(samples, .5), "p95Ms": quantile(samples, .95), "maxMs": quantile(samples, 1),
			"overBudgetFrames": overBudget, "handFrames": handFrames,
			"detections": detections, "detectionTotalMs": float64(detectTime) / float64(time.Millisecond),
			"landmarkRuns": landmarkRuns, "landmarkTotalMs": float64(landmarkTime) / float64(time.Millisecond),
			"allocKBPerFrame":    float64(after.TotalAlloc-before.TotalAlloc) / 1024 / float64(*frames),
			"gcRuns":             after.NumGC - before.NumGC,
			"pageFaultsPerFrame": float64(faultsAfter.pageFaults-faultsBefore.pageFaults) / float64(*frames),
		}
		if err := addMemory(report); err != nil {
			return err
		}
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
