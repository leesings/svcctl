package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lxn/walk"
	"github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"

	"svcctl/internal/core"
)

// showServiceEditor 弹出添加/编辑服务对话框。
// existing 为 nil 表示新建；返回空字符串表示用户取消。
func showServiceEditor(owner *walk.MainWindow, mgr *core.ServiceManager, existing *core.Service) (string, error) {
	var (
		dlg        *walk.Dialog
		nameEdit   *walk.LineEdit
		exeEdit    *walk.LineEdit
		argsEdit   *walk.LineEdit
		dirEdit    *walk.LineEdit
		startCombo *walk.ComboBox
		autoCheck  *walk.CheckBox
		envSummary *walk.LineEdit
		saveBtn    *walk.PushButton
		cancelBtn  *walk.PushButton
		saved      string
	)

	var envVars []string
	if existing != nil {
		envVars = append(envVars, existing.EnvVars...)
	}

	title := "添加新服务"
	submitText := "创建"
	if existing != nil {
		title = "编辑服务 - " + existing.Name
		submitText = "保存"
	}

	browseExe := func() {
		fd := walk.FileDialog{
			Title:  "选择可执行文件",
			Filter: "可执行文件 (*.exe;*.bat;*.cmd)|*.exe;*.bat;*.cmd|所有文件 (*.*)|*.*",
		}
		ok, err := fd.ShowOpen(dlg)
		if err != nil {
			walk.MsgBox(dlg, "错误", err.Error(), walk.MsgBoxIconError)
			return
		}
		if !ok {
			return
		}
		_ = exeEdit.SetText(fd.FilePath)
		if strings.TrimSpace(dirEdit.Text()) == "" {
			_ = dirEdit.SetText(filepath.Dir(fd.FilePath))
		}
	}

	browseDir := func() {
		fd := walk.FileDialog{Title: "选择工作目录"}
		ok, err := fd.ShowBrowseFolder(dlg)
		if err != nil {
			walk.MsgBox(dlg, "错误", err.Error(), walk.MsgBoxIconError)
			return
		}
		if ok {
			_ = dirEdit.SetText(fd.FilePath)
		}
	}

	envSummaryText := func() string {
		if len(envVars) == 0 {
			return "未配置"
		}
		preview := strings.Join(envVars, ", ")
		if len(preview) > 60 {
			preview = preview[:60] + "..."
		}
		return fmt.Sprintf("%d 项: %s", len(envVars), preview)
	}

	openEnv := func() {
		updated, ok := showEnvEditor(dlg, envVars)
		if !ok {
			return
		}
		envVars = updated
		_ = envSummary.SetText(envSummaryText())
	}

	doSubmit := func() {
		name := strings.TrimSpace(nameEdit.Text())
		exe := strings.TrimSpace(exeEdit.Text())
		if name == "" || exe == "" {
			walk.MsgBox(dlg, "验证错误", "服务名称和可执行文件路径为必填项。", walk.MsgBoxIconWarning)
			return
		}

		var startType uint32 = windows.SERVICE_AUTO_START
		if startCombo.CurrentIndex() == 1 {
			startType = windows.SERVICE_DEMAND_START
		}

		cfg := core.ServiceConfig{
			Name:        name,
			ExePath:     exe,
			Args:        strings.TrimSpace(argsEdit.Text()),
			WorkingDir:  strings.TrimSpace(dirEdit.Text()),
			AutoRestart: autoCheck.Checked(),
			StartupType: startType,
			EnvVars:     envVars,
		}

		saveBtn.SetEnabled(false)
		var err error
		if existing == nil {
			err = mgr.Create(cfg)
		} else {
			err = mgr.Update(existing.ID, cfg)
		}
		saveBtn.SetEnabled(true)
		if err != nil {
			walk.MsgBox(dlg, "错误", "保存服务失败: "+err.Error(), walk.MsgBoxIconError)
			return
		}

		saved = name
		dlg.Accept()
	}

	autoRestartDefault := existing == nil || existing.AutoRestart
	browseSize := declarative.Size{Width: 80}

	err := declarative.Dialog{
		AssignTo:  &dlg,
		Title:     title,
		FixedSize: true,
		Size:      declarative.Size{Width: 660, Height: 500},
		MinSize:   declarative.Size{Width: 620, Height: 480},
		Layout:    declarative.VBox{},
		Children: []declarative.Widget{
			declarative.GroupBox{
				Title:  "基本信息",
				Layout: declarative.Grid{Columns: 2},
				Children: []declarative.Widget{
					declarative.Label{Text: "服务名称"},
					declarative.LineEdit{AssignTo: &nameEdit, MinSize: declarative.Size{Width: 400}},

					declarative.Label{Text: "启动类型"},
					declarative.ComboBox{AssignTo: &startCombo, Model: []string{"开机自启", "手动启动"}},

					declarative.Label{Text: "可执行文件"},
					declarative.Composite{
						Layout: declarative.HBox{MarginsZero: true},
						Children: []declarative.Widget{
							declarative.LineEdit{AssignTo: &exeEdit, MinSize: declarative.Size{Width: 310}},
							declarative.PushButton{Text: "浏览...", MinSize: browseSize, OnClicked: browseExe},
						},
					},

					declarative.Label{Text: "启动参数"},
					declarative.LineEdit{AssignTo: &argsEdit, MinSize: declarative.Size{Width: 400}},

					declarative.Label{Text: "工作目录"},
					declarative.Composite{
						Layout: declarative.HBox{MarginsZero: true},
						Children: []declarative.Widget{
							declarative.LineEdit{AssignTo: &dirEdit, MinSize: declarative.Size{Width: 310}},
							declarative.PushButton{Text: "浏览...", MinSize: browseSize, OnClicked: browseDir},
						},
					},

					declarative.Label{Text: "环境变量"},
					declarative.Composite{
						Layout: declarative.HBox{MarginsZero: true},
						Children: []declarative.Widget{
							declarative.LineEdit{AssignTo: &envSummary, ReadOnly: true, MinSize: declarative.Size{Width: 310}},
							declarative.PushButton{Text: "配置...", MinSize: browseSize, OnClicked: openEnv},
						},
					},
				},
			},
			declarative.CheckBox{
				AssignTo: &autoCheck,
				Checked:  autoRestartDefault,
				Text:     "失败自动重启（指数退避，不限次数）",
			},
			declarative.Composite{
				Layout: declarative.HBox{MarginsZero: true},
				Children: []declarative.Widget{
					declarative.HSpacer{},
					declarative.PushButton{
						AssignTo:  &cancelBtn,
						Text:      "取消",
						MinSize:   declarative.Size{Width: 80, Height: 30},
						OnClicked: func() { dlg.Cancel() },
					},
					declarative.PushButton{
						AssignTo:  &saveBtn,
						Text:      submitText,
						MinSize:   declarative.Size{Width: 80, Height: 30},
						OnClicked: doSubmit,
					},
				},
			},
		},
	}.Create(owner)
	if err != nil {
		return "", err
	}

	if existing != nil {
		_ = nameEdit.SetText(existing.Name)
		_ = exeEdit.SetText(existing.ExePath)
		_ = argsEdit.SetText(existing.Args)
		_ = dirEdit.SetText(existing.WorkingDir)
	}

	_ = envSummary.SetText(envSummaryText())
	autoCheck.SetChecked(autoRestartDefault)
	if existing != nil && existing.StartupType == windows.SERVICE_DEMAND_START {
		_ = startCombo.SetCurrentIndex(1)
	} else {
		_ = startCombo.SetCurrentIndex(0)
	}

	_ = dlg.SetDefaultButton(saveBtn)
	_ = dlg.SetCancelButton(cancelBtn)

	if dlg.Run() != walk.DlgCmdOK {
		return "", nil
	}
	return saved, nil
}

