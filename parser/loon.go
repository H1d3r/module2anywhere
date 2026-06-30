// parser 包：Loon .plugin 解析器。
package parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/H1d3r/module2anywhere/ir"
)

// ParseLoon 解析 Loon .plugin 内容为 IR Module。
func ParseLoon(content string) (*ir.Module, error) {
	m := &ir.Module{Source: ir.SourceLoon, RawMeta: map[string]string{}}

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
			m.Rules = append(m.Rules, parseLoonRules(sec.body)...)
		case "Rewrite":
			m.Rewrites = append(m.Rewrites, parseLoonRewrites(sec.body)...)
		case "Script":
			m.Scripts = append(m.Scripts, parseLoonScripts(sec.body)...)
		case "Argument":
			m.Arguments = append(m.Arguments, parseLoonArguments(sec.body)...)
		case "MitM", "MITM", "mitm":
			// 兼容 [MitM] / [MITM] / [mitm] 任意大小写写法
			m.Hostnames = append(m.Hostnames, parseLoonMitM(sec.body)...)
		}
	}
	return m, nil
}

// parseLoonRules 解析 [Rule] 段。
// 格式：TYPE,VALUE,ACTION[,OPTIONS...]
// 例：DOMAIN-SUFFIX,bilibili.com,DIRECT
//
//	URL-REGEX,"^http://...",REJECT-DICT
//	IP-CIDR,192.168.0.0/16,DIRECT,no-resolve
func parseLoonRules(body string) []ir.RoutingRule {
	var rules []ir.RoutingRule
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		fields := splitCSVFields(line)
		if len(fields) < 2 {
			continue
		}
		r := ir.RoutingRule{Raw: line}
		r.Type = strings.ToUpper(strings.TrimSpace(fields[0]))
		r.Value = strings.TrimSpace(fields[1])
		if len(fields) >= 3 {
			r.Action = strings.ToUpper(strings.TrimSpace(fields[2]))
		}
		if len(fields) > 3 {
			r.Options = append(r.Options, fields[3:]...)
		}
		rules = append(rules, r)
	}
	return rules
}

// parseLoonRewrites 解析 [Rewrite] 段。
// 格式：<pattern> <action> [args...]
// 例：^https://... reject-dict
//
//	^https://... 302 https://new.url
//	^https://... mock-response-body data-type=json data="..." status-code=200
//	^https://... response-body-json-del data.path
//	^https://... request-header {JS}
func parseLoonRewrites(body string) []ir.RewriteRule {
	var rules []ir.RewriteRule
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		r := ir.RewriteRule{Raw: line, Args: map[string]string{}}

		// 先分离 pattern 与剩余：优先空白分隔，同时兼容紧贴写法 pattern-302$1$3 / pattern-reject
		pattern, rest := splitRewritePatternAndRest(line)
		r.Pattern = pattern
		r.Action = rest
		if rest == "" {
			rules = append(rules, r)
			continue
		}

		// 按动作类型分别解析
		action, args, rawJS := parseLoonRewriteAction(rest)
		r.Action = action
		r.Args = args
		r.RawJS = rawJS
		rules = append(rules, r)
	}
	return rules
}

// splitFirstWhitespace 以第一个空白（空格/tab）分割字符串。
func splitFirstWhitespace(s string) (string, string) {
	s = strings.TrimLeft(s, " \t")
	for i, c := range s {
		if c == ' ' || c == '\t' {
			return s[:i], strings.TrimLeft(s[i+1:], " \t")
		}
	}
	return s, ""
}

