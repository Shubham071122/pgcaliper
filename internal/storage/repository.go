package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"pgcaliper/internal/model"
)

type Repository struct {
	pool   *pgxpool.Pool
	schema string
}

func NewRepository(pool *pgxpool.Pool, schema string) *Repository {
	if schema == "" {
		schema = "_pgcaliper"
	}
	return &Repository{pool: pool, schema: schema}
}

func (r *Repository) SaveSnapshotsBatch(ctx context.Context, snapshots []*model.GroupSnapshot) error {
	if len(snapshots) == 0 {
		return nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin batch transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	insertSnapshotQuery := fmt.Sprintf(`
		INSERT INTO %s.snapshots (
			group_id, group_type, heap_bytes, index_bytes, total_bytes, 
			quota_bytes, usage_percentage, status, tables_count, captured_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id;
	`, r.schema)

	insertDetailQuery := fmt.Sprintf(`
		INSERT INTO %s.table_details (
			snapshot_id, schema_name, table_name, data_bytes, index_bytes, total_bytes, captured_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7);
	`, r.schema)

	for _, s := range snapshots {
		var snapshotID int64
		err = tx.QueryRow(ctx, insertSnapshotQuery,
			s.GroupID,
			string(s.GroupType),
			s.HeapBytes,
			s.IndexBytes,
			s.TotalBytes,
			s.QuotaBytes,
			s.UsagePercentage,
			string(s.Status),
			len(s.Tables),
			s.CapturedAt,
		).Scan(&snapshotID)

		if err != nil {
			return fmt.Errorf("failed inserting snapshot for %s: %w", s.GroupID, err)
		}
		s.ID = snapshotID

		if len(s.Tables) > 0 {
			batch := &pgx.Batch{}
			for _, t := range s.Tables {
				batch.Queue(insertDetailQuery,
					snapshotID,
					t.SchemaName,
					t.TableName,
					t.DataBytes,
					t.IndexBytes,
					t.TotalBytes,
					s.CapturedAt,
				)
			}
			br := tx.SendBatch(ctx, batch)
			for range s.Tables {
				if _, err := br.Exec(); err != nil {
					br.Close()
					return fmt.Errorf("failed writing batch table details for %s: %w", s.GroupID, err)
				}
			}
			br.Close()
		}
	}

	return tx.Commit(ctx)
}

func (r *Repository) GetLatestSnapshots(ctx context.Context) ([]model.GroupSnapshot, error) {
	query := fmt.Sprintf(`
		SELECT DISTINCT ON (group_id)
			id, group_id, group_type, heap_bytes, index_bytes, total_bytes,
			quota_bytes, usage_percentage, status, tables_count, captured_at
		FROM %s.snapshots
		ORDER BY group_id, captured_at DESC;
	`, r.schema)

	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch latest snapshots: %w", err)
	}
	defer rows.Close()

	var snapshots []model.GroupSnapshot
	for rows.Next() {
		var s model.GroupSnapshot
		var groupTypeStr, statusStr string
		err := rows.Scan(
			&s.ID,
			&s.GroupID,
			&groupTypeStr,
			&s.HeapBytes,
			&s.IndexBytes,
			&s.TotalBytes,
			&s.QuotaBytes,
			&s.UsagePercentage,
			&statusStr,
			&s.TablesCount,
			&s.CapturedAt,
		)
		if err != nil {
			return nil, err
		}
		s.GroupType = model.GroupType(groupTypeStr)
		s.Status = model.QuotaStatus(statusStr)
		snapshots = append(snapshots, s)
	}

	return snapshots, nil
}

func (r *Repository) GetTableDetails(ctx context.Context, groupID string) ([]model.TableMetrics, *model.GroupSnapshot, error) {
	snapQuery := fmt.Sprintf(`
		SELECT id, group_id, group_type, heap_bytes, index_bytes, total_bytes, quota_bytes, usage_percentage, status, captured_at
		FROM %s.snapshots
		WHERE group_id = $1
		ORDER BY captured_at DESC
		LIMIT 1;
	`, r.schema)

	var s model.GroupSnapshot
	var groupTypeStr, statusStr string
	err := r.pool.QueryRow(ctx, snapQuery, groupID).Scan(
		&s.ID,
		&s.GroupID,
		&groupTypeStr,
		&s.HeapBytes,
		&s.IndexBytes,
		&s.TotalBytes,
		&s.QuotaBytes,
		&s.UsagePercentage,
		&statusStr,
		&s.CapturedAt,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("no snapshot found for '%s': %w", groupID, err)
	}
	s.GroupType = model.GroupType(groupTypeStr)
	s.Status = model.QuotaStatus(statusStr)

	detailQuery := fmt.Sprintf(`
		SELECT schema_name, table_name, data_bytes, index_bytes, total_bytes,
		       pg_size_pretty(data_bytes), pg_size_pretty(index_bytes), pg_size_pretty(total_bytes)
		FROM %s.table_details
		WHERE snapshot_id = $1
		ORDER BY total_bytes DESC;
	`, r.schema)

	rows, err := r.pool.Query(ctx, detailQuery, s.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch table details: %w", err)
	}
	defer rows.Close()

	var tables []model.TableMetrics
	for rows.Next() {
		var tm model.TableMetrics
		if err := rows.Scan(
			&tm.SchemaName,
			&tm.TableName,
			&tm.DataBytes,
			&tm.IndexBytes,
			&tm.TotalBytes,
			&tm.HumanDataSize,
			&tm.HumanIdxSize,
			&tm.HumanTotal,
		); err != nil {
			return nil, nil, err
		}
		tables = append(tables, tm)
	}

	return tables, &s, nil
}

func (r *Repository) PurgeOldSnapshots(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}

	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	query := fmt.Sprintf(`DELETE FROM %s.snapshots WHERE captured_at < $1;`, r.schema)
	cmdTag, err := r.pool.Exec(ctx, query, cutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to purge old snapshots: %w", err)
	}
	return cmdTag.RowsAffected(), nil
}
