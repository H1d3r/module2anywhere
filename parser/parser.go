// Package parser 解析 Loon .plugin / Surge .sgmodule / QuantumultX .conf 模块文件为统一 IR。
//
// 三种格式的特点：
//   - Loon (.plugin / .lpx):   [Rule] / [Rewrite] / [Script] / [Argument] / [MitM] 段，#!name 元数据
//   - Surge (.sgmodule):       [Rule] / [URL Rewrite] / [Header Rewrite] / [Map Local] / [Script] / [MITM] 段
//   - QuantumultX (.conf):     行式规则，格式 "<pattern> url <action> [args...]"，无强制段头
//
// 本包提供 Parse(content, source) 统一入口，根据 source 分派到具体解析器。
// 也提供 DetectSource 用于在未知 source 时根据文件名/内容特征自动识别。
package parser

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/H1d3r/module2anywhere/ir"
)

var inlineSectionHeaderRE = regexp.MustCompile(`\s+(\[(?:Rule|Rewrite|URL Rewrite|Header Rewrite|Map Local|Script|MitM|MITM|mitm|Argument|Host|General)\])\s*`)
var inlineMetadataRE = regexp.MustCompile(`\s+(#![A-Za-z0-9_-]+\s*=)`)

// Parse 根据来源格式分派解析器。source 决定使用 Loon/Surge/QuantumultX 语法。
func Parse(content string, source ir.Source) (*ir.Module, error) {
	switch source {
	case ir.SourceLoon:
		return ParseLoon(content)
	case ir.SourceSurge:
		return ParseSurge(content)
	case ir.SourceQuantumultX:
		return ParseQuantumultX(content)
	default:
		return nil, fmt.Errorf("未知来源格式: %v", source)
	}
}

// DetectSource 依据内容/文件名推断来源格式。
// 优先看文件名后缀；其次用段名特征：
//   - Surge 独有：[URL Rewrite] / [Header Rewrite] / [Map Local]
//   - Loon  独有：[Rewrite]（单数）/ [Argument]
//   - [MitM]/[MITM]/[mitm] 写法不区分（两侧都接受）
//   - Loon [Script] 行通常以 "http-request"/"http-response" 开头；Surge [Script] 行为 name = key=val,key=val 形式
//   - QX 行式：开头是 "pattern url action ..." 形式（pattern 是 URL 正则）
//
// 文件后缀约定：
//   - .plugin / .lpx → Loon（.lpx 是 Loon 插件的 XML 格式压缩包，但文本外壳可同样按 Loon 处理）
//   - .sgmodule     → Surge
//   - .conf         → QuantumultX
func DetectSource(content, filename string) ir.Source {
	// 1. 文件名后缀最可靠
	lowerName := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lowerName, ".plugin"), strings.HasSuffix(lowerName, ".lpx"):
		return ir.SourceLoon
	case strings.HasSuffix(lowerName, ".sgmodule"):
		return ir.SourceSurge
	case strings.HasSuffix(lowerName, ".conf"):
		return ir.SourceQuantumultX
	}

	// 2. 内容特征：Surge 独有段
	lowerContent := strings.ToLower(content)
	if strings.Contains(lowerContent, "[url rewrite]") ||
		strings.Contains(lowerContent, "[header rewrite]") ||
		strings.Contains(lowerContent, "[map local]") {
		return ir.SourceSurge
	}

	// 3. 内容特征：Loon 独有段
	if strings.Contains(lowerContent, "[rewrite]") || strings.Contains(lowerContent, "[argument]") {
		return ir.SourceLoon
	}

	// 4. [Script] 段语法差异：Loon 行首为 phase，Surge 行首为 name=...
	//    Loon  示例：http-request ^... script-path=...
	//    Surge 示例：name1 = type=http-request,pattern=...
	if strings.Contains(lowerContent, "[script]") {
		// 提取 [Script] 段后第一非空行
		if firstScriptLine := extractFirstScriptLine(content); firstScriptLine != "" {
			lowerLine := strings.ToLower(strings.TrimSpace(firstScriptLine))
			if strings.HasPrefix(lowerLine, "http-request ") || strings.HasPrefix(lowerLine, "http-response ") {
				return ir.SourceLoon
			}
			if strings.Contains(lowerLine, "=type=") || strings.Contains(lowerLine, "type=http-") {
				return ir.SourceSurge
			}
		}
	}

	// 5. 内容特征：QuantumultX 行式特征
	//    - 出现 " hostname = " 或 " hostname=" 行（QX 风格，可能在 # 注释行之后）
	//    - 出现 " url reject" / " url 302" / " url response-body" 等（行式规则）
	if detectQuantumultXContent(content) {
		return ir.SourceQuantumultX
	}

	// 6. 默认按 Loon 处理
	return ir.SourceLoon
}

