//go:build windows && amd64

package panel

import (
	"syscall"
	"unsafe"

	"gesturectl/internal/control"
	"gesturectl/internal/gesture"
	"gesturectl/internal/winui"
)

// The home page lists one gesture a row: a check box that turns it on and
// off, a picture of the hand, and plain words for how it is made and what it
// does. Everything else about a binding is on the advanced pages.

var comctl = syscall.NewLazyDLL("comctl32.dll")

// Notification codes are negative numbers carried as unsigned ones.
const (
	nmDoubleClick  = ^uint32(2)
	lvnItemChanged = ^uint32(100)
	tcnSelChange   = ^uint32(550)
	tcnSelChanging = ^uint32(551)
)

type listColumn struct {
	Mask                                                               uint32
	Format, Width                                                      int32
	Text                                                               *uint16
	TextMax, SubItem, Image, Order, MinWidth, DefaultWidth, IdealWidth int32
}
type listItem struct {
	Mask             uint32
	Item, SubItem    int32
	State, StateMask uint32
	Text             *uint16
	TextMax, Image   int32
	Param            uintptr
	Indent, Group    int32
	Columns          uint32
	ColumnList       *uint32
	ColumnFormats    *int32
	GroupIndex       int32
}
type notifyHeader struct {
	From, ID uintptr
	Code     uint32
}

// listNotify is NMLISTVIEW; NMITEMACTIVATE begins the same way.
type listNotify struct {
	Header                      notifyHeader
	Item, SubItem               int32
	NewState, OldState, Changed uint32
	Point                       point
	Param                       uintptr
}

var homeColumns = [3]int{240, 330, 400}

var glyphOn, glyphOff = winui.RGB{R: 37, G: 99, B: 235}, winui.RGB{R: 172, G: 180, B: 193}

func (w *Window) createHome() {
	w.label("打勾的手势正在使用。点方框就启用或停用，马上生效；双击一行可以改它的细节。", pageHome, 24, 0, 992, 20)
	w.pin(growWidth)
	list := w.add("SysListView32", "", idHome, pageHome, 24, 26, 992, 384, 0x1800d)
	w.pin(growWidth | growHeight)
	w.useFont(fontList)
	// Check boxes, whole-row selection and flicker-free drawing.
	send(list, 0x1036, 0, 0x10024)
	syscall.NewLazyDLL("uxtheme.dll").NewProc("SetWindowTheme").Call(list, uintptr(unsafe.Pointer(utf16("Explorer"))), 0)
	for i, title := range []string{"手势", "怎么比", "做什么"} {
		column := listColumn{Mask: 0xf, Width: int32(w.scale(homeColumns[i])), Text: utf16(title), SubItem: int32(i)}
		send(list, 0x1061, uintptr(i), uintptr(unsafe.Pointer(&column)))
	}
	w.add("BUTTON", "在光标旁显示提示：认到的手势、确认进度，触发时闪一下", idHints, pageHome, 24, 420, 620, 24, 0x10003)
	w.pin(moveDown)
	w.button("修改选中的手势…", idDetails, pageHome, 846, 418, 170)
	w.pin(moveRight | moveDown)
}

// sizeColumns scales the first columns and gives the last one the rest.
func (w *Window) sizeColumns() {
	list := w.controls[idHome]
	for i, width := range homeColumns[:2] {
		send(list, 0x101e, uintptr(i), uintptr(w.scale(width)))
	}
	send(list, 0x101e, 2, ^uintptr(1))
}

// glyph is the picture of a pose: fingers the pose needs open are raised,
// those it needs closed are curled, and the others rest in between.
func glyph(pose gesture.PoseDef, left bool) winui.Hand {
	hand := winui.Hand{Pinch: pose.Touch == "index", Left: left}
	for i, state := range pose.Fingers {
		switch state {
		case gesture.Open:
			hand.Fingers[i] = winui.Raised
		case gesture.Any:
			hand.Fingers[i] = winui.Loose
		}
	}
	return hand
}

func (w *Window) renderHome() {
	loading := w.loading
	w.loading = true
	defer func() { w.loading = loading }()
	list := w.controls[idHome]
	bindings := w.session.config.Bindings
	send(list, 0x1009, 0, 0)
	poses := map[string]gesture.PoseDef{}
	for _, pose := range control.GestureCatalog() {
		poses[pose.ID] = pose
	}
	size := w.scale(24)
	images, _, _ := comctl.NewProc("ImageList_Create").Call(arg(size), arg(size), 0x20, arg(len(bindings)), 1)
	for _, b := range bindings {
		canvas := winui.NewCanvas(size, size)
		canvas.Hand(0, 0, float64(size), glyph(poses[b.Pose], b.Hand == "left"), glyphOn, glyphOff)
		bitmap := winui.Bitmap(canvas)
		comctl.NewProc("ImageList_Add").Call(images, bitmap, 0)
		gdi.NewProc("DeleteObject").Call(bitmap)
	}
	if old := send(list, 0x1003, 1, images); old != 0 {
		comctl.NewProc("ImageList_Destroy").Call(old)
	}
	for i, b := range bindings {
		how, what := control.Describe(b)
		item := listItem{Mask: 0x3, Item: int32(i), Text: utf16(b.Name), Image: int32(i)}
		send(list, 0x104d, 0, uintptr(unsafe.Pointer(&item)))
		for column, text := range []string{how, what} {
			cell := listItem{SubItem: int32(column + 1), Text: utf16(text)}
			send(list, 0x1074, uintptr(i), uintptr(unsafe.Pointer(&cell)))
		}
		w.checkHome(i, b.Enabled)
	}
	w.selectHome(w.selected)
	shown := uintptr(1)
	if w.session.config.Preferences.HideHints {
		shown = 0
	}
	send(w.controls[idHints], bmSetCheck, shown, 0)
}

