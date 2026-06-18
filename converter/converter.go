// converter 包：核心转换器，将 IR Module 转换为 Anywhere .arrs / .amrs 规则集。
package converter

import (
	"context"
	"fmt"
	"strings"

	"github.com/Loon2Anywhere/loon2anywhere/fetcher"
	"github.com/Loon2Anywhere/loon2anywhere/ir"
)

// Result 转换结果。
type Result struct {
	Arrs     string // .arrs 文件内容（无路由规则时为空）
	Amrs     string // .amrs 文件内容（无 MITM 规则时为空）
	ArrsName string // .arrs 文件名（不含扩展名）
	AmrsName string // .amrs 文件名（不含扩展名）
	Report   Report // 转换报告
}

// Report 转换报告，记录跳过/降级项。
type Report struct {
	Skipped   []string // 不可转换的规则
	Degraded  []string // 降级处理的规则
	Warnings  []string // 警告信息
	ScriptErr []string // 脚本下载/改写失败
}

// AddSkipped 记录跳过项。
func (r *Report) AddSkipped(reason string) {
	r.Skipped = append(r.Skipped, reason)
}

// AddDegraded 记录降级项。
func (r *Report) AddDegraded(reason string) {
	r.Degraded = append(r.Degraded, reason)
}

// AddWarning 记录警告。
func (r *Report) AddWarning(reason string) {
	r.Warnings = append(r.Warnings, reason)
}

// AddScriptErr 记录脚本错误。
func (r *Report) AddScriptErr(reason string) {
	r.ScriptErr = append(r.ScriptErr, reason)
}

// String 返回报告的可读形式。
func (r *Report) String() string {
	var buf strings.Builder
	if len(r.Skipped) > 0 {
		buf.WriteString("=== 跳过（不可转换） ===\n")
		for _, s := range r.Skipped {
			buf.WriteString("  - " + s + "\n")
		}
	}
	if len(r.Degraded) > 0 {
		buf.WriteString("=== 降级处理 ===\n")
		for _, s := range r.Degraded {
			buf.WriteString("  - " + s + "\n")
		}
	}
	if len(r.Warnings) > 0 {
		buf.WriteString("=== 警告 ===\n")
		for _, s := range r.Warnings {
			buf.WriteString("  - " + s + "\n")
		}
	}
	if len(r.ScriptErr) > 0 {
		buf.WriteString("=== 脚本错误 ===\n")
		for _, s := range r.ScriptErr {
			buf.WriteString("  - " + s + "\n")
		}
	}
	if buf.Len() == 0 {
		buf.WriteString("(无报告项)\n")
	}
	return buf.String()
}

// Converter 转换器。
type Converter struct {
	Fetcher *fetcher.Fetcher
	Opts    Options
	BaseURL string // 远程模块的 base URL，用于解析相对 script-path
}

// New 创建转换器。
func New(f *fetcher.Fetcher, opts Options) *Converter {
	return &Converter{Fetcher: f, Opts: opts}
}

// Convert 执行转换，返回 Result。
func (c *Converter) Convert(ctx context.Context, m *ir.Module) (*Result, error) {
	res := &Result{Report: Report{}}
	baseName := m.Name
	if baseName == "" {
		baseName = "Loon2Anywhere"
	}
	res.ArrsName = baseName + ".arrs"
	res.AmrsName = baseName + ".amrs"

	// 0. 清理 hostname：去除含通配符 ? / * 的项（Anywhere 不支持）
	cleanedHosts := make([]string, 0, len(m.Hostnames))
	for _, h := range m.Hostnames {
		if strings.ContainsAny(h, "?*") {
			res.Report.AddWarning(fmt.Sprintf("hostname 含通配符无法静态展开，已跳过: %s（请手动添加具体主机）", h))
			continue
		}
		cleanedHosts = append(cleanedHosts, h)
	}
	m.Hostnames = cleanedHosts

	// 1. 路由规则 → .arrs
	arrsLines, amrsFromRules := c.convertRoutingRules(m.Rules, &res.Report)
	res.Arrs = c.generateArrs(baseName, arrsLines, m)

	// 2. MITM 重写规则 → .amrs
	amrsLines := amrsFromRules
	amrsLines = append(amrsLines, c.convertRewriteRules(ctx, m, &res.Report)...)
	amrsLines = append(amrsLines, c.convertHeaderRules(m.HeaderRWs, &res.Report)...)
	amrsLines = append(amrsLines, c.convertMapLocals(ctx, m.MapLocals, &res.Report)...)
	amrsLines = append(amrsLines, c.convertScriptRules(ctx, m, &res.Report)...)

	// 3. accept-encoding 预处理对（可选）
	if c.Opts.EncodingPreprocess {
		amrsLines = c.addEncodingPreprocess(amrsLines)
	}

	res.Amrs = c.generateAmrs(baseName, m.Hostnames, amrsLines, m)

	return res, nil
}

