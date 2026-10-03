package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"
	"pgcaliper/internal/alert"
	"pgcaliper/internal/config"
	"pgcaliper/internal/daemon"
	"pgcaliper/internal/inspector"
	"pgcaliper/internal/model"
	"pgcaliper/internal/storage"
	"pgcaliper/internal/ui"
)

const Version = "1.1.1"

func createPool(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.Database.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid database connection string: %w", err)
	}

	if cfg.Database.SafeModeReadReplica {
		poolConfig.ConnConfig.RuntimeParams["default_transaction_read_only"] = "off"
		if cfg.Database.StatementTimeout != "" {
			poolConfig.ConnConfig.RuntimeParams["statement_timeout"] = cfg.Database.StatementTimeout
		}
	}

	return pgxpool.NewWithConfig(ctx, poolConfig)
}

func exportSnapshots(snapshots []*model.GroupSnapshot, format string, outputPath string) error {
	var data []byte
	var err error

	if format == "json" {
		data, err = json.MarshalIndent(snapshots, "", "  ")
		if err != nil {
			return err
		}
	} else if format == "csv" {
		var b strings.Builder
		w := csv.NewWriter(&b)
		w.Write([]string{"group_id", "group_type", "heap_bytes", "index_bytes", "total_bytes", "quota_bytes", "usage_percentage", "status", "captured_at"})
		for _, s := range snapshots {
			w.Write([]string{
				s.GroupID,
				string(s.GroupType),
				strconv.FormatInt(s.HeapBytes, 10),
				strconv.FormatInt(s.IndexBytes, 10),
				strconv.FormatInt(s.TotalBytes, 10),
				strconv.FormatInt(s.QuotaBytes, 10),
				fmt.Sprintf("%.2f", s.UsagePercentage),
				string(s.Status),
				s.CapturedAt.Format(time.RFC3339),
			})
		}
		w.Flush()
		data = []byte(b.String())
	}

	if outputPath != "" {
		if err := os.WriteFile(outputPath, data, 0600); err != nil {
			return fmt.Errorf("failed writing export file %s: %w", outputPath, err)
		}
		fmt.Printf("%s Export written to %s\n", ui.Green("✓"), outputPath)
		return nil
	}
	fmt.Println(string(data))
	return nil
}

func runScan(cfg *config.Config, format string, output string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var spinner *ui.Spinner
	if format == "" {
		spinner = ui.StartSpinner("Connecting to PostgreSQL database...")
	}
	pool, err := createPool(ctx, cfg)
	if err != nil {
		if spinner != nil {
			spinner.Stop("Connection failed", false)
		}
		return err
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		if spinner != nil {
			spinner.Stop("Database ping failed", false)
		}
		return err
	}
	if spinner != nil {
		spinner.Stop("Connected to PostgreSQL successfully", true)
	}

	migrator := storage.NewMigrator(pool, cfg.Storage.Schema)
	if err := migrator.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}

	repo := storage.NewRepository(pool, cfg.Storage.Schema)
	ins := inspector.New(pool, cfg)
	dispatcher := alert.NewDispatcher(&cfg.Alerts)

	if format == "" {
		totalDbBytes, prettyDbSize, err := ins.GetDatabaseTotalSize(ctx)
		if err == nil {
			fmt.Printf("\n  %s %s: %s %s\n\n",
				ui.Cyan("›"),
				ui.White("Database Footprint"),
				ui.Green(prettyDbSize),
				ui.Gray(fmt.Sprintf("(%d bytes)", totalDbBytes)),
			)
		}
	}

	var scanSpinner *ui.Spinner
	if format == "" {
		scanSpinner = ui.StartSpinner(fmt.Sprintf("Measuring storage (Mode: %s, Concurrency: %d workers)...", cfg.Strategy.Mode, cfg.Engine.Concurrency))
	}
	start := time.Now()
	snapshots, err := ins.RunInspection(ctx)
	if err != nil {
		if scanSpinner != nil {
			scanSpinner.Stop("Inspection failed", false)
		}
		return err
	}
	duration := time.Since(start)
	if scanSpinner != nil {
		scanSpinner.Stop(fmt.Sprintf("Inspection completed in %s", duration), true)
		fmt.Println()
	}

	if len(snapshots) == 0 {
		if format == "" {
			fmt.Println(ui.Yellow("  ▲ No schemas, workspaces, or table groups matched the configured pattern."))
		}
		return nil
	}

	if err := repo.SaveSnapshotsBatch(ctx, snapshots); err != nil {
		fmt.Printf("▲ Failed to save snapshots batch: %v\n", err)
	}

	for _, s := range snapshots {
		if s.Status != model.StatusActive {
			go func(snap *model.GroupSnapshot) {
				_ = dispatcher.Dispatch(context.Background(), snap)
			}(s)
		}
	}

	if format == "json" || format == "csv" {
		return exportSnapshots(snapshots, format, output)
	}

	ui.RenderTable(snapshots)
	fmt.Printf("\n  %s Telemetry snapshots recorded in %s\n",
		ui.Cyan("›"),
		ui.Cyan(fmt.Sprintf("'%s.snapshots'", cfg.Storage.Schema)),
	)

	return nil
}

