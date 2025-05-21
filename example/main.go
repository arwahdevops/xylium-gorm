// main.go
package main

import (
	"errors"
	"net/http"

	"github.com/arwahdevops/xylium-core/src/xylium" // Ganti dengan path Xylium core Anda
	xyliumgorm "github.com/arwahdevops/xylium-gorm" // Ganti dengan path xylium-gorm Anda
	"gorm.io/driver/sqlite"                         // Impor driver yang akan digunakan
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Definisikan model Anda (contoh)
type Product struct {
	gorm.Model
	Code  string `json:"code"`
	Price uint   `json:"price"`
}

// Fungsi untuk membuat GORM Dialector untuk SQLite
func sqliteDialectorFunc(dsn string) gorm.Dialector {
	return sqlite.Open(dsn)
}

// Fungsi untuk membuat GORM Dialector untuk PostgreSQL (contoh jika Anda ingin beralih)
// import "gorm.io/driver/postgres"
// func postgresDialectorFunc(dsn string) gorm.Dialector {
// 	return postgres.Open(dsn)
// }

func main() {
	app := xylium.New()
	appLogger := app.Logger() // Gunakan logger aplikasi Xylium

	// Konfigurasi untuk xylium-gorm Connector menggunakan SQLite
	dbConfig := xyliumgorm.Config{
		DSN:           "local_test.db",     // File database SQLite
		DialectorFunc: sqliteDialectorFunc, // Fungsi yang membuat dialector SQLite
		AppLogger:     appLogger,           // Logger Xylium untuk connector
		EnableGormLog: true,                // Aktifkan logging query GORM
		GormLogLevel:  gormlogger.Info,     // Level log untuk GORM (Info, Warn, Error)
		// SlowQueryThreshold: 200 * time.Millisecond, // Bisa diset jika perlu
		// IgnoreRecordNotFoundError: true, // Defaultnya sudah true di connector
		// MaxIdleConns:      10,
		// MaxOpenConns:      100,
		// ConnMaxLifetime:   time.Hour,
	}

	dbConnector, err := xyliumgorm.New(dbConfig)
	if err != nil {
		appLogger.Fatalf("Gagal menginisialisasi GORM connector: %v", err)
	}
	// Daftarkan Close() untuk graceful shutdown jika xylium-gorm disimpan di AppSet
	// Ini akan otomatis dilakukan jika Xylium Router Anda mendukungnya untuk io.Closer.
	// Jika tidak, Anda mungkin perlu memanggil defer dbConnector.Close() di main.
	// app.AppSet("db", dbConnector) akan otomatis mendaftarkannya jika dbConnector adalah io.Closer.
	// Jadi, pastikan dbConnector di-set ke app.AppSet.
	app.AppSet("db", dbConnector) // Ini akan mendaftarkan dbConnector.Close()

	// Jalankan migrasi (opsional, hanya contoh)
	// Gunakan dbConnector.RawDB() jika migrasi tidak memerlukan context request Xylium
	// atau dbConnector.AutoMigrate(nil, ...) jika helpernya digunakan
	err = dbConnector.AutoMigrate(nil, &Product{}) // Menggunakan helper AutoMigrate dari connector
	if err != nil {
		appLogger.Fatalf("Gagal migrasi database: %v", err)
	}
	appLogger.Info("Migrasi database berhasil.")

	// Definisi Route
	app.GET("/pingdb", func(c *xylium.Context) error {
		dbVal, ok := c.AppGet("db")
		if !ok {
			return xylium.NewHTTPError(http.StatusInternalServerError, "Database connector tidak ditemukan")
		}
		conn := dbVal.(*xyliumgorm.Connector)

		// Gunakan Ping dengan Xylium Context
		if errPing := conn.Ping(c); errPing != nil {
			c.Logger().Errorf("Ping DB gagal: %v", errPing)
			return c.String(http.StatusInternalServerError, "DB Ping Failed")
		}
		return c.String(http.StatusOK, "DB Ping OK")
	})

	app.POST("/products-test", func(c *xylium.Context) error {
		dbVal, ok := c.AppGet("db")
		if !ok {
			return xylium.NewHTTPError(http.StatusInternalServerError, "Database connector tidak ditemukan")
		}
		conn := dbVal.(*xyliumgorm.Connector)

		var inputProduct Product
		// Contoh data, idealnya dari c.BindAndValidate(&inputProduct)
		// Untuk demo, kita hardcode saja
		inputProduct.Code = "PROD" + xylium.Mode() // Tambahkan mode untuk membedakan
		inputProduct.Price = 1500
		if errBind := c.BindAndValidate(&inputProduct); errBind != nil {
			c.Logger().Warnf("Binding & Validation failed for product: %v", errBind)
			return errBind // Kembalikan error dari BindAndValidate
		}

		// Membuat produk baru menggunakan conn.Ctx(c)
		if errDb := conn.Ctx(c).Create(&inputProduct).Error; errDb != nil {
			c.Logger().Errorf("Gagal membuat produk: %v", errDb)
			return xylium.NewHTTPError(http.StatusInternalServerError, "Tidak dapat membuat produk")
		}
		c.Logger().Infof("Produk berhasil dibuat dengan ID: %d, Kode: %s", inputProduct.ID, inputProduct.Code)
		return c.JSON(http.StatusCreated, inputProduct)
	})

	app.GET("/products-test/:code", func(c *xylium.Context) error {
		dbVal, ok := c.AppGet("db")
		if !ok {
			return xylium.NewHTTPError(http.StatusInternalServerError, "Database connector tidak ditemukan")
		}
		conn := dbVal.(*xyliumgorm.Connector)
		productCode := c.Param("code")
		var product Product

		// Mengambil produk menggunakan conn.Ctx(c)
		if errDb := conn.Ctx(c).Where("code = ?", productCode).First(&product).Error; errDb != nil {
			if errors.Is(errDb, gorm.ErrRecordNotFound) {
				return xylium.NewHTTPError(http.StatusNotFound, "Produk tidak ditemukan")
			}
			c.Logger().Errorf("Error mengambil produk %s: %v", productCode, errDb)
			return xylium.NewHTTPError(http.StatusInternalServerError, "Error mengambil data produk")
		}
		return c.JSON(http.StatusOK, product)
	})

	appLogger.Infof("Server testing memulai di :8081 (Mode: %s)", app.CurrentMode())
	if err := app.Start(":8081"); err != nil {
		appLogger.Fatalf("Error memulai server testing: %v", err)
	}
	appLogger.Info("Server testing telah berhenti.") // Pesan ini akan muncul setelah graceful shutdown
}
