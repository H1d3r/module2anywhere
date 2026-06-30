// parser 包：QuantumultX .conf 解析器。
//
// QuantumultX 插件的文本格式与 Loon/Surge 段式结构不同，采用「行式规则」：
//   - 不强制使用段头（[rewrite_local] / [mitm] / [task_local] 等为可选）
//   - 每条重写规则格式：<pattern> url <action> [args...]
//   - hostname 通常以 "# hostname = a.com, b.com" 形式紧跟在注释行后
//   - 元数据使用 #!name=... / #!desc=... 形式（与 Loon 一致）
//
// 支持的动作（按 Script-Hub 风格归类）：
//   - reject / reject-200 / reject-dict / reject-img / reject-array
//   - 302 <url>（支持 $1/$2 捕获组，降级为脚本）
//   - response-body <search> url response-body <replacement>（双 url 标记 → body-replace）
//   - echo-response <content-type> url echo-response <body-or-url>（模拟响应）
//   - script-request-body / script-response-body / script-request-header / script-response-header
//   - jsonjq-response-body '<jq-expr>'（JSON 操作）
//   - script-analyze-echo-response <script-url>（脚本分析后模拟响应）
//
// 参考：
//   - https://github.com/Script-Hub-Org/Script-Hub（QX ↔ Surge/Shadowrocket/Loon/Stash 转换参考）
//   - Quantumult X 官方文档（http_backend / script_request_body 等）
package parser

import (
	"strings"

	"github.com/H1d3r/module2anywhere/ir"
)

// ParseQuantumultX 解析 QuantumultX .conf 内容为 IR Module。
// 自动跳过 [rewrite_local] / [mitm] / [task_local] / [server_local] / [filter_local] / [hostname] 等段头。
// 同时支持 Greasemonkey/Tampermonkey 风格的 UserScript 头（// @ScriptName 等）作为元数据补充。
func ParseQuantumultX(content string) (*ir.Module, error) {
	m := &ir.Module{Source: ir.SourceQuantumultX, RawMeta: map[string]string{}}

	// 优先提取 GM/TM 风格 UserScript 头中的元数据
	applyUserScriptMeta(m, content)

	// 切分为行，逐行处理（行式格式，无强制段结构）
	rawLines := strings.Split(content, "\n")

	// 第一遍：扫描元数据 #!key=val 与 hostname 声明
	// hostname 可能跨多段或以注释行 + 下一行的形式出现
	var hostnames []string
	for i := 0; i < len(rawLines); i++ {
		line := strings.TrimSpace(rawLines[i])
		if line == "" {
			continue
		}
		// 元数据
		if strings.HasPrefix(line, "#!") {
			kv := strings.TrimPrefix(line, "#!")
			idx := strings.Index(kv, "=")
			if idx > 0 {
				key := strings.TrimSpace(kv[:idx])
				val := strings.TrimSpace(kv[idx+1:])
				m.RawMeta[key] = val
				switch key {
				case "name":
					m.Name = val
				case "desc":
					m.Desc = val
				case "author":
					m.Author = val
				case "homepage":
					m.Homepage = val
				case "date":
					m.Date = val
				}
			}
			// 元数据行后如果紧跟 hostname 行则一并提取
			if i+1 < len(rawLines) {
				next := strings.TrimSpace(rawLines[i+1])
				if hs := extractHostnameValue(next); hs != "" {
					hostnames = append(hostnames, normalizeHostnames(hs)...)
				}
			}
			continue
		}
		// 注释行（# 或 ; 开头）后可能跟 hostname
		if isCommentLine(line) {
			if i+1 < len(rawLines) {
				next := strings.TrimSpace(rawLines[i+1])
				if hs := extractHostnameValue(next); hs != "" {
					hostnames = append(hostnames, normalizeHostnames(hs)...)
				}
			}
			continue
		}
		// 非注释行中的 hostname = ... 声明
		if hs := extractHostnameValue(line); hs != "" {
			hostnames = append(hostnames, normalizeHostnames(stripInlineComment(hs))...)
			continue
		}
		// 段头（含 hostname = ... 的 [hostname] 段体已通过行内容捕获）
		if isQXSectionHeader(line) {
			// 段体扫描到下一段头为止，提取 hostname
			for j := i + 1; j < len(rawLines); j++ {
				inner := strings.TrimSpace(rawLines[j])
				if isQXSectionHeader(inner) {
					break
				}
				if hs := extractHostnameValue(inner); hs != "" {
					hostnames = append(hostnames, normalizeHostnames(stripInlineComment(hs))...)
				}
			}
			continue
		}
	}
	m.Hostnames = dedupStrings(hostnames)
	m.Arguments = mergeArguments(parseMetadataArguments(m.RawMeta["arguments"], m.RawMeta["arguments-desc"]), m.Arguments)

	// 第二遍：解析行式规则
	for _, raw := range rawLines {
		line := strings.TrimSpace(raw)
		if line == "" || isQXSectionHeader(line) {
			continue
		}
		if strings.HasPrefix(line, "#!") {
			continue
		}
		// hostname 行
		if extractHostnameValue(line) != "" {
			continue
		}
		// 注释行（# 或 ; 开头）：跳过，但路由规则可能在注释后
		if isCommentLine(line) {
			continue
		}
		// 解析单行规则
		rule, kind := parseQuantumultXLine(line)
		if rule != nil {
			switch kind {
			case qxRuleRewrite:
				m.Rewrites = append(m.Rewrites, *rule)
			case qxRuleScript:
				if s, ok := ruleToScript(*rule); ok {
					m.Scripts = append(m.Scripts, s)
				}
			}
			continue
		}
		// 路由规则：TYPE,value,action[,options...]
		if r := parseQXRoutingRule(line); r != nil {
			m.Rules = append(m.Rules, *r)
		}
	}

	return m, nil
}

