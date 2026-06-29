// parser 包：Surge .sgmodule 解析器。
package parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/H1d3r/module2anywhere/ir"
)

// ParseSurge 解析 Surge .sgmodule 内容为 IR Module。
func ParseSurge(content string) (*ir.Module, error) {
	m := &ir.Module{Source: ir.SourceSurge, RawMeta: map[string]string{}}

	for _, sec := range splitSections(content) {
		switch sec.name {
		case metaSection:
			meta, _ := parseMeta(sec.body)
			for k, v := range meta {
				m.RawMeta[k] = v
				switch k {
				case "name":
					m.Name = v
				case "desc":
					m.Desc = v
				case "author":
					m.Author = v
				case "homepage":
					m.Homepage = v
				case "date":
					m.Date = v
				}
			}
		case "Rule":
			m.Rules = append(m.Rules, parseSurgeRules(sec.body)...)
		case "URL Rewrite":
			m.Rewrites = append(m.Rewrites, parseSurgeURLRewrites(sec.body)...)
		case "Header Rewrite":
			m.HeaderRWs = append(m.HeaderRWs, parseSurgeHeaderRewrites(sec.body)...)
		case "Map Local":
			m.MapLocals = append(m.MapLocals, parseSurgeMapLocals(sec.body)...)
		case "Script":
			m.Scripts = append(m.Scripts, parseSurgeScripts(sec.body)...)
		case "MITM", "MitM", "mitm":
			// 兼容 [MITM] / [MitM] / [mitm] 任意大小写写法
			m.Hostnames = append(m.Hostnames, parseSurgeMITM(sec.body)...)
		}
	}
	return m, nil
}

// parseSurgeRules 解析 [Rule] 段（与 Loon 同构）。
func parseSurgeRules(body string) []ir.RoutingRule {
	// Surge 与 Loon 的 [Rule] 段语法基本一致
	return parseLoonRules(body)
}

// parseSurgeURLRewrites 解析 [URL Rewrite] 段。
// 格式：<pattern> <action> [args...]
// 例：^https://... reject-dict
//
//	^https://... 302 https://new.url
//	^https://... _response-body {JS}
//	^https://... _request-header {JS}
func parseSurgeURLRewrites(body string) []ir.RewriteRule {
	var rules []ir.RewriteRule
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		r := ir.RewriteRule{Raw: line, Args: map[string]string{}}
		pattern, rest := splitRewritePatternAndRest(line)
		r.Pattern = pattern
		if rest == "" {
			rules = append(rules, r)
			continue
		}
		action, args, rawJS := parseSurgeURLRewriteAction(rest)
		r.Action = action
		r.Args = args
		r.RawJS = rawJS
		rules = append(rules, r)
	}
	return rules
}

// splitRewritePatternAndRest 拆分 rewrite 的 pattern 与动作部分。
// 除空白分隔外，兼容紧贴写法：pattern-302$1$3 / pattern_reject / pattern-reject。
func splitRewritePatternAndRest(line string) (string, string) {
	pattern, rest := splitFirstWhitespace(line)
	if rest != "" {
		return pattern, rest
	}
	for _, marker := range []string{"-302", "-307", "_302", "_307", "-reject-200", "_reject-200", "-reject-dict", "_reject-dict", "-reject-array", "_reject-array", "-reject-img", "_reject-img", "-reject", "_reject"} {
		idx := strings.LastIndex(strings.ToLower(line), marker)
		if idx > 0 && isTightRewriteActionSuffix(line[idx+len(marker):], marker) {
			return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:])
		}
	}
	return pattern, rest
}

// isTightRewriteActionSuffix 判断紧贴动作后缀是否像真实动作参数，避免误切 pattern 中的 -reject / -302 字样。
func isTightRewriteActionSuffix(suffix, marker string) bool {
	action := strings.TrimLeft(marker, "-_")
	suffix = strings.TrimSpace(suffix)
	if strings.HasPrefix(action, "reject") {
		return suffix == ""
	}
	if action == "302" || action == "307" {
		return suffix != "" && (strings.HasPrefix(suffix, "$") || strings.HasPrefix(suffix, "http://") || strings.HasPrefix(suffix, "https://"))
	}
	return false
}

