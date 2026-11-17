package core

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	startTimeout = 15 * time.Second
	stopTimeout  = 30 * time.Second
)

// ServiceManager 负责受管服务的增删启停与状态维护。
type ServiceManager struct {
	mu       sync.RWMutex
	services map[string]*Service
}

// NewServiceManager 创建服务管理器。
func NewServiceManager() *ServiceManager {
	return &ServiceManager{services: make(map[string]*Service)}
}

// Load 从注册表索引重新加载全部受管服务。
func (m *ServiceManager) Load() error {
	conn, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("无法连接服务管理器: %w", err)
	}
	defer conn.Disconnect()

	names, err := managedServiceNames()
	if err != nil {
		return fmt.Errorf("读取受管服务索引失败: %w", err)
	}

	services := make(map[string]*Service, len(names))
	for _, name := range names {
		s, err := loadService(conn, name)
		if err != nil {
			continue
		}
		services[name] = s
	}

	m.mu.Lock()
	m.services = services
	m.mu.Unlock()
	return nil
}

func loadService(conn *mgr.Mgr, name string) (*Service, error) {
	s, err := loadServiceParams(name)
	if err != nil {
		return nil, err
	}

	s.Status, s.PID = "未知", 0
	if obj, err := conn.OpenService(name); err == nil {
		if st, err := obj.Query(); err == nil {
			s.Status = statusText(st.State)
			s.PID = st.ProcessId
		}
		if cfg, err := obj.Config(); err == nil {
			s.StartupType = cfg.StartType
		}
		obj.Close()
	}
	return s, nil
}

func queryStatus(conn *mgr.Mgr, name string) (string, uint32) {
	s, err := conn.OpenService(name)
	if err != nil {
		return "未知", 0
	}
	defer s.Close()

	st, err := s.Query()
	if err != nil {
		return "未知", 0
	}
	return statusText(st.State), st.ProcessId
}

func statusText(state svc.State) string {
	switch state {
	case svc.Stopped:
		return "已停止"
	case svc.StartPending:
		return "启动中"
	case svc.StopPending:
		return "停止中"
	case svc.Running:
		return "运行中"
	case svc.Paused:
		return "已暂停"
	case svc.PausePending:
		return "暂停中"
	case svc.ContinuePending:
		return "恢复中"
	default:
		return "未知"
	}
}

// Snapshot 返回当前服务列表的副本，按显示名排序。
func (m *ServiceManager) Snapshot() []*Service {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := make([]*Service, 0, len(m.services))
	for _, s := range m.services {
		c := *s
		list = append(list, &c)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})
	return list
}

// RefreshStatuses 批量刷新服务运行状态与 PID。
func (m *ServiceManager) RefreshStatuses() {
	conn, err := mgr.Connect()
	if err != nil {
		return
	}
	defer conn.Disconnect()

	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for id, s := range m.services {
		status, pid := queryStatus(conn, id)
		s.Status = status
		s.PID = pid
		s.UpdatedAt = now
	}
}

