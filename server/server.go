// Package server 提供 Web 服务模式，允许通过 HTTP GET 请求转换远程模块文件。
//
// 提供以下接口：
//   - GET /mitm.amrs  返回 MITM 规则（.amrs 格式，Anywhere 订阅要求 URL path 以 .amrs 结尾）
//   - GET /rule.arrs   返回路由规则（.arrs 格式，Anywhere 订阅要求 URL path 以 .arrs 结尾）
//   - GET /mitm        同 /mitm.amrs（兼容旧版）
//   - GET /rule        同 /rule.arrs（兼容旧版）
//   - GET /convert     统一转换接口（兼容旧版）
//   - GET /deeplink    返回 Anywhere 深度链接
//
// 所有接口的 url 参数需 URL 编码。响应为 text/plain，可直接被 Anywhere 订阅导入。
package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/H1d3r/module2anywhere/converter"
	"github.com/H1d3r/module2anywhere/fetcher"
	"github.com/H1d3r/module2anywhere/ir"
	"github.com/H1d3r/module2anywhere/parser"
)

// Config Web 服务配置。
type Config struct {
	Listen             string
	GeneralizeHost     bool
	FetchScripts       bool
	EncodingPreprocess bool
	IncludeMetadata    bool
	UseStreamScript    bool
	AutoContentType    bool
	ProxyMode          string
	ProxyRetry         bool
	Concurrency        int
	ScriptTimeoutSec   int
	MaxInputBytes      int64
	MaxScriptBytes     int64
	MaxScriptFetches   int
	PreserveParameters bool
}

// Run 启动 Web 服务。
func Run(cfg Config) error {
	srv := &Server{
		cfg:   cfg,
		cache: NewCache(5*time.Minute, 256), // 缓存 5 分钟，最多 256 条
	}
	mux := http.NewServeMux()
	// 带 .amrs/.arrs 后缀的路由（Anywhere 订阅要求 URL path 以对应扩展名结尾）
	mux.HandleFunc("/mitm.amrs", srv.handleMitm)
	mux.HandleFunc("/rule.arrs", srv.handleRule)
	mux.HandleFunc("/direct.arrs", srv.handleDirect)
	mux.HandleFunc("/reject.arrs", srv.handleReject)
	// 兼容旧版无后缀路由
	mux.HandleFunc("/mitm", srv.handleMitm)
	mux.HandleFunc("/rule", srv.handleRule)
	mux.HandleFunc("/convert", srv.handleConvert)
	mux.HandleFunc("/deeplink", srv.handleDeeplink)
	mux.HandleFunc("/script.js", srv.handleScript)
	mux.HandleFunc("/health", srv.handleHealth)

	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
	}
	return httpSrv.ListenAndServe()
}

// Server Web 服务实现。
type Server struct {
	cfg   Config
	cache *Cache
}

// handleHealth 健康检查。
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "ok")
}

