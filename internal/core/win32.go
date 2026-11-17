package core

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var procSendMessageTimeoutW = windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")

const (
	hwndBroadcast   = 0xffff
	wmSettingChange = 0x001A
	smtoAbortIfHung = 0x0002
)

// broadcastEnvironmentChange 通知系统环境变量已变更。
func broadcastEnvironmentChange() {
	p, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	var result uintptr
	_, _, _ = procSendMessageTimeoutW.Call(
		hwndBroadcast,
		wmSettingChange,
		0,
		uintptr(unsafe.Pointer(p)),
		smtoAbortIfHung,
		5000,
		uintptr(unsafe.Pointer(&result)),
	)
}

// OpenFolder 用资源管理器打开指定目录。
func OpenFolder(path string) error {
	return shellOpen(path)
}

// OpenURL 用系统默认程序（通常是浏览器）打开链接。
func OpenURL(url string) error {
	return shellOpen(url)
}

// shellOpen 调用 ShellExecute("open", target) 打开文件、目录或链接。
func shellOpen(target string) error {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, nil, nil, windows.SW_SHOWNORMAL)
}
