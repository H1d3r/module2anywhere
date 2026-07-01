// Package converter 将 IR Module 转换为 Anywhere .arrs / .amrs 规则集。
//
// 主要子模块：
//   - urlpattern.go: URL 正则模式转换（去 \/ 转义、主机泛化、结尾锚定）
//   - script.go:     脚本 API 改写 + base64 编码
//   - converter.go:  核心转换器，串联上述工具产出最终规则行
package converter

import (
	"regexp"
	"strings"
)

// Options 控制转换器行为。
type Options struct {
	// GeneralizeHost 是否将 URL pattern 中的主机部分泛化为 [^/]+。
	// 出于安全考虑（避免匹配到不相关域名），仅当显式开启且 pattern 主机已被 hostname 列表覆盖时才会泛化。
	// 默认 false（保留原始主机）。
	GeneralizeHost      bool
	EncodingPreprocess  bool // 为 body 处理规则自动添加 accept-encoding 预处理对（默认 true）
	FetchScripts        bool // 远程下载脚本并改写（默认 true；false 时仅生成占位符）
	IncludeMetadata     bool // 在输出文件头部写入 desc/author 等注释（默认 true）
	UseStreamScript     bool // 将脚本转为 stream-script (op 101)，用于流式响应处理（默认 false）
	WrapScripts         bool // 包装执行模式：将上游脚本源码原样编码，运行时构造兼容全局变量后执行（默认 true）
	AutoContentType     bool // 兼容旧参数；官方 Anywhere 当前不识别顶层 content-type，转换器不再输出该头
	Concurrency         int  // 并发下载脚本数（默认 8）
	ScriptTimeoutSec    int  // 单个脚本下载超时（秒，默认 10）
	MaxScriptBytes      int64
	MaxTotalScriptBytes int64
	MaxScriptFetches    int
	Arguments           map[string]string
	PreserveParameters  bool // 在 .amrs 中保留 [Parameter] 段（默认 false）
	ScriptMode          string
	ScriptBaseURL       string // loader 模式下远程脚本端点基地址
}

// DefaultOptions 返回推荐默认值。
// 注意：GeneralizeHost 默认 false，因为把 pattern 主机泛化为 [^/]+ 会导致
// "所有 HTTPS 域名都匹配"，存在严重安全隐患（hostname 字段不全时尤其危险）。
// 如需泛化请显式 --generalize-host，且仅在确认 hostname 字段已覆盖所有 pattern 主机时使用。
func DefaultOptions() Options {
	return Options{
		GeneralizeHost:      false,
		EncodingPreprocess:  true,
		FetchScripts:        true,
		IncludeMetadata:     true,
		UseStreamScript:     false,
		WrapScripts:         true,
		AutoContentType:     true,
		Concurrency:         8,
		ScriptTimeoutSec:    10,
		MaxScriptBytes:      1024 * 1024,
		MaxTotalScriptBytes: 5 * 1024 * 1024,
		MaxScriptFetches:    45,
	}
}

// ConvertURLPattern 转换 URL 正则模式以适配 Anywhere。
// 步骤：
//  1. \/ → /（去除不必要的转义）
//  2. （可选）安全主机泛化：仅当显式开启且 pattern 中所有具体主机都被 hostnames 覆盖时才执行
//  3. 结尾 \? → (?:\?|$)
func ConvertURLPattern(pattern string, generalize bool) string {
	return ConvertURLPatternWithHostnames(pattern, generalize, nil)
}

// ConvertURLPatternWithHostnames 转换 URL 正则模式，已知 hostname 列表时可启用安全主机泛化。
// 主机泛化的安全条件：
//   - 显式启用 generalize
//   - pattern 主机段不含通配形式（.*/[^/]+/等）
//   - pattern 中每个具体主机都已被 hostnames 列表覆盖（精确匹配或子域）
func ConvertURLPatternWithHostnames(pattern string, generalize bool, hostnames []string) string {
	if pattern == "" {
		return pattern
	}
	// 1. 去除 \/ 转义
	pattern = strings.ReplaceAll(pattern, `\/`, `/`)

	// 2. 安全主机泛化
	if generalize {
		pattern = safeGeneralizeHost(pattern, hostnames)
	}

	// 3. 结尾 \? → (?:\?|$)
	if strings.HasSuffix(pattern, `\?`) {
		pattern = strings.TrimSuffix(pattern, `\?`) + `(?:\?|$)`
	}
	return pattern
}

