// Command migrate applies (or rolls back) Cornifer's Postgres schema using
// goose. It exists as its own binary, separate from cmd/cornifer, because
// running Go migrations requires the migrations package to be linked in
// (goose's version numbering matches Go migration functions to their
// source filename) — see migrations/doc.go.
//
// Usage:
//
//	go run ./cmd/migrate [up|down|status]   # defaults to "up"
//
// The target database is read from CORNIFER_DATABASE_URL, defaulting to the
// docker-compose Postgres started by `make up`.
package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"

	_ "github.com/Hendrixx-RE/cornifer/migrations" // registers Go migrations via init()
)

const defaultDatabaseURL = "postgres://cornifer:cornifer@localhost:5433/cornifer?sslmode=disable"

const migrationsDir = "migrations"

func main() {
	dsn := os.Getenv("CORNIFER_DATABASE_URL")
	if dsn == "" {
		dsn = defaultDatabaseURL
	}

	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	if err := goose.SetDialect("postgres"); err != nil {
		log.Fatalf("set dialect: %v", err)
	}

	switch command {
	case "up":
		err = goose.Up(db, migrationsDir)
	case "down":
		err = goose.Down(db, migrationsDir)
	case "status":
		err = goose.Status(db, migrationsDir)
	default:
		err = fmt.Errorf("unknown command %q (want up, down, or status)", command)
	}
	if err != nil {
		log.Fatalf("migrate %s: %v", command, err)
	}
}
