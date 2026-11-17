package ui

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lxn/walk"
	"github.com/lxn/walk/declarative"
	"github.com/lxn/win"

	"svcctl/internal/core"
)

const maxLogLines = 3000

// showLogWindow 打开非模态日志窗口，每 2 秒增量读取一次。
func showLogWindow(owner *walk.MainWindow, serviceID, displayName string) {
	var (
		dlg        *walk.Dialog
		te         *walk.TextEdit
		refreshBtn *walk.PushButton
	)

	var (
		lines       []string
		offset      int64
		currentPath string
	)

	appendText := func(text string) {
		if te == nil || text == "" {
			return
		}
		te.AppendText(text)
		te.SetTextSelection(te.TextLength(), te.TextLength())
		te.SendMessage(win.EM_SCROLLCARET, 0, 0)
	}

	load := func(force bool) {
		if te == nil {
			return
		}

		path := core.LatestLogPath(serviceID)
		if path != currentPath || force {
			currentPath = path
			offset = 0
			lines = nil
			_ = te.SetText("")
		}

		if currentPath == "" {
			if len(lines) == 0 {
				text := "等待日志生成... (目录: " + core.LogDir() + ")"
				lines = append(lines, text)
				_ = te.SetText(text)
				te.SetTextSelection(te.TextLength(), te.TextLength())
			}
			return
		}

		newLines, newOffset, reset := readNewLines(currentPath, offset)
		if reset {
			lines = nil
			_ = te.SetText("")
		}
		offset = newOffset
		if len(newLines) == 0 {
			return
		}

		lines = append(lines, newLines...)
		if len(lines) > maxLogLines {
			lines = lines[len(lines)-maxLogLines:]
			_ = te.SetText(strings.Join(lines, "\r\n") + "\r\n")
			te.SetTextSelection(te.TextLength(), te.TextLength())
			te.SendMessage(win.EM_SCROLLCARET, 0, 0)
			return
		}
		appendText(strings.Join(newLines, "\r\n") + "\r\n")
	}

	err := declarative.Dialog{
		AssignTo: &dlg,
		Title:    "日志 - " + displayName,
		Size:     declarative.Size{Width: 1000, Height: 680},
		MinSize:  declarative.Size{Width: 900, Height: 600},
		Layout:   declarative.VBox{},
		Children: []declarative.Widget{
			declarative.TextEdit{
				AssignTo: &te,
				ReadOnly: true,
				VScroll:  true,
				HScroll:  true,
				MinSize:  declarative.Size{Width: 700, Height: 460},
				Font:     declarative.Font{Family: "Consolas", PointSize: 9},
			},
			declarative.Composite{
				Layout: declarative.HBox{MarginsZero: true},
				Children: []declarative.Widget{
					declarative.HSpacer{},
					declarative.PushButton{
						AssignTo:  &refreshBtn,
						Text:      "刷新",
						MinSize:   declarative.Size{Width: 80, Height: 30},
						OnClicked: func() { load(true) },
					},
					declarative.PushButton{
						Text:    "打开日志目录",
						MinSize: declarative.Size{Width: 120, Height: 30},
						OnClicked: func() {
							_ = core.OpenFolder(core.LogDir())
						},
					},
					declarative.PushButton{
						Text:      "关闭",
						MinSize:   declarative.Size{Width: 80, Height: 30},
						OnClicked: func() { dlg.Cancel() },
					},
				},
			},
		},
	}.Create(owner)
	if err != nil {
		walk.MsgBox(owner, "错误", err.Error(), walk.MsgBoxIconError)
		return
	}

	// 日志窗口允许最大化。
	enableMaximize(dlg)

	load(true)
	dlg.Show()

	// 避免日志框获得焦点时 Windows 默认全选文本。
	if refreshBtn != nil {
		_ = refreshBtn.SetFocus()
	}

	stopCh := make(chan struct{})
	var once sync.Once
	dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		once.Do(func() { close(stopCh) })
	})

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				dlg.Synchronize(func() { load(false) })
			}
		}
	}()
}

// enableMaximize 给窗口添加最大化按钮并刷新非客户区。
// walk 的 Dialog 默认只有 WS_THICKFRAME，没有 WS_MAXIMIZEBOX。
func enableMaximize(form walk.Form) {
	hwnd := form.Handle()
	if hwnd == 0 {
		return
	}

	style := uint32(win.GetWindowLong(hwnd, win.GWL_STYLE))
	style |= win.WS_MAXIMIZEBOX
	win.SetWindowLong(hwnd, win.GWL_STYLE, int32(style))
	win.SetWindowPos(hwnd, 0, 0, 0, 0, 0,
		win.SWP_NOSIZE|win.SWP_NOMOVE|win.SWP_NOZORDER|win.SWP_FRAMECHANGED)
}

// readNewLines 从 offset 起读取新增的完整行，未以换行结尾的部分留到下次。
func readNewLines(path string, offset int64) (lines []string, newOffset int64, reset bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset, false
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, offset, false
	}

	if st.Size() < offset {
		offset = 0
		reset = true
	}
	if st.Size() == offset {
		return nil, offset, reset
	}

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, reset
	}

	data := make([]byte, st.Size()-offset)
	n, _ := io.ReadFull(f, data)
	data = data[:n]

	idx := bytes.LastIndexByte(data, '\n')
	if idx < 0 {
		return nil, offset, reset
	}

	consumed := data[:idx+1]
	text := strings.ReplaceAll(string(consumed), "\r\n", "\n")
	parts := strings.Split(text, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}

	return parts, offset + int64(len(consumed)), reset
}
