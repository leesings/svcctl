package main

import (
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"

	"svcctl/internal/core"
	"svcctl/internal/ui"
)

const (
	mutexName  = `Global\SvcCtl_SingleInstance_Mutex`
	windowName = ui.WindowTitle
)

var singleInstanceHandle windows.Handle

func main() {
	// 服务模式：由 SCM 以 “svcctl.exe --service-wrapper <name>” 启动。
	if len(os.Args) >= 3 && os.Args[1] == "--service-wrapper" {
		runServiceMode(os.Args[2])
		return
	}

	handle, first := core.AcquireSingleInstance(mutexName)
	if !first {
		activateExistingWindow()
		return
	}
	singleInstanceHandle = handle

	// 服务管理需要管理员权限；未提权时静默提权重启。
	if !core.IsElevated() {
		_ = windows.CloseHandle(handle)
		if err := core.RelaunchElevated(); err != nil {
			messageBox("需要管理员权限", "以管理员身份重新启动失败: "+err.Error())
		}
		return
	}

	if err := ui.Run(); err != nil {
		messageBox("启动失败", err.Error())
	}
}

func runServiceMode(name string) {
	if exe, err := os.Executable(); err == nil {
		if dir := filepath.Dir(exe); dir != "" {
			_ = os.Chdir(dir)
		}
	}
	if err := core.RunService(name); err != nil {
		os.Exit(1)
	}
}

// activateExistingWindow 让已在运行的实例显示到前台。
func activateExistingWindow() {
	title, err := windows.UTF16PtrFromString(windowName)
	if err != nil {
		return
	}

	for i := 0; i < 5; i++ {
		hwnd := win.FindWindow(nil, title)
		if hwnd != 0 {
			win.ShowWindow(hwnd, win.SW_RESTORE)
			win.SetForegroundWindow(hwnd)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// messageBox 在 GUI 子系统下也能给出错误提示。
func messageBox(title, text string) {
	win.MessageBox(
		0,
		syscall.StringToUTF16Ptr(text),
		syscall.StringToUTF16Ptr(title),
		win.MB_OK|win.MB_ICONERROR,
	)
}