// prependTightRewriteTarget 保留紧贴 URL 与后续参数之间的分隔空格。
func prependTightRewriteTarget(prefix, remain string) string {
	remain = strings.TrimSpace(remain)
	if remain == "" {
		return prefix
	}
	return prefix + " " + remain
}

// parseSurgeURLRewriteAction 解析 Surge [URL Rewrite] 动作。
func parseSurgeURLRewriteAction(rest string) (string, map[string]string, string) {
	args := make(map[string]string)
	action, remain := splitFirstWhitespace(rest)
	action = strings.ToLower(strings.TrimSpace(action))

	// Loon/QX 混入 Surge 时常带 url 前缀：url reject-dict / url 302 ...
	if action == "url" {
		action, remain = splitFirstWhitespace(remain)
		action = strings.ToLower(strings.TrimSpace(action))
	}

	// 兼容紧贴写法：302$1$3 / 307https://... → 动作与目标 URL 之间补出空格语义
	if strings.HasPrefix(action, "302") && len(action) > 3 {
		remain = prependTightRewriteTarget(action[3:], remain)
		action = "302"
	} else if strings.HasPrefix(action, "307") && len(action) > 3 {
		remain = prependTightRewriteTarget(action[3:], remain)
		action = "307"
	}

	// 跳过 - / _ 占位符（部分模块写成 "pattern - reject" 或 "pattern _ reject"）
	if action == "-" || action == "_" {
		action, remain = splitFirstWhitespace(remain)
		action = strings.ToLower(strings.TrimSpace(action))
	}

	switch action {
	case "reject", "reject-200", "reject-dict", "reject-array", "reject-img":
		return action, args, ""

	case "302", "307":
		args["url"] = strings.TrimSpace(remain)
		return action, args, ""

	case "request-header", "request-body", "response-body":
		return action, args, strings.TrimSpace(remain)

	case "_request-header", "_request-body", "_response-body":
		// 内联 JS（兼容 Surge/QX 的下划线写法）
		return strings.TrimPrefix(action, "_"), args, strings.TrimSpace(remain)

	case "header-del":
		args["header"] = strings.TrimSpace(remain)
		return action, args, ""

	case "_header-del":
		// Surge _header-del <name>：删除请求头（与 Loon header-del 等价）
		args["header"] = strings.TrimSpace(remain)
		// 归一化为 header-del 动作，便于 converter 统一处理
		return "header-del", args, ""

	default:
		// Surge [URL Rewrite] 中无动作前缀的纯 URL 替换是 transparent rewrite
		// 格式：<pattern> <new-url>，其中 new-url 以 http:// 或 https:// 开头
		trimmedRemain := strings.TrimSpace(remain)
		if strings.Contains(action, "$") {
			switch strings.ToLower(trimmedRemain) {
			case "302", "307":
				args["url"] = action
				return strings.ToLower(trimmedRemain), args, ""
			}
		}
		if first, tail := splitFirstWhitespace(trimmedRemain); first != "" {
			switch strings.ToLower(strings.TrimSpace(tail)) {
			case "302", "307":
				args["url"] = first
				return strings.ToLower(strings.TrimSpace(tail)), args, ""
			}
		}
		if strings.HasPrefix(action, "http://") || strings.HasPrefix(action, "https://") {
			switch strings.ToLower(trimmedRemain) {
			case "302", "307":
				args["url"] = action
				return strings.ToLower(trimmedRemain), args, ""
			}
		}
			if strings.HasPrefix(trimmedRemain, "http://") || strings.HasPrefix(trimmedRemain, "https://") {
				args["url"] = trimmedRemain
				return "transparent", args, ""
			}
			if strings.HasPrefix(trimmedRemain, "request-header ") ||
				strings.HasPrefix(trimmedRemain, "request-body ") ||
				strings.HasPrefix(trimmedRemain, "response-body ") {
				jsAction, jsRemain := splitFirstWhitespace(trimmedRemain)
				args["_raw"] = strings.TrimSpace(jsRemain)
				return jsAction, args, strings.TrimSpace(jsRemain)
			}
		if strings.HasPrefix(strings.ToLower(trimmedRemain), "header-del ") {
			args["header"] = strings.TrimSpace(trimmedRemain[len("header-del "):])
			return "header-del", args, ""
		}
		// 未知动作：保留原始 remain 以便日志
		args["_raw"] = strings.TrimSpace(remain)
		return action, args, ""
	}
}

