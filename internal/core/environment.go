package core

import (
	"fmt"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const envKeyPath = `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`

// NormalizeEnvVars 校验并规范化 KEY=VALUE 列表：去空行、去注释、去重。
func NormalizeEnvVars(vars []string) ([]string, error) {
	var normalized []string
	seen := make(map[string]bool, len(vars))

	for _, raw := range vars {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r\n"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		idx := strings.Index(line, "=")
		if idx <= 0 {
			return nil, fmt.Errorf("环境变量格式应为 KEY=VALUE: %s", line)
		}

		key := strings.TrimSpace(line[:idx])
		if key == "" {
			return nil, fmt.Errorf("环境变量名不能为空: %s", line)
		}
		if strings.ContainsAny(key, " \t") {
			return nil, fmt.Errorf("环境变量名不能包含空格: %s", key)
		}

		upper := strings.ToUpper(key)
		if seen[upper] {
			return nil, fmt.Errorf("环境变量 %s 重复", key)
		}
		seen[upper] = true

		normalized = append(normalized, key+"="+line[idx+1:])
	}

	return normalized, nil
}

// AddToSystemPath 把目录追加到系统 PATH（去重，保留原有 %VAR% 写法）。
func AddToSystemPath(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return fmt.Errorf("目录路径不能为空")
	}

	k, err := registry.OpenKey(registry.LOCAL_MACHINE, envKeyPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("无法打开系统环境变量注册表键: %w", err)
	}
	defer k.Close()

	current, _, err := k.GetStringValue("Path")
	if err != nil && err != syscall.ERROR_FILE_NOT_FOUND {
		return fmt.Errorf("读取系统 PATH 失败: %w", err)
	}

	for _, part := range strings.Split(current, ";") {
		if strings.EqualFold(strings.TrimSpace(part), dir) {
			return nil
		}
	}

	newPath := strings.TrimRight(current, ";")
	if newPath != "" {
		newPath += ";"
	}
	newPath += dir

	if err := k.SetExpandStringValue("Path", newPath); err != nil {
		return fmt.Errorf("写入系统 PATH 失败: %w", err)
	}

	broadcastEnvironmentChange()
	return nil
}