// showSettings 弹出设置对话框，返回是否保存成功及保留天数。
func showSettings(owner *walk.MainWindow) (bool, int, error) {
	var (
		dlg       *walk.Dialog
		daysEdit  *walk.NumberEdit
		saveBtn   *walk.PushButton
		cancelBtn *walk.PushButton
		days      = core.GetRetentionDays()
		changed   bool
	)

	doSave := func() {
		d := int(daysEdit.Value())
		if d < 1 {
			d = 1
		}
		if err := core.SetRetentionDays(d); err != nil {
			walk.MsgBox(dlg, "错误", "保存设置失败: "+err.Error(), walk.MsgBoxIconError)
			return
		}
		core.CleanupOldLogs(d)
		days = d
		changed = true
		dlg.Accept()
	}

	err := declarative.Dialog{
		AssignTo:  &dlg,
		Title:     "应用设置",
		FixedSize: true,
		Size:      declarative.Size{Width: 520, Height: 300},
		MinSize:   declarative.Size{Width: 500, Height: 290},
		Layout:    declarative.VBox{},
		Children: []declarative.Widget{
			declarative.Label{Text: "Windows 后台服务管理工具"},
			declarative.LinkLabel{
				Text: `GitHub: <a id="github" href="https://github.com/leesings/svcctl">https://github.com/leesings/svcctl</a>`,
				OnLinkActivated: func(link *walk.LinkLabelLink) {
					_ = core.OpenURL(link.URL())
				},
			},

			declarative.GroupBox{
				Title:  "日志保留策略",
				Layout: declarative.Grid{Columns: 2},
				Children: []declarative.Widget{
					declarative.Label{Text: "保留天数"},
					declarative.NumberEdit{
						AssignTo:           &daysEdit,
						MinValue:           1,
						MaxValue:           365,
						SpinButtonsVisible: true,
						MinSize:            declarative.Size{Width: 140},
					},
				},
			},
			declarative.Label{Text: "修改后立即生效，过期日志会自动清理。"},

			declarative.Composite{
				Layout: declarative.HBox{MarginsZero: true},
				Children: []declarative.Widget{
					declarative.HSpacer{},
					declarative.PushButton{
						AssignTo:  &cancelBtn,
						Text:      "取消",
						MinSize:   declarative.Size{Width: 80, Height: 30},
						OnClicked: func() { dlg.Cancel() },
					},
					declarative.PushButton{
						AssignTo:  &saveBtn,
						Text:      "保存",
						MinSize:   declarative.Size{Width: 80, Height: 30},
						OnClicked: doSave,
					},
				},
			},
		},
	}.Create(owner)
	if err != nil {
		return false, days, err
	}

	_ = daysEdit.SetValue(float64(days))
	_ = dlg.SetDefaultButton(saveBtn)
	_ = dlg.SetCancelButton(cancelBtn)

	if dlg.Run() != walk.DlgCmdOK {
		return false, days, nil
	}
	return changed, days, nil
}

