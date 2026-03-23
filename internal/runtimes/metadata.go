package runtimes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func (m *Manager) metadataCacheDir() string {
	return filepath.Join(m.paths.CacheRoot, "metadata")
}

func (m *Manager) FetchJSON(ctx context.Context, rawURL string, ttl time.Duration, target any) error {
	data, err := m.fetchMetadata(ctx, rawURL, ttl)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func (m *Manager) FetchText(ctx context.Context, rawURL string, ttl time.Duration) (string, error) {
	data, err := m.fetchMetadata(ctx, rawURL, ttl)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (m *Manager) fetchMetadata(ctx context.Context, rawURL string, ttl time.Duration) ([]byte, error) {
	cacheDir := m.metadataCacheDir()
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, err
	}

	cachePath := filepath.Join(cacheDir, metadataCacheName(rawURL))
	if data, fresh, err := readCachedMetadata(cachePath, ttl); err == nil && fresh {
		return data, nil
	} else if err != nil {
		return nil, err
	}

	staleData, _ := os.ReadFile(cachePath)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}

	response, err := m.httpClient().Do(request)
	if err != nil {
		if len(staleData) > 0 {
			return staleData, nil
		}
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		if len(staleData) > 0 {
			return staleData, nil
		}
		return nil, fmt.Errorf("metadata request failed: %s returned %s", rawURL, response.Status)
	}

	data, err := io.ReadAll(response.Body)
	if err != nil {
		if len(staleData) > 0 {
			return staleData, nil
		}
		return nil, err
	}

	if err := writeCacheFile(cacheDir, cachePath, data); err != nil {
		return nil, err
	}
	_ = pruneCacheDir(cacheDir, metadataCachePolicy)

	return data, nil
}

func metadataCacheName(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(sum[:8]) + ".cache"
}

func readCachedMetadata(path string, ttl time.Duration) ([]byte, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}

	if ttl > 0 && time.Since(info.ModTime()) > ttl {
		return data, false, nil
	}

	return data, true, nil
}
