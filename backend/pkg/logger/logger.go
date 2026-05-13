package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"os"
	"sync"
	"time"
)

var Log *zap.Logger

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

func InitLogger() {
	config := zap.NewProductionEncoderConfig()
	config.EncodeTime = zapcore.ISO8601TimeEncoder
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(config),
		zapcore.AddSync(os.Stdout),
		zapcore.DebugLevel,
	)
	Log = zap.New(core)
}

func Info(msg string, fields ...zap.Field) {
	PushLog("info", msg)
	Log.Info(msg, fields...)
}

func Error(msg string, fields ...zap.Field) {
	PushLog("error", msg)
	Log.Error(msg, fields...)
}

func Debug(msg string, fields ...zap.Field) {
	PushLog("debug", msg)
	Log.Debug(msg, fields...)
}