// checkHome sets the check box of a row, as a click on it does.
func (w *Window) checkHome(index int, on bool) {
	item := listItem{StateMask: 0xf000, State: 0x1000}
	if on {
		item.State = 0x2000
	}
	send(w.controls[idHome], 0x102b, uintptr(index), uintptr(unsafe.Pointer(&item)))
}

// selectHome moves the selection of the home list without the notification
// being taken for the user's.
func (w *Window) selectHome(index int) {
	list := w.controls[idHome]
	if list == 0 {
		return
	}
	loading := w.loading
	w.loading = true
	defer func() { w.loading = loading }()
	none := listItem{StateMask: 0x3}
	send(list, 0x102b, ^uintptr(0), uintptr(unsafe.Pointer(&none)))
	if index >= 0 {
		item := listItem{StateMask: 0x3, State: 0x3}
		send(list, 0x102b, uintptr(index), uintptr(unsafe.Pointer(&item)))
		send(list, 0x1013, uintptr(index), 0)
	}
}

func (w *Window) homeNotify(n *listNotify) {
	i := int(n.Item)
	if i < 0 || i >= len(w.session.config.Bindings) {
		return
	}
	switch n.Header.Code {
	case nmDoubleClick:
		// Two clicks on the check box are two toggles, not a request for the
		// details.
		hit := struct {
			Point                point
			Flags                uint32
			Item, SubItem, Group int32
		}{Point: n.Point}
		send(w.controls[idHome], 0x1012, 0, uintptr(unsafe.Pointer(&hit)))
		if hit.Flags&0x8 == 0 {
			w.later(func() { w.openEditor(i) })
		}
	case lvnItemChanged:
		switch {
		case n.Changed&0x8 == 0:
		case (n.NewState^n.OldState)&0xf000 != 0:
			if enabled := n.NewState&0xf000 == 0x2000; n.OldState&0xf000 != 0 && enabled != w.session.config.Bindings[i].Enabled {
				w.later(func() { w.toggle(i, enabled) })
			}
		case n.NewState&0x2 != 0 && n.OldState&0x2 == 0 && i != w.selected:
			w.later(func() {
				if i < len(w.session.config.Bindings) && w.commitBinding() == nil {
					w.selectBinding(i)
				}
			})
		}
	}
}

func (w *Window) toggle(index int, enabled bool) {
	if index >= len(w.session.config.Bindings) {
		return
	}
	done := "已停用“" + w.session.config.Bindings[index].Name + "”，马上生效。"
	if enabled {
		done = "已启用“" + w.session.config.Bindings[index].Name + "”，马上生效。"
	}
	w.quick(func(c *control.Configuration) { c.Bindings[index].Enabled = enabled }, done)
}

// quick carries a one-click change of the home page to the running
// configuration at once. When the draft already holds other changes, those are
// not applied behind the user's back: the change joins the draft instead.
func (w *Window) quick(change func(*control.Configuration), done string) {
	clean := !w.formDirty && !w.session.dirty()
	if !clean {
		if err := w.commitAll(); err != nil {
			w.renderHome()
			w.notice("请先修正“高级”页里的表单：" + err.Error())
			return
		}
	}
	previous := w.session.config.Clone()
	change(&w.session.config)
	if !clean {
		w.renderAll()
		w.notice("已记入草稿。草稿里还有其他修改，点“保存并应用”后一起生效。")
		return
	}
	if err := w.session.apply(w.options.Apply); err != nil {
		w.session.config = previous
		w.renderAll()
		w.notice("没有生效：" + err.Error())
		return
	}
	w.renderAll()
	w.notice(done)
}

// openEditor shows a binding on the advanced page that edits all of it.
func (w *Window) openEditor(index int) {
	if index >= 0 && index < len(w.session.config.Bindings) && index != w.selected {
		if err := w.commitBinding(); err != nil {
			w.notice("切换前请修正：" + err.Error())
			return
		}
		w.selectBinding(index)
	}
	w.showPage(pageEditor)
	user.NewProc("SetFocus").Call(w.controls[idName])
}
