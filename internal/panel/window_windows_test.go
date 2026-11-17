//go:build windows && amd64

package panel

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
	"unsafe"

	"gesturectl/internal/control"
)

func nativePanel(t *testing.T) (*Window, func() control.Snapshot, string) {
	t.Helper()
	if os.Getenv("MOUSE_PANEL_TEST") != "1" {
		t.Skip("set MOUSE_PANEL_TEST=1 for native controls; no global input is sent")
	}
	var mu sync.Mutex
	snapshot := control.Snapshot{Config: control.DefaultConfiguration(control.DefaultPreferences()), Revision: 1}
	path := filepath.Join(t.TempDir(), "settings.json")
	get := func() control.Snapshot {
		mu.Lock()
		defer mu.Unlock()
		return control.Snapshot{Config: snapshot.Config.Clone(), Revision: snapshot.Revision}
	}
	w, err := New(Options{Snapshot: get, Apply: func(rev uint64, cfg control.Configuration) (control.Snapshot, error) {
		if err := control.SaveConfiguration(path, cfg); err != nil {
			return control.Snapshot{}, err
		}
		mu.Lock()
		defer mu.Unlock()
		snapshot = control.Snapshot{Config: cfg.Clone(), Revision: rev + 1}
		return snapshot, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w, get, path
}

func TestNativeEditKeyDisableAndSave(t *testing.T) {
	w, get, path := nativePanel(t)
	var index int = -1
	for i, b := range get().Config.Bindings {
		if b.Action.Type == "key_combo" && b.Action.Params["keys"] == "tab" {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatal("default Tab binding missing")
	}
	w.invoke(func() {
		w.selectBinding(index)
		setText(w.params[0].handle, "enter")
		send(w.hwnd, wmCommand, uintptr(idSave), 0)
	})
	if get().Config.Bindings[index].Action.Params["keys"] != "enter" {
		t.Fatal("native form did not apply Enter")
	}
	stored, err := control.LoadConfiguration(path)
	if err != nil || stored.Bindings[index].Action.Params["keys"] != "enter" {
		t.Fatalf("reload: %v", err)
	}
	w.invoke(func() {
		send(w.controls[idEnabled], bmSetCheck, 0, 0)
		send(w.hwnd, wmCommand, uintptr(idEnabled), w.controls[idEnabled])
		send(w.hwnd, wmCommand, uintptr(idSave), 0)
	})
	if get().Config.Bindings[index].Enabled {
		t.Fatal("disable was not applied")
	}
}

func TestNativeDraftNewDeleteResetAndFileRoundTrip(t *testing.T) {
	w, get, _ := nativePanel(t)
	initial := len(get().Config.Bindings)
	var failure error
	path := filepath.Join(t.TempDir(), "export.json")
	w.invoke(func() {
		send(w.hwnd, wmCommand, uintptr(idNew), 0)
		if len(w.session.config.Bindings) != initial+1 {
			t.Error("new binding missing")
		}
		send(w.hwnd, wmCommand, uintptr(idDelete), 0)
		if len(w.session.config.Bindings) != initial {
			t.Error("delete binding missing")
		}
		w.selectBinding(0)
		setText(w.controls[idName], "草稿名称")
		if failure = w.commitAll(); failure != nil {
			return
		}
		if failure = w.session.export(path); failure != nil {
			return
		}
		w.session.resetBindings()
		w.renderAll()
		if failure = w.session.importFile(path); failure != nil {
			return
		}
		w.renderAll()
		if w.session.config.Bindings[0].Name != "草稿名称" {
			t.Error("import draft lost edits")
		}
		if get().Config.Bindings[0].Name == "草稿名称" {
			t.Error("import applied runtime config")
		}
		send(w.hwnd, wmCommand, uintptr(idResetBindings), 0)
		if w.session.config.Bindings[0].Name == "草稿名称" {
			t.Error("mapping reset missing")
		}
	})
	if failure != nil {
		t.Fatal(failure)
	}
}

func TestNativeWindowHideShowAndForcedClose(t *testing.T) {
	w, _, _ := nativePanel(t)
	w.invoke(func() { setText(w.controls[idName], "关闭保留草稿"); send(w.hwnd, wmClose, 0, 0) })
	w.Show()
	w.invoke(func() {
		if textOf(w.controls[idName]) != "关闭保留草稿" {
			t.Error("hidden window lost form")
		}
	})
	done := make(chan struct{})
	go func() { w.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close hung")
	}
	w.Close()
	w.Show()
	w.PublishStatus("closed")
}

func TestNativeFileDialogsCancelKeepsDraft(t *testing.T) {
	w, get, _ := nativePanel(t)
	w.invoke(func() { setText(w.controls[idName], "保留未保存草稿") })
	for _, item := range []struct {
		id    int
		title string
	}{{idImport, "导入到配置草稿"}, {idExport, "导出当前配置草稿"}} {
		finished := make(chan struct{})
		go func(id int) { w.invoke(func() { send(w.hwnd, wmCommand, uintptr(id), 0) }); close(finished) }(item.id)
		var dialog uintptr
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			h, _, _ := user.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(utf16("#32770"))), uintptr(unsafe.Pointer(utf16(item.title))))
			if h != 0 {
				owner, _, _ := user.NewProc("GetWindow").Call(h, 4)
				if owner == w.hwnd {
					dialog = h
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		if dialog == 0 {
			t.Fatal("own native file dialog did not open")
		}
		post.Call(dialog, wmCommand, 2, 0)
		select {
		case <-finished:
		case <-time.After(4 * time.Second):
			t.Fatal("canceling native file dialog hung")
		}
	}
	w.invoke(func() {
		if textOf(w.controls[idName]) != "保留未保存草稿" {
			t.Error("canceling dialog lost form draft")
		}
	})
	if get().Config.Bindings[0].Name == "保留未保存草稿" {
		t.Fatal("file dialog applied runtime configuration")
	}
}

func TestNativeExportRejectsLiveConfigurationPath(t *testing.T) {
	w, _, path := nativePanel(t)
	w.invoke(func() {
		original := w.options.Snapshot
		w.options.Snapshot = func() control.Snapshot { s := original(); s.Path = path; return s }
		if err := w.exportDraft(filepath.Join(filepath.Dir(path), ".", filepath.Base(path))); err == nil {
			t.Error("export bypassed live revision protection")
		}
	})
}

func TestNativeChangingTriggerClearsUnusedTimingFields(t *testing.T) {
	w, get, _ := nativePanel(t)
	w.invoke(func() {
		for i, b := range w.session.config.Bindings {
			if b.ID == "tab" {
				w.selectBinding(i)
				break
			}
		}
		setText(w.controls[idInterval], "无效但已不使用")
		for i, id := range w.triggers {
			if id == "exit" {
				send(w.controls[idTrigger], 0x14e, uintptr(i), 0)
				send(w.hwnd, wmCommand, uintptr(idTrigger|(1<<16)), w.controls[idTrigger])
				break
			}
		}
		send(w.hwnd, wmCommand, uintptr(idSave), 0)
	})
	for _, b := range get().Config.Bindings {
		if b.ID == "tab" && b.Trigger != "exit" {
			t.Fatal("unused timing input blocked saving new trigger")
		}
	}
}

func TestNativeUnavailableDisabledActionSurvivesUnrelatedSave(t *testing.T) {
	w, get, _ := nativePanel(t)
	w.invoke(func() {
		w.session.config.Bindings[0].Enabled = false
		w.session.config.Bindings[0].Action.Type = "open"
		w.session.config.Bindings[0].Action.Params = map[string]any{"target": "example.txt"}
		w.session.config.Bindings[0].Trigger = "enter"
		w.renderAll()
		send(w.hwnd, wmCommand, uintptr(idSave), 0)
	})
	b := get().Config.Bindings[0]
	if b.Action.Type != "open" || b.Action.Params["target"] != "example.txt" {
		t.Fatalf("unavailable disabled action silently changed: %+v", b)
	}
}

func TestNativeChangingToBothHandsRemovesSingleHandReleaseProtection(t *testing.T) {
	w, get, _ := nativePanel(t)
	w.invoke(func() {
		for i, b := range w.session.config.Bindings {
			if b.ID == "tab" {
				w.selectBinding(i)
				break
			}
		}
		send(w.controls[idHand], 0x14e, 3, 0)
		send(w.hwnd, wmCommand, uintptr(idHand|(1<<16)), w.controls[idHand])
		send(w.hwnd, wmCommand, uintptr(idSave), 0)
	})
	for _, b := range get().Config.Bindings {
		if b.ID == "tab" && (b.Hand != "both" || b.Pose != "two_index" || len(b.ReleaseTo) != 0) {
			t.Fatalf("single-hand protection prevented valid composite binding: %+v", b)
		}
	}
}
