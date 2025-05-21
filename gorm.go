// Package xyliumgorm provides a Xylium framework connector for GORM,
// enabling seamless integration with contextual logging, Go context propagation,
// and lifecycle management.
package xyliumgorm

import (
	"context"
	"database/sql" // Required for sql.TxOptions
	"errors"
	"fmt"
	"reflect" // Used in AutoMigrate helper for logging model names
	"sync"    // For sync.Once and sync.Mutex in DoOnce helper
	"time"

	"github.com/arwahdevops/xylium-core/src/xylium" // PASTIKAN PATH INI BENAR
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	// Database drivers (e.g., _ "gorm.io/driver/sqlite") are NOT imported by this
	// library package. The application using xylium-gorm is responsible for blank
	// importing the required GORM driver for their chosen database.
)

// DialectorFunc defines a function signature that returns a gorm.Dialector.
// This allows applications to provide a pre-configured GORM dialector,
// giving them full control over database driver specifics if needed.
// Example for PostgreSQL:
//
//	func(dsn string) gorm.Dialector { return postgres.Open(dsn) }
type DialectorFunc func(dsn string) gorm.Dialector

// Config holds all configuration options for initializing the GORM Connector.
type Config struct {
	// DSN (Data Source Name) for the database connection. This field is mandatory.
	DSN string

	// DialectorFunc is a user-supplied function that returns a gorm.Dialector
	// based on the DSN. This field is mandatory and determines the database driver used.
	DialectorFunc DialectorFunc

	// AppLogger is the primary Xylium application logger. It's used for internal
	// logging by the connector and serves as the target for GORM's own log messages
	// if GORM logging is enabled. This field is mandatory.
	AppLogger xylium.Logger

	// EnableGormLog, if true, enables GORM's internal query logging, which will be
	// piped through the AppLogger (via xyliumGormLogger adapter).
	// Defaults to false if not set.
	EnableGormLog bool

	// GormLogLevel specifies the GORM-specific log level (e.g., Info, Warn, Error)
	// if EnableGormLog is true. Defaults to gormlogger.Warn.
	GormLogLevel gormlogger.LogLevel

	// SlowQueryThreshold defines the duration for GORM to consider a query "slow".
	// Used if EnableGormLog is true. Defaults to 200ms.
	SlowQueryThreshold time.Duration

	// IgnoreRecordNotFoundError, if true, GORM's "record not found" errors will not
	// be logged as errors by the GORM logger. Defaults to true for this connector.
	IgnoreRecordNotFoundError bool

	// MaxIdleConns sets the maximum number of connections in the idle connection pool.
	// Optional; if 0, Go's sql package default (usually 2) is used.
	MaxIdleConns int
	// MaxOpenConns sets the maximum number of open connections to the database.
	// Optional; if 0, there is no limit.
	MaxOpenConns int
	// ConnMaxLifetime sets the maximum amount of time a connection may be reused.
	// Optional; if 0, connections are reused indefinitely.
	ConnMaxLifetime time.Duration
	// ConnMaxIdleTime sets the maximum amount of time a connection may be idle.
	// Optional; if 0, connections are not closed due to a connection's idle time.
	ConnMaxIdleTime time.Duration

	// CustomGormConfig allows providing a fully custom *gorm.Config. If set,
	// it's used as the base GORM configuration. Fields like EnableGormLog,
	// GormLogLevel, etc., from this XyliumGorm.Config might override logger settings
	// in CustomGormConfig unless CustomGormConfig.Logger is already non-nil.
	CustomGormConfig *gorm.Config
}

// Connector is a Xylium-aware wrapper around GORM's *gorm.DB.
// It facilitates integration with Xylium's context, logging, and lifecycle.
type Connector struct {
	DB     *gorm.DB // The underlying GORM database instance.
	config Config   // The configuration used to initialize this connector.
}