func runStatus(cfg *config.Config, format string, output string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var spinner *ui.Spinner
	if format == "" {
		spinner = ui.StartSpinner("Fetching latest telemetry records from PostgreSQL...")
	}
	pool, err := createPool(ctx, cfg)
	if err != nil {
		if spinner != nil {
			spinner.Stop("Connection failed", false)
		}
		return err
	}
	defer pool.Close()

	repo := storage.NewRepository(pool, cfg.Storage.Schema)
	snapshots, err := repo.GetLatestSnapshots(ctx)
	if err != nil {
		if spinner != nil {
			spinner.Stop("Failed fetching snapshots", false)
		}
		return err
	}
	if spinner != nil {
		spinner.Stop("Telemetry fetched successfully", true)
		fmt.Println()
	}

	if len(snapshots) == 0 {
		if format == "" {
			fmt.Printf("▲ No snapshot records found in schema '%s'. Run 'pgcaliper scan' first.\n", cfg.Storage.Schema)
		}
		return nil
	}

	var ptrs []*model.GroupSnapshot
	for i := range snapshots {
		ptrs = append(ptrs, &snapshots[i])
	}

	if format == "json" || format == "csv" {
		return exportSnapshots(ptrs, format, output)
	}

	fmt.Printf("  %s %s\n\n", ui.Cyan("›"), ui.White("Latest Storage State from Database Ledger:"))
	ui.RenderTable(ptrs)
	return nil
}

func runDetails(cfg *config.Config, tenantID string) error {
	if tenantID == "" {
		return fmt.Errorf("tenant ID is required. Example: pgcaliper details --tenant=tenant_acme")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	spinner := ui.StartSpinner(fmt.Sprintf("Loading table breakdown for '%s'...", tenantID))
	pool, err := createPool(ctx, cfg)
	if err != nil {
		spinner.Stop("Connection failed", false)
		return err
	}
	defer pool.Close()

	repo := storage.NewRepository(pool, cfg.Storage.Schema)
	tables, snap, err := repo.GetTableDetails(ctx, tenantID)
	if err != nil {
		spinner.Stop("Lookup failed", false)
		return err
	}
	spinner.Stop("Details retrieved", true)

	ui.RenderTableDetails(snap, tables)
	return nil
}

func runClearData(cfg *config.Config) error {
	reader := bufio.NewReader(os.Stdin)
	ui.PrintBanner()
	fmt.Println(ui.Yellow("  ▲ RESET/PURGE: This will drop the historical telemetry schema from PostgreSQL."))
	fmt.Printf("  Target Database Schema: %s\n", ui.Red(fmt.Sprintf("'%s'", cfg.Storage.Schema)))
	fmt.Println(ui.Gray("  (Your application tables, configs, and pgcaliper binary remain completely untouched)\n"))
	fmt.Print(ui.White("  Are you sure you want to purge telemetry history? (y/N): "))

	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))

	if input != "y" && input != "yes" {
		fmt.Println(ui.Gray("\n  ✖ Purge cancelled."))
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	spinner := ui.StartSpinner(fmt.Sprintf("Dropping schema '%s' from PostgreSQL...", cfg.Storage.Schema))
	pool, err := createPool(ctx, cfg)
	if err != nil {
		spinner.Stop("Connection failed", false)
		return err
	}
	defer pool.Close()

	migrator := storage.NewMigrator(pool, cfg.Storage.Schema)
	if err := migrator.DropSchema(ctx); err != nil {
		spinner.Stop("Drop failed", false)
		return err
	}

	spinner.Stop(fmt.Sprintf("Schema '%s' dropped successfully from database.", cfg.Storage.Schema), true)
	return nil
}

