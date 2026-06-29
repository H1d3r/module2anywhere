// Package fetcher 负责从本地或远程加载 Loon/Surge 模块文件与脚本 JS 文件。
//
// 设计要点：
//   - 本地路径直接读取
//   - http(s) URL 走 HTTP 客户端，带 UA 与超时
//   - 对 GitHub 原始域名自动使用加速代理（ghfast.top / ph.ipv9.win），失败回退直连
//   - 按来源格式自动选择 User-Agent（Loon 用 Loon UA，Surge 用 Shadowrocket UA）
//   - 提供 ScriptCache 以避免同一脚本被重复下载
package fetcher

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/H1d3r/module2anywhere/ir"
)

// 常用 User-Agent 列表。
const (
	// UserAgentLoon 模拟 Loon 客户端发起的请求
	UserAgentLoon = "Loon/3.4.1 (iPhone; iOS 17.0; Scale/3.00)"
	// UserAgentShadowrocket 模拟 Shadowrocket 客户端发起的请求（Surge 生态兼容）
	UserAgentShadowrocket = "Shadowrocket/2780 CFNetwork/1490 Darwin/24.0.0"
	// UserAgentSurge 模拟 Surge 客户端发起的请求
	UserAgentSurge = "Surge/2965 (Macintosh; OS X 14.5; Build 23F79)"
	// UserAgentQuantumultX 模拟 Quantumult X 客户端（兼容部分 Loon 插件）
	UserAgentQuantumultX = "Quantumult%20X%20Patched/1.0.30 (iPhone;iOS%2017.0)"
	// UserAgentDefault 通用浏览器 UA（兜底）
	UserAgentDefault = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

// ProxyMode 控制 GitHub 加速代理使用策略。
type ProxyMode int

const (
	ProxyModeAuto ProxyMode = iota // 自动尝试代理列表，失败回退直连（默认）
	ProxyModeNone                  // 禁用代理，直连
	ProxyModeOnly                  // 仅使用代理，不回退直连
)

// ProxyConfig 代理配置。
type ProxyConfig struct {
	Mode     ProxyMode
	Proxies  []string // 代理前缀列表，如 ["https://ghfast.top/", "https://ph.ipv9.win/"]
	RetryAll bool     // 代理失败时尝试所有备用代理（默认 true）
}

// DefaultProxyConfig 返回默认代理配置（ghfast.top → ph.ipv9.win → 直连）。
func DefaultProxyConfig() ProxyConfig {
	return ProxyConfig{
		Mode:     ProxyModeAuto,
		Proxies:  []string{"https://ghfast.top/", "https://ph.ipv9.win/"},
		RetryAll: true,
	}
}

// Fetcher 统一的资源加载器。
type Fetcher struct {
	Client    *http.Client
	UserAgent string
	cache     *ScriptCache
	Proxy     ProxyConfig
	// Source 当前转换目标格式，用于动态选择 UA
	Source ir.Source
}

// New 创建默认 Fetcher。
func New() *Fetcher {
	return &Fetcher{
		Client: &http.Client{
			Timeout: 10 * time.Second,
		},
		UserAgent: UserAgentDefault,
		cache:     NewScriptCache(),
		Proxy:     DefaultProxyConfig(),
	}
}

// UserAgentForSource 根据来源格式返回合适的 User-Agent。
//   - Loon        → UserAgentLoon
//   - Surge       → UserAgentShadowrocket（Surge 模块上游多为 Shadowrocket 分发）
//   - QuantumultX → UserAgentQuantumultX
//   - 未知        → UserAgentDefault
func UserAgentForSource(source ir.Source) string {
	switch source {
	case ir.SourceLoon:
		return UserAgentLoon
	case ir.SourceSurge:
		return UserAgentShadowrocket
	case ir.SourceQuantumultX:
		return UserAgentQuantumultX
	default:
		return UserAgentDefault
	}
}

// UserAgentForFilename 按文件名后缀推导 User-Agent。
// 用于下载时尚未解析、只能依据文件名的场景。
//   - .plugin / .lpx → Loon UA（.lpx 是 Loon 插件的 XML 格式压缩包）
//   - .sgmodule      → Shadowrocket UA（Surge 生态常用）
//   - .conf          → QuantumultX UA
//   - 其他           → 浏览器默认 UA
func UserAgentForFilename(filename string) string {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".plugin"), strings.HasSuffix(lower, ".lpx"):
		return UserAgentLoon
	case strings.HasSuffix(lower, ".sgmodule"):
		return UserAgentShadowrocket
	case strings.HasSuffix(lower, ".conf"):
		return UserAgentQuantumultX
	default:
		return UserAgentDefault
	}
}

