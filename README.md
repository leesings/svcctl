# svcctl

使用 **Go + walk** 的 Windows 后台服务管理工具，可作为 NSSM 的可视化替代品。

基于 https://github.com/sky22333/services 的（Go + walk 复刻版）

## 功能

- 列出由本工具创建的服务（名称、标识、状态、PID、自动重启、创建时间、程序路径）
- 创建 / 编辑服务：包装任意可执行文件，支持启动参数、工作目录、启动类型、失败自动重启
- 服务环境变量：弹窗按「一行一个 KEY=VALUE」配置，随服务配置持久化并传给目标进程
- 失败自动重启默认开启，次数不限，指数退避
- 启动 / 停止 / 删除服务，失败恢复由 SCM 按策略自动拉起
- 查看服务日志（增量 tail，最多保留 3000 行），打开日志目录
- 系统 PATH 环境变量追加（去重并广播 `WM_SETTINGCHANGE`）
- 日志保留天数设置，启动时与每日自动清理
- 托盘图标（双击显示、右键显示/退出），关闭窗口最小化到托盘
- 单实例运行，重复启动自动激活已有窗口
- 非管理员启动时自动通过 UAC 提权重启

## 重要：Windows 7 支持

Windows 7 只能运行由 **Go 1.20.x** 构建的程序（Go 1.21 起官方要求 Windows 10 / Server 2016 及以上）。

```powershell
# 方式一：自动下载 Go 1.20.14 到 .\toolchain 并构建
.\build.ps1 -DownloadToolchain

# 方式二：已安装 Go 1.20.x，直接指定
$env:SVCCTL_GO = "C:\Go1.20\bin\go.exe"
.\build.ps1
```

> 若用 Go 1.21+ 构建，程序无法在 Win7 上启动，构建脚本会给出警告。

调试时可输出控制台窗口：

```powershell
.\build.ps1 -Console
```

> **不要直接用 `go build` 打包分发。** walk 依赖 Common Controls 6.0 清单，必须通过 `rsrc` 将 `svcctl.manifest` 嵌入 exe，否则启动时会报 `TTM_ADDTOOL failed`。`build.ps1` 会自动检测并安装 `rsrc`（`go install github.com/akavel/rsrc@latest`），随后生成 `cmd/svcctl/rsrc.syso` 再编译。

## 运行

```text
svcctl.exe                  # 图形界面
svcctl.exe --service-wrapper <服务名>   # 服务模式，由 SCM 调用
```

首次运行会请求管理员权限。创建服务后，SCM 以 `svcctl.exe --service-wrapper <name>` 启动包装器，包装器读取注册表配置并拉起目标进程。

## 数据位置

| 内容 | 位置                                                                       |
| --- |--------------------------------------------------------------------------|
| 服务参数 | `HKEY_LOCAL_MACHINE\SYSTEM\CurrentControlSet\Services\<服务标识>\Parameters` |
| 受管服务索引 | `HKEY_LOCAL_MACHINE\SOFTWARE\WindowsServiceManager\ManagedServices`      |
| 日志保留天数 | `HKEY_LOCAL_MACHINE\SOFTWARE\WindowsServiceManager\LogRetentionDays`     |
| 服务日志 | `%ProgramData%\windows_service_logs\<服务标识>_<时间戳>.log`                     |

其中 `Parameters\Environment` 以 `REG_MULTI_SZ` 保存 `KEY=VALUE` 形式的环境变量。

服务标识生成规则：`WinSvcMgr_<名称字母数字>_<8位随机十六进制>`。

## 目录结构

```text
cmd/svcctl/            入口：服务模式分流、提权、单实例
internal/core/         服务管理、注册表、包装器、日志、环境变量、Win32
internal/ui/           walk 主窗口、对话框、日志窗口、托盘
internal/ui/icon.ico   内嵌图标
svcctl.manifest        可选清单（rsrc 安装后自动嵌入）
build.ps1              构建脚本
```

## 依赖版本

- `github.com/lxn/walk`：`v0.0.0-20210112085537-c389da54e794`
- `golang.org/x/sys`：`v0.15.0`
- Go：`1.20.x`（最后一个支持 Windows 7 的版本）
