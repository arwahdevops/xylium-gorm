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
// It directs GORM's internal log messages through the Xylium logging system,
// allowing for consistent log formatting, contextualization (e.g., request IDs),
// and level management as defined by the Xylium application.
type xyliumGormLogger struct {
	appLogger                 xylium.Logger // The primary Xylium logger used for output.
	gormLogLevel              gormlogger.LogLevel
	slowThreshold             time.Duration
	ignoreRecordNotFoundError bool
}

// XyliumGormLoggerConfig holds configuration for creating a xyliumGormLogger.
type XyliumGormLoggerConfig struct {
	// AppLogger is the Xylium application logger that GORM logs will be piped through. This is mandatory.
	AppLogger xylium.Logger
	// GormLogLevel specifies the minimum GORM log level (e.g., Info, Warn, Error) to be processed.
	// If not set, defaults to gormlogger.Warn.
	GormLogLevel gormlogger.LogLevel
	// SlowThreshold defines the duration after which a GORM query is considered "slow" and logged as such.
	// If not set or zero, defaults to 200ms.
	SlowThreshold time.Duration
	// IgnoreRecordNotFoundError, if true, suppresses GORM's "record not found" errors from being logged as errors.
	// This is often desirable as "not found" can be a normal application flow rather than a system error.
	IgnoreRecordNotFoundError bool
}

// NewXyliumGormLogger creates a new GORM logger instance that integrates with a Xylium logger.
// It uses the provided XyliumGormLoggerConfig to configure its behavior.
func NewXyliumGormLogger(config XyliumGormLoggerConfig) gormlogger.Interface {
	if config.AppLogger == nil {
		// This fallback should ideally not be hit if the connector's New() function is used correctly.
		fallbackLogger := xylium.NewDefaultLogger()
		fallbackLogger.SetLevel(xylium.LevelWarn)
		fallbackLogger.Warnf("xylium-gorm (logger.go): AppLogger not provided to GORM logger adapter, using a new default Xylium logger as fallback.")
		config.AppLogger = fallbackLogger
	}

	if config.GormLogLevel == 0 { // 0 is the zero value for gormlogger.LogLevel enum.
		config.GormLogLevel = gormlogger.Warn // Default GORM log level to Warn.
		config.AppLogger.Debugf("xylium-gorm (logger.go): GormLogLevel not specified, defaulting to WARN for GORM's internal logging.")
	}

	if config.SlowThreshold <= 0 {
		config.SlowThreshold = 200 * time.Millisecond // Common GORM default for slow queries.
		config.AppLogger.Debugf("xylium-gorm (logger.go): SlowQueryThreshold not specified or invalid, defaulting to %v for GORM's slow query logging.", config.SlowThreshold)
	}
	// config.IgnoreRecordNotFoundError defaults to false (Go default for bool) if not set.
	// The main Connector's New() function in gorm.go handles setting a sensible default (true) for this.

	return &xyliumGormLogger{
		appLogger:                 config.AppLogger,
		gormLogLevel:              config.GormLogLevel,
		slowThreshold:             config.SlowThreshold,
		ignoreRecordNotFoundError: config.IgnoreRecordNotFoundError,
	}
}

// LogMode sets the GORM log level for this logger instance.
// This method is part of the gormlogger.Interface.
func (l *xyliumGormLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	newLogger := *l // Create a copy to modify, GORM expects a new interface instance.
	newLogger.gormLogLevel = level
	l.appLogger.Debugf("xylium-gorm (logger.go): GORM logger mode dynamically changed to %v.", level)
	return &newLogger
}

// getLoggerFromContext retrieves the most appropriate xylium.Logger.
// If a *xylium.Context is found within the provided Go context.Context,
// its contextual logger is used. Otherwise, the base appLogger (passed during
// xyliumGormLogger initialization) is returned. This ensures logs carry
// request-specific context (like request_id) when available.
func (l *xyliumGormLogger) getLoggerFromContext(ctx context.Context) xylium.Logger {
	if xc, ok := ctx.Value(xyliumContextKey).(*xylium.Context); ok && xc != nil {
		// A Xylium context is available; use its logger and add a field to indicate GORM scope.
		return xc.Logger().WithFields(xylium.M{"gorm_log_scope": "request_contextual"})
	}
	// No Xylium context found; use the base application logger with a GORM scope field.
	return l.appLogger.WithFields(xylium.M{"gorm_log_scope": "application_base"})
}

