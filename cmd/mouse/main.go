// Command mouse controls the Windows cursor with camera hand gestures.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"gesturectl/assets"
	"gesturectl/internal/camera"
	"gesturectl/internal/control"
	"gesturectl/internal/overlay"
	"gesturectl/internal/panel"
	"gesturectl/internal/preview"
	"gesturectl/internal/vision"
	"gesturectl/internal/wininput"
)

func main() {
	err := run(os.Args[1:], os.Stdout, os.Stderr)
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return
	}
	// Started from Explorer the program has no console, so printing fails.
	if _, printErr := fmt.Fprintln(os.Stderr, "mouse:", err); printErr != nil {
		showFailure(failureText(err))
	}
	os.Exit(1)
}

func run(args []string, out, errOut io.Writer) (result error) {
	flags := flag.NewFlagSet("mouse", flag.ContinueOnError)
	flags.SetOutput(errOut)
	cameraIndex := flags.Int("camera", 0, "摄像头编号（用 -list 查看）")
	list := flags.Bool("list", false, "仅列出摄像头")
	width := flags.Int("width", 640, "摄像头宽度")
	height := flags.Int("height", 480, "摄像头高度")
	fps := flags.Int("fps", 30, "请求摄像头帧率")
	threads := flags.Int("threads", 2, "模型推理线程数")
	sensitivity := flags.Float64("sensitivity", .5, "覆盖左右手移动倍率；其他独立倍率在预览窗口调整")
	configPath := flags.String("config", "", "设置 JSON 路径，默认 EXE 同目录 settings.json")
	dry := flags.Bool("dry-run", false, "输出动作日志，不移动鼠标或发送输入")
	paused := flags.Bool("paused", false, "以暂停状态启动，F8 恢复")
	seconds := flags.Float64("seconds", 0, "运行指定秒数后退出；0 表示持续运行")
	verbose := flags.Bool("verbose", false, "每秒输出识别状态")
	swapHands := flags.Bool("swap-hands", false, "校正摄像头的左右手识别反转")
	showPreview := flags.Bool("preview", false, "显示实时画面、关键点和识别结果")
	showPanel := flags.Bool("panel", true, "显示 Windows 原生配置面板；关闭后 F7 可重开")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("不支持的位置参数：%s", strings.Join(flags.Args(), " "))
	}
	if *cameraIndex < 0 || *width <= 0 || *height <= 0 || *fps <= 0 || *fps > 240 || *threads < 1 || *threads > 64 {
		return fmt.Errorf("摄像头编号须非负，分辨率须为正，fps 须为 1–240，threads 须为 1–64")
	}
	if *sensitivity < .1 || math.IsNaN(*sensitivity) || math.IsInf(*sensitivity, 0) || *sensitivity > 5 {
		return fmt.Errorf("sensitivity 须为 0.1～5")
	}
	if *seconds < 0 || math.IsNaN(*seconds) || math.IsInf(*seconds, 0) || *seconds > 86400 {
		return fmt.Errorf("seconds 须为 0–86400")
	}
	logger := log.New(out, "", log.Ltime)
	if *list {
		names, err := camera.List()
		if err != nil {
			return err
		}
		if len(names) == 0 {
			fmt.Fprintln(out, "未检测到摄像头")
		}
		for n, name := range names {
			fmt.Fprintf(out, "camera %d: %s\n", n, name)
		}
		return nil
	}
	if anotherInstanceRunning() {
		return withHint(errAlreadyRunning, runningHint)
	}
	if *configPath == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		*configPath = filepath.Join(filepath.Dir(exe), "settings.json")
	}
	configuration, err := control.LoadConfiguration(*configPath)
	if err != nil {
		return withHint(err, settingsHint(*configPath))
	}
	prefs := configuration.Preferences
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "sensitivity":
			prefs.MoveLeft, prefs.MoveRight = *sensitivity, *sensitivity
		case "swap-hands":
			prefs.SwapHands = *swapHands
		}
	})
	configuration.Preferences = prefs
	if err := configuration.Normalize(); err != nil {
		return withHint(err, settingsHint(*configPath))
	}
	if err := wininput.EnableDPI(); err != nil {
		return err
	}
	if err := wininput.RaiseProcessPriority(); err != nil {
		logger.Printf("提高进程优先级失败，继续以普通优先级运行：%v", err)
	}
	lib, err := vision.FindRuntimeLibrary()
	if err != nil {
		return withHint(err, runtimeHint)
	}
	models, err := vision.NewModels(vision.ModelOptions{LibraryPath: lib, PalmModel: assets.PalmModel, LandmarkModel: assets.LandmarkModel, Threads: *threads})
	if err != nil {
		return withHint(err, runtimeHint)
	}
	defer models.Close()
	cameraConfig := camera.Config{Index: *cameraIndex, Width: *width, Height: *height, FPS: *fps, Mirror: true}
	capture, err := camera.Open(cameraConfig)
	if err != nil {
		return withHint(err, cameraHint)
	}
	var cam control.Camera = capture
	defer func() {
		if cam == nil {
			return
		}
		closeCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		result = errors.Join(result, cam.CloseContext(closeCtx))
	}()
	inj := wininput.New()
	if *dry {
		inj = wininput.NewDryRun(out)
	}
	defer func() { result = errors.Join(result, inj.Close()) }()
	service, err := control.NewService(*configPath, configuration, inj)
	if err != nil {
		return err
	}
	ctl := service.Controller()
	defer ctl.Abort()
	ctl.SetPaused(*paused)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if *seconds > 0 {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(ctx, time.Duration(*seconds*float64(time.Second)))
		defer stop()
	}
	cursorHint, err := overlay.New()
	if err != nil {
		logger.Printf("光标旁提示无法创建，继续运行但不显示提示：%v", err)
	}
	defer cursorHint.Close()
	var hinter atomic.Pointer[control.Hinter]
	useConfiguration := func(cfg control.Configuration) {
		hinter.Store(control.NewHinter(cfg))
		cursorHint.SetEnabled(!cfg.Preferences.HideHints)
	}
	useConfiguration(service.Snapshot().Config)
	var view *preview.Window
	var configurationPanel *panel.Window
	exit := func() { ctl.SetPaused(true); cancel() }
	if *showPreview || *showPanel {
		options := preview.Options{Preferences: prefs, Visible: *showPreview, HideOnClose: *showPanel,
			OnPreferencePatch: func(base, next control.Preferences) (control.Preferences, error) {
				snapshot, err := service.PatchPreferences(base, next)
				if err == nil {
					useConfiguration(snapshot.Config)
					logger.Printf("灵敏度已保存，配置修订=%d", snapshot.Revision)
				}
				return snapshot.Config.Preferences, err
			},
		}
		if !*showPanel {
			options.OnClose = exit
		}
		view, err = preview.NewWithOptions(options)
		if err != nil {
			return err
		}
		defer view.Close()
	}
	toggleHands := func() error {
		snapshot, err := service.ToggleHands()
		if err == nil {
			useConfiguration(snapshot.Config)
			if view != nil {
				view.SetPreferences(snapshot.Config.Preferences)
			}
			logger.Printf("左右手校正已保存：%t", snapshot.Config.Preferences.SwapHands)
		}
		return err
	}
	if *showPanel {
		configurationPanel, err = panel.New(panel.Options{
			Snapshot: service.Snapshot,
			Apply: func(revision uint64, next control.Configuration) (control.Snapshot, error) {
				snapshot, err := service.Apply(revision, next)
				if err == nil {
					useConfiguration(snapshot.Config)
					if view != nil {
						view.SetPreferences(snapshot.Config.Preferences)
					}
					logger.Printf("绑定配置已应用，修订=%d，绑定数=%d", snapshot.Revision, len(snapshot.Config.Bindings))
				}
				return snapshot, err
			},
			Paused: ctl.Paused, SetPaused: ctl.SetPaused, Exit: exit,
			TogglePreview: func() {
				if view != nil {
					if view.Visible() {
						view.Hide()
					} else {
						view.Show()
					}
				}
			},
		})
		if err != nil {
			return err
		}
		defer configurationPanel.Close()
		configurationPanel.PublishStatus("摄像头已就绪，等待识别帧…")
	}
	hotkeyCtx, stopHotkeys := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchControls(hotkeyCtx, exit, ctl, logger, toggleHands, func() {
			if configurationPanel != nil {
				configurationPanel.Show()
			}
		}, cursorHint.Paused)
	}()
	defer func() { stopHotkeys(); <-done }()
	info := capture.Info()
	logger.Printf("摄像头 %d：%s，%dx%d，%.1f FPS，镜像开启", *cameraIndex, info.Name, info.Width, info.Height, info.FPS)
	for _, line := range control.HelpLines() {
		logger.Println(line)
	}
	logger.Println("F6 交换左右手标签（用实际左手核对画面标签后调整）")
	if *showPanel {
		logger.Println("F7 打开配置面板；面板和预览关闭后程序仍在后台运行")
	}
	logger.Printf("F8 暂停/恢复；F9 或 Ctrl+C 退出。暂停=%t；dry-run=%t；独立设置=%+v；文件=%s", *paused, *dry, prefs, *configPath)
	frames := 0
	var latency time.Duration
	lastLog := time.Now()
	lastRecognition := map[int]string{}
	started := lastLog
	lastStatus, statusFrames := started, 0
	lastAction := "尚无动作"
	refused, lastRefusal := 0, time.Time{}
	cam, result = control.RunRecovering(ctx, cam, vision.NewTracker(models, vision.DefaultTrackerConfig()), ctl, control.RunOptions{
		ObservePaused: func() bool { return view != nil && view.Visible() },
		InputError:    inj.TakeErr,
		OnInputError: func(err error) {
			refused++
			if time.Since(lastRefusal) >= 10*time.Second {
				logger.Printf("Windows 拒绝了输入（锁屏或 UAC 提示时会这样），已松开按住的键并继续运行（自上条记录起共 %d 次）：%v", refused, err)
				refused, lastRefusal = 0, time.Now()
			}
		},
		OnFrame: func(frame control.FrameResult) {
			running, events := ctl.Feedback()
			for _, event := range events {
				logger.Printf("action hand=%s gesture=%s name=%s event=%s", event.Hand, event.Pose, event.Binding, event.What)
				lastAction = event.Binding + " / " + event.What
			}
			if !ctl.Paused() {
				cursorHint.Frame(hinter.Load().Hints(frame.States, running, time.Now()), events)
			}
			if view != nil && view.Visible() {
				view.SetActions(running, events)
				view.Publish(frame.Frame, frame.States, ctl.Paused(), ctl.SwapHands(), *sensitivity, *dry)
			}
			frames++
			statusFrames++
			latency += frame.Latency
			if configurationPanel != nil && time.Since(lastStatus) >= 500*time.Millisecond {
				var hands []string
				for _, st := range frame.States {
					if st.Present {
						hands = append(hands, fmt.Sprintf("%s：%s", st.Side, st.RawPose))
					}
				}
				if len(hands) == 0 {
					hands = []string{"未检测到手"}
				}
				configurationPanel.PublishStatus(fmt.Sprintf("摄像头 %d / %dx%d  |  %.1f FPS  |  处理 %.1f ms\r\n%s  |  最近：%s", *cameraIndex, info.Width, info.Height, float64(statusFrames)/time.Since(lastStatus).Seconds(), float64(frame.Latency.Microseconds())/1000, strings.Join(hands, "；"), lastAction))
				lastStatus = time.Now()
				statusFrames = 0
			}
			recognitionChanged := false
			if *verbose {
				current := make(map[int]string, len(frame.States))
				for _, s := range frame.States {
					signature := fmt.Sprintf("%s/%s/%d/%t/%t/%v", s.Pose, s.RawPose, s.Side, s.Present, s.Lost, s.Fingers)
					current[s.ID] = signature
					if signature != lastRecognition[s.ID] {
						recognitionChanged = true
					}
				}
				recognitionChanged = recognitionChanged || len(current) != len(lastRecognition)
				lastRecognition = current
			}
			if *verbose && (recognitionChanged || time.Since(lastLog) >= time.Second) {
				labels := make([]string, 0, len(frame.States))
				for _, s := range frame.States {
					labels = append(labels, fmt.Sprintf("id=%d %s:pose=%s raw=%s any=%s present=%t fingers=%v open=%.2v leftScore=%.2f palm=%.0fpx facing=%d normalZ=%.2f", s.ID, s.Side, s.Pose, s.RawPose, s.AnyPose, s.Present, s.Fingers, s.Features.Openness, s.LeftScore, s.Features.PalmSize, s.Facing, s.Features.PalmNormalZ))
				}
				logger.Printf("frame=%d latency=%.1fms %s", frame.Sequence, float64(frame.Latency.Microseconds())/1000, strings.Join(labels, "; "))
				lastLog = time.Now()
			}
		},
	}, control.CameraRecovery{
		Open: func() (control.Camera, error) {
			capture, err := camera.Open(cameraConfig)
			if err != nil {
				return nil, err
			}
			info = capture.Info()
			return capture, nil
		},
		OnLost: func(err error) {
			cursorHint.Frame(nil, nil)
			logger.Printf("摄像头中断，已松开程序按住的键，稍后自动重新连接：%v", err)
			if configurationPanel != nil {
				configurationPanel.PublishStatus("摄像头已断开，正在自动重新连接…")
			}
		},
		OnRetry: func(attempt int, err error) {
			// After the first few, attempts come every 5 seconds; log about one a minute.
			if attempt <= 3 || attempt%12 == 0 {
				logger.Printf("第 %d 次重新连接摄像头未成功，继续重试：%v", attempt, err)
			}
			if configurationPanel != nil {
				configurationPanel.PublishStatus(fmt.Sprintf("摄像头已断开，第 %d 次重新连接未成功，继续重试\r\n%v", attempt, err))
			}
		},
		OnRecovered: func(control.Camera) {
			logger.Printf("摄像头已重新连接：%s，%dx%d，%.1f FPS", info.Name, info.Width, info.Height, info.FPS)
		},
	})
	ctl.SetPaused(true)
	if frames > 0 {
		logger.Printf("已停止：%d 帧，处理 %.1f FPS，采集到结果平均 %.1fms", frames, float64(frames)/time.Since(started).Seconds(), float64(latency.Microseconds())/float64(frames)/1000)
	} else {
		logger.Println("已停止")
	}
	return result
}