// parseLoonRewriteAction 解析动作及其参数。
// 返回 (action, args, rawJS)。rawJS 仅在 request-header/response-body 等内联 JS 动作时非空。
func parseLoonRewriteAction(rest string) (string, map[string]string, string) {
	args := make(map[string]string)
	// 动作名 = 第一个 token
	action, remain := splitFirstWhitespace(rest)
	action = strings.ToLower(strings.TrimSpace(action))

	// Loon 部分插件使用 "url <action>" 前缀（url reject-dict / url 302 ...），去除 url 前缀
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

	case "reject-data":
		// reject-data <base64-data>
		args["data"] = strings.TrimSpace(remain)
		return action, args, ""

	case "302", "307":
		// 302 <url>  /  307 <url>
		url := strings.TrimSpace(remain)
		args["url"] = url
		return action, args, ""

	case "transparent", "rewrite":
		// transparent <url> / rewrite <url> — 透明 URL 重写
		url := strings.TrimSpace(remain)
		args["url"] = url
		return action, args, ""

	case "mock-response-body":
		// mock-response-body data-type=json data="..." status-code=200
		parseKVArgs(remain, args)
		return action, args, ""

	case "response-body-json-del", "response-body-json-add", "response-body-json-replace":
		// response-body-json-del <dot-path>
		// response-body-json-add <dot-path> <value>
		// response-body-json-replace <dot-path> <value>
		tokens := splitWhitespace(remain)
		if len(tokens) >= 1 {
			args["path"] = tokens[0]
		}
		if len(tokens) >= 2 {
			args["value"] = strings.Join(tokens[1:], " ")
		}
		return action, args, ""

	case "response-body-json-delete-recursive":
		// response-body-json-delete-recursive <key>
		tokens := splitWhitespace(remain)
		if len(tokens) >= 1 {
			args["key"] = tokens[0]
		}
		return action, args, ""

	case "response-body-json-replace-recursive":
		// response-body-json-replace-recursive <key> <value>
		tokens := splitWhitespace(remain)
		if len(tokens) >= 1 {
			args["key"] = tokens[0]
		}
		if len(tokens) >= 2 {
			args["value"] = strings.Join(tokens[1:], " ")
		}
		return action, args, ""

	case "response-body-json-remove-where-key-exists":
		// response-body-json-remove-where-key-exists <dot-path> <key>
		tokens := splitWhitespace(remain)
		if len(tokens) >= 1 {
			args["path"] = tokens[0]
		}
		if len(tokens) >= 2 {
			args["key"] = tokens[1]
		}
		return action, args, ""

	case "response-body-json-remove-where-field-in":
		// response-body-json-remove-where-field-in <dot-path> <field> <values>
		tokens := splitWhitespace(remain)
		if len(tokens) >= 1 {
			args["path"] = tokens[0]
		}
		if len(tokens) >= 2 {
			args["field"] = tokens[1]
		}
		if len(tokens) >= 3 {
			args["values"] = strings.Join(tokens[2:], " ")
		}
		return action, args, ""

	case "request-header", "request-body", "response-body":
		// 内联 JS：request-header { ... }
		// remain 即 JS 源码（可能含空格、花括号）
		return action, args, strings.TrimSpace(remain)

	case "_request-header", "_request-body", "_response-body":
		// 兼容 Surge 风格的下划线内联 JS 别名
		return strings.TrimPrefix(action, "_"), args, strings.TrimSpace(remain)

	case "header-add", "header-replace", "header-del",
		"request-header-add", "request-header-replace", "request-header-del",
		"response-header-add", "response-header-replace", "response-header-del",
		"_header-add", "_header-replace", "_header-del",
		"_request-header-add", "_request-header-replace", "_request-header-del",
		"_response-header-add", "_response-header-replace", "_response-header-del":
		// 头部简写：header-del <name> / request-header-add <name> <value> / response-header-replace <name> <value>
		return parseHeaderRewriteShortcut(action, remain, args)

	case "response-body-replace-regex":
		// response-body-replace-regex <search> <replacement>
		// search 与 replacement 以空白分隔；含空格时需引号包裹
		search, repl := splitFirstWhitespace(remain)
		args["search"] = trimQuotes(search)
		args["replacement"] = trimQuotes(repl)
		return action, args, ""

	default:
		// 未知动作：保留原始 remain 以便日志
		trimmedRemain := strings.TrimSpace(remain)
		if strings.Contains(action, "$") {
			switch strings.ToLower(trimmedRemain) {
			case "302", "307":
				args["url"] = action
				return strings.TrimSpace(trimmedRemain), args, ""
			}
		}
		if first, tail := splitFirstWhitespace(trimmedRemain); first != "" {
			switch strings.ToLower(strings.TrimSpace(tail)) {
			case "302", "307":
				args["url"] = first
				return strings.TrimSpace(tail), args, ""
			}
		}
		if strings.HasPrefix(action, "http://") || strings.HasPrefix(action, "https://") {
			switch strings.ToLower(trimmedRemain) {
			case "302", "307":
				args["url"] = action
				return strings.TrimSpace(trimmedRemain), args, ""
			}
		}
		args["_raw"] = trimmedRemain
		return action, args, ""
	}
}