func (s *Server) handleScript(w http.ResponseWriter, r *http.Request) {
	rawScript := r.URL.Query().Get("script")
	if rawScript == "" {
		http.Error(w, "Error: script parameter is required", http.StatusBadRequest)
		return
	}
	phase := 0
	if r.URL.Query().Get("phase") == "1" {
		phase = 1
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	f := fetcher.New()
	configureProxy(f, s.cfg.ProxyMode, s.cfg.ProxyRetry)
	baseURL := r.URL.Query().Get("base")
	wrap := r.URL.Query().Get("wrap") == "true"
	argument := r.URL.Query().Get("argument")
	maxScriptBytes := int64Query(r.URL.Query().Get("maxScriptBytes"), s.cfg.MaxScriptBytes)
	if maxScriptBytes <= 0 {
		maxScriptBytes = 1024 * 1024
	}
	source, err := converter.FetchAndRewriteScript(ctx, f, rawScript, baseURL, true, phase, false, wrap, argument, maxScriptBytes)
	if err != nil {
		http.Error(w, "Error: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(source))
}

// handleMitm 返回 MITM 规则（.amrs 格式）。
// 参数：url（必填）、name、fetch、generalize
func (s *Server) handleMitm(w http.ResponseWriter, r *http.Request) {
	result, err := s.convert(r, "amrs")
	if err != nil {
		http.Error(w, "Error: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.writeResultWithRequest(w, r, result.Amrs, result.AmrsName)
}

// handleRule 返回路由规则（.arrs 格式，不含 routing 字段）。
// 仅返回 PROXY 等非 DIRECT/非 REJECT 类路由规则。
// 参数：url（必填）、name
func (s *Server) handleRule(w http.ResponseWriter, r *http.Request) {
	result, err := s.convert(r, "arrs")
	if err != nil {
		http.Error(w, "Error: "+err.Error(), http.StatusBadRequest)
		return
	}
	// 优先返回 routing=0 的分组（PROXY 等非 DIRECT/非 REJECT 规则）
	group := findArrsGroup(result.ArrsGroups, 0)
	if group != nil && group.Content != "" {
		s.writeResultWithRequest(w, r, group.Content, group.Name)
		return
	}
	// 兼容：若无分组则返回合并版 arrs
	s.writeResultWithRequest(w, r, result.Arrs, result.ArrsName)
}

// handleDirect 返回 DIRECT 路由规则（.arrs 格式，routing=1）。
// 参数：url（必填）、name
func (s *Server) handleDirect(w http.ResponseWriter, r *http.Request) {
	result, err := s.convert(r, "arrs")
	if err != nil {
		http.Error(w, "Error: "+err.Error(), http.StatusBadRequest)
		return
	}
	group := findArrsGroup(result.ArrsGroups, 1)
	if group == nil || group.Content == "" {
		http.Error(w, "Error: no DIRECT routing rules in module", http.StatusNotFound)
		return
	}
	s.writeResultWithRequest(w, r, group.Content, group.Name)
}

// handleReject 返回 REJECT 路由规则（.arrs 格式，routing=2）。
// 参数：url（必填）、name
func (s *Server) handleReject(w http.ResponseWriter, r *http.Request) {
	result, err := s.convert(r, "arrs")
	if err != nil {
		http.Error(w, "Error: "+err.Error(), http.StatusBadRequest)
		return
	}
	group := findArrsGroup(result.ArrsGroups, 2)
	if group == nil || group.Content == "" {
		http.Error(w, "Error: no REJECT routing rules in module", http.StatusNotFound)
		return
	}
	s.writeResultWithRequest(w, r, group.Content, group.Name)
}

// handleConvert 统一转换接口（兼容旧版）。
// 参数：url（必填）、to（mitm/rule，默认 mitm）、name、fetch、generalize
func (s *Server) handleConvert(w http.ResponseWriter, r *http.Request) {
	to := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("to")))
	if to == "" {
		to = "mitm"
	}
	var format string
	switch to {
	case "mitm", "amrs":
		format = "amrs"
	case "rule", "arrs":
		format = "arrs"
	default:
		http.Error(w, "Error: Invalid 'to' parameter. Use: mitm/rule", http.StatusBadRequest)
		return
	}
	result, err := s.convert(r, format)
	if err != nil {
		http.Error(w, "Error: "+err.Error(), http.StatusBadRequest)
		return
	}
	if format == "amrs" {
		s.writeResultWithRequest(w, r, result.Amrs, result.AmrsName)
	} else {
		s.writeResultWithRequest(w, r, result.Arrs, result.ArrsName)
	}
}

// handleDeeplink 返回 Anywhere 深度链接（anywhere://add-rule-set）。
// 默认返回 302 跳转到深度链接；若 format=text 则返回纯文本。
// 参数：url（必填）、name、fetch、generalize、format
func (s *Server) handleDeeplink(w http.ResponseWriter, r *http.Request) {
	rawURL := r.URL.Query().Get("url")
	if rawURL == "" {
		http.Error(w, "Error: url parameter is required", http.StatusBadRequest)
		return
	}

	result, err := s.convertAll(r)
	if err != nil {
		http.Error(w, "Error: "+err.Error(), http.StatusBadRequest)
		return
	}

	links := make([]string, 0, 4)
	if result.Amrs != "" {
		links = append(links, buildEndpointURL(r, "/mitm.amrs", rawURL))
	}
	// 按 routing 分组生成 arrs 子链接
	for _, g := range result.ArrsGroups {
		if g.Content != "" {
			links = append(links, buildEndpointURL(r, g.Endpoint, rawURL))
		}
	}
	// 兼容：若无分组但有合并 arrs，仍生成 /rule.arrs
	if len(result.ArrsGroups) == 0 && result.Arrs != "" {
		links = append(links, buildEndpointURL(r, "/rule.arrs", rawURL))
	}
	if len(links) == 0 {
		http.Error(w, "Error: no rules to import", http.StatusNotFound)
		return
	}

	deepLink := "anywhere://add-rule-set?" + encodeLinks(links)
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "text" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(deepLink))
		return
	}
	http.Redirect(w, r, deepLink, http.StatusFound)
}

// convert 执行转换并按 format 校验，返回 Result。
// 支持 Quantumult X 一键订阅协议：若 url 是 quantumult.app add-resource 链接，
// 会先展开为多个远端订阅 URL 逐个转换后合并。
func (s *Server) convert(r *http.Request, format string) (*converter.Result, error) {
	merged, err := s.convertAll(r)
	if err != nil {
		return nil, err
	}
	// 若请求 arrs 但无内容，返回错误
	if format == "arrs" && merged.Arrs == "" {
		return nil, fmt.Errorf("no routing rules in module")
	}
	if format == "amrs" && merged.Amrs == "" {
		return nil, fmt.Errorf("no MITM rules in module")
	}
	return merged, nil
}

// convertAll 执行完整转换，同时返回 .amrs 与 .arrs 结果。
// 支持结果缓存：相同 URL+参数 在 TTL 内直接返回缓存结果，避免重复下载。
func (s *Server) convertAll(r *http.Request) (*converter.Result, error) {
	rawURL := r.URL.Query().Get("url")
	if rawURL == "" {
		return nil, fmt.Errorf("url parameter is required")
	}

	decodedURL, err := url.QueryUnescape(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL encoding: %w", err)
	}

	name := r.URL.Query().Get("name")
	// Web 服务默认开启脚本下载（避免每次都拿占位符）
	// 通过 fetch=false 显式关闭；默认 fetch=true
	fetchScripts := r.URL.Query().Get("fetch") != "false"
	// 默认 generalize=false（不泛化主机），通过 generalize=true 显式开启
	generalize := r.URL.Query().Get("generalize") == "true"
	wrapScripts := truthyQuery(r.URL.Query().Get("wrap"))
	preserveParameters := s.cfg.PreserveParameters || truthyQuery(r.URL.Query().Get("preserveParameters")) || truthyQuery(r.URL.Query().Get("preserveArguments"))
	arguments := argumentsFromQuery(r.URL.Query())
	scriptMode := normalizeScriptMode(r.URL.Query().Get("scriptMode"))
	maxInputBytes := int64Query(r.URL.Query().Get("maxInputBytes"), s.cfg.MaxInputBytes)
	if maxInputBytes <= 0 {
		maxInputBytes = 512 * 1024
	}
	maxScriptBytes := int64Query(r.URL.Query().Get("maxScriptBytes"), s.cfg.MaxScriptBytes)
	if maxScriptBytes <= 0 {
		maxScriptBytes = 1024 * 1024
	}
	maxScriptFetches := intQuery(r.URL.Query().Get("maxScriptFetches"), s.cfg.MaxScriptFetches)
	if maxScriptFetches <= 0 {
		maxScriptFetches = 45
	}

	// 检查缓存
	ck := cacheKey(decodedURL, name, fetchScripts, generalize, preserveParameters, wrapScripts, scriptMode, maxInputBytes, maxScriptBytes, maxScriptFetches, arguments)
	if cached, ok := s.cache.Get(ck); ok {
		var res converter.Result
		if err := res.UnmarshalBinary([]byte(cached)); err == nil {
			return &res, nil
		}
		// 反序列化失败，忽略缓存继续转换
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	f := fetcher.New()
	configureProxy(f, s.cfg.ProxyMode, s.cfg.ProxyRetry)

	// 展开输入：quantumult.app add-resource 协议会拆为多个 URL
	inputs, isAddResource, err := expandInputs(decodedURL)
	if err != nil {
		return nil, err
	}

	type modRes = serverModRes
	results := make([]modRes, 0, len(inputs))
	for _, in := range inputs {
		content, err := f.FetchWithLimit(ctx, in, maxInputBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch remote file: %w", err)
		}

		source := parser.DetectSource(content, filepath.Base(in))
		f.Source = source
		m, err := parser.Parse(content, source)
		if err != nil {
			return nil, fmt.Errorf("failed to parse module: %w", err)
		}

		if name != "" {
			m.Name = name
		} else if m.Name == "" {
			m.Name = deriveNameFromURL(in)
		}

		opts := converter.Options{
			GeneralizeHost:     generalize && s.cfg.GeneralizeHost,
			EncodingPreprocess: s.cfg.EncodingPreprocess,
			FetchScripts:       fetchScripts && s.cfg.FetchScripts,
			IncludeMetadata:    s.cfg.IncludeMetadata,
			UseStreamScript:    s.cfg.UseStreamScript,
			WrapScripts:        wrapScripts,
			AutoContentType:    s.cfg.AutoContentType,
			Concurrency:        s.cfg.Concurrency,
			ScriptTimeoutSec:   s.cfg.ScriptTimeoutSec,
			MaxScriptBytes:     maxScriptBytes,
			MaxScriptFetches:   maxScriptFetches,
			PreserveParameters: preserveParameters,
			Arguments:          arguments,
			ScriptMode:         scriptMode,
			ScriptBaseURL:      buildScriptServiceURL(r),
		}
		conv := converter.New(f, opts)
		conv.BaseURL = in
		// 记录来源 URL 与本服务 URL（用于在 .amrs/.arrs 头部添加注释）
		// 量子态 add-resource 链接时记录展开前的链接，其他远程 URL 记录原始 URL。
		if isAddResource {
			conv.SourceURL = decodedURL
		} else {
			conv.SourceURL = in
		}
		conv.ServiceURL = buildServiceURL(r)

		res, err := conv.Convert(ctx, m)
		if err != nil {
			return nil, fmt.Errorf("convert failed: %w", err)
		}
		results = append(results, modRes{mod: m, res: res})
	}

	merged := mergeServerResults(results)

	// 写入缓存
	if data, err := merged.MarshalBinary(); err == nil {
		s.cache.Put(ck, string(data))
	}

	return merged, nil
}

// writeResult 写入响应。
func (s *Server) writeResult(w http.ResponseWriter, content, filename string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%s", filename))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(content))
}

// writeResultWithRequest 写入转换结果，并动态修正 # this: 注释为当前请求的实际路径。
// 因为缓存的结果可能来自不同端点（如 /direct.arrs），但当前请求可能是 /reject.arrs。
func (s *Server) writeResultWithRequest(w http.ResponseWriter, r *http.Request, content, filename string) {
	// 动态替换 # this: 注释为当前请求的实际 URL
	actualURL := buildServiceURL(r)
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "# this: ") {
			lines[i] = "# this: " + actualURL
			break
		}
	}
	content = strings.Join(lines, "\n")
	s.writeResult(w, content, filename)
}