// qxRuleKind 区分 QX 行式规则类型，便于统一进入 IR。
type qxRuleKind int

const (
	qxRuleRewrite qxRuleKind = iota
	qxRuleScript
)

// applyUserScriptMeta 从 GM/TM 风格 UserScript 头中提取元数据。
// 支持 // @ScriptName / @Author / @Function / @Description / @UpdateTime / @Version 等。
// 仅在 ir.Module 对应字段为空时写入，避免覆盖 QX 原生 #!name= 等显式声明。
func applyUserScriptMeta(m *ir.Module, content string) {
	lines := strings.Split(content, "\n")
	inBlock := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "// ==UserScript==" {
			inBlock = true
			continue
		}
		if line == "// ==/UserScript==" {
			break
		}
		if !inBlock {
			continue
		}
		if !strings.HasPrefix(line, "// @") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "// @"))
		idx := strings.IndexFunc(rest, func(r rune) bool { return r == ' ' || r == '\t' })
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(rest[:idx])
		val := strings.TrimSpace(rest[idx:])
		if key == "" || val == "" {
			continue
		}
		m.RawMeta[key] = val
		switch strings.ToLower(key) {
		case "scriptname":
			if m.Name == "" {
				m.Name = val
			}
		case "author":
			if m.Author == "" {
				m.Author = val
			}
		case "function", "description":
			if m.Desc == "" {
				m.Desc = val
			}
		case "updatetime":
			if m.Date == "" {
				m.Date = val
			}
		case "homepage", "homepageurl":
			if m.Homepage == "" {
				m.Homepage = val
			}
		}
	}
}

// extractHostnameValue 从一行提取 hostname = 后的值。
// 返回空表示该行不是 hostname 声明。
func extractHostnameValue(line string) string {
	lower := strings.ToLower(line)
	// 形式：hostname = a.com, b.com  / hostname=a.com
	idx := strings.Index(lower, "hostname")
	if idx < 0 {
		return ""
	}
	rest := line[idx+len("hostname"):]
	// 必须是 " = " 或 "=" 紧跟
	rest = strings.TrimLeft(rest, " \t")
	if rest == "" {
		return ""
	}
	if rest[0] != '=' {
		return ""
	}
	rest = strings.TrimLeft(rest[1:], " \t")
	return rest
}

// isCommentLine 判断行是否为注释行（# 或 ; 开头，允许前导空白）。
func isCommentLine(line string) bool {
	if line == "" {
		return false
	}
	if line[0] == '#' || line[0] == ';' {
		return true
	}
	return false
}

// isQXSectionHeader 判断是否为 QX 段头（[rewrite_local] / [mitm] / [task_local] 等）。
func isQXSectionHeader(line string) bool {
	return strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]")
}

