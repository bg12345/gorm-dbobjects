package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"github.com/joho/godotenv"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
)

// loadEnv loads the repo-root .env regardless of the calling test
// binary's working directory. `go test` runs each package's test
// binary with its cwd set to that package's own source directory (e.g.
// tests/), not the repo root -- a bare godotenv.Load() silently finds
// nothing when called from any package other than this one, which is
// why every integration test using NewPostgres/NewMySQL was skipping
// with an empty DSN ("no DB reachable") instead of actually connecting.
// Deriving the path from this source file's own location (fixed at
// compile time via runtime.Caller) sidesteps the cwd dependency.
func loadEnv() {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return
	}
	// thisFile is internal/testutil/db.go; the repo root is two levels up.
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	_ = godotenv.Load(filepath.Join(root, ".env"))
}

func NewPostgres() (*gorm.DB,error){
	loadEnv()

	dsn := fmt.Sprintf(
		"host=%s user=%s password=%s dbname=%s port=%s sslmode=%s",
		os.Getenv("POSTGRES_HOST"),
		os.Getenv("POSTGRES_USER"),
		os.Getenv("POSTGRES_PASSWORD"),
		os.Getenv("POSTGRES_DB"),
		os.Getenv("POSTGRES_PORT"),
		os.Getenv("POSTGRES_SSLMODE"),
	)

	return gorm.Open(postgres.Open(dsn), &gorm.Config{})
}


func NewMySQL() (*gorm.DB,error){
	loadEnv()

	dsn := fmt.Sprintf(
		"%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		os.Getenv("MYSQL_USER"),os.Getenv("MYSQL_PASSWORD"),os.Getenv("MYSQL_HOST"),os.Getenv("MYSQL_PORT"),os.Getenv("MYSQL_DB"))
	
	return gorm.Open(mysql.Open(dsn), &gorm.Config{})
}

// NewSQLite opens a SQLite *gorm.DB at dsn. Unlike NewPostgres/NewMySQL,
// dsn is a parameter rather than read from .env -- SQLite needs no
// external service to configure, and callers own their own file
// lifecycle (typically a fresh t.TempDir() path per test) rather than
// sharing one configured connection target.
func NewSQLite(dsn string) (*gorm.DB, error) {
	return gorm.Open(sqlite.Open(dsn), &gorm.Config{})
}