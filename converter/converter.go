// converter 包：核心转换器，将 IR Module 转换为 Anywhere .arrs / .amrs 规则集。
package converter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/H1d3r/module2anywhere/fetcher"
	"github.com/H1d3r/module2anywhere/ir"
)

// ArrsGroup 单个 .arrs 分组（按 routing action 拆分）。
type ArrsGroup struct {
	Content  string // .arrs 文件内容
	Name     string // .arrs 文件名（含 .arrs 扩展名）
	Routing  int    // routing 值：0=未指定, 1=DIRECT, 2=REJECT
	Endpoint string // 对应的服务端点路径（如 /direct.arrs, /reject.arrs, /rule.arrs）
}

// Result 转换结果。
type Result struct {
	Arrs       string      // .arrs 文件内容（无路由规则时为空），兼容旧接口，合并所有分组
	Amrs       string      // .amrs 文件内容（无 MITM 规则时为空）
	ArrsName   string      // .arrs 文件名（不含扩展名），兼容旧接口
	AmrsName   string      // .amrs 文件名（不含扩展名）
	ArrsGroups []ArrsGroup // 按 routing 拆分的 .arrs 分组列表
	Report     Report      // 转换报告
}

// MarshalBinary 将 Result 序列化为 JSON 字节（用于缓存存储）。
func (r *Result) MarshalBinary() ([]byte, error) {
	return json.Marshal(r)
}

// UnmarshalBinary 从 JSON 字节反序列化 Result（用于缓存读取）。
func (r *Result) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, r)
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
	Fetcher   *fetcher.Fetcher
	Opts      Options
	BaseURL   string   // 远程模块的 base URL，用于解析相对 script-path
	Hostnames []string // MITM hostname 列表（用于安全主机泛化判断）

	// SourceURL 注释中显示的「模块来源 URL」。
	//   - 本地文件：留空
	//   - 远程模块：原始 URL（量子态 add-resource 链接展开前）
	//   - Web 服务中转：原始请求 URL
	SourceURL string

	// ServiceURL Web 服务的本机地址（用于在 .amrs/.arrs 头部添加「本链接」注释）。
	// 仅在 Web 服务模式下设置。
	ServiceURL string
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
		baseName = "module2anywhere"
	}
	res.ArrsName = baseName + ".arrs"
	res.AmrsName = baseName + ".amrs"

	// 注入 hostname 列表供后续 pattern 主机泛化判断使用
	c.Hostnames = m.Hostnames

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

	// 1. 路由规则 → .arrs（按 action 分组：DIRECT/REJECT/其他）
	directLines, rejectLines, otherLines, amrsFromRules := c.convertRoutingRules(m.Rules, &res.Report)

	// 生成分组 .arrs
	var groups []ArrsGroup
	if len(directLines) > 0 {
		content := c.generateArrs(baseName+"-Direct", directLines, m, 1)
		groups = append(groups, ArrsGroup{
			Content:  content,
			Name:     baseName + "-Direct.arrs",
			Routing:  1,
			Endpoint: "/direct.arrs",
		})
	}
	if len(rejectLines) > 0 {
		content := c.generateArrs(baseName+"-Reject", rejectLines, m, 2)
		groups = append(groups, ArrsGroup{
			Content:  content,
			Name:     baseName + "-Reject.arrs",
			Routing:  2,
			Endpoint: "/reject.arrs",
		})
	}
	if len(otherLines) > 0 {
		content := c.generateArrs(baseName, otherLines, m, 0)
		groups = append(groups, ArrsGroup{
			Content:  content,
			Name:     baseName + ".arrs",
			Routing:  0,
			Endpoint: "/rule.arrs",
		})
	}
	res.ArrsGroups = groups

	// 兼容旧接口：合并所有分组
	var allArrsLines []string
	allArrsLines = append(allArrsLines, directLines...)
	allArrsLines = append(allArrsLines, rejectLines...)
	allArrsLines = append(allArrsLines, otherLines...)
	res.Arrs = c.generateArrs(baseName, allArrsLines, m, 0)

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

	// 4. 自动推断 content-type 头部字段（当存在 JSON reject/mock 内容时）
	if c.Opts.AutoContentType && m.ContentType == "" {
		if ct := c.inferContentType(amrsLines); ct != "" {
			m.ContentType = ct
		}
	}

	res.Amrs = c.generateAmrs(baseName, m.Hostnames, amrsLines, m)

	return res, nil
}

