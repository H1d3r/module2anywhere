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
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/H1d3r/module2anywhere/fetcher"
	"github.com/H1d3r/module2anywhere/ir"
)

var scriptCache sync.Map

// jsStringLiteral 将任意字符串转为安全的 JS 字符串字面量（含双引号）。
// 用于将外部输入（如 script-path）安全嵌入生成的 JS 代码，防止引号/反斜杠注入。
// Go 的 strconv.Quote 生成双引号转义，与 JS 字符串字面量语法兼容。
func jsStringLiteral(s string) string {
	return strconv.Quote(s)
}

// notFetchedPlaceholder 生成"脚本未下载"占位 process 函数的 base64 编码。
// 使用 jsStringLiteral 转义 scriptPath，防止恶意 script-path 注入 JS。
// 拼接形式：Anywhere.log.warning("script not fetched: " + "<escaped>");
func notFetchedPlaceholder(scriptPath string) string {
	placeholder := "function process(ctx){Anywhere.log.warning(\"script not fetched: \" + " +
		jsStringLiteral(scriptPath) + ");}"
	return base64.StdEncoding.EncodeToString([]byte(placeholder))
}

// EncodeStaticRespondScript 生成一个 request 阶段直接响应的轻量脚本，并返回 base64。
// 用于保留 Anywhere 原生 rewrite 无法表达的 status/header/body 组合。
func EncodeStaticRespondScript(status int, headers [][2]string, body string, bodyEncoding string) string {
	if status <= 0 {
		status = 200
	}
	headerJSON, err := json.Marshal(headers)
	if err != nil {
		headerJSON = []byte("[]")
	}
	bodyExpr := "Anywhere.codec.utf8.encode(" + jsStringLiteral(body) + ")"
	if strings.EqualFold(bodyEncoding, "base64") {
		bodyExpr = "Anywhere.codec.base64.decode(" + jsStringLiteral(body) + ")"
	}
	js := fmt.Sprintf("function process(ctx){\n  if (ctx.phase !== \"request\") return;\n  Anywhere.respond({status:%d,headers:%s,body:%s});\n}", status, string(headerJSON), bodyExpr)
	return base64.StdEncoding.EncodeToString([]byte(js))
}

// EncodeLoaderScript 生成轻量 loader，运行时从远端加载已转换后的 process(ctx) 脚本。
func EncodeLoaderScript(scriptURL string) string {
	js := fmt.Sprintf(`async function process(ctx) {
  var key = %s;
  globalThis.__m2aLoaderCache = globalThis.__m2aLoaderCache || {};
  var fn = globalThis.__m2aLoaderCache[key];
  if (!fn) {
    var res = await Anywhere.http.get(key);
    var source = Anywhere.codec.utf8.decode(res.body || new Uint8Array());
    fn = new Function(source + "\n; return process;")();
    globalThis.__m2aLoaderCache[key] = fn;
  }
  return await fn(ctx);
}
`, jsStringLiteral(scriptURL))
	return base64.StdEncoding.EncodeToString([]byte(js))
}

// FetchAndEncodeScript 下载脚本，改写 API，base64 编码。
// scriptPath 可以是 URL 或本地路径。baseURL 用于解析相对路径。
// 若 fetchScripts=false，返回占位符 base64。
// 若 useStreamScript=true，将改写后的脚本再包装为 stream-script (op 101) 形式。
func FetchAndEncodeScript(ctx context.Context, f *fetcher.Fetcher, scriptPath, baseURL string, fetchScripts bool, phase int, useStreamScript bool, wrapScript bool, argument string, maxScriptBytes int64) (string, error) {
	source, err := FetchAndRewriteScript(ctx, f, scriptPath, baseURL, fetchScripts, phase, useStreamScript, wrapScript, argument, maxScriptBytes)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString([]byte(source)), nil
}

// FetchAndRewriteScript 下载脚本并返回适配 Anywhere 的 JS 源码（未 base64）。
func FetchAndRewriteScript(ctx context.Context, f *fetcher.Fetcher, scriptPath, baseURL string, fetchScripts bool, phase int, useStreamScript bool, wrapScript bool, argument string, maxScriptBytes int64) (string, error) {
	cacheKey := resolvedScriptCacheKey(scriptPath, baseURL, phase, useStreamScript, wrapScript, fetchScripts, argument)
	if cached, ok := getScriptCache(cacheKey); ok {
		return cached, nil
	}

	if !fetchScripts {
		// 占位符：返回一个空 process 函数（scriptPath 已转义防注入）
		placeholder := "function process(ctx){Anywhere.log.warning(\"script not fetched: \" + " +
			jsStringLiteral(scriptPath) + ");}"
		setScriptCache(cacheKey, placeholder)
		return placeholder, nil
	}

	resolved := scriptPath
	if f != nil {
		resolved = f.ResolveScriptPath(scriptPath, baseURL)
	}
	if f == nil {
		return "", fmt.Errorf("fetcher 未初始化")
	}
	src, err := f.FetchScriptWithLimit(ctx, resolved, maxScriptBytes)
	if err != nil {
		return "", fmt.Errorf("下载脚本失败 %q: %w", resolved, err)
	}

	// 检测上游 script-path 误用 .conf/.plugin/.sgmodule 等非 JS 文件
	// 这些文件是 QuantumultX/Loon/Surge 模块配置，不是 JS 脚本，直接执行会导致语法错误
	if isLikelyNonJSScript(resolved, src) {
		fmt.Fprintf(os.Stderr, "[警告] script-path %q 可能不是 JS 文件（含模块配置特征），转换后可能语法错误\n", resolved)
	}

	// 包装执行模式：将上游脚本源码 base64 编码，生成包装器 process(ctx)
	if wrapScript {
		wrapped := BuildWrappedScript(src, phase, argument)
		setScriptCache(cacheKey, wrapped)
		return wrapped, nil
	}

	rewritten := RewriteScriptAPI(src, phase, argument)
	if useStreamScript {
		rewritten = WrapAsStreamScript(rewritten, phase)
	}
	setScriptCache(cacheKey, rewritten)
	return rewritten, nil
}

// isLikelyNonJSScript 检测下载的脚本内容是否实际上不是 JS 文件（而是模块配置文件）。
// 上游模块的 script-path 可能误指向 .conf/.plugin/.sgmodule 等配置文件，
// 直接执行这些文件会导致 JSCore 语法错误（如 "Unexpected token '*'"）。
// 检测特征：
//  1. 文件后缀为 .conf/.plugin/.sgmodule/.list
//  2. 非 .js 文件：内容含 [General]/[Rule]/[Rewrite]/[MITM]/[Script] 等模块段头
//  3. 非 .js 文件：内容含 hostname= 等 QuantumultX/Loon 配置特征
//
// 注意：.js 文件跳过内容检测，因为部分上游 .js 文件是混合格式（开头有 [rewrite_local] 配置 + JS 代码），
// 内容检测会误报。.js 文件只靠后缀判断。
func isLikelyNonJSScript(path, content string) bool {
	// 1. 文件后缀检测（总是生效）
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".conf") || strings.HasSuffix(lower, ".plugin") ||
		strings.HasSuffix(lower, ".sgmodule") || strings.HasSuffix(lower, ".list") {
		return true
	}
	// .js 文件跳过内容检测，避免混合格式文件误报
	if strings.HasSuffix(lower, ".js") {
		return false
	}
	// 2. 内容特征检测：模块段头（仅对无后缀或未知后缀文件）
	if strings.Contains(content, "[General]") || strings.Contains(content, "[Rule]") ||
		strings.Contains(content, "[Rewrite]") || strings.Contains(content, "[MITM]") ||
		strings.Contains(content, "[Script]") || strings.Contains(content, "[Host]") ||
		strings.Contains(content, "[URL Rewrite]") || strings.Contains(content, "[Header Rewrite]") {
		return true
	}
	// 3. QuantumultX 配置特征：hostname= 开头的行
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "hostname=") || strings.HasPrefix(trimmed, "hostname =") {
			return true
		}
	}
	return false
}

// resolvedScriptCacheKey 构造脚本缓存 key，避免同一脚本在同一转换中重复 fetch/转换。
func resolvedScriptCacheKey(scriptPath, baseURL string, phase int, useStreamScript, wrapScript, fetchScripts bool, argument string) string {
	return fmt.Sprintf("%s|%s|%d|%t|%t|%t|%s", scriptPath, baseURL, phase, useStreamScript, wrapScript, fetchScripts, argument)
}

// getScriptCache 读取脚本转换缓存。
func getScriptCache(key string) (string, bool) {
	if key == "" {
		return "", false
	}
	if v, ok := scriptCache.Load(key); ok {
		if s, ok := v.(string); ok {
			return s, true
		}
	}
	return "", false
}

// setScriptCache 写入脚本转换缓存。
func setScriptCache(key, value string) {
	if key == "" || value == "" {
		return
	}
	scriptCache.Store(key, value)
}

