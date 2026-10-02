package ui

import (
	"fmt"
)

func PrintRootHelp() {
	PrintBanner()

	fmt.Println(White("USAGE:"))
	fmt.Printf("  %s %s %s\n\n", Cyan("pgcaliper"), Yellow("[command]"), Gray("[flags]"))

	fmt.Println(White("DESCRIPTION:"))
	fmt.Println("  Enterprise-grade, ultra-lightweight PostgreSQL storage metering, telemetry,")
	fmt.Println("  and multi-tenant quota engine. Inspects table heaps, TOAST pages, and secondary")
	fmt.Println("  indexes without locking or impacting active OLTP transactions.")
	fmt.Println()

	fmt.Println(White("CORE COMMANDS:"))
	fmt.Printf("  %s %s\n", PadRight(Yellow("init"), 16), "Launch interactive setup wizard to generate 'pgcaliper.yaml'")
	fmt.Printf("  %s %s\n", PadRight(Yellow("scan"), 16), "Execute an immediate storage calibration & save snapshots to DB")
	fmt.Printf("  %s %s\n", PadRight(Yellow("daemon"), 16), "Start 24/7 background scheduler (Duration/Cron + Auto-Retention)")
	fmt.Printf("  %s %s\n", PadRight(Yellow("status"), 16), "Display latest recorded tenant metrics from database ledger")
	fmt.Printf("  %s %s\n", PadRight(Yellow("details"), 16), "Drill down into granular per-table data vs index breakdown")
	fmt.Printf("  %s %s\n", PadRight(Yellow("export"), 16), "Export telemetry records to stdout or file (JSON / CSV)")
	fmt.Printf("  %s %s\n", PadRight(Yellow("uninstall"), 16), "Clean zero-trace teardown of '_pgcaliper' schema from database")
	fmt.Printf("  %s %s\n", PadRight(Yellow("help"), 16), "Show help and usage examples for any command")
	fmt.Println()

	fmt.Println(White("GLOBAL FLAGS:"))
	fmt.Printf("  %s %s\n", PadRight(Cyan("-c, --config string"), 26), "Path to configuration file "+Gray("(default: pgcaliper.yaml)"))
	fmt.Printf("  %s %s\n", PadRight(Cyan("-h, --help"), 26), "Show help documentation for pgcaliper")
	fmt.Printf("  %s %s\n", PadRight(Cyan("-v, --version"), 26), "Display version information")
	fmt.Println()

	fmt.Println(White("EXAMPLES:"))
	fmt.Printf("  %s  %s\n", Gray("# 1. Run interactive configuration wizard"), Cyan("pgcaliper init"))
	fmt.Printf("  %s  %s\n", Gray("# 2. Execute an immediate storage scan"), Cyan("pgcaliper scan --config=pgcaliper.yaml"))
	fmt.Printf("  %s  %s\n", Gray("# 3. Inspect table breakdown for a tenant"), Cyan("pgcaliper details --tenant=tenant_acme"))
	fmt.Printf("  %s  %s\n", Gray("# 4. Export tenant storage records to JSON"), Cyan("pgcaliper export --format=json --output=report.json"))
	fmt.Printf("  %s  %s\n", Gray("# 5. Run 24/7 daemon background scheduler"), Cyan("pgcaliper daemon"))
	fmt.Printf("  %s  %s\n", Gray("# 6. View detailed help for a specific command"), Cyan("pgcaliper help details"))
	fmt.Println()

	fmt.Printf("Use %s for detailed information on a specific command.\n", Cyan("pgcaliper help <command>"))
}

