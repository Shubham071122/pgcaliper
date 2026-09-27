package model

import "time"

// QuotaStatus represents the threshold state of a tenant
type QuotaStatus string

const (
	StatusActive        QuotaStatus = "ACTIVE"
	StatusWarning80     QuotaStatus = "WARNING_80"
	StatusWarning95     QuotaStatus = "WARNING_95"
	StatusLimitExceeded QuotaStatus = "EXCEEDED"
)

// TableMetrics contains the granular storage breakdown for an individual table
type TableMetrics struct {
	SchemaName    string `json:"schema_name"`
	TableName     string `json:"table_name"`
	DataBytes     int64  `json:"data_bytes"`     // Heap data + TOAST
	IndexBytes    int64  `json:"index_bytes"`    // B-Tree, GIN, etc.
	TotalBytes    int64  `json:"total_bytes"`    // Data + Index + FSM
	HumanDataSize string `json:"human_data_size"`
	HumanIdxSize  string `json:"human_idx_size"`
	HumanTotal    string `json:"human_total"`
}

// TenantMetrics represents the aggregated storage footprint for a tenant/schema
type TenantMetrics struct {
	TenantID        string         `json:"tenant_id"`
	SchemaName      string         `json:"schema_name"`
	TotalBytes      int64          `json:"total_bytes"`
	DataBytes       int64          `json:"data_bytes"`
	IndexBytes      int64          `json:"index_bytes"`
	LimitBytes      int64          `json:"limit_bytes"`
	UsagePercentage float64        `json:"usage_percentage"`
	Status          QuotaStatus    `json:"status"`
	Tables          []TableMetrics `json:"tables,omitempty"`
	InspectedAt     time.Time      `json:"inspected_at"`
}
