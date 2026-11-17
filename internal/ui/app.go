package ui

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/lxn/walk"
	"github.com/lxn/walk/declarative"
	"github.com/lxn/win"

	"svcctl/internal/core"
)

const (
	// WindowTitle 同时用于单实例激活时查找窗口。
	WindowTitle = "Services Manager"

	refreshVisible = 2 * time.Second
	refreshHidden  = 30 * time.Second
)

// App 持有主窗口及其依赖。
type App struct {
	mw      *walk.MainWindow
	tv      *walk.TableView
	model   *serviceModel
	status  *walk.StatusBarItem
	manager *core.ServiceManager
	icon    *walk.Icon
	tray    *walk.NotifyIcon
	ctxMenu *walk.Menu

	realExit atomic.Bool
	stopCh   chan struct{}
}

// Run 创建并运行主窗口。
func Run() error {
	a := &App{
		manager: core.NewServiceManager(),
		stopCh:  make(chan struct{}),
	}
	return a.run()
}

func (a *App) run() error {
	_ = core.EnsureLogDir()
	go core.CleanupOldLogs(core.GetRetentionDays())

	_ = a.manager.Load()
	a.model = newServiceModel(a.manager.Snapshot())
	a.icon = loadAppIcon()

	err := declarative.MainWindow{
		AssignTo: &a.mw,
		Title:    WindowTitle,
		Size:     declarative.Size{Width: 1060, Height: 700},
		MinSize:  declarative.Size{Width: 900, Height: 520},
		Layout:   declarative.VBox{Margins: declarative.Margins{Left: 9, Top: 9, Right: 9, Bottom: 9}},
		StatusBarItems: []declarative.StatusBarItem{
			{AssignTo: &a.status},
		},
		Children: []declarative.Widget{
			declarative.Composite{
				Layout: declarative.HBox{MarginsZero: true},
				Children: []declarative.Widget{
					declarative.PushButton{
						Text:      "添加服务",
						MinSize:   declarative.Size{Width: 96, Height: 30},
						OnClicked: a.onAddService,
					},
					declarative.PushButton{
						Text:      "刷新",
						MinSize:   declarative.Size{Width: 72, Height: 30},
						OnClicked: a.onRefresh,
					},
					declarative.PushButton{
						Text:      "环境变量",
						MinSize:   declarative.Size{Width: 96, Height: 30},
						OnClicked: a.onEnvVars,
					},
					declarative.PushButton{
						Text:      "设置",
						MinSize:   declarative.Size{Width: 72, Height: 30},
						OnClicked: a.onSettings,
					},
					declarative.HSpacer{},
					declarative.PushButton{
						Text:      "启动",
						MinSize:   declarative.Size{Width: 72, Height: 30},
						OnClicked: a.onStart,
					},
					declarative.PushButton{
						Text:      "停止",
						MinSize:   declarative.Size{Width: 72, Height: 30},
						OnClicked: a.onStop,
					},
					declarative.PushButton{
						Text:      "编辑",
						MinSize:   declarative.Size{Width: 72, Height: 30},
						OnClicked: a.onEdit,
					},
					declarative.PushButton{
						Text:      "日志",
						MinSize:   declarative.Size{Width: 72, Height: 30},
						OnClicked: a.onLogs,
					},
					declarative.PushButton{
						Text:      "删除",
						MinSize:   declarative.Size{Width: 72, Height: 30},
						OnClicked: a.onDelete,
					},
				},
			},
			declarative.TableView{
				AssignTo:            &a.tv,
				Model:               a.model,
				AlternatingRowBG:    true,
				ColumnsSizable:      true,
				LastColumnStretched: true,
				Columns: []declarative.TableViewColumn{
					{Title: "服务名称", Width: 150},
					{Title: "服务标识", Width: 180},
					{Title: "状态", Width: 80},
					{Title: "PID", Width: 60, Alignment: declarative.AlignFar},
					{Title: "自动重启", Width: 80},
					{Title: "创建时间", Width: 135},
					{Title: "程序路径", Width: 300},
				},
				StyleCell:       a.styleCell,
				OnItemActivated: a.onLogs,
			},
		},
	}.Create()
	if err != nil {
		return err
	}

	if a.icon != nil {
		_ = a.mw.SetIcon(a.icon)
	}

	a.setupWindow()
	a.setupTray()
	a.startTicker()
	a.updateStatus(fmt.Sprintf("已加载 %d 个服务。", a.model.RowCount()))

	a.mw.Run()

	close(a.stopCh)
	a.disposeTray()
	return nil
}

