package ui

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/fatih/color"
	"pgcaliper/internal/model"
)

var (
	Cyan    = color.New(color.FgCyan, color.Bold).SprintFunc()
	Blue    = color.New(color.FgBlue, color.Bold).SprintFunc()
	Green   = color.New(color.FgGreen, color.Bold).SprintFunc()
	Yellow  = color.New(color.FgYellow, color.Bold).SprintFunc()
	Red     = color.New(color.FgRed, color.Bold).SprintFunc()
	Magenta = color.New(color.FgMagenta, color.Bold).SprintFunc()
	White   = color.New(color.FgWhite, color.Bold).SprintFunc()
	Gray    = color.New(color.FgHiBlack).SprintFunc()
)

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func StripANSI(str string) string {
	return ansiRegex.ReplaceAllString(str, "")
}

func VisibleWidth(str string) int {
	return utf8.RuneCountInString(StripANSI(str))
}

func PadRight(str string, length int) string {
	w := VisibleWidth(str)
	if w >= length {
		return str
	}
	return str + strings.Repeat(" ", length-w)
}

func PadLeft(str string, length int) string {
	w := VisibleWidth(str)
	if w >= length {
		return str
	}
	return strings.Repeat(" ", length-w) + str
}

func PrintBanner() {
	banner := `
  ╔═════════════════════════════════════════════════════════════════════════╗
  ║  ██████╗  ██████╗  ██████╗  █████╗ ██╗     ██╗██████╗ ███████╗██████╗   ║
  ║  ██╔══██╗██╔════╝ ██╔════╝ ██╔══██╗██║     ██║██╔══██╗██╔════╝██╔══██╗  ║
  ║  ██████╔╝██║  ███╗██║      ███████║██║     ██║██████╔╝█████╗  ██████╔╝  ║
  ║  ██╔═══╝ ██║   ██║██║      ██╔══██║██║     ██║██╔═══╝ ██╔══╝  ██╔══██╗  ║
  ║  ██║     ╚██████╔╝╚██████╗ ██║  ██║███████╗██║██║     ███████╗██║  ██║  ║
  ║  ╚═╝      ╚═════╝  ╚═════╝ ╚═╝  ╚═╝╚══════╝╚═╝╚═╝     ╚══════╝╚═╝  ╚═╝  ║
  ║                                                                         ║
  ║                 PostgreSQL Multi-Tenant Storage Caliper                 ║
  ╚═════════════════════════════════════════════════════════════════════════╝`
	fmt.Println(Cyan(banner))
	fmt.Println()
}

type Spinner struct {
	message string
	stop    chan struct{}
	wg      sync.WaitGroup
}

func StartSpinner(msg string) *Spinner {
	s := &Spinner{
		message: msg,
		stop:    make(chan struct{}),
	}
	s.wg.Add(1)

	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	go func() {
		defer s.wg.Done()
		i := 0
		for {
			select {
			case <-s.stop:
				fmt.Print("\r\033[K")
				return
			default:
				fmt.Printf("\r  %s %s", Cyan(frames[i%len(frames)]), msg)
				i++
				time.Sleep(70 * time.Millisecond)
			}
		}
	}()

	return s
}

func (s *Spinner) Stop(finalMsg string, success bool) {
	close(s.stop)
	s.wg.Wait()
	if success {
		fmt.Printf("\r  %s %s\n", Green("✓"), finalMsg)
	} else {
		fmt.Printf("\r  %s %s\n", Red("✖"), finalMsg)
	}
}