// detectQuantumultXContent 粗略判断内容是否符合 QX 行式特征。
// 关键特征：
//   - "hostname = ..."  出现在注释行（# 或 ; 开头）之后
//   - 多行以 " url reject" / " url 302" / " url response-body" / " url echo-response" /
//     " url script-response-body" / " url jsonjq-response-body" 等动作开头
func detectQuantumultXContent(content string) bool {
	// 出现 "hostname = ..." 段（可能在注释行内）
	if strings.Contains(content, "hostname =") || strings.Contains(content, "hostname=") {
		// 进一步看是否同时存在 QX 风格动作
		lower := strings.ToLower(content)
		if strings.Contains(lower, " url reject") ||
			strings.Contains(lower, " url 302") ||
			strings.Contains(lower, " url response-body") ||
			strings.Contains(lower, " url echo-response") ||
			strings.Contains(lower, " url script-response-body") ||
			strings.Contains(lower, " url script-request-body") ||
			strings.Contains(lower, " url script-request-header") ||
			strings.Contains(lower, " url script-response-header") ||
			strings.Contains(lower, " url jsonjq-response-body") ||
			strings.Contains(lower, " url script-analyze-echo-response") {
			return true
		}
	}
	return false
}

// extractFirstScriptLine 提取 [Script] 段后第一非空、非注释行。
func extractFirstScriptLine(content string) string {
	lower := strings.ToLower(content)
	idx := strings.Index(lower, "[script]")
	if idx < 0 {
		return ""
	}
	rest := content[idx+len("[script]"):]
	for _, line := range strings.Split(rest, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
			continue
		}
		return trimmed
	}
	return ""
}

// splitSections 将原始内容切分为 (section, body) 列表。
// section 名保留原始大小写以便区分 Loon [MitM] 与 Surge [MITM]。
func splitSections(content string) []section {
	var sections []section
	var current section
	var bodyLines []string

	lines := strings.Split(normalizeInlineSections(content), "\n")
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if current.name != "" {
				bodyLines = append(bodyLines, line)
			}
			continue
		}
		// 元数据 #!key=value 保留在虚拟段 __meta__
		if strings.HasPrefix(trimmed, "#!") {
			if current.name == "" {
				current.name = metaSection
			}
			if current.name == metaSection {
				bodyLines = append(bodyLines, line)
				continue
			}
			// 已进入正式段：把 #! 视为注释，归入当前段
			bodyLines = append(bodyLines, line)
			continue
		}
		// 段头 [Section]
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if current.name != "" {
				current.body = strings.Join(bodyLines, "\n")
				sections = append(sections, current)
			}
			current = section{name: strings.TrimSpace(trimmed[1 : len(trimmed)-1])}
			bodyLines = nil
			continue
		}
		// 普通行
		bodyLines = append(bodyLines, line)
	}
	if current.name != "" {
		current.body = strings.Join(bodyLines, "\n")
		sections = append(sections, current)
	}
	return sections
}

// normalizeInlineSections 兼容被压成单行的模块文件。
// 例如部分 Loon plugin 会形如：
//
//	#!name=... [Script] http-response ... [MITM] hostname=...
//
// 标准解析器只识别独立一行的段头，因此这里仅对已知模块段头插入换行，
// 避免误伤 URL 正则中普通的字符类方括号。
func normalizeInlineSections(content string) string {
	content = inlineMetadataRE.ReplaceAllString(content, "\n$1")
	return inlineSectionHeaderRE.ReplaceAllString(content, "\n$1\n")
}

type section struct {
	name string
	body string
}

const metaSection = "__meta__"

// parseMeta 解析 #!key=value 元数据。
func parseMeta(body string) (map[string]string, []string) {
	meta := make(map[string]string)
	var ordered []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#!") {
			continue
		}
		kv := strings.TrimPrefix(line, "#!")
		idx := strings.Index(kv, "=")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(kv[:idx])
		val := strings.TrimSpace(kv[idx+1:])
		meta[key] = val
		ordered = append(ordered, key)
	}
	return meta, ordered
}

