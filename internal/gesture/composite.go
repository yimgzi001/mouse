package gesture

import (
	"math"

	"gesturectl/internal/vision"
)

// Composite is recognition data only. The binding engine decides whether it
// owns either hand and what action to perform.
type Composite struct {
	Pose            string
	LeftID, RightID int
	Stable          bool
	Distance        float64
}

// TwoIndex detects the raw two-index candidate and its stable/distance data.
// Unknown hands cannot supply a composite identity.
func TwoIndex(states []HandState) (Composite, bool) {
	var left, right *HandState
	for i := range states {
		st := &states[i]
		if !st.Present || st.Lost || st.RawPose != "point" {
			continue
		}
		switch st.Side {
		case SideLeft:
			left = st
		case SideRight:
			right = st
		}
	}
	if left == nil || right == nil || left.ID == right.ID {
		return Composite{}, false
	}
	a, b := left.Landmarks[vision.IndexTip], right.Landmarks[vision.IndexTip]
	return Composite{Pose: "two_index", LeftID: left.ID, RightID: right.ID,
		Stable:   left.Pose == "point" && right.Pose == "point",
		Distance: math.Hypot(float64(a.X-b.X), float64(a.Y-b.Y))}, true
}