// parseSurgeHeaderRewrites 解析 [Header Rewrite] 段。
// 格式：<pattern> <request-header|response-header> <add|replace|delete> <name> [value]
// 例：^https://... request-header add X-Header value
//
//	^https://... response-header delete Cookie
func parseSurgeHeaderRewrites(body string) []ir.HeaderRule {
	var rules []ir.HeaderRule
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		r := ir.HeaderRule{Raw: line}
		// pattern <target> <op> <name> [value]
		pattern, rest := splitFirstWhitespace(line)
		r.Pattern = pattern
		if rest == "" {
			continue
		}
		target, rest2 := splitFirstWhitespace(rest)
		target = strings.ToLower(strings.TrimSpace(target))
		switch target {
		case "request-header":
		case "request":
			r.Phase = 0
		case "response-header":
		case "response":
			r.Phase = 1
		default:
			continue
		}
		op, rest3 := splitFirstWhitespace(rest2)
		r.Op = strings.ToLower(strings.TrimSpace(op))
		// 剩余：name [value]，保留引号中的空格
		tokens := tokenizeKV(rest3)
		if len(tokens) == 0 {
			continue
		}
		r.Name = trimQuotes(strings.TrimSpace(tokens[0]))
		if len(tokens) >= 2 {
			r.Value = trimQuotes(strings.TrimSpace(strings.Join(tokens[1:], " ")))
		}
		rules = append(rules, r)
	}
	return rules
}

// parseSurgeMapLocals 解析 [Map Local] 段。
// 格式：<pattern> data="<file-url>" header="<Header: value>"
func parseSurgeMapLocals(body string) []ir.MapLocalRule {
	var rules []ir.MapLocalRule
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		r := ir.MapLocalRule{Raw: line}
		pattern, rest := splitFirstWhitespace(line)
		r.Pattern = pattern
		// 解析 data="..." header="..."
		tokens := tokenizeKV(rest)
		for _, t := range tokens {
			idx := strings.Index(t, "=")
			if idx <= 0 {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(t[:idx]))
			val := trimQuotes(strings.TrimSpace(t[idx+1:]))
			val = stripInlineComment(val)
			switch key {
			case "data":
				r.DataURL = val
			case "url", "file", "body", "uri", "data-url":
				r.DataURL = val
			case "header":
			case "headers":
				r.Header = val
			}
		}
		rules = append(rules, r)
	}
	return rules
}

// parseSurgeScripts 解析 [Script] 段。
// 格式：<name> = type=http-response,pattern=<regex>,requires-body=1,script-path=<url>[,binary-body-mode=1][,max-size=...]
func parseSurgeScripts(body string) []ir.ScriptRule {
	var scripts []ir.ScriptRule
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		s, err := parseSurgeScriptLine(line)
		if err != nil || s == nil {
			continue
		}
		scripts = append(scripts, *s)
	}
	return scripts
}

