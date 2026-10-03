package model

import "time"

type QuotaStatus string

const (
	StatusActive        QuotaStatus = "ACTIVE"
	StatusWarning80     QuotaStatus = "WARNING_80"
	StatusWarning95     QuotaStatus = "WARNING_95"
	StatusLimitExceeded QuotaStatus = "EXCEEDED"
)

type GroupType string

const (
	GroupTypeSchema   GroupType = "SCHEMA"
	GroupTypeDatabase GroupType = "DATABASE"
	GroupTypeCustom   GroupType = "CUSTOM_GROUP"
	GroupTypeRowLevel GroupType = "ROW_LEVEL"
)

type TableMetrics struct {
	SchemaName    string `json:"schema_name"`
	TableName     string `json:"table_name"`
	DataBytes     int64  `json:"data_bytes"`
	IndexBytes    int64  `json:"index_bytes"`
	TotalBytes    int64  `json:"total_bytes"`
	HumanDataSize string `json:"human_data_size"`
	HumanIdxSize  string `json:"human_idx_size"`
	HumanTotal    string `json:"human_total"`
}

type GroupSnapshot struct {
	ID              int64          `json:"id"`
	GroupID         string         `json:"group_id"`
	GroupType       GroupType      `json:"group_type"`
	HeapBytes       int64          `json:"heap_bytes"`
	IndexBytes      int64          `json:"index_bytes"`
	TotalBytes      int64          `json:"total_bytes"`
	QuotaBytes      int64          `json:"quota_bytes"`
	UsagePercentage float64        `json:"usage_percentage"`
	Status          QuotaStatus    `json:"status"`
	TablesCount     int            `json:"tables_count"`
	Tables          []TableMetrics `json:"tables,omitempty"`
	CapturedAt      time.Time      `json:"captured_at"`
}
