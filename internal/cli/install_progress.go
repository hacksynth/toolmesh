package cli

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strings"

	"toolmesh/internal/runtimes"
)

const (
	interactiveProgressByteStep    int64 = 512 << 10
	nonInteractiveProgressByteStep int64 = 5 << 20
)

type installProgressReporter struct {
	writer                  io.Writer
	interactive             bool
	lastInteractivePercent  int
	lastInteractiveBytes    int64
	lastStablePercentStep   int
	lastStableBytes         int64
	lastInteractiveLineSize int
	activeLine              bool
}

func newInstallProgressReporter(writer io.Writer) runtimes.DownloadProgressObserver {
	if writer == nil {
		return nil
	}
	return &installProgressReporter{
		writer:      writer,
		interactive: isInteractiveWriter(writer),
	}
}

func (r *installProgressReporter) OnDownloadProgress(event runtimes.DownloadProgressEvent) {
	switch event.State {
	case runtimes.DownloadProgressStarted:
		r.handleStarted(event)
	case runtimes.DownloadProgressAdvanced:
		r.handleAdvanced(event)
	case runtimes.DownloadProgressCompleted:
		r.handleCompleted(event)
	case runtimes.DownloadProgressFailed:
		r.handleFailed(event)
	}
}

func (r *installProgressReporter) handleStarted(event runtimes.DownloadProgressEvent) {
	r.lastInteractivePercent = 0
	r.lastInteractiveBytes = 0
	r.lastStablePercentStep = 0
	r.lastStableBytes = 0

	line := formatDownloadStart(event)
	if r.interactive {
		r.writeInteractive(line, false)
		return
	}
	fmt.Fprintln(r.writer, line)
}

func (r *installProgressReporter) handleAdvanced(event runtimes.DownloadProgressEvent) {
	if event.WrittenBytes <= 0 {
		return
	}

	if r.interactive {
		if !r.shouldRenderInteractive(event) {
			return
		}
		r.writeInteractive(formatDownloadProgress(event), false)
		return
	}

	if !r.shouldRenderStable(event) {
		return
	}
	fmt.Fprintln(r.writer, formatDownloadProgress(event))
}

func (r *installProgressReporter) handleCompleted(event runtimes.DownloadProgressEvent) {
	line := formatDownloadComplete(event)
	if r.interactive {
		r.writeInteractive(line, true)
		return
	}
	fmt.Fprintln(r.writer, line)
}

func (r *installProgressReporter) handleFailed(event runtimes.DownloadProgressEvent) {
	if r.interactive && r.activeLine {
		fmt.Fprintln(r.writer)
		r.activeLine = false
		r.lastInteractiveLineSize = 0
	}
	if !event.WillRetry {
		return
	}
	fmt.Fprintf(
		r.writer,
		"download attempt %d/%d for %s failed: %v\n",
		event.Attempt,
		event.MaxAttempts,
		downloadLabel(event),
		event.Err,
	)
}

func (r *installProgressReporter) shouldRenderInteractive(event runtimes.DownloadProgressEvent) bool {
	if event.TotalBytes > 0 {
		percent := int(event.WrittenBytes * 100 / event.TotalBytes)
		if percent <= r.lastInteractivePercent && event.WrittenBytes < event.TotalBytes {
			return false
		}
		r.lastInteractivePercent = percent
		return true
	}

	if event.WrittenBytes-r.lastInteractiveBytes < interactiveProgressByteStep {
		return false
	}
	r.lastInteractiveBytes = event.WrittenBytes
	return true
}

func (r *installProgressReporter) shouldRenderStable(event runtimes.DownloadProgressEvent) bool {
	if event.TotalBytes > 0 {
		percent := int(event.WrittenBytes * 100 / event.TotalBytes)
		percentStep := (percent / 10) * 10
		if percentStep < 10 || percentStep <= r.lastStablePercentStep {
			return event.WrittenBytes >= event.TotalBytes && percent > r.lastStablePercentStep
		}
		r.lastStablePercentStep = percentStep
		return true
	}

	if event.WrittenBytes-r.lastStableBytes < nonInteractiveProgressByteStep {
		return false
	}
	r.lastStableBytes = event.WrittenBytes
	return true
}

func (r *installProgressReporter) writeInteractive(line string, final bool) {
	padding := ""
	if extra := r.lastInteractiveLineSize - len(line); extra > 0 {
		padding = strings.Repeat(" ", extra)
	}

	if final {
		fmt.Fprintf(r.writer, "\r%s%s\n", line, padding)
		r.activeLine = false
		r.lastInteractiveLineSize = 0
		return
	}

	fmt.Fprintf(r.writer, "\r%s%s", line, padding)
	r.activeLine = true
	r.lastInteractiveLineSize = len(line)
}

func formatDownloadStart(event runtimes.DownloadProgressEvent) string {
	label := downloadLabelWithAttempt(event)
	if event.TotalBytes > 0 {
		return fmt.Sprintf("downloading %s (%s)", label, formatBytes(event.TotalBytes))
	}
	return fmt.Sprintf("downloading %s (size unknown)", label)
}

func formatDownloadProgress(event runtimes.DownloadProgressEvent) string {
	label := downloadLabelWithAttempt(event)
	if event.TotalBytes > 0 {
		percent := int(event.WrittenBytes * 100 / event.TotalBytes)
		return fmt.Sprintf(
			"downloading %s %d%% (%s/%s)",
			label,
			percent,
			formatBytes(event.WrittenBytes),
			formatBytes(event.TotalBytes),
		)
	}
	return fmt.Sprintf("downloading %s %s", label, formatBytes(event.WrittenBytes))
}

func formatDownloadComplete(event runtimes.DownloadProgressEvent) string {
	label := downloadLabelWithAttempt(event)
	if event.WrittenBytes > 0 {
		return fmt.Sprintf("downloaded %s (%s)", label, formatBytes(event.WrittenBytes))
	}
	if event.TotalBytes > 0 {
		return fmt.Sprintf("downloaded %s (%s)", label, formatBytes(event.TotalBytes))
	}
	return fmt.Sprintf("downloaded %s", label)
}

func downloadLabelWithAttempt(event runtimes.DownloadProgressEvent) string {
	label := downloadLabel(event)
	if event.Attempt > 1 && event.MaxAttempts > 1 {
		return fmt.Sprintf("%s (attempt %d/%d)", label, event.Attempt, event.MaxAttempts)
	}
	return label
}

func downloadLabel(event runtimes.DownloadProgressEvent) string {
	if event.Runtime != "" && event.Version != "" {
		return event.Runtime + " " + event.Version
	}
	if event.Runtime != "" {
		return event.Runtime
	}
	if event.Version != "" {
		return event.Version
	}
	if parsed, err := url.Parse(event.URL); err == nil {
		name := path.Base(parsed.Path)
		if name != "" && name != "." && name != "/" {
			return name
		}
	}
	return "artifact"
}

func formatBytes(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}

	units := []string{"KiB", "MiB", "GiB", "TiB"}
	value := float64(size)
	unit := "B"
	for _, nextUnit := range units {
		value /= 1024
		unit = nextUnit
		if value < 1024 {
			break
		}
	}

	if value >= 10 {
		return fmt.Sprintf("%.0f %s", value, unit)
	}
	return fmt.Sprintf("%.1f %s", value, unit)
}

func isInteractiveWriter(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
