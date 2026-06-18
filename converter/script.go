// converter 包：脚本 API 改写与 base64 编码。
//
// Loon/Surge 脚本使用 $request / $response / $done / $httpClient / $persistentStore / $notification 等 API，
// Anywhere 使用 ctx / Anywhere.done() / Anywhere.http / Anywhere.store / Anywhere.log 等。
// 本子模块负责：
//  1. 下载远程脚本（由 fetcher 完成）
//  2. 改写 API 调用
//  3. 包装为 function process(ctx) {...}
//  4. base64 编码（UTF-8）
package converter

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"

	"github.com/Loon2Anywhere/loon2anywhere/fetcher"
	"github.com/Loon2Anywhere/loon2anywhere/ir"
)

// FetchAndEncodeScript 下载脚本，改写 API，base64 编码。
// scriptPath 可以是 URL 或本地路径。baseURL 用于解析相对路径。
// 若 fetchScripts=false，返回占位符 base64。
func FetchAndEncodeScript(ctx context.Context, f *fetcher.Fetcher, scriptPath, baseURL string, fetchScripts bool, phase int) (string, error) {
	if !fetchScripts {
		// 占位符：返回一个空 process 函数
		placeholder := `function process(ctx){Anywhere.log.warning("script not fetched: ` + scriptPath + `");}`
		return base64.StdEncoding.EncodeToString([]byte(placeholder)), nil
	}

	resolved := scriptPath
	if f != nil {
		resolved = f.ResolveScriptPath(scriptPath, baseURL)
	}
	if f == nil {
		return "", fmt.Errorf("fetcher 未初始化")
	}
	src, err := f.FetchScript(ctx, resolved)
	if err != nil {
		return "", fmt.Errorf("下载脚本失败 %q: %w", resolved, err)
	}
	rewritten := RewriteScriptAPI(src, phase)
	return base64.StdEncoding.EncodeToString([]byte(rewritten)), nil
}

// EncodeInlineScript 改写内联 JS 并 base64 编码。
// 用于 Loon request-header/response-body 与 Surge _request-header/_response-body 等。
func EncodeInlineScript(rawJS string, phase int) string {
	rewritten := RewriteScriptAPI(rawJS, phase)
	return base64.StdEncoding.EncodeToString([]byte(rewritten))
}

