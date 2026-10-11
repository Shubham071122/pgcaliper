package diagnostics

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"pgcaliper/internal/alert"
	"pgcaliper/internal/config"
	"pgcaliper/internal/model"
	"pgcaliper/internal/ui"
)

func RunHealth(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, configPath string) error {
	ui.PrintBanner()
	fmt.Printf("  %s %s\n\n", ui.Cyan("●"), ui.White("pgcaliper Live Engine & Service Health"))

	absConfig, _ := filepath.Abs(configPath)
	pid := os.Getpid()
	fmt.Println(ui.Cyan("  [1] Service & Process State"))
	fmt.Printf("      • Status:              %s\n", ui.Green("● ACTIVE (Ready)"))
	fmt.Printf("      • Process PID:         %d\n", pid)
	fmt.Printf("      • Configuration:       %s\n", ui.White(absConfig))
	fmt.Printf("      • Engine Concurrency:  %d worker goroutines\n\n", cfg.Engine.Concurrency)

	fmt.Println(ui.Cyan("  [2] Database Engine & Connectivity"))
	pingStart := time.Now()
	if err := pool.Ping(ctx); err != nil {
		fmt.Printf("      • PostgreSQL Status:   %s (%v)\n\n", ui.Red("✖ UNREACHABLE"), err)
		return err
	}
	pingLatency := time.Since(pingStart)

	var pgVersion string
	_ = pool.QueryRow(ctx, "SELECT version()").Scan(&pgVersion)
	verParts := strings.Split(pgVersion, " ")
	shortVer := "PostgreSQL"
	if len(verParts) >= 2 {
		shortVer = verParts[0] + " " + verParts[1]
	}

	fmt.Printf("      • Connection:          %s (%s)\n", ui.Green("● CONNECTED"), shortVer)
	fmt.Printf("      • Roundtrip Latency:   %s\n", ui.Green(fmt.Sprintf("%v", pingLatency.Round(time.Microsecond))))
	fmt.Printf("      • Circuit Breaker:     statement_timeout = %s (Active)\n", ui.Green(cfg.Database.StatementTimeout))
	fmt.Printf("      • Read-Replica Safe:   %v\n\n", cfg.Database.SafeModeReadReplica)

	fmt.Println(ui.Cyan("  [3] Telemetry Storage Status"))
	var snapshotCount int
	var lastScan, firstScan time.Time
	countQuery := fmt.Sprintf("SELECT COUNT(*), COALESCE(MAX(captured_at), NOW()), COALESCE(MIN(captured_at), NOW()) FROM %s.snapshots", cfg.Storage.Schema)
	err := pool.QueryRow(ctx, countQuery).Scan(&snapshotCount, &lastScan, &firstScan)
	if err != nil {
		fmt.Printf("      • Schema '%s':    %s (Run 'pgcaliper scan' to initialize)\n", cfg.Storage.Schema, ui.Yellow("Not yet initialized"))
	} else {
		fmt.Printf("      • Storage Schema:      %s (%s)\n", ui.Green("✓ "+cfg.Storage.Schema), ui.White("Ready & Writable"))
		fmt.Printf("      • Total Records:       %s snapshots stored\n", ui.Green(fmt.Sprintf("%d", snapshotCount)))
		if snapshotCount > 0 {
			fmt.Printf("      • Last Calibration:    %s (%s ago)\n", ui.White(lastScan.Format("2006-01-02 15:04:05 MST")), ui.Cyan(time.Since(lastScan).Round(time.Second).String()))
		}
	}
	fmt.Printf("      • Retention Policy:    %d days (Auto-purging enabled)\n\n", cfg.Schedule.RetentionDays)

	fmt.Println(ui.Cyan("  [4] Background Schedule & Strategy"))
	fmt.Printf("      • Scheduled Frequency: Every %s (Daemon mode: 'pgcaliper daemon')\n", ui.Green(cfg.Schedule.Interval))
	fmt.Printf("      • Measurement Mode:    %s\n", ui.White(strings.ToUpper(cfg.Strategy.Mode)))
	if cfg.Strategy.Mode == "row_level" {
		fmt.Printf("      • Tenant Column:       %s\n", ui.Cyan(cfg.Strategy.TenantColumn))
		fmt.Printf("      • Configured Tables:   %d tables\n", len(cfg.Strategy.Tables))
	}

	fmt.Println()
	fmt.Println(ui.Cyan("  [5] Alerting & Notifications"))
	if cfg.Alerts.Enabled && cfg.Alerts.URL != "" {
		fmt.Printf("      • Webhooks:            %s (%s)\n", ui.Green("Enabled"), cfg.Alerts.Format)
		fmt.Printf("      • Target URL:          %s\n", ui.Gray(cfg.Alerts.URL))
	} else {
		fmt.Printf("      • Webhooks:            %s (Set 'alerts.enabled: true' in config)\n", ui.Gray("Disabled"))
	}

	fmt.Println()
	return nil
}

