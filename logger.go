// Package xyliumgorm provides a Xylium framework connector for GORM,
// enabling seamless integration with contextual logging, Go context propagation,
// and lifecycle management.
package xyliumgorm

import (
	"context"
	"errors"
	"time"

	"github.com/arwahdevops/xylium-core/src/xylium" // PASTIKAN PATH INI BENAR
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/utils"
)

// xyliumContextKeyType is a private key type used to store *xylium.Context
// within a standard context.Context. This prevents key collisions.
type xyliumContextKeyType struct{}

// xyliumContextKey is the actual key instance used with context.WithValue and ctx.Value.
var xyliumContextKey = xyliumContextKeyType{}

// xyliumGormLogger adapts a xylium.Logger to GORM's gormlogger.Interface.
type xyliumGormLogger struct {
	appLogger                 xylium.Logger
	gormLogLevel              gormlogger.LogLevel
	slowThreshold             time.Duration // Nama field yang benar (huruf kecil di awal)
	ignoreRecordNotFoundError bool
}

// XyliumGormLoggerConfig holds configuration for creating a xyliumGormLogger.
type XyliumGormLoggerConfig struct {
	AppLogger                 xylium.Logger
	GormLogLevel              gormlogger.LogLevel
	SlowThreshold             time.Duration
	IgnoreRecordNotFoundError bool
}

// NewXyliumGormLogger creates a new GORM logger instance that integrates with a Xylium logger.
func NewXyliumGormLogger(config XyliumGormLoggerConfig) gormlogger.Interface {
	if config.AppLogger == nil {
		fallbackLogger := xylium.NewDefaultLogger()
		fallbackLogger.SetLevel(xylium.LevelWarn)
		fallbackLogger.Warnf("xylium-gorm (logger.go): AppLogger not provided to GORM logger adapter, using a new default Xylium logger as fallback.")
		config.AppLogger = fallbackLogger
	}

	if config.GormLogLevel == 0 {
		config.GormLogLevel = gormlogger.Warn
		config.AppLogger.Debugf("xylium-gorm (logger.go): GormLogLevel not specified, defaulting to WARN for GORM's internal logging.")
	}

	if config.SlowThreshold <= 0 {
		config.SlowThreshold = 200 * time.Millisecond
		config.AppLogger.Debugf("xylium-gorm (logger.go): SlowQueryThreshold not specified or invalid, defaulting to %v for GORM's slow query logging.", config.SlowThreshold)
	}

	return &xyliumGormLogger{
		appLogger:                 config.AppLogger,
		gormLogLevel:              config.GormLogLevel,
		slowThreshold:             config.SlowThreshold, // Menggunakan nilai dari config
		ignoreRecordNotFoundError: config.IgnoreRecordNotFoundError,
	}
}

// LogMode sets the GORM log level for this logger instance.
func (l *xyliumGormLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	newLogger := *l
	newLogger.gormLogLevel = level
	// Menggunakan l.appLogger karena ini adalah tindakan pada instance logger itu sendiri,
	// bukan log dari query GORM yang spesifik untuk request.
	l.appLogger.Debugf("xylium-gorm (logger.go): GORM logger mode dynamically changed to %v.", level)
	return &newLogger
}

// getLoggerFromContext retrieves the most appropriate xylium.Logger.
func (l *xyliumGormLogger) getLoggerFromContext(ctx context.Context) xylium.Logger {
	if xc, ok := ctx.Value(xyliumContextKey).(*xylium.Context); ok && xc != nil {
		return xc.Logger().WithFields(xylium.M{"gorm_log_scope": "request_contextual"})
	}
	return l.appLogger.WithFields(xylium.M{"gorm_log_scope": "application_base"})
}

// Info logs informational messages from GORM.
func (l *xyliumGormLogger) Info(ctx context.Context, msg string, data ...interface{}) {
	if l.gormLogLevel >= gormlogger.Info {
		// PERBAIKAN: Gunakan variabel logger yang benar yang diambil dari konteks.
		// Tidak perlu mendeklarasikan ulang 'logger' di sini jika sudah diambil di baris atas.
		// (Perbaikan sebelumnya sudah benar dengan memanggil l.getLoggerFromContext(ctx) di setiap method log)
		currentLogger := l.getLoggerFromContext(ctx) // Mengambil logger yang tepat
		currentLogger.Infof(msg, data...)
	}
}

// Warn logs warning messages from GORM.
func (l *xyliumGormLogger) Warn(ctx context.Context, msg string, data ...interface{}) {
	if l.gormLogLevel >= gormlogger.Warn {
		currentLogger := l.getLoggerFromContext(ctx) // Mengambil logger yang tepat
		currentLogger.Warnf(msg, data...)
	}
}

// Error logs error messages from GORM.
func (l *xyliumGormLogger) Error(ctx context.Context, msg string, data ...interface{}) {
	if l.gormLogLevel >= gormlogger.Error {
		currentLogger := l.getLoggerFromContext(ctx) // Mengambil logger yang tepat
		currentLogger.Errorf(msg, data...)
	}
}

// Trace logs SQL query execution details.
func (l *xyliumGormLogger) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	if l.gormLogLevel <= gormlogger.Silent {
		return
	}

	currentLogger := l.getLoggerFromContext(ctx) // Mengambil logger yang tepat
	elapsed := time.Since(begin)
	sqlQuery, rows := fc()
	gormCaller := utils.FileWithLineNum()

	logFields := xylium.M{
		"gorm_latency_ms": float64(elapsed.Nanoseconds()) / 1e6,
		"gorm_rows":       rows,
		"gorm_sql":        sqlQuery,
		"gorm_sql_caller": gormCaller,
	}

	switch {
	case err != nil && l.gormLogLevel >= gormlogger.Error && (!errors.Is(err, gormlogger.ErrRecordNotFound) || !l.ignoreRecordNotFoundError):
		logFields["gorm_error"] = err.Error()
		currentLogger.WithFields(logFields).Errorf("GORM Query Error")
	// PERBAIKAN: Gunakan l.slowThreshold (huruf kecil 's')
	case l.slowThreshold > 0 && elapsed > l.slowThreshold && l.gormLogLevel >= gormlogger.Warn:
		currentLogger.WithFields(logFields).Warnf("GORM Slow Query (Threshold: %v)", l.slowThreshold) // PERBAIKAN: Gunakan l.slowThreshold
	case l.gormLogLevel >= gormlogger.Info:
		currentLogger.WithFields(logFields).Debugf("GORM Query Executed")
	}
}

// contextWithXylium helper function
func contextWithXylium(parent context.Context, xc *xylium.Context) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithValue(parent, xyliumContextKey, xc)
}
