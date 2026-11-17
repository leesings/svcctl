package core

import (
	"errors"

	"golang.org/x/sys/windows"
)

// AcquireSingleInstance 创建命名互斥量。第二个参数表示是否为首个实例。
func AcquireSingleInstance(name string) (windows.Handle, bool) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, true
	}

	h, err := windows.CreateMutex(nil, false, p)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		// 创建失败时不阻止程序启动。
		return h, true
	}
	return h, !errors.Is(err, windows.ERROR_ALREADY_EXISTS)
}
