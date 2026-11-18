package tray

import (
	"slices"
	"testing"
)

func TestMenuFollowsTheProgramState(t *testing.T) {
	texts := func(items []menuItem) []string {
		var out []string
		for _, item := range items {
			text := item.text
			if item.id == 0 {
				text = "-"
			}
			if item.checked {
				text += " [x]"
			}
			out = append(out, text)
		}
		return out
	}
	for _, tc := range []struct {
		canOpen, paused, canStart, starts bool
		want                              []string
	}{
		{true, false, true, false, []string{"打开设置", "暂停手势", "开机时自动启动", "-", "退出手势鼠标"}},
		{true, true, true, true, []string{"打开设置", "恢复手势", "开机时自动启动 [x]", "-", "退出手势鼠标"}},
		{false, false, false, false, []string{"暂停手势", "-", "退出手势鼠标"}},
	} {
		if got := texts(menuItems(tc.canOpen, tc.paused, tc.canStart, tc.starts)); !slices.Equal(got, tc.want) {
			t.Errorf("menu for %+v is %q", tc, got)
		}
	}
}

func TestIconTurnsGreyWhilePaused(t *testing.T) {
	blue, grey := [3]byte{0xeb, 0x63, 0x25}, [3]byte{0x98, 0x8f, 0x8a}
	for _, size := range []int{16, 20, 24, 32} {
		for _, paused := range []bool{false, true} {
			c := drawIcon(size, paused)
			if c.Pix[3] == 255 {
				t.Errorf("%d px icon has a square corner", size)
			}
			count := map[[3]byte]int{}
			for i := 0; i < len(c.Pix); i += 4 {
				if c.Pix[i+3] == 255 {
					count[[3]byte(c.Pix[i:i+3])]++
				}
			}
			tile, other := blue, grey
			if paused {
				tile, other = grey, blue
			}
			if count[tile] < size*size/4 || count[other] != 0 || count[[3]byte{0xff, 0xff, 0xff}] < size*size/10 {
				t.Errorf("%d px icon paused=%t has %d tile, %d of the other tile and %d white pixels", size, paused, count[tile], count[other], count[[3]byte{0xff, 0xff, 0xff}])
			}
		}
	}
}
