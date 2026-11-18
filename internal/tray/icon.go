package tray

import "gesturectl/internal/winui"

// Menu commands.
const (
	itemOpen = 1 + iota
	itemPause
	itemStartup
	itemExit
)

// menuItem is a line of the icon's menu; the zero item is a separator.
type menuItem struct {
	id      int
	text    string
	checked bool
}

// menuItems lists the menu for the program's state: whether it has settings to
// open, whether gestures are paused, whether it can start with Windows and
// whether it does.
func menuItems(canOpen, paused, canStart, starts bool) []menuItem {
	var items []menuItem
	if canOpen {
		items = append(items, menuItem{id: itemOpen, text: "打开设置"})
	}
	pause := menuItem{id: itemPause, text: "暂停手势"}
	if paused {
		pause.text = "恢复手势"
	}
	items = append(items, pause)
	if canStart {
		items = append(items, menuItem{id: itemStartup, text: "开机时自动启动", checked: starts})
	}
	return append(items, menuItem{}, menuItem{id: itemExit, text: "退出手势鼠标"})
}

// tip is the line shown while the pointer rests on the icon.
func tip(paused, canOpen bool) string {
	switch {
	case paused:
		return "手势鼠标：已暂停，右键菜单可恢复"
	case canOpen:
		return "手势鼠标：手势控制中，双击打开设置"
	}
	return "手势鼠标：手势控制中"
}

// drawIcon draws the icon at size pixels square: a white open hand on a blue
// tile, or on a grey one while gestures are paused.
func drawIcon(size int, paused bool) *winui.Canvas {
	c := winui.NewCanvas(size, size)
	s := float64(size)
	tile := winui.RGB{R: 0x25, G: 0x63, B: 0xeb}
	if paused {
		tile = winui.RGB{R: 0x8a, G: 0x8f, B: 0x98}
	}
	c.RoundRect(0, 0, s, s, s*.22, tile, 1)
	white := winui.RGB{R: 0xff, G: 0xff, B: 0xff}
	open := winui.Hand{Fingers: [5]int{winui.Raised, winui.Raised, winui.Raised, winui.Raised, winui.Raised}}
	// The glyph fills about .76 by .87 of its square, off centre; this centres
	// it on the tile.
	c.Hand(s*.1, s*.06, s*.9, open, white, white)
	return c
}
