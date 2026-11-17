package ui

import (
	_ "embed"
	"os"
	"path/filepath"

	"github.com/lxn/walk"
)

//go:embed icon.ico
var iconData []byte

// loadAppIcon 把内嵌图标释放到临时目录后加载，避免依赖外部资源文件。
func loadAppIcon() *walk.Icon {
	if len(iconData) == 0 {
		return nil
	}

	path := filepath.Join(os.TempDir(), "svcctl_app.ico")
	if _, err := os.Stat(path); err != nil {
		if err := os.WriteFile(path, iconData, 0o644); err != nil {
			return nil
		}
	}

	icon, err := walk.NewIconFromFile(path)
	if err != nil {
		return nil
	}
	return icon
}
