package inspector

import (
	"context"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"pgcaliper/internal/config"
	"pgcaliper/internal/model"
)

type Inspector struct {
	pool *pgxpool.Pool
	cfg  *config.Config
}

func New(pool *pgxpool.Pool, cfg *config.Config) *Inspector {
	return &Inspector{
		pool: pool,
		cfg:  cfg,
	}
}

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

func (ins *Inspector) ListTargetSchemas(ctx context.Context, pattern string) ([]string, error) {
	query := `
		SELECT schema_name 
		FROM information_schema.schemata 
		WHERE schema_name NOT IN ('pg_catalog', 'information_schema', 'pg_toast', '_pgcaliper')
		  AND schema_name NOT LIKE 'pg_temp_%'
		  AND schema_name NOT LIKE 'pg_toast_temp_%'
		ORDER BY schema_name;
	`
	rows, err := ins.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list schemas: %w", err)
	}
	defer rows.Close()

	var compiledRegex *regexp.Regexp
	if pattern != "" {
		var err error
		compiledRegex, err = regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid schema regex pattern '%s': %w", pattern, err)
		}
	}

	var schemas []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}

		if compiledRegex == nil || compiledRegex.MatchString(s) {
			schemas = append(schemas, s)
		}
	}

	return schemas, nil
}

func (ins *Inspector) InspectSchema(ctx context.Context, schemaName string, quotaBytes int64) (*model.GroupSnapshot, error) {
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
	if quotaBytes > 0 {
		usagePct = (float64(sumTotal) / float64(quotaBytes)) * 100.0
	}

	status := model.StatusActive
	if usagePct >= 100.0 {
		status = model.StatusLimitExceeded
	} else if usagePct >= 95.0 {
		status = model.StatusWarning95
	} else if usagePct >= 80.0 {
		status = model.StatusWarning80
	}

	return &model.GroupSnapshot{
		GroupID:         schemaName,
		GroupType:       model.GroupTypeSchema,
		HeapBytes:       sumData,
		IndexBytes:      sumIndex,
		TotalBytes:      sumTotal,
		QuotaBytes:      quotaBytes,
		UsagePercentage: usagePct,
		Status:          status,
		TablesCount:     len(tables),
		Tables:          tables,
		CapturedAt:      time.Now(),
	}, nil
}

func (ins *Inspector) InspectDatabaseFleet(ctx context.Context, quotaBytes int64) ([]*model.GroupSnapshot, error) {
	query := `
		SELECT 
			datname,
			pg_database_size(datname) AS total_bytes
		FROM pg_stat_database
		WHERE datname NOT IN ('postgres', 'template0', 'template1')
		ORDER BY total_bytes DESC;
	`
	rows, err := ins.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect database fleet: %w", err)
	}
	defer rows.Close()

	var snapshots []*model.GroupSnapshot
	for rows.Next() {
		var dbName string
		var totalBytes int64
		if err := rows.Scan(&dbName, &totalBytes); err != nil {
			return nil, err
		}

		usagePct := 0.0
		if quotaBytes > 0 {
			usagePct = (float64(totalBytes) / float64(quotaBytes)) * 100.0
		}

		status := model.StatusActive
		if usagePct >= 100.0 {
			status = model.StatusLimitExceeded
		} else if usagePct >= 95.0 {
			status = model.StatusWarning95
		} else if usagePct >= 80.0 {
			status = model.StatusWarning80
		}

		snapshots = append(snapshots, &model.GroupSnapshot{
			GroupID:         dbName,
			GroupType:       model.GroupTypeDatabase,
			HeapBytes:       totalBytes,
			IndexBytes:      0,
			TotalBytes:      totalBytes,
			QuotaBytes:      quotaBytes,
			UsagePercentage: usagePct,
			Status:          status,
			TablesCount:     0,
			CapturedAt:      time.Now(),
		})
	}

	return snapshots, nil
}

