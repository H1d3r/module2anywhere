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
		pattern, rest := splitFirstWhitespace(line)
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

// parseSurgeURLRewriteAction 解析 Surge [URL Rewrite] 动作。
func parseSurgeURLRewriteAction(rest string) (string, map[string]string, string) {
	args := make(map[string]string)
	action, remain := splitFirstWhitespace(rest)
	action = strings.ToLower(strings.TrimSpace(action))

	switch action {
	case "reject", "reject-200", "reject-dict", "reject-array", "reject-img":
		return action, args, ""

	case "302", "307":
		args["url"] = strings.TrimSpace(remain)
		return action, args, ""

	case "_request-header", "_request-body", "_response-body":
		// 内联 JS
		return action, args, strings.TrimSpace(remain)

	case "_header-del":
		// Surge _header-del <name>：删除请求头（与 Loon header-del 等价）
		args["header"] = strings.TrimSpace(remain)
		// 归一化为 header-del 动作，便于 converter 统一处理
		return "header-del", args, ""

	default:
		// Surge 还有 header rewrite 等动作，但通常在独立段
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
			r.Phase = 0
		case "response-header":
			r.Phase = 1
		default:
			continue
		}
		op, rest3 := splitFirstWhitespace(rest2)
		r.Op = strings.ToLower(strings.TrimSpace(op))
		// 剩余：name [value]
		tokens := splitWhitespace(rest3)
		if len(tokens) >= 1 {
			r.Name = tokens[0]
		}
		if len(tokens) >= 2 {
			r.Value = strings.Join(tokens[1:], " ")
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
			switch key {
			case "data":
				r.DataURL = val
			case "header":
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

// parseSurgeMITM 解析 [MITM] 段。
// 格式：hostname = %APPEND% a.com, b.com
//
//	hostname = a.com, b.com
func parseSurgeMITM(body string) []string {
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
			return normalizeHostnames(line[idx+1:])
		}
	}
	return nil
}
