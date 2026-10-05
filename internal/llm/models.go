package llm

// models.go — G8: fetch daftar model dari endpoint /models (OpenAI-compatible).
//
// 9Router menyediakan GET {base}/models. Hasil di-cache 6 jam (disk-first:
// cache file JSON di dataDir, survives restart) agar menu Telegram tidak
// memanggil API tiap kali dibuka.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ModelInfo ringkasan model dari /models.
type ModelInfo struct {
	ID string `json:"id"`
}

// modelsResult bentuk respons /models.
type modelsResult struct {
	Data []ModelInfo `json:"data"`
}

const modelsCacheTTL = 6 * time.Hour
const modelsCacheFile = "model-cache.json"

// ListModels ambil daftar model: cache file dulu (jika segar), lalu API.
// return (ids, fromCache, error).
func (c *Client) ListModels(ctx context.Context, dataDir string) ([]string, bool, error) {
	// 1) coba cache
	if ids, ok := readModelsCache(dataDir); ok {
		return ids, true, nil
	}
	// 2) fetch API
	ids, err := c.fetchModels(ctx)
	if err != nil {
		// 3) fallback: cache basi pun lebih baik daripada gagal total
		if ids2, ok := readModelsCacheStale(dataDir); ok {
			return ids2, true, nil
		}
		return nil, false, err
	}
	writeModelsCache(dataDir, ids)
	return ids, false, nil
}

// fetchModels panggil GET {BaseURL}/models dengan timeout 15 dtk.
func (c *Client) fetchModels(ctx context.Context) ([]string, error) {
	fctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(fctx, "GET", strings.TrimRight(c.BaseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d dari /models", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // maks 2 MB
	if err != nil {
		return nil, err
	}
	var res modelsResult
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(res.Data))
	for _, m := range res.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return nil, fmt.Errorf("/models kosong")
	}
	return ids, nil
}

type modelsCache struct {
	FetchedAt time.Time `json:"fetched_at"`
	IDs       []string  `json:"ids"`
}

func cachePath(dataDir string) string { return filepath.Join(dataDir, modelsCacheFile) }

func readModelsCache(dataDir string) ([]string, bool) {
	b, err := os.ReadFile(cachePath(dataDir))
	if err != nil {
		return nil, false
	}
	var mc modelsCache
	if json.Unmarshal(b, &mc) != nil || len(mc.IDs) == 0 {
		return nil, false
	}
	if time.Since(mc.FetchedAt) > modelsCacheTTL {
		return nil, false
	}
	return mc.IDs, true
}

// readModelsCacheStale — cache basi (fallback saat API down).
func readModelsCacheStale(dataDir string) ([]string, bool) {
	b, err := os.ReadFile(cachePath(dataDir))
	if err != nil {
		return nil, false
	}
	var mc modelsCache
	if json.Unmarshal(b, &mc) != nil || len(mc.IDs) == 0 {
		return nil, false
	}
	return mc.IDs, true
}

func writeModelsCache(dataDir string, ids []string) {
	if dataDir == "" {
		return
	}
	mc := modelsCache{FetchedAt: time.Now(), IDs: ids}
	b, err := json.Marshal(mc)
	if err != nil {
		return
	}
	tmp := cachePath(dataDir) + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, cachePath(dataDir)) // atomik
	}
}