// parseQuantumultXLine 解析单行 QX 规则。
// 格式：<pattern> url <action> [args...]
//
//   - <pattern> task <cron>        → 任务，丢弃（Anywhere 不支持 cron）
//   - <pattern> url reject         → RewriteRule
//   - <pattern> url response-body  → 复杂双 url 标记，需要聚合下一段
//   - <pattern> url echo-response  → 复杂双 url 标记，需要聚合下一段
//   - <pattern> url script-*       → ScriptRule
func parseQuantumultXLine(line string) (*ir.RewriteRule, qxRuleKind) {
	// 跳过 hostname 行
	if extractHostnameValue(line) != "" {
		return nil, qxRuleRewrite
	}
	// 拆分为 token（按空白切分，保留行结构用于聚合）
	parts := splitQXTokens(line)
	if len(parts) < 3 {
		return nil, qxRuleRewrite
	}
	pattern := parts[0]
	verb := strings.ToLower(parts[1]) // "url" 或 "task"

	if verb == "task" {
		// 任务类：丢弃
		return nil, qxRuleRewrite
	}
	if verb != "url" {
		// 未知 verb：忽略
		return nil, qxRuleRewrite
	}

	action := strings.ToLower(parts[2])
	r := &ir.RewriteRule{Raw: line, Args: map[string]string{}, Pattern: pattern}
	r.Action = action

	// 根据 action 解析后续参数
	switch action {
	case "reject", "reject-200", "reject-dict", "reject-img", "reject-array":
		// 无额外参数
		return r, qxRuleRewrite

	case "302":
		// 302 <url>，可能含 $1 捕获组
		if len(parts) >= 4 {
			r.Args["url"] = strings.Join(parts[3:], " ")
		}
		return r, qxRuleRewrite

	case "307":
		// 307 <url>，可能含 $1 捕获组
		if len(parts) >= 4 {
			r.Args["url"] = strings.Join(parts[3:], " ")
		}
		return r, qxRuleRewrite

	case "rewrite", "transparent":
		// 透明 URL 重写：rewrite <url> / transparent <url>
		if len(parts) >= 4 {
			r.Args["url"] = strings.Join(parts[3:], " ")
		}
		return r, qxRuleRewrite

	case "response-body":
		// 形如：pattern url response-body <search> url response-body <replacement>
		// 简化处理：把 search 与 replacement 都放在 args
		// parts: [0]=pattern [1]=url [2]=response-body [3]=search [4]=url [5]=response-body [6]=replacement
		if len(parts) >= 4 {
			r.Args["search"] = parts[3]
		}
		if len(parts) >= 7 {
			r.Args["replacement"] = parts[6]
		}
		parseQXTrailingKeyValueArgs(r.Args, parts[7:])
		return r, qxRuleRewrite

	case "echo-response":
		// 形如：pattern url echo-response <content-type> url echo-response [body] <body-or-url>
		// parts: [0]=pattern [1]=url [2]=echo-response [3]=content-type [4]=url [5]=echo-response [6..]=body
		if len(parts) >= 4 {
			r.Args["content-type"] = parts[3]
		}
		bodyParts := parts[6:]
		bodyParts, trailingArgs := splitQXTrailingKeyValueTokens(bodyParts)
		if len(bodyParts) > 0 {
			body := strings.Join(bodyParts, " ")
			body = strings.TrimPrefix(body, "body ")
			r.Args["body"] = body
		}
		parseQXTrailingKeyValueArgs(r.Args, trailingArgs)
		return r, qxRuleRewrite

	case "jsonjq-response-body":
		// jsonjq-response-body '<jq-expr>'，可能带单引号
		if len(parts) >= 4 {
			expr := strings.Join(parts[3:], " ")
			expr = trimQuotes(expr)
			r.Args["jq"] = expr
		}
		return r, qxRuleRewrite

	case "script-request-body",
		"script-response-body",
		"script-request-header",
		"script-response-header",
		"script-analyze-echo-response":
		// 形如：pattern url script-response-body <script-url>
		if len(parts) >= 4 {
			r.Args["url"] = parts[3]
		}
		parseQXTrailingKeyValueArgs(r.Args, parts[4:])
		// 转为 ScriptRule
		return r, qxRuleScript

	default:
		// 未知动作：保留原行，记录到 args
		if len(parts) >= 4 {
			r.Args["_raw"] = strings.Join(parts[3:], " ")
		}
		return r, qxRuleRewrite
	}
}