// Controls run independently of camera reads, so a stalled camera cannot block pause.
func watchControls(ctx context.Context, stop func(), ctl *control.Controller, logger *log.Logger, toggleHands func() error, showPanel func(), notifyPaused func(bool)) {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	f6, f8, f9 := wininput.KeyPressed(0x75), wininput.KeyPressed(0x77), wininput.KeyPressed(0x78)
	f7 := wininput.KeyPressed(0x76)
	for {
		select {
		case <-ctx.Done():
			ctl.SetPaused(true)
			return
		case <-ticker.C:
			next6, next8, next9 := wininput.KeyPressed(0x75), wininput.KeyPressed(0x77), wininput.KeyPressed(0x78)
			next7 := wininput.KeyPressed(0x76)
			if next7 && !f7 && showPanel != nil {
				showPanel()
			}
			f7 = next7
			if next9 && !f9 {
				stop()
				return
			}
			if next8 && !f8 {
				ctl.SetPaused(!ctl.Paused())
				logger.Printf("暂停=%t", ctl.Paused())
				if notifyPaused != nil {
					notifyPaused(ctl.Paused())
				}
			}
			if next6 && !f6 {
				if err := toggleHands(); err != nil {
					logger.Printf("左右手校正未保存：%v", err)
				}
			}
			f6, f8, f9 = next6, next8, next9
		}
	}
}
