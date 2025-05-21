# Xylium GORM Connector (`xylium-gorm`)

[![Go Reference](https://pkg.go.dev/badge/github.com/arwahdevops/xylium-gorm.svg)](https://pkg.go.dev/github.com/arwahdevops/xylium-gorm)

`xylium-gorm` is the official Xylium framework connector for seamless and productive integration with [GORM](https://gorm.io/), the fantastic ORM library for Go. This connector is designed to simplify the use of GORM within Xylium applications by providing:

*   **Go Context Propagation:** Database operations automatically utilize the `context.Context` from your `xylium.Context`, enabling effective request timeouts and cancellations.
*   **Contextual Logging:** GORM query logs are integrated with `xylium.Logger`. Each logged query will include contextual information from the Xylium request (like `request_id` if Xylium's `RequestID` middleware is used).
*   **Easy Configuration:** A clear and flexible `Config` struct for database and GORM setup.
*   **Integrated Lifecycle Management:** Implements `io.Closer`, allowing Xylium to automatically close database connections during graceful shutdown if the connector is stored in Xylium's `appStore`.
*   **Helper Functions:** Provides utility functions like `AutoMigrate`, `RunInTransaction`, and `DoOnce` with integrated Xylium context handling and logging.

## Table of Contents

*   [Key Features](#key-features)
*   [Prerequisites](#prerequisites)
*   [Installation](#installation)
*   [Basic Usage](#basic-usage)
    *   [1. Initialize the Connector](#1-initialize-the-connector)
    *   [2. Store the Connector in Xylium's AppStore](#2-store-the-connector-in-xyliums-appstore)
    *   [3. Use the Connector in Handlers](#3-use-the-connector-in-handlers)
    *   [4. Example GORM Model](#4-example-gorm-model)
*   [Advanced Configuration](#advanced-configuration)
    *   [`xyliumgorm.Config` Struct](#xyliumgormconfig-struct)
    *   [Using `CustomGormConfig`](#using-customgormconfig)
    *   [GORM Logging](#gorm-logging)
*   [Helper Functions](#helper-functions)
    *   [`connector.Ping(xc *xylium.Context)`](#connectorpnigxc-xyliumcontext)
    *   [`connector.AutoMigrate(xc *xylium.Context, dst ...interface{})`](#connectorautomigratexc-xyliumcontext-dst-interface)
    *   [`connector.RunInTransaction(xc *xylium.Context, fn func(txScopedDB *gorm.DB) error, opts ...*sql.TxOptions)`](#connectorrunintransactionxc-xyliumcontext-fn-functpscopeddb-gormdb-error-opts-sqltxoptions)
    *   [`connector.DoOnce(xc *xylium.Context, key string, fn func(dbWithContext *gorm.DB) error)`](#connectordoooncexc-xyliumcontext-key-string-fn-funcdbwithcontext-gormdb-error)
*   [Accessing the Raw `*gorm.DB` Instance](#accessing-the-raw-gormdb-instance)
*   [Contributing](#contributing)
*   [License](#license)

## Key Features

*   **Full Xylium Context Integration:** `connector.Ctx(c)` returns a `*gorm.DB` instance aware of the current `xylium.Context`.
*   **Logging via `xylium.Logger`:** All GORM query logs (errors, slow queries, normal queries) are processed through Xylium's logging system, inheriting its format and contextual fields.
*   **Connection Pool Management:** Easy configuration for `MaxIdleConns`, `MaxOpenConns`, `ConnMaxLifetime`, and `ConnMaxIdleTime`.
*   **Graceful Shutdown:** Implements `io.Closer` ensuring database connections are closed safely when the Xylium application shuts down.
*   **Dialect Flexibility:** The `DialectorFunc` configuration allows using any GORM driver (e.g., SQLite, PostgreSQL, MySQL, SQL Server) with full dialector customization.
*   **Helpers for Common Operations:** Includes `AutoMigrate`, `RunInTransaction`, and `DoOnce` with Xylium context and logging integration.

## Prerequisites

*   Go (a version supported by Xylium Core and GORM, e.g., 1.18+).
*   Xylium Core framework installed in your project.
*   The GORM driver for your target database (e.g., `gorm.io/driver/postgres`).

## Installation

1.  Add `xylium-gorm` to your project:
    ```bash
    go get github.com/arwahdevops/xylium-gorm
    ```

2.  Ensure you have also installed the appropriate GORM driver for your database and *blank import* it in your application's `main.go` or database configuration package:
    ```go
    // main.go or config/database.go
    import (
        _ "gorm.io/driver/sqlite"   // For SQLite
        _ "gorm.io/driver/postgres" // For PostgreSQL
        _ "gorm.io/driver/mysql"    // For MySQL
        // ... other drivers if needed
    )
    ```
    `xylium-gorm` itself does not blank import drivers to maintain flexibility.

## Basic Usage

### 1. Initialize the Connector

Connector initialization is typically done in your application's `main()` function or a separate configuration package.

```go
// main.go
package main

import (
	"errors" // For errors.Is
	"fmt"
	"net/http"
	"os" // Example for env var
	"time" // For time.Duration and time.Hour

	"github.com/arwahdevops/xylium-core/src/xylium"
	xyliumgorm "github.com/arwahdevops/xylium-gorm"   // Replace with your connector path
	"gorm.io/driver/postgres" // Example using PostgreSQL
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Example function to create a GORM Dialector
func postgresDialectorFunc(dsn string) gorm.Dialector {
	return postgres.Open(dsn)
}

func main() {
	app := xylium.New()
	appLogger := app.Logger() // Use Xylium's application logger

	// Get DSN from environment variable or set a default
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		dsn = "host=localhost user=postgres password=secret dbname=mydb port=5432 sslmode=disable TimeZone=Asia/Jakarta"
		appLogger.Warnf("DB_DSN environment variable not set, using default DSN (PostgreSQL example).")
	}

	// Configuration for the xylium-gorm Connector
	dbConfig := xyliumgorm.Config{
		DSN:                   dsn,
		DialectorFunc:         postgresDialectorFunc, // Provide the appropriate dialector function
		AppLogger:             appLogger,             // Required: Xylium application logger
		EnableGormLog:         true,                  // Enable GORM query logging via Xylium
		GormLogLevel:          gormlogger.Info,       // Log all SQL queries from GORM (will be logged as DEBUG by xylium-gorm)
		SlowQueryThreshold:    200 * time.Millisecond,
		IgnoreRecordNotFoundError: true,              // Generally desired
		MaxIdleConns:          10,
		MaxOpenConns:          100,
		ConnMaxLifetime:       time.Hour,
	}

	dbConnector, err := xyliumgorm.New(dbConfig)
	if err != nil {
		appLogger.Fatalf("Failed to initialize GORM connector: %v", err)
	}
```

### 2. Store the Connector in Xylium's AppStore

For easy access in handlers and to leverage automatic graceful shutdown:

```go
	// ... after dbConnector is successfully initialized
	app.AppSet("db", dbConnector) // Xylium will automatically call dbConnector.Close() on shutdown
```

### 3. Use the Connector in Handlers

Use `connector.Ctx(c)` to get a `*gorm.DB` instance bound to the current `xylium.Context`.

```go
// Example Model
type User struct {
	gorm.Model
	Name  string `json:"name"`
	Email string `json:"email" gorm:"unique"`
}

// Handler to get a user by ID
func GetUserHandler(c *xylium.Context) error {
	// 1. Retrieve the connector from the appStore
	dbVal, ok := c.AppGet("db")
	if !ok {
		// This should not happen if initialized correctly
		return xylium.NewHTTPError(http.StatusInternalServerError, "Database connector unavailable")
	}
	dbConnector := dbVal.(*xyliumgorm.Connector) // Type assertion

	userID := c.Param("id") // Assuming an :id parameter in the route

	var user User
	// 2. Use .Ctx(c) for database operations
	// This will use c.GoContext() for timeouts/cancellation
	// and c.Logger() for GORM query logging.
	result := dbConnector.Ctx(c).First(&user, userID)

	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return xylium.NewHTTPError(http.StatusNotFound, fmt.Sprintf("User with ID '%s' not found.", userID))
		}
		// The GORM logger (if active) will have already logged SQL error details via Xylium logger.
		// Additional application-specific logging can be added here if needed.
		c.Logger().Errorf("Failed to retrieve user ID '%s': %v", userID, result.Error)
		return xylium.NewHTTPError(http.StatusInternalServerError, "Error retrieving user data.")
	}

	return c.JSON(http.StatusOK, user)
}

// Handler to create a new user
func CreateUserHandler(c *xylium.Context) error {
	dbConnector := c.AppGet("db").(*xyliumgorm.Connector) // Quick way if you're sure "db" always exists

	var newUser User
	if err := c.BindAndValidate(&newUser); err != nil {
		return err // Let Xylium's GlobalErrorHandler handle this
	}

	if err := dbConnector.Ctx(c).Create(&newUser).Error; err != nil {
		// Check for unique constraint violation (example)
		// This implementation might be driver-specific
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint") || 
		   strings.Contains(strings.ToLower(err.Error()), "duplicate key") {
			return xylium.NewHTTPError(http.StatusConflict, fmt.Sprintf("User with email '%s' already exists.", newUser.Email))
		}
		c.Logger().Errorf("Failed to create new user: %v", err)
		return xylium.NewHTTPError(http.StatusInternalServerError, "Could not save new user.")
	}

	return c.JSON(http.StatusCreated, newUser)
}

func main() {
    // ... (dbConnector initialization as above) ...
	app.AppSet("db", dbConnector)

    // Run migrations after successful connection and before starting the server
    // Using the AutoMigrate helper from the connector (first argument can be nil if outside Xylium request context)
    if err := dbConnector.AutoMigrate(nil, &User{}); err != nil { // Add your GORM models here
        appLogger.Fatalf("Failed to auto-migrate database: %v", err)
    }
    appLogger.Info("Database auto-migration successful.")

    app.POST("/users", CreateUserHandler)
    app.GET("/users/:id", GetUserHandler)

    appLogger.Info("Server starting on :8080...")
    if err := app.Start(":8080"); err != nil { // app.Start() provides graceful shutdown
        appLogger.Fatalf("Server failed to start: %v", err)
    }
}
```

### 4. Example GORM Model

Define your GORM models as usual:

```go
package entity // or models

import "gorm.io/gorm"

type User struct {
	gorm.Model        // Includes ID, CreatedAt, UpdatedAt, DeletedAt
	Name         string `json:"name"`
	Email        string `json:"email" gorm:"uniqueIndex"` // Ensure email is unique
	Age          int    `json:"age"`
}

type Product struct {
	gorm.Model
	Code        string  `json:"code" gorm:"uniqueIndex"`
	Description string  `json:"description"`
	Price       float64 `json:"price"`
}
```

## Advanced Configuration

### `xyliumgorm.Config` Struct

Key fields in the `xyliumgorm.Config` struct:

*   `DSN` (string): **Required**. The Data Source Name for the database connection.
*   `DialectorFunc` (xyliumgorm.DialectorFunc): **Required**. A function that returns a `gorm.Dialector`. Example:
    ```go
    import "gorm.io/driver/sqlite"
    func mySQLiteDialector(dsn string) gorm.Dialector { return sqlite.Open(dsn) }
    // ...
    config.DialectorFunc = mySQLiteDialector
    ```
*   `AppLogger` (xylium.Logger): **Required**. Your Xylium application's logger instance.
*   `EnableGormLog` (bool): Optional (default `false`). If `true`, enables GORM's internal query logging through `AppLogger`.
*   `GormLogLevel` (gormlogger.LogLevel): Optional (default `gormlogger.Warn`). Sets GORM-specific log level (if `EnableGormLog` is true). Options: `Silent`, `Error`, `Warn`, `Info`. Successful GORM queries at `Info` level are logged as `DEBUG` by the Xylium logger.
*   `SlowQueryThreshold` (time.Duration): Optional (default `200ms`). Queries slower than this are logged as `WARN`.
*   `IgnoreRecordNotFoundError` (bool): Optional (default `true` by this connector). If `true`, `gorm.ErrRecordNotFound` errors are not logged as errors by GORM's logger.
*   `MaxIdleConns` (int): Optional. Maximum idle connections in the pool.
*   `MaxOpenConns` (int): Optional. Maximum open connections to the database.
*   `ConnMaxLifetime` (time.Duration): Optional. Maximum lifetime for a reusable connection.
*   `ConnMaxIdleTime` (time.Duration): Optional. Maximum idle time for a connection.
*   `CustomGormConfig` (*gorm.Config): Optional. Provide your custom `*gorm.Config` for full GORM control. If `CustomGormConfig.Logger` is already set, `EnableGormLog` and `GormLogLevel` from `xyliumgorm.Config` will be ignored.

### Using `CustomGormConfig`

For deeper GORM customization (e.g., NamingStrategy, PrepareStmt):

```go
import "gorm.io/gorm/schema" // For NamingStrategy

myGormNamingStrategy := schema.NamingStrategy{
    TablePrefix: "app_",
    SingularTable: true,
}

customGormCfg := &gorm.Config{
    NamingStrategy: myGormNamingStrategy,
    // PrepareStmt: true, // Enable if your database supports prepared statements
    // Logger: myOwnCustomGormLogger, // You can provide your GORM logger here
}

dbConfig := xyliumgorm.Config{
    DSN:              "...",
    DialectorFunc:    myDialectorFunc,
    AppLogger:        appLogger,
    CustomGormConfig: customGormCfg, // Pass your custom GORM config
    // EnableGormLog can still be true if CustomGormConfig.Logger is not set.
    EnableGormLog:    true,
}
```

### GORM Logging

If `EnableGormLog` is `true` (and `CustomGormConfig.Logger` is not set), `xylium-gorm` initializes `xyliumGormLogger`. This adapter will:
*   Use the `AppLogger` from your configuration.
*   If a GORM operation is performed via `connector.Ctx(c)`, it will use `c.Logger()` (the contextual Xylium logger).
*   Successful GORM queries (at `gormlogger.Info` level) are logged as `DEBUG` by `xylium.Logger`.
*   Slow queries (based on `SlowQueryThreshold`) are logged as `WARN`.
*   Query errors are logged as `ERROR`.
*   Additional log fields like `gorm_latency_ms`, `gorm_rows`, `gorm_sql`, and `gorm_sql_caller` are included.

## Helper Functions

The connector provides several helper functions:

### `connector.Ping(xc *xylium.Context)`

Checks connectivity to the database. Uses the Go context from `xc`.

```go
if err := dbConnector.Ping(c); err != nil {
    // Handle ping error
}
```

### `connector.AutoMigrate(xc *xylium.Context, dst ...interface{})`

Runs `gorm.AutoMigrate` for the given models. Uses Xylium logger for output. `xc` can be `nil` if run outside a request context (e.g., at startup).

```go
// During startup, after connector initialization:
err := dbConnector.AutoMigrate(nil, &User{}, &Product{}) // Pass nil for xc if no Xylium context
if err != nil {
    appLogger.Fatalf("Migration failed: %v", err)
}
```

### `connector.RunInTransaction(xc *xylium.Context, fn func(txScopedDB *gorm.DB) error, opts ...*sql.TxOptions)`

Executes the function `fn` within a database transaction. The `txScopedDB` passed to `fn` is bound to the context `xc`.

```go
import "database/sql" // For sql.TxOptions

err := dbConnector.RunInTransaction(c, func(tx *gorm.DB) error {
    // Database operations within the transaction using 'tx'
    if err := tx.Create(&User{Name: "Transacted User"}).Error; err != nil {
        return err // Rollback
    }
    // ... other operations ...
    return nil // Commit
}, &sql.TxOptions{Isolation: sql.LevelSerializable}) // Optional transaction options
if err != nil {
    // Handle transaction error
}
```

### `connector.DoOnce(xc *xylium.Context, key string, fn func(dbWithContext *gorm.DB) error)`

Executes function `fn` exactly once for the given `key` during the application's lifetime. Useful for one-time database setups.

```go
// Example: Create a PostgreSQL extension only once
keyCreateExtension := "setup_uuid_ossp_extension"
err := dbConnector.DoOnce(nil, keyCreateExtension, func(db *gorm.DB) error {
    // Use db.Exec() or other raw SQL methods if needed
    return db.Exec("CREATE EXTENSION IF NOT EXISTS \"uuid-ossp\"").Error
})
if err != nil {
    appLogger.Errorf("Failed to execute DoOnce for '%s': %v", keyCreateExtension, err)
}
```

## Accessing the Raw `*gorm.DB` Instance

If you need direct access to the `*gorm.DB` instance without Xylium context wrapping (e.g., for background operations not tied to a request):

```go
rawGormDB := dbConnector.RawDB()
// Perform operations with rawGormDB.
// Remember, this will not automatically use c.GoContext() or c.Logger().
var users []User
rawGormDB.WithContext(context.Background()).Find(&users) // Example of manual context usage
```

## Contributing

Contributions are welcome! Please open an *issue* to discuss bugs or feature proposals, or submit a *pull request* for fixes and enhancements.

## License

`xylium-gorm` is licensed under the [MIT License](LICENSE).
