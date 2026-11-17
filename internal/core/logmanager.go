package core

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// LogDir 返回日志目录：%ProgramData%\windows_service_logs。
func LogDir() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "windows_service_logs")
}

// EnsureLogDir 确保日志目录存在。
func EnsureLogDir() error {
	return os.MkdirAll(LogDir(), 0o755)
}

func creationTime(fi os.FileInfo) time.Time {
	if data, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok {
		return time.Unix(0, data.CreationTime.Nanoseconds())
	}
	return fi.ModTime()
}

// LatestLogPath 返回指定服务最新的日志文件路径，未找到时返回空串。
func LatestLogPath(serviceID string) string {
	entries, err := os.ReadDir(LogDir())
	if err != nil {
		return ""
	}

	prefix := serviceID + "_"
	var latest string
	var latestTime time.Time
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(strings.ToLower(name), ".log") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if ct := creationTime(info); ct.After(latestTime) {
			latestTime = ct
			latest = filepath.Join(LogDir(), name)
		}
	}
	return latest
}

// CleanupOldLogs 删除早于保留天数的日志文件。
func CleanupOldLogs(retentionDays int) {
	if retentionDays < 1 {
		retentionDays = DefaultRetentionDays
	}
	entries, err := os.ReadDir(LogDir())
	if err != nil {
		return
	}

	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".log") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if creationTime(info).Before(cutoff) {
			_ = os.Remove(filepath.Join(LogDir(), entry.Name()))
		}
	}
}

// NewServiceLogPath 为某个服务生成新的日志文件名。
func NewServiceLogPath(serviceID string) string {
	_ = EnsureLogDir()
	name := serviceID + "_" + time.Now().Format("20060102_150405") + ".log"
	return filepath.Join(LogDir(), name)
}