// deriveNameFromURL 从 URL 推导规则集名称。
func deriveNameFromURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "Unnamed"
	}
	path := parsed.Path
	parts := strings.Split(path, "/")
	filename := parts[len(parts)-1]
	for _, ext := range []string{".plugin", ".sgmodule", ".lpx", ".conf", ".list"} {
		filename = strings.TrimSuffix(filename, ext)
	}
	if filename == "" {
		filename = "Unnamed"
	}
	return filename
}

// configureProxy 根据字符串参数配置 fetcher 代理模式。
func configureProxy(f *fetcher.Fetcher, mode string, retry bool) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "none":
		f.Proxy.Mode = fetcher.ProxyModeNone
	case "only":
		f.Proxy.Mode = fetcher.ProxyModeOnly
	default:
		f.Proxy.Mode = fetcher.ProxyModeAuto
	}
	f.Proxy.RetryAll = retry
}

// expandInputs 展开 Quantumult X 一键订阅协议为多个远端 URL。
// 普通 URL 原样返回 [url]；quantumult.app add-resource 链接展开为内嵌的订阅列表。
// 同时返回 isAddResource 标志，标识是否走了一键订阅协议。
func expandInputs(input string) ([]string, bool, error) {
	if !fetcher.IsQuantumultXAddResourceURL(input) {
		return []string{input}, false, nil
	}
	urls, err := fetcher.ExtractQuantumultXResourceURLs(input)
	if err != nil {
		return nil, false, err
	}
	if len(urls) == 0 {
		return nil, false, fmt.Errorf("add-resource 链接未包含任何可下载的订阅 URL")
	}
	return urls, true, nil
}