// showEnvVars 弹出“添加到 PATH”对话框。
func showEnvVars(owner *walk.MainWindow) (bool, error) {
	var (
		dlg       *walk.Dialog
		pathEdit  *walk.LineEdit
		addBtn    *walk.PushButton
		cancelBtn *walk.PushButton
		added     bool
	)

	browse := func() {
		fd := walk.FileDialog{Title: "选择目录"}
		ok, err := fd.ShowBrowseFolder(dlg)
		if err != nil {
			walk.MsgBox(dlg, "错误", err.Error(), walk.MsgBoxIconError)
			return
		}
		if ok {
			_ = pathEdit.SetText(fd.FilePath)
		}
	}

	doAdd := func() {
		dir := strings.TrimSpace(pathEdit.Text())
		if dir == "" {
			walk.MsgBox(dlg, "提示", "请填写要添加到 PATH 的目录。", walk.MsgBoxIconWarning)
			return
		}
		if err := core.AddToSystemPath(dir); err != nil {
			walk.MsgBox(dlg, "错误", err.Error(), walk.MsgBoxIconError)
			return
		}
		added = true
		dlg.Accept()
	}

	err := declarative.Dialog{
		AssignTo:  &dlg,
		Title:     "添加到 PATH 环境变量",
		FixedSize: true,
		Size:      declarative.Size{Width: 560, Height: 240},
		MinSize:   declarative.Size{Width: 540, Height: 230},
		Layout:    declarative.VBox{},
		Children: []declarative.Widget{
			declarative.Label{Text: "目录路径（将追加到系统 PATH，重复路径会自动忽略）"},
			declarative.Composite{
				Layout: declarative.HBox{MarginsZero: true},
				Children: []declarative.Widget{
					declarative.LineEdit{AssignTo: &pathEdit, MinSize: declarative.Size{Width: 360}},
					declarative.PushButton{
						Text:      "浏览...",
						MinSize:   declarative.Size{Width: 80},
						OnClicked: browse,
					},
				},
			},
			declarative.Composite{
				Layout: declarative.HBox{MarginsZero: true},
				Children: []declarative.Widget{
					declarative.HSpacer{},
					declarative.PushButton{
						AssignTo:  &cancelBtn,
						Text:      "取消",
						MinSize:   declarative.Size{Width: 80, Height: 30},
						OnClicked: func() { dlg.Cancel() },
					},
					declarative.PushButton{
						AssignTo:  &addBtn,
						Text:      "添加",
						MinSize:   declarative.Size{Width: 80, Height: 30},
						OnClicked: doAdd,
					},
				},
			},
		},
	}.Create(owner)
	if err != nil {
		return false, err
	}

	_ = dlg.SetDefaultButton(addBtn)
	_ = dlg.SetCancelButton(cancelBtn)

	if dlg.Run() != walk.DlgCmdOK {
		return false, nil
	}
	return added, nil
}

