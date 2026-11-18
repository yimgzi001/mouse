//go:build windows && amd64

package panel

import (
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"gesturectl/internal/engine"
)

// shownChoice returns the text a drop-down list displays.
func shownChoice(h uintptr) string {
	buffer := make([]uint16, 256)
	send(h, 0x148, send(h, 0x147, 0, 0), uintptr(unsafe.Pointer(&buffer[0])))
	return syscall.UTF16ToString(buffer)
}

func TestNativeEditorListsShowTheStoredChoices(t *testing.T) {
	w, get, _ := nativePanel(t)
	hands := map[string]string{"left": "左手", "right": "右手", "any": "任意单手", "both": "双手"}
	triggers := map[string]string{}
	for _, trigger := range engine.Triggers() {
		triggers[trigger.ID] = trigger.Name
	}
	for i, b := range get().Config.Bindings {
		var hand, pose, action, trigger string
		w.invoke(func() {
			w.selectBinding(i)
			hand, pose = shownChoice(w.controls[idHand]), shownChoice(w.controls[idPose])
			action, trigger = shownChoice(w.controls[idAction]), shownChoice(w.controls[idTrigger])
		})
		if hand != hands[b.Hand] || pose != poseLabel(b.Pose) || !strings.HasSuffix(action, " · "+actionLabel(b.Action.Type)) || trigger != triggers[b.Trigger] {
			t.Errorf("%s shows %q, %q, %q and %q", b.ID, hand, pose, action, trigger)
		}
	}
}