// RewriteScriptAPI 将 Loon/Surge 脚本 API 改写为 Anywhere API。
//
// 改写规则（按 README 7.3 节）：
//   - $request.url      → ctx.url
//   - $request.method   → ctx.method
//   - $request.headers  → ctx.headers
//   - $request.body     → ctx.body（phase=0）
//   - $response.status  → ctx.status
//   - $response.headers → ctx.headers
//   - $response.body    → ctx.body（phase=1）
//   - $done({})         → Anywhere.done()
//   - $done({body:x})   → ctx.body=x; Anywhere.done()
//   - $done({response:{...}}) → Anywhere.respond({...})
//   - $persistentStore.read(k)   → Anywhere.store.getString(k, true)
//   - $persistentStore.write(v,k) → Anywhere.store.set(k, v, true)
//   - $notification.post(...)    → Anywhere.log.info(...)
//   - $httpClient.get/post(...)  → await Anywhere.http.get/post(...)
//
// 注意：复杂脚本可能需要人工审核。本函数做尽力改写。
func RewriteScriptAPI(src string, phase int) string {
	// 1. 简单符号替换
	out := src

	// $request / $response → ctx（保留 .url/.method/.status/.headers）
	out = strings.ReplaceAll(out, "$request.url", "ctx.url")
	out = strings.ReplaceAll(out, "$request.method", "ctx.method")
	out = strings.ReplaceAll(out, "$request.headers", "ctx.headers")
	out = strings.ReplaceAll(out, "$response.status", "ctx.status")
	out = strings.ReplaceAll(out, "$response.headers", "ctx.headers")

	// body 需 codec 转换：$response.body → Anywhere.codec.utf8.decode(ctx.body)
	// 但若脚本只是读取后整体替换，简单替换为 ctx.body 即可（用户后续手动调整）
	// 这里采用保守策略：替换为 ctx.body，并在包装时添加 codec 提示注释
	out = strings.ReplaceAll(out, "$request.body", "ctx.body")
	out = strings.ReplaceAll(out, "$response.body", "ctx.body")

	// 2. $done 改写
	out = rewriteDoneCalls(out)

	// 3. $persistentStore
	out = regexp.MustCompile(`\$persistentStore\.read\(\s*([^)]+?)\s*\)`).
		ReplaceAllString(out, "Anywhere.store.getString($1, true)")
	out = regexp.MustCompile(`\$persistentStore\.write\(\s*([^,]+?)\s*,\s*([^)]+?)\s*\)`).
		ReplaceAllString(out, "Anywhere.store.set($2, $1, true)")

	// 4. $notification.post(title, sub, body) → Anywhere.log.info(title + " " + sub + " " + body)
	out = regexp.MustCompile(`\$notification\.post\(\s*([^,]+?)\s*,\s*([^,]*?)\s*,\s*([^)]+?)\s*\)`).
		ReplaceAllString(out, "Anywhere.log.info($1 + \" \" + $2 + \" \" + $3)")

	// 5. $httpClient.get(url, cb) → await Anywhere.http.get(url)
	//    Surge 回调式：(error, response, data) => {...}
	//    Anywhere Promise 式：const r = await Anywhere.http.get(url); const data = Anywhere.codec.utf8.decode(r.body);
	//    复杂回调无法自动转换，仅做简单替换并提示
	out = regexp.MustCompile(`\$httpClient\.get\(`).
		ReplaceAllString(out, "Anywhere.http.get(")
	out = regexp.MustCompile(`\$httpClient\.post\(`).
		ReplaceAllString(out, "Anywhere.http.post(")

	// 6. JSON.parse($response.body) → JSON.parse(Anywhere.codec.utf8.decode(ctx.body))
	out = strings.ReplaceAll(out, "JSON.parse(ctx.body)", "JSON.parse(Anywhere.codec.utf8.decode(ctx.body))")
	out = strings.ReplaceAll(out, "JSON.parse($response.body)", "JSON.parse(Anywhere.codec.utf8.decode(ctx.body))")

	// 7. 包装为 function process(ctx) {...}
	out = wrapAsProcess(out, phase)

	return out
}

// rewriteDoneCalls 改写 $done 调用。
func rewriteDoneCalls(src string) string {
	// $done({}) → Anywhere.done()
	out := regexp.MustCompile(`\$done\(\s*\{\s*\}\s*\)`).ReplaceAllString(src, "Anywhere.done()")

	// $done() → Anywhere.done()
	out = regexp.MustCompile(`\$done\(\s*\)`).ReplaceAllString(out, "Anywhere.done()")

	// $done({body: x}) → ctx.body = x; Anywhere.done()
	out = regexp.MustCompile(`\$done\(\s*\{\s*body\s*:\s*([^}]+?)\s*\}\s*\)`).
		ReplaceAllString(out, "ctx.body = $1; Anywhere.done()")

	// $done({response: {...}}) → Anywhere.respond({...})
	out = regexp.MustCompile(`\$done\(\s*\{\s*response\s*:\s*(\{[^}]*\})\s*\}\s*\)`).
		ReplaceAllString(out, "Anywhere.respond($1)")

	// $done({...}) 其他形式：保守处理，转为 Anywhere.done()
	out = regexp.MustCompile(`\$done\(\s*\{[^}]*\}\s*\)`).ReplaceAllString(out, "Anywhere.done()")

	return out
}

// wrapAsProcess 将脚本包装为 function process(ctx) {...}。
// 若源码已定义 process(ctx) 则不重复包装。
func wrapAsProcess(src string, phase int) string {
	trimmed := strings.TrimSpace(src)
	// 已有 process 函数定义
	if regexp.MustCompile(`(?m)^function\s+process\s*\(\s*ctx\s*\)`).MatchString(trimmed) ||
		regexp.MustCompile(`(?m)^async\s+function\s+process\s*\(\s*ctx\s*\)`).MatchString(trimmed) {
		return trimmed
	}

	// 若源码定义了 function run()，则包装为 process 并调用 run
	if regexp.MustCompile(`(?m)^function\s+run\s*\(\s*\)`).MatchString(trimmed) {
		phaseCheck := "request"
		if phase == 1 {
			phaseCheck = "response"
		}
		return fmt.Sprintf(`function process(ctx) {
  if (ctx.phase !== "%s") return;
  try {
    run();
  } catch (e) {
    Anywhere.log.warning("script error: " + e);
  }
}

%s
`, phaseCheck, trimmed)
	}

	// 否则整体包装
	phaseCheck := "request"
	if phase == 1 {
		phaseCheck = "response"
	}
	return fmt.Sprintf(`function process(ctx) {
  if (ctx.phase !== "%s") return;
  try {
%s
  } catch (e) {
    Anywhere.log.warning("script error: " + e);
  }
  Anywhere.done();
}
`, phaseCheck, indent(trimmed, "    "))
}

