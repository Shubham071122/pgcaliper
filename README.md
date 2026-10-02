# 📐 pgcaliper

> Enterprise-grade, ultra-lightweight PostgreSQL storage metering, telemetry, and multi-tenant quota engine written in Go.

`pgcaliper` operates as an independent, non-intrusive database telemetry agent. It inspects low-level PostgreSQL table heaps, TOAST pages, and secondary indexes without locking rows or degrading active OLTP transactions, recording structured snapshot telemetry directly into a dedicated database ledger (`_pgcaliper`).

---

## 📑 Table of Contents
1. [Why pgcaliper?](#-why-pgcaliper)
2. [Architecture Overview](#-architecture-overview)
3. [Installation](#-installation)
4. [Interactive Setup Wizard (`pgcaliper init`)](#-interactive-setup-wizard-pgcaliper-init)
5. [CLI Commands Reference](#-cli-commands-reference)
6. [Granular Table Breakdown (`pgcaliper details`)](#-granular-table-breakdown-pgcaliper-details)
7. [Running 24/7 as a Background Daemon (`pgcaliper daemon`)](#-running-247-as-a-background-daemon-pgcaliper-daemon)
8. [Measurement Strategies (`pgcaliper.yaml`)](#-measurement-strategies-pgcaliperyaml)
9. [Event Alerting & Webhooks (Slack/Discord/Custom)](#-event-alerting--webhooks-slackdiscordcustom)
10. [Querying Telemetry in Your Application / SaaS](#-querying-telemetry-in-your-application--saas)
11. [Clean Uninstallation (`pgcaliper uninstall`)](#-clean-uninstallation-pgcaliper-uninstall)
12. [Local Development & Docker Testing](#-local-development--docker-testing)

---

## 💡 Why pgcaliper?

In multi-tenant SaaS, ERPs, and database platforms, managing user storage (e.g. giving 15 GB free and charging for extra) has historically been painful:

- **Running live `SUM(pg_column_size)` on API requests** causes severe CPU spikes and blocks active user transactions.
- **Pure row counts are misleading**: In PostgreSQL, secondary indexes (B-Tree, GIN) and out-of-line TOAST data (compressed JSONB, XML, PDF text) often consume **40% to 70% of total storage**.
- **External billing tools** (like Stripe or Lago) do not know how PostgreSQL stores bytes on disk.

**`pgcaliper` solves this** by running as a lightweight daemon or sidecar. It queries PostgreSQL's storage catalogs with strict statement timeouts, separates Heap vs Index footprints, evaluates quotas, and records historical snapshots with zero application coupling.

---

## 🏛️ Architecture Overview

```
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

## 📦 Installation

### Option 1: One-Line Curl Installer (Linux / macOS)
```bash
curl -sSL https://raw.githubusercontent.com/Shubham071122/pgcaliper/main/install.sh | sudo bash
```

### Option 2: Pre-compiled Binary Release
Download the binary for your OS and architecture from GitHub Releases:
```bash
# Example for Linux x86_64
curl -L https://github.com/Shubham071122/pgcaliper/releases/latest/download/pgcaliper-linux-amd64 -o pgcaliper
chmod +x pgcaliper
sudo mv pgcaliper /usr/local/bin/
```

### Option 3: Build from Source (Go 1.22+)
```bash
git clone https://github.com/Shubham071122/pgcaliper.git
cd pgcaliper
go build -o bin/pgcaliper ./cmd/pgcaliper
```

---

## 🧙‍♂️ Interactive Setup Wizard (`pgcaliper init`)

To configure `pgcaliper` without writing YAML manually, run:

```bash
pgcaliper init
```

*The wizard tests your PostgreSQL connection live and generates `pgcaliper.yaml`.*

---

## 💻 CLI Commands Reference

| Command | Description |
| :--- | :--- |
| `pgcaliper init` | Interactive setup wizard with live PostgreSQL connection validation |
| `pgcaliper scan` | Immediate storage calibration, batch snapshot persistence & table output |
| `pgcaliper daemon` | 24/7 background scheduler (Duration or standard Cron + Retention Trimmer) |
| `pgcaliper status` | Fast lookup of latest tenant metrics directly from DB ledger |
| `pgcaliper details --tenant=<name>` | Granular table-by-table breakdown with heap vs index ratios |
| `pgcaliper export [--format=json\|csv]` | Export telemetry records to stdout or file (`--output=report.json`) |
| `pgcaliper uninstall` | Clean zero-trace drop of `_pgcaliper` schema from database |
| `pgcaliper help [command]` | Display built-in help and real-world examples |

---

## 📁 Granular Table Breakdown (`pgcaliper details`)

Inspect which specific tables and secondary indexes are consuming a tenant's quota:

```bash
./bin/pgcaliper details --tenant=tenant_acme
```

**Output:**
```text
  › Granular Breakdown for Tenant: tenant_acme (25.42 MB total, 15.00 GB limit)
     • Capacity: [░░░░░░░░░░]   0.2% | Status: ● ACTIVE | Captured: 2026-10-02 12:40:04

TABLE NAME                    HEAP + TOAST         INDEXES      TOTAL SIZE         INDEX %
────────────────────────  ────────────────  ──────────────  ──────────────  ──────────────
invoices                          17.80 MB         5.94 MB        23.73 MB           25.0%
customers                        792.00 KB       936.00 KB         1.69 MB           54.2%
```

---

## 🕒 Running 24/7 as a Background Daemon (`pgcaliper daemon`)

Supports human durations (e.g. `15m`, `1h`, `24h`) and standard 5-part cron expressions (`0 * * * *`, `*/30 * * * *`, `@daily`):

```bash
./bin/pgcaliper daemon --config=pgcaliper.yaml
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
ExecStart=/usr/local/bin/pgcaliper daemon --config=/etc/pgcaliper/pgcaliper.yaml
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

## ⚙️ Measurement Strategies (`pgcaliper.yaml`)

`pgcaliper` supports 3 configurable measurement strategies to match any database architecture:

### Mode 1: Multi-Tenant Schemas (Recommended for B2B SaaS & ERP)
```yaml
version: "1"

database:
  url: "${DATABASE_URL:-postgres://postgres:password123@localhost:5432/erp_db?sslmode=disable}"
  safe_mode_read_replica: true
  statement_timeout: "10s"

schedule:
  interval: "15m"      # "15m", "1h", "24h" or standard cron "0 * * * *"
  retention_days: 90   # Auto-purges old snapshot records older than 90 days

storage:
  schema: "_pgcaliper" # Schema name created inside PostgreSQL

strategy:
  mode: "schema"
  schema_pattern: "tenant_.*|org_.*"
  default_quota_bytes: 16106127360 # 15 GB

engine:
  concurrency: 4
```

### Mode 2: Database Fleet (DB-per-Tenant)
```yaml
strategy:
  mode: "database"
  default_quota_bytes: 16106127360 # 15 GB per DB
```

### Mode 3: Custom Domain Table Groups
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

## 🔔 Event Alerting & Webhooks (Slack/Discord/Custom)

Configure real-time notifications when tenants reach storage thresholds:

```yaml
alerts:
  enabled: true
  url: "https://hooks.slack.com/services/YOUR/WEBHOOK/URL"
  format: "slack" # "slack", "discord", "generic"
  on_events: ["WARN_80", "CRITICAL_95", "EXCEEDED"]
```

---

## 📊 Querying Telemetry in Your Application / SaaS

Your application backend (Node.js, Go, Python, PHP, Ruby) can query `_pgcaliper` directly for billing and UI dashboards:

```sql
-- Query latest storage usage per tenant for application dashboards
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

## 🧹 Clean Uninstallation (`pgcaliper uninstall`)

```bash
pgcaliper uninstall --config=pgcaliper.yaml
```
- Prompts for confirmation.
- Executes `DROP SCHEMA IF EXISTS _pgcaliper CASCADE;`.
- Leaves your user tables and application data completely untouched.

---

## 🧪 Local Development & Docker Testing

```bash
# 1. Start test Postgres container (seeded with multi-tenant data)
docker compose -f test/docker-compose.yml up -d

# 2. Build binary
go build -o bin/pgcaliper ./cmd/pgcaliper

# 3. Run scan
./bin/pgcaliper scan

# 4. Check details
./bin/pgcaliper details --tenant=tenant_acme

# 5. Export JSON
./bin/pgcaliper export --format=json
```