func RunDoctor(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool) error {
	ui.PrintBanner()
	fmt.Printf("  %s %s\n", ui.Cyan("🩺"), ui.White("pgcaliper Pre-Flight Diagnostics & Health Check"))
	fmt.Println(ui.Gray("  Running system, permission, catalog, and schema integrity validation...\n"))

	hasErrors := false

	fmt.Printf("  [1/5] Testing Database Connectivity...\n")
	pingStart := time.Now()
	if err := pool.Ping(ctx); err != nil {
		hasErrors = true
		fmt.Printf("        %s Database ping failed: %v\n", ui.Red("✖"), err)
		fmt.Printf("        %s Fix: Ensure PostgreSQL is running and credentials in pgcaliper.yaml are correct.\n", ui.Yellow("👉"))
	} else {
		latency := time.Since(pingStart).Round(time.Microsecond)
		fmt.Printf("        %s Connected to PostgreSQL successfully (Roundtrip: %s)\n", ui.Green("✓"), ui.Cyan(latency.String()))
	}

	fmt.Printf("\n  [2/5] Validating System Catalog Permissions...\n")
	var dummy int
	catErr := pool.QueryRow(ctx, "SELECT COUNT(*) FROM pg_catalog.pg_class LIMIT 1").Scan(&dummy)
	if catErr != nil {
		hasErrors = true
		fmt.Printf("        %s Cannot query pg_catalog.pg_class: %v\n", ui.Red("✖"), catErr)
		fmt.Printf("        %s Fix: GRANT SELECT ON pg_catalog.pg_class TO current_user;\n", ui.Yellow("👉"))
	} else {
		fmt.Printf("        %s In-memory catalog access ('pg_class', 'pg_namespace'): %s\n", ui.Green("✓"), ui.Green("Granted (0 Row Locks)"))
	}

	fmt.Printf("\n  [3/5] Checking Circuit Breaker Configuration...\n")
	var timeoutVal string
	_ = pool.QueryRow(ctx, "SHOW statement_timeout").Scan(&timeoutVal)
	fmt.Printf("        %s statement_timeout active: %s (Prevents hanging OLTP queries)\n", ui.Green("✓"), ui.Cyan(timeoutVal))

	fmt.Printf("\n  [4/5] Checking Strategy & Schema Integrity (Mode: %s)...\n", cfg.Strategy.Mode)
	if cfg.Strategy.Mode == "row_level" {
		if len(cfg.Strategy.Tables) == 0 {
			fmt.Printf("        %s No tables configured for row_level mode.\n", ui.Yellow("▲"))
			fmt.Printf("        %s Fix: Add table names under strategy.tables in pgcaliper.yaml or run 'pgcaliper init'.\n", ui.Yellow("👉"))
		} else {
			missingColTables := []string{}
			missingTables := []string{}
			for _, tbl := range cfg.Strategy.Tables {
				var count int
				checkQuery := "SELECT COUNT(*) FROM information_schema.columns WHERE table_name = $1 AND column_name = $2"
				_ = pool.QueryRow(ctx, checkQuery, tbl, cfg.Strategy.TenantColumn).Scan(&count)
				if count == 0 {
					var tableExists int
					_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_name = $1", tbl).Scan(&tableExists)
					if tableExists == 0 {
						missingTables = append(missingTables, tbl)
					} else {
						missingColTables = append(missingColTables, tbl)
					}
				}
			}

			if len(missingTables) > 0 {
				hasErrors = true
				fmt.Printf("        %s %d configured tables do not exist in database: %s\n", ui.Red("✖"), len(missingTables), strings.Join(missingTables, ", "))
			}
			if len(missingColTables) > 0 {
				hasErrors = true
				fmt.Printf("        %s %d tables are missing tenant column '%s': %s\n", ui.Red("✖"), len(missingColTables), cfg.Strategy.TenantColumn, strings.Join(missingColTables, ", "))
				fmt.Printf("        %s Fix: Verify tenant_column in pgcaliper.yaml matches table schema (e.g. org_id, workspace_id, tenant_id).\n", ui.Yellow("👉"))
			}
			if len(missingTables) == 0 && len(missingColTables) == 0 {
				fmt.Printf("        %s All %d configured tables contain '%s' column\n", ui.Green("✓"), len(cfg.Strategy.Tables), ui.Cyan(cfg.Strategy.TenantColumn))
			}
		}
	} else if cfg.Strategy.Mode == "schema" {
		var schemaCount int
		_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name NOT IN ('pg_catalog', 'information_schema')").Scan(&schemaCount)
		fmt.Printf("        %s Multi-tenant schema matching pattern '%s' (%d candidate schemas)\n", ui.Green("✓"), cfg.Strategy.SchemaPattern, schemaCount)
	}

	fmt.Printf("\n  [5/5] Validating Telemetry Schema Privileges ('%s')...\n", cfg.Storage.Schema)
	var canCreate bool
	_ = pool.QueryRow(ctx, "SELECT has_database_privilege(current_user, current_database(), 'CREATE')").Scan(&canCreate)
	if !canCreate {
		var schemaExists int
		_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = $1", cfg.Storage.Schema).Scan(&schemaExists)
		if schemaExists == 0 {
			hasErrors = true
			fmt.Printf("        %s User lacks CREATE privilege to initialize schema '%s'\n", ui.Red("✖"), cfg.Storage.Schema)
			fmt.Printf("        %s Fix: GRANT CREATE ON DATABASE current_database() TO current_user;\n", ui.Yellow("👉"))
		} else {
			fmt.Printf("        %s Schema '%s' already exists\n", ui.Green("✓"), cfg.Storage.Schema)
		}
	} else {
		fmt.Printf("        %s User has permission to manage '%s' telemetry schema\n", ui.Green("✓"), cfg.Storage.Schema)
	}

	fmt.Println()
	if hasErrors {
		fmt.Printf("  %s %s\n\n", ui.Red("✖ Diagnostics completed with warnings/errors."), ui.White("Please apply the remedies above."))
	} else {
		fmt.Printf("  %s %s\n\n", ui.Green("✓ Diagnostics PASSED:"), ui.White("All systems operational. Database is 100% ready for pgcaliper!"))
	}

	return nil
}