// New creates and initializes a new GORM Connector instance based on the provided Config.
// It establishes the database connection, configures GORM (including logging),
// and sets up the connection pool.
// Returns the initialized *Connector or an error if initialization fails.
func New(cfg Config) (*Connector, error) {
	// Validate mandatory configuration fields.
	if cfg.DSN == "" {
		return nil, errors.New("xylium-gorm: Config.DSN is required")
	}
	if cfg.DialectorFunc == nil {
		return nil, errors.New("xylium-gorm: Config.DialectorFunc is required")
	}
	if cfg.AppLogger == nil {
		return nil, errors.New("xylium-gorm: Config.AppLogger is required")
	}

	// Obtain the GORM dialector using the user-provided function.
	dialector := cfg.DialectorFunc(cfg.DSN)
	if dialector == nil {
		return nil, errors.New("xylium-gorm: Config.DialectorFunc returned a nil dialector")
	}

	// Prepare GORM's core configuration.
	gormConfig := &gorm.Config{} // Start with a default gorm.Config.
	if cfg.CustomGormConfig != nil {
		// If user provided a custom *gorm.Config, use it as the base.
		gormConfig = cfg.CustomGormConfig
		cfg.AppLogger.Debugf("xylium-gorm: Using custom *gorm.Config provided by user.")
	}

	// Configure GORM's logger if EnableGormLog is true and no logger is already
	// set in a CustomGormConfig.
	if cfg.EnableGormLog && gormConfig.Logger == nil {
		// Prepare config for our Xylium-integrated GORM logger.
		adapterLoggerCfg := XyliumGormLoggerConfig{
			AppLogger:                 cfg.AppLogger,
			GormLogLevel:              cfg.GormLogLevel,       // Will default in NewXyliumGormLogger if zero.
			SlowThreshold:             cfg.SlowQueryThreshold, // Will default in NewXyliumGormLogger if zero.
			IgnoreRecordNotFoundError: cfg.IgnoreRecordNotFoundError,
		}
		// Default IgnoreRecordNotFoundError to true for this connector if not explicitly set by user
		// AND not part of a fully custom GORM config.
		if !cfg.IgnoreRecordNotFoundError && cfg.CustomGormConfig == nil {
			adapterLoggerCfg.IgnoreRecordNotFoundError = true
		}

		gormConfig.Logger = NewXyliumGormLogger(adapterLoggerCfg)
		cfg.AppLogger.Infof("xylium-gorm: GORM query logging enabled via Xylium logger (Effective GORM Level: %v, Slow Threshold: %v, Ignore Not Found: %t).",
			adapterLoggerCfg.GormLogLevel, // Log effective levels after defaults applied by NewXyliumGormLogger
			adapterLoggerCfg.SlowThreshold,
			adapterLoggerCfg.IgnoreRecordNotFoundError)
	} else if gormConfig.Logger == nil {
		// If GORM logging is not enabled and no custom logger provided, set GORM to silent.
		gormConfig.Logger = gormlogger.Default.LogMode(gormlogger.Silent)
		cfg.AppLogger.Infof("xylium-gorm: GORM query logging is silent (not enabled and no custom GORM logger provided).")
	}
	// If gormConfig.Logger was already set (e.g., via CustomGormConfig), that user-defined logger is respected.

	// Open the database connection using the dialector and GORM config.
	db, err := gorm.Open(dialector, gormConfig)
	if err != nil {
		cfg.AppLogger.Errorf("xylium-gorm: Failed to open database connection: %v (DSN length: %d)", err, len(cfg.DSN))
		return nil, fmt.Errorf("xylium-gorm: open database: %w", err)
	}

	// Configure the underlying SQL connection pool.
	sqlDB, err := db.DB() // Get the *sql.DB instance from GORM.
	if err != nil {
		// This is unlikely but possible. Log a warning and GORM will use its defaults.
		cfg.AppLogger.Warnf("xylium-gorm: Failed to get underlying *sql.DB for pool configuration: %v. Using GORM/driver default pool settings.", err)
	} else {
		poolConfigured := false
		if cfg.MaxIdleConns > 0 {
			sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
			poolConfigured = true
		}
		if cfg.MaxOpenConns > 0 {
			sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
			poolConfigured = true
		}
		if cfg.ConnMaxLifetime > 0 {
			sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
			poolConfigured = true
		}
		if cfg.ConnMaxIdleTime > 0 {
			sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
			poolConfigured = true
		}

		if poolConfigured {
			cfg.AppLogger.Infof("xylium-gorm: SQL connection pool configured (MaxIdle: %d, MaxOpen: %d, MaxLifetime: %v, MaxIdleTime: %v).",
				cfg.MaxIdleConns, cfg.MaxOpenConns, cfg.ConnMaxLifetime, cfg.ConnMaxIdleTime)
		} else {
			cfg.AppLogger.Debugf("xylium-gorm: Using default GORM/driver SQL connection pool settings (no pool options specified).")
		}
	}

	cfg.AppLogger.Infof("xylium-gorm: Connector successfully initialized.")
	return &Connector{
		DB:     db,
		config: cfg,
	}, nil
}