// parseSurgeScriptLine 解析单行 Surge 脚本规则。
func parseSurgeScriptLine(line string) (*ir.ScriptRule, error) {
	// name = params
	idx := strings.Index(line, "=")
	if idx <= 0 {
		return nil, fmt.Errorf("缺少 '=' 分隔: %q", line)
	}
	params := strings.TrimSpace(line[idx+1:])
	tokens := splitCSVFields(params)
	args, _ := parseKeyValueList(tokens)
	if args["script-path"] == "" {
		// 兼容混合写法：type=http-response,pattern=... url script-response-header <script-url>
		// 这类规则本质上是 QX/Loon 的 url script-* 语法混入 Surge 模块中，需归一化为 ScriptRule。
		if p, ok := parseMixedURLScriptParams(args, line); ok {
			return p, nil
		}
	}

	s := &ir.ScriptRule{Raw: line}
	s.ScriptPath = args["script-path"]
	s.Tag = args["tag"]
	s.Argument = args["argument"]
	s.Engine = args["engine"]
	s.Pattern = args["pattern"]

	switch strings.ToLower(strings.TrimSpace(args["type"])) {
	case "http-request":
		s.Phase = 0
	case "http-response":
		s.Phase = 1
	case "cron":
		return nil, nil
	default:
		return nil, fmt.Errorf("未知脚本类型: %q", args["type"])
	}

	if v, ok := args["requires-body"]; ok {
		s.RequiresBody = v == "1" || strings.EqualFold(v, "true")
	}
	if v, ok := args["binary-body-mode"]; ok {
		s.BinaryBody = v == "1" || strings.EqualFold(v, "true")
	}
	if v, ok := args["max-size"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			s.MaxSize = n
		}
	}
	return s, nil
}

// parseMixedURLScriptParams 解析混入 Surge Script 段中的 url script-* 写法。
func parseMixedURLScriptParams(args map[string]string, raw string) (*ir.ScriptRule, bool) {
	patternValue := strings.TrimSpace(args["pattern"])
	lower := strings.ToLower(patternValue)
	marker := " url "
	idx := strings.Index(lower, marker)
	if idx < 0 {
		return nil, false
	}
	pattern := strings.TrimSpace(patternValue[:idx])
	right := strings.TrimSpace(patternValue[idx+len(marker):])
	if strings.TrimSpace(args["type"]) == "" || strings.TrimSpace(args["pattern"]) == "" {
		return nil, false
	}
	tokens := splitWhitespace(right)
	if len(tokens) < 2 {
		return nil, false
	}
	action := strings.ToLower(strings.TrimSpace(tokens[0]))
	scriptPath, trailingArgs := splitScriptPathAndTrailingArgs(tokens[1])
	for _, token := range tokens[2:] {
		if strings.Contains(token, "=") {
			trailingArgs = append(trailingArgs, token)
		}
	}
	if len(trailingArgs) > 0 {
		extraArgs, _ := parseKeyValueList(trailingArgs)
		for k, v := range extraArgs {
			args[k] = v
		}
	}
	if scriptPath == "" {
		return nil, false
	}
	s := &ir.ScriptRule{Raw: raw}
	s.Pattern = pattern
	s.ScriptPath = scriptPath
	s.Tag = args["tag"]
	s.Argument = args["argument"]
	s.Engine = args["engine"]
	s.RequiresBody = args["requires-body"] == "1" || strings.EqualFold(args["requires-body"], "true")
	s.BinaryBody = args["binary-body-mode"] == "1" || strings.EqualFold(args["binary-body-mode"], "true")
	if v, ok := args["max-size"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			s.MaxSize = n
		}
	}
	switch action {
	case "script-request-body", "script-request-header":
		s.Phase = 0
		return s, true
	case "script-response-body", "script-response-header", "script-analyze-echo-response":
		s.Phase = 1
		return s, true
	default:
		return nil, false
	}
}

// splitScriptPathAndTrailingArgs 拆分混合脚本语法中 script URL 后方的逗号参数。
func splitScriptPathAndTrailingArgs(raw string) (string, []string) {
	raw = strings.TrimSpace(raw)
	parts := splitCSVFields(raw)
	if len(parts) == 0 {
		return "", nil
	}
	return strings.TrimSpace(parts[0]), parts[1:]
}

// parseSurgeMITM 解析 [MITM] 段。
// 格式：hostname = %APPEND% a.com, b.com
//
//	hostname = a.com, b.com
func parseSurgeMITM(body string) []string {
	var hostnames []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:idx]))
		if key == "hostname" {
			hostnames = append(hostnames, normalizeHostnames(stripInlineComment(line[idx+1:]))...)
		}
	}
	return dedupStrings(hostnames)
}