// InferHostnameSuffixesFromPattern 从 AMRS URL pattern 中推断 Anywhere 可表达的 hostname 后缀。
// 仅返回保守候选：固定主机、简单 alternation 主机、以及 regex 主机中的可识别品牌后缀。
func InferHostnameSuffixesFromPattern(pattern string) []string {
	var out []string
	for _, patternPart := range splitTopLevelAlternation(pattern) {
		p := unwrapNonCapture(strings.TrimSpace(patternPart))
		hostPart := urlPatternHostPart(p)
		if hostPart == "" {
			continue
		}
		hostPart = strings.TrimPrefix(hostPart, "?")
		hostPart = unwrapNonCapture(hostPart)
		hostPart = unwrapCapture(hostPart)
		for _, part := range expandLeadingHostGroup(hostPart) {
			out = appendHostnameCandidate(out, inferHostnameSuffixFromHostPattern(part))
		}
	}
	return out
}

// HasComplexHostnamePattern 判断 URL pattern 的主机段是否含 Anywhere hostname 无法直接表达的正则。
func HasComplexHostnamePattern(pattern string) bool {
	for _, patternPart := range splitTopLevelAlternation(pattern) {
		p := unwrapNonCapture(strings.TrimSpace(patternPart))
		hostPart := urlPatternHostPart(p)
		if hostPart == "" {
			continue
		}
		hostPart = unwrapCapture(unwrapNonCapture(strings.TrimPrefix(hostPart, "?")))
		if strings.ContainsAny(hostPart, "*+?[]()|") || strings.Contains(hostPart, `\d`) {
			return true
		}
	}
	return false
}

func unwrapNonCapture(s string) string {
	if strings.HasPrefix(s, "(?:") && strings.HasSuffix(s, ")") {
		if close := findMatchingParen(s[3:]); close == len(s)-4 {
			return s[3 : len(s)-1]
		}
	}
	return s
}