// showEnvEditor 以“一行一个 KEY=VALUE”的方式编辑服务环境变量。
func showEnvEditor(owner walk.Form, initial []string) ([]string, bool) {
	var (
		dlg       *walk.Dialog
		te        *walk.TextEdit
		okBtn     *walk.PushButton
		cancelBtn *walk.PushButton
		result    []string
		confirmed bool
	)

	doOK := func() {
		lines := strings.Split(strings.ReplaceAll(te.Text(), "\r\n", "\n"), "\n")
		normalized, err := core.NormalizeEnvVars(lines)
		if err != nil {
			walk.MsgBox(dlg, "格式错误", err.Error(), walk.MsgBoxIconWarning)
			return
		}
		result = normalized
		confirmed = true
		dlg.Accept()
	}

	err := declarative.Dialog{
		AssignTo:  &dlg,
		Title:     "环境变量",
		FixedSize: true,
		Size:      declarative.Size{Width: 640, Height: 470},
		MinSize:   declarative.Size{Width: 620, Height: 450},
		Layout:    declarative.VBox{},
		Children: []declarative.Widget{
			declarative.Label{Text: "每行一个环境变量，格式 KEY=VALUE；空行和 # 开头的行会被忽略。"},
			declarative.TextEdit{
				AssignTo: &te,
				VScroll:  true,
				HScroll:  true,
				MinSize:  declarative.Size{Width: 580, Height: 320},
				Font:     declarative.Font{Family: "Consolas", PointSize: 10},
			},
			declarative.Composite{
				Layout: declarative.HBox{MarginsZero: true},
				Children: []declarative.Widget{
					declarative.Label{Text: "示例: PATH=C:\\tools"},
					declarative.HSpacer{},
					declarative.PushButton{
						AssignTo:  &cancelBtn,
						Text:      "取消",
						MinSize:   declarative.Size{Width: 80, Height: 30},
						OnClicked: func() { dlg.Cancel() },
					},
					declarative.PushButton{
						AssignTo:  &okBtn,
						Text:      "确定",
						MinSize:   declarative.Size{Width: 80, Height: 30},
						OnClicked: doOK,
					},
				},
			},
		},
	}.Create(owner)
	if err != nil {
		return nil, false
	}

	_ = te.SetText(strings.Join(initial, "\r\n"))
	_ = dlg.SetDefaultButton(okBtn)
	_ = dlg.SetCancelButton(cancelBtn)

	// Windows 编辑框获得焦点时会默认全选，延迟把光标收起到末尾。
	var closed int32
	dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		atomic.StoreInt32(&closed, 1)
	})
	time.AfterFunc(150*time.Millisecond, func() {
		if atomic.LoadInt32(&closed) != 0 {
			return
		}
		dlg.Synchronize(func() {
			if atomic.LoadInt32(&closed) != 0 {
				return
			}
			te.SetTextSelection(te.TextLength(), te.TextLength())
			te.SendMessage(win.EM_SCROLLCARET, 0, 0)
		})
	})

	if dlg.Run() != walk.DlgCmdOK {
		return nil, false
	}
	return result, confirmed
}
