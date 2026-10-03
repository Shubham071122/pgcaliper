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
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"pgcaliper/internal/alert"
	"pgcaliper/internal/config"
	"pgcaliper/internal/daemon"
	"pgcaliper/internal/inspector"
	"pgcaliper/internal/model"
	"pgcaliper/internal/storage"
	"pgcaliper/internal/ui"
)

const Version = "1.0.0"

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
			fmt.Println(ui.Yellow("  ▲ No schemas or table groups matched the configured pattern."))
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

func runUninstall(cfg *config.Config) error {
	reader := bufio.NewReader(os.Stdin)
	ui.PrintBanner()
	fmt.Println(ui.Yellow("  ▲ WARNING: You are about to completely remove the pgcaliper schema from PostgreSQL."))
	fmt.Printf("  Target Schema to drop: %s\n\n", ui.Red(fmt.Sprintf("'%s'", cfg.Storage.Schema)))
	fmt.Print(ui.White("  Are you sure you want to drop all historical storage telemetry? (y/N): "))

	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))

	if input != "y" && input != "yes" {
		fmt.Println(ui.Gray("\n  ✖ Uninstallation cancelled."))
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	spinner := ui.StartSpinner(fmt.Sprintf("Dropping schema '%s' CASCADE from PostgreSQL...", cfg.Storage.Schema))
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

	spinner.Stop(fmt.Sprintf("Successfully dropped schema '%s'. Database is completely pristine.", cfg.Storage.Schema), true)
	return nil
}

func runInitInteractive() error {
	reader := bufio.NewReader(os.Stdin)
	ui.PrintBanner()

	fmt.Println(ui.White("  Welcome to the pgcaliper setup wizard!"))
	fmt.Println(ui.Gray("  This wizard configures storage limits, scan intervals, and measurement strategies.\n"))

	ui.PrintStep(1, "PostgreSQL Connection URL", "Enter standard connection string (credentials, host, port, db name)")
	defaultURL := "postgres://postgres:password123@localhost:5432/erp_enterprise_db?sslmode=disable"
	fmt.Printf("  URL [%s]:\n  %s ", ui.Gray(defaultURL), ui.Cyan(">"))
	dbURL, _ := reader.ReadString('\n')
	dbURL = strings.TrimSpace(dbURL)
	if dbURL == "" {
		dbURL = defaultURL
	}

	ui.PrintStep(2, "Measurement Strategy", "How is your database data organized?")
	fmt.Println("    " + ui.Cyan("[1]") + " " + ui.White("Multi-Tenant Schemas") + " " + ui.Gray("(tenant_*, org_*, company_*) - [Recommended]"))
	fmt.Println("    " + ui.Cyan("[2]") + " " + ui.White("Database Fleet") + " " + ui.Gray("(One distinct database per customer)"))
	fmt.Println("    " + ui.Cyan("[3]") + " " + ui.White("Custom Table Groups") + " " + ui.Gray("(Group tables by feature/domain: audit vs finance)"))
	fmt.Printf("  Choice [%s]:\n  %s ", ui.Gray("1"), ui.Cyan(">"))
	stratChoice, _ := reader.ReadString('\n')
	stratChoice = strings.TrimSpace(stratChoice)

	mode := "schema"
	schemaPattern := "tenant_.*|org_.*"

	if stratChoice == "2" {
		mode = "database"
		schemaPattern = ""
	} else if stratChoice == "3" {
		mode = "custom_group"
		schemaPattern = ""
	} else {
		fmt.Printf("\n  Enter Schema Regex Pattern [%s]:\n  %s ", ui.Gray("tenant_.*|org_.*"), ui.Cyan(">"))
		patternInput, _ := reader.ReadString('\n')
		patternInput = strings.TrimSpace(patternInput)
		if patternInput != "" {
			schemaPattern = patternInput
		}
	}

	ui.PrintStep(3, "Storage Quota Cap", "Set default limit in Gigabytes (GB) per tenant")
	fmt.Printf("  Limit in GB [%s]:\n  %s ", ui.Gray("15.0"), ui.Cyan(">"))
	quotaStr, _ := reader.ReadString('\n')
	quotaStr = strings.TrimSpace(quotaStr)
	quotaGB := 15.0
	if quotaStr != "" {
		if q, err := strconv.ParseFloat(quotaStr, 64); err == nil && q > 0 {
			quotaGB = q
		}
	}
	quotaBytes := int64(quotaGB * 1024 * 1024 * 1024)

	ui.PrintStep(4, "Scan Frequency", "How often should background calibration execute? (e.g., 15m, 1h, 24h)")
	fmt.Printf("  Interval [%s]:\n  %s ", ui.Gray("15m"), ui.Cyan(">"))
	intervalStr, _ := reader.ReadString('\n')
	intervalStr = strings.TrimSpace(intervalStr)
	if intervalStr == "" {
		intervalStr = "15m"
	}

	fmt.Println()
	testSpinner := ui.StartSpinner("Validating database connection...")
	testCfg := &config.Config{
		Database: config.DatabaseConfig{URL: dbURL, SafeModeReadReplica: true, StatementTimeout: "5s"},
	}
	testCtx, testCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer testCancel()

	pool, err := createPool(testCtx, testCfg)
	if err != nil {
		testSpinner.Stop(fmt.Sprintf("Connection failed: %v", err), false)
	} else {
		if pingErr := pool.Ping(testCtx); pingErr != nil {
			testSpinner.Stop(fmt.Sprintf("Database ping failed: %v", pingErr), false)
		} else {
			testSpinner.Stop("PostgreSQL connection verified successfully!", true)
		}
		pool.Close()
	}

	yamlContent := fmt.Sprintf(`version: "1"

database:
  url: "%s"
  safe_mode_read_replica: true
  statement_timeout: "10s"

schedule:
  interval: "%s"
  retention_days: 90

storage:
  schema: "_pgcaliper"

strategy:
  mode: "%s"
  schema_pattern: "%s"
  default_quota_bytes: %d # %.2f GB

alerts:
  enabled: false
  url: "https://hooks.slack.com/services/YOUR/WEBHOOK/URL"
  format: "slack"
  on_events: ["WARN_80", "CRITICAL_95", "EXCEEDED"]

engine:
  concurrency: 4
`, dbURL, intervalStr, mode, schemaPattern, quotaBytes, quotaGB)

	targetFile := "pgcaliper.yaml"
	if os.Geteuid() == 0 {
		_ = os.MkdirAll("/etc/pgcaliper", 0755)
		if _, err := os.Stat("/etc/pgcaliper"); err == nil {
			targetFile = "/etc/pgcaliper/pgcaliper.yaml"
		}
	}

	if err := os.WriteFile(targetFile, []byte(yamlContent), 0600); err != nil {
		targetFile = "pgcaliper.yaml"
		if err := os.WriteFile(targetFile, []byte(yamlContent), 0600); err != nil {
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
			fmt.Printf("pgcaliper version %s (darwin/arm64)\n", Version)
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
	case "uninstall":
		cfg, err := config.LoadConfig(*configPath)
		if err != nil {
			ui.PrintErrorWithHint(err)
			os.Exit(1)
		}
		if err := runUninstall(cfg); err != nil {
			ui.PrintErrorWithHint(err)
			os.Exit(1)
		}
	default:
		ui.PrintRootHelp()
	}
}
