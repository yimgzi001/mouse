package gesture

import (
	"fmt"
	"regexp"
	"sort"
)

// Finger states used in pose definitions.
const (
	Any    = "any"
	Open   = "open"
	Closed = "closed"
)

// PoseDef describes a static hand shape. Poses are data: the built-in ones are
// listed in BuiltinPoses and users can add their own in the configuration.
type PoseDef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Desc string `json:"desc,omitempty"`
	// Fingers is the required state of the thumb, index, middle, ring and little
	// finger: "open", "closed" or "any".
	Fingers [NumFingers]string `json:"fingers"`
	// Touch names a finger ("index", "middle", "ring", "pinky") whose tip must
	// touch the tip of the thumb. The states of the thumb and of that finger are
	// then ignored.
	Touch string `json:"touch,omitempty"`
	// ThumbDir and IndexDir require the thumb or the index finger to point "up",
	// "down", "left" or "right" as the user sees it.
	ThumbDir string `json:"thumbDir,omitempty"`
	IndexDir string `json:"indexDir,omitempty"`
	// Palm requires the palm ("front") or the back of the hand ("back") to face
	// the camera.
	Palm string `json:"palm,omitempty"`
	// Builtin marks poses that ship with the program.
	Builtin bool `json:"builtin,omitempty"`
	// Experimental marks poses that are harder to recognise reliably.
	Experimental bool `json:"experimental,omitempty"`
}

var poseIDPattern = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

var directionAngles = map[string]float32{"up": 0, "right": 90, "down": 180, "left": -90}

// Normalize fills in defaults and reports what is wrong with the definition.
func (p *PoseDef) Normalize() error {
	if !poseIDPattern.MatchString(p.ID) {
		return fmt.Errorf("pose id %q: use 1 to 32 lower-case letters, digits or underscores", p.ID)
	}
	if p.Name == "" {
		p.Name = p.ID
	}
	if len([]rune(p.Name)) > 40 {
		return fmt.Errorf("pose %s: name is longer than 40 characters", p.ID)
	}
	constraints := 0
	for i, s := range p.Fingers {
		switch s {
		case "":
			p.Fingers[i] = Any
		case Any:
		case Open, Closed:
			constraints++
		default:
			return fmt.Errorf("pose %s: finger state %q is not open, closed or any", p.ID, s)
		}
	}
	if p.Touch != "" {
		if touchIndex(p.Touch) < 0 {
			return fmt.Errorf("pose %s: touch %q is not index, middle, ring or pinky", p.ID, p.Touch)
		}
		constraints++
	}
	for _, d := range []string{p.ThumbDir, p.IndexDir} {
		if d == "" {
			continue
		}
		if _, ok := directionAngles[d]; !ok {
			return fmt.Errorf("pose %s: direction %q is not up, down, left or right", p.ID, d)
		}
		constraints++
	}
	switch p.Palm {
	case "":
	case "front", "back":
		constraints++
	default:
		return fmt.Errorf("pose %s: palm %q is not front or back", p.ID, p.Palm)
	}
	if constraints == 0 {
		return fmt.Errorf("pose %s: no condition set, it would match every hand", p.ID)
	}
	return nil
}

func touchIndex(name string) int {
	for i := Index; i < NumFingers; i++ {
		if FingerNames[i] == name {
			return i - Index
		}
	}
	return -1
}