func unwrapCapture(s string) string {
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") && !strings.HasPrefix(s, "(?") {
		if close := findMatchingParen(s[1:]); close == len(s)-2 {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func expandLeadingHostGroup(s string) []string {
	if !strings.HasPrefix(s, "(") {
		return splitTopLevelAlternation(s)
	}
	innerStart := 1
	if strings.HasPrefix(s, "(?:") {
		innerStart = 3
	}
	close := findMatchingParen(s[innerStart:])
	if close < 0 {
		return splitTopLevelAlternation(s)
	}
	closeAbs := innerStart + close
	suffix := s[closeAbs+1:]
	alts := splitTopLevelAlternation(s[innerStart:closeAbs])
	out := make([]string, 0, len(alts))
	for _, alt := range alts {
		out = append(out, alt+suffix)
	}
	return out
}

func urlPatternHostPart(pattern string) string {
	pattern = strings.ReplaceAll(pattern, `\\/`, `/`)
	pattern = strings.ReplaceAll(pattern, `\/`, `/`)
	for _, marker := range []string{"://", `:\/\/`} {
		idx := strings.Index(pattern, marker)
		if idx < 0 {
			continue
		}
		rest := pattern[idx+len(marker):]
		if rest == "" {
			return ""
		}
		if strings.HasPrefix(rest, "(?:") {
			if close := findMatchingParen(rest[3:]); close >= 0 {
				end := 3 + close + 1
				if end < len(rest) && strings.HasPrefix(rest[end:], `\.`) {
					end += 2
					for end < len(rest) && isHostPatternByte(rest[end]) {
						end++
					}
				}
				if end < len(rest) && rest[end] == ':' {
					end++
					for end < len(rest) && (isDigitByte(rest[end]) || rest[end] == '\\' || rest[end] == 'd' || rest[end] == '+') {
						end++
					}
				}
				return rest[:end]
			}
		}
		end := 0
		for end < len(rest) && rest[end] != '/' {
			end++
		}
		return rest[:end]
	}
	return ""
}

func inferHostnameSuffixFromHostPattern(part string) string {
	part = strings.TrimSpace(part)
	if part == "" {
		return ""
	}
	part = strings.TrimPrefix(part, "^")
	part = strings.TrimSuffix(part, "$")
	part = strings.TrimPrefix(part, "(?:")
	part = strings.TrimSuffix(part, ")")
	part = stripRegexPort(part)
	if !hasStaticHostnameTail(part) {
		return ""
	}
	part = strings.ReplaceAll(part, `\.`, ".")
	part = strings.ReplaceAll(part, `\-`, "-")
	part = strings.ReplaceAll(part, `\_`, "_")
	part = strings.ReplaceAll(part, "[A-Za-z0-9-]+", "")
	part = strings.ReplaceAll(part, "[a-zA-Z0-9-]+", "")
	part = strings.ReplaceAll(part, "[a-z0-9-]+", "")
	part = strings.ReplaceAll(part, "[0-9]+", "")
	part = strings.ReplaceAll(part, ".*", "")
	part = strings.ReplaceAll(part, ".+", "")
	part = strings.ReplaceAll(part, "*", "")
	part = strings.ReplaceAll(part, "+", "")
	part = strings.ReplaceAll(part, "?", "")
	part = strings.Trim(part, ".")
	if strings.ContainsAny(part, `(){}|^$/`) {
		if idx := strings.LastIndex(part, "."); idx >= 0 && idx+1 < len(part) {
			part = part[idx+1:]
		} else {
			return ""
		}
	}
	labels := strings.Split(part, ".")
	filtered := labels[:0]
	for _, label := range labels {
		label = strings.Trim(label, "-")
		if label != "" {
			filtered = append(filtered, label)
		}
	}
	if len(filtered) < 2 {
		return ""
	}
	originalLabels := append([]string(nil), filtered...)
	registrable := registrableHostnameSuffix(originalLabels)
	if registrable == "" {
		return ""
	}
	if len(originalLabels) == 2 && strings.ContainsAny(originalLabels[0], `\[]()+*?`) {
		return ""
	}
	filtered = strings.Split(registrable, ".")
	for _, label := range filtered {
		if strings.ContainsAny(label, `\[]()+*?`) {
			return ""
		}
	}
	host := strings.ToLower(registrable)
	if isUnsafeInferredHostnameSuffix(host) {
		return ""
	}
	return host
}

func hasStaticHostnameTail(hostPattern string) bool {
	raw := strings.ReplaceAll(hostPattern, `\\.`, ".")
	raw = strings.ReplaceAll(raw, `\.`, ".")
	raw = strings.ReplaceAll(raw, `\-`, "-")
	labels := strings.Split(raw, ".")
	filtered := labels[:0]
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label != "" {
			filtered = append(filtered, label)
		}
	}
	if len(filtered) < 2 {
		return false
	}
	for _, label := range filtered[len(filtered)-2:] {
		if strings.ContainsAny(label, `\[]()+*?`) {
			return false
		}
	}
	return true
}

func splitTopLevelAlternation(s string) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case '|':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func stripRegexPort(s string) string {
	idx := strings.LastIndex(s, ":")
	if idx < 0 {
		return s
	}
	port := s[idx+1:]
	if port == "" {
		return s
	}
	for _, r := range port {
		if (r >= '0' && r <= '9') || r == '\\' || r == 'd' || r == '+' {
			continue
		}
		return s
	}
	return s[:idx]
}

func appendHostnameCandidate(hosts []string, host string) []string {
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" || strings.ContainsAny(host, "*?/ \\") || isUnsafeInferredHostnameSuffix(host) {
		return hosts
	}
	for _, existing := range hosts {
		if existing == host {
			return hosts
		}
	}
	return append(hosts, host)
}

func isUnsafeInferredHostnameSuffix(host string) bool {
	if !strings.Contains(host, ".") {
		return true
	}
	if isIPLiteral(host) {
		return true
	}
	for _, r := range host {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '-' {
			continue
		}
		return true
	}
	switch host {
	case "com", "net", "org", "top", "cn", "tv", "cc", "io", "app", "co", "me", "xyz", "site", "vip":
		return true
	}
	return false
}

func registrableHostnameSuffix(labels []string) string {
	labels = sanitizeHostnameLabels(labels)
	if len(labels) < 2 || allNumericLabels(labels) {
		return ""
	}
	keep := 2
	if len(labels) >= 3 && isTwoPartPublicSuffix(labels[len(labels)-2], labels[len(labels)-1]) {
		keep = 3
	}
	if len(labels) < keep {
		return ""
	}
	return strings.Join(labels[len(labels)-keep:], ".")
}

func sanitizeHostnameLabels(labels []string) []string {
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		label = strings.TrimSpace(strings.Trim(label, "-"))
		if label != "" {
			out = append(out, label)
		}
	}
	return out
}