// resolveUserAgent 综合 source、filename、显式 UA 决定最终 UA。
// 优先级：显式 UA > Source > 文件名后缀 > 默认浏览器 UA。
func (f *Fetcher) resolveUserAgent(filename string) string {
	if f.UserAgent != "" && f.UserAgent != UserAgentDefault {
		return f.UserAgent
	}
	if f.Source != ir.SourceUnknown {
		return UserAgentForSource(f.Source)
	}
	return UserAgentForFilename(filename)
}

// ScriptCache 缓存已下载的脚本内容，避免重复请求。
// 带 TTL 与容量上限，防止长期运行时内存单调增长（与 server.Cache 语义对齐）。
type ScriptCache struct {
	mu      sync.RWMutex
	items   map[string]*scriptEntry
	ttl     time.Duration
	maxSize int
	stop    chan struct{}
}

// scriptEntry 脚本缓存条目。
type scriptEntry struct {
	value     string
	expiresAt time.Time
}

// expired 判断条目是否已过期。
func (e *scriptEntry) expired() bool {
	return time.Now().After(e.expiresAt)
}

// NewScriptCache 创建带 TTL 与容量上限的脚本缓存。
// 默认 TTL 10 分钟（脚本相对静态），maxSize 512（脚本数量通常多于转换结果）。
func NewScriptCache() *ScriptCache {
	return NewScriptCacheWith(10*time.Minute, 512)
}

// NewScriptCacheWith 自定义 TTL 与 maxSize 创建缓存。
func NewScriptCacheWith(ttl time.Duration, maxSize int) *ScriptCache {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	if maxSize <= 0 {
		maxSize = 512
	}
	c := &ScriptCache{
		items:   make(map[string]*scriptEntry, 64),
		ttl:     ttl,
		maxSize: maxSize,
		stop:    make(chan struct{}),
	}
	go c.evictLoop()
	return c
}

// Get 读取缓存，未命中或已过期返回空字符串和 false。
func (c *ScriptCache) Get(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.items[key]
	if !ok || e.expired() {
		return "", false
	}
	return e.value, true
}

// Put 写入缓存。
func (c *ScriptCache) Put(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) >= c.maxSize {
		c.evictLocked()
	}
	c.items[key] = &scriptEntry{
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// Close 停止后台淘汰协程。允许重复调用。
func (c *ScriptCache) Close() {
	select {
	case <-c.stop:
		// 已关闭
	default:
		close(c.stop)
	}
}

// evictLoop 定期清理过期条目，直到缓存被关闭。
func (c *ScriptCache) evictLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			c.mu.Lock()
			c.evictLocked()
			c.mu.Unlock()
		}
	}
}

// evictLocked 在已持锁状态下淘汰过期和最旧条目。
func (c *ScriptCache) evictLocked() {
	// 先删过期
	for k, e := range c.items {
		if e.expired() {
			delete(c.items, k)
		}
	}
	// 仍超容量则按过期时间排序淘汰最旧的
	for len(c.items) > c.maxSize {
		var oldestKey string
		var oldestTime time.Time
		first := true
		for k, e := range c.items {
			if first || e.expiresAt.Before(oldestTime) {
				oldestKey = k
				oldestTime = e.expiresAt
				first = false
			}
		}
		if oldestKey == "" {
			break
		}
		delete(c.items, oldestKey)
	}
}