// serverModRes 单 URL 转换结果。
type serverModRes struct {
	mod *ir.Module
	res *converter.Result
}

// mergeServerResults 合并多个 module 的转换结果。
func mergeServerResults(results []serverModRes) *converter.Result {
	if len(results) == 0 {
		return &converter.Result{Report: converter.Report{}}
	}
	if len(results) == 1 {
		return results[0].res
	}
	base := results[0].res
	report := base.Report
	for _, r := range results[1:] {
		base.Amrs = appendNonEmpty(base.Amrs, r.res.Amrs)
		base.Arrs = appendNonEmpty(base.Arrs, r.res.Arrs)
		report.Skipped = append(report.Skipped, r.res.Report.Skipped...)
		report.Degraded = append(report.Degraded, r.res.Report.Degraded...)
		report.ScriptErr = append(report.ScriptErr, r.res.Report.ScriptErr...)
	}
	base.Report = report
	return base
}

// appendNonEmpty 用换行把 b 拼到 a 后面，去掉重复的尾部换行。
func appendNonEmpty(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return strings.TrimRight(a, "\n") + "\n" + b
}

// buildServiceURL 推导本服务的可访问地址（用于在 .amrs/.arrs 头部添加「本链接」注释）。
// 优先级：
//  1. X-Forwarded-Proto + X-Forwarded-Host（反向代理场景）
//  2. r.TLS != nil（直接 HTTPS）→ https://<Host><RequestURI>
//  3. r.TLS == nil（直接 HTTP）  → http://<Host><RequestURI>
//
// 注意：RequestURI 可能含 query，但实际写入注释时只取 Host + URL.Path。
func buildServiceURL(r *http.Request) string {
	scheme, host := deriveSchemeHost(r)
	return scheme + "://" + host + r.URL.Path
}