func allNumericLabels(labels []string) bool {
	if len(labels) == 0 {
		return false
	}
	for _, label := range labels {
		for _, r := range label {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func isIPLiteral(host string) bool {
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	if strings.Contains(host, ":") {
		for _, r := range host {
			if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || r == ':' || r == '.' {
				continue
			}
			return false
		}
		return strings.Contains(host, ":")
	}
	parts := strings.Split(host, ".")
	if len(parts) != 4 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 3 {
			return false
		}
		n := 0
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
			n = n*10 + int(r-'0')
		}
		if n > 255 {
			return false
		}
	}
	return true
}

func isTwoPartPublicSuffix(second, top string) bool {
	switch top {
	case "cn", "hk", "tw", "jp", "kr", "uk", "au", "nz", "br", "mx", "tr", "za", "sg", "my", "id", "th", "vn", "in", "ru":
	default:
		return false
	}
	switch second {
	case "com", "net", "org", "edu", "gov", "ac", "co", "ne", "or", "go":
		return true
	}
	return false
}

func isHostPatternByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '.' || b == '-' || b == '\\'
}

func isDigitByte(b byte) bool {
	return b >= '0' && b <= '9'
}

// safeGeneralizeHost 在确认 pattern 主机已被 hostnames 覆盖时才泛化。
// 若 pattern 主机段已含通配形式（.* / [^/]+ / * / ? 等），视为已泛化不再处理。
// 若主机段含真正捕获组（`(...))` 形式但不是 `(?:...)`），保留原样。
// 若任一具体主机未被 hostnames 覆盖，保留原始 pattern。
func safeGeneralizeHost(pattern string, hostnames []string) string {
	// 仅处理以 ^http(s)?:// 开头的 pattern
	if !strings.HasPrefix(pattern, "^http") {
		return pattern
	}
	idx := strings.Index(pattern, "://")
	if idx < 0 {
		return pattern
	}
	rest := pattern[idx+3:]
	slash := strings.Index(rest, "/")
	if slash < 0 {
		// 无路径，仅主机：保留原样
		return pattern
	}
	hostPart := rest[:slash]
	// 含真正捕获组：保留原样（(?:...) 是非捕获组，允许）
	if containsRealCaptureGroup(hostPart) {
		return pattern
	}
	// 去除所有 (?:xxx) 非捕获组后，检查是否含通配
	if containsRealWildcard(stripNonCapture(hostPart)) {
		return pattern
	}
	// 提取具体主机列表
	hosts := extractHostsFromHostPart(hostPart)
	if len(hosts) == 0 {
		return pattern
	}
	// 检查每个主机是否被 hostnames 覆盖
	for _, h := range hosts {
		if !hostCoveredByHostnames(h, hostnames) {
			// 有主机未被覆盖，保留原始 pattern
			return pattern
		}
	}
	// 全部覆盖，安全泛化
	return pattern[:idx+3] + "[^/]+" + rest[slash:]
}