func runCompleteUninstall(configPath string) error {
	reader := bufio.NewReader(os.Stdin)
	ui.PrintBanner()
	fmt.Println(ui.Red("  ▲ COMPLETE UNINSTALL: This will completely remove pgcaliper from your server."))
	fmt.Println(ui.Gray("  Removes binary, configuration files, and database telemetry schema.\n"))

	fmt.Print(ui.White("  1. Drop '_pgcaliper' schema from PostgreSQL database? (Y/n): "))
	dbInput, _ := reader.ReadString('\n')
	dbDrop := strings.TrimSpace(strings.ToLower(dbInput)) != "n"

	if dbDrop {
		cfg, err := config.LoadConfig(configPath)
		if err == nil && cfg.Database.URL != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if pool, err := createPool(ctx, cfg); err == nil {
				migrator := storage.NewMigrator(pool, cfg.Storage.Schema)
				_ = migrator.DropSchema(ctx)
				pool.Close()
				fmt.Printf("  %s Dropped '%s' schema from PostgreSQL\n", ui.Green("✓"), cfg.Storage.Schema)
			}
			cancel()
		}
	}

	fmt.Print(ui.White("  2. Remove configuration files (/etc/pgcaliper, pgcaliper.yaml)? (Y/n): "))
	cfgInput, _ := reader.ReadString('\n')
	if strings.TrimSpace(strings.ToLower(cfgInput)) != "n" {
		_ = os.Remove("pgcaliper.yaml")
		_ = os.RemoveAll("/etc/pgcaliper")
		if home, err := os.UserHomeDir(); err == nil {
			_ = os.RemoveAll(home + "/.config/pgcaliper")
		}
		fmt.Printf("  %s Removed configuration files\n", ui.Green("✓"))
	}

	fmt.Print(ui.White("  3. Remove pgcaliper executable binary (/usr/local/bin/pgcaliper)? (Y/n): "))
	binInput, _ := reader.ReadString('\n')
	if strings.TrimSpace(strings.ToLower(binInput)) != "n" {
		execPath, err := os.Executable()
		if err == nil {
			_ = os.Remove(execPath)
		}
		_ = os.Remove("/usr/local/bin/pgcaliper")
		fmt.Printf("  %s Removed binary executable\n", ui.Green("✓"))
	}

	fmt.Printf("\n  %s pgcaliper has been completely uninstalled. Goodbye!\n\n", ui.Green("✓"))
	return nil
}

func isValidInterval(s string) bool {
	if s == "" {
		return false
	}
	if _, err := time.ParseDuration(s); err == nil {
		return true
	}
	if s == "@hourly" || s == "@daily" || s == "@weekly" || s == "@midnight" {
		return true
	}
	fields := strings.Fields(s)
	return len(fields) == 5
}

