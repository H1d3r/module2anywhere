// Package fetcher 负责从本地或远程加载 Loon/Surge 模块文件与脚本 JS 文件。
//
// 设计要点：
//   - 本地路径直接读取
//   - http(s) URL 走 HTTP 客户端，带 UA 与超时
//   - 提供 ScriptCache 以避免同一脚本被重复下载
package fetcher

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Fetcher 统一的资源加载器。
type Fetcher struct {
	Client    *http.Client
	UserAgent string
	cache     *ScriptCache
}

// New 创建默认 Fetcher。
func New() *Fetcher {
	return &Fetcher{
		Client: &http.Client{
			Timeout: 30 * time.Second,
		},
		UserAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		cache:     NewScriptCache(),
	}
}

// ScriptCache 缓存已下载的脚本内容，避免重复请求。
type ScriptCache struct {
	mu    sync.RWMutex
	items map[string]string
}

// NewScriptCache 创建空缓存。
func NewScriptCache() *ScriptCache {
	return &ScriptCache{items: make(map[string]string)}
}

// Get 读取缓存。
func (c *ScriptCache) Get(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.items[key]
	return v, ok
}

// Put 写入缓存。
func (c *ScriptCache) Put(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = value
}

// IsRemote 判断路径是否为远程 URL。
func IsRemote(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// Fetch 加载资源内容。本地路径直接读，远程 URL 走 HTTP。
func (f *Fetcher) Fetch(ctx context.Context, path string) (string, error) {
	if !IsRemote(path) {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("读取本地文件失败 %q: %w", path, err)
		}
		return string(b), nil
	}
	return f.fetchRemote(ctx, path)
}

// FetchScript 下载脚本 JS。命中缓存则直接返回。
func (f *Fetcher) FetchScript(ctx context.Context, url string) (string, error) {
	if v, ok := f.cache.Get(url); ok {
		return v, nil
	}
	src, err := f.fetchRemote(ctx, url)
	if err != nil {
		return "", err
	}
	f.cache.Put(url, src)
	return src, nil
}

// fetchRemote 通过 HTTP GET 拉取远程内容。
func (f *Fetcher) fetchRemote(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("构造请求失败 %q: %w", url, err)
	}
	if f.UserAgent != "" {
		req.Header.Set("User-Agent", f.UserAgent)
	}
	req.Header.Set("Accept", "*/*")

	resp, err := f.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求远程资源失败 %q: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("远程资源 %q 返回非 2xx 状态码: %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取远程响应失败 %q: %w", url, err)
	}
	return string(body), nil
}

// ResolveScriptPath 将 script-path 解析为可下载的 URL。
// 支持绝对 URL、相对路径（基于 base URL 解析）。
func (f *Fetcher) ResolveScriptPath(scriptPath, baseURL string) string {
	if IsRemote(scriptPath) {
		return scriptPath
	}
	// 本地路径：直接返回（后续 Fetch 会读本地）
	if baseURL == "" {
		return scriptPath
	}
	// 若 scriptPath 是相对路径且 base 是远程 URL，尝试拼接
	if IsRemote(baseURL) && !filepath.IsAbs(scriptPath) && !strings.HasPrefix(scriptPath, "/") {
		// 简单以 base URL 的目录为前缀
		idx := strings.LastIndex(baseURL, "/")
		if idx > 0 {
			return baseURL[:idx+1] + scriptPath
		}
	}
	return scriptPath
}
