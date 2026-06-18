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
	GeneralizeHost     bool // URL pattern 主机泛化为 [^/]+（默认 true）
	EncodingPreprocess bool // 为 body 处理规则自动添加 accept-encoding 预处理对（默认 true）
	FetchScripts       bool // 远程下载脚本并改写（默认 true；false 时仅生成占位符）
	IncludeMetadata    bool // 在输出文件头部写入 desc/author 等注释（默认 true）
}

// DefaultOptions 返回推荐默认值。
func DefaultOptions() Options {
	return Options{
		GeneralizeHost:     true,
		EncodingPreprocess: true,
		FetchScripts:       true,
		IncludeMetadata:    true,
	}
}

// ConvertURLPattern 转换 URL 正则模式以适配 Anywhere。
// 步骤：
//  1. \/ → /（去除不必要的转义）
//  2. （可选）主机泛化：^https://<host>/ → ^https://[^/]+/
//  3. 结尾 \? → (?:\?|$)
func ConvertURLPattern(pattern string, generalize bool) string {
	if pattern == "" {
		return pattern
	}
	// 1. 去除 \/ 转义
	pattern = strings.ReplaceAll(pattern, `\/`, `/`)

	// 2. 主机泛化
	if generalize {
		pattern = generalizeHost(pattern)
	}

	// 3. 结尾 \? → (?:\?|$)
	if strings.HasSuffix(pattern, `\?`) {
		pattern = strings.TrimSuffix(pattern, `\?`) + `(?:\?|$)`
	}
	return pattern
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