// EncodeInlineScript 改写内联 JS 并 base64 编码。
// 用于 Loon request-header/response-body 与 Surge _request-header/_response-body 等。
func EncodeInlineScript(rawJS string, phase int) string {
	rewritten := RewriteScriptAPI(rawJS, phase, "")
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
//   - $httpClient.get/post(...)  → await Anywhere.http.get/post(...)（自动 async 包装）
//
// 当检测到 $httpClient 调用时，自动将 process 函数声明为 async。
// 注意：复杂脚本可能需要人工审核。本函数做尽力改写。
func RewriteScriptAPI(src string, phase int, argument string) string {
	// 0. 检测是否需要 async（含 $httpClient / await / async / $done({response: 等）
	// 注意：需与 BuildWrappedScript 的检测条件保持一致
	needsAsync := strings.Contains(src, "$httpClient") || strings.Contains(src, "$.http") ||
		strings.Contains(src, "$env.http") || strings.Contains(src, "await ") ||
		strings.Contains(src, "async ") || strings.Contains(src, "$done({response:")

	// 1. 简单符号替换
	out := src

	// $request / $response → ctx（保留 .url/.method/.status/.headers）
	out = strings.ReplaceAll(out, "$request.url", "ctx.url")
	out = strings.ReplaceAll(out, "$request.method", "ctx.method")
	// 注意：ctx.headers 是 [[name, value], ...] 数组对格式（per MITM.md），
	// Loon/Surge 的 $request.headers/$response.headers 是 {name: value} 对象格式。
	// 替换为预转换变量 _headersObj（由 wrapAsProcess 在 process 函数体开头注入）
	out = strings.ReplaceAll(out, "$request.headers", "_headersObj")
	out = strings.ReplaceAll(out, "$response.headers", "_headersObj")
	// 注意：必须先替换更长的标识符（statusCode/bodyBytes），否则会被 $response.status/$response.body 部分匹配
	// $response.statusCode 是 $response.status 的别名 → ctx.status
	out = strings.ReplaceAll(out, "$response.statusCode", "ctx.status")
	out = strings.ReplaceAll(out, "$response.status", "ctx.status")
	// $response.bodyBytes / $request.bodyBytes 是 Loon 的二进制 body API，直接映射为 ctx.body（Uint8Array）
	out = strings.ReplaceAll(out, "$request.bodyBytes", "ctx.body")
	out = strings.ReplaceAll(out, "$response.bodyBytes", "ctx.body")

	// body 需 codec 转换：$response.body → Anywhere.codec.utf8.decode(ctx.body)
	// Anywhere 中 ctx.body 是 Uint8Array，不是字符串，不能直接调用 .replace() 等字符串方法。
	// 因此所有对 $request.body / $response.body 的读取都需要 decode 为字符串。
	// 注意：JSON.parse($response.body) 已在步骤 6 中单独处理，此处先做通用替换。
	out = strings.ReplaceAll(out, "$request.body", "Anywhere.codec.utf8.decode(ctx.body)")
	out = strings.ReplaceAll(out, "$response.body", "Anywhere.codec.utf8.decode(ctx.body)")

	// 2. $done 改写
	out = rewriteDoneCalls(out)

	// 3. $persistentStore
	out = rePersistentStoreRead.
		ReplaceAllString(out, "Anywhere.store.getString($1, true)")
	// $persistentStore.write(val, key) — 需要处理 null/undefined 的删除语义
	// 当 val 为 null 或 undefined 时，应调用 Anywhere.store.delete(key, true) 而非 set
	out = rePersistentStoreWrite.
		ReplaceAllStringFunc(out, func(match string) string {
			parts := rePersistentStoreWrite.FindStringSubmatch(match)
			if len(parts) < 3 {
				return match
			}
			val := parts[1]
			key := parts[2]
			// 如果 val 是 null 或 undefined 字面量，直接用 delete
			if val == "null" || val == "undefined" {
				return fmt.Sprintf("Anywhere.store.delete(%s, true)", key)
			}
			// 否则生成运行时判断代码
			return fmt.Sprintf("((%s === null || %s === undefined) ? Anywhere.store.delete(%s, true) : Anywhere.store.set(%s, String(%s), true))", val, val, key, key, val)
		})

	// 4. $notification.post(title, sub, body) → Anywhere.log.info(title + " " + sub + " " + body)
	out = reNotificationPost.
		ReplaceAllString(out, "Anywhere.log.info($1 + \" \" + $2 + \" \" + $3)")

	// 5. $httpClient 回调式 → async/await Promise 式
	//    Surge/Loon: $httpClient.get(url, (error, response, data) => {...})
	//    Anywhere:   const res = await Anywhere.http.get(url); const data = Anywhere.codec.utf8.decode(res.body);
	//    复杂回调无法自动转换，仅做简单替换并提示
	out = rewriteHttpClientCalls(out)

	// 6. JSON.parse($response.body) / JSON.parse(ctx.body) → 需 codec decode
	out = strings.ReplaceAll(out, "JSON.parse(ctx.body)", "JSON.parse(Anywhere.codec.utf8.decode(ctx.body))")

	// 7. 注入 BoxJS Env 兼容层（如果脚本使用了 Env 类或 $.xxx API）
	out = injectBoxJSPolyfill(out)

	// 8. 包装为 function process(ctx) {...}（按需 async）
	out = wrapAsProcess(out, phase, needsAsync, argument)

	return out
}

// rewriteHttpClientCalls 改写 $httpClient.get/post 等回调式调用为 await Anywhere.http.*.then()。
// Loon/Surge: $httpClient.get(url, function(err, resp, body) { ... })
// Anywhere:   await Anywhere.http.get(url).then(function(res) { var err=null; var resp={...}; var body=...; ... })
// 由于 Go 的 regexp 不支持 JS 的回调式替换（只能用 ReplaceAllStringFunc 接收整体 match），
// 这里用 ReplaceAllStringFunc + FindStringSubmatch 重新解析捕获组。
func rewriteHttpClientCalls(src string) string {
	out := src

	// buildCallbackVars 构造回调参数变量声明
	// params 形如 "err, resp, body" 或 "err,resp,body" 或 ""
	// 注意：Anywhere.http 返回的 res.headers 是 [[name, value], ...] 数组对格式，
	// Loon/Surge 的 $httpClient 回调中 resp.headers 是 {name: value} 对象格式，需要转换
	buildCallbackVars := func(params string) string {
		paramList := strings.Split(params, ",")
		varDecls := ""
		for i, p := range paramList {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			switch i {
			case 0:
				varDecls += "var " + p + " = null;"
			case 1:
				varDecls += "var " + p + " = {status: res.status, headers: (function(h){var o={};if(h&&h.forEach){h.forEach(function(p){o[String(p[0]||\"\")]=String(p[1]||\"\");});}return o;})(res.headers)};"
			case 2:
				varDecls += "var " + p + " = Anywhere.codec.utf8.decode(res.body || new Uint8Array());"
			}
		}
		return varDecls
	}

	// $httpClient.get/put/delete(url, function(err, resp, body) { ... })
	// → await Anywhere.http.get(url).then(function(res) { var err=null; var resp={...}; var body=...;
	out = reHttpCliGetFn.ReplaceAllStringFunc(out, func(match string) string {
		sub := reHttpCliGetFn.FindStringSubmatch(match)
		if len(sub) < 4 {
			return match
		}
		method, url, params := sub[1], sub[2], sub[3]
		return "await Anywhere.http." + method + "(" + url + ").then(function(res) {" + buildCallbackVars(params)
	})

	// 箭头函数形式: $httpClient.get/put/delete(url, (err, resp, body) => {
	out = reHttpCliGetArrow.ReplaceAllStringFunc(out, func(match string) string {
		sub := reHttpCliGetArrow.FindStringSubmatch(match)
		if len(sub) < 4 {
			return match
		}
		method, url, params := sub[1], sub[2], sub[3]
		return "await Anywhere.http." + method + "(" + url + ").then(function(res) {" + buildCallbackVars(params)
	})

	// 单参数箭头函数: $httpClient.get/put/delete(url, err => {
	out = reHttpCliGet1Arg.ReplaceAllStringFunc(out, func(match string) string {
		sub := reHttpCliGet1Arg.FindStringSubmatch(match)
		if len(sub) < 4 {
			return match
		}
		method, url, paramName := sub[1], sub[2], sub[3]
		return "await Anywhere.http." + method + "(" + url + ").then(function(res) {var " + paramName + " = null;"
	})

	// $httpClient.post(url, opts, function(err, resp, body) { ... })
	out = reHttpCliPostFn.ReplaceAllStringFunc(out, func(match string) string {
		sub := reHttpCliPostFn.FindStringSubmatch(match)
		if len(sub) < 5 {
			return match
		}
		url, opts, params := sub[1], sub[2], sub[3]
		return "await Anywhere.http.post(" + url + ", " + opts + ").then(function(res) {" + buildCallbackVars(params)
	})

	// $httpClient.post 箭头函数形式
	out = reHttpCliPostArr.ReplaceAllStringFunc(out, func(match string) string {
		sub := reHttpCliPostArr.FindStringSubmatch(match)
		if len(sub) < 5 {
			return match
		}
		url, opts, params := sub[1], sub[2], sub[3]
		return "await Anywhere.http.post(" + url + ", " + opts + ").then(function(res) {" + buildCallbackVars(params)
	})

	// $httpClient.post 单参数箭头函数
	out = reHttpCliPost1Arg.ReplaceAllStringFunc(out, func(match string) string {
		sub := reHttpCliPost1Arg.FindStringSubmatch(match)
		if len(sub) < 5 {
			return match
		}
		url, opts, paramName := sub[1], sub[2], sub[3]
		return "await Anywhere.http.post(" + url + ", " + opts + ").then(function(res) {var " + paramName + " = null;"
	})

	// $httpClient.request(opts, function(err, resp, body) { ... })
	out = reHttpCliReqFn.ReplaceAllStringFunc(out, func(match string) string {
		sub := reHttpCliReqFn.FindStringSubmatch(match)
		if len(sub) < 4 {
			return match
		}
		opts, params := sub[1], sub[2]
		return "await Anywhere.http.request(" + opts + ").then(function(res) {" + buildCallbackVars(params)
	})

	// $httpClient.request 箭头函数形式
	out = reHttpCliReqArr.ReplaceAllStringFunc(out, func(match string) string {
		sub := reHttpCliReqArr.FindStringSubmatch(match)
		if len(sub) < 4 {
			return match
		}
		opts, params := sub[1], sub[2]
		return "await Anywhere.http.request(" + opts + ").then(function(res) {" + buildCallbackVars(params)
	})

	return out
}

// boxjsEnvPattern 匹配脚本中使用了 BoxJS Env 类或 $.xxx API 或常见缺失 Web API 的特征。
var boxjsEnvPattern = regexp.MustCompile(`new\s+Env\s*\(|\$\.((?i)getdata|setdata|getjson|setjson|msg|log|logErr|http|fetch|request|notify|runScript|toURL|setvalue|getvalue|isQuanX|isSurge|isLoon|isNode|wait|done|name)|\$env\s*\.|URLSearchParams|new\s+URL\s*\(|\bfetch\s*\(|\bTextEncoder\b|\bTextDecoder\b|\bHeaders\b|\bRequest\b|\bResponse\b|\bsetTimeout\s*\(|\bsetInterval\s*\(|\bclearTimeout\s*\(|\bclearInterval\s*\(|console\.(log|warn|error|info|debug|assert|trace|table)`)

// boxjsOnlyPattern 只匹配 BoxJS Env 特征（不包含 Web API 模式），用于细粒度 polyfill 注入检测。
var boxjsOnlyPattern = regexp.MustCompile(`new\s+Env\s*\(|\$\.((?i)getdata|setdata|getjson|setjson|msg|log|logErr|http|fetch|request|notify|runScript|toURL|setvalue|getvalue|isQuanX|isSurge|isLoon|isNode|wait|done|name)|\$env\s*\.`)

// 以下包级正则在脚本改写 hot path 中频繁使用，提升为包级 var 避免每次调用重新编译。
// 对应 RewriteScriptAPI / rewriteDoneCalls / rewriteHttpClientCalls / wrapAsProcess 中的局部 MustCompile。
var (
	// RewriteScriptAPI: $persistentStore / $notification
	rePersistentStoreRead  = regexp.MustCompile(`\$persistentStore\.read\(\s*([^)]+?)\s*\)`)
	rePersistentStoreWrite = regexp.MustCompile(`\$persistentStore\.write\(\s*([^,]+?)\s*,\s*([^)]+?)\s*\)`)
	reNotificationPost     = regexp.MustCompile(`\$notification\.post\(\s*([^,]+?)\s*,\s*([^,]*?)\s*,\s*([^)]+?)\s*\)`)

	// rewriteDoneCalls: $done 各种形式
	reDoneEmpty     = regexp.MustCompile(`\$done\(\s*\{\s*\}\s*\)`)
	reDoneNoArg     = regexp.MustCompile(`\$done\(\s*\)`)
	reDoneBody      = regexp.MustCompile(`\$done\(\s*\{\s*body\s*:\s*([^}]+?)\s*\}\s*\)`)
	reDoneBodyShort = regexp.MustCompile(`\$done\(\s*\{\s*body\s*\}\s*\)`)
	reDoneRespStart = regexp.MustCompile(`\$done\(\s*\{\s*response\s*:\s*`)
	reDoneRespBody  = regexp.MustCompile(`__DONE_RESPONSE_START__(\{[\s\S]*?\}\s*\})\s*\)`)
	reDoneObjStart  = regexp.MustCompile(`\$done\(\s*\{`)
	reDoneObjBody   = regexp.MustCompile(`__DONE_OBJECT_START__\{[\s\S]*?\}\s*\)`)
	reDoneVar       = regexp.MustCompile(`\$done\(\s*([^){}\s][^)]*?)\s*\)`)

	// rewriteHttpClientCalls: $httpClient.get/put/delete/post/request 回调式与箭头函数
	reHttpCliGetFn    = regexp.MustCompile(`\$httpClient\.(get|put|delete)\(\s*([^,]+?)\s*,\s*function\s*\(([^)]*)\)\s*\{`)
	reHttpCliGetArrow = regexp.MustCompile(`\$httpClient\.(get|put|delete)\(\s*([^,]+?)\s*,\s*\(([^)]*)\)\s*=>\s*\{`)
	reHttpCliGet1Arg  = regexp.MustCompile(`\$httpClient\.(get|put|delete)\(\s*([^,]+?)\s*,\s*(\w+)\s*=>\s*\{`)
	reHttpCliPostFn   = regexp.MustCompile(`\$httpClient\.post\(\s*([^,]+?)\s*,\s*([^,]+?)\s*,\s*function\s*\(([^)]*)\)\s*\{`)
	reHttpCliPostArr  = regexp.MustCompile(`\$httpClient\.post\(\s*([^,]+?)\s*,\s*([^,]+?)\s*,\s*\(([^)]*)\)\s*=>\s*\{`)
	reHttpCliPost1Arg = regexp.MustCompile(`\$httpClient\.post\(\s*([^,]+?)\s*,\s*([^,]+?)\s*,\s*(\w+)\s*=>\s*\{`)
	reHttpCliReqFn    = regexp.MustCompile(`\$httpClient\.request\(\s*([^,]+?)\s*,\s*function\s*\(([^)]*)\)\s*\{`)
	reHttpCliReqArr   = regexp.MustCompile(`\$httpClient\.request\(\s*([^,]+?)\s*,\s*\(([^)]*)\)\s*=>\s*\{`)

	// wrapAsProcess: process / run 函数检测与注入
	rePolyfillBlock    = regexp.MustCompile(`(?s)// === BoxJS Env 兼容层.*?// === BoxJS Env 兼容层 \+ Web API Polyfill 结束 ===\n`)
	reProcessSyncDecl  = regexp.MustCompile(`(function\s+process\s*\(\s*ctx\s*\)\s*\{)`)
	reProcessAsyncDecl = regexp.MustCompile(`(async\s+function\s+process\s*\(\s*ctx\s*\)\s*\{)`)
	reHasProcessSync   = regexp.MustCompile(`(?m)^function\s+process\s*\(\s*ctx\s*\)`)
	reHasProcessAsync  = regexp.MustCompile(`(?m)^async\s+function\s+process\s*\(\s*ctx\s*\)`)
	reHasRunDecl       = regexp.MustCompile(`(?m)^function\s+run\s*\(\s*\)`)
)

// polyfillBase 总是注入的基础模块：开始标记、_BoxJS_Env_injected 标志、Array.isArray、console、atob/btoa。
const polyfillBase = `// === BoxJS Env 兼容层 + Web API Polyfill (由 module2anywhere 自动注入) ===
var _BoxJS_Env_injected = true;

// --- Web API Polyfill: Array.isArray ---
if (typeof Array.isArray === 'undefined') {
  Array.isArray = function(arg) { return Object.prototype.toString.call(arg) === '[object Array]'; };
}

// --- Web API Polyfill: console ---
if (typeof globalThis.console === 'undefined') {
  globalThis.console = {
    log: function() { Anywhere.log.info([].slice.call(arguments).map(String).join(' ')); },
    warn: function() { Anywhere.log.warning([].slice.call(arguments).map(String).join(' ')); },
    error: function() { Anywhere.log.error([].slice.call(arguments).map(String).join(' ')); },
    info: function() { Anywhere.log.info([].slice.call(arguments).map(String).join(' ')); },
    debug: function() { Anywhere.log.debug([].slice.call(arguments).map(String).join(' ')); }
  };
}

// --- Web API Polyfill: atob / btoa ---
if (typeof globalThis.atob === 'undefined') {
  globalThis.atob = function(str) { return Anywhere.codec.utf8.decode(Anywhere.codec.base64.decode(str)); };
  globalThis.btoa = function(str) { return Anywhere.codec.base64.encode(Anywhere.codec.utf8.encode(str)); };
}

`

// polyfillURL 在 needURL 时注入：URLSearchParams 和 URL polyfill。
const polyfillURL = `// --- Web API Polyfill: URLSearchParams ---
// 使用 globalThis 赋值而非 var 声明，确保 new Function() 内可访问
if (typeof globalThis.URLSearchParams === 'undefined') {
  globalThis.URLSearchParams = function(init) {
    this._params = [];
    if (typeof init === 'string') {
      var s = init.charAt(0) === '?' ? init.slice(1) : init;
      var pairs = s.split('&');
      for (var i = 0; i < pairs.length; i++) {
        var idx = pairs[i].indexOf('=');
        if (idx < 0) { this._params.push([decodeURIComponent(pairs[i]), '']); }
        else { this._params.push([decodeURIComponent(pairs[i].slice(0, idx)), decodeURIComponent(pairs[i].slice(idx + 1))]); }
      }
    } else if (init && typeof init === 'object' && !Array.isArray(init)) {
      for (var key in init) {
        if (init.hasOwnProperty(key)) this._params.push([key, String(init[key])]);
      }
    } else if (Array.isArray(init)) {
      for (var i = 0; i < init.length; i++) { this._params.push([String(init[i][0]), String(init[i][1])]); }
    }
  };
  globalThis.URLSearchParams.prototype.append = function(name, value) { this._params.push([name, value]); };
  globalThis.URLSearchParams.prototype.delete = function(name) { this._params = this._params.filter(function(p) { return p[0] !== name; }); };
  globalThis.URLSearchParams.prototype.get = function(name) { for (var i = 0; i < this._params.length; i++) { if (this._params[i][0] === name) return this._params[i][1]; } return null; };
  globalThis.URLSearchParams.prototype.getAll = function(name) { var r = []; for (var i = 0; i < this._params.length; i++) { if (this._params[i][0] === name) r.push(this._params[i][1]); } return r; };
  globalThis.URLSearchParams.prototype.has = function(name) { for (var i = 0; i < this._params.length; i++) { if (this._params[i][0] === name) return true; } return false; };
  globalThis.URLSearchParams.prototype.set = function(name, value) { var found = false; for (var i = 0; i < this._params.length; i++) { if (this._params[i][0] === name) { this._params[i][1] = value; found = true; break; } } if (!found) this._params.push([name, value]); };
  globalThis.URLSearchParams.prototype.toString = function() { return this._params.map(function(p) { return encodeURIComponent(p[0]) + '=' + encodeURIComponent(p[1]); }).join('&'); };
  globalThis.URLSearchParams.prototype.keys = function() { return this._params.map(function(p) { return p[0]; }); };
  globalThis.URLSearchParams.prototype.values = function() { return this._params.map(function(p) { return p[1]; }); };
  globalThis.URLSearchParams.prototype.entries = function() { return this._params.map(function(p) { return [p[0], p[1]]; }); };
  globalThis.URLSearchParams.prototype.forEach = function(cb, thisArg) { for (var i = 0; i < this._params.length; i++) { cb.call(thisArg, this._params[i][1], this._params[i][0], this); } };
}

// --- Web API Polyfill: URL ---
if (typeof globalThis.URL === 'undefined') {
  globalThis.URL = function(url, base) {
    var fullUrl = url;
    if (base) {
      if (url.indexOf('://') < 0) {
        var baseEnd = base.lastIndexOf('/');
        fullUrl = (baseEnd >= 0 ? base.slice(0, baseEnd + 1) : base + '/') + url;
      } else { fullUrl = url; }
    }
    var m = fullUrl.match(/^(https?):\/\/([^:/?#]+)(?::(\d+))?([^?#]*)?(\?[^#]*)?(#.*)?$/);
    if (!m) throw new Error('Invalid URL: ' + fullUrl);
    this.protocol = m[1] + ':';
    this.hostname = m[2];
    this.port = m[3] || '';
    this.host = this.hostname + (this.port ? ':' + this.port : '');
    this.pathname = m[4] || '/';
    this.search = m[5] || '';
    this.hash = m[6] || '';
    this.href = fullUrl;
    this.origin = this.protocol + '//' + this.host;
    this.searchParams = new globalThis.URLSearchParams(this.search);
    Object.defineProperty(this, 'username', { get: function() { return ''; } });
    Object.defineProperty(this, 'password', { get: function() { return ''; } });
  };
  globalThis.URL.prototype.toString = function() { return this.href; };
  globalThis.URL.prototype.toJSON = function() { return this.href; };
}

`

// polyfillHelpers 在 needFetch || needBoxJS 时注入：_boxBytes/_boxHeaderPairs/_boxRequest 辅助函数。
const polyfillHelpers = `function _boxBytes(value) {
  if (!value) return new Uint8Array();
  if (value instanceof Uint8Array) return value;
  if (value instanceof ArrayBuffer) return new Uint8Array(value);
  if (ArrayBuffer.isView(value)) return new Uint8Array(value.buffer, value.byteOffset, value.byteLength);
  if (typeof value === 'string') return Anywhere.codec.utf8.encode(value);
  return Anywhere.codec.utf8.encode(JSON.stringify(value));
}
function _boxHeaderPairs(headers) {
  if (!headers) return [];
  if (Array.isArray(headers)) return headers;
  if (headers && headers._items) return headers._items;
  var out = [];
  for (var name in headers) { if (headers.hasOwnProperty(name)) out.push([String(name), String(headers[name])]); }
  return out;
}
function _boxRequest(input, init) {
  var req = {};
  if (typeof input === 'string') req.url = input;
  else if (input && typeof input === 'object') { for (var k in input) if (input.hasOwnProperty(k)) req[k] = input[k]; }
  if (init && typeof init === 'object') { for (var k2 in init) if (init.hasOwnProperty(k2)) req[k2] = init[k2]; }
  req.method = String(req.method || (req.body ? 'POST' : 'GET')).toUpperCase();
  req.headers = _boxHeaderPairs(req.headers);
  if (req.body) req.body = _boxBytes(req.body);
  return req;
}

`

// polyfillFetch 在 needFetch 时注入：TextEncoder/TextDecoder/Headers/Request/Response/fetch polyfill。
const polyfillFetch = `if (typeof globalThis.TextEncoder === 'undefined') {
  globalThis.TextEncoder = function() {};
  globalThis.TextEncoder.prototype.encode = function(value) { return Anywhere.codec.utf8.encode(String(value)); };
}
if (typeof globalThis.TextDecoder === 'undefined') {
  globalThis.TextDecoder = function() {};
  globalThis.TextDecoder.prototype.decode = function(value) { return Anywhere.codec.utf8.decode(_boxBytes(value)); };
}
if (typeof globalThis.Headers === 'undefined') {
  globalThis.Headers = function(init) { this._items = _boxHeaderPairs(init); };
  globalThis.Headers.prototype.append = function(name, value) { this._items.push([String(name), String(value)]); };
  globalThis.Headers.prototype.get = function(name) { name = String(name).toLowerCase(); for (var i = 0; i < this._items.length; i++) if (String(this._items[i][0]).toLowerCase() === name) return this._items[i][1]; return null; };
  globalThis.Headers.prototype.has = function(name) { return this.get(name) !== null; };
  globalThis.Headers.prototype.set = function(name, value) { this.delete(name); this.append(name, value); };
  globalThis.Headers.prototype.delete = function(name) { name = String(name).toLowerCase(); this._items = this._items.filter(function(h) { return String(h[0]).toLowerCase() !== name; }); };
  globalThis.Headers.prototype.forEach = function(cb, thisArg) { for (var i = 0; i < this._items.length; i++) cb.call(thisArg, this._items[i][1], this._items[i][0], this); };
}
if (typeof globalThis.Request === 'undefined') {
  globalThis.Request = function(input, init) { var req = _boxRequest(input, init); this.url = req.url; this.method = req.method; this.headers = new globalThis.Headers(req.headers); this.body = req.body; };
}
if (typeof globalThis.Response === 'undefined') {
  globalThis.Response = function(body, init) { init = init || {}; this.body = _boxBytes(body); this.status = init.status || 200; this.headers = new globalThis.Headers(init.headers); };
  globalThis.Response.prototype.text = function() { var self = this; return Promise.resolve(Anywhere.codec.utf8.decode(self.body || new Uint8Array())); };
  globalThis.Response.prototype.json = function() { return this.text().then(function(t) { return JSON.parse(t); }); };
  globalThis.Response.prototype.arrayBuffer = function() { return Promise.resolve((this.body || new Uint8Array()).buffer); };
}
if (typeof globalThis.fetch === 'undefined') {
  globalThis.fetch = function(input, init) {
    var req = _boxRequest(input, init);
    var opts = { method: req.method, headers: req.headers, body: req.body };
    if (req.method === 'GET' || req.method === 'HEAD') delete opts.body;
    var p = req.method === 'GET' ? Anywhere.http.get(req.url, opts) : req.method === 'POST' ? Anywhere.http.post(req.url, opts) : req.method === 'PUT' ? Anywhere.http.put(req.url, opts) : req.method === 'DELETE' ? Anywhere.http.delete(req.url, opts) : Anywhere.http.request(opts);
    return p.then(function(res) { return new globalThis.Response(res.body || new Uint8Array(), { status: res.status || 200, headers: res.headers || [] }); });
  };
}

`

// polyfillTimer 在 needTimer 时注入：setTimeout/clearTimeout/setInterval/clearInterval + console.assert/trace/table。
// 所有 timer 句柄注册到 globalThis._requestTimersStack 栈顶（由 wrapAsProcess/BuildWrappedScript 在 process 开头压栈），
// process() 返回后 finally 块自动标记所有未清除的 timer 为 inactive 并出栈，防止 setInterval 递归 Promise 链无限延续。
// 栈式隔离确保多个规则并发执行时，各自定时器互不干扰。
const polyfillTimer = `if (typeof globalThis.setTimeout === 'undefined') globalThis.setTimeout = function(fn, ms) { var h = { active: true }; var _s = globalThis._requestTimersStack; if (_s && _s.length) _s[_s.length - 1].push(h); Anywhere.wait(ms || 0).then(function() { if (h.active) fn(); }); return h; };
if (typeof globalThis.clearTimeout === 'undefined') globalThis.clearTimeout = function(h) { if (h) h.active = false; };
if (typeof globalThis.setInterval === 'undefined') globalThis.setInterval = function(fn, ms) { var h = { active: true }; var _s = globalThis._requestTimersStack; if (_s && _s.length) _s[_s.length - 1].push(h); (function tick(){ if (!h.active) return; Anywhere.wait(ms || 0).then(function(){ if (!h.active) return; fn(); tick(); }); })(); return h; };
if (typeof globalThis.clearInterval === 'undefined') globalThis.clearInterval = function(h) { if (h) h.active = false; };
if (typeof globalThis.console.assert === 'undefined') globalThis.console.assert = function(cond) { if (!cond) globalThis.console.warn('Assertion failed'); };
if (typeof globalThis.console.trace === 'undefined') globalThis.console.trace = function() { globalThis.console.warn('trace'); };
if (typeof globalThis.console.table === 'undefined') globalThis.console.table = function(obj) { globalThis.console.info(JSON.stringify(obj)); };

`

// polyfillBoxJS 在 needBoxJS 时注入：Env 类 + _wrapBoxJSResponse/_wrapBoxJSRequest + $env 兼容。
const polyfillBoxJS = `// --- BoxJS Env 类 ---
function Env(name) {
  this.name = name || 'BoxJS';
}
// _wrapBoxJSResponse 将 Anywhere.http 的响应转换为 BoxJS 脚本期望的格式
// Anywhere: {status, headers: [[name, value], ...], body: Uint8Array}
// BoxJS:    {status, headers: {name: value}, body: string, bodyBytes: Uint8Array}
function _wrapBoxJSResponse(res) {
  var headers = {};
  if (res.headers && res.headers.forEach) {
    res.headers.forEach(function(h) { headers[String(h[0] || "")] = String(h[1] || ""); });
  }
  return {
    status: res.status || 200,
    headers: headers,
    body: Anywhere.codec.utf8.decode(res.body || new Uint8Array()),
    bodyBytes: res.body,
    url: res.url
  };
}
function _wrapBoxJSRequest(opts) {
  if (typeof opts === 'string') return opts;
  if (!opts || typeof opts !== 'object') return opts;
  var out = {};
  for (var k in opts) { if (opts.hasOwnProperty(k)) out[k] = opts[k]; }
  if (out.headers && !Array.isArray(out.headers)) {
    var hs = [];
    for (var name in out.headers) { if (out.headers.hasOwnProperty(name)) hs.push([String(name), String(out.headers[name])]); }
    out.headers = hs;
  }
  if (typeof out.body === 'string') out.body = Anywhere.codec.utf8.encode(out.body);
  else if (out.body && typeof out.body === 'object' && !(out.body instanceof Uint8Array) && !(out.body instanceof ArrayBuffer) && !ArrayBuffer.isView(out.body)) out.body = Anywhere.codec.utf8.encode(JSON.stringify(out.body));
  return out;
}
Env.prototype.getdata = function(key) {
  return Anywhere.store.getString(key, true) || null;
};
Env.prototype.setdata = function(val, key) {
  try {
    if (val === null || val === undefined) { Anywhere.store.delete(key, true); return true; }
    Anywhere.store.set(key, String(val), true); return true;
  } catch(e) { return false; }
};
Env.prototype.getjson = function(key, defaultVal) {
  var val = this.getdata(key);
  if (val === null || val === undefined) return defaultVal || null;
  try { return JSON.parse(val); } catch(e) { return defaultVal || null; }
};
Env.prototype.setjson = function(val, key) {
  try { this.setdata(JSON.stringify(val), key); return true; } catch(e) { return false; }
};
Env.prototype.msg = function(title, subtitle, body) {
  Anywhere.log.info(title + (subtitle ? " " + subtitle : "") + (body ? " " + body : ""));
};
Env.prototype.log = function(msg) {
  Anywhere.log.info(String(msg));
};
Env.prototype.logErr = function(msg) {
  Anywhere.log.warning(String(msg));
};
Env.prototype.notify = function(title, subtitle, body) {
  Anywhere.log.info((title || '') + (subtitle ? ' ' + subtitle : '') + (body ? ' ' + body : ''));
};
Env.prototype.fetch = function(input, init) {
  return Anywhere.http.request(_wrapBoxJSRequest(typeof input === 'string' ? { url: input } : input, init)).then(function(res) { return _wrapBoxJSResponse(res); });
};
Env.prototype.request = function(input, init) {
  return this.fetch(input, init);
};
Env.prototype.runScript = function(script) {
  return Promise.resolve().then(function() { return eval(script); });
};
Env.prototype.toURL = function(value, base) {
  return new URL(String(value || ''), base);
};
Env.prototype.getvalue = function(key) { return this.getdata(key); };
Env.prototype.setvalue = function(val, key) { return this.setdata(val, key); };
Env.prototype.http = {
  // 包装响应：Anywhere.http 返回 headers: [[name, value], ...] + body: Uint8Array
  // BoxJS 脚本期望 headers: {name: value} + body: string
  get: function(opts) {
    var req = _wrapBoxJSRequest(opts);
    var url = typeof req === 'string' ? req : req.url;
    return Anywhere.http.get(url, req).then(function(res) { return _wrapBoxJSResponse(res); });
  },
  post: function(opts) {
    var req = _wrapBoxJSRequest(opts);
    var url = typeof req === 'string' ? req : req.url;
    return Anywhere.http.post(url, req).then(function(res) { return _wrapBoxJSResponse(res); });
  },
  put: function(opts) {
    var req = _wrapBoxJSRequest(opts);
    var url = typeof req === 'string' ? req : req.url;
    return Anywhere.http.put(url, req).then(function(res) { return _wrapBoxJSResponse(res); });
  },
  delete: function(opts) {
    var req = _wrapBoxJSRequest(opts);
    var url = typeof req === 'string' ? req : req.url;
    return Anywhere.http.delete(url, req).then(function(res) { return _wrapBoxJSResponse(res); });
  },
  request: function(opts) {
    return Anywhere.http.request(_wrapBoxJSRequest(opts)).then(function(res) { return _wrapBoxJSResponse(res); });
  }
};
Env.prototype.isQuanX = function() { return false; };
Env.prototype.isSurge = function() { return false; };
Env.prototype.isLoon = function() { return false; };
Env.prototype.isNode = function() { return false; };
Env.prototype.isShadowrocket = function() { return false; };
Env.prototype.isStash = function() { return false; };
Env.prototype.wait = function(ms) {
  return new Promise(function(resolve) { setTimeout(resolve, ms || 0); });
};
Env.prototype.done = function() { Anywhere.done(); };
// $env 兼容（Quantumult X 的 $env 对象）
var $env = globalThis.$env || { isBoxJS: false, isAnywhere: true };

`

// polyfillLocalVarsBase 总是注入：console/atob/btoa 的局部变量映射（必须在所有 polyfill 安装之后）。
const polyfillLocalVarsBase = `// 局部变量映射：将 globalThis 上的 polyfill 映射为局部标识符
// 注意：必须在所有 globalThis.XXX 赋值之后执行，否则读到的是 undefined
// （JSCore 中 var 声明会提升，但赋值在原位置执行）
var console = globalThis.console;
var atob = globalThis.atob;
var btoa = globalThis.btoa;
`

// polyfillLocalVarsURL 在 needURL 时注入：URLSearchParams/URL 的局部变量映射。
const polyfillLocalVarsURL = `var URLSearchParams = globalThis.URLSearchParams;
var URL = globalThis.URL;
`

// polyfillFooter 总是注入：结束标记（wrapAsProcess 用正则匹配此标记提取 polyfill 代码块）。
const polyfillFooter = `
// === BoxJS Env 兼容层 + Web API Polyfill 结束 ===
`

// injectBoxJSPolyfill 为使用 BoxJS Env 类（$.getdata/$.setdata/$.msg 等）的脚本注入兼容层。
// BoxJS 脚本通常使用 `const $ = new Env('name')` 创建 Env 实例，
// 然后通过 $.getdata/$.setdata/$.msg/$.http/$.log 等方法与 BoxJS 交互。
// Anywhere 没有内置 Env 类，因此需要在脚本头部注入一个轻量 polyfill，
// 将这些调用映射到 Anywhere 的 Anywhere.store/Anywhere.log/Anywhere.http 等 API。
// 同时注入常用 Web API polyfill（URLSearchParams/URL/console/atob/btoa 等），
// 因为 Anywhere 的 JavaScriptCore 运行时不提供这些浏览器 API。
func injectBoxJSPolyfill(src string) string {
	if !boxjsEnvPattern.MatchString(src) {
		return src
	}

	// 细粒度检测：根据脚本使用的 API 特征决定注入哪些 polyfill 模块，减少不必要的体积
	needURL := strings.Contains(src, "URLSearchParams") || strings.Contains(src, "new URL(") || strings.Contains(src, ".searchParams")
	needFetch := strings.Contains(src, "fetch(") || strings.Contains(src, "TextEncoder") || strings.Contains(src, "TextDecoder") || strings.Contains(src, "Headers") || strings.Contains(src, "Request") || strings.Contains(src, "Response")
	needTimer := strings.Contains(src, "setTimeout") || strings.Contains(src, "setInterval") || strings.Contains(src, "clearTimeout") || strings.Contains(src, "clearInterval")
	needBoxJS := boxjsOnlyPattern.MatchString(src)

	// 按需拼接 polyfill 模块（顺序确保依赖关系正确）
	var b strings.Builder
	b.WriteString(polyfillBase)
	if needURL {
		b.WriteString(polyfillURL)
	}
	if needFetch || needBoxJS {
		b.WriteString(polyfillHelpers)
	}
	if needFetch {
		b.WriteString(polyfillFetch)
	}
	if needTimer {
		b.WriteString(polyfillTimer)
	}
	if needBoxJS {
		b.WriteString(polyfillBoxJS)
	}
	// 局部变量映射：必须在所有 polyfill 安装之后（JSCore var 声明提升但赋值不提升）
	b.WriteString(polyfillLocalVarsBase)
	if needURL {
		b.WriteString(polyfillLocalVarsURL)
	}
	b.WriteString(polyfillFooter)

	return src + "\n" + b.String()
}

// rewriteDoneCalls 改写 $done 调用。
func rewriteDoneCalls(src string) string {
	// $done({}) → Anywhere.done()
	out := reDoneEmpty.ReplaceAllString(src, "Anywhere.done()")

	// $done() → Anywhere.done()
	out = reDoneNoArg.ReplaceAllString(out, "Anywhere.done()")

	// $done({body: x}) → ctx.body = Anywhere.codec.utf8.encode(x); Anywhere.done()
	out = reDoneBody.
		ReplaceAllString(out, "ctx.body = Anywhere.codec.utf8.encode($1); Anywhere.done()")

	// $done({ body }) ES6 shorthand
	out = reDoneBodyShort.
		ReplaceAllString(out, "ctx.body = Anywhere.codec.utf8.encode(body); Anywhere.done()")

	// $done({response: {...}}) → Anywhere.respond({...})
	// 使用标记替换法处理嵌套大括号
	out = reDoneRespStart.ReplaceAllString(out, "__DONE_RESPONSE_START__")
	// 匹配标记后的内容，使用函数式替换验证大括号平衡
	out = reDoneRespBody.ReplaceAllStringFunc(out, func(match string) string {
		// 提取 responseObj 部分
		sub := reDoneRespBody.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		responseObj := sub[1]
		// 验证大括号是否平衡
		depth := 0
		endIdx := -1
		for i, ch := range responseObj {
			if ch == '{' {
				depth++
			} else if ch == '}' {
				depth--
			}
			if depth == 0 {
				endIdx = i
				break
			}
		}
		if endIdx >= 0 {
			inner := responseObj[:endIdx+1]
			return "Anywhere.respond(" + inner + ")"
		}
		return match
	})

	// $done({...}) 其他形式 → Anywhere.done()
	// 使用标记替换法处理嵌套大括号
	out = reDoneObjStart.ReplaceAllString(out, "__DONE_OBJECT_START__{")
	out = reDoneObjBody.ReplaceAllStringFunc(out, func(match string) string {
		inner := strings.TrimSuffix(strings.TrimSuffix(match, ")"), "}")
		inner = strings.TrimPrefix(inner, "__DONE_OBJECT_START__{")
		// 验证大括号平衡
		depth := 0
		balanced := true
		for _, ch := range inner {
			if ch == '{' {
				depth++
			} else if ch == '}' {
				depth--
			}
			if depth < 0 {
				balanced = false
				break
			}
		}
		if balanced && depth == 0 {
			return "Anywhere.done()"
		}
		return match
	})

	// $done(variable) → _doneVar(variable)
	// 处理剩余的 $done 调用：参数不是对象字面量（如 $done(r)、$done(result)、$done(null)）
	// 这些无法静态分析，需要运行时适配函数 _doneVar 处理 body/headers/status/response 字段
	// 注意：此正则不匹配 $done() 和 $done({...})（已在前面的步骤中处理完毕）
	out = reDoneVar.ReplaceAllString(out, "_doneVar($1)")

	return out
}

// headersHelpers 是注入到 rewrite 模式脚本中的 headers 格式转换共享 helper。
// - _headersToObj: [[name, value], ...] 数组对 → {name: value} 对象
// - _headersToPairs: {name: value} 对象 → [[name, value], ...] 数组对
// 抽取自原 doneVarAdapter / httpClientAdapter / wrapAsProcess 中重复的内联 IIFE，
// 减少双端同步点（Go/EdgeOne 各只需维护一份）。
const headersHelpers = `  function _headersToObj(h) {
    var o = {};
    if (h && h.forEach) { h.forEach(function(p) { o[String(p[0]||"")] = String(p[1]||""); }); }
    return o;
  }
  function _headersToPairs(h) {
    var arr = [];
    if (h && typeof h === 'object' && !Array.isArray(h)) {
      for (var k in h) { if (h.hasOwnProperty(k)) arr.push([k, String(h[k])]); }
    }
    return arr;
  }
`

// doneVarAdapter 是注入到 rewrite 模式脚本中的 $done(variable) 运行时适配函数。
// 当上游脚本使用 $done(r) 形式（r 是运行时变量）时，rewriteDoneCalls 会将其改写为 _doneVar(r)。
// _doneVar 负责将 Loon/Surge 的 {body, headers, status, response} 对象语义映射到 Anywhere API。
// 注意：headers 需要从 {name: value} 对象转换为 [[name, value], ...] 数组对格式。
const doneVarAdapter = `  function _doneVar(r) {
    if (!r || typeof r !== 'object') { Anywhere.done(); return; }
    if (r.response) {
      var resp = r.response;
      var h = resp.headers;
      if (h && typeof h === 'object' && !Array.isArray(h)) {
        h = _headersToPairs(h);
      }
      Anywhere.respond({
        status: resp.status || resp.statusCode || 200,
        headers: h,
        body: resp.body != null ? Anywhere.codec.utf8.encode(String(resp.body)) : undefined
      });
      return;
    }
    if (r.body != null) { ctx.body = Anywhere.codec.utf8.encode(String(r.body)); }
    if (r.headers && typeof r.headers === 'object' && !Array.isArray(r.headers)) {
      ctx.headers = _headersToPairs(r.headers);
    }
    if (r.status != null) { ctx.status = r.status; }
    Anywhere.done();
  }
`

// httpClientAdapter 是注入到 rewrite 模式脚本中的 $httpClient 运行时适配对象。
// 当上游脚本使用 $httpClient.get(var, callback) 等回调变量形式（非内联函数）时，
// rewriteHttpClientCalls 的正则无法匹配。此时注入 $httpClient 对象定义，
// 让上游脚本直接使用兼容的 $httpClient API，而非改写调用形式。
// 注意：headers 需要从 [[name, value], ...] 转换为 {name: value} 对象格式，复用 _headersToObj。
const httpClientAdapter = `  var $httpClient = {
    get: function(url, opts, cb) {
      if (typeof opts === 'function') { cb = opts; opts = null; }
      Anywhere.http.get(url, opts).then(function(res) {
        if (cb) cb(null, { status: res.status || 200, headers: _headersToObj(res.headers || []) }, Anywhere.codec.utf8.decode(res.body || new Uint8Array()));
      }).catch(function(e) { if (cb) cb(e, null, null); });
    },
    post: function(url, opts, cb) {
      if (typeof opts === 'function') { cb = opts; opts = null; }
      Anywhere.http.post(url, opts).then(function(res) {
        if (cb) cb(null, { status: res.status || 200, headers: _headersToObj(res.headers || []) }, Anywhere.codec.utf8.decode(res.body || new Uint8Array()));
      }).catch(function(e) { if (cb) cb(e, null, null); });
    },
    put: function(url, opts, cb) {
      if (typeof opts === 'function') { cb = opts; opts = null; }
      Anywhere.http.put(url, opts).then(function(res) {
        if (cb) cb(null, { status: res.status || 200, headers: _headersToObj(res.headers || []) }, Anywhere.codec.utf8.decode(res.body || new Uint8Array()));
      }).catch(function(e) { if (cb) cb(e, null, null); });
    },
    delete: function(url, opts, cb) {
      if (typeof opts === 'function') { cb = opts; opts = null; }
      Anywhere.http.delete(url, opts).then(function(res) {
        if (cb) cb(null, { status: res.status || 200, headers: _headersToObj(res.headers || []) }, Anywhere.codec.utf8.decode(res.body || new Uint8Array()));
      }).catch(function(e) { if (cb) cb(e, null, null); });
    },
    request: function(opts, cb) {
      Anywhere.http.request(opts).then(function(res) {
        if (cb) cb(null, { status: res.status || 200, headers: _headersToObj(res.headers || []) }, Anywhere.codec.utf8.decode(res.body || new Uint8Array()));
      }).catch(function(e) { if (cb) cb(e, null, null); });
    }
  };
`

// wrapAsProcess 将脚本包装为 function process(ctx) {...}。
// 若源码已定义 process(ctx) 则不重复包装。
// needsAsync 为 true 时使用 async function 声明（用于含 await 的脚本）。
// 当检测到上游脚本可能污染 globalThis 时（如使用了 $loon/$environment/$script 等），
// 自动添加 _saveGlobals/_restoreGlobals 隔离。
func wrapAsProcess(src string, phase int, needsAsync bool, argument string) string {
	trimmed := strings.TrimSpace(src)
	asyncKw := ""
	if needsAsync {
		asyncKw = "async "
	}

	// 检测是否注入了 BoxJS polyfill（由 injectBoxJSPolyfill 添加的标记）
	hasPolyfill := strings.Contains(trimmed, "_BoxJS_Env_injected")

	// 局部变量映射已移至 polyfill 字符串末尾（在 globalThis.XXX 赋值之后）
	// 注意：之前在这里注入 var URLSearchParams = globalThis.URLSearchParams; 会导致
	//       赋值在 polyfill 安装之前执行，读到 undefined。
	// 现在在 polyfill 字符串中所有 globalThis.XXX 赋值之后才声明 var，顺序正确。
	localVarMappings := ""

	// headers 预转换：将 ctx.headers（[[name, value], ...] 数组对）转换为 {name: value} 对象
	// Loon/Surge 的 $request.headers/$response.headers 是对象格式，RewriteScriptAPI 中替换为 _headersObj
	// 注意：必须在 process 函数体最开头执行，确保上游脚本使用前已初始化
	// 如果需要注入 $request/$response 对象，也需要 _headersObj（因为 headers 字段要用转换后的对象）
	needsHeadersObj := strings.Contains(trimmed, "_headersObj") || strings.Contains(trimmed, "$request") || strings.Contains(trimmed, "$response")
	headersInject := ""
	if needsHeadersObj {
		headersInject = "  var _headersObj = (function(h){var o={};if(h&&h.forEach){h.forEach(function(p){o[String(p[0]||\"\")]=String(p[1]||\"\");});}return o;})(ctx.headers);\n"
	}
	argumentInject := buildArgumentInject(argument)

	// 检测是否使用了 _doneVar（由 rewriteDoneCalls 将 $done(variable) 改写而来）
	// 如果是，注入运行时适配函数，将 {body, headers, status, response} 映射到 Anywhere API
	doneVarInject := ""
	if strings.Contains(trimmed, "_doneVar(") {
		doneVarInject = doneVarAdapter
	}

	// 检测是否需要注入 $httpClient 适配对象
	// rewriteHttpClientCalls 的正则只能匹配内联 function/箭头函数形式，
	// 上游脚本若使用 $httpClient.get(var, var) 等变量参数形式则无法改写。
	// 此时注入兼容的 $httpClient 对象定义，让上游脚本直接调用。
	// 检测条件：只要 $httpClient 标识符还存在（说明有未改写的调用），就注入 adapter。
	// 注意：不能用 !contains("Anywhere.http") 判断，因为 BoxJS polyfill 中也会出现 Anywhere.http，
	// 与上游 $httpClient 改写无关，会导致漏注入。
	needsHttpClientVar := strings.Contains(trimmed, "$httpClient")
	httpClientInject := ""
	if needsHttpClientVar {
		httpClientInject = httpClientAdapter
	}

	// headersHelpersInject：doneVarAdapter 和 httpClientAdapter 共享 _headersToObj/_headersToPairs。
	// 只要其中任一被注入，就必须先注入共享 helper（顺序：helpers 在 adapter 之前）。
	headersHelpersInject := ""
	if doneVarInject != "" || httpClientInject != "" {
		headersHelpersInject = headersHelpers
	}

	// 检测是否需要注入 $request/$response 对象定义
	// 上游脚本可能使用 typeof $request、$response.hasOwnProperty(...) 等形式，
	// 这些形式中 $request/$response 作为变量本身出现，无法通过属性替换处理。
	// 需要 注入对象定义，使这些引用能正常工作。
	// 注意：_headersObj 必须在此注入之前已定义（headersInject 已处理）。
	needsReqRespVar := strings.Contains(trimmed, "$request") || strings.Contains(trimmed, "$response")
	reqRespInject := ""
	if needsReqRespVar {
		reqRespInject = "  var $request = {\n" +
			"    url: ctx.url || '',\n" +
			"    method: ctx.method || 'GET',\n" +
			"    headers: typeof _headersObj !== 'undefined' ? _headersObj : {},\n" +
			"    body: Anywhere.codec.utf8.decode(ctx.body || new Uint8Array()),\n" +
			"    bodyBytes: ctx.body\n" +
			"  };\n" +
			"  var $response = {\n" +
			"    status: ctx.status || 200,\n" +
			"    statusCode: ctx.status || 200,\n" +
			"    headers: typeof _headersObj !== 'undefined' ? _headersObj : {},\n" +
			"    body: Anywhere.codec.utf8.decode(ctx.body || new Uint8Array()),\n" +
			"    bodyBytes: ctx.body,\n" +
			"    rawBody: ctx.body\n" +
			"  };\n"
	}

	// 检测是否需要 globalThis 隔离（上游脚本可能往 globalThis 写 $loon/$environment 等）
	needsIsolation := strings.Contains(trimmed, "$loon") ||
		strings.Contains(trimmed, "$environment") ||
		strings.Contains(trimmed, "$script") ||
		strings.Contains(trimmed, "$argument") ||
		strings.Contains(trimmed, "globalThis.$")

	// 检测是否需要 timer 清理（setInterval 递归 Promise 链在 process 返回后可能无限延续）
	needsTimerCleanup := strings.Contains(trimmed, "setTimeout") || strings.Contains(trimmed, "setInterval")

	// 注意：原 GC nudge（finally 中分配 1MB randomBytes 触发 JSGarbageCollect）已移除，
	// 因为在接近 50MB 内存临界值时，1MB 分配会加剧峰值压力，反而触发 VPN 进程重启。

	// 清理代码：globalThis 动态快照 + request-scoped timer 清理
	// 当 needsIsolation/needsTimerCleanup 时，添加 try/finally 块
	// 关键改进：
	//   1. 动态快照 Object.getOwnPropertyNames 替代固定 10 个名称，捕获上游脚本所有 globalThis 写入
	//   2. _POLYFILL_NAMES 排除列表保护 polyfill 安装的属性，避免每次请求重新安装
	//   3. _requestTimers 注册表在 finally 中批量清理，防止 setInterval 泄漏
	//   4. GC nudge 加速 TypedArray 计数器达到 16MB 软上限，触发全量 JSGarbageCollect 回收 JSCore 堆
	// 注意：_saveGlobals/_restoreGlobals 定义内联在 try 块之前，确保 JSCore function 声明可用
	isolationPrefix := ""
	isolationSuffix := ""
	if needsIsolation || needsTimerCleanup {
		var b strings.Builder
		b.WriteString("  var _requestTimers = [];\n")
		b.WriteString("  globalThis._requestTimersStack = globalThis._requestTimersStack || [];\n")
		b.WriteString("  globalThis._requestTimersStack.push(_requestTimers);\n")
		if needsIsolation {
			b.WriteString("  var _globalsSnapshot = {};\n")
			b.WriteString("  var _POLYFILL_NAMES = [\"console\", \"URLSearchParams\", \"URL\", \"TextEncoder\", \"TextDecoder\", \"Headers\", \"Request\", \"Response\", \"fetch\", \"setTimeout\", \"clearTimeout\", \"setInterval\", \"clearInterval\", \"atob\", \"btoa\", \"Env\", \"_wrapBoxJSResponse\", \"_wrapBoxJSRequest\", \"_boxBytes\", \"_boxHeaderPairs\", \"_boxRequest\", \"$env\", \"_requestTimersStack\"];\n")
			b.WriteString("  function _saveGlobals(snapshot) { var names = Object.getOwnPropertyNames(globalThis); for (var i = 0; i < names.length; i++) { var name = names[i]; if (_POLYFILL_NAMES.indexOf(name) >= 0) continue; snapshot[name] = globalThis[name]; } }\n")
			b.WriteString("  function _restoreGlobals(snapshot) { var names = Object.getOwnPropertyNames(globalThis); for (var i = 0; i < names.length; i++) { var name = names[i]; if (_POLYFILL_NAMES.indexOf(name) >= 0) continue; if (!snapshot.hasOwnProperty(name)) delete globalThis[name]; else globalThis[name] = snapshot[name]; } }\n")
			b.WriteString("  _saveGlobals(_globalsSnapshot);\n")
		}
		b.WriteString("  try {\n")
		isolationPrefix = b.String()

		var sb strings.Builder
		sb.WriteString("\n  } finally {\n")
		if needsTimerCleanup {
			sb.WriteString("    for (var _ti = 0; _ti < _requestTimers.length; _ti++) { if (_requestTimers[_ti]) _requestTimers[_ti].active = false; }\n")
			sb.WriteString("    var _tsIdx = globalThis._requestTimersStack ? globalThis._requestTimersStack.indexOf(_requestTimers) : -1; if (_tsIdx >= 0) globalThis._requestTimersStack.splice(_tsIdx, 1);\n")
		}
		if needsIsolation {
			sb.WriteString("    _restoreGlobals(_globalsSnapshot);\n")
		}
		sb.WriteString("  }\n")
		isolationSuffix = sb.String()
	}

	// polyfill 注入到 process 函数体内部的辅助函数
	injectPolyfillIntoProcess := func(out string) string {
		if !hasPolyfill {
			return out
		}
		// 提取 polyfill 部分（从 _BoxJS_Env_injected 到 === 结束标记）
		polyfillCode := rePolyfillBlock.FindString(out)
		if polyfillCode == "" {
			// 没有找到 polyfill 代码，只注入局部变量映射
			out = reProcessSyncDecl.
				ReplaceAllString(out, "${1}\n"+localVarMappings)
			return out
		}
		// 从原位置移除 polyfill
		out = strings.Replace(out, polyfillCode, "", 1)
		// 缩进 polyfill 代码
		indentedPolyfill := strings.ReplaceAll(polyfillCode, "\n", "\n  ")
		// 注入到 process 函数体开头
		injectCode := localVarMappings + indentedPolyfill
		out = reProcessSyncDecl.
			ReplaceAllString(out, "${1}\n"+injectCode)
		return out
	}

	// 已有 process 函数定义（同步或异步）
	if reHasProcessSync.MatchString(trimmed) {
		out := trimmed
		if needsAsync && !strings.HasPrefix(out, "async ") {
			out = "async " + out
		}
		// 注入 headers 预转换变量、headers 共享 helper、$request/$response 对象、$argument、_doneVar 适配函数、$httpClient 适配对象（必须在最开头，polyfill 之前）
		// 顺序：headersHelpers 必须在 doneVarInject/httpClientInject 之前（adapter 引用 helper）
		injectPrefix := headersInject + headersHelpersInject + reqRespInject + argumentInject + doneVarInject + httpClientInject
		if injectPrefix != "" {
			out = reProcessSyncDecl.
				ReplaceAllString(out, "${1}\n"+injectPrefix)
		}
		// 将 polyfill 移入 process 函数体内部
		out = injectPolyfillIntoProcess(out)
		if (needsIsolation || needsTimerCleanup) && isolationPrefix != "" {
			out = reProcessSyncDecl.
				ReplaceAllString(out, "${1}\n"+isolationPrefix)
			lastBrace := strings.LastIndex(out, "}")
			if lastBrace > 0 {
				out = out[:lastBrace] + isolationSuffix + out[lastBrace:]
			}
		}
		return out
	}
	if reHasProcessAsync.MatchString(trimmed) {
		out := trimmed
		// 注入 headers 预转换变量、headers 共享 helper、$request/$response 对象、$argument、_doneVar 适配函数、$httpClient 适配对象
		// 顺序：headersHelpers 必须在 doneVarInject/httpClientInject 之前（adapter 引用 helper）
		injectPrefix := headersInject + headersHelpersInject + reqRespInject + argumentInject + doneVarInject + httpClientInject
		if injectPrefix != "" {
			out = reProcessAsyncDecl.
				ReplaceAllString(out, "${1}\n"+injectPrefix)
		}
		out = injectPolyfillIntoProcess(out)
		if (needsIsolation || needsTimerCleanup) && isolationPrefix != "" {
			out = reProcessAsyncDecl.
				ReplaceAllString(out, "${1}\n"+isolationPrefix)
			lastBrace := strings.LastIndex(out, "}")
			if lastBrace > 0 {
				out = out[:lastBrace] + isolationSuffix + out[lastBrace:]
			}
		}
		return out
	}

	// 若源码定义了 function run()，则包装为 process 并调用 run
	if reHasRunDecl.MatchString(trimmed) {
		phaseCheck := "request"
		if phase == 1 {
			phaseCheck = "response"
		}
		return fmt.Sprintf(`%sfunction process(ctx) {
  if (ctx.phase !== "%s") return;
%s%s%s%s%s%s%s%s  try {
    run();
  } catch (e) {
    Anywhere.log.warning("script error: " + e);
  }%s
}

%s
`, asyncKw, phaseCheck, headersInject, headersHelpersInject, reqRespInject, argumentInject, doneVarInject, httpClientInject, localVarMappings, isolationPrefix, isolationSuffix, trimmed)
	}

	// 否则整体包装
	phaseCheck := "request"
	if phase == 1 {
		phaseCheck = "response"
	}
	return fmt.Sprintf(`%sfunction process(ctx) {
  if (ctx.phase !== "%s") return;
%s%s%s%s%s%s%s%s  try {
%s
  } catch (e) {
    Anywhere.log.warning("script error: " + e);
  }%s
  Anywhere.done();
}
`, asyncKw, phaseCheck, headersInject, headersHelpersInject, reqRespInject, argumentInject, doneVarInject, httpClientInject, localVarMappings, isolationPrefix, indent(trimmed, "    "), isolationSuffix)
}

// buildArgumentInject 构造 $argument 兼容注入代码。
func buildArgumentInject(argument string) string {
	argument = strings.TrimSpace(argument)
	if argument == "" {
		return ""
	}
	b, _ := json.Marshal(argument)
	return "  globalThis.$argument = " + string(b) + ";\n  var $argument = globalThis.$argument;\n"
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

// BuildTransparentRewriteScript 构造透明 URL 重写脚本（带捕获组）。
// Anywhere rewrite sub-mode 0 不支持 $1 捕获展开，带捕获组的透明重写需用脚本实现。
// 脚本通过 Anywhere.http.request 向新 URL 发请求，再用 Anywhere.respond 返回响应。
func BuildTransparentRewriteScript(pattern, captureURL string) string {
	js := fmt.Sprintf(`async function process(ctx) {
  if (ctx.phase !== "request" || !ctx.url) return;
  var m = ctx.url.match(%s);
  if (!m) return;
  var newUrl = %s;
  try {
    var headers = [];
    (ctx.headers || []).forEach(function(h) {
      var name = String(h[0] || "");
      var lower = name.toLowerCase();
      if (!name || lower === "host" || lower === "content-length" || lower === "connection" || lower === "transfer-encoding") return;
      headers.push([name, String(h[1] || "")]);
    });
    var res = await Anywhere.http.request({
      url: newUrl,
      method: ctx.method || "GET",
      headers: headers,
      timeout: 8000,
      redirect: "follow"
    });
    Anywhere.respond({
      status: res.status || 200,
      headers: res.headers || [],
      body: res.body || new Uint8Array()
    });
  } catch (e) {
    Anywhere.log.warning("transparent rewrite failed: " + e);
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

// BuildWrappedScript 将上游脚本源码 base64 编码存储，
// 生成一个包装器 process(ctx) 函数，在运行时构造 $request/$response/$persistentStore/$done 等
// Loon/Surge 兼容全局变量，然后用 new Function(source)() 执行上游脚本。
// 这种方式不做字符串替换，能最大程度保持上游脚本的原始逻辑，
// 适用于 wloc.js 等自包含跨平台脚本。
func BuildWrappedScript(rawJS string, phase int, argument string) string {
	phaseCheck := "request"
	if phase == 1 {
		phaseCheck = "response"
	}
	needsAsync := strings.Contains(rawJS, "$httpClient") || strings.Contains(rawJS, "$.http") ||
		strings.Contains(rawJS, "$env.http") || strings.Contains(rawJS, "await ") ||
		strings.Contains(rawJS, "async ") || strings.Contains(rawJS, "$done({response:")

	upstreamB64 := base64.StdEncoding.EncodeToString([]byte(rawJS))
	argumentLiteral := "{}"
	if strings.TrimSpace(argument) != "" {
		if b, err := json.Marshal(argument); err == nil {
			argumentLiteral = string(b)
		}
	}

	asyncKw := ""
	if needsAsync {
		asyncKw = "async "
	}

	wrapper := fmt.Sprintf(`%sfunction process(ctx) {
  if (ctx.phase !== "%s") return;
  var _requestTimers = [];
  globalThis._requestTimersStack = globalThis._requestTimersStack || [];
  globalThis._requestTimersStack.push(_requestTimers);
  var _globalsSnapshot = {};
  var _POLYFILL_NAMES = ["console", "URLSearchParams", "URL", "TextEncoder", "TextDecoder", "Headers", "Request", "Response", "fetch", "setTimeout", "clearTimeout", "setInterval", "clearInterval", "atob", "btoa", "Env", "_wrapBoxJSResponse", "_wrapBoxJSRequest", "_boxBytes", "_boxHeaderPairs", "_boxRequest", "$env", "_requestTimersStack"];
  function _saveGlobals(snapshot) { var names = Object.getOwnPropertyNames(globalThis); for (var i = 0; i < names.length; i++) { var name = names[i]; if (_POLYFILL_NAMES.indexOf(name) >= 0) continue; snapshot[name] = globalThis[name]; } }
  function _restoreGlobals(snapshot) { var names = Object.getOwnPropertyNames(globalThis); for (var i = 0; i < names.length; i++) { var name = names[i]; if (_POLYFILL_NAMES.indexOf(name) >= 0) continue; if (!snapshot.hasOwnProperty(name)) delete globalThis[name]; else globalThis[name] = snapshot[name]; } }
  _saveGlobals(_globalsSnapshot);
  try {
    return await new Promise(function(resolve) {
      var settled = false;
      function finish(out) {
        if (settled) return;
        settled = true;
        resolve(out || {});
      }

      // 构造 Loon/Surge 兼容全局变量
      // 注意：ctx.headers 是 [[name, value], ...] 数组对，Loon/Surge 的 $request.headers 是 {name: value} 对象，需要转换
      function _headersToObject(headers) {
        var obj = {};
        if (headers && headers.forEach) {
          headers.forEach(function(h) { obj[String(h[0] || "")] = String(h[1] || ""); });
        }
        return obj;
      }
      globalThis.$loon = {};
      globalThis.$environment = undefined;
      globalThis.$script = { startTime: new Date() };
		// $argument 先注入原始字符串，供脚本自行解析；空字符串会导致某些脚本读取失败
      globalThis.$argument = %s;
      globalThis.$request = {
        url: ctx.url || '',
        method: ctx.method || 'GET',
        headers: _headersToObject(ctx.headers),
        body: Anywhere.codec.utf8.decode(ctx.body || new Uint8Array())
      };
      globalThis.$response = {
        status: ctx.status || 200,
        statusCode: ctx.status || 200,
        headers: _headersToObject(ctx.headers),
        body: Anywhere.codec.utf8.decode(ctx.body || new Uint8Array()),
        bodyBytes: ctx.body,
        rawBody: ctx.body
      };
      globalThis.$persistentStore = {
        read: function(key) {
          var value = Anywhere.store.getString(key, true);
          return typeof value === "undefined" ? null : value;
        },
        write: function(value, key) {
          try {
            if (value === null || typeof value === "undefined") {
              Anywhere.store.delete(key, true);
            } else {
              Anywhere.store.set(key, String(value), true);
            }
            return true;
          } catch (e) { return false; }
        }
      };
      globalThis.$done = finish;
      globalThis.$httpClient = {
        get: function(url, cb) { _wrapHttp('get', url, null, cb); },
        post: function(url, opts, cb) { _wrapHttp('post', url, opts, cb); },
        put: function(url, opts, cb) { _wrapHttp('put', url, opts, cb); },
        delete: function(url, opts, cb) { _wrapHttp('delete', url, opts, cb); },
        request: function(opts, cb) { _wrapHttp('request', null, opts, cb); }
      };
      globalThis.$notification = {
        post: function(title, sub, body) { Anywhere.log.info(title + " " + (sub||"") + " " + (body||"")); }
      };

      // 注入 Web API Polyfill 到 globalThis（确保 new Function() 内可访问）
      if (typeof Array.isArray === 'undefined') {
        Array.isArray = function(arg) { return Object.prototype.toString.call(arg) === '[object Array]'; };
      }
      if (typeof globalThis.URLSearchParams === 'undefined') {
        globalThis.URLSearchParams = function(init) {
          this._params = [];
          if (typeof init === 'string') {
            var s = init.charAt(0) === '?' ? init.slice(1) : init;
            var pairs = s.split('&');
            for (var i = 0; i < pairs.length; i++) {
              var idx = pairs[i].indexOf('=');
              if (idx < 0) { this._params.push([decodeURIComponent(pairs[i]), '']); }
              else { this._params.push([decodeURIComponent(pairs[i].slice(0, idx)), decodeURIComponent(pairs[i].slice(idx + 1))]); }
            }
          } else if (init && typeof init === 'object' && !Array.isArray(init)) {
            for (var key in init) { if (init.hasOwnProperty(key)) this._params.push([key, String(init[key])]); }
          } else if (Array.isArray(init)) {
            for (var i = 0; i < init.length; i++) { this._params.push([String(init[i][0]), String(init[i][1])]); }
          }
        };
        globalThis.URLSearchParams.prototype.append = function(n, v) { this._params.push([n, v]); };
        globalThis.URLSearchParams.prototype.delete = function(n) { this._params = this._params.filter(function(p) { return p[0] !== n; }); };
        globalThis.URLSearchParams.prototype.get = function(n) { for (var i = 0; i < this._params.length; i++) { if (this._params[i][0] === n) return this._params[i][1]; } return null; };
        globalThis.URLSearchParams.prototype.has = function(n) { for (var i = 0; i < this._params.length; i++) { if (this._params[i][0] === n) return true; } return false; };
        globalThis.URLSearchParams.prototype.set = function(n, v) { var f = false; for (var i = 0; i < this._params.length; i++) { if (this._params[i][0] === n) { this._params[i][1] = v; f = true; break; } } if (!f) this._params.push([n, v]); };
        globalThis.URLSearchParams.prototype.toString = function() { return this._params.map(function(p) { return encodeURIComponent(p[0]) + '=' + encodeURIComponent(p[1]); }).join('&'); };
        globalThis.URLSearchParams.prototype.forEach = function(cb, t) { for (var i = 0; i < this._params.length; i++) { cb.call(t, this._params[i][1], this._params[i][0], this); } };
      }
      var URLSearchParams = globalThis.URLSearchParams;
      // 注入 globalThis.URL polyfill（与 injectBoxJSPolyfill 保持一致）
      if (typeof globalThis.URL === 'undefined') {
        globalThis.URL = function(url, base) {
          var fullUrl = url;
          if (base) {
            if (url.indexOf('://') < 0) {
              var baseEnd = base.lastIndexOf('/');
              fullUrl = (baseEnd >= 0 ? base.slice(0, baseEnd + 1) : base + '/') + url;
            } else { fullUrl = url; }
          }
          var m = fullUrl.match(/^(https?):\\/\\/([^:/?#]+)(?::(\\d+))?([^?#]*)?(\\?[^#]*)?(#.*)?$/);
          if (!m) throw new Error('Invalid URL: ' + fullUrl);
          this.protocol = m[1] + ':';
          this.hostname = m[2];
          this.port = m[3] || '';
          this.host = this.hostname + (this.port ? ':' + this.port : '');
          this.pathname = m[4] || '/';
          this.search = m[5] || '';
          this.hash = m[6] || '';
          this.href = fullUrl;
          this.origin = this.protocol + '//' + this.host;
          this.searchParams = new globalThis.URLSearchParams(this.search);
          Object.defineProperty(this, 'username', { get: function() { return ''; } });
          Object.defineProperty(this, 'password', { get: function() { return ''; } });
        };
        globalThis.URL.prototype.toString = function() { return this.href; };
        globalThis.URL.prototype.toJSON = function() { return this.href; };
      }
      var URL = globalThis.URL;
      if (typeof globalThis.console === 'undefined') {
        globalThis.console = { log: function() { Anywhere.log.info([].slice.call(arguments).map(String).join(' ')); }, warn: function() { Anywhere.log.warning([].slice.call(arguments).map(String).join(' ')); }, error: function() { Anywhere.log.error([].slice.call(arguments).map(String).join(' ')); }, info: function() { Anywhere.log.info([].slice.call(arguments).map(String).join(' ')); } };
      }
      var console = globalThis.console;
      if (typeof globalThis.atob === 'undefined') {
        globalThis.atob = function(s) { return Anywhere.codec.utf8.decode(Anywhere.codec.base64.decode(s)); };
        globalThis.btoa = function(s) { return Anywhere.codec.base64.encode(Anywhere.codec.utf8.encode(s)); };
      }
      var atob = globalThis.atob;
      var btoa = globalThis.btoa;

      // HTTP 辅助函数
      // 注意：Anywhere.http 返回的 res.headers 是 [[name, value], ...] 数组对格式，
      // Loon/Surge 的 $httpClient 回调中 resp.headers 是 {name: value} 对象格式，需要转换
      function _wrapHttp(method, url, opts, cb) {
        var p;
        if (method === 'request') { p = Anywhere.http.request(opts); }
        else if (method === 'get') { p = Anywhere.http.get(typeof url === 'string' ? url : url, opts); }
        else { p = Anywhere.http[method](typeof url === 'string' ? url : url, opts); }
        p.then(function(res) {
          cb(null, { status: res.status || 200, headers: _headersToObject(res.headers || []) }, Anywhere.codec.utf8.decode(res.body || new Uint8Array()));
        }).catch(function(e) { cb(e, null, null); });
      }

      // 解码并执行上游脚本
      // 注意：new Function() 创建的函数只能访问全局作用域，无法访问外层 var 变量。
      // 因此在源码前注入 var 声明，将 globalThis 上的 polyfill 映射为局部标识符。
      try {
        var _upstreamSource = Anywhere.codec.utf8.decode(Anywhere.codec.base64.decode("%s"));
        var _polyfillVars = "var URLSearchParams = globalThis.URLSearchParams; var URL = globalThis.URL; var console = globalThis.console; var atob = globalThis.atob; var btoa = globalThis.btoa; var $env = globalThis.$env || { isBoxJS: false, isAnywhere: true };";
        new Function(_polyfillVars + "\\n" + _upstreamSource)();
      } catch (e) {
        Anywhere.log.error("[wrap] upstream script error: " + e);
        finish({});
      }
    }).then(function(out) {
      var response = out.response || out;
      var body = _wlocBytes(response.bodyBytes || response.rawBody || response.body);
      if (body.length > 0) ctx.body = body;
    });
  } finally {
    for (var _ti = 0; _ti < _requestTimers.length; _ti++) { if (_requestTimers[_ti]) _requestTimers[_ti].active = false; }
    var _tsIdx = globalThis._requestTimersStack ? globalThis._requestTimersStack.indexOf(_requestTimers) : -1; if (_tsIdx >= 0) globalThis._requestTimersStack.splice(_tsIdx, 1);
    _restoreGlobals(_globalsSnapshot);
  }
}

function _wlocBytes(value) {
  if (!value) return new Uint8Array();
  if (value instanceof Uint8Array) return value;
  if (value instanceof ArrayBuffer) return new Uint8Array(value);
  if (ArrayBuffer.isView(value)) return new Uint8Array(value.buffer, value.byteOffset, value.byteLength);
  if (typeof value === "string") return Anywhere.codec.utf8.encode(value);
  return new Uint8Array();
}

// === 顶层 Web API Polyfill（与 injectBoxJSPolyfill 保持一致）===
// 注意：包装执行模式下，上游脚本通过 new Function(_polyfillVars + src)() 执行，
// 只能访问 globalThis 上的属性。因此必须在此处安装顶层 polyfill，
// 否则 wloc.js 等自包含脚本中的 fetch/setTimeout/TextEncoder/Headers 等会 ReferenceError。
function _boxBytes(value) {
  if (!value) return new Uint8Array();
  if (value instanceof Uint8Array) return value;
  if (value instanceof ArrayBuffer) return new Uint8Array(value);
  if (ArrayBuffer.isView(value)) return new Uint8Array(value.buffer, value.byteOffset, value.byteLength);
  if (typeof value === 'string') return Anywhere.codec.utf8.encode(value);
  return Anywhere.codec.utf8.encode(JSON.stringify(value));
}
function _boxHeaderPairs(headers) {
  if (!headers) return [];
  if (Array.isArray(headers)) return headers;
  if (headers && headers._items) return headers._items;
  var out = [];
  for (var name in headers) { if (headers.hasOwnProperty(name)) out.push([String(name), String(headers[name])]); }
  return out;
}
function _boxRequest(input, init) {
  var req = {};
  if (typeof input === 'string') req.url = input;
  else if (input && typeof input === 'object') { for (var k in input) if (input.hasOwnProperty(k)) req[k] = input[k]; }
  if (init && typeof init === 'object') { for (var k2 in init) if (init.hasOwnProperty(k2)) req[k2] = init[k2]; }
  req.method = String(req.method || (req.body ? 'POST' : 'GET')).toUpperCase();
  req.headers = _boxHeaderPairs(req.headers);
  if (req.body) req.body = _boxBytes(req.body);
  return req;
}
if (typeof globalThis.TextEncoder === 'undefined') {
  globalThis.TextEncoder = function() {};
  globalThis.TextEncoder.prototype.encode = function(value) { return Anywhere.codec.utf8.encode(String(value)); };
}
if (typeof globalThis.TextDecoder === 'undefined') {
  globalThis.TextDecoder = function() {};
  globalThis.TextDecoder.prototype.decode = function(value) { return Anywhere.codec.utf8.decode(_boxBytes(value)); };
}
if (typeof globalThis.Headers === 'undefined') {
  globalThis.Headers = function(init) { this._items = _boxHeaderPairs(init); };
  globalThis.Headers.prototype.append = function(name, value) { this._items.push([String(name), String(value)]); };
  globalThis.Headers.prototype.get = function(name) { name = String(name).toLowerCase(); for (var i = 0; i < this._items.length; i++) if (String(this._items[i][0]).toLowerCase() === name) return this._items[i][1]; return null; };
  globalThis.Headers.prototype.has = function(name) { return this.get(name) !== null; };
  globalThis.Headers.prototype.set = function(name, value) { this.delete(name); this.append(name, value); };
  globalThis.Headers.prototype.delete = function(name) { name = String(name).toLowerCase(); this._items = this._items.filter(function(h) { return String(h[0]).toLowerCase() !== name; }); };
  globalThis.Headers.prototype.forEach = function(cb, thisArg) { for (var i = 0; i < this._items.length; i++) cb.call(thisArg, this._items[i][1], this._items[i][0], this); };
}
if (typeof globalThis.Request === 'undefined') {
  globalThis.Request = function(input, init) { var req = _boxRequest(input, init); this.url = req.url; this.method = req.method; this.headers = new globalThis.Headers(req.headers); this.body = req.body; };
}
if (typeof globalThis.Response === 'undefined') {
  globalThis.Response = function(body, init) { init = init || {}; this.body = _boxBytes(body); this.status = init.status || 200; this.headers = new globalThis.Headers(init.headers); };
  globalThis.Response.prototype.text = function() { var self = this; return Promise.resolve(Anywhere.codec.utf8.decode(self.body || new Uint8Array())); };
  globalThis.Response.prototype.json = function() { return this.text().then(function(t) { return JSON.parse(t); }); };
  globalThis.Response.prototype.arrayBuffer = function() { return Promise.resolve((this.body || new Uint8Array()).buffer); };
}
if (typeof globalThis.fetch === 'undefined') {
  globalThis.fetch = function(input, init) {
    var req = _boxRequest(input, init);
    var opts = { method: req.method, headers: req.headers, body: req.body };
    if (req.method === 'GET' || req.method === 'HEAD') delete opts.body;
    var p = req.method === 'GET' ? Anywhere.http.get(req.url, opts) : req.method === 'POST' ? Anywhere.http.post(req.url, opts) : req.method === 'PUT' ? Anywhere.http.put(req.url, opts) : req.method === 'DELETE' ? Anywhere.http.delete(req.url, opts) : Anywhere.http.request(opts);
    return p.then(function(res) { return new globalThis.Response(res.body || new Uint8Array(), { status: res.status || 200, headers: res.headers || [] }); });
  };
}
if (typeof globalThis.setTimeout === 'undefined') globalThis.setTimeout = function(fn, ms) { var h = { active: true }; var _s = globalThis._requestTimersStack; if (_s && _s.length) _s[_s.length - 1].push(h); Anywhere.wait(ms || 0).then(function() { if (h.active) fn(); }); return h; };
if (typeof globalThis.clearTimeout === 'undefined') globalThis.clearTimeout = function(h) { if (h) h.active = false; };
if (typeof globalThis.setInterval === 'undefined') globalThis.setInterval = function(fn, ms) { var h = { active: true }; var _s = globalThis._requestTimersStack; if (_s && _s.length) _s[_s.length - 1].push(h); (function tick(){ if (!h.active) return; Anywhere.wait(ms || 0).then(function(){ if (!h.active) return; fn(); tick(); }); })(); return h; };
if (typeof globalThis.clearInterval === 'undefined') globalThis.clearInterval = function(h) { if (h) h.active = false; };
if (typeof globalThis.console !== 'undefined') {
  if (typeof globalThis.console.assert === 'undefined') globalThis.console.assert = function(cond) { if (!cond) globalThis.console.warn('Assertion failed'); };
  if (typeof globalThis.console.trace === 'undefined') globalThis.console.trace = function() { globalThis.console.warn('trace'); };
  if (typeof globalThis.console.table === 'undefined') globalThis.console.table = function(obj) { globalThis.console.info(JSON.stringify(obj)); };
}
`, asyncKw, phaseCheck, argumentLiteral, upstreamB64)

	return wrapper
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

// WrapAsStreamScript 将已改写的脚本包装为 stream-script (op 101) 形式。
// stream-script 的低内存收益来自逐帧处理；转换器不能默认把所有 frame 累积到 ctx.state，
// 否则长连接/SSE/大响应会重新退化为全量缓冲，甚至比 script 更容易触发 VPN 扩展内存上限。
//
// 注意：这是尽力包装。需要跨帧状态的脚本应显式、少量地使用 ctx.state。
func WrapAsStreamScript(rewrittenSrc string, phase int) string {
	phaseCheck := "request"
	if phase == 1 {
		phaseCheck = "response"
	}
	// 移除已包装的 process 函数，提取内部逻辑
	inner := extractProcessBody(rewrittenSrc)
	if inner == "" {
		inner = rewrittenSrc
	}

	tmpl := fmt.Sprintf(`async function process(ctx) {
  if (ctx.phase !== "%s" || !ctx.body) return;
  try {
%s
  } catch (e) {
    Anywhere.log.warning("stream process failed: " + e);
  }
}
`, phaseCheck, indent(inner, "    "))
	return tmpl
}

// extractProcessBody 从已包装的 process(ctx) 函数中提取内部逻辑。
// 若不是 process 函数则返回空字符串。
func extractProcessBody(src string) string {
	// 匹配 async function process(ctx) {...} 或 function process(ctx) {...}
	// 简化：找到第一个 { 与最后一个 }
	trimmed := strings.TrimSpace(src)
	if !strings.Contains(trimmed, "function process(ctx)") {
		return ""
	}
	firstBrace := strings.Index(trimmed, "{")
	lastBrace := strings.LastIndex(trimmed, "}")
	if firstBrace < 0 || lastBrace < 0 || lastBrace <= firstBrace {
		return ""
	}
	body := trimmed[firstBrace+1 : lastBrace]
	// 移除 phase 检查与 try/catch 包装，保留核心逻辑
	// 简化处理：直接返回 body，由 stream 包装重新组织
	return strings.TrimSpace(body)
}