// parseQXRoutingRule 解析 QX 行式路由规则。
// 格式：TYPE,value,action[,options...]，例如 DOMAIN-SUFFIX,example.com,DIRECT。
// 不是路由规则时返回 nil。
func parseQXRoutingRule(line string) *ir.RoutingRule {
	// 行式 rewrite 规则以 ^ 开头，已在 parseQuantumultXLine 处理；此处跳过。
	if strings.HasPrefix(line, "^") {
		return nil
	}
	fields := splitCSVFields(line)
	if len(fields) < 3 {
		return nil
	}
	ruleType := strings.ToUpper(strings.TrimSpace(fields[0]))
	// QX 路由规则类型白名单
	switch ruleType {
	case "DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "DOMAIN-WILDCARD",
		"HOST", "HOST-SUFFIX", "HOST-KEYWORD", "HOST-WILDCARD",
		"DOMAIN-SET", "RULE-SET", "IP-CIDR", "IP-CIDR6", "IP6-CIDR", "GEOIP", "USER-AGENT",
		"DEST-PORT", "SRC-PORT", "SRC-IP", "SRC-IP-CIDR", "PROCESS-NAME",
		"SUBNET", "CELLULAR-RADIO":
	default:
		return nil
	}
	return &ir.RoutingRule{
		Raw:     line,
		Type:    ruleType,
		Value:   strings.TrimSpace(fields[1]),
		Action:  strings.ToUpper(strings.TrimSpace(fields[2])),
		Options: trimSpaces(fields[3:]),
	}
}

// trimSpaces 批量去除字符串切片中每个元素的空白。
func trimSpaces(ss []string) []string {
	for i, s := range ss {
		ss[i] = strings.TrimSpace(s)
	}
	return ss
}

// splitQXTokens 按空白切分 QX 行。
// 注意：保留单引号包裹内容，jsonjq-response-body 的参数含单引号。
func splitQXTokens(line string) []string {
	var tokens []string
	var buf strings.Builder
	inSingle := false
	inDouble := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inSingle:
			buf.WriteByte(c)
			if c == '\'' {
				inSingle = false
			}
		case inDouble:
			buf.WriteByte(c)
			if c == '"' {
				inDouble = false
			}
		case c == '\'':
			inSingle = true
			buf.WriteByte(c)
		case c == '"':
			inDouble = true
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

// extractBetween 从 parts[start:] 提取直到 stopAt（不区分大小写）。
// stopAt 为空表示提取到末尾。
func extractBetween(parts []string, start int, stopAt string) string {
	if start >= len(parts) {
		return ""
	}
	if stopAt == "" {
		return strings.Join(parts[start:], " ")
	}
	var collected []string
	for i := start; i < len(parts); i++ {
		if strings.EqualFold(parts[i], stopAt) {
			break
		}
		collected = append(collected, parts[i])
	}
	return strings.Join(collected, " ")
}

// splitQXTrailingKeyValueTokens 从尾部拆出连续的 key=value token，避免把 body 误当尾参。
func splitQXTrailingKeyValueTokens(tokens []string) (bodyTokens []string, trailingTokens []string) {
	cut := len(tokens)
	for cut > 0 {
		token := tokens[cut-1]
		if isQXTrailingKey(token) {
			cut--
			trailingTokens = append([]string{token}, trailingTokens...)
			continue
		}
		break
	}
	return tokens[:cut], trailingTokens
}

// parseQXTrailingKeyValueArgs 将尾部 key=value tokens 写入 args。
func parseQXTrailingKeyValueArgs(args map[string]string, tokens []string) {
	for _, token := range tokens {
		if idx := strings.Index(token, "="); idx > 0 && isQXTrailingKey(token) {
			args[strings.ToLower(strings.TrimSpace(token[:idx]))] = strings.TrimSpace(token[idx+1:])
		}
	}
}

// isQXTrailingKey 仅把常见的尾参键视为可剥离参数，避免正文中意外的 `=` 被误切。
func isQXTrailingKey(token string) bool {
	idx := strings.Index(token, "=")
	if idx <= 0 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(token[:idx])) {
	case "format", "body", "content-type", "contenttype", "requires-body", "binary-body-mode", "tag", "argument", "max-size", "engine":
		return true
	default:
		return false
	}
}

// ruleToScript 将 QX 脚本类 RewriteRule 转为 ScriptRule。
// 通过 action 推断 phase：
//
//	script-request-body / script-request-header    → 0
//	script-response-body / script-response-header  → 1
//	script-analyze-echo-response                   → 1（响应阶段 + echo 模拟）
func ruleToScript(r ir.RewriteRule) (ir.ScriptRule, bool) {
	s := ir.ScriptRule{Raw: r.Raw, Pattern: r.Pattern, ScriptPath: r.Args["url"]}
	if s.ScriptPath == "" {
		return s, false
	}
	switch r.Action {
	case "script-request-body", "script-request-header":
		s.Phase = 0
	case "script-response-body", "script-response-header", "script-analyze-echo-response":
		s.Phase = 1
	default:
		return s, false
	}
	return s, true
}

// dedupStrings 字符串去重，保持顺序。
func dedupStrings(ss []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