// Info logs informational messages from GORM.
// This method is part of the gormlogger.Interface.
func (l *xyliumGormLogger) Info(ctx context.Context, msg string, data ...interface{}) {
	if l.gormLogLevel >= gormlogger.Info {
		logger := l.getLoggerFromContext(ctx)
		logger.Infof(msg, data...)
	}
}

// Warn logs warning messages from GORM.
// This method is part of the gormlogger.Interface.
func (l *xyliumGormLogger) Warn(ctx context.Context, msg string, data ...interface{}) {
	if l.gormLogLevel >= gormlogger.Warn {
		logger := l.getLoggerFromContext(ctx)
		logger.Warnf(msg, data...)
	}
}

// Error logs error messages from GORM.
// This method is part of the gormlogger.Interface.
func (l *xyliumGormLogger) Error(ctx context.Context, msg string, data ...interface{}) {
	if l.gormLogLevel >= gormlogger.Error {
		logger := l.getLoggerFromContext(ctx)
		logger.Errorf(msg, data...)
	}
}

// Trace logs SQL query execution details (SQL, rows affected, duration, errors).
// This method is part of the gormlogger.Interface.
// The decision to log (and at what Xylium level) depends on the GORM log level,
// whether an error occurred, or if the query exceeded the slow threshold.
func (l *xyliumGormLogger) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	if l.gormLogLevel <= gormlogger.Silent { // Do nothing if GORM is set to silent.
		return
	}

	logger := l.getLoggerFromContext(ctx)
	elapsed := time.Since(begin)
	sqlQuery, rows := fc() // Get SQL query and rows affected from the callback.

	// Get the source file and line number from where GORM was called in the application code.
	gormCaller := utils.FileWithLineNum()

	logFields := xylium.M{
		"gorm_latency_ms": float64(elapsed.Nanoseconds()) / 1e6, // Query duration in milliseconds.
		"gorm_rows":       rows,                                 // Number of rows affected/returned.
		"gorm_sql":        sqlQuery,                             // The actual SQL query.
		"gorm_sql_caller": gormCaller,                           // File:line of the GORM call.
	}

	// Determine how to log based on error, slow query, or normal execution.
	switch {
	case err != nil && l.gormLogLevel >= gormlogger.Error && (!errors.Is(err, gormlogger.ErrRecordNotFound) || !l.ignoreRecordNotFoundError):
		// An error occurred (and it's not an ignored "record not found").
		logFields["gorm_error"] = err.Error()
		logger.WithFields(logFields).Errorf("GORM Query Error") // Log as ERROR level in Xylium.
	case l.slowThreshold > 0 && elapsed > l.slowThreshold && l.gormLogLevel >= gormlogger.Warn:
		// Query exceeded the slow threshold.
		logger.WithFields(logFields).Warnf("GORM Slow Query (Threshold: %v)", l.slowThreshold) // Log as WARN level.
	case l.gormLogLevel >= gormlogger.Info:
		// Normal query execution, log as DEBUG level in Xylium for verbosity control.
		// Application can set Xylium logger to DEBUG to see these, or INFO to hide them.
		logger.WithFields(logFields).Debugf("GORM Query Executed")
	}
}

// contextWithXylium is an unexported helper function that embeds a *xylium.Context
// into a new Go context.Context, derived from the parent.
// This is used by the Connector in gorm.go to pass the Xylium context to this logger adapter.
func contextWithXylium(parent context.Context, xc *xylium.Context) context.Context {
	if parent == nil {
		parent = context.Background() // Ensure parent is not nil to avoid panic.
	}
	return context.WithValue(parent, xyliumContextKey, xc)
}
