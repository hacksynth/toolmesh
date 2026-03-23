package runtimes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	downloadRetryAttempts = 3
	downloadRetryDelay    = 200 * time.Millisecond
)

var (
	artifactCachePolicy = cachePolicy{
		MaxAge:       30 * 24 * time.Hour,
		MaxEntries:   24,
		MaxSizeBytes: 2 << 30,
	}
	metadataCachePolicy = cachePolicy{
		MaxAge:       7 * 24 * time.Hour,
		MaxEntries:   64,
		MaxSizeBytes: 64 << 20,
	}
)

type cachePolicy struct {
	MaxAge       time.Duration
	MaxEntries   int
	MaxSizeBytes int64
}

type cacheFile struct {
	Path    string
	ModTime time.Time
	Size    int64
}

func (m *Manager) download(ctx context.Context, runtimeName string, version string, pkg PackageSpec) (string, error) {
	cacheDir := m.paths.CacheRoot
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}

	cachePath, err := artifactCachePath(cacheDir, pkg.URL)
	if err != nil {
		return "", err
	}

	if err := validateArtifact(cachePath, pkg); err == nil {
		now := time.Now()
		_ = os.Chtimes(cachePath, now, now)
		return cachePath, nil
	} else if !os.IsNotExist(err) {
		_ = os.Remove(cachePath)
	}

	var lastErr error
	for attempt := 1; attempt <= downloadRetryAttempts; attempt++ {
		if err := m.downloadOnce(ctx, runtimeName, version, pkg, cachePath, attempt, downloadRetryAttempts); err == nil {
			_ = pruneCacheDir(cacheDir, artifactCachePolicy)
			return cachePath, nil
		} else {
			lastErr = err
			m.emitDownloadProgress(DownloadProgressEvent{
				State:       DownloadProgressFailed,
				Runtime:     runtimeName,
				Version:     version,
				URL:         pkg.URL,
				Attempt:     attempt,
				MaxAttempts: downloadRetryAttempts,
				WillRetry:   attempt < downloadRetryAttempts,
				Err:         err,
			})
		}

		if attempt == downloadRetryAttempts {
			break
		}
		if err := sleepWithContext(ctx, time.Duration(attempt)*downloadRetryDelay); err != nil {
			return "", err
		}
	}

	return "", fmt.Errorf("download failed after %d attempts: %w", downloadRetryAttempts, lastErr)
}

func (m *Manager) downloadOnce(ctx context.Context, runtimeName string, version string, pkg PackageSpec, cachePath string, attempt int, maxAttempts int) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, pkg.URL, nil)
	if err != nil {
		return err
	}

	response, err := m.httpClient().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s returned %s", pkg.URL, response.Status)
	}

	cacheDir := filepath.Dir(cachePath)
	tempFile, err := os.CreateTemp(cacheDir, filepath.Base(cachePath)+".tmp-*")
	if err != nil {
		return err
	}

	tempPath := tempFile.Name()
	totalBytes := pkg.Size
	if totalBytes <= 0 && response.ContentLength > 0 {
		totalBytes = response.ContentLength
	}

	m.emitDownloadProgress(DownloadProgressEvent{
		State:       DownloadProgressStarted,
		Runtime:     runtimeName,
		Version:     version,
		URL:         pkg.URL,
		Attempt:     attempt,
		MaxAttempts: maxAttempts,
		TotalBytes:  totalBytes,
	})

	written, sum, err := copyWithOptionalHash(tempFile, response.Body, pkg.SHA256 != "", func(written int64) {
		m.emitDownloadProgress(DownloadProgressEvent{
			State:        DownloadProgressAdvanced,
			Runtime:      runtimeName,
			Version:      version,
			URL:          pkg.URL,
			Attempt:      attempt,
			MaxAttempts:  maxAttempts,
			WrittenBytes: written,
			TotalBytes:   totalBytes,
		})
	})
	closeErr := tempFile.Close()
	if err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if closeErr != nil {
		_ = os.Remove(tempPath)
		return closeErr
	}

	if pkg.Size > 0 && written != pkg.Size {
		_ = os.Remove(tempPath)
		return fmt.Errorf("downloaded size mismatch for %s: expected %d bytes, got %d", pkg.URL, pkg.Size, written)
	}
	if pkg.SHA256 != "" && !strings.EqualFold(hex.EncodeToString(sum), strings.TrimSpace(pkg.SHA256)) {
		_ = os.Remove(tempPath)
		return fmt.Errorf("checksum mismatch for %s", pkg.URL)
	}

	if err := os.Rename(tempPath, cachePath); err != nil {
		_ = os.Remove(tempPath)
		return err
	}

	m.emitDownloadProgress(DownloadProgressEvent{
		State:        DownloadProgressCompleted,
		Runtime:      runtimeName,
		Version:      version,
		URL:          pkg.URL,
		Attempt:      attempt,
		MaxAttempts:  maxAttempts,
		WrittenBytes: written,
		TotalBytes:   totalBytes,
	})

	return nil
}

