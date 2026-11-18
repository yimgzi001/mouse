// Command visioncheck runs the Go hand pipeline on still images and compares the
// landmarks with the ones produced by the official MediaPipe Python package
// (tools/mp_reference.py writes testdata/reference/mediapipe_landmarks.json).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gesturectl/assets"
	"gesturectl/internal/gesture"
	"gesturectl/internal/vision"
)

type refHand struct {
	Label     string       `json:"label"`
	Score     float64      `json:"score"`
	Landmarks [][3]float64 `json:"landmarks"`
}

type refImage struct {
	Width  int       `json:"width"`
	Height int       `json:"height"`
	Hands  []refHand `json:"hands"`
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
	b := img.Bounds()
	fr := &vision.Frame{W: b.Dx(), H: b.Dy(), Pix: make([]byte, b.Dx()*b.Dy()*3)}
	i := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			fr.Pix[i] = byte(r >> 8)
			fr.Pix[i+1] = byte(g >> 8)
			fr.Pix[i+2] = byte(bl >> 8)
			i += 3
		}
	}
	return fr, nil
}

func main() {
	dir := flag.String("images", "testdata/images", "folder with test images")
	ref := flag.String("ref", "testdata/reference/mediapipe_landmarks.json", "MediaPipe reference landmarks")
	border := flag.String("border", "replicate", "landmark crop border: replicate or zero")
	dump := flag.String("dump", "", "write the Go landmarks to this JSON file")
	features := flag.Bool("features", false, "print the gesture features of every hand")
	palmPath := flag.String("palm", "", "palm detection model file to use instead of the built-in one")
	landmarkPath := flag.String("landmark", "", "hand landmark model file to use instead of the built-in one")
	bench := flag.Int("bench", 0, "time each model over this many runs and exit")
	flag.Parse()

	lib, err := vision.FindRuntimeLibrary()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	palmModel, landmarkModel := assets.PalmModel, assets.LandmarkModel
	if *palmPath != "" {
		if palmModel, err = os.ReadFile(*palmPath); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}
	if *landmarkPath != "" {
		if landmarkModel, err = os.ReadFile(*landmarkPath); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}
	if *bench > 0 {
		for _, threads := range []int{1, 2, 4} {
			m, err := vision.NewModels(vision.ModelOptions{LibraryPath: lib, PalmModel: palmModel, LandmarkModel: landmarkModel, Threads: threads})
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			if _, _, err := m.Benchmark(5); err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			palm, lm, _ := m.Benchmark(*bench)
			fmt.Printf("threads=%d  palm=%.1fms  landmark=%.1fms\n", threads, float64(palm.Microseconds())/1000, float64(lm.Microseconds())/1000)
			m.Close()
		}
		return
	}
	models, err := vision.NewModels(vision.ModelOptions{LibraryPath: lib, PalmModel: palmModel, LandmarkModel: landmarkModel})
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer models.Close()
	models.ReplicateBorder = *border == "replicate"
	fmt.Println("onnxruntime", vision.RuntimeVersion(), "border", *border)

	refs := map[string]refImage{}
	if data, err := os.ReadFile(*ref); err == nil {
		if err := json.Unmarshal(data, &refs); err != nil {
			fmt.Println("reference file:", err)
			os.Exit(1)
		}
	} else {
		fmt.Println("no reference file, printing detections only")
	}

	paths, _ := filepath.Glob(filepath.Join(*dir, "*.jpg"))
	sort.Strings(paths)
	dumped := map[string][]vision.Hand{}
	var worst, sum float64
	var count int
	failed := false
	for _, p := range paths {
		name := filepath.Base(p)
		fr, err := loadFrame(p)
		if err != nil {
			fmt.Println(name, "load error:", err)
			failed = true
			continue
		}
		t0 := time.Now()
		hands, err := models.DetectStill(fr, 0.5, 0.5, 2)
		el := time.Since(t0)
		if err != nil {
			fmt.Println(name, "error:", err)
			failed = true
			continue
		}
		dumped[name] = hands
		r, hasRef := refs[name]
		fmt.Printf("%-26s %dx%d  hands=%d (ref %d)  %.1fms\n", name, fr.W, fr.H, len(hands), len(r.Hands), float64(el.Microseconds())/1000)
		if hasRef && len(hands) != len(r.Hands) {
			failed = true
		}
		for _, h := range hands {
			line := fmt.Sprintf("    presence=%.3f left=%.3f", h.Presence, h.LeftScore)
			if hasRef {
				best, bestMean, bestMax := -1, math.Inf(1), 0.0
				for i, rh := range r.Hands {
					var s, mx float64
					for k := 0; k < vision.NumLandmarks; k++ {
						d := math.Hypot(float64(h.Landmarks[k].X)-rh.Landmarks[k][0], float64(h.Landmarks[k].Y)-rh.Landmarks[k][1])
						s += d
						mx = math.Max(mx, d)
					}
					if s/vision.NumLandmarks < bestMean {
						best, bestMean, bestMax = i, s/vision.NumLandmarks, mx
					}
				}
				if best >= 0 {
					rh := r.Hands[best]
					line += fmt.Sprintf("  ref=%s %.3f  mean=%.2fpx max=%.2fpx  roi=%.0fpx", rh.Label, rh.Score, bestMean, bestMax, h.ROI.W)
					sum += bestMean
					count++
					worst = math.Max(worst, bestMax)
				}
			}
			fmt.Println(line)
			if *features {
				tuning := gesture.DefaultTuning()
				ft := gesture.Extract(&h, &tuning)
				fmt.Printf("      open T%.2f I%.2f M%.2f R%.2f P%.2f  touch %.2f %.2f %.2f %.2f  palm=%.0fpx hand=%.0f thumb=%.0f index=%.0f middle=%.0f nz=%.2f  tbend=%.0f tspread=%.2f\n",
					ft.Openness[0], ft.Openness[1], ft.Openness[2], ft.Openness[3], ft.Openness[4],
					ft.TouchDist[0], ft.TouchDist[1], ft.TouchDist[2], ft.TouchDist[3],
					ft.PalmSize, ft.HandAngle, ft.ThumbAngle, ft.IndexAngle, ft.MiddleAngle, ft.PalmNormalZ, ft.ThumbBend, ft.ThumbSpread)
			}
		}
	}
	if count > 0 {
		fmt.Printf("SUMMARY hands=%d  mean error=%.2fpx  worst landmark=%.2fpx\n", count, sum/float64(count), worst)
	}
	if *dump != "" {
		data, _ := json.Marshal(dumped)
		if err := os.WriteFile(*dump, data, 0o644); err != nil {
			fmt.Println("dump:", err)
		}
	}
	if failed {
		os.Exit(1)
	}
}
