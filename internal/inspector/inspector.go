package inspector

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"pgcaliper/internal/model"
)

type Inspector struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Inspector {
	return &Inspector{pool: pool}
}

// GetDatabaseTotalSize retrieves the total on-disk size of the entire connected PostgreSQL database
func (ins *Inspector) GetDatabaseTotalSize(ctx context.Context) (int64, string, error) {
	var totalBytes int64
	var prettySize string

	query := `SELECT pg_database_size(current_database()), pg_size_pretty(pg_database_size(current_database()))`
	err := ins.pool.QueryRow(ctx, query).Scan(&totalBytes, &prettySize)
	if err != nil {
		return 0, "", fmt.Errorf("failed to get database total size: %w", err)
	}
	return totalBytes, prettySize, nil
}

// ListSchemas finds all user schemas, optionally filtering by prefix (e.g. "tenant_")
func (ins *Inspector) ListSchemas(ctx context.Context, prefix string) ([]string, error) {
	query := `
		SELECT schema_name 
		FROM information_schema.schemata 
		WHERE schema_name NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
		  AND schema_name NOT LIKE 'pg_temp_%'
		  AND schema_name NOT LIKE 'pg_toast_temp_%'
		  AND ($1 = '' OR schema_name LIKE $1 || '%')
		ORDER BY schema_name;
	`
	rows, err := ins.pool.Query(ctx, query, prefix)
	if err != nil {
		return nil, fmt.Errorf("failed to list schemas: %w", err)
	}
	defer rows.Close()

	var schemas []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		schemas = append(schemas, s)
	}
	return schemas, nil
}

// InspectSchema calculates granular data, index, and total bytes for a specific schema
func (ins *Inspector) InspectSchema(ctx context.Context, schemaName string, quotaLimitBytes int64) (*model.TenantMetrics, error) {
	query := `
		SELECT 
			table_name,
			pg_table_size(quote_ident(table_schema) || '.' || quote_ident(table_name)) AS data_bytes,
			pg_indexes_size(quote_ident(table_schema) || '.' || quote_ident(table_name)) AS index_bytes,
			pg_total_relation_size(quote_ident(table_schema) || '.' || quote_ident(table_name)) AS total_bytes,
			pg_size_pretty(pg_table_size(quote_ident(table_schema) || '.' || quote_ident(table_name))) AS human_data,
			pg_size_pretty(pg_indexes_size(quote_ident(table_schema) || '.' || quote_ident(table_name))) AS human_index,
			pg_size_pretty(pg_total_relation_size(quote_ident(table_schema) || '.' || quote_ident(table_name))) AS human_total
		FROM information_schema.tables
		WHERE table_schema = $1 AND table_type = 'BASE TABLE'
		ORDER BY total_bytes DESC;
	`
	rows, err := ins.pool.Query(ctx, query, schemaName)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect tables for schema %s: %w", schemaName, err)
	}
	defer rows.Close()

	var tables []model.TableMetrics
	var sumData, sumIndex, sumTotal int64

	for rows.Next() {
		var tm model.TableMetrics
		tm.SchemaName = schemaName
		if err := rows.Scan(
			&tm.TableName,
			&tm.DataBytes,
			&tm.IndexBytes,
			&tm.TotalBytes,
			&tm.HumanDataSize,
			&tm.HumanIdxSize,
			&tm.HumanTotal,
		); err != nil {
			return nil, err
		}

		sumData += tm.DataBytes
		sumIndex += tm.IndexBytes
		sumTotal += tm.TotalBytes
		tables = append(tables, tm)
	}

	usagePct := 0.0
	if quotaLimitBytes > 0 {
		usagePct = (float64(sumTotal) / float64(quotaLimitBytes)) * 100.0
	}

	status := model.StatusActive
	if usagePct >= 100.0 {
		status = model.StatusLimitExceeded
	} else if usagePct >= 95.0 {
		status = model.StatusWarning95
	} else if usagePct >= 80.0 {
		status = model.StatusWarning80
	}

	return &model.TenantMetrics{
		TenantID:        schemaName,
		SchemaName:      schemaName,
		TotalBytes:      sumTotal,
		DataBytes:       sumData,
		IndexBytes:      sumIndex,
		LimitBytes:      quotaLimitBytes,
		UsagePercentage: usagePct,
		Status:          status,
		Tables:          tables,
		InspectedAt:     time.Now(),
	}, nil
}

// InspectAllConcurrently runs concurrent inspections across multiple schemas using worker goroutines
func (ins *Inspector) InspectAllConcurrently(ctx context.Context, schemas []string, quotaLimitBytes int64, maxWorkers int) ([]*model.TenantMetrics, error) {
	if maxWorkers <= 0 {
		maxWorkers = 4
	}

	results := make([]*model.TenantMetrics, len(schemas))
	jobs := make(chan int, len(schemas))
	var wg sync.WaitGroup

	for w := 0; w < maxWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				schemaName := schemas[idx]
				metrics, err := ins.InspectSchema(ctx, schemaName, quotaLimitBytes)
				if err != nil {
					fmt.Printf("[ERROR] failed to inspect schema %s: %v\n", schemaName, err)
					continue
				}
				results[idx] = metrics
			}
		}()
	}

	for i := range schemas {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	var filtered []*model.TenantMetrics
	for _, r := range results {
		if r != nil {
			filtered = append(filtered, r)
		}
	}

	return filtered, nil
}
