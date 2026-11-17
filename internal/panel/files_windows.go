//go:build windows && amd64

package panel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"gesturectl/internal/control"
)

type openFileName struct {
	Size                         uint32
	Owner, Instance              uintptr
	Filter, CustomFilter         *uint16
	MaxCustomFilter, FilterIndex uint32
	File                         *uint16
	MaxFile                      uint32
	FileTitle                    *uint16
	MaxFileTitle                 uint32
	InitialDir, Title            *uint16
	Flags                        uint32
	FileOffset, FileExtension    uint16
	DefaultExtension             *uint16
	CustomData, Hook             uintptr
	TemplateName                 *uint16
	Reserved                     uintptr
	Reserved2, FlagsEx           uint32
}

func (w *Window) createFiles() {
	w.label("配置文件", 2, 20, 4, 900, 30)
	w.label("导入与恢复只修改草稿；点击“保存并应用”后才写入运行配置。导出前会校验当前草稿。", 2, 20, 46, 1052, 52)
	w.button("导入 JSON…", idImport, 2, 20, 118, 220)
	w.button("导出草稿 JSON…", idExport, 2, 258, 118, 240)
	w.label("导入上限 1 MiB。支持当前版本配置及旧版七项偏好设置。导出请选择独立文件，以保留运行配置的版本保护。", 2, 20, 166, 1052, 52)
	w.label("分别恢复默认", 2, 20, 252, 900, 30)
	w.button("恢复默认手势映射", idResetBindings, 2, 20, 302, 270)
	w.label("仅替换映射；保留六项基础倍率、识别参数与左右手校正。媒体动作不会重新添加到默认映射。", 2, 310, 306, 762, 52)
	w.button("恢复默认灵敏度", idResetPreferences, 2, 20, 382, 270)
	w.label("仅恢复六项基础倍率；保留映射、识别参数与左右手校正。", 2, 310, 386, 762, 48)
	w.button("恢复默认识别设置", idResetRecognition, 2, 20, 462, 270)
	w.label("仅恢复“识别与防误触”里的七项设置。", 2, 310, 466, 762, 48)
}
func (w *Window) fileCommand(id int) {
	switch id {
	case idImport:
		path, err := w.chooseFile(false)
		if err != nil {
			w.notice(err.Error())
			return
		}
		if path == "" {
			return
		}
		if err = w.session.importFile(path); err != nil {
			w.notice("导入失败，当前草稿保留：" + err.Error())
			return
		}
		w.selected = 0
		w.renderAll()
		w.notice("配置已导入草稿；尚未应用，点击保存后生效。")
	case idExport:
		if err := w.commitAll(); err != nil {
			w.notice("无法导出：" + err.Error())
			return
		}
		path, err := w.chooseFile(true)
		if err != nil {
			w.notice(err.Error())
			return
		}
		if path == "" {
			return
		}
		if err = w.exportDraft(path); err != nil {
			w.notice("导出失败：" + err.Error())
			return
		}
		w.notice("已导出当前草稿；运行配置保持原版本。")
	case idResetBindings:
		if err := w.commitSettings(); err != nil {
			w.notice(err.Error())
			return
		}
		w.session.resetBindings()
		w.selected = 0
		w.renderAll()
		w.notice("默认映射已载入草稿，灵敏度和左右手校正已保留。")
	case idResetPreferences:
		if err := w.commitBinding(); err != nil {
			w.notice(err.Error())
			return
		}
		w.session.config.Preferences.SwapHands = send(w.controls[idSwapHands], bmGetCheck, 0, 0) != 0
		w.session.resetPreferences()
		w.renderAll()
		w.notice("默认基础灵敏度已载入草稿，映射和左右手校正已保留。")
	case idResetRecognition:
		if err := w.commitBinding(); err != nil {
			w.notice(err.Error())
			return
		}
		// Capture valid sensitivity edits without requiring the invalid recognition
		// fields being reset to pass validation first.
		original := w.session.config.Recognition
		w.session.config.Recognition = control.DefaultRecognition()
		for i, n := range recognitionValues(w.session.config.Recognition) {
			setText(w.settingEdits[6+i], fmt.Sprint(n))
		}
		if err := w.commitSettings(); err != nil {
			w.session.config.Recognition = original
			w.notice(err.Error())
			return
		}
		w.renderAll()
		w.notice("默认识别设置已载入草稿。")
	}
}
func (w *Window) exportDraft(path string) error {
	running := w.options.Snapshot().Path
	if running != "" {
		same, err := samePath(path, running)
		if err != nil {
			return err
		}
		if same {
			return fmt.Errorf("请选择其他文件；运行配置请使用“保存并应用”写入")
		}
	}
	return w.session.export(path)
}
func samePath(a, b string) (bool, error) {
	aa, err := filepath.Abs(a)
	if err != nil {
		return false, err
	}
	bb, err := filepath.Abs(b)
	if err != nil {
		return false, err
	}
	if v, e := filepath.EvalSymlinks(aa); e == nil {
		aa = v
	}
	if v, e := filepath.EvalSymlinks(bb); e == nil {
		bb = v
	}
	if strings.EqualFold(filepath.Clean(aa), filepath.Clean(bb)) {
		return true, nil
	}
	ai, ae := os.Stat(aa)
	bi, be := os.Stat(bb)
	return ae == nil && be == nil && os.SameFile(ai, bi), nil
}
func (w *Window) chooseFile(save bool) (string, error) {
	buffer := make([]uint16, 32768)
	filter := append(syscall.StringToUTF16("JSON 配置 (*.json)"), syscall.StringToUTF16("*.json")...)
	filter = append(filter, syscall.StringToUTF16("所有文件 (*.*)")...)
	filter = append(filter, syscall.StringToUTF16("*.*")...)
	filter = append(filter, 0)
	dialog := openFileName{Owner: w.hwnd, Filter: &filter[0], FilterIndex: 1, File: &buffer[0], MaxFile: uint32(len(buffer)), DefaultExtension: utf16("json"), Flags: 0x80000 | 0x800 | 0x8}
	dialog.Size = uint32(unsafe.Sizeof(dialog))
	procedure := "GetOpenFileNameW"
	dialog.Title = utf16("导入到配置草稿")
	if save {
		procedure = "GetSaveFileNameW"
		dialog.Flags |= 2
		dialog.Title = utf16("导出当前配置草稿")
		copy(buffer, syscall.StringToUTF16("mouse-draft.json"))
	} else {
		dialog.Flags |= 0x1000
	}
	dll := syscall.NewLazyDLL("comdlg32.dll")
	ok, _, _ := dll.NewProc(procedure).Call(uintptr(unsafe.Pointer(&dialog)))
	if ok == 0 {
		code, _, _ := dll.NewProc("CommDlgExtendedError").Call()
		if code != 0 {
			return "", fmt.Errorf("文件对话框失败（%d）", code)
		}
		return "", nil
	}
	return syscall.UTF16ToString(buffer), nil
}