// IsRemote 判断路径是否为远程 URL。
func IsRemote(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// githubHosts 已知的 GitHub 原始内容域名集合。
// 使用 map + 后缀匹配避免 strings.Contains 误匹配（如 example.com/redirect/github.com/foo）。
var githubHosts = map[string]bool{
	"raw.githubusercontent.com":  true,
	"github.com":                 true,
	"gist.githubusercontent.com": true,
	"codeload.github.com":        true,
}

// isGitHubURL 检测 URL 是否为 GitHub 原始内容域名。
// 通过解析 URL 的 host 字段精确匹配，避免路径中含 github.com 子串导致误判。
func isGitHubURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if githubHosts[host] {
		return true
	}
	// 支持子域：xxx.githubusercontent.com / gist.xxx.github.com 等
	for h := range githubHosts {
		if strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// buildProxyURL 构造代理 URL：proxy + 原始 URL（去除 https:// 前缀）。
// 示例：https://raw.githubusercontent.com/... → https://ghfast.top/raw.githubusercontent.com/...
func buildProxyURL(proxyPrefix, rawURL string) string {
	return proxyPrefix + strings.TrimPrefix(rawURL, "https://")
}

// Fetch 加载资源内容。本地路径直接读，远程 URL 走 HTTP（GitHub 自动代理）。
// 按文件名后缀选择 User-Agent：.plugin/.lpx 用 Loon UA，.sgmodule 用 Shadowrocket UA，.conf 用 QuantumultX UA。
func (f *Fetcher) Fetch(ctx context.Context, path string) (string, error) {
	if !IsRemote(path) {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("读取本地文件失败 %q: %w", path, err)
		}
		return string(b), nil
	}
	filename := filepath.Base(path)
	return f.fetchRemoteWithProxy(ctx, path, filename)
}

// FetchScript 下载脚本 JS。命中缓存则直接返回。
// 若已设置 f.Source（解析阶段识别了模块来源），则按 source 选择 UA。
// 否则按脚本 URL 路径后缀选择 UA。
func (f *Fetcher) FetchScript(ctx context.Context, url string) (string, error) {
	if v, ok := f.cache.Get(url); ok {
		return v, nil
	}
	src, err := f.fetchRemoteWithProxy(ctx, url, filepath.Base(url))
	if err != nil {
		return "", err
	}
	f.cache.Put(url, src)
	return src, nil
}

// fetchRemoteWithProxy 通过 HTTP GET 拉取远程内容，对 GitHub URL 自动应用加速代理。
// filename 用于按后缀选择 UA（仅在显式 UA 未设置时生效）。
func (f *Fetcher) fetchRemoteWithProxy(ctx context.Context, url, filename string) (string, error) {
	ua := f.resolveUserAgent(filename)
	// 非 GitHub URL 直接请求
	if !isGitHubURL(url) {
		return f.fetchRemoteWithUA(ctx, url, ua)
	}

	// GitHub URL：根据代理模式处理
	switch f.Proxy.Mode {
	case ProxyModeNone:
		// 禁用代理，直连
		return f.fetchRemoteWithUA(ctx, url, ua)
	case ProxyModeOnly:
		// 仅使用代理，不回退
		if len(f.Proxy.Proxies) == 0 {
			return f.fetchRemoteWithUA(ctx, url, ua)
		}
		for _, proxy := range f.Proxy.Proxies {
			proxyURL := buildProxyURL(proxy, url)
			data, err := f.fetchRemoteWithUA(ctx, proxyURL, ua)
			if err == nil {
				return data, nil
			}
		}
		return "", fmt.Errorf("所有代理均失败: %s", url)
	case ProxyModeAuto:
		// 自动模式：依次尝试代理，失败回退直连
		var lastErr error
		for _, proxy := range f.Proxy.Proxies {
			proxyURL := buildProxyURL(proxy, url)
			data, err := f.fetchRemoteWithUA(ctx, proxyURL, ua)
			if err == nil {
				return data, nil
			}
			lastErr = err
			if !f.Proxy.RetryAll {
				break
			}
		}
		// 所有代理失败，回退直连
		data, err := f.fetchRemoteWithUA(ctx, url, ua)
		if err == nil {
			return data, nil
		}
		return "", fmt.Errorf("代理与直连均失败 (代理最后错误: %v, 直连错误: %w)", lastErr, err)
	default:
		return f.fetchRemoteWithUA(ctx, url, ua)
	}
}

// fetchRemote 通过 HTTP GET 拉取远程内容（使用 Fetcher 自身 UA）。
func (f *Fetcher) fetchRemote(ctx context.Context, url string) (string, error) {
	return f.fetchRemoteWithUA(ctx, url, f.UserAgent)
}

// fetchRemoteWithUA 通过 HTTP GET 拉取远程内容，使用指定 UA。
// 自动处理 gzip 响应以减少传输量。
func (f *Fetcher) fetchRemoteWithUA(ctx context.Context, url, ua string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("构造请求失败 %q: %w", url, err)
	}
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	} else if f.UserAgent != "" {
		req.Header.Set("User-Agent", f.UserAgent)
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Encoding", "gzip") // 请求 gzip 压缩以减少传输量

	resp, err := f.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求远程资源失败 %q: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("远程资源 %q 返回非 2xx 状态码: %d", url, resp.StatusCode)
	}

	// 自动解压 gzip 响应
	var reader io.Reader = resp.Body
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return "", fmt.Errorf("解压 gzip 失败 %q: %w", url, err)
		}
		defer gz.Close()
		reader = gz
	}

	body, err := io.ReadAll(reader)
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

