package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"pgcaliper/internal/model"
)

type WebhookConfig struct {
	Enabled  bool     `yaml:"enabled"`
	URL      string   `yaml:"url"`
	OnEvents []string `yaml:"on_events"`
	Format   string   `yaml:"format"`
}

type Payload struct {
	Event      string               `json:"event"`
	Timestamp  time.Time            `json:"timestamp"`
	GroupID    string               `json:"group_id"`
	TotalBytes int64                `json:"total_bytes"`
	QuotaBytes int64                `json:"quota_bytes"`
	UsagePct   float64              `json:"usage_percentage"`
	Status     string               `json:"status"`
	Snapshot   *model.GroupSnapshot `json:"snapshot,omitempty"`
}

type Dispatcher struct {
	cfg    *WebhookConfig
	client *http.Client
}

func NewDispatcher(cfg *WebhookConfig) *Dispatcher {
	if cfg == nil {
		cfg = &WebhookConfig{Enabled: false}
	}
	return &Dispatcher{
		cfg:    cfg,
		client: &http.Client{Timeout: 8 * time.Second},
	}
}

func (d *Dispatcher) shouldTrigger(status model.QuotaStatus) bool {
	if !d.cfg.Enabled || d.cfg.URL == "" {
		return false
	}
	if len(d.cfg.OnEvents) == 0 {
		return status != model.StatusActive
	}
	for _, e := range d.cfg.OnEvents {
		if string(status) == e || (e == "WARN_80" && status == model.StatusWarning80) ||
			(e == "CRITICAL_95" && status == model.StatusWarning95) ||
			(e == "EXCEEDED" && status == model.StatusLimitExceeded) {
			return true
		}
	}
	return false
}

func (d *Dispatcher) Dispatch(ctx context.Context, snapshot *model.GroupSnapshot) error {
	if !d.shouldTrigger(snapshot.Status) {
		return nil
	}

	payload := Payload{
		Event:      fmt.Sprintf("quota.%s", string(snapshot.Status)),
		Timestamp:  time.Now(),
		GroupID:    snapshot.GroupID,
		TotalBytes: snapshot.TotalBytes,
		QuotaBytes: snapshot.QuotaBytes,
		UsagePct:   snapshot.UsagePercentage,
		Status:     string(snapshot.Status),
		Snapshot:   snapshot,
	}

	var reqBody []byte
	var err error

	if d.cfg.Format == "slack" {
		slackPayload := map[string]interface{}{
			"text": fmt.Sprintf("▲ *[pgcaliper Alert]* Tenant `%s` reached *%.1f%%* storage quota (%s)!\nTotal Used: `%d bytes`",
				snapshot.GroupID, snapshot.UsagePercentage, snapshot.Status, snapshot.TotalBytes),
		}
		reqBody, err = json.Marshal(slackPayload)
	} else {
		reqBody, err = json.Marshal(payload)
	}

	if err != nil {
		return fmt.Errorf("failed marshaling webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", d.cfg.URL, bytes.NewBuffer(reqBody))
	if err != nil {
		return fmt.Errorf("failed creating webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "pgcaliper-alert-agent/1.0")

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook request to %s failed: %w", d.cfg.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook endpoint returned HTTP status %d", resp.StatusCode)
	}

	return nil
}