func buildScriptServiceURL(r *http.Request) string {
	scheme, host := deriveSchemeHost(r)
	return scheme + "://" + host + "/script.js"
}

// deriveSchemeHost 从请求中推导 scheme 与 host，支持 X-Forwarded-* 头。
func deriveSchemeHost(r *http.Request) (string, string) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if xfHost := r.Header.Get("X-Forwarded-Host"); xfHost != "" {
		host = xfHost
	}
	if xfProto := r.Header.Get("X-Forwarded-Proto"); xfProto != "" {
		scheme = xfProto
	}
	return scheme, host
}

// buildEndpointURL 构造本服务指定端点的完整 URL，并保留原请求参数。
// rawURL 是用户传入的原始 url 参数值（将原样放入子链接的 url 参数）。
// 会自动剔除 deeplink 接口自身的 format 参数，避免污染子链接。
func buildEndpointURL(r *http.Request, path, rawURL string) string {
	scheme, host := deriveSchemeHost(r)
	q := r.URL.Query()
	q.Del("format")
	q.Set("url", rawURL)
	return scheme + "://" + host + path + "?" + q.Encode()
}

// encodeLinks 把多个规则集 URL 编码为 anywhere://add-rule-set 的 query 字符串。
// 每个 link 以 `link=...` 形式出现，保留传入顺序。
func encodeLinks(links []string) string {
	parts := make([]string, 0, len(links))
	for _, link := range links {
		parts = append(parts, "link="+url.QueryEscape(link))
	}
	return strings.Join(parts, "&")
}

// ensureIRImported 防止 ir 包被裁剪（占位）。
var _ = ir.SourceLoon

// findArrsGroup 在分组列表中查找指定 routing 值的分组。
func findArrsGroup(groups []converter.ArrsGroup, routing int) *converter.ArrsGroup {
	for i := range groups {
		if groups[i].Routing == routing {
			return &groups[i]
		}
	}
	return nil
}

func argumentsFromQuery(values url.Values) map[string]string {
	args := make(map[string]string)
	for key, vals := range values {
		var name string
		switch {
		case strings.HasPrefix(key, "argument."):
			name = strings.TrimPrefix(key, "argument.")
		case strings.HasPrefix(key, "arg."):
			name = strings.TrimPrefix(key, "arg.")
		default:
			continue
		}
		if !validQueryArgumentName(name) || len(vals) == 0 {
			continue
		}
		args[name] = vals[len(vals)-1]
	}
	return args
}

func truthyQuery(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func validQueryArgumentName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' || (i > 0 && r >= '0' && r <= '9') || (i > 0 && r == '-') {
			continue
		}
		return false
	}
	return true
}

// cacheKey 生成缓存键，由 URL + 参数组合而成。
func normalizeScriptMode(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "loader") {
		return "loader"
	}
	return "inline"
}

func cacheKey(decodedURL, name string, fetchScripts, generalize, preserveParameters, wrapScripts bool, scriptMode string, maxInputBytes, maxScriptBytes int64, maxScriptFetches int, arguments map[string]string) string {
	var argParts []string
	for key, value := range arguments {
		argParts = append(argParts, key+"="+value)
	}
	sort.Strings(argParts)
	return fmt.Sprintf("%s|%s|%v|%v|%v|%v|%s|%d|%d|%d|%s", decodedURL, name, fetchScripts, generalize, preserveParameters, wrapScripts, scriptMode, maxInputBytes, maxScriptBytes, maxScriptFetches, strings.Join(argParts, "&"))
}

func int64Query(value string, fallback int64) int64 {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

func intQuery(value string, fallback int) int {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return n
}
