# 📐 pgcaliper

> Ultra-lightweight, concurrent PostgreSQL storage metering and multi-tenant quota calculation engine in Go.

`pgcaliper` inspects low-level PostgreSQL table heaps, TOAST pages, and indexes to calculate exact per-tenant storage footprints and enforce quotas (e.g., 15GB free tier).

---

## 🚀 Features

- **Granular Storage Calculation**: Separates Heap Data, TOAST (compressed JSONB/text), and B-Tree Indexes.
- **Concurrent Inspection**: Uses Go worker pools and goroutines to scan multiple tenant schemas in parallel with minimal database lock overhead.
- **Quota Lifecycles**: Evaluates usage against configured limits (80% Soft Warning, 95% Critical, 100%+ Quota Exceeded).
- **Zero Heavy Dependencies**: Pure Go with high-performance `jackc/pgx/v5`.

---

## 📦 Quick Start

### 1. Build
```bash
go build -o bin/pgcaliper ./cmd/pgcaliper
```

### 2. Run a Storage Scan
```bash
./bin/pgcaliper --db="postgres://user:password@localhost:5432/my_erp_db" --limit-gb=15.0
```

### 3. Filter by Schema Prefix
```bash
./bin/pgcaliper --db="postgres://user:password@localhost:5432/my_erp_db" --prefix="tenant_" --limit-gb=15.0
```

---

## 🗄️ Project Structure

```
pgcaliper/
├── cmd/
│   └── pgcaliper/
│       └── main.go       # CLI & Daemon entrypoint
├── internal/
│   ├── inspector/
│   │   └── inspector.go  # Low-level PostgreSQL catalog inspection & worker pool
│   └── model/
│       └── model.go      # Quota thresholds, tenant metrics, and table breakdowns
├── go.mod
└── go.sum
```
