# pgcaliper

> Enterprise-grade PostgreSQL storage metering, telemetry, and multi-tenant quota engine written in Go.

`pgcaliper` operates as an independent, non-intrusive database telemetry agent. It inspects low-level PostgreSQL table heaps, TOAST pages, and secondary indexes without locking rows or degrading active OLTP transactions, recording structured snapshot telemetry directly into a dedicated database ledger (`_pgcaliper`).

---

## Table of Contents
1. [Overview](#overview)
2. [Architecture](#architecture)
3. [Quick Start (3 Steps)](#quick-start-3-steps)
4. [Installation Options](#installation-options)
5. [Interactive Setup (`pgcaliper init`)](#interactive-setup-pgcaliper-init)
6. [CLI Commands Reference](#cli-commands-reference)
7. [Granular Table Breakdown (`pgcaliper details`)](#granular-table-breakdown-pgcaliper-details)
8. [Running 24/7 as a Background Daemon (`pgcaliper daemon`)](#running-247-as-a-background-daemon-pgcaliper-daemon)
9. [Measurement Strategies (`pgcaliper.yaml`)](#measurement-strategies-pgcaliperyaml)
10. [Configuration Security & Best Practices](#configuration-security--best-practices)
11. [Event Alerting & Webhooks](#event-alerting--webhooks)
12. [Application Integration Recipes](#application-integration-recipes)
13. [Uninstallation](#uninstallation)
14. [License](#license)

---

## Overview

In multi-tenant SaaS platforms, ERPs, and database clusters, managing storage quotas (e.g. enforcing a 15 GB tier limit) is typically challenging:

- **Running live `SUM(pg_column_size)` on API requests** causes CPU spikes and blocks active user transactions.
- **Row counts are misleading**: Secondary indexes (B-Tree, GIN) and out-of-line TOAST data (compressed JSONB, text) often consume **40% to 70% of total disk space**.
- **External billing tools** (such as Stripe or Lago) have no direct visibility into on-disk storage layout.

`pgcaliper` addresses this by running as a lightweight daemon or sidecar. It queries PostgreSQL's storage catalogs with strict statement timeouts, distinguishes Heap vs Index footprints, evaluates quota thresholds, and records historical snapshots with zero application coupling.

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
│  │  _pgcaliper (Auto-created Isolated Telemetry Schema)                  │  │
│  │  - snapshots: Historical usage totals, % used, quota status           │  │
│  │  - table_details: Per-table breakdown (Heap/TOAST vs Indexes)         │  │
│  └───────────────────────────────────▲───────────────────────────────────┘  │
└──────────────────────────────────────┼──────────────────────────────────────┘
                                       │
                      Reads & Records Snapshot Telemetry
                                       │
┌──────────────────────────────────────┴──────────────────────────────────────┐
│                    pgcaliper Engine (Go Daemon / CLI)                       │
│                                                                             │
│  ┌──────────────────────┐  ┌──────────────────────┐  ┌───────────────────┐  │
│  │   Config Engine      │  │  Interval Scheduler  │  │ High-Throughput   │  │
│  │   (pgcaliper.yaml)   │  │  (Duration or Cron)  │  │ Batch Persister   │  │
│  └──────────────────────┘  └──────────────────────┘  └───────────────────┘  │
│                                                                             │
│  ┌──────────────────────┐  ┌──────────────────────┐  ┌───────────────────┐  │
│  │   Webhook Dispatcher │  │  Retention Trimmer   │  │ ANSI-Aware UI     │  │
│  │   (Slack/Discord)    │  │  (Auto-Purges Old)   │  │ Layout Engine     │  │
│  └──────────────────────┘  └──────────────────────┘  └───────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Quick Start (3 Steps)

### 1. Install
```bash
curl -sSL https://raw.githubusercontent.com/Shubham071122/pgcaliper/master/install.sh | sudo bash
```

### 2. Configure (Interactive Wizard)
```bash
sudo pgcaliper init
```
*Validates your database connection live and automatically creates a secure configuration with `chmod 600` (Owner-only) permissions.*

### 3. Scan & View Metrics
```bash
sudo pgcaliper scan
```

---

## Installation Options

### Option 1: One-Line Installer (Linux / macOS)
```bash
curl -sSL https://raw.githubusercontent.com/Shubham071122/pgcaliper/master/install.sh | sudo bash
```

### Option 2: Pre-compiled Binary Release
Download the binary directly from GitHub Releases:
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

### Option 4: Build from Source (Go 1.22+)
```bash
git clone https://github.com/Shubham071122/pgcaliper.git
cd pgcaliper
go build -o bin/pgcaliper ./cmd/pgcaliper
```

---

## CLI Commands Reference

| Command | Description |
| :--- | :--- |
| `pgcaliper init` | Interactive setup wizard with live PostgreSQL connection validation |
| `pgcaliper scan` | Immediate storage calibration, batch snapshot persistence, and table output |
| `pgcaliper daemon` | 24/7 background scheduler (Duration or Cron + Retention Trimmer) |
| `pgcaliper status` | Fast lookup of latest tenant metrics directly from the DB ledger |
| `pgcaliper details --tenant=<name>` | Granular table-by-table breakdown with heap vs index ratios |
| `pgcaliper export [--format=json\|csv]` | Export telemetry records to stdout or file (`--output=report.json`) |
| `pgcaliper uninstall` | Clean zero-trace drop of `_pgcaliper` schema from database |
| `pgcaliper help [command]` | Display built-in help and command examples |

---

## Granular Table Breakdown (`pgcaliper details`)

Inspect which specific tables and secondary indexes are consuming a tenant's quota:

```bash
pgcaliper details --tenant=tenant_acme
```

**Example Output:**
```text
  › Granular Breakdown for Tenant: tenant_acme (25.42 MB total, 15.00 GB limit)
     • Capacity: [░░░░░░░░░░]   0.2% | Status: ● ACTIVE | Captured: 2026-10-02 12:40:04

TABLE NAME                    HEAP + TOAST         INDEXES      TOTAL SIZE         INDEX %
────────────────────────  ────────────────  ──────────────  ──────────────  ──────────────
invoices                          17.80 MB         5.94 MB        23.73 MB           25.0%
customers                        792.00 KB       936.00 KB         1.69 MB           54.2%
```

---

## Running 24/7 as a Background Daemon (`pgcaliper daemon`)

Supports standard durations (`15m`, `1h`, `24h`) and 5-part cron expressions (`0 * * * *`, `*/30 * * * *`):

```bash
pgcaliper daemon
```

### Production `systemd` Service (`/etc/systemd/system/pgcaliper.service`):
```ini
[Unit]
Description=pgcaliper PostgreSQL Storage Metering Daemon
After=network.target postgresql.service

[Service]
Type=simple
User=postgres
WorkingDirectory=/etc/pgcaliper
ExecStart=/usr/local/bin/pgcaliper daemon
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now pgcaliper
```

---

## Measurement Strategies (`pgcaliper.yaml`)

`pgcaliper` supports 3 measurement strategies to match various database topologies:

### Strategy 1: Multi-Tenant Schemas (SaaS & ERP)
```yaml
version: "1"

database:
  url: "${DATABASE_URL:-postgres://postgres:password123@localhost:5432/erp_db?sslmode=disable}"
  safe_mode_read_replica: true
  statement_timeout: "10s"

schedule:
  interval: "15m"      # "15m", "1h", "24h" or cron "0 * * * *"
  retention_days: 90   # Auto-purges snapshot records older than 90 days

storage:
  schema: "_pgcaliper" # Internal schema created inside PostgreSQL

strategy:
  mode: "schema"
  schema_pattern: "tenant_.*|org_.*"
  default_quota_bytes: 16106127360 # 15 GB

engine:
  concurrency: 4
```

### Strategy 2: Database Fleet (DB-per-Tenant)
```yaml
strategy:
  mode: "database"
  default_quota_bytes: 16106127360 # 15 GB per DB
```

### Strategy 3: Custom Domain Table Groups
```yaml
strategy:
  mode: "custom_group"
  default_quota_bytes: 16106127360
  groups:
    - name: "accounting"
      tables: ["invoices", "vouchers", "payments"]
    - name: "audit_and_logs"
      tables: ["audit_logs", "user_activity_*"]
```

---

## Configuration Security & Best Practices

1. **Automatic `0600` Permissions:** `pgcaliper init` automatically generates configuration files with `chmod 600` (Owner Read/Write Only) so other system users cannot view database credentials.
2. **Multi-Path Config Discovery:** `pgcaliper` commands automatically discover configuration files across standard Linux paths:
   - `./pgcaliper.yaml`
   - `/etc/pgcaliper/pgcaliper.yaml`
   - `~/.config/pgcaliper/pgcaliper.yaml`
3. **Environment Variable Interpolation:** Passwords can be passed via environment variables without hardcoding secrets:
   ```yaml
   database:
     url: "${DATABASE_URL}"
   ```

---

## Event Alerting & Webhooks

Configure real-time notifications for threshold transitions:

```yaml
alerts:
  enabled: true
  url: "https://hooks.slack.com/services/YOUR/WEBHOOK/URL"
  format: "slack" # "slack", "discord", "generic"
  on_events: ["WARN_80", "CRITICAL_95", "EXCEEDED"]
```

---

## Application Integration Recipes

Application backends can query `_pgcaliper` directly for billing and customer dashboards:

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
WHERE group_id = 'tenant_acme'
ORDER BY captured_at DESC
LIMIT 1;
```

---

## Uninstallation

To remove all `pgcaliper` metadata cleanly:

```bash
pgcaliper uninstall
```

- Prompts for confirmation.
- Executes `DROP SCHEMA IF EXISTS _pgcaliper CASCADE;`.
- Leaves your user tables and application data untouched.

---

## License

This project is licensed under the Apache 2.0 License - see the [LICENSE](LICENSE) file for details.