// BuiltinPoses returns the poses that ship with the program.
func BuiltinPoses() []PoseDef {
	const a, o, c = Any, Open, Closed
	poses := []PoseDef{
		{ID: "fist", Name: "握拳", Desc: "四指握起，拇指随意", Fingers: [5]string{a, c, c, c, c}},
		{ID: "point", Name: "一指（食指）", Desc: "只伸食指，拇指随意", Fingers: [5]string{a, o, c, c, c}},
		{ID: "two", Name: "二指（食指+中指）", Desc: "伸食指和中指，拇指随意", Fingers: [5]string{a, o, o, c, c}},
		{ID: "middle", Name: "只伸中指", Fingers: [5]string{a, c, o, c, c}},
		{ID: "click_pinch", Name: "拇食指捏合", Desc: "拇指和食指相碰，另三指收起", Fingers: [5]string{a, a, c, c, c}, Touch: "index"},
		{ID: "three", Name: "三指（食指+中指+无名指）", Desc: "伸食指、中指、无名指，拇指随意", Fingers: [5]string{a, o, o, o, c}},
		{ID: "four", Name: "四指（收拇指）", Desc: "四指伸直，拇指收起", Fingers: [5]string{c, o, o, o, o}},
		{ID: "five", Name: "五指张开", Desc: "五指全部伸直", Fingers: [5]string{o, o, o, o, o}},
		{ID: "thumb_up", Name: "点赞（拇指向上）", Fingers: [5]string{o, c, c, c, c}, ThumbDir: "up"},
		{ID: "thumb_down", Name: "拇指向下", Fingers: [5]string{o, c, c, c, c}, ThumbDir: "down"},
		{ID: "thumb_left", Name: "拇指向左", Fingers: [5]string{o, c, c, c, c}, ThumbDir: "left"},
		{ID: "thumb_right", Name: "拇指向右", Fingers: [5]string{o, c, c, c, c}, ThumbDir: "right"},
		{ID: "thumb_out", Name: "只伸拇指（不限方向）", Fingers: [5]string{o, c, c, c, c}},
		{ID: "gun", Name: "手枪（拇指+食指）", Desc: "拇指和食指伸直成 L 形", Fingers: [5]string{o, o, c, c, c}},
		{ID: "eight_back", Name: "比八（手背朝摄像头）", Desc: "拇指和食指伸直成直角，另三指收起，手背朝摄像头", Fingers: [5]string{o, o, c, c, c}, Palm: "back"},
		{ID: "three_thumb", Name: "拇指+食指+中指", Fingers: [5]string{o, o, o, c, c}},
		{ID: "rock", Name: "摇滚（食指+小指）", Desc: "伸食指和小指，拇指随意", Fingers: [5]string{a, o, c, c, o}},
		{ID: "love", Name: "我爱你（拇指+食指+小指）", Fingers: [5]string{o, o, c, c, o}},
		{ID: "six", Name: "六（拇指+小指）", Fingers: [5]string{o, c, c, c, o}},
		{ID: "pinky", Name: "只伸小指", Desc: "只伸小指，拇指随意", Fingers: [5]string{a, c, c, c, o}},
		{ID: "ok", Name: "OK", Desc: "拇指尖与食指尖相碰，其余三指伸直", Fingers: [5]string{a, a, o, o, o}, Touch: "index"},
		{ID: "middle_ring_pinky", Name: "后三指（中指+无名指+小指）", Desc: "中指、无名指、小指伸直，食指收起，拇指随意", Fingers: [5]string{a, c, o, o, o}},
		{ID: "pinch", Name: "捏合（拇指+食指）", Desc: "拇指尖与食指尖相碰，其余手指随意", Fingers: [5]string{a, a, a, a, a}, Touch: "index", Experimental: true},
		{ID: "pinch_middle", Name: "捏合（拇指+中指）", Desc: "拇指尖与中指尖相碰，食指伸直", Fingers: [5]string{a, o, a, a, a}, Touch: "middle", Experimental: true},
		{ID: "point_up", Name: "食指指向上", Fingers: [5]string{a, o, c, c, c}, IndexDir: "up"},
		{ID: "point_down", Name: "食指指向下", Fingers: [5]string{a, o, c, c, c}, IndexDir: "down"},
		{ID: "point_left", Name: "食指指向左", Fingers: [5]string{a, o, c, c, c}, IndexDir: "left"},
		{ID: "point_right", Name: "食指指向右", Fingers: [5]string{a, o, c, c, c}, IndexDir: "right"},
		{ID: "five_back", Name: "五指张开（手背朝摄像头）", Fingers: [5]string{o, o, o, o, o}, Palm: "back", Experimental: true},
	}
	for i := range poses {
		poses[i].Builtin = true
	}
	return poses
}

