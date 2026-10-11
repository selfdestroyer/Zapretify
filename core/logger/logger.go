package logger

import (
	"fmt"
	"os"
	"sync"
	"time"
)

const maxLogLines = 500

type Logger struct {
	mu       sync.RWMutex
	lines    []string
	filePath string
	file     *os.File
}

var defaultLogger = &Logger{
	lines: make([]string, 0, maxLogLines),
}

func Init(filePath string) {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()

	defaultLogger.filePath = filePath
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		defaultLogger.file = f
	}
}

func Log(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	line := fmt.Sprintf("[%s] %s", timestamp, msg)

	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()

	if len(defaultLogger.lines) >= maxLogLines {
		defaultLogger.lines = defaultLogger.lines[1:]
	}
	defaultLogger.lines = append(defaultLogger.lines, line)

	if defaultLogger.file != nil {
		_, _ = defaultLogger.file.WriteString(line + "\n")
	}
}

func GetLines() []string {
	defaultLogger.mu.RLock()
	defer defaultLogger.mu.RUnlock()

	res := make([]string, len(defaultLogger.lines))
	copy(res, defaultLogger.lines)
	return res
}

func Clear() {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()

	defaultLogger.lines = make([]string, 0, maxLogLines)
	if defaultLogger.file != nil {
		_ = defaultLogger.file.Truncate(0)
	}
}