func FormatProgressBar(pct float64) string {
	const barWidth = 10
	if pct < 0 {
		pct = 0
	}
	filled := int((pct / 100.0) * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	empty := barWidth - filled

	filledBlocks := strings.Repeat("█", filled)
	emptyBlocks := strings.Repeat("░", empty)

	var coloredBar string
	if pct >= 100.0 {
		coloredBar = Red(filledBlocks + emptyBlocks)
	} else if pct >= 95.0 {
		coloredBar = Red(filledBlocks) + Gray(emptyBlocks)
	} else if pct >= 80.0 {
		coloredBar = Yellow(filledBlocks) + Gray(emptyBlocks)
	} else {
		coloredBar = Green(filledBlocks) + Gray(emptyBlocks)
	}

	return fmt.Sprintf("[%s] %5.1f%%", coloredBar, pct)
}

func FormatBytes(b int64) string {
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

func GetStatusBadge(status model.QuotaStatus) string {
	switch status {
	case model.StatusActive:
		return Green("● ACTIVE")
	case model.StatusWarning80:
		return Yellow("▲ WARNING (80%)")
	case model.StatusWarning95:
		return Red("▲ CRITICAL (95%)")
	case model.StatusLimitExceeded:
		return Red("■ EXCEEDED")
	default:
		return string(status)
	}
}

func PrintStep(stepNum int, title string, desc string) {
	fmt.Printf("\n  %s %s\n", Cyan(fmt.Sprintf("[%d]", stepNum)), White(title))
	if desc != "" {
		fmt.Printf("      %s\n", Gray(desc))
	}
}

func RenderTable(snapshots []*model.GroupSnapshot) {
	const (
		wGroup  = 18
		wType   = 8
		wData   = 17
		wIndex  = 11
		wTotal  = 12
		wCap    = 19
		wStatus = 18
	)

	fmt.Println(
		PadRight(Cyan("TENANT / GROUP"), wGroup) + "  " +
			PadRight(Cyan("TYPE"), wType) + "  " +
			PadLeft(Cyan("DATA (HEAP+TOAST)"), wData) + "  " +
			PadLeft(Cyan("INDEXES"), wIndex) + "  " +
			PadLeft(Cyan("TOTAL SIZE"), wTotal) + "  " +
			PadRight(Cyan("USAGE CAPACITY"), wCap) + "  " +
			PadRight(Cyan("STATUS"), wStatus),
	)

	fmt.Println(
		Gray(strings.Repeat("─", wGroup)) + "  " +
			Gray(strings.Repeat("─", wType)) + "  " +
			Gray(strings.Repeat("─", wData)) + "  " +
			Gray(strings.Repeat("─", wIndex)) + "  " +
			Gray(strings.Repeat("─", wTotal)) + "  " +
			Gray(strings.Repeat("─", wCap)) + "  " +
			Gray(strings.Repeat("─", wStatus)),
	)

	for _, s := range snapshots {
		fmt.Println(
			PadRight(White(s.GroupID), wGroup) + "  " +
				PadRight(Gray(string(s.GroupType)), wType) + "  " +
				PadLeft(FormatBytes(s.HeapBytes), wData) + "  " +
				PadLeft(FormatBytes(s.IndexBytes), wIndex) + "  " +
				PadLeft(Cyan(FormatBytes(s.TotalBytes)), wTotal) + "  " +
				PadRight(FormatProgressBar(s.UsagePercentage), wCap) + "  " +
				GetStatusBadge(s.Status),
		)
	}
}

func RenderTableDetails(snap *model.GroupSnapshot, tables []model.TableMetrics) {
	fmt.Printf("\n  %s %s: %s (%s total, %s limit)\n",
		Cyan("›"),
		White("Granular Breakdown for Tenant"),
		Cyan(snap.GroupID),
		Green(FormatBytes(snap.TotalBytes)),
		Yellow(FormatBytes(snap.QuotaBytes)),
	)
	fmt.Printf("     %s Capacity: %s | Status: %s | Captured: %s\n\n",
		Gray("•"),
		FormatProgressBar(snap.UsagePercentage),
		GetStatusBadge(snap.Status),
		Gray(snap.CapturedAt.Format("2006-01-02 15:04:05")),
	)

	const (
		wTable = 24
		wData  = 16
		wIndex = 14
		wTotal = 14
		wRatio = 14
	)

	fmt.Println(
		PadRight(Cyan("TABLE NAME"), wTable) + "  " +
			PadLeft(Cyan("HEAP + TOAST"), wData) + "  " +
			PadLeft(Cyan("INDEXES"), wIndex) + "  " +
			PadLeft(Cyan("TOTAL SIZE"), wTotal) + "  " +
			PadLeft(Cyan("INDEX %"), wRatio),
	)

	fmt.Println(
		Gray(strings.Repeat("─", wTable)) + "  " +
			Gray(strings.Repeat("─", wData)) + "  " +
			Gray(strings.Repeat("─", wIndex)) + "  " +
			Gray(strings.Repeat("─", wTotal)) + "  " +
			Gray(strings.Repeat("─", wRatio)),
	)

	for _, t := range tables {
		idxPct := 0.0
		if t.TotalBytes > 0 {
			idxPct = (float64(t.IndexBytes) / float64(t.TotalBytes)) * 100.0
		}
		fmt.Println(
			PadRight(White(t.TableName), wTable) + "  " +
				PadLeft(FormatBytes(t.DataBytes), wData) + "  " +
				PadLeft(FormatBytes(t.IndexBytes), wIndex) + "  " +
				PadLeft(Cyan(FormatBytes(t.TotalBytes)), wTotal) + "  " +
				PadLeft(fmt.Sprintf("%.1f%%", idxPct), wRatio),
		)
	}
	fmt.Println()
}

func PrintErrorWithHint(err error) {
	errStr := err.Error()
	fmt.Printf("\n  %s %s\n", Red("✖ ERROR:"), White(errStr))

	if strings.Contains(errStr, "connect: connection refused") || strings.Contains(errStr, "dial tcp") {
		fmt.Println(Yellow("\n  › Troubleshooting Hint:"))
		fmt.Println("     • PostgreSQL might not be running on the specified host/port.")
		fmt.Println("     • If running Docker test container, ensure: docker compose -f test/docker-compose.yml up -d")
		fmt.Println("     • Check pg_hba.conf to verify TCP connections are accepted.")
	} else if strings.Contains(errStr, "password authentication failed") {
		fmt.Println(Yellow("\n  › Troubleshooting Hint:"))
		fmt.Println("     • Invalid PostgreSQL username or password in connection string.")
		fmt.Println("     • Check DATABASE_URL or pgcaliper.yaml credentials.")
	} else if strings.Contains(errStr, "permission denied") {
		fmt.Println(Yellow("\n  › Troubleshooting Hint:"))
		fmt.Println("     • The database user lacks permission to create schema '_pgcaliper' or read tables.")
		fmt.Println("     • Grant required privileges: GRANT CREATE ON DATABASE <db_name> TO <user>;")
	}
	fmt.Println()
}