func runInitInteractive() error {
	reader := bufio.NewReader(os.Stdin)
	ui.PrintBanner()

	fmt.Println(ui.White("  Welcome to the pgcaliper setup wizard!"))
	fmt.Println(ui.Gray("  This wizard configures storage limits, scan intervals, and measurement strategies.\n"))

	var dbURL string
	var pool *pgxpool.Pool
	defaultURL := "postgres://postgres:password123@localhost:5432/erp_enterprise_db?sslmode=disable"

	for {
		ui.PrintStep(1, "PostgreSQL Connection URL", "Enter standard connection string (credentials, host, port, db name)")
		fmt.Printf("  URL [%s]:\n  %s ", ui.Gray(defaultURL), ui.Cyan(">"))
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "" {
			dbURL = defaultURL
			fmt.Printf("  %s %s\n", ui.Gray("› Selected default:"), ui.Cyan(defaultURL))
		} else {
			dbURL = input
		}

		testSpinner := ui.StartSpinner("Validating database connection...")
		testCfg := &config.Config{
			Database: config.DatabaseConfig{URL: dbURL, SafeModeReadReplica: true, StatementTimeout: "5s"},
		}
		testCtx, testCancel := context.WithTimeout(context.Background(), 5*time.Second)
		p, err := createPool(testCtx, testCfg)
		if err != nil {
			testCancel()
			testSpinner.Stop(fmt.Sprintf("Connection format invalid: %v", err), false)
			fmt.Println(ui.Red("  ✖ Please re-enter a valid PostgreSQL connection URL.\n"))
			continue
		}

		if pingErr := p.Ping(testCtx); pingErr != nil {
			testCancel()
			p.Close()
			testSpinner.Stop(fmt.Sprintf("Connection failed: %v", pingErr), false)
			fmt.Println(ui.Red("  ✖ Database is unreachable with provided credentials. Please try again.\n"))
			continue
		}
		testCancel()
		testSpinner.Stop("PostgreSQL connection verified successfully!", true)
		pool = p
		break
	}
	defer pool.Close()

	ins := inspector.New(pool, &config.Config{})

	fmt.Println()
	ui.PrintStep(2, "Measurement Strategy", "How is your database data organized?")
	fmt.Println("    " + ui.Cyan("[1]") + " " + ui.White("Multi-Tenant Schemas") + " " + ui.Gray("(tenant_*, org_*, company_*) - [Recommended for Schema-per-Tenant]"))
	fmt.Println("    " + ui.Cyan("[2]") + " " + ui.White("Database Fleet") + " " + ui.Gray("(One distinct database per customer)"))
	fmt.Println("    " + ui.Cyan("[3]") + " " + ui.White("Custom Table Groups") + " " + ui.Gray("(Group tables by feature/domain: audit vs finance)"))
	fmt.Println("    " + ui.Cyan("[4]") + " " + ui.White("Row-Level / Workspace Column") + " " + ui.Gray("(Single shared schema partitioned by workspace_id/tenant_id)"))

	var stratChoice string
	for {
		fmt.Printf("  Choice [%s]:\n  %s ", ui.Gray("1"), ui.Cyan(">"))
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "" {
			stratChoice = "1"
			fmt.Printf("  %s %s\n", ui.Gray("› Selected default:"), ui.Cyan("[1] Multi-Tenant Schemas"))
			break
		}
		if input == "1" || input == "2" || input == "3" || input == "4" {
			stratChoice = input
			break
		}
		fmt.Println(ui.Red("  ✖ Invalid choice. Please enter 1, 2, 3, or 4."))
	}

	mode := "schema"
	schemaPattern := ""
	tenantCol := ""
	var targetTables []string
	var customGroups []config.CustomGroup

	switch stratChoice {
	case "2":
		mode = "database"
		fmt.Println(ui.Green("  ✓ Database Fleet mode selected."))

	case "3":
		mode = "custom_group"
		for groupNum := 1; ; groupNum++ {
			fmt.Printf("\n  %s %s #%d:\n", ui.Cyan("›"), ui.White("Configure Group"), groupNum)

			var grpName string
			for {
				fmt.Printf("    • Group Name (e.g. accounting, logs):\n    %s ", ui.Cyan(">"))
				nameInput, _ := reader.ReadString('\n')
				grpName = strings.TrimSpace(nameInput)
				if grpName == "" {
					grpName = fmt.Sprintf("group_%d", groupNum)
					fmt.Printf("    %s %s\n", ui.Gray("› Selected default name:"), ui.Cyan(grpName))
					break
				}
				break
			}

			var validTables []string
			for {
				fmt.Printf("    • Enter table names (comma-separated, e.g. invoices, vouchers):\n    %s ", ui.Cyan(">"))
				tablesInput, _ := reader.ReadString('\n')
				tablesInput = strings.TrimSpace(tablesInput)

				if tablesInput == "" {
					fmt.Println(ui.Red("    ✖ At least one table name is required for a group."))
					continue
				}

				rawList := strings.Split(tablesInput, ",")
				for _, t := range rawList {
					tbl := strings.TrimSpace(t)
					if tbl != "" {
						validTables = append(validTables, tbl)
					}
				}

				if len(validTables) > 0 {
					break
				}
				fmt.Println(ui.Red("    ✖ No valid table names entered."))
			}

			customGroups = append(customGroups, config.CustomGroup{
				Name:   grpName,
				Tables: validTables,
			})
			fmt.Printf("    %s Added group '%s' with %d tables.\n", ui.Green("✓"), grpName, len(validTables))

			fmt.Printf("\n  Do you want to add another table group? (y/N):\n  %s ", ui.Cyan(">"))
			moreInput, _ := reader.ReadString('\n')
			moreInput = strings.TrimSpace(strings.ToLower(moreInput))
			if moreInput != "y" && moreInput != "yes" {
				break
			}
		}

	case "4":
		mode = "row_level"
		defaultCol := "workspace_id"
		for {
			fmt.Printf("\n  • Enter Tenant Column Name [%s]:\n  %s ", ui.Gray(defaultCol), ui.Cyan(">"))
			colInput, _ := reader.ReadString('\n')
			colInput = strings.TrimSpace(colInput)
			if colInput == "" {
				tenantCol = defaultCol
				fmt.Printf("  %s %s\n", ui.Gray("› Selected default:"), ui.Cyan(defaultCol))
				break
			} else {
				tenantCol = colInput
				break
			}
		}

		for {
			fmt.Printf("  • Auto-discover all tables containing '%s'? (Y/n):\n  %s ", tenantCol, ui.Cyan(">"))
			autoChoice, _ := reader.ReadString('\n')
			autoChoice = strings.TrimSpace(strings.ToLower(autoChoice))
			if autoChoice == "" {
				autoChoice = "y"
				fmt.Println(ui.Gray("  › Selected default: Auto-discover (Y)"))
			}

			if autoChoice == "n" || autoChoice == "no" {
				for {
					fmt.Printf("\n  • Enter table names manually (comma-separated, e.g. invoices, vouchers):\n  %s ", ui.Cyan(">"))
					manualTables, _ := reader.ReadString('\n')
					manualTables = strings.TrimSpace(manualTables)

					if manualTables == "" {
						fmt.Println(ui.Red("  ✖ Please enter at least one table name."))
						continue
					}

					var verified []string
					testCtx, testCancel := context.WithTimeout(context.Background(), 5*time.Second)
					for _, t := range strings.Split(manualTables, ",") {
						tbl := strings.TrimSpace(t)
						if tbl == "" {
							continue
						}

						hasCol, _ := ins.ValidateTableHasColumn(testCtx, tbl, tenantCol)
						if hasCol {
							verified = append(verified, tbl)
							fmt.Printf("    %s Table '%s' verified with column '%s'\n", ui.Green("✓"), tbl, tenantCol)
						} else {
							fmt.Printf("    %s Table '%s' does not contain column '%s' (skipped)\n", ui.Yellow("▲"), tbl, tenantCol)
						}
					}
					testCancel()

					if len(verified) == 0 {
						fmt.Println(ui.Red(fmt.Sprintf("  ✖ None of the entered tables contain column '%s'. Please re-enter.", tenantCol)))
						continue
					}
					targetTables = verified
					break
				}
				break
			} else if autoChoice == "y" || autoChoice == "yes" {
				discSpinner := ui.StartSpinner(fmt.Sprintf("Scanning database for tables with '%s'...", tenantCol))
				testCtx, testCancel := context.WithTimeout(context.Background(), 5*time.Second)
				foundTables, err := ins.FindTablesWithColumn(testCtx, tenantCol)
				testCancel()

				if err != nil || len(foundTables) == 0 {
					discSpinner.Stop(fmt.Sprintf("No tables found containing column '%s'", tenantCol), false)
					fmt.Printf("  %s Would you like to enter table names manually? (Y/n):\n  %s ", ui.Yellow("▲"), ui.Cyan(">"))
					retryInput, _ := reader.ReadString('\n')
					if strings.TrimSpace(strings.ToLower(retryInput)) == "n" {
						return fmt.Errorf("configuration aborted: no tables with tenant column '%s'", tenantCol)
					}
					continue
				} else {
					discSpinner.Stop(fmt.Sprintf("Discovered %d tables containing '%s':", len(foundTables), tenantCol), true)
					targetTables = foundTables
					for _, ft := range foundTables {
						fmt.Printf("    • %s\n", ui.Cyan(ft))
					}
					break
				}
			} else {
				fmt.Println(ui.Red("  ✖ Please enter 'y' for auto-discovery or 'n' for manual entry."))
			}
		}

	default:
		mode = "schema"
		defaultPat := "tenant_.*|org_.*"
		for {
			fmt.Printf("\n  Enter Schema Regex Pattern [%s]:\n  %s ", ui.Gray(defaultPat), ui.Cyan(">"))
			patternInput, _ := reader.ReadString('\n')
			patternInput = strings.TrimSpace(patternInput)
			if patternInput == "" {
				schemaPattern = defaultPat
				fmt.Printf("  %s %s\n", ui.Gray("› Selected default:"), ui.Cyan(defaultPat))
				break
			}
			if _, err := regexp.Compile(patternInput); err != nil {
				fmt.Println(ui.Red(fmt.Sprintf("  ✖ Invalid regular expression pattern: %v. Please try again.", err)))
				continue
			}
			schemaPattern = patternInput
			break
		}
	}

	fmt.Println()
	ui.PrintStep(3, "Storage Quota Cap", "Set default limit in Gigabytes (GB) per tenant / group")
	defaultQuotaGB := 15.0
	var quotaBytes int64

	for {
		fmt.Printf("  Limit in GB [%s]:\n  %s ", ui.Gray("15.0"), ui.Cyan(">"))
		quotaStr, _ := reader.ReadString('\n')
		quotaStr = strings.TrimSpace(quotaStr)
		if quotaStr == "" {
			quotaBytes = int64(defaultQuotaGB * 1024 * 1024 * 1024)
			fmt.Printf("  %s %s\n", ui.Gray("› Selected default:"), ui.Cyan("15.0 GB"))
			break
		}

		q, err := strconv.ParseFloat(quotaStr, 64)
		if err != nil || q <= 0 {
			fmt.Println(ui.Red("  ✖ Invalid number. Please enter a positive number in GB (e.g. 15 or 50)."))
			continue
		}
		quotaBytes = int64(q * 1024 * 1024 * 1024)
		break
	}

	fmt.Println()
	ui.PrintStep(4, "Scan Frequency", "How often should background calibration execute? (e.g., 15m, 1h, 24h, @daily)")
	defaultInterval := "15m"
	var intervalStr string

	for {
		fmt.Printf("  Interval [%s]:\n  %s ", ui.Gray(defaultInterval), ui.Cyan(">"))
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "" {
			intervalStr = defaultInterval
			fmt.Printf("  %s %s\n", ui.Gray("› Selected default:"), ui.Cyan(defaultInterval))
			break
		}

		if !isValidInterval(input) {
			fmt.Println(ui.Red("  ✖ Invalid interval. Valid examples: 15m, 1h, 24h, @daily, '0 * * * *'"))
			continue
		}
		intervalStr = input
		break
	}

	cfgOut := config.Config{
		Version: "1",
		Database: config.DatabaseConfig{
			URL:                 dbURL,
			SafeModeReadReplica: true,
			StatementTimeout:    "10s",
		},
		Schedule: config.ScheduleConfig{
			Interval:      intervalStr,
			RetentionDays: 90,
		},
		Storage: config.StorageConfig{
			Schema: "_pgcaliper",
		},
		Strategy: config.StrategyConfig{
			Mode:              mode,
			SchemaPattern:     schemaPattern,
			TenantColumn:      tenantCol,
			Tables:            targetTables,
			DefaultQuotaBytes: quotaBytes,
			Groups:            customGroups,
		},
		Alerts: alert.WebhookConfig{
			Enabled:  false,
			URL:      "https://hooks.slack.com/services/YOUR/WEBHOOK/URL",
			Format:   "slack",
			OnEvents: []string{"WARN_80", "CRITICAL_95", "EXCEEDED"},
		},
		Engine: config.EngineConfig{
			Concurrency: 4,
		},
	}

	yamlBytes, err := yaml.Marshal(&cfgOut)
	if err != nil {
		return fmt.Errorf("failed serializing config: %w", err)
	}

	targetFile := "pgcaliper.yaml"
	if os.Geteuid() == 0 {
		_ = os.MkdirAll("/etc/pgcaliper", 0755)
		if _, err := os.Stat("/etc/pgcaliper"); err == nil {
			targetFile = "/etc/pgcaliper/pgcaliper.yaml"
		}
	}

	if err := os.WriteFile(targetFile, yamlBytes, 0600); err != nil {
		targetFile = "pgcaliper.yaml"
		if err := os.WriteFile(targetFile, yamlBytes, 0600); err != nil {
			return fmt.Errorf("failed writing configuration file: %w", err)
		}
	}

	absPath, _ := filepath.Abs(targetFile)
	fmt.Printf("\n  %s Generated secure configuration (%s) at %s\n", ui.Green("✓"), ui.Yellow("chmod 600 - Owner Only"), ui.Cyan(fmt.Sprintf("'%s'", absPath)))
	fmt.Printf("  %s Run %s to execute your first storage calibration.\n\n", ui.Cyan("›"), ui.Green("pgcaliper scan"))
	return nil
}

