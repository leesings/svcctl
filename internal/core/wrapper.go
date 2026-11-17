package core

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

const (
	baseBackoff    = 5 * time.Second
	maxBackoff     = 5 * time.Minute
	restartResetAt = 10 * time.Minute
)

// RunService 以 Windows 服务方式运行包装器（由 SCM 调用）。
func RunService(name string) error {
	return svc.Run(name, &serviceHandler{name: name})
}

type serviceHandler struct {
	name string
	cfg  *Service

	mu       sync.Mutex
	logger   *AsyncLogger
	proc     *runningProcess
	stopping bool
}

type runningProcess struct {
	cmd    *exec.Cmd
	exited chan int
}

func (h *serviceHandler) Execute(_ []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}

	if err := h.init(); err != nil {
		h.writeCrashLog(err)
		changes <- svc.Status{State: svc.Stopped}
		return false, 1064
	}
	defer h.shutdown()

	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	var (
		proc         *runningProcess
		exitCh       <-chan int
		restartTimer *time.Timer
		restartCh    <-chan time.Time
		restartCount int
		lastRestart  time.Time
	)
	defer func() {
		if restartTimer != nil {
			restartTimer.Stop()
		}
	}()

	spawn := func() bool {
		p, err := h.spawn()
		if err != nil {
			h.logf("启动目标进程失败: %v", err)
			return false
		}
		proc = p
		exitCh = p.exited
		h.proc = p
		h.logf("目标进程已启动，PID=%d", p.cmd.Process.Pid)
		return true
	}

	if !spawn() {
		if !h.cfg.AutoRestart {
			h.writeCrashLog(fmt.Errorf("目标进程启动失败，且未启用自动重启"))
			return false, 1064
		}
		restartCount = 1
		lastRestart = time.Now()
		restartTimer = time.NewTimer(restartDelay(restartCount))
		restartCh = restartTimer.C
	}

	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus

			case svc.Stop, svc.Shutdown:
				h.setStopping()
				changes <- svc.Status{State: svc.StopPending}
				if restartTimer != nil {
					restartTimer.Stop()
				}
				if proc != nil {
					proc.kill(5 * time.Second)
				}
				h.logf("服务已停止")
				return false, 0
			}

		case code := <-exitCh:
			exitCh = nil
			proc = nil
			h.setProc(nil)
			h.logf("目标进程已退出（code=%d）", code)

			if h.isStopping() {
				return false, 0
			}
			if !h.cfg.AutoRestart {
				h.logf("未启用自动重启，停止服务")
				return false, 0
			}
			if code == 0 {
				h.logf("进程正常退出，不再重启")
				return false, 0
			}

			if time.Since(lastRestart) > restartResetAt {
				restartCount = 0
			}
			restartCount++

			lastRestart = time.Now()
			delay := restartDelay(restartCount)
			h.logf("第 %d 次重启，%s 后执行", restartCount, delay)
			restartTimer = time.NewTimer(delay)
			restartCh = restartTimer.C

		case <-restartCh:
			restartCh = nil
			restartTimer = nil
			if h.isStopping() {
				return false, 0
			}
			if spawn() {
				continue
			}
			if !h.cfg.AutoRestart {
				return false, 1064
			}
			restartCount++
			lastRestart = time.Now()
			delay := restartDelay(restartCount)
			h.logf("进程启动失败，第 %d 次重试，%s 后执行", restartCount, delay)
			restartTimer = time.NewTimer(delay)
			restartCh = restartTimer.C
		}
	}
}

// restartDelay 采用指数退避，上限 5 分钟，重启次数不设上限。
func restartDelay(attempt int) time.Duration {
	delay := baseBackoff
	if attempt > 1 {
		shift := attempt - 1
		if shift > 6 {
			shift = 6
		}
		delay <<= shift
	}
	if delay > maxBackoff {
		delay = maxBackoff
	}
	return delay
}

func (h *serviceHandler) init() error {
	cfg, err := loadServiceParams(h.name)
	if err != nil {
		return fmt.Errorf("读取服务配置失败: %w", err)
	}
	h.cfg = cfg

	h.mu.Lock()
	h.logger = NewAsyncLogger(NewServiceLogPath(h.name))
	h.mu.Unlock()

	h.logf("服务启动，目标进程: %s %s", cfg.ExePath, cfg.Args)
	return nil
}

