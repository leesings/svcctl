package core

import "time"

// Service 描述一个由本工具托管的 Windows 服务。
type Service struct {
	ID          string
	Name        string
	ExePath     string
	Args        string
	WorkingDir  string
	Status      string
	PID         uint32
	AutoStart   bool
	AutoRestart bool
	// StartupType 为 2(自动) 或 3(手动)，用于编辑时回显。
	StartupType uint32
	// EnvVars 为传入目标进程的环境变量，格式 KEY=VALUE。
	EnvVars   []string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ServiceConfig 是创建服务时的输入参数。
type ServiceConfig struct {
	Name        string
	ExePath     string
	Args        string
	WorkingDir  string
	AutoRestart bool
	// StartupType 取 windows.SERVICE_AUTO_START 或 windows.SERVICE_DEMAND_START。
	StartupType uint32
	// EnvVars 为传入目标进程的环境变量，格式 KEY=VALUE。
	EnvVars []string
}
