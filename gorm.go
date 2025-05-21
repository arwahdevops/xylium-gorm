// Package xyliumgorm provides a Xylium framework connector for GORM,
// enabling seamless integration with contextual logging, Go context propagation,
// and lifecycle management.
package xyliumgorm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/arwahdevops/xylium-core/src/xylium"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// DialectorFunc defines a function signature that returns a gorm.Dialector.
type DialectorFunc func(dsn string) gorm.Dialector

// Config holds all configuration options for initializing the GORM Connector.
type Config struct {
	DSN                       string
	DialectorFunc             DialectorFunc
	AppLogger                 xylium.Logger
	EnableGormLog             bool
	GormLogLevel              gormlogger.LogLevel
	SlowQueryThreshold        time.Duration
	IgnoreRecordNotFoundError bool
	MaxIdleConns              int
	MaxOpenConns              int
	ConnMaxLifetime           time.Duration
	ConnMaxIdleTime           time.Duration
	CustomGormConfig          *gorm.Config
}

// Connector is a Xylium-aware wrapper around GORM's *gorm.DB.
type Connector struct {
	DB     *gorm.DB
	config Config
}

// New creates and initializes a new GORM Connector instance.
func New(cfg Config) (*Connector, error) {
	if cfg.DSN == "" {
		return nil, errors.New("xylium-gorm: Config.DSN is required")
	}
	if cfg.DialectorFunc == nil {
		return nil, errors.New("xylium-gorm: Config.DialectorFunc is required")
	}
	if cfg.AppLogger == nil {
		return nil, errors.New("xylium-gorm: Config.AppLogger is required")
	}

	dialector := cfg.DialectorFunc(cfg.DSN)
	if dialector == nil {
		return nil, errors.New("xylium-gorm: Config.DialectorFunc returned a nil dialector")
	}

	gormConfig := &gorm.Config{}
	if cfg.CustomGormConfig != nil {
		gormConfig = cfg.CustomGormConfig
		cfg.AppLogger.Debugf("xylium-gorm: Using custom *gorm.Config provided by user.")
	}

	if cfg.EnableGormLog && gormConfig.Logger == nil {
		adapterLoggerCfg := XyliumGormLoggerConfig{
			AppLogger:                 cfg.AppLogger,
			GormLogLevel:              cfg.GormLogLevel,
			SlowThreshold:             cfg.SlowQueryThreshold,
			IgnoreRecordNotFoundError: cfg.IgnoreRecordNotFoundError,
		}
		if !cfg.IgnoreRecordNotFoundError && cfg.CustomGormConfig == nil {
			adapterLoggerCfg.IgnoreRecordNotFoundError = true
		}
		gormConfig.Logger = NewXyliumGormLogger(adapterLoggerCfg)
		// Mengambil nilai efektif setelah NewXyliumGormLogger mungkin telah menerapkan default
		effectiveAdapter, _ := gormConfig.Logger.(*xyliumGormLogger)
		if effectiveAdapter != nil {
			cfg.AppLogger.Infof("xylium-gorm: GORM query logging enabled via Xylium logger (Effective GORM Level: %v, Slow Threshold: %v, Ignore Not Found: %t).",
				effectiveAdapter.gormLogLevel,
				effectiveAdapter.slowThreshold,
				effectiveAdapter.ignoreRecordNotFoundError)
		} else {
			cfg.AppLogger.Warnf("xylium-gorm: GORM logger was set but could not be cast to *xyliumGormLogger to report effective settings.")
		}

	} else if gormConfig.Logger == nil {
		gormConfig.Logger = gormlogger.Default.LogMode(gormlogger.Silent)
		cfg.AppLogger.Infof("xylium-gorm: GORM query logging is silent.")
	}

	db, err := gorm.Open(dialector, gormConfig)
	if err != nil {
		cfg.AppLogger.Errorf("xylium-gorm: Failed to open database connection: %v (DSN length: %d)", err, len(cfg.DSN))
		return nil, fmt.Errorf("xylium-gorm: open database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		cfg.AppLogger.Warnf("xylium-gorm: Failed to get underlying *sql.DB for pool configuration: %v.", err)
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
			cfg.AppLogger.Debugf("xylium-gorm: Using default GORM/driver SQL connection pool settings.")
		}
	}

	cfg.AppLogger.Infof("xylium-gorm: Connector successfully initialized.")
	return &Connector{DB: db, config: cfg}, nil
}