// observation is what one frame says about a hand, after hysteresis.
type observation struct {
	open       [NumFingers]bool
	touch      [NumFingers - 1]bool
	thumbAngle float32
	indexAngle float32
	palm       int8 // 1 palm faces the camera, -1 back of the hand, 0 unclear
}

type compiledPose struct {
	id          string
	need        [NumFingers]int8 // -1 any, 0 closed, 1 open
	touch       int              // index into observation.touch, -1 for none
	hasThumbDir bool
	hasIndexDir bool
	thumbDir    float32
	indexDir    float32
	palm        int8
	weight      int
}

func angleDiff(a, b float32) float32 {
	d := a - b
	for d > 180 {
		d -= 360
	}
	for d < -180 {
		d += 360
	}
	if d < 0 {
		d = -d
	}
	return d
}

func (c *compiledPose) matches(ob *observation, tolerance float32) bool {
	for i, need := range c.need {
		if need < 0 {
			continue
		}
		if (need == 1) != ob.open[i] {
			return false
		}
	}
	if c.touch >= 0 && !ob.touch[c.touch] {
		return false
	}
	if c.hasThumbDir && angleDiff(ob.thumbAngle, c.thumbDir) > tolerance {
		return false
	}
	if c.hasIndexDir && angleDiff(ob.indexAngle, c.indexDir) > tolerance {
		return false
	}
	if c.palm != 0 && ob.palm != c.palm {
		return false
	}
	return true
}

// PoseSet is a list of poses prepared for matching. When several poses fit a
// hand, the one with the most conditions wins.
type PoseSet struct {
	defs  []PoseDef
	poses []compiledPose
}

// NewPoseSet validates the definitions and prepares them for matching.
func NewPoseSet(defs []PoseDef) (*PoseSet, error) {
	s := &PoseSet{}
	seen := map[string]bool{}
	for _, d := range defs {
		if err := d.Normalize(); err != nil {
			return nil, err
		}
		if seen[d.ID] {
			return nil, fmt.Errorf("pose id %q is used twice", d.ID)
		}
		seen[d.ID] = true
		c := compiledPose{id: d.ID, touch: -1}
		for i, st := range d.Fingers {
			switch st {
			case Open:
				c.need[i] = 1
			case Closed:
				c.need[i] = 0
			default:
				c.need[i] = -1
			}
		}
		if d.Touch != "" {
			c.touch = touchIndex(d.Touch)
			c.need[Thumb] = -1
			c.need[Index+c.touch] = -1
			// A touch outweighs any set of plain finger states, so that
			// a pinch is not mistaken for the fist or pointing pose that
			// has the same fingers curled.
			c.weight += NumFingers + 1
		}
		for _, need := range c.need {
			if need >= 0 {
				c.weight++
			}
		}
		if d.ThumbDir != "" {
			c.hasThumbDir, c.thumbDir = true, directionAngles[d.ThumbDir]
			c.weight++
		}
		if d.IndexDir != "" {
			c.hasIndexDir, c.indexDir = true, directionAngles[d.IndexDir]
			c.weight++
		}
		switch d.Palm {
		case "front":
			c.palm = 1
			c.weight++
		case "back":
			c.palm = -1
			c.weight++
		}
		s.defs = append(s.defs, d)
		s.poses = append(s.poses, c)
	}
	sort.SliceStable(s.poses, func(i, j int) bool { return s.poses[i].weight > s.poses[j].weight })
	return s, nil
}

// Defs returns the pose definitions in the order they were given.
func (s *PoseSet) Defs() []PoseDef { return s.defs }

// Has reports whether a pose with this id exists.
func (s *PoseSet) Has(id string) bool {
	for i := range s.defs {
		if s.defs[i].ID == id {
			return true
		}
	}
	return false
}

// match returns the id of the best fitting pose, or "" when none fits. With a
// non-nil active set only those poses are considered.
func (s *PoseSet) match(ob *observation, active map[string]bool, tolerance float32) string {
	for i := range s.poses {
		p := &s.poses[i]
		if active != nil && !active[p.id] {
			continue
		}
		if p.matches(ob, tolerance) {
			return p.id
		}
	}
	return ""
}
