// Package parser 解析 Loon .plugin 与 Surge .sgmodule 模块文件为统一 IR。
//
// 两种格式都是 INI 风格分段，但段名与字段语法略有差异：
//   - Loon:  [Rule] / [Rewrite] / [Script] / [Argument] / [MitM]
//   - Surge: [Rule] / [URL Rewrite] / [Header Rewrite] / [Map Local] / [Script] / [MITM]
//
// 本包提供 Parse(content, source) 统一入口，根据 source 分派到具体解析器。
package parser

import (
	"fmt"
	"strings"

	"github.com/Loon2Anywhere/loon2anywhere/ir"
)

// Parse 根据来源格式分派解析器。source 决定使用 Loon 还是 Surge 语法。
func Parse(content string, source ir.Source) (*ir.Module, error) {
	switch source {
	case ir.SourceLoon:
		return ParseLoon(content)
	case ir.SourceSurge:
		return ParseSurge(content)
	default:
		return nil, fmt.Errorf("未知来源格式: %v", source)
	}
}

// DetectSource 依据内容/文件名推断来源格式。
// 优先看文件名后缀；其次用段名特征：
//   - Surge 独有：[URL Rewrite] / [Header Rewrite] / [Map Local]
//   - Loon  独有：[Rewrite]（单数）/ [Argument]
//   - [MitM] vs [MITM] 大小写敏感区分（Loon 用 [MitM]，Surge 用 [MITM]）
func DetectSource(content, filename string) ir.Source {
	// 1. 文件名后缀最可靠
	lowerName := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lowerName, ".plugin"):
		return ir.SourceLoon
	case strings.HasSuffix(lowerName, ".sgmodule"):
		return ir.SourceSurge
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

	// 4. MITM 段大小写敏感区分
	if strings.Contains(content, "[MitM]") {
		return ir.SourceLoon
	}
	if strings.Contains(content, "[MITM]") {
		return ir.SourceSurge
	}

	// 5. 默认按 Loon 处理
	return ir.SourceLoon
}

// splitSections 将原始内容切分为 (section, body) 列表。
// section 名保留原始大小写以便区分 Loon [MitM] 与 Surge [MITM]。
func splitSections(content string) []section {
	var sections []section
	var current section
	var bodyLines []string

	lines := strings.Split(content, "\n")
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

// normalizeHostnames 规范化 hostname 列表：
//   - 去除 %APPEND% / %APPEND% 前缀（Surge）
//   - 去除 *. / * 前缀（Anywhere 用后缀匹配，无需通配符）
//   - 去重、trim
func normalizeHostnames(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool)
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Surge %APPEND% / %APPEND%
		p = strings.TrimPrefix(p, "%APPEND%")
		p = strings.TrimSpace(p)
		// 去除通配符前缀
		p = strings.TrimPrefix(p, "*.")
		p = strings.TrimPrefix(p, "*")
		// Loon 通配符 ap?.bilibili.com 无法静态展开，保留原样（Anywhere 会按后缀匹配失败，但至少不报错）
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}