// convertRoutingRules 转换路由规则。
// 返回 .arrs 行与（URL-REGEX REJECT 类转入 .amrs 的）行。
func (c *Converter) convertRoutingRules(rules []ir.RoutingRule, report *Report) (arrsLines, amrsLines []string) {
	for _, r := range rules {
		switch r.Type {
		case "DOMAIN-SUFFIX", "DOMAIN":
			arrsLines = append(arrsLines, fmt.Sprintf("2, %s", r.Value))
		case "DOMAIN-KEYWORD":
			arrsLines = append(arrsLines, fmt.Sprintf("3, %s", r.Value))
		case "IP-CIDR":
			arrsLines = append(arrsLines, fmt.Sprintf("0, %s", r.Value))
		case "IP-CIDR6":
			arrsLines = append(arrsLines, fmt.Sprintf("1, %s", r.Value))
		case "URL-REGEX":
			// REJECT 类 → .amrs rewrite reject
			if ir.IsRejectAction(r.Action) {
				line := c.convertURLRegexReject(r, report)
				if line != "" {
					amrsLines = append(amrsLines, line)
				}
			} else {
				report.AddSkipped(fmt.Sprintf("URL-REGEX 非 REJECT 类不可转换: %s", r.Raw))
			}
		case "GEOIP", "PROCESS-NAME", "DEST-PORT", "SRC-PORT", "SRC-IP", "SRC-IP-CIDR", "CELLULAR-RADIO", "SUBNET":
			report.AddSkipped(fmt.Sprintf("%s 不可转换: %s", r.Type, r.Raw))
		case "DOMAIN-SET", "RULE-SET":
			// 远程列表需单独下载并展开，此处仅记录
			report.AddWarning(fmt.Sprintf("DOMAIN-SET/RULE-SET 需单独下载展开: %s", r.Raw))
		default:
			report.AddSkipped(fmt.Sprintf("未知规则类型 %s: %s", r.Type, r.Raw))
		}
	}
	return
}

// convertURLRegexReject 转换 URL-REGEX REJECT 类规则为 .amrs rewrite 行。
func (c *Converter) convertURLRegexReject(r ir.RoutingRule, report *Report) string {
	pattern := ConvertURLPattern(r.Value, c.Opts.GeneralizeHost)
	switch r.Action {
	case "REJECT", "REJECT-200":
		return fmt.Sprintf("0, 0, %s, 2", pattern)
	case "REJECT-DICT":
		return fmt.Sprintf("0, 0, %s, 2, {}", pattern)
	case "REJECT-ARRAY":
		return fmt.Sprintf("0, 0, %s, 2, []", pattern)
	case "REJECT-IMG":
		return fmt.Sprintf("0, 0, %s, 3", pattern)
	default:
		report.AddSkipped(fmt.Sprintf("URL-REGEX 未知 REJECT 动作 %s: %s", r.Action, r.Raw))
		return ""
	}
}