func (a *App) setupWindow() {
	a.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if !a.realExit.Load() {
			*canceled = true
			a.mw.Hide()
		}
	})

	a.buildContextMenu()
}

func (a *App) buildContextMenu() {
	menu, err := walk.NewMenu()
	if err != nil {
		return
	}

	add := func(text string, fn func()) {
		action := walk.NewAction()
		_ = action.SetText(text)
		action.Triggered().Attach(fn)
		_ = menu.Actions().Add(action)
	}
	add("启动", a.onStart)
	add("停止", a.onStop)
	add("编辑", a.onEdit)
	add("查看日志", a.onLogs)
	add("删除", a.onDelete)

	a.ctxMenu = menu
	a.tv.SetContextMenu(menu)
}

func (a *App) setupTray() {
	ni, err := walk.NewNotifyIcon(a.mw)
	if err != nil {
		return
	}
	a.tray = ni

	if a.icon != nil {
		_ = ni.SetIcon(a.icon)
	}
	_ = ni.SetToolTip(WindowTitle + " - 双击显示窗口")

	showAction := walk.NewAction()
	_ = showAction.SetText("显示窗口")
	showAction.Triggered().Attach(a.showWindow)
	_ = ni.ContextMenu().Actions().Add(showAction)

	exitAction := walk.NewAction()
	_ = exitAction.SetText("退出")
	exitAction.Triggered().Attach(a.exitApp)
	_ = ni.ContextMenu().Actions().Add(exitAction)

	var lastClick time.Time
	ni.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
		if button != walk.LeftButton {
			return
		}
		now := time.Now()
		if now.Sub(lastClick) < 500*time.Millisecond {
			a.showWindow()
		}
		lastClick = now
	})

	_ = ni.SetVisible(true)
}

func (a *App) disposeTray() {
	if a.tray != nil {
		a.tray.Dispose()
		a.tray = nil
	}
}

func (a *App) showWindow() {
	if a.mw == nil {
		return
	}
	a.mw.Show()
	win.ShowWindow(a.mw.Handle(), win.SW_RESTORE)
	win.SetForegroundWindow(a.mw.Handle())
}

func (a *App) exitApp() {
	a.realExit.Store(true)
	a.disposeTray()
	walk.App().Exit(0)
}

func (a *App) startTicker() {
	go func() {
		for {
			interval := refreshVisible
			if !a.mw.Visible() {
				interval = refreshHidden
			}

			select {
			case <-a.stopCh:
				return
			case <-time.After(interval):
				if a.realExit.Load() {
					return
				}

				a.manager.RefreshStatuses()
				snapshot := a.manager.Snapshot()
				a.mw.Synchronize(func() {
					a.model.sync(snapshot)
				})
			}
		}
	}()
}

func (a *App) styleCell(style *walk.CellStyle) {
	if style.Col() != 2 {
		return
	}
	s := a.model.at(style.Row())
	if s == nil {
		return
	}

	switch s.Status {
	case "运行中":
		style.TextColor = walk.RGB(0, 140, 0)
	case "已停止":
		style.TextColor = walk.RGB(110, 110, 110)
	case "启动中", "停止中", "暂停中", "恢复中":
		style.TextColor = walk.RGB(200, 120, 0)
	default:
		style.TextColor = walk.RGB(190, 30, 30)
	}
}

func (a *App) selected() *core.Service {
	if a.tv == nil {
		return nil
	}
	idx := a.tv.CurrentIndex()
	if idx < 0 {
		if sel := a.tv.SelectedIndexes(); len(sel) > 0 {
			idx = sel[0]
		}
	}
	return a.model.at(idx)
}

func (a *App) requireSelection() *core.Service {
	s := a.selected()
	if s == nil {
		walk.MsgBox(a.mw, "提示", "请先在列表中选择一个服务。", walk.MsgBoxIconInformation)
	}
	return s
}

func (a *App) reloadModel() {
	a.model.sync(a.manager.Snapshot())
}

func (a *App) updateStatus(message string) {
	if a.status != nil {
		_ = a.status.SetText(time.Now().Format("15:04:05") + " - " + message)
	}
}

