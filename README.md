# pgcaliper

<p align="center">
  <a href="https://github.com/Shubham071122/pgcaliper/releases"><img src="https://img.shields.io/github/v/release/Shubham071122/pgcaliper?style=flat-square&color=0070f3" alt="Latest Release"></a>
  <a href="https://github.com/Shubham071122/pgcaliper/releases"><img src="https://img.shields.io/github/downloads/Shubham071122/pgcaliper/total?style=flat-square&logo=github&color=0070f3&label=Downloads" alt="Downloads"></a>
  <a href="https://golang.org"><img src="https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat-square&logo=go" alt="Go Version"></a>
  <a href="https://www.postgresql.org/"><img src="https://img.shields.io/badge/PostgreSQL-12+-336791?style=flat-square&logo=postgresql&logoColor=white" alt="PostgreSQL 12+"></a>
  <a href="https://github.com/Shubham071122/pgcaliper/blob/master/LICENSE"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg?style=flat-square" alt="License"></a>
</p>

<p align="center">
  <b>Enterprise-grade, non-intrusive PostgreSQL storage metering, telemetry, and multi-tenant quota calculation engine written in Go.</b>
</p>

---

## Why pgcaliper?

In multi-tenant SaaS platforms, shared databases, and enterprise applications, managing storage quotas (e.g., enforcing a 5 GB or 15 GB tier limit) is notoriously difficult:

- **OLTP Blocking:** Running live `SUM(pg_column_size)` queries inside application request loops spikes CPU and locks active transactions.
- **Hidden Index & TOAST Overhead:** B-Tree/GIN indexes and compressed out-of-line TOAST data often consume **40% to 70% of total database footprint**, yet standard queries fail to account for them.
- **Architectural Fragmentation:** Different applications organize data differently—some use Schema-per-Tenant, some use Database-per-Tenant, and others use shared tables with `tenant_id` or `org_id` columns.

`pgcaliper` solves this by acting as an independent, lightweight telemetry engine. It safely queries PostgreSQL system catalogs with strict statement timeouts, isolates table heaps, TOAST pages, and indexes, computes precise quota utilization, and stores historical metrics in a dedicated `_pgcaliper` schema—with zero changes to your application code.

---

## Architecture

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                            PostgreSQL Server                                │
│                                                                             │
│  ┌───────────────────────┐  ┌───────────────────────┐  ┌─────────────────┐  │
│  │   tenant_acme_corp    │  │   tenant_globex       │  │  public / app   │  │
│  │   - invoices          │  │   - invoices          │  │  - users        │  │
│  │   - vouchers          │  │   - vouchers          │  │  - audit_logs   │  │
│  └───────────────────────┘  └───────────────────────┘  └─────────────────┘  │
│                                                                             │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │  _pgcaliper (Auto-managed Isolated Telemetry Schema)                  │  │
│  │  - snapshots: Historical usage totals, % capacity, quota status       │  │
│  │  - table_details: Granular per-table breakdown (Heap vs Indexes)      │  │
│  └───────────────────────────────────▲───────────────────────────────────┘  │
└──────────────────────────────────────┼──────────────────────────────────────┘
                                       │
                      Reads Catalogs & Records Telemetry
                                       │