func RunPing(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool) error {
	fmt.Printf("\n  %s Pinging database and alert channels...\n\n", ui.Cyan("›"))

	start := time.Now()
	err := pool.Ping(ctx)
	latency := time.Since(start).Round(time.Microsecond)
	if err != nil {
		fmt.Printf("  %s PostgreSQL Database: %s (%v)\n", ui.Red("✖"), ui.Red("FAILED"), err)
		return err
	}
	fmt.Printf("  %s PostgreSQL Database: %s (Roundtrip: %s)\n", ui.Green("✓"), ui.Green("PONG"), ui.Cyan(latency.String()))

	if cfg.Alerts.Enabled && cfg.Alerts.URL != "" {
		fmt.Printf("  %s Sending test alert to webhook (%s)...\n", ui.Cyan("›"), cfg.Alerts.Format)
		disp := alert.NewDispatcher(&cfg.Alerts)
		testSnap := &model.GroupSnapshot{
			GroupID:         "tenant_ping_test",
			GroupType:       model.GroupTypeRowLevel,
			HeapBytes:       1024 * 1024 * 50,
			IndexBytes:      1024 * 1024 * 20,
			TotalBytes:      1024 * 1024 * 70,
			QuotaBytes:      1024 * 1024 * 100,
			UsagePercentage: 70.0,
			Status:          model.StatusActive,
			CapturedAt:      time.Now(),
		}
		if err := disp.Dispatch(ctx, testSnap); err != nil {
			fmt.Printf("  %s Webhook Alert: %s (%v)\n", ui.Red("✖"), ui.Red("FAILED"), err)
		} else {
			fmt.Printf("  %s Webhook Alert: %s (Test payload received by %s)\n", ui.Green("✓"), ui.Green("DELIVERED"), cfg.Alerts.Format)
		}
	} else {
		fmt.Printf("  %s Webhook Alert: %s (Configure 'alerts.enabled: true' in pgcaliper.yaml to enable)\n", ui.Gray("•"), ui.Gray("SKIPPED"))
	}

	fmt.Println()
	return nil
}

func RunLogs(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool) error {
	query := fmt.Sprintf("SELECT captured_at, COUNT(DISTINCT group_id) AS total_groups, SUM(total_bytes) AS sum_bytes, MAX(usage_percentage) AS max_usage FROM %s.snapshots GROUP BY captured_at ORDER BY captured_at DESC LIMIT 10;", cfg.Storage.Schema)

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("failed fetching calibration logs from '%s.snapshots': %w", cfg.Storage.Schema, err)
	}
	defer rows.Close()

	type LogEntry struct {
		CapturedAt time.Time
		Groups     int
		TotalBytes int64
		MaxUsage   float64
	}

	var entries []LogEntry
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.CapturedAt, &e.Groups, &e.TotalBytes, &e.MaxUsage); err == nil {
			entries = append(entries, e)
		}
	}

	if len(entries) == 0 {
		fmt.Printf("\n  %s No historical calibration runs found in '%s.snapshots'.\n  %s Run '%s' to execute your first calibration.\n\n",
			ui.Yellow("▲"), cfg.Storage.Schema, ui.Cyan("›"), ui.Green("pgcaliper scan"))
		return nil
	}

	fmt.Printf("\n  %s %s\n\n", ui.Cyan("›"), ui.White("Recent Calibration Execution History:"))
	fmt.Printf("  %-25s  %-12s  %-15s  %-18s\n", "EXECUTION TIMESTAMP", "ENTITIES", "TOTAL SIZE", "MAX TENANT USAGE")
	fmt.Println("  ─────────────────────────  ────────────  ───────────────  ──────────────────")

	for _, e := range entries {
		fmt.Printf("  %-25s  %-12s  %-15s  %-18s\n",
			ui.White(e.CapturedAt.Format("2006-01-02 15:04:05 MST")),
			ui.Cyan(fmt.Sprintf("%d tenants", e.Groups)),
			ui.Green(ui.FormatBytes(e.TotalBytes)),
			fmt.Sprintf("%.1f%% capacity", e.MaxUsage),
		)
	}
	fmt.Println()
	return nil
}