// IsQuantumultXAddResourceURL 判断给定 URL 是否为 Quantumult X 的「add-resource」协议链接。
// 形式：https://quantumult.app/x/open-app/add-resource?remote-resource=<encoded-json>
//
// 客户端用此链接把远端重写/服务器/过滤/任务订阅嵌入到一行可点击 URL，
// 任何订阅 URL 都可以通过这个链接被一键导入 Quantumult X。
func IsQuantumultXAddResourceURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Host)
	if !strings.HasSuffix(host, "quantumult.app") {
		return false
	}
	// 路径必须以 /x/open-app/add-resource 开头（允许带或不带斜杠）
	p := u.Path
	if strings.HasSuffix(p, "/") {
		p = strings.TrimSuffix(p, "/")
	}
	return p == "/x/open-app/add-resource"
}

// ExtractQuantumultXResourceURLs 从 quantumult.app add-resource 链接展开远端订阅 URL 列表。
//
// 过程：
//  1. URL 解码 remote-resource 参数
//  2. 解析 JSON，遍历以下 key：
//     - rewrite_remote: 重写订阅
//     - server_remote:  服务器订阅
//     - filter_remote:  过滤订阅
//     - task_remote:    任务订阅
//  3. 数组中每项形如 "https://xxx/yyy.js, tag=..., update-interval=..., opt-parser=..., enabled=..."，
//     只取 URL 主体（第一个逗号之前的部分）
//
// 参考：Quantumult X 一键订阅资源协议
func ExtractQuantumultXResourceURLs(rawURL string) ([]string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("URL 解析失败: %w", err)
	}
	raw := u.Query().Get("remote-resource")
	if raw == "" {
		return nil, fmt.Errorf("缺少 remote-resource 参数")
	}
	// remote-resource 本身也可能被 URL 编码一次
	if decoded, err := url.QueryUnescape(raw); err == nil && decoded != "" {
		raw = decoded
	}

	var payload struct {
		RewriteRemote []string `json:"rewrite_remote"`
		ServerRemote  []string `json:"server_remote"`
		FilterRemote  []string `json:"filter_remote"`
		TaskRemote    []string `json:"task_remote"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("remote-resource JSON 解析失败: %w", err)
	}

	all := append([]string{}, payload.RewriteRemote...)
	all = append(all, payload.ServerRemote...)
	all = append(all, payload.FilterRemote...)
	all = append(all, payload.TaskRemote...)

	var urls []string
	for _, entry := range all {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		// 形如 "https://xxx/yyy.js, tag=..., opt-parser=true, enabled=true"
		if idx := strings.Index(entry, ","); idx > 0 {
			entry = entry[:idx]
		}
		entry = strings.TrimSpace(entry)
		// 必须以 http(s):// 开头
		if !strings.HasPrefix(entry, "http://") && !strings.HasPrefix(entry, "https://") {
			continue
		}
		urls = append(urls, entry)
	}
	return urls, nil
}