// indent 给每行加缩进。
func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
}

// BuildRedirectScript 构造 302/307 带捕获组的重定向脚本。
// pattern 是已转换的 URL pattern，captureURL 是含 $1 的目标 URL，status 为 302 或 307。
// Anywhere 仅支持 302，307 降级为 302。
func BuildRedirectScript(pattern, captureURL string, status int) string {
	// 将 captureURL 中的 $1 转为 JS 模板拼接
	// 简化：用正则 match 提取捕获组
	js := fmt.Sprintf(`function process(ctx) {
  if (ctx.phase !== "request" || !ctx.url) return;
  var m = ctx.url.match(%s);
  if (m) {
    var url = %s;
    Anywhere.respond({ status: 302, headers: [["Location", url]] });
  }
}
`, jsRegexLiteral(pattern), jsCaptureReplace(captureURL))
	return base64.StdEncoding.EncodeToString([]byte(js))
}

// jsRegexLiteral 将 pattern 转为 JS 正则字面量 /pattern/。
// 简化处理：直接包裹。若 pattern 含 /，需转义。
func jsRegexLiteral(pattern string) string {
	escaped := strings.ReplaceAll(pattern, "/", "\\/")
	return "/" + escaped + "/"
}

// jsCaptureReplace 将含 $1/$2 的 URL 转为 JS 表达式（字符串拼接）。
// 例：https://new.url/$1 → "https://new.url/" + m[1]
func jsCaptureReplace(url string) string {
	// 简单实现：按 $n 分割
	var buf strings.Builder
	buf.WriteByte('"')
	i := 0
	for i < len(url) {
		if url[i] == '$' && i+1 < len(url) && url[i+1] >= '1' && url[i+1] <= '9' {
			// 结束当前字符串，拼接 m[n]
			buf.WriteString("\" + m[")
			buf.WriteByte(url[i+1])
			buf.WriteString("] + \"")
			i += 2
		} else {
			// 转义 " 和 \
			if url[i] == '"' || url[i] == '\\' {
				buf.WriteByte('\\')
			}
			buf.WriteByte(url[i])
			i++
		}
	}
	buf.WriteByte('"')
	return buf.String()
}

// EncodeInlineRewriteJS 为 Loon request-header/response-body 等内联 JS 构造 Anywhere 脚本。
// phase: 0=request, 1=response。
func EncodeInlineRewriteJS(rawJS string, phase int) string {
	// 内联 JS 通常是 { ... } 形式，去掉外层花括号
	js := strings.TrimSpace(rawJS)
	if strings.HasPrefix(js, "{") && strings.HasSuffix(js, "}") {
		js = strings.TrimSpace(js[1 : len(js)-1])
	}
	return EncodeInlineScript(js, phase)
}

// ScriptPhaseName 返回阶段的可读名称。
func ScriptPhaseName(phase int) string {
	if phase == 1 {
		return "response"
	}
	return "request"
}

// IsInlineJSAction 判断是否为内联 JS 重写动作。
func IsInlineJSAction(action string) bool {
	switch action {
	case "request-header", "request-body", "response-body",
		"_request-header", "_request-body", "_response-body":
		return true
	}
	return false
}

// InlineJSPhase 返回内联 JS 动作对应的阶段。
// request-header/_request-header/_request-body → 0
// response-body/_response-body → 1
func InlineJSPhase(action string) int {
	switch action {
	case "response-body", "_response-body":
		return 1
	default:
		return 0
	}
}

// ensureIRImported 防止 ir 包被裁剪（占位）。
var _ = ir.SourceLoon
