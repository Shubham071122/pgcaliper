package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"pgcaliper/internal/inspector"
	"pgcaliper/internal/model"
)

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func getStatusBadge(status model.QuotaStatus) string {
	switch status {
	case model.StatusActive:
		return "🟢 OK"
	case model.StatusWarning80:
		return "🟡 WARN (80%)"
	case model.StatusWarning95:
		return "🟠 CRITICAL (95%)"
	case model.StatusLimitExceeded:
		return "🔴 EXCEEDED (100%+)"
	default:
		return string(status)
	}
}

func main() {
	dbURL := flag.String("db", "", "PostgreSQL Connection URL (or via DATABASE_URL env)")
	limitGB := flag.Float64("limit-gb", 15.0, "Storage quota limit per tenant in Gigabytes (default: 15 GB)")
	schemaPrefix := flag.String("prefix", "", "Filter schemas by prefix (e.g., 'tenant_')")
	concurrency := flag.Int("concurrency", 4, "Number of concurrent worker goroutines")
	flag.Parse()

	connString := *dbURL
	if connString == "" {
		connString = os.Getenv("DATABASE_URL")
	}

	if connString == "" {
		fmt.Println("╔════════════════════════════════════════════════════════════════╗")
		fmt.Println("║               pgcaliper - PostgreSQL Quota Engine             ║")
		fmt.Println("╚════════════════════════════════════════════════════════════════╝")
		fmt.Println("\nUsage:")
		fmt.Println("  pgcaliper --db=\"postgres://user:password@localhost:5432/erp_db\" [options]")
		fmt.Println("\nOptions:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fmt.Println("🔍 Connecting to PostgreSQL...")
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		fmt.Printf("❌ Invalid database URL: %v\n", err)
		os.Exit(1)
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		fmt.Printf("❌ Failed to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		fmt.Printf("❌ Database ping failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✅ Connected successfully.")

	ins := inspector.New(pool)

	// 1. Overall Database Size
	totalDbBytes, prettyDbSize, err := ins.GetDatabaseTotalSize(ctx)
	if err != nil {
		fmt.Printf("❌ Error fetching DB size: %v\n", err)
	} else {
		fmt.Printf("\n📦 Entire Database Footprint: %s (%d bytes)\n\n", prettyDbSize, totalDbBytes)
	}

	// 2. Discover Schemas
	schemas, err := ins.ListSchemas(ctx, *schemaPrefix)
	if err != nil {
		fmt.Printf("❌ Error discovering schemas: %v\n", err)
		os.Exit(1)
	}

	if len(schemas) == 0 {
		fmt.Println("⚠️  No user schemas found matching prefix.")
		return
	}

	limitBytes := int64(*limitGB * 1024 * 1024 * 1024)
	fmt.Printf("📊 Metering %d schemas (Quota Limit: %.2f GB per tenant, Concurrency: %d workers)...\n\n", len(schemas), *limitGB, *concurrency)

	start := time.Now()
	metrics, err := ins.InspectAllConcurrently(ctx, schemas, limitBytes, *concurrency)
	if err != nil {
		fmt.Printf("❌ Error during inspection: %v\n", err)
		os.Exit(1)
	}
	duration := time.Since(start)

	// 3. Render Output Table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', tabwriter.TabIndent)
	fmt.Fprintln(w, "SCHEMA\tDATA (HEAP+TOAST)\tINDEXES\tTOTAL SIZE\tUSAGE %\tSTATUS")
	fmt.Fprintln(w, "──────\t─────────────────\t───────\t──────────\t───────\t──────")

	for _, m := range metrics {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%.2f%%\t%s\n",
			m.SchemaName,
			formatBytes(m.DataBytes),
			formatBytes(m.IndexBytes),
			formatBytes(m.TotalBytes),
			m.UsagePercentage,
			getStatusBadge(m.Status),
		)
	}
	w.Flush()

	fmt.Printf("\n⚡ Scan completed in %s\n", duration)
}
