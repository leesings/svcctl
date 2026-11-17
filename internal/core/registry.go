package core

import (
	"fmt"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"golang.org/x/sys/windows/registry"
)

const (
	svcKeyRoot      = `SYSTEM\CurrentControlSet\Services`
	appKeyRoot      = `SOFTWARE\WindowsServiceManager`
	managedIndexKey = appKeyRoot + `\ManagedServices`
	managedByValue  = "SvcCtl"
)

// DefaultRetentionDays 是日志默认保留天数。
const DefaultRetentionDays = 7

var errNotManaged = fmt.Errorf("service is not managed by svcctl")

func serviceParamsPath(name string) string {
	return svcKeyRoot + `\` + name + `\Parameters`
}

func keyExists(path string) bool {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	k.Close()
	return true
}

// saveServiceParams 把服务配置写入注册表 Parameters 子键。
func saveServiceParams(serviceName string, cfg ServiceConfig) error {
	return saveServiceParamsWithCreated(serviceName, cfg, time.Time{})
}

// saveServiceParamsWithCreated 写入配置，createdAt 为零值时使用当前时间。
func saveServiceParamsWithCreated(serviceName string, cfg ServiceConfig, createdAt time.Time) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, serviceParamsPath(serviceName), registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	workingDir := cfg.WorkingDir
	if workingDir == "" {
		workingDir = filepath.Dir(cfg.ExePath)
	}

	autoRestart := uint32(0)
	if cfg.AutoRestart {
		autoRestart = 1
	}

	if err := k.SetStringValue("ExePath", cfg.ExePath); err != nil {
		return err
	}
	if err := k.SetStringValue("Args", cfg.Args); err != nil {
		return err
	}
	if err := k.SetStringValue("WorkingDir", workingDir); err != nil {
		return err
	}
	if err := k.SetStringValue("DisplayName", cfg.Name); err != nil {
		return err
	}
	if err := k.SetDWordValue("AutoRestart", autoRestart); err != nil {
		return err
	}
	created := createdAt
	if created.IsZero() {
		created = time.Now()
	}
	if err := k.SetStringValue("CreatedAt", created.Format(time.RFC3339)); err != nil {
		return err
	}
	if len(cfg.EnvVars) == 0 {
		_ = k.DeleteValue("Environment")
	} else if err := k.SetStringsValue("Environment", cfg.EnvVars); err != nil {
		return err
	}
	return k.SetStringValue("ManagedBy", managedByValue)
}

// updateServiceParams 更新配置并保留原始创建时间。
func updateServiceParams(serviceName string, cfg ServiceConfig) error {
	var created time.Time
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, serviceParamsPath(serviceName), registry.QUERY_VALUE); err == nil {
		if v, _, err := k.GetStringValue("CreatedAt"); err == nil && v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				created = t
			}
		}
		k.Close()
	}
	return saveServiceParamsWithCreated(serviceName, cfg, created)
}

// loadServiceParams 读取注册表中的服务配置（不含运行状态）。
func loadServiceParams(name string) (*Service, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, serviceParamsPath(name), registry.QUERY_VALUE)
	if err != nil {
		return nil, err
	}
	defer k.Close()

	exePath, _, err := k.GetStringValue("ExePath")
	if err != nil || exePath == "" {
		return nil, errNotManaged
	}

	display, _, _ := k.GetStringValue("DisplayName")
	if display == "" {
		display = name
	}
	args, _, _ := k.GetStringValue("Args")
	workingDir, _, _ := k.GetStringValue("WorkingDir")

	autoRestart := false
	if v, _, err := k.GetIntegerValue("AutoRestart"); err == nil && v == 1 {
		autoRestart = true
	}

	envVars, _, _ := k.GetStringsValue("Environment")

	created := time.Now()
	if v, _, err := k.GetStringValue("CreatedAt"); err == nil && v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			created = t
		}
	}

	return &Service{
		ID:          name,
		Name:        display,
		ExePath:     exePath,
		Args:        args,
		WorkingDir:  workingDir,
		AutoStart:   true,
		AutoRestart: autoRestart,
		EnvVars:     envVars,
		CreatedAt:   created,
		UpdatedAt:   time.Now(),
	}, nil
}

// managedServiceNames 返回受管服务名列表；索引缺失时退化为全盘扫描。
func managedServiceNames() ([]string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, managedIndexKey, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		if err == syscall.ERROR_FILE_NOT_FOUND {
			return legacyScan()
		}
		return nil, err
	}
	defer k.Close()

	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

// legacyScan 扫描所有服务，找出带 Parameters\ExePath 的项并补建索引。
func legacyScan() ([]string, error) {
	root, err := registry.OpenKey(registry.LOCAL_MACHINE, svcKeyRoot, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	names, err := root.ReadSubKeyNames(-1)
	if err != nil {
		return nil, err
	}

	var managed []string
	for _, name := range names {
		if keyExists(serviceParamsPath(name)) {
			managed = append(managed, name)
			addToManagedIndex(name)
		}
	}
	sort.Strings(managed)
	return managed, nil
}

func addToManagedIndex(serviceName string) {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, managedIndexKey+`\`+serviceName, registry.SET_VALUE)
	if err == nil {
		k.Close()
	}
}

func removeFromManagedIndex(serviceName string) {
	_ = registry.DeleteKey(registry.LOCAL_MACHINE, managedIndexKey+`\`+serviceName)
}

// deleteRegistryTree 递归删除注册表键。
func deleteRegistryTree(path string) error {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return err
	}
	names, _ := k.ReadSubKeyNames(-1)
	k.Close()

	for _, name := range names {
		_ = deleteRegistryTree(path + `\` + name)
	}
	return registry.DeleteKey(registry.LOCAL_MACHINE, path)
}

func deleteServiceParams(serviceName string) {
	_ = deleteRegistryTree(serviceParamsPath(serviceName))
}

// GetRetentionDays 读取日志保留天数配置。
func GetRetentionDays() int {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, appKeyRoot, registry.QUERY_VALUE)
	if err != nil {
		return DefaultRetentionDays
	}
	defer k.Close()

	v, _, err := k.GetIntegerValue("LogRetentionDays")
	if err != nil || v < 1 || v > 3650 {
		return DefaultRetentionDays
	}
	return int(v)
}

// SetRetentionDays 保存日志保留天数配置。
func SetRetentionDays(days int) error {
	if days < 1 {
		days = 1
	}
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, appKeyRoot, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetDWordValue("LogRetentionDays", uint32(days))
}
