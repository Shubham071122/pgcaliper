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

	status := calculateStatus(usagePct)

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

func (ins *Inspector) InspectDatabase(ctx context.Context, dbName string, quotaBytes int64) (*model.GroupSnapshot, error) {
	var totalBytes int64
	err := ins.pool.QueryRow(ctx, `SELECT pg_database_size($1)`, dbName).Scan(&totalBytes)
	if err != nil {
		return nil, fmt.Errorf("failed measuring database %s size: %w", dbName, err)
	}

	usagePct := 0.0
	if quotaBytes > 0 {
		usagePct = (float64(totalBytes) / float64(quotaBytes)) * 100.0
	}

	return &model.GroupSnapshot{
		GroupID:         dbName,
		GroupType:       model.GroupTypeDatabase,
		HeapBytes:       totalBytes,
		IndexBytes:      0,
		TotalBytes:      totalBytes,
		QuotaBytes:      quotaBytes,
		UsagePercentage: usagePct,
		Status:          calculateStatus(usagePct),
		TablesCount:     0,
		CapturedAt:      time.Now(),
	}, nil
}

func (ins *Inspector) InspectCustomGroup(ctx context.Context, group config.CustomGroup, quotaBytes int64) (*model.GroupSnapshot, error) {
	var tables []model.TableMetrics
	var sumData, sumIndex, sumTotal int64

	for _, tbl := range group.Tables {
		query := `
			SELECT 
				pg_table_size(quote_ident($1)) AS data_bytes,
				pg_indexes_size(quote_ident($1)) AS index_bytes,
				pg_total_relation_size(quote_ident($1)) AS total_bytes,
				pg_size_pretty(pg_table_size(quote_ident($1))) AS human_data,
				pg_size_pretty(pg_indexes_size(quote_ident($1))) AS human_index,
				pg_size_pretty(pg_total_relation_size(quote_ident($1))) AS human_total
		`
		var tm model.TableMetrics
		tm.SchemaName = "public"
		tm.TableName = tbl
		err := ins.pool.QueryRow(ctx, query, tbl).Scan(
			&tm.DataBytes,
			&tm.IndexBytes,
			&tm.TotalBytes,
			&tm.HumanDataSize,
			&tm.HumanIdxSize,
			&tm.HumanTotal,
		)
		if err != nil {
			continue
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

	return &model.GroupSnapshot{
		GroupID:         group.Name,
		GroupType:       model.GroupTypeCustom,
		HeapBytes:       sumData,
		IndexBytes:      sumIndex,
		TotalBytes:      sumTotal,
		QuotaBytes:      quotaBytes,
		UsagePercentage: usagePct,
		Status:          calculateStatus(usagePct),
		TablesCount:     len(tables),
		Tables:          tables,
		CapturedAt:      time.Now(),
	}, nil
}

func (ins *Inspector) FindTablesWithColumn(ctx context.Context, columnName string) ([]string, error) {
	query := `
		SELECT table_name 
		FROM information_schema.columns 
		WHERE column_name = $1 
		  AND table_schema = 'public' 
		  AND table_name NOT IN (SELECT table_name FROM information_schema.views WHERE table_schema = 'public')
		ORDER BY table_name;
	`
	rows, err := ins.pool.Query(ctx, query, columnName)
	if err != nil {
		return nil, fmt.Errorf("failed to discover tables with column '%s': %w", columnName, err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err == nil {
			tables = append(tables, t)
		}
	}
	return tables, nil
}

func (ins *Inspector) ValidateTableHasColumn(ctx context.Context, tableName string, columnName string) (bool, error) {
	var count int
	query := `
		SELECT COUNT(*) 
		FROM information_schema.columns 
		WHERE table_name = $1 AND column_name = $2 AND table_schema = 'public'
	`
	err := ins.pool.QueryRow(ctx, query, tableName, columnName).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (ins *Inspector) InspectRowLevelTenants(ctx context.Context, tenantColumn string, targetTables []string, quotaBytes int64) ([]*model.GroupSnapshot, error) {
	type TenantAggregate struct {
		HeapBytes  int64
		IndexBytes int64
		TablesSeen int
	}

	tenantMap := make(map[string]*TenantAggregate)
	var mu sync.Mutex

	for _, tbl := range targetTables {
		var totalTableHeap, totalTableIndex int64
		sizeQuery := fmt.Sprintf(`SELECT pg_table_size('%s'), pg_indexes_size('%s')`, tbl, tbl)
		_ = ins.pool.QueryRow(ctx, sizeQuery).Scan(&totalTableHeap, &totalTableIndex)

		query := fmt.Sprintf(`
			SELECT 
				CAST(%s AS TEXT) AS tenant_id,
				SUM(pg_column_size(t.*)) AS tenant_heap
			FROM %s t
			WHERE %s IS NOT NULL
			GROUP BY %s;
		`, tenantColumn, tbl, tenantColumn, tenantColumn)

		rows, err := ins.pool.Query(ctx, query)
		if err != nil {
			continue
		}

		for rows.Next() {
			var tenantID string
			var tenantHeap int64
			if err := rows.Scan(&tenantID, &tenantHeap); err != nil {
				continue
			}

			propIndex := int64(0)
			if totalTableHeap > 0 && totalTableIndex > 0 {
				propIndex = int64(float64(totalTableIndex) * (float64(tenantHeap) / float64(totalTableHeap)))
			}

			mu.Lock()
			if _, exists := tenantMap[tenantID]; !exists {
				tenantMap[tenantID] = &TenantAggregate{}
			}
			tenantMap[tenantID].HeapBytes += tenantHeap
			tenantMap[tenantID].IndexBytes += propIndex
			tenantMap[tenantID].TablesSeen++
			mu.Unlock()
		}
		rows.Close()
	}

	var snapshots []*model.GroupSnapshot
	for tenantID, agg := range tenantMap {
		totalBytes := agg.HeapBytes + agg.IndexBytes
		usagePct := 0.0
		if quotaBytes > 0 {
			usagePct = (float64(totalBytes) / float64(quotaBytes)) * 100.0
		}

		snapshots = append(snapshots, &model.GroupSnapshot{
			GroupID:         tenantID,
			GroupType:       model.GroupTypeRowLevel,
			HeapBytes:       agg.HeapBytes,
			IndexBytes:      agg.IndexBytes,
			TotalBytes:      totalBytes,
			QuotaBytes:      quotaBytes,
			UsagePercentage: usagePct,
			Status:          calculateStatus(usagePct),
			TablesCount:     agg.TablesSeen,
			CapturedAt:      time.Now(),
		})
	}

	return snapshots, nil
}

func (ins *Inspector) RunInspection(ctx context.Context) ([]*model.GroupSnapshot, error) {
	mode := ins.cfg.Strategy.Mode
	quota := ins.cfg.Strategy.DefaultQuotaBytes

	switch mode {
	case "database":
		var dbName string
		_ = ins.pool.QueryRow(ctx, `SELECT current_database()`).Scan(&dbName)
		snap, err := ins.InspectDatabase(ctx, dbName, quota)
		if err != nil {
			return nil, err
		}
		return []*model.GroupSnapshot{snap}, nil

	case "custom_group":
		var snapshots []*model.GroupSnapshot
		for _, grp := range ins.cfg.Strategy.Groups {
			snap, err := ins.InspectCustomGroup(ctx, grp, quota)
			if err != nil {
				continue
			}
			snapshots = append(snapshots, snap)
		}
		return snapshots, nil

	case "row_level":
		targetTables := ins.cfg.Strategy.Tables
		if len(targetTables) == 0 {
			var err error
			targetTables, err = ins.FindTablesWithColumn(ctx, ins.cfg.Strategy.TenantColumn)
			if err != nil {
				return nil, err
			}
		}
		return ins.InspectRowLevelTenants(ctx, ins.cfg.Strategy.TenantColumn, targetTables, quota)

	case "schema":
		fallthrough
	default:
		schemas, err := ins.ListTargetSchemas(ctx, ins.cfg.Strategy.SchemaPattern)
		if err != nil {
			return nil, err
		}

		concurrency := ins.cfg.Engine.Concurrency
		if concurrency <= 0 {
			concurrency = 4
		}

		schemaChan := make(chan string, len(schemas))
		for _, s := range schemas {
			schemaChan <- s
		}
		close(schemaChan)

		var wg sync.WaitGroup
		var mu sync.Mutex
		var snapshots []*model.GroupSnapshot

		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for s := range schemaChan {
					snap, err := ins.InspectSchema(ctx, s, quota)
					if err == nil && snap != nil {
						mu.Lock()
						snapshots = append(snapshots, snap)
						mu.Unlock()
					}
				}
			}()
		}
		wg.Wait()
		return snapshots, nil
	}
}

func calculateStatus(usagePct float64) model.QuotaStatus {
	if usagePct >= 100.0 {
		return model.StatusLimitExceeded
	} else if usagePct >= 95.0 {
		return model.StatusWarning95
	} else if usagePct >= 80.0 {
		return model.StatusWarning80
	}
	return model.StatusActive
}
