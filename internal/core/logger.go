package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxLogFileSize = 50 * 1024 * 1024 // 50 MB

// AsyncLogger 是异步日志写入器，支持按大小轮转，行为对齐原 C# 实现。
type AsyncLogger struct {
	path     string
	ch       chan string
	done     chan struct{}
	finished chan struct{}
	once     sync.Once

	file       *os.File
	size       int64
	hasRotated bool
}

// NewAsyncLogger 创建日志写入器并启动后台协程。
func NewAsyncLogger(path string) *AsyncLogger {
	l := &AsyncLogger{
		path:     path,
		ch:       make(chan string, 4096),
		done:     make(chan struct{}),
		finished: make(chan struct{}),
	}
	go l.run()
	return l
}

// Log 追加一行日志；关闭后调用会被安全忽略。
func (l *AsyncLogger) Log(msg string) {
	select {
	case l.ch <- msg:
	case <-l.done:
	}
}

// Close 停止接收日志并等待剩余日志落盘。
func (l *AsyncLogger) Close() {
	l.once.Do(func() { close(l.done) })
	<-l.finished
}

func (l *AsyncLogger) run() {
	defer close(l.finished)
	defer func() {
		if l.file != nil {
			l.file.Close()
			l.file = nil
		}
	}()

	for {
		select {
		case msg := <-l.ch:
			l.write(msg)
		case <-l.done:
			for {
				select {
				case msg := <-l.ch:
					l.write(msg)
				default:
					return
				}
			}
		}
	}
}

func (l *AsyncLogger) write(line string) {
	if l.file == nil || l.size > maxLogFileSize {
		l.rotate()
	}
	if l.file == nil {
		return
	}
	n, err := l.file.WriteString(line + "\r\n")
	if err == nil {
		l.size += int64(n)
	}
}

func (l *AsyncLogger) rotate() {
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}

	path := l.path
	if l.hasRotated {
		dir := filepath.Dir(l.path)
		base := filepath.Base(l.path)
		ext := filepath.Ext(base)
		name := strings.TrimSuffix(base, ext)
		path = filepath.Join(dir, fmt.Sprintf("%s_%s%s", name, time.Now().Format("150405"), ext))
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	l.file = f
	l.size = 0
	if st, err := f.Stat(); err == nil {
		l.size = st.Size()
	}
	l.hasRotated = true
}