// Ctx returns a GORM database instance (*gorm.DB) that is bound to the
// provided *xylium.Context. Operations performed on this returned *gorm.DB
// will:
//  1. Use the Go context.Context from `xc.GoContext()` for timeout/cancellation.
//  2. Ensure GORM's logger (if enabled) uses `xc.Logger()` for contextualized logging.
//
// This is the primary method to use for database operations within Xylium handlers.
// Usage: `dbConnector.Ctx(xyliumCtx).First(&user, 1)`
func (c *Connector) Ctx(xc *xylium.Context) *gorm.DB {
	if xc == nil {
		// If called with a nil Xylium context (e.g., outside a request),
		// log a warning and return the base GORM DB instance.
		// Operations will use a background Go context and application-level logger.
		c.config.AppLogger.Warnf("xylium-gorm: Ctx(nil) called. Returning base GORM DB. Xylium context features (request-scoped logging, cancellation propagation) will not be active for this DB instance.")
		// Return DB with a background context to ensure GORM operations don't fail due to nil context.
		return c.DB.WithContext(context.Background())
	}
	// Embed the Xylium context into a new Go context that GORM will use.
	goCtxWithXylium := contextWithXylium(xc.GoContext(), xc) // Uses helper from logger.go
	return c.DB.WithContext(goCtxWithXylium)
}

// RawDB returns the raw, underlying *gorm.DB instance.
// Use this if you need to perform operations outside a Xylium request context
// or require GORM features not directly facilitated by `Ctx()`.
// Be mindful that operations on this instance will not automatically use
// Xylium's contextual logging or Go context propagation unless explicitly managed.
func (c *Connector) RawDB() *gorm.DB {
	return c.DB
}

// Close closes the database connection pool.
// This method implements `io.Closer`, allowing the Xylium Router to automatically
// close this connector during graceful shutdown if it's stored in the AppStore.
func (c *Connector) Close() error {
	c.config.AppLogger.Infof("xylium-gorm: Closing database connection...")
	sqlDB, err := c.DB.DB() // Get the underlying *sql.DB.
	if err != nil {
		c.config.AppLogger.Errorf("xylium-gorm: Failed to get underlying *sql.DB for closing: %v", err)
		return fmt.Errorf("xylium-gorm: get DB for close: %w", err)
	}
	if err := sqlDB.Close(); err != nil {
		c.config.AppLogger.Errorf("xylium-gorm: Error closing database connection: %v", err)
		return fmt.Errorf("xylium-gorm: close DB: %w", err)
	}
	c.config.AppLogger.Infof("xylium-gorm: Database connection closed successfully.")
	return nil
}

// Ping checks the database connection by sending a ping.
// It uses the Go context from the provided *xylium.Context for the ping operation.
// If xc is nil, a background context is used.
func (c *Connector) Ping(xc *xylium.Context) error {
	// Get a GORM DB instance appropriately contextualized (or background if xc is nil).
	dbForPing := c.Ctx(xc)

	sqlDB, err := dbForPing.DB()
	if err != nil {
		return fmt.Errorf("xylium-gorm: failed to get DB for ping: %w", err)
	}

	// Determine the Go context to use for PingContext.
	goCtxToUse := context.Background() // Default if xc is nil or xc.GoContext() is nil.
	if xc != nil && xc.GoContext() != nil {
		goCtxToUse = xc.GoContext()
	}
	return sqlDB.PingContext(goCtxToUse)
}

