package logger

import (
	"os"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Log = zap.NewNop()

type LogEntry struct {
	Level   string `json:"level"`
	Message string `json:"message"`
	Time    string `json:"time"`
}

var (
	recentLogs []LogEntry
	logMutex   sync.Mutex
)

func PushLog(level, msg string) {
	logMutex.Lock()
	defer logMutex.Unlock()
	recentLogs = append([]LogEntry{{
		Level:   level,
		Message: msg,
		Time:    time.Now().Format("15:04:05"),
	}}, recentLogs...)
	if len(recentLogs) > 100 {
		recentLogs = recentLogs[:100]
	}
}

func GetRecentLogs() []LogEntry {
	logMutex.Lock()
	defer logMutex.Unlock()

	// Return a copy so we don't return the slice that can be mutated
	cp := make([]LogEntry, len(recentLogs))
	copy(cp, recentLogs)
	return cp
}

// InitLogger configures the global JSON logger. level is one of debug, info, warn, error.
func InitLogger(level ...string) {
	lvl := zapcore.InfoLevel
	if len(level) > 0 && level[0] != "" {
		if err := lvl.UnmarshalText([]byte(level[0])); err != nil {
			lvl = zapcore.InfoLevel
		}
	}
	config := zap.NewProductionEncoderConfig()
	config.EncodeTime = zapcore.ISO8601TimeEncoder
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(config),
		zapcore.AddSync(os.Stdout),
		lvl,
	)
	Log = zap.New(core)
}

func Info(msg string, fields ...zap.Field) {
	PushLog("info", msg)
	Log.Info(msg, fields...)
}

func Warn(msg string, fields ...zap.Field) {
	PushLog("warn", msg)
	Log.Warn(msg, fields...)
}

func Error(msg string, fields ...zap.Field) {
	PushLog("error", msg)
	Log.Error(msg, fields...)
}

func Debug(msg string, fields ...zap.Field) {
	Log.Debug(msg, fields...)
}