// Create 创建并托管一个新服务。
func (m *ServiceManager) Create(cfg ServiceConfig) error {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		return fmt.Errorf("服务名称不能为空")
	}
	if !validServiceName(name) {
		return fmt.Errorf("服务名称只能包含字母、数字、下划线、短横线和空格")
	}

	exePath := strings.TrimSpace(cfg.ExePath)
	if exePath == "" {
		return fmt.Errorf("可执行文件路径不能为空")
	}
	if st, err := os.Stat(exePath); err != nil || st.IsDir() {
		return fmt.Errorf("可执行文件不存在: %s", exePath)
	}

	envVars, err := NormalizeEnvVars(cfg.EnvVars)
	if err != nil {
		return err
	}
	cfg.EnvVars = envVars

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("无法获取当前程序路径: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	serviceName := generateServiceName(name)
	if keyExists(svcKeyRoot + `\` + serviceName) {
		return fmt.Errorf("服务 %s 已存在", serviceName)
	}

	startType := cfg.StartupType
	if startType == 0 {
		startType = windows.SERVICE_AUTO_START
	}

	conn, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("无法连接服务管理器: %w", err)
	}
	defer conn.Disconnect()

	svcObj, err := conn.CreateService(serviceName, exe, mgr.Config{
		DisplayName:  name,
		StartType:    startType,
		ErrorControl: mgr.ErrorNormal,
		Description:  "Managed by Windows Service Manager: " + name,
	}, "--service-wrapper", serviceName)
	if err != nil {
		return fmt.Errorf("创建服务失败: %w", err)
	}
	defer svcObj.Close()

	// 失败恢复：1 分钟后重启，最多连续 3 次，24 小时后重置计数。
	_ = svcObj.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: time.Minute},
		{Type: mgr.ServiceRestart, Delay: time.Minute},
		{Type: mgr.ServiceRestart, Delay: time.Minute},
	}, 86400)

	cfg.Name = name
	if err := saveServiceParams(serviceName, cfg); err != nil {
		_ = svcObj.Delete()
		return fmt.Errorf("写入服务配置失败: %w", err)
	}
	addToManagedIndex(serviceName)

	return m.Load()
}

// Update 修改已有服务的显示名、启动类型、目标程序与参数。
func (m *ServiceManager) Update(id string, cfg ServiceConfig) error {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		return fmt.Errorf("服务名称不能为空")
	}
	if !validServiceName(name) {
		return fmt.Errorf("服务名称只能包含字母、数字、下划线、短横线和空格")
	}

	exePath := strings.TrimSpace(cfg.ExePath)
	if exePath == "" {
		return fmt.Errorf("可执行文件路径不能为空")
	}
	if st, err := os.Stat(exePath); err != nil || st.IsDir() {
		return fmt.Errorf("可执行文件不存在: %s", exePath)
	}

	envVars, err := NormalizeEnvVars(cfg.EnvVars)
	if err != nil {
		return err
	}
	cfg.EnvVars = envVars

	startType := cfg.StartupType
	if startType == 0 {
		startType = windows.SERVICE_AUTO_START
	}

	conn, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("无法连接服务管理器: %w", err)
	}
	defer conn.Disconnect()

	s, err := conn.OpenService(id)
	if err != nil {
		return fmt.Errorf("打开服务失败: %w", err)
	}
	defer s.Close()

	displayPtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return fmt.Errorf("服务名称无效: %w", err)
	}

	// ChangeServiceConfig 的“保持不变”必须用 SERVICE_NO_CHANGE(0xFFFFFFFF)，
	// 传 0 会被视为非法参数（The parameter is incorrect）。
	if err := windows.ChangeServiceConfig(
		s.Handle,
		windows.SERVICE_NO_CHANGE,
		startType,
		windows.SERVICE_NO_CHANGE,
		nil, nil, nil, nil, nil, nil,
		displayPtr,
	); err != nil {
		return fmt.Errorf("更新服务配置失败: %w", err)
	}

	desc := windows.SERVICE_DESCRIPTION{Description: displayPtr}
	if err := windows.ChangeServiceConfig2(s.Handle, windows.SERVICE_CONFIG_DESCRIPTION, (*byte)(unsafe.Pointer(&desc))); err != nil {
		return fmt.Errorf("更新服务描述失败: %w", err)
	}

	cfg.Name = name
	if err := updateServiceParams(id, cfg); err != nil {
		return fmt.Errorf("写入服务配置失败: %w", err)
	}

	return m.Load()
}

// Start 启动指定服务并等待进入可运行状态。
func (m *ServiceManager) Start(id string) error {
	conn, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("无法连接服务管理器: %w", err)
	}
	defer conn.Disconnect()

	s, err := conn.OpenService(id)
	if err != nil {
		return fmt.Errorf("打开服务失败: %w", err)
	}
	defer s.Close()

	st, err := s.Query()
	if err != nil {
		return fmt.Errorf("查询服务状态失败: %w", err)
	}
	if st.State != svc.Running && st.State != svc.StartPending {
		if err := s.Start(); err != nil {
			return fmt.Errorf("启动服务失败: %w", err)
		}
	}

	deadline := time.Now().Add(startTimeout)
	for time.Now().Before(deadline) {
		st, err = s.Query()
		if err == nil && (st.State == svc.Running || st.State == svc.StartPending) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	m.refreshOne(conn, id)
	return nil
}

// Stop 停止指定服务并等待其完全停止。
func (m *ServiceManager) Stop(id string) error {
	conn, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("无法连接服务管理器: %w", err)
	}
	defer conn.Disconnect()

	s, err := conn.OpenService(id)
	if err != nil {
		return fmt.Errorf("打开服务失败: %w", err)
	}
	defer s.Close()

	st, err := s.Query()
	if err != nil {
		return fmt.Errorf("查询服务状态失败: %w", err)
	}
	if st.State == svc.Running || st.State == svc.StartPending {
		if _, err := s.Control(svc.Stop); err != nil {
			return fmt.Errorf("停止服务失败: %w", err)
		}

		deadline := time.Now().Add(stopTimeout)
		for time.Now().Before(deadline) {
			st, err = s.Query()
			if err == nil && st.State == svc.Stopped {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	m.refreshOne(conn, id)
	return nil
}

// Delete 停止并删除服务，同时清理注册表配置与索引。
func (m *ServiceManager) Delete(id string) error {
	conn, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("无法连接服务管理器: %w", err)
	}
	defer conn.Disconnect()

	s, err := conn.OpenService(id)
	if err != nil {
		return fmt.Errorf("打开服务失败: %w", err)
	}
	defer s.Close()

	if st, err := s.Query(); err == nil && (st.State == svc.Running || st.State == svc.StartPending) {
		if _, err := s.Control(svc.Stop); err == nil {
			deadline := time.Now().Add(stopTimeout)
			for time.Now().Before(deadline) {
				if st, err := s.Query(); err == nil && st.State == svc.Stopped {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
		}
	}

	if err := s.Delete(); err != nil {
		return fmt.Errorf("删除服务失败: %w", err)
	}

	removeFromManagedIndex(id)
	deleteServiceParams(id)

	m.mu.Lock()
	delete(m.services, id)
	m.mu.Unlock()
	return nil
}

func (m *ServiceManager) refreshOne(conn *mgr.Mgr, id string) {
	status, pid := queryStatus(conn, id)

	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.services[id]; ok {
		s.Status = status
		s.PID = pid
		s.UpdatedAt = time.Now()
	}
}

func validServiceName(name string) bool {
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == ' ' {
			continue
		}
		return false
	}
	return true
}

func generateServiceName(displayName string) string {
	var b strings.Builder
	for _, r := range displayName {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}

	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("WinSvcMgr_%s_%d", b.String(), time.Now().UnixNano()%100000000)
	}
	return "WinSvcMgr_" + b.String() + "_" + hex.EncodeToString(buf)
}