// AutoMigrate runs GORM's AutoMigrate feature for the given destination models (dst).
// It logs the process using the Xylium logger (contextual if `xc` is provided).
// This is a convenience helper.
func (c *Connector) AutoMigrate(xc *xylium.Context, dst ...interface{}) error {
	var currentLogger xylium.Logger
	dbForMigrate := c.RawDB() // Default to RawDB for operations outside a request.

	if xc != nil {
		currentLogger = xc.Logger().WithFields(xylium.M{"gorm_operation": "automigrate"})
		dbForMigrate = c.Ctx(xc) // Use contextual DB if Xylium context is available.
	} else {
		currentLogger = c.config.AppLogger.WithFields(xylium.M{"gorm_operation": "automigrate", "context_scope": "application"})
	}

	if len(dst) == 0 {
		currentLogger.Info("AutoMigrate called with no destination models; no action taken.")
		return nil
	}

	// Log model names for clarity.
	modelNames := make([]string, len(dst))
	for i, model := range dst {
		// Get type name robustly.
		val := reflect.ValueOf(model)
		if val.Kind() == reflect.Ptr { // If model is &User{}, get name of User.
			val = val.Elem()
		}
		if val.IsValid() && val.Type().Name() != "" {
			modelNames[i] = val.Type().Name()
		} else {
			modelNames[i] = fmt.Sprintf("anonymous_model_at_index_%d_(type_%T)", i, model) // Fallback name.
		}
	}

	currentLogger.Infof("Starting auto-migration for models: %v...", modelNames)
	err := dbForMigrate.AutoMigrate(dst...)
	if err != nil {
		currentLogger.Errorf("Auto-migration failed for models %v: %v", modelNames, err)
		return fmt.Errorf("xylium-gorm: auto-migrate models %v: %w", modelNames, err)
	}
	currentLogger.Infof("Auto-migration completed successfully for models: %v.", modelNames)
	return nil
}

// RunInTransaction executes the provided function `fn` within a database transaction.
// If `fn` returns an error, the transaction is rolled back. Otherwise, it's committed.
// The Go context from `xc` (if provided) is propagated to the transaction.
// `opts` can be used to specify `*sql.TxOptions` for transaction isolation levels.
func (c *Connector) RunInTransaction(xc *xylium.Context, fn func(txScopedDB *gorm.DB) error, opts ...*sql.TxOptions) error {
	// Get a GORM DB instance that is contextualized with the Xylium context (if available).
	// The .Transaction method of this contextualDB will then use the correct Go context.
	contextualDB := c.Ctx(xc)
	return contextualDB.Transaction(fn, opts...)
}

// syncOnceState holds the state for DoOnce operations.
var (
	// syncedOnceOps maps a unique key to a sync.Once, ensuring an operation is run only once.
	syncedOnceOps = make(map[string]*sync.Once)
	// syncedOnceMux protects concurrent access to syncedOnceOps.
	syncedOnceMux sync.Mutex
)

// DoOnce executes the function `fn` exactly once for a given `key` across the application's lifetime
// (as long as the `syncedOnceOps` map is not reset). This is useful for one-time database
// setup tasks like creating extensions, custom types, or initial seed data.
// The operation uses the Xylium logger for messages (contextual if `xc` is provided).
// The `*gorm.DB` passed to `fn` will be contextualized if `xc` is available.
func (c *Connector) DoOnce(xc *xylium.Context, key string, fn func(dbWithContext *gorm.DB) error) error {
	syncedOnceMux.Lock()
	once, exists := syncedOnceOps[key]
	if !exists {
		once = &sync.Once{}
		syncedOnceOps[key] = once
	}
	syncedOnceMux.Unlock()

	var operationError error
	once.Do(func() {
		var currentLogger xylium.Logger
		dbForOperation := c.RawDB() // Default for operations outside a request.

		if xc != nil {
			currentLogger = xc.Logger().WithFields(xylium.M{"gorm_do_once_key": key})
			dbForOperation = c.Ctx(xc) // Use contextual DB.
		} else {
			currentLogger = c.config.AppLogger.WithFields(xylium.M{"gorm_do_once_key": key, "context_scope": "application"})
		}

		currentLogger.Infof("Executing DoOnce database operation for key '%s'...", key)
		operationError = fn(dbForOperation) // Execute the user's function.
		if operationError != nil {
			currentLogger.Errorf("DoOnce database operation for key '%s' failed: %v", key, operationError)
		} else {
			currentLogger.Infof("DoOnce database operation for key '%s' completed successfully.", key)
		}
	})
	return operationError
}