// stripNonCapture 去除主机段中所有 (?:xxx) 非捕获组。
func stripNonCapture(s string) string {
	for {
		idx := strings.Index(s, "(?:")
		if idx < 0 {
			return s
		}
		closeIdx := findMatchingParen(s[idx+3:])
		if closeIdx < 0 {
			return s
		}
		// 把 (?:xxx) 替换为空
		s = s[:idx] + s[idx+3+closeIdx+1:]
	}
}

// containsRealWildcard 判断是否含正则通配（剥离非捕获组后）。
// 包含：* + .* [ 等通配形式
func containsRealWildcard(s string) bool {
	if strings.Contains(s, ".*") || strings.Contains(s, "[^") {
		return true
	}
	if strings.Contains(s, "*") || strings.Contains(s, "+") {
		return true
	}
	return false
}

// containsRealCaptureGroup 判断是否含真正捕获组（排除 (?:...) 等非捕获组）。
// 真正捕获组：形如 (xxx) 但不是 (?:xxx) / (?P<name>xxx) / (?=xxx) / (?!xxx) 等。
func containsRealCaptureGroup(s string) bool {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
			// 查看是否是 (?: / (?P / (?= / (?! 等扩展
			if i+1 < len(s) && s[i+1] == '?' {
				// 跳过 (?:xxx) 整组
				if i+2 < len(s) {
					// 找到匹配的 ) 跳过整组
					inner := s[i+2:]
					closeIdx := findMatchingParen(inner)
					if closeIdx >= 0 {
						i += 2 + closeIdx
						depth--
					}
				}
			} else {
				// 真正捕获组
				return true
			}
		case ')':
			if depth > 0 {
				depth--
			}
		}
	}
	return false
}

// findMatchingParen 找到匹配的 ) 索引（不计嵌套）。
func findMatchingParen(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return i
			}
			depth--
		}
	}
	return -1
}

// containsWildcardHost 判断主机段是否已含通配形式（.* [^/]+ * ?）。
func containsWildcardHost(s string) bool {
	if strings.Contains(s, ".*") || strings.Contains(s, "[^/]+") {
		return true
	}
	if strings.Contains(s, "[") || strings.Contains(s, "*") || strings.Contains(s, "?") {
		return true
	}
	return false
}

// extractHostsFromHostPart 从 pattern 的主机段提取具体主机列表。
// 支持 alternation：(?:a\.com|b\.com)
// 自动反转义 \. → .
func extractHostsFromHostPart(hostPart string) []string {
	hostPart = strings.TrimPrefix(hostPart, "?")
	// 去除 (?: 头与 ) 尾
	inner := hostPart
	if strings.HasPrefix(inner, "(?:") && strings.HasSuffix(inner, ")") {
		inner = inner[3 : len(inner)-1]
	}
	// 按 | 切分
	parts := strings.Split(inner, "|")
	hosts := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// 反转义 \. → .
		p = strings.ReplaceAll(p, `\.`, ".")
		// 去除残留的转义
		p = strings.ReplaceAll(p, `\-`, "-")
		p = strings.ReplaceAll(p, `\_`, "_")
		// 去除首尾锚定符 ^ $
		p = strings.TrimPrefix(p, "^")
		p = strings.TrimSuffix(p, "$")
		// 空或含通配符：跳过（让外层 containsWildcardHost 处理）
		if p == "" || strings.ContainsAny(p, "*?[") {
			continue
		}
		hosts = append(hosts, p)
	}
	return hosts
}