// splitCSVFields 将一行按逗号切分为字段，支持双引号包裹（内部 "" 表示字面量 "）。
// 与 Anywhere .amrs 字段语法一致，便于解析 Loon/Surge 中带引号的 URL-REGEX 等。
func splitCSVFields(line string) []string {
	var fields []string
	var buf strings.Builder
	inQuote := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inQuote:
			if c == '"' {
				if i+1 < len(line) && line[i+1] == '"' {
					buf.WriteByte('"')
					i++
				} else {
					inQuote = false
				}
			} else {
				buf.WriteByte(c)
			}
		case c == '"':
			inQuote = true
		case c == ',':
			fields = append(fields, strings.TrimSpace(buf.String()))
			buf.Reset()
		default:
			buf.WriteByte(c)
		}
	}
	fields = append(fields, strings.TrimSpace(buf.String()))
	return fields
}

// parseKeyValueList 解析 "key=val,key2=val2,tag=xxx" 形式的参数列表。
// 第一个非 key=value 的 token 视为位置参数（positional），返回 args 与 positional。
func parseKeyValueList(tokens []string) (args map[string]string, positional []string) {
	args = make(map[string]string)
	for _, t := range tokens {
		if t == "" {
			continue
		}
		idx := strings.Index(t, "=")
		if idx > 0 {
			args[strings.ToLower(strings.TrimSpace(t[:idx]))] = strings.TrimSpace(t[idx+1:])
		} else {
			positional = append(positional, t)
		}
	}
	return args, positional
}

// trimQuotes 去除字段两端的引号（如有）。
func trimQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return strings.ReplaceAll(s[1:len(s)-1], `""`, `"`)
	}
	return s
}

// stripInlineComment 去掉值末尾的行内注释（# / ;），保留正文中的字符。
func stripInlineComment(s string) string {
	s = strings.TrimSpace(s)
	for i := 0; i < len(s); i++ {
		if s[i] != '#' && s[i] != ';' {
			continue
		}
		if i == 0 || s[i-1] == ' ' || s[i-1] == '\t' {
			return strings.TrimSpace(s[:i])
		}
	}
	return s
}

// normalizeHostnames 规范化 hostname 列表：
//   - 去除 %APPEND% / %APPEND% 前缀（Surge）
//   - 去除 *. / * 前缀（Anywhere 用后缀匹配，无需通配符）
//   - 对部分复杂通配符提取可表达的后缀候选（例如 v.foo*.com -> foo.com）
//   - 去重、trim
func normalizeHostnames(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool)
	for _, p := range parts {
		for _, host := range normalizeHostnameCandidates(p) {
			if host == "" || seen[host] {
				continue
			}
			seen[host] = true
			out = append(out, host)
		}
	}
	return out
}

func normalizeHostnameCandidates(raw string) []string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.TrimPrefix(value, "%append%")
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") || strings.ContainsAny(value, "?/ \t") {
		return nil
	}
	add := func(candidates []string, item string) []string {
		item = strings.TrimSpace(item)
		if idx := strings.LastIndex(item, ":"); idx > -1 && allDigits(item[idx+1:]) {
			item = item[:idx]
		}
		item = strings.Trim(item, ".")
		if item == "" || strings.ContainsAny(item, "*?") || isPublicHostnameSuffix(item) || !validHostnameSuffix(item) {
			return candidates
		}
		for _, existing := range candidates {
			if existing == item {
				return candidates
			}
		}
		return append(candidates, item)
	}

	var candidates []string
	switch {
	case strings.HasPrefix(value, "*."):
		candidates = add(candidates, value[2:])
	case strings.HasPrefix(value, "*") && strings.Contains(value, "."):
		candidates = add(candidates, value[1:])
		candidates = add(candidates, value[strings.Index(value, ".")+1:])
	case strings.Contains(strings.SplitN(value, ".", 2)[0], "*") && strings.Contains(value, "."):
		candidates = add(candidates, value[strings.Index(value, ".")+1:])
	default:
		candidates = add(candidates, value)
	}
	return candidates
}

func validHostnameSuffix(host string) bool {
	for _, r := range host {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '-' {
			continue
		}
		return false
	}
	return strings.Contains(host, ".")
}

func isPublicHostnameSuffix(host string) bool {
	switch host {
	case "com", "net", "org", "top", "cn", "tv", "cc", "io", "app", "co", "me", "xyz", "site", "vip":
		return true
	}
	return false
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