func (ins *Inspector) InspectCustomGroups(ctx context.Context, quotaBytes int64) ([]*model.GroupSnapshot, error) {
	var snapshots []*model.GroupSnapshot

	for _, g := range ins.cfg.Strategy.Groups {
		var sumData, sumIndex, sumTotal int64
		var tables []model.TableMetrics

		for _, tblPattern := range g.Tables {
			query := `
				SELECT 
					table_schema,
					table_name,
					pg_table_size(quote_ident(table_schema) || '.' || quote_ident(table_name)) AS data_bytes,
					pg_indexes_size(quote_ident(table_schema) || '.' || quote_ident(table_name)) AS index_bytes,
					pg_total_relation_size(quote_ident(table_schema) || '.' || quote_ident(table_name)) AS total_bytes
				FROM information_schema.tables
				WHERE table_type = 'BASE TABLE'
				  AND table_schema NOT IN ('pg_catalog', 'information_schema', '_pgcaliper')
				  AND table_name LIKE $1;
			`
			likePattern := regexp.MustCompile(`\*`).ReplaceAllString(tblPattern, "%")
			rows, err := ins.pool.Query(ctx, query, likePattern)
			if err != nil {
				continue
			}

			for rows.Next() {
				var tm model.TableMetrics
				if err := rows.Scan(&tm.SchemaName, &tm.TableName, &tm.DataBytes, &tm.IndexBytes, &tm.TotalBytes); err == nil {
					sumData += tm.DataBytes
					sumIndex += tm.IndexBytes
					sumTotal += tm.TotalBytes
					tables = append(tables, tm)
				}
			}
			rows.Close()
		}

		usagePct := 0.0
		if quotaBytes > 0 {
			usagePct = (float64(sumTotal) / float64(quotaBytes)) * 100.0
		}

		status := model.StatusActive
		if usagePct >= 100.0 {
			status = model.StatusLimitExceeded
		} else if usagePct >= 95.0 {
			status = model.StatusWarning95
		} else if usagePct >= 80.0 {
			status = model.StatusWarning80
		}

		snapshots = append(snapshots, &model.GroupSnapshot{
			GroupID:         g.Name,
			GroupType:       model.GroupTypeCustom,
			HeapBytes:       sumData,
			IndexBytes:      sumIndex,
			TotalBytes:      sumTotal,
			QuotaBytes:      quotaBytes,
			UsagePercentage: usagePct,
			Status:          status,
			TablesCount:     len(tables),
			Tables:          tables,
			CapturedAt:      time.Now(),
		})
	}

	return snapshots, nil
}

func (ins *Inspector) RunInspection(ctx context.Context) ([]*model.GroupSnapshot, error) {
	switch ins.cfg.Strategy.Mode {
	case "database":
		return ins.InspectDatabaseFleet(ctx, ins.cfg.Strategy.DefaultQuotaBytes)
	case "custom_group":
		return ins.InspectCustomGroups(ctx, ins.cfg.Strategy.DefaultQuotaBytes)
	default:
		schemas, err := ins.ListTargetSchemas(ctx, ins.cfg.Strategy.SchemaPattern)
		if err != nil {
			return nil, err
		}

		if len(schemas) == 0 {
			return nil, nil
		}

		maxWorkers := ins.cfg.Engine.Concurrency
		if maxWorkers <= 0 {
			maxWorkers = 4
		}

		results := make([]*model.GroupSnapshot, len(schemas))
		jobs := make(chan int, len(schemas))
		var wg sync.WaitGroup

		for w := 0; w < maxWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for idx := range jobs {
					schemaName := schemas[idx]
					snapshot, err := ins.InspectSchema(ctx, schemaName, ins.cfg.Strategy.DefaultQuotaBytes)
					if err != nil {
						fmt.Printf("▲ Error inspecting schema %s: %v\n", schemaName, err)
						continue
					}
					results[idx] = snapshot
				}
			}()
		}

		for i := range schemas {
			jobs <- i
		}
		close(jobs)
		wg.Wait()

		var validSnapshots []*model.GroupSnapshot
		for _, r := range results {
			if r != nil {
				validSnapshots = append(validSnapshots, r)
			}
		}

		return validSnapshots, nil
	}
}
