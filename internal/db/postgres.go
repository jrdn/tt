package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

//go:embed migrations/postgres/*.sql
var pgMigrations embed.FS

// migrationLockID is an arbitrary constant for pg_advisory_xact_lock so that
// concurrent servers don't apply migrations twice.
const migrationLockID = 7_415_020_001

// OpenPostgres connects to Postgres at url and applies pending migrations.
func OpenPostgres(ctx context.Context, url string) (*sqlx.DB, error) {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse postgres url: %w", err)
	}
	sqlDB := stdlib.OpenDB(*cfg, stdlib.OptionAfterConnect(func(ctx context.Context, c *pgx.Conn) error {
		// Scan timestamptz in UTC so values format the same as the RFC3339
		// "Z" strings stored by SQLite.
		c.TypeMap().RegisterType(&pgtype.Type{
			Name:  "timestamptz",
			OID:   pgtype.TimestamptzOID,
			Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC},
		})
		return nil
	}))
	d := sqlx.NewDb(sqlDB, "pgx")
	if err := d.PingContext(ctx); err != nil {
		d.Close()
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := migratePostgres(ctx, d); err != nil {
		d.Close()
		return nil, fmt.Errorf("migrate postgres: %w", err)
	}
	return d, nil
}

// migratePostgres applies each migrations/postgres/NNNN_*.sql file not yet
// recorded in schema_migrations, in order, in a single transaction.
func migratePostgres(ctx context.Context, d *sqlx.DB) error {
	tx, err := d.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return err
	}
	var applied []int
	if err := tx.SelectContext(ctx, &applied, `SELECT version FROM schema_migrations`); err != nil {
		return err
	}
	done := map[int]bool{}
	for _, v := range applied {
		done[v] = true
	}

	names, err := fs.Glob(pgMigrations, "migrations/postgres/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		base := name[strings.LastIndexByte(name, '/')+1:]
		version, err := strconv.Atoi(strings.SplitN(base, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration %s: bad version prefix", base)
		}
		if done[version] {
			continue
		}
		body, err := pgMigrations.ReadFile(name)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", base, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			return err
		}
	}
	return tx.Commit()
}
