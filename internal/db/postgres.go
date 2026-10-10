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

	"github.com/XSAM/otelsql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

//go:embed migrations/postgres
var pgMigrations embed.FS

// OpenPostgres connects to the server's global schema (users, projects,
// keys) at url and applies pending global migrations.
func OpenPostgres(ctx context.Context, url string) (*sqlx.DB, error) {
	return openPostgres(ctx, url, "global", "global")
}

// OpenProjectSchema connects to one project's task schema, creating it if
// needed, and applies pending project migrations. Each project gets its own
// schema so a missed filter can never leak tasks across projects.
func OpenProjectSchema(ctx context.Context, url, schema string) (*sqlx.DB, error) {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse postgres url: %w", err)
	}
	if !validSchemaName(schema) {
		return nil, fmt.Errorf("invalid schema name %q", schema)
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	_, err = conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+schema)
	conn.Close(ctx)
	if err != nil {
		return nil, fmt.Errorf("create schema: %w", err)
	}
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	return openPostgres(ctx, url+sep+"search_path="+schema, "project", schema)
}

// validSchemaName allows only names safe to splice into SQL unquoted.
func validSchemaName(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for i, c := range s {
		if !(c == '_' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// openPostgres opens a pool whose queries are traced and whose pool stats are
// exported as metrics, labelled tt.pool=pool (one pool per project schema).
func openPostgres(ctx context.Context, url, migrationSet, pool string) (*sqlx.DB, error) {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse postgres url: %w", err)
	}
	connector := stdlib.GetConnector(*cfg, stdlib.OptionAfterConnect(func(ctx context.Context, c *pgx.Conn) error {
		// Scan timestamptz in UTC so values format the same as the RFC3339
		// "Z" strings stored by SQLite.
		c.TypeMap().RegisterType(&pgtype.Type{
			Name:  "timestamptz",
			OID:   pgtype.TimestamptzOID,
			Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC},
		})
		return nil
	}))
	sqlDB := otelsql.OpenDB(connector,
		otelsql.WithAttributes(semconv.DBSystemPostgreSQL, attribute.String("tt.pool", pool)),
		otelsql.WithSpanOptions(otelsql.SpanOptions{OmitConnResetSession: true, OmitRows: true}))
	// Unsafe: tables carry columns the Go structs don't map (e.g. seq), and
	// queries use SELECT * for parity with SQLite.
	d := sqlx.NewDb(sqlDB, "pgx").Unsafe()
	if err := d.PingContext(ctx); err != nil {
		d.Close()
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if _, err := otelsql.RegisterDBStatsMetrics(sqlDB,
		otelsql.WithAttributes(semconv.DBSystemPostgreSQL, attribute.String("tt.pool", pool))); err != nil {
		d.Close()
		return nil, fmt.Errorf("register db metrics: %w", err)
	}
	if err := migratePostgres(ctx, d, migrationSet); err != nil {
		d.Close()
		return nil, fmt.Errorf("migrate postgres: %w", err)
	}
	return d, nil
}

// migratePostgres applies each migrations/postgres/<set>/NNNN_*.sql file not
// yet recorded in the current schema's schema_migrations, in order, in a
// single transaction.
func migratePostgres(ctx context.Context, d *sqlx.DB, set string) error {
	tx, err := d.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Serialize concurrent servers migrating the same schema.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('tt:' || current_schema()))`); err != nil {
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

	names, err := fs.Glob(pgMigrations, "migrations/postgres/"+set+"/*.sql")
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
