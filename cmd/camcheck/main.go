// Command camcheck opens the camera for a few seconds, runs hand tracking on the
// live frames and prints timing figures. Frames stay in memory and are discarded;
// with -dump the tracked hands of every frame are written to a file, never the
// image itself.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"gesturectl/assets"
	"gesturectl/internal/camera"
	"gesturectl/internal/vision"
)

// dumpFrame is one line of the -dump file.
type dumpFrame struct {
	Ms    int64         `json:"ms"`
	Hands []vision.Hand `json:"hands"`
}

func main() {
	index := flag.Int("camera", 0, "camera index")
	width := flag.Int("width", 640, "wanted width")
	height := flag.Int("height", 480, "wanted height")
	fps := flag.Int("fps", 30, "wanted frame rate")
	seconds := flag.Float64("seconds", 5, "how long to capture")
	threads := flag.Int("threads", 2, "inference threads")
	listOnly := flag.Bool("list", false, "only list cameras")
	dumpPath := flag.String("dump", "", "write the tracked hands of every frame to this JSON Lines file (landmarks only, no images)")
	flag.Parse()

	names, err := camera.List()
	if err != nil {
		fmt.Println("list:", err)
		os.Exit(1)
	}
	for i, n := range names {
		fmt.Printf("camera %d: %s\n", i, n)
	}
	if *listOnly {
		return
	}

	lib, err := vision.FindRuntimeLibrary()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	models, err := vision.NewModels(vision.ModelOptions{LibraryPath: lib, PalmModel: assets.PalmModel, LandmarkModel: assets.LandmarkModel, Threads: *threads})
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer models.Close()
	tracker := vision.NewTracker(models, vision.DefaultTrackerConfig())

	openStart := time.Now()
	cam, err := camera.Open(camera.Config{Index: *index, Width: *width, Height: *height, FPS: *fps, Mirror: true})
	if err != nil {
		fmt.Println("open:", err)
		os.Exit(1)
	}
	defer cam.Close()
	info := cam.Info()
	fmt.Printf("opened %q in %.0fms: %dx%d %s nominal %.1f fps\n", info.Name, time.Since(openStart).Seconds()*1000, info.Width, info.Height, info.Format, info.FPS)

	var dump *json.Encoder
	if *dumpPath != "" {
		f, err := os.Create(*dumpPath)
		if err != nil {
			fmt.Println("dump:", err)
			os.Exit(1)
		}
		defer f.Close()
		w := bufio.NewWriter(f)
		defer w.Flush()
		dump = json.NewEncoder(w)
	}

	var (
		frame      *vision.Frame
		seq        uint64
		frames     int
		dropped    uint64
		detRuns    int
		detTime    time.Duration
		lmRuns     int
		lmTime     time.Duration
		handFrames [3]int
		latency    time.Duration
		firstFrame time.Duration
	)
	start := time.Now()
	deadline := start.Add(time.Duration(*seconds * float64(time.Second)))
	for time.Now().Before(deadline) {
		f, s, stamp, err := cam.Next(frame, seq, 2*time.Second)
		frame = f
		if err != nil {
			fmt.Println("frame:", err)
			os.Exit(1)
		}
		if frames == 0 {
			firstFrame = time.Since(start)
		} else if s > seq+1 {
			dropped += s - seq - 1
		}
		seq = s
		hands, st, err := tracker.Process(frame, stamp)
		if err != nil {
			fmt.Println("process:", err)
			os.Exit(1)
		}
		latency += time.Since(stamp)
		if dump != nil {
			if err := dump.Encode(dumpFrame{Ms: stamp.Sub(start).Milliseconds(), Hands: hands}); err != nil {
				fmt.Println("dump:", err)
				os.Exit(1)
			}
		}
		frames++
		if st.RanDetection {
			detRuns++
			detTime += st.DetectionTime
		}
		lmRuns += st.LandmarkRuns
		lmTime += st.LandmarkTime
		n := len(hands)
		if n > 2 {
			n = 2
		}
		handFrames[n]++
	}
	el := time.Since(start).Seconds()
	fmt.Printf("first frame after %.0fms; processed %d frames in %.1fs = %.1f fps (camera delivered %.1f fps, %d skipped)\n",
		firstFrame.Seconds()*1000, frames, el, float64(frames)/el, cam.MeasuredFPS(), dropped)
	if detRuns > 0 {
		fmt.Printf("palm detection: %d runs, %.1fms each\n", detRuns, detTime.Seconds()*1000/float64(detRuns))
	}
	if lmRuns > 0 {
		fmt.Printf("landmarks: %d runs, %.1fms each\n", lmRuns, lmTime.Seconds()*1000/float64(lmRuns))
	}
	if frames > 0 {
		fmt.Printf("capture-to-result latency: %.1fms average\n", latency.Seconds()*1000/float64(frames))
	}
	fmt.Printf("frames with 0/1/2 hands: %d/%d/%d\n", handFrames[0], handFrames[1], handFrames[2])
}
