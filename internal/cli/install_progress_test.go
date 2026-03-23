package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"toolmesh/internal/runtimes"
)

func TestInstallProgressReporterNonInteractiveKnownSize(t *testing.T) {
	var stderr bytes.Buffer
	reporter := &installProgressReporter{
		writer:      &stderr,
		interactive: false,
	}

	reporter.OnDownloadProgress(runtimes.DownloadProgressEvent{
		State:       runtimes.DownloadProgressStarted,
		Runtime:     "go",
		Version:     "1.22.2",
		Attempt:     1,
		MaxAttempts: 3,
		TotalBytes:  100,
	})
	reporter.OnDownloadProgress(runtimes.DownloadProgressEvent{
		State:        runtimes.DownloadProgressAdvanced,
		Runtime:      "go",
		Version:      "1.22.2",
		WrittenBytes: 5,
		TotalBytes:   100,
	})
	reporter.OnDownloadProgress(runtimes.DownloadProgressEvent{
		State:        runtimes.DownloadProgressAdvanced,
		Runtime:      "go",
		Version:      "1.22.2",
		WrittenBytes: 15,
		TotalBytes:   100,
	})
	reporter.OnDownloadProgress(runtimes.DownloadProgressEvent{
		State:        runtimes.DownloadProgressAdvanced,
		Runtime:      "go",
		Version:      "1.22.2",
		WrittenBytes: 55,
		TotalBytes:   100,
	})
	reporter.OnDownloadProgress(runtimes.DownloadProgressEvent{
		State:        runtimes.DownloadProgressCompleted,
		Runtime:      "go",
		Version:      "1.22.2",
		WrittenBytes: 100,
		TotalBytes:   100,
	})

	output := stderr.String()
	if !strings.Contains(output, "downloading go 1.22.2 (100 B)") {
		t.Fatalf("expected start line, got %q", output)
	}
	if !strings.Contains(output, "downloading go 1.22.2 15% (15 B/100 B)") {
		t.Fatalf("expected stable progress line, got %q", output)
	}
	if !strings.Contains(output, "downloading go 1.22.2 55% (55 B/100 B)") {
		t.Fatalf("expected second stable progress line, got %q", output)
	}
	if !strings.Contains(output, "downloaded go 1.22.2 (100 B)") {
		t.Fatalf("expected completion line, got %q", output)
	}
}

func TestInstallProgressReporterNonInteractiveUnknownSizeAndRetry(t *testing.T) {
	var stderr bytes.Buffer
	reporter := &installProgressReporter{
		writer:      &stderr,
		interactive: false,
	}

	reporter.OnDownloadProgress(runtimes.DownloadProgressEvent{
		State:   runtimes.DownloadProgressStarted,
		Runtime: "python",
		Version: "3.12.10",
	})
	reporter.OnDownloadProgress(runtimes.DownloadProgressEvent{
		State:        runtimes.DownloadProgressAdvanced,
		Runtime:      "python",
		Version:      "3.12.10",
		WrittenBytes: 6 << 20,
	})
	reporter.OnDownloadProgress(runtimes.DownloadProgressEvent{
		State:       runtimes.DownloadProgressFailed,
		Runtime:     "python",
		Version:     "3.12.10",
		Attempt:     1,
		MaxAttempts: 3,
		WillRetry:   true,
		Err:         errors.New("temporary failure"),
	})
	reporter.OnDownloadProgress(runtimes.DownloadProgressEvent{
		State:        runtimes.DownloadProgressCompleted,
		Runtime:      "python",
		Version:      "3.12.10",
		Attempt:      2,
		MaxAttempts:  3,
		WrittenBytes: 7 << 20,
	})

	output := stderr.String()
	if !strings.Contains(output, "downloading python 3.12.10 (size unknown)") {
		t.Fatalf("expected unknown-size start line, got %q", output)
	}
	if !strings.Contains(output, "downloading python 3.12.10 6.0 MiB") {
		t.Fatalf("expected byte progress line, got %q", output)
	}
	if !strings.Contains(output, "download attempt 1/3 for python 3.12.10 failed:") {
		t.Fatalf("expected retry failure line, got %q", output)
	}
	if !strings.Contains(output, "downloaded python 3.12.10 (attempt 2/3) (7.0 MiB)") {
		t.Fatalf("expected completion line for retry attempt, got %q", output)
	}
}
