package updater

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"pgcaliper/internal/ui"
)

type GitHubRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	PublishedAt string `json:"published_at"`
	HTMLURL     string `json:"html_url"`
}

func fetchLatestRelease() (*GitHubRelease, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", "https://api.github.com/repos/Shubham071122/pgcaliper/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "pgcaliper-self-updater")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed checking latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api returned status %d", resp.StatusCode)
	}

	var rel GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("failed parsing release metadata: %w", err)
	}
	return &rel, nil
}

func cleanVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

func Upgrade(currentVersion string) error {
	ui.PrintBanner()
	spinner := ui.StartSpinner("Checking for pgcaliper updates on GitHub...")

	rel, err := fetchLatestRelease()
	if err != nil {
		spinner.Stop(fmt.Sprintf("Update check failed: %v", err), false)
		return err
	}

	currClean := cleanVersion(currentVersion)
	latestClean := cleanVersion(rel.TagName)

	if currClean == latestClean && currentVersion != "dev" {
		spinner.Stop(fmt.Sprintf("pgcaliper is already up to date (%s)!", rel.TagName), true)
		return nil
	}

	spinner.Stop(fmt.Sprintf("New version available: %s -> %s", currentVersion, rel.TagName), true)

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("unable to determine binary path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("unable to resolve binary symlink: %w", err)
	}

	binaryName := fmt.Sprintf("pgcaliper-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	downloadURL := fmt.Sprintf("https://github.com/Shubham071122/pgcaliper/releases/latest/download/%s", binaryName)

	dlSpinner := ui.StartSpinner(fmt.Sprintf("Downloading %s for %s/%s...", rel.TagName, runtime.GOOS, runtime.GOARCH))

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(downloadURL)
	if err != nil {
		dlSpinner.Stop("Download failed", false)
		return fmt.Errorf("failed downloading binary: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		dlSpinner.Stop(fmt.Sprintf("Binary not found on remote (HTTP %d)", resp.StatusCode), false)
		return fmt.Errorf("binary %s not available in release %s", binaryName, rel.TagName)
	}

	tempDir := filepath.Dir(execPath)
	tempFile, err := os.CreateTemp(tempDir, "pgcaliper-update-*")
	if err != nil {
		tempFile, err = os.CreateTemp("", "pgcaliper-update-*")
		if err != nil {
			dlSpinner.Stop("Failed creating temp file", false)
			return fmt.Errorf("failed creating temp file: %w", err)
		}
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	_, err = io.Copy(tempFile, resp.Body)
	tempFile.Close()
	if err != nil {
		dlSpinner.Stop("Failed saving download", false)
		return fmt.Errorf("failed writing downloaded binary: %w", err)
	}

	if err := os.Chmod(tempPath, 0755); err != nil {
		dlSpinner.Stop("Failed setting permissions", false)
		return fmt.Errorf("failed setting executable permissions: %w", err)
	}

	// Replace the old binary
	if err := os.Rename(tempPath, execPath); err != nil {
		// Fallback for cross-device link: copy over
		input, errRead := os.ReadFile(tempPath)
		if errRead != nil {
			dlSpinner.Stop("Failed updating binary", false)
			if os.IsPermission(err) {
				return fmt.Errorf("permission denied writing to %s. Please run with 'sudo pgcaliper upgrade'", execPath)
			}
			return err
		}
		if errWrite := os.WriteFile(execPath, input, 0755); errWrite != nil {
			dlSpinner.Stop("Failed updating binary", false)
			if os.IsPermission(errWrite) {
				return fmt.Errorf("permission denied writing to %s. Please run with 'sudo pgcaliper upgrade'", execPath)
			}
			return errWrite
		}
	}

	dlSpinner.Stop(fmt.Sprintf("Successfully upgraded pgcaliper to %s!", rel.TagName), true)
	fmt.Printf("\n  %s Binary updated at %s\n", ui.Green("✓"), ui.Cyan(fmt.Sprintf("'%s'", execPath)))
	fmt.Printf("  %s Run '%s' to verify.\n\n", ui.Cyan("›"), ui.Green("pgcaliper -v"))
	return nil
}