func (a *App) onAddService() {
	name, err := showServiceEditor(a.mw, a.manager, nil)
	if err != nil {
		walk.MsgBox(a.mw, "错误", "创建服务失败: "+err.Error(), walk.MsgBoxIconError)
		a.updateStatus("创建服务失败: " + err.Error())
		return
	}
	if name == "" {
		return
	}

	a.reloadModel()
	a.updateStatus(fmt.Sprintf("服务 %s 已创建。", name))
}

func (a *App) onEdit() {
	s := a.requireSelection()
	if s == nil {
		return
	}

	wasRunning := s.Status == "运行中"

	name, err := showServiceEditor(a.mw, a.manager, s)
	if err != nil {
		walk.MsgBox(a.mw, "错误", "保存服务失败: "+err.Error(), walk.MsgBoxIconError)
		a.updateStatus("保存服务失败: " + err.Error())
		return
	}
	if name == "" {
		return
	}

	a.reloadModel()
	a.updateStatus(fmt.Sprintf("服务 %s 已更新。", name))

	if wasRunning {
		if walk.MsgBox(a.mw, "重启服务",
			"修改已保存。服务正在运行，是否立即重启以应用修改？",
			walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
			return
		}

		if err := a.manager.Stop(s.ID); err == nil {
			_ = a.manager.Start(s.ID)
		}
		a.reloadModel()
		a.updateStatus(fmt.Sprintf("服务 %s 已重启。", name))
	}
}

func (a *App) onRefresh() {
	a.updateStatus("正在刷新服务列表...")
	if err := a.manager.Load(); err != nil {
		walk.MsgBox(a.mw, "错误", "刷新失败: "+err.Error(), walk.MsgBoxIconError)
		a.updateStatus("刷新失败: " + err.Error())
		return
	}
	a.reloadModel()
	a.updateStatus(fmt.Sprintf("已加载 %d 个服务。", a.model.RowCount()))
}

func (a *App) onStart() {
	s := a.requireSelection()
	if s == nil {
		return
	}

	a.updateStatus("正在启动服务 " + s.ID + " ...")
	if err := a.manager.Start(s.ID); err != nil {
		walk.MsgBox(a.mw, "错误", "启动服务失败: "+err.Error(), walk.MsgBoxIconError)
		a.updateStatus("启动失败: " + err.Error())
		return
	}
	a.reloadModel()
	a.updateStatus("服务 " + s.ID + " 已启动。")
}

func (a *App) onStop() {
	s := a.requireSelection()
	if s == nil {
		return
	}

	a.updateStatus("正在停止服务 " + s.ID + " ...")
	if err := a.manager.Stop(s.ID); err != nil {
		walk.MsgBox(a.mw, "错误", "停止服务失败: "+err.Error(), walk.MsgBoxIconError)
		a.updateStatus("停止失败: " + err.Error())
		return
	}
	a.reloadModel()
	a.updateStatus("服务 " + s.ID + " 已停止。")
}

func (a *App) onDelete() {
	s := a.requireSelection()
	if s == nil {
		return
	}

	if walk.MsgBox(a.mw, "删除服务",
		fmt.Sprintf("确定要删除服务 '%s' 吗？此操作无法撤销。", s.ID),
		walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
		return
	}

	a.updateStatus("正在删除服务 " + s.ID + " ...")
	if err := a.manager.Delete(s.ID); err != nil {
		walk.MsgBox(a.mw, "错误", "删除服务失败: "+err.Error(), walk.MsgBoxIconError)
		a.updateStatus("删除失败: " + err.Error())
		return
	}
	a.reloadModel()
	a.updateStatus("服务 " + s.ID + " 已删除。")
}

func (a *App) onLogs() {
	s := a.requireSelection()
	if s == nil {
		return
	}
	showLogWindow(a.mw, s.ID, s.Name)
}

func (a *App) onEnvVars() {
	added, err := showEnvVars(a.mw)
	if err != nil {
		walk.MsgBox(a.mw, "错误", "更新环境变量失败: "+err.Error(), walk.MsgBoxIconError)
		return
	}
	if added {
		a.updateStatus("环境变量已更新。")
	}
}

func (a *App) onSettings() {
	changed, days, err := showSettings(a.mw)
	if err != nil {
		walk.MsgBox(a.mw, "错误", "保存设置失败: "+err.Error(), walk.MsgBoxIconError)
		return
	}
	if changed {
		a.updateStatus(fmt.Sprintf("设置已保存。已清理早于 %d 天的日志。", days))
	}
}
