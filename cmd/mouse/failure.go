package main

import (
	"errors"
	"fmt"
)

// What someone who just unpacked the program can do about the failures they
// are likely to meet.
const (
	cameraHint = "请检查：\n" +
		"1. 摄像头已经插好，没有被视频会议、相机等其他程序占用；\n" +
		"2. Windows 设置 → 隐私和安全性（Windows 10 叫“隐私”）→ 摄像头（相机）里，摄像头访问和“允许桌面应用访问你的摄像头”都已打开。"
	runtimeHint = "请把压缩包完整解压后再运行，程序旁边要有 runtime 文件夹。\n" +
		"仍然不行的话，安装微软 VC++ 运行库后再试：https://aka.ms/vs/17/release/vc_redist.x64.exe"
	runningHint = "按 F7 打开正在运行的那个的配置面板，按 F9 让它退出。"
)

var errAlreadyRunning = errors.New("已经有一个手势鼠标在运行")

func settingsHint(path string) string {
	return fmt.Sprintf("设置文件可能已损坏：%s\n把它改名或删除后再打开程序，会用默认设置重新生成。", path)
}

type hintedError struct {
	err  error
	hint string
}

func (e hintedError) Error() string { return e.err.Error() }
func (e hintedError) Unwrap() error { return e.err }

func withHint(err error, hint string) error { return hintedError{err, hint} }

// failureText explains to someone without a console why the program exits.
func failureText(err error) string {
	text := "手势鼠标已退出。\n\n原因：" + err.Error()
	var hinted hintedError
	if errors.As(err, &hinted) {
		text += "\n\n" + hinted.hint
	}
	return text
}