func PrintCommandHelp(cmd string) {
	switch cmd {
	case "init":
		fmt.Printf("\n%s: %s\n\n", White("COMMAND"), Cyan("pgcaliper init"))
		fmt.Println(White("DESCRIPTION:"))
		fmt.Println("  Starts an interactive CLI wizard that asks for your PostgreSQL connection URL,")
		fmt.Println("  measurement strategy (Schemas vs Database Fleet vs Custom Groups), storage quota limit,")
		fmt.Println("  and scan intervals. Automatically tests the connection and writes 'pgcaliper.yaml'.")
		fmt.Println("\n" + White("USAGE:") + "\n  pgcaliper init\n")

	case "scan":
		fmt.Printf("\n%s: %s\n\n", White("COMMAND"), Cyan("pgcaliper scan"))
		fmt.Println(White("DESCRIPTION:"))
		fmt.Println("  Connects to PostgreSQL, measures table heap and secondary indexes across all configured")
		fmt.Println("  tenants in parallel, stores snapshot records into '_pgcaliper.snapshots', and renders a")
		fmt.Println("  formatted terminal table.")
		fmt.Println("\n" + White("USAGE:") + "\n  pgcaliper scan [flags]\n")
		fmt.Println(White("FLAGS:"))
		fmt.Printf("  %s %s\n", PadRight(Cyan("--config string"), 22), "Path to config file (default: pgcaliper.yaml)")
		fmt.Printf("  %s %s\n", PadRight(Cyan("--format string"), 22), "Output format: table, json, csv")
		fmt.Printf("  %s %s\n", PadRight(Cyan("--output string"), 22), "Write output to destination file path")
		fmt.Println("\n" + White("EXAMPLES:") + "\n  pgcaliper scan\n  pgcaliper scan --config=prod.yaml\n  pgcaliper scan --format=json --output=scan.json\n")

	case "daemon":
		fmt.Printf("\n%s: %s\n\n", White("COMMAND"), Cyan("pgcaliper daemon"))
		fmt.Println(White("DESCRIPTION:"))
		fmt.Println("  Runs as a long-lived 24/7 background scheduler. Executes calibration scans on the configured")
		fmt.Println("  interval (Duration like '15m' or Cron like '0 * * * *'), batch persists snapshots, auto-purges")
		fmt.Println("  historical records older than retention_days, and dispatches webhook alerts.")
		fmt.Println("\n" + White("USAGE:") + "\n  pgcaliper daemon [flags]\n")
		fmt.Println(White("FLAGS:"))
		fmt.Printf("  %s %s\n", PadRight(Cyan("--config string"), 22), "Path to config file (default: pgcaliper.yaml)")
		fmt.Println("\n" + White("EXAMPLES:") + "\n  pgcaliper daemon\n  pgcaliper daemon --config=/etc/pgcaliper/pgcaliper.yaml\n")

	case "status":
		fmt.Printf("\n%s: %s\n\n", White("COMMAND"), Cyan("pgcaliper status"))
		fmt.Println(White("DESCRIPTION:"))
		fmt.Println("  Fast zero-overhead lookup of the most recent recorded storage snapshots directly from")
		fmt.Println("  the database ledger ('_pgcaliper.snapshots') without triggering a new table scan.")
		fmt.Println("\n" + White("USAGE:") + "\n  pgcaliper status [flags]\n")
		fmt.Println(White("FLAGS:"))
		fmt.Printf("  %s %s\n", PadRight(Cyan("--format string"), 22), "Output format: table, json, csv")
		fmt.Printf("  %s %s\n", PadRight(Cyan("--output string"), 22), "Write output to file path")
		fmt.Println("\n" + White("EXAMPLES:") + "\n  pgcaliper status\n  pgcaliper status --format=json | jq .\n")

	case "details":
		fmt.Printf("\n%s: %s\n\n", White("COMMAND"), Cyan("pgcaliper details"))
		fmt.Println(White("DESCRIPTION:"))
		fmt.Println("  Drills down into a specific tenant or group, displaying table-by-table sizes, Heap vs TOAST,")
		fmt.Println("  secondary index sizes, and index percentage ratios to pinpoint database bloat.")
		fmt.Println("\n" + White("USAGE:") + "\n  pgcaliper details --tenant=<name> [flags]\n")
		fmt.Println(White("FLAGS:"))
		fmt.Printf("  %s %s\n", PadRight(Cyan("--tenant string"), 22), "Name of tenant/schema/group (e.g. tenant_acme)")
		fmt.Printf("  %s %s\n", PadRight(Cyan("--config string"), 22), "Path to config file (default: pgcaliper.yaml)")
		fmt.Println("\n" + White("EXAMPLES:") + "\n  pgcaliper details --tenant=tenant_acme\n  pgcaliper details tenant_globex\n")

	case "export":
		fmt.Printf("\n%s: %s\n\n", White("COMMAND"), Cyan("pgcaliper export"))
		fmt.Println(White("DESCRIPTION:"))
		fmt.Println("  Exports historical snapshot telemetry into JSON or CSV format for integration with")
		fmt.Println("  billing platforms (Stripe/Lago), data warehouses, or CI/CD pipelines.")
		fmt.Println("\n" + White("USAGE:") + "\n  pgcaliper export [flags]\n")
		fmt.Println(White("FLAGS:"))
		fmt.Printf("  %s %s\n", PadRight(Cyan("--format string"), 22), "Export format: json, csv (default: json)")
		fmt.Printf("  %s %s\n", PadRight(Cyan("--output string"), 22), "File path to save the export")
		fmt.Println("\n" + White("EXAMPLES:") + "\n  pgcaliper export --format=json --output=report.json\n  pgcaliper export --format=csv > data.csv\n")

	case "uninstall":
		fmt.Printf("\n%s: %s\n\n", White("COMMAND"), Cyan("pgcaliper uninstall"))
		fmt.Println(White("DESCRIPTION:"))
		fmt.Println("  Clean zero-trace uninstallation. Prompts for confirmation and drops the '_pgcaliper'")
		fmt.Println("  schema and all telemetry tables from PostgreSQL, leaving user tables completely untouched.")
		fmt.Println("\n" + White("USAGE:") + "\n  pgcaliper uninstall [flags]\n")
		fmt.Println(White("FLAGS:"))
		fmt.Printf("  %s %s\n", PadRight(Cyan("--config string"), 22), "Path to config file (default: pgcaliper.yaml)")
		fmt.Println("\n" + White("EXAMPLES:") + "\n  pgcaliper uninstall\n  pgcaliper uninstall --config=pgcaliper.yaml\n")

	default:
		PrintRootHelp()
	}
}
