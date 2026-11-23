// Package mysqltest creates disposable databases from the application schema.
package mysqltest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// Open creates a new database using TEST_MYSQL_DSN and drops it on test cleanup.
func Open(t testing.TB) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN not set")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse TEST_MYSQL_DSN: %v", err)
	}
	schema, err := loadSchema()
	if err != nil {
		t.Fatal(err)
	}

	cfg.DBName = ""
	admin, err := openSQL(cfg)
	if err != nil {
		t.Fatalf("open MySQL admin connection: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var suffix [8]byte
	rand.Read(suffix[:])
	name := "t_" + hex.EncodeToString(suffix[:])
	quotedName := "`" + name + "`"
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+quotedName); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP DATABASE "+quotedName); err != nil {
			t.Errorf("drop test database %s: %v", name, err)
		}
	})

	cfg.DBName = name
	cfg.ParseTime = true
	cfg.MultiStatements = true
	cfg.Loc = time.UTC
	conn, err := openSQL(cfg)
	if err != nil {
		t.Fatalf("open test database %s: %v", name, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.ExecContext(ctx, string(schema)); err != nil {
		t.Fatalf("initialize test database %s: %v", name, err)
	}
	db, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: conn}), &gorm.Config{})
	if err != nil {
		t.Fatalf("initialize GORM for %s: %v", name, err)
	}
	return db
}

func openSQL(cfg *mysql.Config) (*sql.DB, error) {
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(connector), nil
}

func loadSchema() ([]byte, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	for {
		_, err := os.Stat(filepath.Join(dir, "go.mod"))
		if err == nil {
			return os.ReadFile(filepath.Join(dir, "migrations", "001_init_schema.sql"))
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, fmt.Errorf("cannot find repository root for MySQL test schema")
		}
		dir = parent
	}
}