// convertRoutingRules 转换路由规则，按 action 拆分为三组。
// 返回 directLines（DIRECT）、rejectLines（REJECT 类）、otherLines（其他）、amrsLines（URL-REGEX REJECT 转入 .amrs 的行）。
func (c *Converter) convertRoutingRules(rules []ir.RoutingRule, report *Report) (directLines, rejectLines, otherLines, amrsLines []string) {
	for _, r := range rules {
		switch r.Type {
		case "DOMAIN-SUFFIX", "DOMAIN":
			line := fmt.Sprintf("2, %s", r.Value)
			c.appendByAction(r.Action, line, &directLines, &rejectLines, &otherLines)
		case "DOMAIN-KEYWORD":
			line := fmt.Sprintf("3, %s", r.Value)
			c.appendByAction(r.Action, line, &directLines, &rejectLines, &otherLines)
		case "IP-CIDR":
			line := fmt.Sprintf("0, %s", r.Value)
			c.appendByAction(r.Action, line, &directLines, &rejectLines, &otherLines)
		case "IP-CIDR6":
			line := fmt.Sprintf("1, %s", r.Value)
			c.appendByAction(r.Action, line, &directLines, &rejectLines, &otherLines)
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

// appendByAction 根据 action 将 arrs 行追加到对应分组。
// DIRECT → directLines, REJECT 类 → rejectLines, 其他 → otherLines。
func (c *Converter) appendByAction(action string, line string, directLines, rejectLines, otherLines *[]string) {
	switch {
	case strings.EqualFold(action, "DIRECT"):
		*directLines = append(*directLines, line)
	case ir.IsRejectAction(action):
		*rejectLines = append(*rejectLines, line)
	default:
		*otherLines = append(*otherLines, line)
	}
}

// convertURLRegexReject 转换 URL-REGEX REJECT 类规则为 .amrs rewrite 行。
func (c *Converter) convertURLRegexReject(r ir.RoutingRule, report *Report) string {
	pattern := ConvertURLPatternWithHostnames(r.Value, c.Opts.GeneralizeHost, c.Hostnames)
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
	pattern := ConvertURLPatternWithHostnames(r.Pattern, c.Opts.GeneralizeHost, c.Hostnames)

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
	case "_request-header", "_request-body":
		b64 := EncodeInlineRewriteJS(r.RawJS, 0)
		return fmt.Sprintf("0, 100, %s, %s", pattern, b64), nil
	case "_response-body":
		b64 := EncodeInlineRewriteJS(r.RawJS, 1)
		return fmt.Sprintf("1, 100, %s, %s", pattern, b64), nil

	// response-body 兼容 Loon 内联 JS 与 QX body-replace（双 url response-body 标记）两种语法：
	//   - Loon: 内联 JS，r.RawJS 非空
	//   - QX:   search/replacement 都在 args
	case "response-body":
		if r.RawJS != "" {
			b64 := EncodeInlineRewriteJS(r.RawJS, 1)
			return fmt.Sprintf("1, 100, %s, %s", pattern, b64), nil
		}
		search := r.Args["search"]
		replacement := r.Args["replacement"]
		if search == "" {
			return "", fmt.Errorf("response-body 缺少 search")
		}
		// QX body-replace 形式：phase=1, op=4
		return fmt.Sprintf("1, 4, %s, %s, %s", pattern, QuoteField(search), QuoteField(replacement)), nil

	// header-del：删除请求头（Loon header-del / Surge _header-del 已归一化）
	case "header-del":
		headerName := r.Args["header"]
		if headerName == "" {
			return "", fmt.Errorf("header-del 缺少 header 名")
		}
		return fmt.Sprintf("0, 2, %s, %s", pattern, QuoteField(headerName)), nil

	// response-body-replace-regex：正则替换响应体 → body-replace (op 4)
	case "response-body-replace-regex":
		search := r.Args["search"]
		replacement := r.Args["replacement"]
		if search == "" {
			return "", fmt.Errorf("response-body-replace-regex 缺少 search")
		}
		return fmt.Sprintf("1, 4, %s, %s, %s", pattern, QuoteField(search), QuoteField(replacement)), nil

	// QX echo-response：content-type + body 模拟响应
	case "echo-response":
		body := r.Args["body"]
		if body == "" {
			return "", fmt.Errorf("echo-response 缺少 body")
		}
		// phase=1, op=2 (reject 带 body)。Anywhere 用此 op 配合 content-type 头部模拟响应
		return fmt.Sprintf("1, 0, %s, 2, %s", pattern, QuoteField(body)), nil

	// QX jsonjq-response-body：phase=1, op=5 (json-manipulate), pattern, jq
	case "jsonjq-response-body":
		jq := r.Args["jq"]
		if jq == "" {
			return "", fmt.Errorf("jsonjq-response-body 缺少 jq 表达式")
		}
		return fmt.Sprintf("1, 5, %s, %s", pattern, QuoteField(jq)), nil

	// QX script-analyze-echo-response：脚本分析后模拟响应（已转为 ScriptRule，理论上不会进入此处）
	case "script-analyze-echo-response":
		report.AddDegraded(fmt.Sprintf("script-analyze-echo-response 转为脚本处理: %s", r.Raw))
		return "", nil

	default:
		report.AddSkipped(fmt.Sprintf("未知重写动作 %s: %s", r.Action, r.Raw))
		return "", nil
	}
}

// convertHeaderRules 转换 Surge [Header Rewrite] 规则。
func (c *Converter) convertHeaderRules(rules []ir.HeaderRule, report *Report) []string {
	var lines []string
	for _, r := range rules {
		pattern := ConvertURLPatternWithHostnames(r.Pattern, c.Opts.GeneralizeHost, c.Hostnames)
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
		pattern := ConvertURLPatternWithHostnames(r.Pattern, c.Opts.GeneralizeHost, c.Hostnames)
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
// 脚本下载采用并发控制：每个脚本独立超时（ScriptTimeoutSec），最大并发数 Concurrency。
// 失败的脚本降级为占位符并记录 ScriptErr，整体流程不被阻塞。
func (c *Converter) convertScriptRules(ctx context.Context, m *ir.Module, report *Report) []string {
	scripts := m.Scripts
	lines := make([]string, 0, len(scripts))

	// 预计算每条脚本的行模板
	type result struct {
		index int
		line  string
	}
	results := make([]result, len(scripts))

	// 信号量控制并发
	concurrency := c.Opts.Concurrency
	if concurrency <= 0 {
		concurrency = 8
	}
	timeoutSec := c.Opts.ScriptTimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 10
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, s := range scripts {
		i, s := i, s
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			pattern := ConvertURLPatternWithHostnames(s.Pattern, c.Opts.GeneralizeHost, c.Hostnames)
			results[i].index = i

			if s.ScriptPath == "" {
				report.AddSkipped(fmt.Sprintf("脚本无 script-path: %s", s.Raw))
				return
			}

			// 单个脚本下载独立超时
			sctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
			defer cancel()

			b64, err := FetchAndEncodeScript(sctx, c.Fetcher, s.ScriptPath, c.BaseURL, c.Opts.FetchScripts, s.Phase, c.Opts.UseStreamScript)
			if err != nil {
				report.AddScriptErr(fmt.Sprintf("脚本下载失败 %s: %v", s.ScriptPath, err))
				// 降级为占位符，保证输出文件完整
				placeholder := fmt.Sprintf(`function process(ctx){Anywhere.log.warning("script not fetched: %s");}`, s.ScriptPath)
				b64 = base64.StdEncoding.EncodeToString([]byte(placeholder))
			}
			// op 100 = script，op 101 = stream-script（流式响应处理）
			op := "100"
			if c.Opts.UseStreamScript {
				op = "101"
			}
			results[i].line = fmt.Sprintf("%d, %s, %s, %s", s.Phase, op, pattern, b64)
		}()
	}
	wg.Wait()

	// 按原顺序输出
	for i := range scripts {
		if results[i].line != "" {
			lines = append(lines, results[i].line)
		}
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
// routing: 0=不添加 routing 字段, 1=DIRECT, 2=REJECT。
func (c *Converter) generateArrs(name string, lines []string, m *ir.Module, routing int) string {
	if len(lines) == 0 {
		return ""
	}
	var buf strings.Builder
	if c.Opts.IncludeMetadata {
		buf.WriteString(c.metadataComments(m))
	}
	buf.WriteString(fmt.Sprintf("name = %s\n", name))
	if routing > 0 {
		buf.WriteString(fmt.Sprintf("routing = %d\n", routing))
	}
	buf.WriteString("\n")
	for _, l := range lines {
		buf.WriteString(l + "\n")
	}
	return buf.String()
}

// generateAmrs 生成 .amrs 文件内容。
func (c *Converter) generateAmrs(name string, hostnames []string, lines []string, m *ir.Module) string {
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
	// content-type 头部字段（可选）：设置 reject/mock 响应的默认 Content-Type
	if m.ContentType != "" {
		buf.WriteString(fmt.Sprintf("content-type = %s\n", m.ContentType))
	}
	buf.WriteString("\n")
	for _, l := range lines {
		buf.WriteString(l + "\n")
	}
	return buf.String()
}

// inferContentType 根据规则行推断合适的 content-type 头部字段。
// 当存在 reject-dict (返回 {}) 或 mock-response-body (返回 JSON) 时，自动设置为 application/json; charset=utf-8。
func (c *Converter) inferContentType(lines []string) string {
	hasJSON := false
	for _, line := range lines {
		// rewrite reject sub-mode 2 带 {} 或 [] 内容
		// 形如：0, 0, pattern, 2, {}  或  0, 0, pattern, 2, {...JSON...}
		if strings.HasPrefix(line, "0, 0, ") {
			// 提取 sub-mode 与 content
			rest := strings.TrimPrefix(line, "0, 0, ")
			// 跳过 pattern，找到 ", 2, " 或 ", 3"
			if idx := strings.Index(rest, ", 2, "); idx >= 0 {
				content := strings.TrimSpace(rest[idx+len(", 2, "):])
				if strings.HasPrefix(content, "{") || strings.HasPrefix(content, `"{"`) || strings.Contains(content, `"code"`) {
					hasJSON = true
					break
				}
			}
		}
	}
	if hasJSON {
		return "application/json; charset=utf-8"
	}
	return ""
}

// metadataComments 生成元数据注释头。
// 注释规则：
//   - 首行：`# 由 module2anywhere 从 <source> 模块转换`
//   - 远程模块：追加 `# source: <原始 URL>`
//   - 量子态 add-resource 链接：追加 `# source: <展开前 URL>`（与上一行相同，标识走了一键订阅协议）
//   - Web 服务：追加 `# this: <本服务 URL>`
//   - 模块自身元数据（desc/author/homepage/date）依次追加
func (c *Converter) metadataComments(m *ir.Module) string {
	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("# 由 module2anywhere 从 %s 模块转换\n", m.Source))
	if c.SourceURL != "" {
		buf.WriteString(fmt.Sprintf("# source: %s\n", c.SourceURL))
	}
	if c.ServiceURL != "" {
		buf.WriteString(fmt.Sprintf("# this: %s\n", c.ServiceURL))
	}
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