func (h *serviceHandler) shutdown() {
	h.mu.Lock()
	p := h.proc
	h.proc = nil
	l := h.logger
	h.logger = nil
	h.mu.Unlock()

	if p != nil {
		p.kill(5 * time.Second)
	}
	if l != nil {
		l.Close()
	}
}

func (h *serviceHandler) spawn() (*runningProcess, error) {
	cfg := h.cfg

	dir := cfg.WorkingDir
	if dir == "" {
		dir = filepath.Dir(cfg.ExePath)
	}

	cmd := exec.Command(cfg.ExePath, splitArgs(cfg.Args)...)
	cmd.Dir = dir
	if len(cfg.EnvVars) > 0 {
		cmd.Env = append(os.Environ(), cfg.EnvVars...)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}

	outWriter := &lineWriter{prefix: "", emit: h.logf}
	errWriter := &lineWriter{prefix: "ERROR: ", emit: h.logf}
	cmd.Stdout = outWriter
	cmd.Stderr = errWriter

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	rp := &runningProcess{cmd: cmd, exited: make(chan int, 1)}
	go func() {
		err := cmd.Wait()
		outWriter.flush()
		errWriter.flush()

		code := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else if err != nil {
			code = -1
		}
		rp.exited <- code
		close(rp.exited)
	}()

	return rp, nil
}

func (rp *runningProcess) kill(wait time.Duration) {
	if rp == nil || rp.cmd == nil || rp.cmd.Process == nil {
		return
	}

	kill := exec.Command("taskkill", "/PID", strconv.Itoa(rp.cmd.Process.Pid), "/T", "/F")
	kill.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	_ = kill.Run()

	select {
	case <-rp.exited:
	case <-time.After(wait):
		_ = rp.cmd.Process.Kill()
	}
}

func (h *serviceHandler) logf(format string, args ...interface{}) {
	h.mu.Lock()
	l := h.logger
	h.mu.Unlock()
	if l != nil {
		l.Log(fmt.Sprintf(format, args...))
	}
}

func (h *serviceHandler) setProc(p *runningProcess) {
	h.mu.Lock()
	h.proc = p
	h.mu.Unlock()
}

func (h *serviceHandler) setStopping() {
	h.mu.Lock()
	h.stopping = true
	h.mu.Unlock()
}

func (h *serviceHandler) isStopping() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stopping
}

func (h *serviceHandler) writeCrashLog(err error) {
	_ = os.MkdirAll(LogDir(), 0o755)
	path := filepath.Join(LogDir(), fmt.Sprintf("%s_CRASH_%s.log", h.name, time.Now().Format("20060102_150405")))
	_ = os.WriteFile(path, []byte("Service Startup Failed:\r\n"+err.Error()+"\r\n"), 0o644)
	h.logf("严重错误: %v", err)
}

// lineWriter 按行切分进程输出，交给日志器写入。
type lineWriter struct {
	mu     sync.Mutex
	buf    []byte
	prefix string
	emit   func(string, ...interface{})
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := string(bytes.TrimRight(w.buf[:i], "\r"))
		w.emit("%s%s", w.prefix, line)
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) > 64*1024 {
		w.emit("%s%s", w.prefix, string(w.buf))
		w.buf = w.buf[:0]
	}
	return len(p), nil
}

func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.emit("%s%s", w.prefix, string(bytes.TrimRight(w.buf, "\r\n")))
		w.buf = w.buf[:0]
	}
}

// splitArgs 按 Windows 命令行规则拆分参数，支持双引号与反斜杠转义。
func splitArgs(s string) []string {
	var args []string
	var cur strings.Builder
	inQuote := false
	hasToken := false

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\':
			j := i
			for j < len(s) && s[j] == '\\' {
				j++
			}
			n := j - i
			if j < len(s) && s[j] == '"' {
				cur.WriteString(strings.Repeat(`\`, n/2))
				if n%2 == 1 {
					cur.WriteByte('"')
					j++
				}
			} else {
				cur.WriteString(strings.Repeat(`\`, n))
			}
			hasToken = true
			i = j - 1
		case c == '"':
			inQuote = !inQuote
			hasToken = true
		case (c == ' ' || c == '\t') && !inQuote:
			if hasToken {
				args = append(args, cur.String())
				cur.Reset()
				hasToken = false
			}
		default:
			cur.WriteByte(c)
			hasToken = true
		}
	}
	if hasToken {
		args = append(args, cur.String())
	}
	return args
}