// splitWhitespace 按空白切分（连续空白合并）。
func splitWhitespace(s string) []string {
	var out []string
	fields := strings.Fields(s)
	out = append(out, fields...)
	return out
}

// parseKVArgs 解析 "key=value key2=value2" 形式参数。
// 支持 data="..." 带引号值。
func parseKVArgs(s string, args map[string]string) {
	tokens := tokenizeKV(s)
	for _, t := range tokens {
		idx := strings.Index(t, "=")
		if idx <= 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(t[:idx]))
		val := strings.TrimSpace(t[idx+1:])
		val = trimQuotes(val)
		args[key] = val
	}
}

// tokenizeKV 简易 token 切分：尊重双引号包裹。
func tokenizeKV(s string) []string {
	var tokens []string
	var buf strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote:
			if c == '"' {
				inQuote = false
				buf.WriteByte(c)
			} else {
				buf.WriteByte(c)
			}
		case c == '"':
			inQuote = true
			buf.WriteByte(c)
		case c == ' ' || c == '\t':
			if buf.Len() > 0 {
				tokens = append(tokens, buf.String())
				buf.Reset()
			}
		default:
			buf.WriteByte(c)
		}
	}
	if buf.Len() > 0 {
		tokens = append(tokens, buf.String())
	}
	return tokens
}

// parseLoonScripts 解析 [Script] 段。
// 格式：http-request <pattern> script-path=<url>,requires-body=true,tag=名称[,binary-body-mode=true][,argument=...]
//
//	http-response <pattern> script-path=<url>,...
func parseLoonScripts(body string) []ir.ScriptRule {
	var scripts []ir.ScriptRule
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		s, err := parseLoonScriptLine(line)
		if err != nil || s == nil {
			continue
		}
		scripts = append(scripts, *s)
	}
	return scripts
}

// parseLoonScriptLine 解析单行 Loon 脚本规则。
func parseLoonScriptLine(line string) (*ir.ScriptRule, error) {
	// 形如：http-request <pattern> <comma-separated-params>
	phase, rest := splitFirstWhitespace(line)
	if rest == "" {
		return nil, fmt.Errorf("缺少 pattern 与参数: %q", line)
	}
	pattern, params := splitFirstWhitespace(rest)

	s := &ir.ScriptRule{Raw: line, Pattern: pattern}
	switch strings.ToLower(strings.TrimSpace(phase)) {
	case "http-request":
		s.Phase = 0
	case "http-response":
		s.Phase = 1
	case "cron":
		// cron 脚本不转换
		return nil, nil
	default:
		return nil, fmt.Errorf("未知脚本阶段: %q", phase)
	}

	tokens := splitCSVFields(params)
	args, _ := parseKeyValueList(tokens)
	if args["script-path"] == "" {
		// 兼容混入的 QX/Loon url script-* 语法
		if p, ok := parseMixedURLScriptParams(args, line); ok {
			return p, nil
		}
	}
	s.ScriptPath = args["script-path"]
	s.Tag = args["tag"]
	s.Argument = args["argument"]
	s.Engine = args["engine"]
	if v, ok := args["requires-body"]; ok {
		s.RequiresBody = strings.EqualFold(v, "true") || v == "1"
	}
	if v, ok := args["binary-body-mode"]; ok {
		s.BinaryBody = strings.EqualFold(v, "true") || v == "1"
	}
	if v, ok := args["max-size"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			s.MaxSize = n
		}
	}
	return s, nil
}

// parseLoonArguments 解析 [Argument] 段（仅记录，不参与转换）。
func parseLoonArguments(body string) []ir.Argument {
	var args []ir.Argument
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}
		args = append(args, ir.Argument{
			Key:   strings.TrimSpace(line[:idx]),
			Value: strings.TrimSpace(line[idx+1:]),
			Raw:   line,
		})
	}
	return args
}

// parseLoonMitM 解析 [MitM] 段。
// 格式：hostname = a.com, *.b.com
func parseLoonMitM(body string) []string {
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