// Ctx returns a GORM database instance bound to the *xylium.Context.
func (c *Connector) Ctx(xc *xylium.Context) *gorm.DB {
	if xc == nil {
		c.config.AppLogger.Warnf("xylium-gorm: Ctx(nil) called. Returning base GORM DB with context.Background().")
		// PERBAIKAN: Gunakan context.Background()
		return c.DB.WithContext(context.Background())
	}
	goCtxWithXylium := contextWithXylium(xc.GoContext(), xc)
	return c.DB.WithContext(goCtxWithXylium)
}

// RawDB returns the raw, underlying *gorm.DB instance.
func (c *Connector) RawDB() *gorm.DB {
	return c.DB
}

// Close closes the database connection pool.
func (c *Connector) Close() error {
	c.config.AppLogger.Infof("xylium-gorm: Closing database connection...")
	sqlDB, err := c.DB.DB()
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

// Ping checks the database connection.
func (c *Connector) Ping(xc *xylium.Context) error {
	dbForPing := c.Ctx(xc)
	sqlDB, err := dbForPing.DB()
	if err != nil {
		return fmt.Errorf("xylium-gorm: failed to get DB for ping: %w", err)
	}
	goCtxToUse := context.Background() // PERBAIKAN: Gunakan context.Background()
	if xc != nil && xc.GoContext() != nil {
		goCtxToUse = xc.GoContext()
	}
	return sqlDB.PingContext(goCtxToUse)
}

// AutoMigrate runs GORM's AutoMigrate.
func (c *Connector) AutoMigrate(xc *xylium.Context, dst ...interface{}) error {
	var currentLogger xylium.Logger
	dbForMigrate := c.RawDB()

	if xc != nil {
		currentLogger = xc.Logger().WithFields(xylium.M{"gorm_operation": "automigrate"})
		dbForMigrate = c.Ctx(xc)
	} else {
		currentLogger = c.config.AppLogger.WithFields(xylium.M{"gorm_operation": "automigrate", "context_scope": "application"})
	}

	if len(dst) == 0 {
		currentLogger.Info("AutoMigrate called with no destination models; no action taken.")
		return nil
	}

	modelNames := make([]string, len(dst))
	for i, model := range dst {
		val := reflect.ValueOf(model)
		if val.Kind() == reflect.Ptr {
			val = val.Elem()
		}
		if val.IsValid() && val.Type().Name() != "" {
			modelNames[i] = val.Type().Name()
		} else {
			modelNames[i] = fmt.Sprintf("anonymous_model_at_index_%d_(type_%T)", i, model)
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

// RunInTransaction executes fn within a database transaction.
func (c *Connector) RunInTransaction(xc *xylium.Context, fn func(txScopedDB *gorm.DB) error, opts ...*sql.TxOptions) error {
	contextualDB := c.Ctx(xc)
	return contextualDB.Transaction(fn, opts...)
}

var (
	syncedOnceOps = make(map[string]*sync.Once)
	syncedOnceMux sync.Mutex
)

// DoOnce executes fn exactly once for a given key.
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
		dbForOperation := c.RawDB()

		if xc != nil {
			currentLogger = xc.Logger().WithFields(xylium.M{"gorm_do_once_key": key})
			dbForOperation = c.Ctx(xc)
		} else {
			currentLogger = c.config.AppLogger.WithFields(xylium.M{"gorm_do_once_key": key, "context_scope": "application"})
		}

		currentLogger.Infof("Executing DoOnce database operation for key '%s'...", key)
		operationError = fn(dbForOperation)
		if operationError != nil {
			currentLogger.Errorf("DoOnce database operation for key '%s' failed: %v", key, operationError)
		} else {
			currentLogger.Infof("DoOnce database operation for key '%s' completed successfully.", key)
		}
	})
	return operationError
}