func artifactCachePath(cacheDir string, rawURL string) (string, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	name := filepath.Base(parsedURL.Path)
	if name == "." || name == "/" || name == "" {
		name = "download.bin"
	}

	sum := sha256.Sum256([]byte(rawURL))
	cacheName := hex.EncodeToString(sum[:8]) + "-" + name
	return filepath.Join(cacheDir, cacheName), nil
}

func validateArtifact(path string, pkg PackageSpec) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() || info.Size() == 0 {
		return fmt.Errorf("invalid cached artifact: %s", path)
	}
	if pkg.Size > 0 && info.Size() != pkg.Size {
		return fmt.Errorf("cached artifact size mismatch: %s", path)
	}
	if pkg.SHA256 == "" {
		return nil
	}

	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	_, sum, err := copyWithOptionalHash(io.Discard, file, true, nil)
	if err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(sum), strings.TrimSpace(pkg.SHA256)) {
		return fmt.Errorf("cached artifact checksum mismatch: %s", path)
	}
	return nil
}

func copyWithOptionalHash(destination io.Writer, source io.Reader, wantHash bool, progress func(int64)) (int64, []byte, error) {
	writer := destination
	counter := &countingWriter{
		writer:   writer,
		progress: progress,
	}

	if !wantHash {
		written, err := io.Copy(counter, source)
		return written, nil, err
	}

	hasher := sha256.New()
	counter.writer = io.MultiWriter(destination, hasher)
	written, err := io.Copy(counter, source)
	if err != nil {
		return written, nil, err
	}
	return written, hasher.Sum(nil), nil
}

type countingWriter struct {
	writer   io.Writer
	written  int64
	progress func(int64)
}

func (w *countingWriter) Write(data []byte) (int, error) {
	written, err := w.writer.Write(data)
	if written > 0 {
		w.written += int64(written)
		if w.progress != nil {
			w.progress(w.written)
		}
	}
	return written, err
}

func pruneCacheDir(cacheDir string, policy cachePolicy) error {
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	now := time.Now()
	files := make([]cacheFile, 0, len(entries))
	var totalSize int64

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		fullPath := filepath.Join(cacheDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}

		if info.Size() == 0 || strings.Contains(entry.Name(), ".tmp-") {
			if now.Sub(info.ModTime()) > time.Hour {
				_ = os.Remove(fullPath)
			}
			continue
		}

		if policy.MaxAge > 0 && now.Sub(info.ModTime()) > policy.MaxAge {
			if err := os.Remove(fullPath); err == nil {
				continue
			}
		}

		files = append(files, cacheFile{
			Path:    fullPath,
			ModTime: info.ModTime(),
			Size:    info.Size(),
		})
		totalSize += info.Size()
	}

	sort.Slice(files, func(i int, j int) bool {
		return files[i].ModTime.Before(files[j].ModTime)
	})

	for len(files) > 0 && exceedsCachePolicy(len(files), totalSize, policy) {
		file := files[0]
		files = files[1:]
		if err := os.Remove(file.Path); err != nil {
			return err
		}
		totalSize -= file.Size
	}

	return nil
}

func exceedsCachePolicy(fileCount int, totalSize int64, policy cachePolicy) bool {
	if policy.MaxEntries > 0 && fileCount > policy.MaxEntries {
		return true
	}
	if policy.MaxSizeBytes > 0 && totalSize > policy.MaxSizeBytes {
		return true
	}
	return false
}

func writeCacheFile(cacheDir string, cachePath string, data []byte) error {
	tempFile, err := os.CreateTemp(cacheDir, filepath.Base(cachePath)+".tmp-*")
	if err != nil {
		return err
	}

	tempPath := tempFile.Name()
	if _, err := tempFile.Write(data); err != nil {
		tempFile.Close()
		_ = os.Remove(tempPath)
		return err
	}
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}

	if err := os.Rename(tempPath, cachePath); err != nil {
		_ = os.Remove(tempPath)
		return err
	}

	return nil
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