// hostCoveredByHostnames 判断 host 是否被 hostnames 列表覆盖。
// 覆盖定义：精确匹配 OR host 是 hostnames 中某项的子域。
func hostCoveredByHostnames(host string, hostnames []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, h := range hostnames {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			continue
		}
		if h == host {
			return true
		}
		// host 是 h 的子域
		if strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// hostGeneralizeRegex 匹配 ^https?://<host>/ 前缀。
// 支持单主机、alternation 主机、可选端口。
var hostGeneralizeRegex = regexp.MustCompile(`^\^https?://(?:\(\?:[^/)]+\)|[^/]+)\.?[a-z0-9\-]+(?:\.[a-z0-9\-]+)*(?::\d+)?/`)

// generalizeHost 将 ^https?://<host>/ 泛化为 ^https?://[^/]+/。
// 仅在主机部分不含捕获组时执行（避免破坏 $1 引用）。
func generalizeHost(pattern string) string {
	// 仅处理以 ^https?:// 开头的 pattern
	if !strings.HasPrefix(pattern, "^http") {
		return pattern
	}
	// 找到 scheme 后第一个 / 的位置（即主机段结束）
	idx := strings.Index(pattern, "://")
	if idx < 0 {
		return pattern
	}
	rest := pattern[idx+3:]
	// 主机段到第一个 / 结束
	slash := strings.Index(rest, "/")
	if slash < 0 {
		// 没有路径，仅主机：不泛化（保留原样）
		return pattern
	}
	hostPart := rest[:slash]
	// 若主机段含捕获组 ( 不带 ?:，则不泛化
	if containsCaptureGroup(hostPart) {
		return pattern
	}
	// 替换主机段为 [^/]+
	return pattern[:idx+3] + "[^/]+" + rest[slash:]
}

// containsCaptureGroup 粗略判断是否含捕获组（用于避免破坏 $1）。
// 检测 ( 但不跟随 ?: 。
func containsCaptureGroup(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '(' {
			// 跳过 (?: (?= (?! (?<= (?<! (?i 等非捕获
			if i+2 < len(s) && s[i+1] == '?' {
				continue
			}
			return true
		}
	}
	return false
}

// DotPathToJSONPath 将 Loon dot-path 转为 Anywhere JSONPath。
//   - data.common_equip    → $.data.common_equip
//   - data.items.0.id      → $.data.items[0].id
//   - data.items[0].id     → $.data.items[0].id（已含方括号则保留）
func DotPathToJSONPath(dotPath string) string {
	dotPath = strings.TrimSpace(dotPath)
	if dotPath == "" {
		return "$"
	}
	// 已是 JSONPath
	if strings.HasPrefix(dotPath, "$.") || dotPath == "$" {
		return dotPath
	}
	parts := strings.Split(dotPath, ".")
	result := "$"
	for _, part := range parts {
		if part == "" {
			continue
		}
		// 处理 part 内已含 [n] 的情况：data.items[0] → .items[0]
		if idx := strings.Index(part, "["); idx >= 0 {
			key := part[:idx]
			brackets := part[idx:]
			if key != "" {
				result += "." + key
			}
			result += brackets
			continue
		}
		// 纯数字 → [n]
		if isAllDigits(part) {
			result += "[" + part + "]"
			continue
		}
		result += "." + part
	}
	return result
}

// isAllDigits 判断字符串是否全为数字。
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// QuoteField 对 .amrs 字段加引号（含逗号或引号时）。
// 内部 " 双写为 ""。
func QuoteField(field string) string {
	if strings.ContainsAny(field, ",\"") || hasSignificantWhitespace(field) {
		escaped := strings.ReplaceAll(field, `"`, `""`)
		return `"` + escaped + `"`
	}
	return field
}

// hasSignificantWhitespace 判断字段是否有显著首尾空白。
func hasSignificantWhitespace(s string) bool {
	if s == "" {
		return false
	}
	return s[0] == ' ' || s[0] == '\t' || s[len(s)-1] == ' ' || s[len(s)-1] == '\t'
}

// HasCaptureGroup 检测 URL 中是否含 $1 / $2 等捕获引用。
func HasCaptureGroup(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '$' && i+1 < len(s) && s[i+1] >= '1' && s[i+1] <= '9' {
			return true
		}
	}
	return false
}