┌──────────────────────────────────────┴──────────────────────────────────────┐
│                    pgcaliper Engine (Go Daemon / CLI)                       │
│                                                                             │
│  ┌──────────────────────┐  ┌──────────────────────┐  ┌───────────────────┐  │
│  │   Config Engine      │  │  Interval Scheduler  │  │ Multi-Tenant      │  │
│  │   (pgcaliper.yaml)   │  │  (Duration / Cron)   │  │ Metering Engine   │  │
│  └──────────────────────┘  └──────────────────────┘  └───────────────────┘  │
│                                                                             │
│  ┌──────────────────────┐  ┌──────────────────────┐  ┌───────────────────┐  │
│  │   Webhook Alerts     │  │  Retention Engine    │  │ Self-Updater      │  │
│  │   (Slack / Discord)  │  │  (Auto-Purge Old)    │  │ (pgcaliper upgrade│  │
│  └──────────────────────┘  └──────────────────────┘  └───────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Quick Start

### 1. Install

```bash
curl -sSL https://raw.githubusercontent.com/Shubham071122/pgcaliper/master/install.sh | sudo bash
```

### 2. Configure (Interactive Wizard)

```bash
pgcaliper init
```
*The wizard connects to PostgreSQL, tests latency, auto-discovers schemas/tables, and writes a secure `0600` (Owner-only) configuration.*

### 3. Run Storage Calibration

```bash
pgcaliper scan
```

---

## Installation Methods

### Option 1: Automated Shell Installer (Recommended)
Supported on Linux (x86_64, ARM64) and macOS (Intel, Apple Silicon):
```bash
curl -sSL https://raw.githubusercontent.com/Shubham071122/pgcaliper/master/install.sh | sudo bash
```

### Option 2: Pre-compiled GitHub Release
Download the standalone binary directly for your platform:
```bash
# Example for Linux x86_64
curl -L https://github.com/Shubham071122/pgcaliper/releases/latest/download/pgcaliper-linux-amd64 -o pgcaliper
chmod +x pgcaliper
sudo mv pgcaliper /usr/local/bin/
```

### Option 3: Go Install
```bash
go install github.com/Shubham071122/pgcaliper/cmd/pgcaliper@latest
```

### Option 4: Build From Source
```bash
git clone https://github.com/Shubham071122/pgcaliper.git
cd pgcaliper
go build -o bin/pgcaliper ./cmd/pgcaliper
```

---

## Measurement Strategies

`pgcaliper` supports four distinct multi-tenant topologies out of the box:

### Strategy 1: Row-Level Multi-Tenancy (`mode: row_level`)
For multi-tenant SaaS platforms sharing a single database schema where rows are partitioned by a column (e.g. `org_id` or `tenant_id`).

```yaml
version: "1"
database:
  url: "postgres://user:password@localhost:5432/myapp_production?sslmode=disable"
  safe_mode_read_replica: true
  statement_timeout: "10s"

schedule:
  interval: "1h"
  retention_days: 90

storage:
  schema: "_pgcaliper"

strategy:
  mode: "row_level"
  tenant_column: "org_id" # or tenant_id
  tables:
    - items
    - invoices
    - payments
    - ledgers
  default_quota_bytes: 5368709120 # 5.00 GB
```
> **Proportional Index Allocation:** `pgcaliper` computes each tenant's table heap contribution and proportionally allocates index storage (`Index_tenant = Index_table * (Heap_tenant / Heap_table)`).

---

### Strategy 2: Schema-per-Tenant (`mode: schema`)
For architectures where each tenant owns a dedicated PostgreSQL schema (`tenant_acme`, `org_101`).

```yaml
strategy:
  mode: "schema"
  schema_pattern: "tenant_.*|org_.*"
  default_quota_bytes: 10737418240 # 10.00 GB
```

---

### Strategy 3: Database Fleet (`mode: database`)
For isolated database-per-customer clusters.

```yaml
strategy:
  mode: "database"
  default_quota_bytes: 21474836480 # 20.00 GB
```

---

### Strategy 4: Custom Table Groups (`mode: custom_group`)
For grouping tables by domain or feature tier (e.g. Audit Logs vs Core Financial Data).

```yaml
strategy:
  mode: "custom_group"
  default_quota_bytes: 10737418240
  groups:
    - name: "accounting"
      tables: ["invoices", "vouchers", "payments"]
    - name: "compliance_logs"
      tables: ["audit_events", "activity_logs"]
```

---

## CLI Reference

| Command | Description |
| :--- | :--- |
| `pgcaliper init` | Launch interactive configuration wizard with live DB validation |
| `pgcaliper scan` | Run immediate calibration scan and record snapshot to DB |
| `pgcaliper daemon` | Start 24/7 background scheduler (Duration or Cron) |
| `pgcaliper status` | Display latest recorded metrics from the database ledger |
| `pgcaliper details --tenant=<id>` | Drill down into table-by-table heap vs index breakdown |
| `pgcaliper export [--format=json|csv]` | Export telemetry records to stdout or file (`--output=report.json`) |
| `pgcaliper upgrade` | Check for newer releases and self-update the binary |
| `pgcaliper reset` | Purge telemetry schema (`_pgcaliper`) while keeping configs & binary |
| `pgcaliper uninstall` | Interactive teardown (removes binary, config files, and DB schema) |

---

## Production Deployment (systemd)

To run `pgcaliper` as a continuous background daemon on Linux servers:

Create `/etc/systemd/system/pgcaliper.service`:
```ini
[Unit]
Description=pgcaliper PostgreSQL Storage Metering Daemon
After=network.target postgresql.service

[Service]
Type=simple
User=root
WorkingDirectory=/etc/pgcaliper
ExecStart=/usr/local/bin/pgcaliper daemon
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

Enable and start the service:
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now pgcaliper
```

---

## Automated Alerts & Webhooks

Receive real-time alerts when tenants cross capacity thresholds:

```yaml
alerts:
  enabled: true
  url: "https://hooks.slack.com/services/YOUR/WEBHOOK/URL"
  format: "slack" # "slack", "discord", "generic"
  on_events:
    - "WARN_80"      # Usage reaches 80% of quota
    - "CRITICAL_95"  # Usage reaches 95% of quota
    - "EXCEEDED"     # Quota breached
```

---

## Querying Telemetry in Your Application

Application backends can query the `_pgcaliper` schema directly for billing enforcement or tenant dashboards:

```sql
SELECT 
    group_id AS tenant_id,
    pg_size_pretty(total_bytes) AS total_storage_used,
    pg_size_pretty(heap_bytes) AS data_size,
    pg_size_pretty(index_bytes) AS index_size,
    usage_percentage,
    status,
    captured_at
FROM _pgcaliper.snapshots
WHERE group_id = '1'
ORDER BY captured_at DESC
LIMIT 1;
```

---

## Self-Updating

Upgrade to the latest release with a single command:

```bash
sudo pgcaliper upgrade
```

---

## License

This project is licensed under the Apache 2.0 License - see the [LICENSE](LICENSE) file for details.
