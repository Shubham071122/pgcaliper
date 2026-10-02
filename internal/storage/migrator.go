package storage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Migrator struct {
	pool   *pgxpool.Pool
	schema string
}

func NewMigrator(pool *pgxpool.Pool, schema string) *Migrator {
	if schema == "" {
		schema = "_pgcaliper"
	}
	return &Migrator{pool: pool, schema: schema}
}

func (m *Migrator) EnsureSchema(ctx context.Context) error {
	queries := []string{
		fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s;`, m.schema),
		fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.snapshots (
				id BIGSERIAL PRIMARY KEY,
				group_id VARCHAR(128) NOT NULL,
				group_type VARCHAR(32) NOT NULL,
				heap_bytes BIGINT NOT NULL,
				index_bytes BIGINT NOT NULL,
				total_bytes BIGINT NOT NULL,
				quota_bytes BIGINT NOT NULL,
				usage_percentage NUMERIC(6,2) NOT NULL,
				status VARCHAR(20) NOT NULL,
				tables_count INT NOT NULL DEFAULT 0,
				captured_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);
		`, m.schema),
		fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.table_details (
				id BIGSERIAL PRIMARY KEY,
				snapshot_id BIGINT NOT NULL REFERENCES %s.snapshots(id) ON DELETE CASCADE,
				schema_name VARCHAR(64) NOT NULL,
				table_name VARCHAR(64) NOT NULL,
				data_bytes BIGINT NOT NULL,
				index_bytes BIGINT NOT NULL,
				total_bytes BIGINT NOT NULL,
				captured_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);
		`, m.schema, m.schema),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_caliper_snap_group_time ON %s.snapshots(group_id, captured_at DESC);`, m.schema),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_caliper_snap_time ON %s.snapshots(captured_at DESC);`, m.schema),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_caliper_details_snap ON %s.table_details(snapshot_id);`, m.schema),
	}

	for _, q := range queries {
		if _, err := m.pool.Exec(ctx, q); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}

	return nil
}

func (m *Migrator) DropSchema(ctx context.Context) error {
	query := fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE;`, m.schema)
	if _, err := m.pool.Exec(ctx, query); err != nil {
		return fmt.Errorf("failed to drop schema %s: %w", m.schema, err)
	}
	return nil
}