func main() {
	var command string
	var subArgs []string

	for i, arg := range os.Args[1:] {
		if !strings.HasPrefix(arg, "-") && command == "" {
			command = arg
			subArgs = os.Args[i+2:]
			break
		}
	}

	for _, arg := range os.Args[1:] {
		if arg == "-v" || arg == "--version" || arg == "version" {
			fmt.Printf("pgcaliper version %s (%s/%s)\n", Version, runtime.GOOS, runtime.GOARCH)
			return
		}
		if (arg == "-h" || arg == "--help") && command == "" {
			ui.PrintRootHelp()
			return
		}
	}

	if command == "" {
		if len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "-") {
			command = "scan"
		} else {
			ui.PrintRootHelp()
			return
		}
	}

	if command == "help" {
		if len(subArgs) > 0 {
			ui.PrintCommandHelp(subArgs[0])
		} else {
			ui.PrintRootHelp()
		}
		return
	}

	for _, arg := range os.Args[1:] {
		if arg == "-h" || arg == "--help" {
			ui.PrintCommandHelp(command)
			return
		}
	}

	fs := flag.NewFlagSet("pgcaliper", flag.ExitOnError)
	configPath := fs.String("config", "pgcaliper.yaml", "Path to pgcaliper.yaml configuration file")
	format := fs.String("format", "", "Output format (json, csv)")
	output := fs.String("output", "", "Output file path (optional)")
	tenantFlag := fs.String("tenant", "", "Target tenant for details command")
	_ = fs.Parse(os.Args[1:])

	var tenantTarget string
	for i := 1; i < len(os.Args); i++ {
		if strings.HasPrefix(os.Args[i], "--format=") {
			*format = strings.TrimPrefix(os.Args[i], "--format=")
		} else if os.Args[i] == "--format" && i+1 < len(os.Args) {
			*format = os.Args[i+1]
		}
		if strings.HasPrefix(os.Args[i], "--output=") {
			*output = strings.TrimPrefix(os.Args[i], "--output=")
		} else if os.Args[i] == "--output" && i+1 < len(os.Args) {
			*output = os.Args[i+1]
		}
		if strings.HasPrefix(os.Args[i], "--config=") {
			*configPath = strings.TrimPrefix(os.Args[i], "--config=")
		} else if os.Args[i] == "--config" && i+1 < len(os.Args) {
			*configPath = os.Args[i+1]
		}
		if strings.HasPrefix(os.Args[i], "--tenant=") {
			*tenantFlag = strings.TrimPrefix(os.Args[i], "--tenant=")
		} else if os.Args[i] == "--tenant" && i+1 < len(os.Args) {
			*tenantFlag = os.Args[i+1]
		}
	}

	if *tenantFlag != "" {
		tenantTarget = *tenantFlag
	} else if len(subArgs) > 0 && !strings.HasPrefix(subArgs[0], "-") {
		tenantTarget = subArgs[0]
	}

	switch command {
	case "init":
		if err := runInitInteractive(); err != nil {
			ui.PrintErrorWithHint(err)
			os.Exit(1)
		}
	case "scan":
		cfg, err := config.LoadConfig(*configPath)
		if err != nil {
			ui.PrintErrorWithHint(err)
			os.Exit(1)
		}
		if err := runScan(cfg, *format, *output); err != nil {
			ui.PrintErrorWithHint(err)
			os.Exit(1)
		}
	case "daemon":
		cfg, err := config.LoadConfig(*configPath)
		if err != nil {
			ui.PrintErrorWithHint(err)
			os.Exit(1)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		pool, err := createPool(ctx, cfg)
		cancel()
		if err != nil {
			ui.PrintErrorWithHint(err)
			os.Exit(1)
		}
		defer pool.Close()

		d := daemon.New(cfg, pool)
		if err := d.Start(); err != nil {
			ui.PrintErrorWithHint(err)
			os.Exit(1)
		}
	case "status":
		cfg, err := config.LoadConfig(*configPath)
		if err != nil {
			ui.PrintErrorWithHint(err)
			os.Exit(1)
		}
		if err := runStatus(cfg, *format, *output); err != nil {
			ui.PrintErrorWithHint(err)
			os.Exit(1)
		}
	case "details":
		cfg, err := config.LoadConfig(*configPath)
		if err != nil {
			ui.PrintErrorWithHint(err)
			os.Exit(1)
		}
		if err := runDetails(cfg, tenantTarget); err != nil {
			ui.PrintErrorWithHint(err)
			os.Exit(1)
		}
	case "export":
		cfg, err := config.LoadConfig(*configPath)
		if err != nil {
			ui.PrintErrorWithHint(err)
			os.Exit(1)
		}
		expFormat := *format
		if expFormat == "" {
			expFormat = "json"
		}
		if err := runStatus(cfg, expFormat, *output); err != nil {
			ui.PrintErrorWithHint(err)
			os.Exit(1)
		}
	case "reset", "clear", "purge":
		cfg, err := config.LoadConfig(*configPath)
		if err != nil {
			ui.PrintErrorWithHint(err)
			os.Exit(1)
		}
		if err := runClearData(cfg); err != nil {
			ui.PrintErrorWithHint(err)
			os.Exit(1)
		}
	case "uninstall":
		if err := runCompleteUninstall(*configPath); err != nil {
			ui.PrintErrorWithHint(err)
			os.Exit(1)
		}
	default:
		ui.PrintRootHelp()
	}
}
