package daemon

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"
	"pgcaliper/internal/alert"
	"pgcaliper/internal/config"
	"pgcaliper/internal/inspector"
	"pgcaliper/internal/model"
	"pgcaliper/internal/storage"
	"pgcaliper/internal/ui"
)

type Daemon struct {
	cfg        *config.Config
	pool       *pgxpool.Pool
	repo       *storage.Repository
	migrator   *storage.Migrator
	ins        *inspector.Inspector
	dispatcher *alert.Dispatcher
}

func New(cfg *config.Config, pool *pgxpool.Pool) *Daemon {
	return &Daemon{
		cfg:        cfg,
		pool:       pool,
		repo:       storage.NewRepository(pool, cfg.Storage.Schema),
		migrator:   storage.NewMigrator(pool, cfg.Storage.Schema),
		ins:        inspector.New(pool, cfg),
		dispatcher: alert.NewDispatcher(&cfg.Alerts),
	}
}

func (d *Daemon) ExecuteCalibration(ctx context.Context) error {
	start := time.Now()
	timeStr := start.Format("15:04:05")

	fmt.Printf("[%s] %s Starting storage calibration (mode: %s)...\n",
		ui.Gray(timeStr), ui.Cyan("›"), ui.White(d.cfg.Strategy.Mode))

	if err := d.migrator.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("telemetry schema migration failed: %w", err)
	}

	totalDbBytes, prettyDbSize, err := d.ins.GetDatabaseTotalSize(ctx)
	if err == nil {
		fmt.Printf("[%s] %s Database footprint: %s %s\n",
			ui.Gray(timeStr), ui.Cyan("›"), ui.Green(prettyDbSize), ui.Gray(fmt.Sprintf("(%d bytes)", totalDbBytes)))
	}

	snapshots, err := d.ins.RunInspection(ctx)
	if err != nil {
		return fmt.Errorf("inspection failed: %w", err)
	}

	if len(snapshots) == 0 {
		fmt.Printf("[%s] %s No schemas or table groups matched pattern.\n", ui.Gray(timeStr), ui.Yellow("▲"))
		return nil
	}

	if err := d.repo.SaveSnapshotsBatch(ctx, snapshots); err != nil {
		return fmt.Errorf("batch snapshot save failed: %w", err)
	}

	if d.cfg.Schedule.RetentionDays > 0 {
		deleted, err := d.repo.PurgeOldSnapshots(ctx, d.cfg.Schedule.RetentionDays)
		if err == nil && deleted > 0 {
			fmt.Printf("[%s] %s Retention trimmer purged %d old snapshot records (>%dd).\n",
				ui.Gray(timeStr), ui.Magenta("~"), deleted, d.cfg.Schedule.RetentionDays)
		}
	}

	for _, s := range snapshots {
		if s.Status != model.StatusActive {
			go func(snap *model.GroupSnapshot) {
				if err := d.dispatcher.Dispatch(context.Background(), snap); err != nil {
					fmt.Printf("[%s] %s Webhook dispatch failed for %s: %v\n",
						ui.Gray(time.Now().Format("15:04:05")), ui.Red("✖"), snap.GroupID, err)
				}
			}(s)
		}
	}

	duration := time.Since(start)
	fmt.Printf("[%s] %s Calibration completed in %s (%d entities measured).\n\n",
		ui.Gray(time.Now().Format("15:04:05")), ui.Green("✓"), duration, len(snapshots))

	return nil
}

func (d *Daemon) Start() error {
	ui.PrintBanner()
	fmt.Printf("  %s Starting 24/7 background metering daemon\n", ui.Cyan("›"))
	fmt.Printf("  %s Schedule: %s | Schema: %s | Mode: %s\n",
		ui.Gray("•"),
		ui.Green(d.cfg.Schedule.Interval),
		ui.Cyan(d.cfg.Storage.Schema),
		ui.White(d.cfg.Strategy.Mode),
	)
	fmt.Printf("  %s Press %s to gracefully stop.\n\n", ui.Gray("•"), ui.Yellow("Ctrl+C"))

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	initCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	if err := d.ExecuteCalibration(initCtx); err != nil {
		fmt.Printf("%s Initial calibration error: %v\n", ui.Red("✖"), err)
	}
	cancel()

	intervalStr := strings.TrimSpace(d.cfg.Schedule.Interval)
	isCron := len(strings.Fields(intervalStr)) == 5 || strings.HasPrefix(intervalStr, "@")

	if isCron {
		c := cron.New()
		_, err := c.AddFunc(intervalStr, func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := d.ExecuteCalibration(ctx); err != nil {
				fmt.Printf("%s Cron calibration error: %v\n", ui.Red("✖"), err)
			}
		})
		if err != nil {
			return fmt.Errorf("invalid cron expression '%s': %w", intervalStr, err)
		}
		c.Start()
		defer c.Stop()

		<-sigChan
		fmt.Println(ui.Yellow("\n› Shutting down pgcaliper daemon gracefully..."))
		return nil
	}

	interval, err := d.cfg.ParseInterval()
	if err != nil {
		return fmt.Errorf("invalid duration interval '%s': %w", intervalStr, err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-sigChan:
			fmt.Println(ui.Yellow("\n› Shutting down pgcaliper daemon gracefully..."))
			return nil
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			if err := d.ExecuteCalibration(ctx); err != nil {
				fmt.Printf("%s Scheduled calibration error: %v\n", ui.Red("✖"), err)
			}
			cancel()
		}
	}
}
