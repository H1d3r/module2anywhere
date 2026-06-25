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

	"github.com/H1d3r/module2anywhere/fetcher"
	"github.com/H1d3r/module2anywhere/ir"
)

// FetchAndEncodeScript 下载脚本，改写 API，base64 编码。
// scriptPath 可以是 URL 或本地路径。baseURL 用于解析相对路径。
// 若 fetchScripts=false，返回占位符 base64。
// 若 useStreamScript=true，将改写后的脚本再包装为 stream-script (op 101) 形式。
func FetchAndEncodeScript(ctx context.Context, f *fetcher.Fetcher, scriptPath, baseURL string, fetchScripts bool, phase int, useStreamScript bool, wrapScript bool) (string, error) {
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

	// 包装执行模式：将上游脚本源码 base64 编码，生成包装器 process(ctx)
	if wrapScript {
		wrapped := BuildWrappedScript(src, phase)
		return base64.StdEncoding.EncodeToString([]byte(wrapped)), nil
	}

	rewritten := RewriteScriptAPI(src, phase)
	if useStreamScript {
		rewritten = WrapAsStreamScript(rewritten, phase)
	}
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
//   - $httpClient.get/post(...)  → await Anywhere.http.get/post(...)（自动 async 包装）
//
// 当检测到 $httpClient 调用时，自动将 process 函数声明为 async。
// 注意：复杂脚本可能需要人工审核。本函数做尽力改写。
func RewriteScriptAPI(src string, phase int) string {
	// 0. 检测是否需要 async（含 $httpClient 或 await 关键字）
	needsAsync := strings.Contains(src, "$httpClient") || strings.Contains(src, "$done({response:") ||
		strings.Contains(src, "$.http") || strings.Contains(src, "$env.http") || strings.Contains(src, "await $.wait")

	// 1. 简单符号替换
	out := src

	// $request / $response → ctx（保留 .url/.method/.status/.headers）
	out = strings.ReplaceAll(out, "$request.url", "ctx.url")
	out = strings.ReplaceAll(out, "$request.method", "ctx.method")
	out = strings.ReplaceAll(out, "$request.headers", "ctx.headers")
	out = strings.ReplaceAll(out, "$response.status", "ctx.status")
	out = strings.ReplaceAll(out, "$response.headers", "ctx.headers")

	// body 需 codec 转换：$response.body → Anywhere.codec.utf8.decode(ctx.body)
	// Anywhere 中 ctx.body 是 Uint8Array，不是字符串，不能直接调用 .replace() 等字符串方法。
	// 因此所有对 $request.body / $response.body 的读取都需要 decode 为字符串。
	// 注意：JSON.parse($response.body) 已在步骤 6 中单独处理，此处先做通用替换。
	out = strings.ReplaceAll(out, "$request.body", "Anywhere.codec.utf8.decode(ctx.body)")
	out = strings.ReplaceAll(out, "$response.body", "Anywhere.codec.utf8.decode(ctx.body)")

	// 2. $done 改写
	out = rewriteDoneCalls(out)

	// 3. $persistentStore
	out = regexp.MustCompile(`\$persistentStore\.read\(\s*([^)]+?)\s*\)`).
		ReplaceAllString(out, "Anywhere.store.getString($1, true)")
	// $persistentStore.write(val, key) — 需要处理 null/undefined 的删除语义
	// 当 val 为 null 或 undefined 时，应调用 Anywhere.store.delete(key, true) 而非 set
	out = regexp.MustCompile(`\$persistentStore\.write\(\s*([^,]+?)\s*,\s*([^)]+?)\s*\)`).
		ReplaceAllStringFunc(out, func(match string) string {
			sub := regexp.MustCompile(`\$persistentStore\.write\(\s*([^,]+?)\s*,\s*([^)]+?)\s*\)`)
			parts := sub.FindStringSubmatch(match)
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
	out = regexp.MustCompile(`\$notification\.post\(\s*([^,]+?)\s*,\s*([^,]*?)\s*,\s*([^)]+?)\s*\)`).
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
	out = wrapAsProcess(out, phase, needsAsync)

	return out
}

// rewriteHttpClientCalls 改写 $httpClient.get/post 回调式调用为 await Anywhere.http.*。
// 由于 Loon/Surge 使用回调式而 Anywhere 使用 Promise 式，完整转换需解析回调体。
// 此处做尽力改写：将 $httpClient.get(url, cb) 替换为 await Anywhere.http.get(url)，
// 并保留原回调体作为后续处理（用户需手动调整）。
func rewriteHttpClientCalls(src string) string {
	out := src
	// $httpClient.get(url, cb) → await Anywhere.http.get(url)
	out = regexp.MustCompile(`\$httpClient\.get\(\s*([^,]+?)\s*,`).
		ReplaceAllString(out, "await Anywhere.http.get($1")
	// $httpClient.post(url, opts, cb) → await Anywhere.http.post(url, opts)
	out = regexp.MustCompile(`\$httpClient\.post\(\s*([^,]+?)\s*,\s*([^,]+?)\s*,`).
		ReplaceAllString(out, "await Anywhere.http.post($1, $2")
	// $httpClient.put/delete 类似
	out = regexp.MustCompile(`\$httpClient\.put\(\s*([^,]+?)\s*,`).
		ReplaceAllString(out, "await Anywhere.http.put($1")
	out = regexp.MustCompile(`\$httpClient\.delete\(\s*([^,]+?)\s*,`).
		ReplaceAllString(out, "await Anywhere.http.delete($1")
	// 通用 $httpClient.request(opts, cb) → await Anywhere.http.request(opts)
	out = regexp.MustCompile(`\$httpClient\.request\(\s*([^,]+?)\s*,`).
		ReplaceAllString(out, "await Anywhere.http.request($1")
	return out
}

// boxjsEnvPattern 匹配脚本中使用了 BoxJS Env 类或 $.xxx API 或常见缺失 Web API 的特征。
var boxjsEnvPattern = regexp.MustCompile(`new\s+Env\s*\(|\$\.((?i)getdata|setdata|getjson|setjson|msg|log|logErr|http|isQuanX|isSurge|isLoon|isNode|wait|done|name)|\$env\s*\.|URLSearchParams|new\s+URL\s*\(|console\.(log|warn|error|info|debug)`)

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

	polyfill := `// === BoxJS Env 兼容层 + Web API Polyfill (由 module2anywhere 自动注入) ===
var _BoxJS_Env_injected = true;

// --- Web API Polyfill: URLSearchParams ---
if (typeof URLSearchParams === 'undefined') {
  var URLSearchParams = function(init) {
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
  URLSearchParams.prototype.append = function(name, value) { this._params.push([name, value]); };
  URLSearchParams.prototype.delete = function(name) { this._params = this._params.filter(function(p) { return p[0] !== name; }); };
  URLSearchParams.prototype.get = function(name) { for (var i = 0; i < this._params.length; i++) { if (this._params[i][0] === name) return this._params[i][1]; } return null; };
  URLSearchParams.prototype.getAll = function(name) { var r = []; for (var i = 0; i < this._params.length; i++) { if (this._params[i][0] === name) r.push(this._params[i][1]); } return r; };
  URLSearchParams.prototype.has = function(name) { for (var i = 0; i < this._params.length; i++) { if (this._params[i][0] === name) return true; } return false; };
  URLSearchParams.prototype.set = function(name, value) { var found = false; for (var i = 0; i < this._params.length; i++) { if (this._params[i][0] === name) { this._params[i][1] = value; found = true; break; } } if (!found) this._params.push([name, value]); };
  URLSearchParams.prototype.toString = function() { return this._params.map(function(p) { return encodeURIComponent(p[0]) + '=' + encodeURIComponent(p[1]); }).join('&'); };
  URLSearchParams.prototype.keys = function() { return this._params.map(function(p) { return p[0]; }); };
  URLSearchParams.prototype.values = function() { return this._params.map(function(p) { return p[1]; }); };
  URLSearchParams.prototype.entries = function() { return this._params.map(function(p) { return [p[0], p[1]]; }); };
  URLSearchParams.prototype.forEach = function(cb, thisArg) { for (var i = 0; i < this._params.length; i++) { cb.call(thisArg, this._params[i][1], this._params[i][0], this); } };
}

// --- Web API Polyfill: URL ---
if (typeof URL === 'undefined') {
  var URL = function(url, base) {
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
    this.searchParams = new URLSearchParams(this.search);
    Object.defineProperty(this, 'username', { get: function() { return ''; } });
    Object.defineProperty(this, 'password', { get: function() { return ''; } });
  };
  URL.prototype.toString = function() { return this.href; };
  URL.prototype.toJSON = function() { return this.href; };
}

// --- Web API Polyfill: console ---
if (typeof console === 'undefined') {
  var console = {
    log: function() { Anywhere.log.info([].slice.call(arguments).map(String).join(' ')); },
    warn: function() { Anywhere.log.warning([].slice.call(arguments).map(String).join(' ')); },
    error: function() { Anywhere.log.error([].slice.call(arguments).map(String).join(' ')); },
    info: function() { Anywhere.log.info([].slice.call(arguments).map(String).join(' ')); },
    debug: function() { Anywhere.log.debug([].slice.call(arguments).map(String).join(' ')); }
  };
}

// --- Web API Polyfill: atob / btoa ---
if (typeof atob === 'undefined') {
  var atob = function(str) { return Anywhere.codec.utf8.decode(Anywhere.codec.base64.decode(str)); };
  var btoa = function(str) { return Anywhere.codec.base64.encode(Anywhere.codec.utf8.encode(str)); };
}

// --- BoxJS Env 类 ---
function Env(name) {
  this.name = name || 'BoxJS';
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
Env.prototype.http = {
  get: function(opts) { return Anywhere.http.get(typeof opts === 'string' ? opts : opts.url, opts); },
  post: function(opts) { return Anywhere.http.post(typeof opts === 'string' ? opts : opts.url, opts); },
  put: function(opts) { return Anywhere.http.put(typeof opts === 'string' ? opts : opts.url, opts); },
  delete: function(opts) { return Anywhere.http.delete(typeof opts === 'string' ? opts : opts.url, opts); },
  request: function(opts) { return Anywhere.http.request(opts); }
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
var $env = { isBoxJS: false, isAnywhere: true };

// --- globalThis 污染隔离工具 ---
// 上游脚本（如 wloc.js）会往 globalThis 上写 $loon/$environment/$script/$argument 等全局变量，
// 污染 Anywhere 运行时环境。使用 _saveGlobals/_restoreGlobals 在执行前后隔离。
var _GLOBAL_POLLUTABLE_NAMES = ["$request", "$response", "$argument", "$persistentStore", "$done", "$loon", "$environment", "$script", "$httpClient", "$notification"];
function _saveGlobals(snapshot) {
  for (var i = 0; i < _GLOBAL_POLLUTABLE_NAMES.length; i++) {
    var name = _GLOBAL_POLLUTABLE_NAMES[i];
    snapshot[name] = globalThis[name];
  }
}
function _restoreGlobals(snapshot) {
  var keys = Object.keys(snapshot);
  for (var i = 0; i < keys.length; i++) {
    var name = keys[i];
    if (typeof snapshot[name] === "undefined") delete globalThis[name];
    else globalThis[name] = snapshot[name];
  }
}
// === BoxJS Env 兼容层 + Web API Polyfill 结束 ===
`
	return polyfill + "\n" + src
}

// rewriteDoneCalls 改写 $done 调用。
func rewriteDoneCalls(src string) string {
	// $done({}) → Anywhere.done()
	out := regexp.MustCompile(`\$done\(\s*\{\s*\}\s*\)`).ReplaceAllString(src, "Anywhere.done()")

	// $done() → Anywhere.done()
	out = regexp.MustCompile(`\$done\(\s*\)`).ReplaceAllString(out, "Anywhere.done()")

	// $done({body: x}) → ctx.body = Anywhere.codec.utf8.encode(x); Anywhere.done()
	// 因为 ctx.body 是 Uint8Array，赋值时需要将字符串编码回 Uint8Array。
	out = regexp.MustCompile(`\$done\(\s*\{\s*body\s*:\s*([^}]+?)\s*\}\s*\)`).
		ReplaceAllString(out, "ctx.body = Anywhere.codec.utf8.encode($1); Anywhere.done()")

	// $done({ body }) ES6 shorthand → ctx.body = Anywhere.codec.utf8.encode(body); Anywhere.done()
	// 注意：必须在 $done({body: x}) 之后处理，避免被错误匹配
	out = regexp.MustCompile(`\$done\(\s*\{\s*body\s*\}\s*\)`).
		ReplaceAllString(out, "ctx.body = Anywhere.codec.utf8.encode(body); Anywhere.done()")

	// $done({response: {...}}) → Anywhere.respond({...})
	// 注意：response.body 也需要 encode，但正则难以处理嵌套，保守处理
	out = regexp.MustCompile(`\$done\(\s*\{\s*response\s*:\s*(\{[^}]*\})\s*\}\s*\)`).
		ReplaceAllString(out, "Anywhere.respond($1)")

	// $done({...}) 其他形式：保守处理，转为 Anywhere.done()
	out = regexp.MustCompile(`\$done\(\s*\{[^}]*\}\s*\)`).ReplaceAllString(out, "Anywhere.done()")

	return out
}

// wrapAsProcess 将脚本包装为 function process(ctx) {...}。
// 若源码已定义 process(ctx) 则不重复包装。
// needsAsync 为 true 时使用 async function 声明（用于含 await 的脚本）。
// 当检测到上游脚本可能污染 globalThis 时（如使用了 $loon/$environment/$script 等），
// 自动添加 _saveGlobals/_restoreGlobals 隔离。
func wrapAsProcess(src string, phase int, needsAsync bool) string {
	trimmed := strings.TrimSpace(src)
	asyncKw := ""
	if needsAsync {
		asyncKw = "async "
	}

	// 检测是否需要 globalThis 隔离（上游脚本可能往 globalThis 写 $loon/$environment 等）
	needsIsolation := strings.Contains(trimmed, "$loon") ||
		strings.Contains(trimmed, "$environment") ||
		strings.Contains(trimmed, "$script") ||
		strings.Contains(trimmed, "$argument") ||
		strings.Contains(trimmed, "globalThis.$")

	isolationPrefix := ""
	isolationSuffix := ""
	if needsIsolation && strings.Contains(trimmed, "_saveGlobals") {
		// polyfill 中已提供 _saveGlobals/_restoreGlobals
		isolationPrefix = "  var _globalsSnapshot = {}; _saveGlobals(_globalsSnapshot);\n  try {\n"
		isolationSuffix = "\n  } finally { _restoreGlobals(_globalsSnapshot); }\n"
	}

	// 已有 process 函数定义（同步或异步）
	if regexp.MustCompile(`(?m)^function\s+process\s*\(\s*ctx\s*\)`).MatchString(trimmed) {
		// 若需要 async 但原函数非 async，则升级为 async
		if needsAsync && !strings.HasPrefix(trimmed, "async ") {
			trimmed = "async " + trimmed
		}
		// 如果需要隔离，在 process 函数体内添加
		if needsIsolation && isolationPrefix != "" {
			// 在函数体开头插入隔离代码
			trimmed = regexp.MustCompile(`(function\s+process\s*\(\s*ctx\s*\)\s*\{)`).
				ReplaceAllString(trimmed, "${1}\n" + isolationPrefix)
			// 在最后的 } 前插入 finally
			lastBrace := strings.LastIndex(trimmed, "}")
			if lastBrace > 0 {
				trimmed = trimmed[:lastBrace] + isolationSuffix + trimmed[lastBrace:]
			}
		}
		return trimmed
	}
	if regexp.MustCompile(`(?m)^async\s+function\s+process\s*\(\s*ctx\s*\)`).MatchString(trimmed) {
		if needsIsolation && isolationPrefix != "" {
			trimmed = regexp.MustCompile(`(async\s+function\s+process\s*\(\s*ctx\s*\)\s*\{)`).
				ReplaceAllString(trimmed, "${1}\n" + isolationPrefix)
			lastBrace := strings.LastIndex(trimmed, "}")
			if lastBrace > 0 {
				trimmed = trimmed[:lastBrace] + isolationSuffix + trimmed[lastBrace:]
			}
		}
		return trimmed
	}

	// 若源码定义了 function run()，则包装为 process 并调用 run
	if regexp.MustCompile(`(?m)^function\s+run\s*\(\s*\)`).MatchString(trimmed) {
		phaseCheck := "request"
		if phase == 1 {
			phaseCheck = "response"
		}
		return fmt.Sprintf(`%sfunction process(ctx) {
  if (ctx.phase !== "%s") return;
%s  try {
    run();
  } catch (e) {
    Anywhere.log.warning("script error: " + e);
  }%s
}

%s
`, asyncKw, phaseCheck, isolationPrefix, isolationSuffix, trimmed)
	}

	// 否则整体包装
	phaseCheck := "request"
	if phase == 1 {
		phaseCheck = "response"
	}
	return fmt.Sprintf(`%sfunction process(ctx) {
  if (ctx.phase !== "%s") return;
%s  try {
%s
  } catch (e) {
    Anywhere.log.warning("script error: " + e);
  }%s
  Anywhere.done();
}
`, asyncKw, phaseCheck, isolationPrefix, indent(trimmed, "    "), isolationSuffix)
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
func BuildWrappedScript(rawJS string, phase int) string {
	phaseCheck := "request"
	if phase == 1 {
		phaseCheck = "response"
	}
	needsAsync := strings.Contains(rawJS, "$httpClient") || strings.Contains(rawJS, "$.http") ||
		strings.Contains(rawJS, "await ") || strings.Contains(rawJS, "async ")

	upstreamB64 := base64.StdEncoding.EncodeToString([]byte(rawJS))

	asyncKw := ""
	if needsAsync {
		asyncKw = "async "
	}

	wrapper := fmt.Sprintf(`%sfunction process(ctx) {
  if (ctx.phase !== "%s") return;
  var _globalsSnapshot = {}; _saveGlobals(_globalsSnapshot);
  try {
    return await new Promise(function(resolve) {
      var settled = false;
      function finish(out) {
        if (settled) return;
        settled = true;
        resolve(out || {});
      }

      // 构造 Loon/Surge 兼容全局变量
      globalThis.$loon = {};
      globalThis.$environment = undefined;
      globalThis.$script = { startTime: new Date() };
      globalThis.$argument = '';
      globalThis.$request = {
        url: ctx.url || '',
        method: ctx.method || 'GET',
        headers: {}
      };
      globalThis.$response = {
        status: ctx.status || 200,
        statusCode: ctx.status || 200,
        headers: {},
        body: ctx.body,
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

      // HTTP 辅助函数
      function _wrapHttp(method, url, opts, cb) {
        var p;
        if (method === 'request') { p = Anywhere.http.request(opts); }
        else if (method === 'get') { p = Anywhere.http.get(typeof url === 'string' ? url : url, opts); }
        else { p = Anywhere.http[method](typeof url === 'string' ? url : url, opts); }
        p.then(function(res) {
          cb(null, { status: res.status || 200, headers: res.headers || {}, body: Anywhere.codec.utf8.decode(res.body || new Uint8Array()) }, Anywhere.codec.utf8.decode(res.body || new Uint8Array()));
        }).catch(function(e) { cb(e, null, null); });
      }

      // 解码并执行上游脚本
      try {
        var _upstreamSource = Anywhere.codec.utf8.decode(Anywhere.codec.base64.decode("%s"));
        new Function(_upstreamSource)();
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
`, asyncKw, phaseCheck, upstreamB64)

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
// stream-script 用于处理流式响应（分块传输），通过 ctx.frame 控制帧边界，ctx.state 累积数据。
//
// 包装策略：
//   - 保留原脚本逻辑，但在末尾添加帧检测：非最后一帧时累积 body 到 ctx.state，不调用 done
//   - 最后一帧时执行原逻辑
//
// 注意：此函数为尽力包装，复杂流式逻辑可能需人工调整。
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
  // 初始化跨帧状态
  if (!ctx.state.buf) ctx.state.buf = [];
  if (!ctx.state.text) ctx.state.text = "";

  // 累积当前帧 body
  ctx.state.buf.push(ctx.body);
  try {
    ctx.state.text += Anywhere.codec.utf8.decode(ctx.body);
  } catch (e) {
    Anywhere.log.warning("decode frame failed: " + e);
  }

  // 非最后一帧：保存状态后等待后续帧
  if (!ctx.frame || !ctx.frame.end) {
    return;
  }

  // 最后一帧：用累积的完整 body 执行原逻辑
  try {
    ctx.body = Anywhere.codec.utf8.encode(ctx.state.text);
%s
  } catch (e) {
    Anywhere.log.warning("stream process failed: " + e);
  }
  Anywhere.done();
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