// convertRewriteRules 转换重写规则为 .amrs 行。
func (c *Converter) convertRewriteRules(ctx context.Context, m *ir.Module, report *Report) []string {
	var lines []string
	for _, r := range m.Rewrites {
		line, err := c.convertRewriteRule(ctx, r, report)
		if err != nil {
			report.AddSkipped(fmt.Sprintf("重写规则转换失败 %q: %v", r.Raw, err))
			continue
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// convertRewriteRule 转换单条重写规则。
func (c *Converter) convertRewriteRule(ctx context.Context, r ir.RewriteRule, report *Report) (string, error) {
	pattern := ConvertURLPattern(r.Pattern, c.Opts.GeneralizeHost)

	switch r.Action {
	// 拒绝类
	case "reject", "reject-200":
		return fmt.Sprintf("0, 0, %s, 2", pattern), nil
	case "reject-dict":
		return fmt.Sprintf("0, 0, %s, 2, {}", pattern), nil
	case "reject-array":
		return fmt.Sprintf("0, 0, %s, 2, []", pattern), nil
	case "reject-img":
		return fmt.Sprintf("0, 0, %s, 3", pattern), nil

	// 重定向类
	case "302":
		url := r.Args["url"]
		if HasCaptureGroup(url) {
			report.AddDegraded(fmt.Sprintf("302 带捕获组转为脚本: %s", r.Raw))
			b64 := BuildRedirectScript(pattern, url, 302)
			return fmt.Sprintf("0, 100, %s, %s", pattern, b64), nil
		}
		return fmt.Sprintf("0, 0, %s, 1, %s", pattern, url), nil
	case "307":
		url := r.Args["url"]
		if HasCaptureGroup(url) {
			report.AddDegraded(fmt.Sprintf("307 带捕获组转为脚本(降级302): %s", r.Raw))
			b64 := BuildRedirectScript(pattern, url, 307)
			return fmt.Sprintf("0, 100, %s, %s", pattern, b64), nil
		}
		report.AddDegraded(fmt.Sprintf("307 降级为 302: %s", r.Raw))
		return fmt.Sprintf("0, 0, %s, 1, %s", pattern, url), nil

	// 模拟响应
	case "mock-response-body":
		body := r.Args["data"]
		return fmt.Sprintf("0, 0, %s, 2, %s", pattern, QuoteField(body)), nil

	// JSON 体重写
	case "response-body-json-del":
		path := DotPathToJSONPath(r.Args["path"])
		return fmt.Sprintf("1, 5, %s, delete, %s", pattern, path), nil
	case "response-body-json-add":
		path := DotPathToJSONPath(r.Args["path"])
		value := r.Args["value"]
		return fmt.Sprintf("1, 5, %s, add, %s, %s", pattern, path, QuoteField(value)), nil
	case "response-body-json-replace":
		path := DotPathToJSONPath(r.Args["path"])
		value := r.Args["value"]
		return fmt.Sprintf("1, 5, %s, replace, %s, %s", pattern, path, QuoteField(value)), nil

	// 内联 JS 体重写
	case "request-header", "request-body":
		b64 := EncodeInlineRewriteJS(r.RawJS, 0)
		return fmt.Sprintf("0, 100, %s, %s", pattern, b64), nil
	case "response-body":
		b64 := EncodeInlineRewriteJS(r.RawJS, 1)
		return fmt.Sprintf("1, 100, %s, %s", pattern, b64), nil
	case "_request-header", "_request-body":
		b64 := EncodeInlineRewriteJS(r.RawJS, 0)
		return fmt.Sprintf("0, 100, %s, %s", pattern, b64), nil
	case "_response-body":
		b64 := EncodeInlineRewriteJS(r.RawJS, 1)
		return fmt.Sprintf("1, 100, %s, %s", pattern, b64), nil

	default:
		report.AddSkipped(fmt.Sprintf("未知重写动作 %s: %s", r.Action, r.Raw))
		return "", nil
	}
}

// convertHeaderRules 转换 Surge [Header Rewrite] 规则。
func (c *Converter) convertHeaderRules(rules []ir.HeaderRule, report *Report) []string {
	var lines []string
	for _, r := range rules {
		pattern := ConvertURLPattern(r.Pattern, c.Opts.GeneralizeHost)
		switch r.Op {
		case "add":
			lines = append(lines, fmt.Sprintf("%d, 1, %s, %s, %s", r.Phase, pattern, QuoteField(r.Name), QuoteField(r.Value)))
		case "replace":
			lines = append(lines, fmt.Sprintf("%d, 3, %s, %s, %s", r.Phase, pattern, QuoteField(r.Name), QuoteField(r.Value)))
		case "delete":
			lines = append(lines, fmt.Sprintf("%d, 2, %s, %s", r.Phase, pattern, QuoteField(r.Name)))
		default:
			report.AddSkipped(fmt.Sprintf("未知 header 操作 %s: %s", r.Op, r.Raw))
		}
	}
	return lines
}

// convertMapLocals 转换 Surge [Map Local] 规则。
// 简化处理：若 data 是 URL，下载内容嵌入；否则直接用 data 值。
func (c *Converter) convertMapLocals(ctx context.Context, rules []ir.MapLocalRule, report *Report) []string {
	var lines []string
	for _, r := range rules {
		pattern := ConvertURLPattern(r.Pattern, c.Opts.GeneralizeHost)
		if r.DataURL == "" {
			report.AddSkipped(fmt.Sprintf("Map Local 无 data: %s", r.Raw))
			continue
		}
		var body string
		if fetcher.IsRemote(r.DataURL) && c.Fetcher != nil {
			content, err := c.Fetcher.Fetch(ctx, r.DataURL)
			if err != nil {
				report.AddScriptErr(fmt.Sprintf("Map Local 下载 data 失败 %s: %v", r.DataURL, err))
				continue
			}
			body = content
		} else {
			body = r.DataURL
		}
		lines = append(lines, fmt.Sprintf("0, 0, %s, 2, %s", pattern, QuoteField(body)))
	}
	return lines
}

// convertScriptRules 转换 [Script] 段规则。
func (c *Converter) convertScriptRules(ctx context.Context, m *ir.Module, report *Report) []string {
	var lines []string
	for _, s := range m.Scripts {
		pattern := ConvertURLPattern(s.Pattern, c.Opts.GeneralizeHost)
		if s.ScriptPath == "" {
			report.AddSkipped(fmt.Sprintf("脚本无 script-path: %s", s.Raw))
			continue
		}
		b64, err := FetchAndEncodeScript(ctx, c.Fetcher, s.ScriptPath, c.BaseURL, c.Opts.FetchScripts, s.Phase)
		if err != nil {
			report.AddScriptErr(fmt.Sprintf("脚本下载失败 %s: %v", s.ScriptPath, err))
			continue
		}
		lines = append(lines, fmt.Sprintf("%d, 100, %s, %s", s.Phase, pattern, b64))
	}
	return lines
}

// addEncodingPreprocess 为含 body 处理的 URL 添加 accept-encoding 预处理对。
// 策略：扫描所有 phase=1 的 body-json/script 规则，提取其 pattern，添加：
//
//	0, 2, <pattern>, accept-encoding
//	0, 1, <pattern>, accept-encoding, identity
func (c *Converter) addEncodingPreprocess(lines []string) []string {
	// 收集需要预处理的 pattern（phase=1 且 op ∈ {5, 100, 101, 4}）
	patterns := make(map[string]bool)
	for _, line := range lines {
		fields := splitAmrsFields(line)
		if len(fields) < 2 {
			continue
		}
		phase := fields[0]
		op := fields[1]
		if phase != "1" {
			continue
		}
		switch op {
		case "4", "5", "100", "101":
			if len(fields) >= 3 {
				patterns[fields[2]] = true
			}
		}
	}
	if len(patterns) == 0 {
		return lines
	}
	// 在原规则前插入预处理对
	var pre []string
	for p := range patterns {
		pre = append(pre, fmt.Sprintf("0, 2, %s, accept-encoding", p))
		pre = append(pre, fmt.Sprintf("0, 1, %s, accept-encoding, identity", p))
	}
	return append(pre, lines...)
}

// splitAmrsFields 简易按逗号切分 .amrs 行字段（不处理引号内逗号，仅用于模式识别）。
func splitAmrsFields(line string) []string {
	// 简化：仅切前 3 个字段
	var fields []string
	rest := line
	for i := 0; i < 3 && rest != ""; i++ {
		idx := strings.Index(rest, ",")
		if idx < 0 {
			fields = append(fields, strings.TrimSpace(rest))
			rest = ""
			break
		}
		fields = append(fields, strings.TrimSpace(rest[:idx]))
		rest = rest[idx+1:]
	}
	if rest != "" {
		fields = append(fields, strings.TrimSpace(rest))
	}
	return fields
}

// generateArrs 生成 .arrs 文件内容。
func (c *Converter) generateArrs(name string, lines []string, m *ir.Module) string {
	if len(lines) == 0 {
		return ""
	}
	var buf strings.Builder
	if c.Opts.IncludeMetadata {
		buf.WriteString(c.metadataComments(m))
	}
	buf.WriteString(fmt.Sprintf("name = %s\n", name))
	buf.WriteString("\n")
	for _, l := range lines {
		buf.WriteString(l + "\n")
	}
	return buf.String()
}

// generateAmrs 生成 .amrs 文件内容。
func (c *Converter) generateAmrs(name string, hostnames, lines []string, m *ir.Module) string {
	if len(lines) == 0 && len(hostnames) == 0 {
		return ""
	}
	var buf strings.Builder
	if c.Opts.IncludeMetadata {
		buf.WriteString(c.metadataComments(m))
	}
	buf.WriteString(fmt.Sprintf("name = %s\n", name))
	if len(hostnames) > 0 {
		buf.WriteString(fmt.Sprintf("hostname = %s\n", strings.Join(hostnames, ", ")))
	}
	buf.WriteString("\n")
	for _, l := range lines {
		buf.WriteString(l + "\n")
	}
	return buf.String()
}

// metadataComments 生成元数据注释头。
func (c *Converter) metadataComments(m *ir.Module) string {
	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("# 由 loon2anywhere 从 %s 模块转换\n", m.Source))
	if m.Desc != "" {
		buf.WriteString(fmt.Sprintf("# desc: %s\n", m.Desc))
	}
	if m.Author != "" {
		buf.WriteString(fmt.Sprintf("# author: %s\n", m.Author))
	}
	if m.Homepage != "" {
		buf.WriteString(fmt.Sprintf("# homepage: %s\n", m.Homepage))
	}
	if m.Date != "" {
		buf.WriteString(fmt.Sprintf("# date: %s\n", m.Date))
	}
	buf.WriteString("\n")
	return buf.String()
}
