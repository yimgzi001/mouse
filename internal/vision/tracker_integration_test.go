package vision_test

import (
	"image"
	_ "image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gesturectl/assets"
	"gesturectl/internal/gesture"
	"gesturectl/internal/vision"
)

// Repeating the same camera frame must not turn a correctly detected hand into
// another pose as the landmark-derived crop feeds back into the next frame.
func TestTrackerKeepsStaticHandPose(t *testing.T) {
	lib, err := filepath.Abs("../../runtime/onnxruntime.dll")
	if err != nil {
		t.Fatal(err)
	}
	models, err := vision.NewModels(vision.ModelOptions{LibraryPath: lib, PalmModel: assets.PalmModel, LandmarkModel: assets.LandmarkModel})
	if err != nil {
		t.Fatal(err)
	}
	defer models.Close()
	poses, err := gesture.NewPoseSet(gesture.BuiltinPoses())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ image, pose string }{{"pointing_up.jpg", "point"}, {"victory.jpg", "two"}, {"fist.jpg", "fist"}} {
		t.Run(tc.pose, func(t *testing.T) {
			file, err := os.Open(filepath.Join("../../testdata/images", tc.image))
			if err != nil {
				t.Fatal(err)
			}
			img, _, err := image.Decode(file)
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
			b := img.Bounds()
			frame := &vision.Frame{W: b.Dx(), H: b.Dy(), Pix: make([]byte, b.Dx()*b.Dy()*3)}
			for y := 0; y < frame.H; y++ {
				for x := 0; x < frame.W; x++ {
					r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
					k := (y*frame.W + x) * 3
					frame.Pix[k] = byte(r >> 8)
					frame.Pix[k+1] = byte(g >> 8)
					frame.Pix[k+2] = byte(bl >> 8)
				}
			}
			cfg := vision.DefaultTrackerConfig()
			cfg.MaxHands = 1
			tracker := vision.NewTracker(models, cfg)
			r := gesture.NewRecognizer(gesture.DefaultConfig(), poses)
			r.Configure(gesture.DefaultConfig(), poses, map[string]bool{"point": true, "two": true, "fist": true})
			now := time.Unix(100, 0)
			var states []gesture.HandState
			for n := 0; n < 25; n++ {
				hands, _, err := tracker.Process(frame, now)
				if err != nil {
					t.Fatal(err)
				}
				states = r.Update(hands, now)
				now = now.Add(33 * time.Millisecond)
			}
			if len(states) != 1 || !states[0].Present || states[0].Pose != tc.pose || states[0].RawPose != tc.pose {
				t.Fatalf("static %s drifted after tracking: %+v", tc.pose, states)
			}
		})
	}
}
