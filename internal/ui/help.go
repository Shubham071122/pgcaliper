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
	fmt.Printf("  %s %s\n", PadRight(Yellow("reset"), 16), "Purge historical telemetry schema ('_pgcaliper') from database")
	fmt.Printf("  %s %s\n", PadRight(Yellow("uninstall"), 16), "Completely remove pgcaliper binary, configs, and DB schema")
	fmt.Printf("  %s %s\n", PadRight(Yellow("help"), 16), "Show help and usage examples for any command")
	fmt.Println()

	fmt.Println(White("GLOBAL FLAGS:"))
	fmt.Printf("  %s %s\n", PadRight(Cyan("-c, --config string"), 26), "Path to configuration file "+Gray("(default: pgcaliper.yaml)"))
	fmt.Printf("  %s %s\n", PadRight(Cyan("-h, --help"), 26), "Show help documentation for pgcaliper")
	fmt.Printf("  %s %s\n", PadRight(Cyan("-v, --version"), 26), "Display version information")
	fmt.Println()

	fmt.Println(White("EXAMPLES:"))
	fmt.Printf("  %s  %s\n", Gray("# 1. Run interactive configuration wizard"), Cyan("pgcaliper init"))
	fmt.Printf("  %s  %s\n", Gray("# 2. Execute an immediate storage scan"), Cyan("pgcaliper scan"))
	fmt.Printf("  %s  %s\n", Gray("# 3. Inspect table breakdown for a tenant"), Cyan("pgcaliper details --tenant=tenant_acme"))
	fmt.Printf("  %s  %s\n", Gray("# 4. Export tenant storage records to JSON"), Cyan("pgcaliper export --format=json --output=report.json"))
	fmt.Printf("  %s  %s\n", Gray("# 5. Run 24/7 daemon background scheduler"), Cyan("pgcaliper daemon"))
	fmt.Printf("  %s  %s\n", Gray("# 6. Purge historical telemetry from DB"), Cyan("pgcaliper reset"))
	fmt.Println()

	fmt.Printf("Use %s for detailed information on a specific command.\n", Cyan("pgcaliper help <command>"))
}

func PrintCommandHelp(cmd string) {
	switch cmd {
	case "init":
		fmt.Printf("\n%s: %s\n\n", White("COMMAND"), Cyan("pgcaliper init"))
		fmt.Println(White("DESCRIPTION:"))
		fmt.Println("  Starts an interactive CLI wizard that asks for your PostgreSQL connection URL,")
		fmt.Println("  measurement strategy (Schemas vs Database Fleet vs Custom Groups vs Row-Level),")
		fmt.Println("  storage quota limits, and scan intervals. Automatically tests the connection and writes config.")
		fmt.Println("\n" + White("USAGE:") + "\n  pgcaliper init\n")

	case "scan":
		fmt.Printf("\n%s: %s\n\n", White("COMMAND"), Cyan("pgcaliper scan"))
		fmt.Println(White("DESCRIPTION:"))
		fmt.Println("  Performs a live storage calibration, displays an ANSI table with Heap vs Index")
		fmt.Println("  breakdown, % used, and quota status indicators, saving snapshots to PostgreSQL.")
		fmt.Println("\n" + White("USAGE:") + "\n  pgcaliper scan [--format=json|csv] [--output=file.json]\n")

	case "reset", "clear", "purge":
		fmt.Printf("\n%s: %s\n\n", White("COMMAND"), Cyan("pgcaliper reset"))
		fmt.Println(White("DESCRIPTION:"))
		fmt.Println("  Drops the '_pgcaliper' telemetry schema from PostgreSQL, removing all historical")
		fmt.Println("  snapshots. Keeps the CLI binary and configuration files intact.")
		fmt.Println("\n" + White("USAGE:") + "\n  pgcaliper reset\n")

	case "uninstall":
		fmt.Printf("\n%s: %s\n\n", White("COMMAND"), Cyan("pgcaliper uninstall"))
		fmt.Println(White("DESCRIPTION:"))
		fmt.Println("  Interactive uninstaller that completely cleans pgcaliper from the system:")
		fmt.Println("  - Drops database telemetry schema ('_pgcaliper')")
		fmt.Println("  - Removes configuration files (/etc/pgcaliper, pgcaliper.yaml)")
		fmt.Println("  - Deletes binary executable (/usr/local/bin/pgcaliper)")
		fmt.Println("\n" + White("USAGE:") + "\n  pgcaliper uninstall\n")

	default:
		PrintRootHelp()
	}
}
