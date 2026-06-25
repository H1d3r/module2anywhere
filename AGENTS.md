# AGENTS.md — module2anywhere 项目指南

> 本文件供 OpenAI Codex、OpenCode、Cursor、GitHub Copilot 等 AI 编码助手识别和参考。
> 修改代码前请务必阅读本文件。

## 项目概述

module2anywhere 是一个 Loon/Surge 模块到 Anywhere 规则集的转换器。它将 Loon `.plugin` 和 Surge `.sgmodule` 模块文件转换为 Anywhere 的 `.arrs`（路由规则集）和 `.amrs`（MITM 规则集）格式。

- **Go 模块名**: `github.com/H1d3r/module2anywhere`
- **Go 版本**: 1.21
- **构建**: `make build`（普通）、`make garble`（混淆）
- **测试**: `make test`、`make vet`

## 项目结构

```
.
├── main.go              # 入口：CLI 参数解析，启动 HTTP 服务器
├── converter/
│   ├── converter.go     # 核心转换逻辑：解析模块 → 生成 .arrs/.amrs
│   ├── script.go        # 脚本 API 改写与 base64 编码（Go 侧）
│   └── urlpattern.go    # URL 模式转换（Surge/Loon → Anywhere）
├── parser/
│   ├── parser.go        # 解析器接口
│   ├── loon.go          # Loon .plugin 解析器
│   ├── surge.go         # Surge .sgmodule 解析器
│   └── quantumultx.go   # Quantumult X .conf 解析器
├── fetcher/
│   └── fetcher.go       # 远程脚本下载（支持 GitHub 代理）
├── ir/
│   └── ir.go            # 中间表示（IR）数据结构
├── server/
│   ├── server.go        # HTTP API 服务器
│   └── cache.go         # 转换结果缓存
├── edgeone/
│   ├── build.js         # 构建脚本：将 lib.js 复制到各端点文件
│   ├── functions/
│   │   ├── lib.js       # 共享库：解析器、转换器、脚本改写（EdgeOne 侧）
│   │   ├── mitm.js      # MITM 转换端点
│   │   ├── rule.js      # 路由规则转换端点
│   │   ├── convert.js   # 综合转换端点
│   │   └── ...          # 其他端点文件（由 build.js 从 lib.js 生成）
│   └── static/
│       └── index.html   # 前端页面
└── docs/
    ├── MITM.md          # Anywhere MITM 系统开发者指南（权威参考）
    └── Routing.md       # Anywhere 路由系统文档
```

## 关键架构：双端转换引擎

本项目有**两套独立的转换引擎**，必须保持同步：

1. **Go 侧** (`converter/script.go`): 用于 CLI 和 HTTP API 的脚本改写
2. **EdgeOne 侧** (`edgeone/functions/lib.js`): 用于 EdgeOne Pages Functions 的在线转换

**任何修改必须同时应用到两侧**，否则行为不一致。

### 同步检查清单

修改以下内容时，必须检查两侧是否一致：

| 功能 | Go 侧文件 | EdgeOne 侧文件 |
|------|----------|---------------|
| 脚本 API 映射 | `converter/script.go` → `RewriteScriptAPI()` | `edgeone/functions/lib.js` → `rewriteScriptAPI()` |
| $done 改写 | `converter/script.go` → `rewriteDoneCalls()` | `edgeone/functions/lib.js` → `rewriteDoneCalls()` |
| $httpClient 改写 | `converter/script.go` → `rewriteHttpClientCalls()` | `edgeone/functions/lib.js` → `rewriteHttpClientCalls()` |
| BoxJS Env polyfill | `converter/script.go` → `injectBoxJSPolyfill()` | `edgeone/functions/lib.js` → `injectBoxJSPolyfill()` |
| 包装执行模式 | `converter/script.go` → `BuildWrappedScript()` | `edgeone/functions/lib.js` → `encodeWrappedScript()` |
| process 包装 | `converter/script.go` → `wrapAsProcess()` | `edgeone/functions/lib.js` → `wrapAsProcess()` |
| needsAsync 检测 | `converter/script.go` 多处 | `edgeone/functions/lib.js` 多处 |

## 核心规范：headers 格式差异

**这是本项目最常见的 bug 来源，修改时务必注意。**

Anywhere 和 Loon/Surge 使用不同的 headers 格式：

| 上下文 | 格式 | 示例 |
|--------|------|------|
| Anywhere `ctx.headers` | `[[name, value], ...]` 数组对 | `[["Content-Type", "text/html"], ["Host", "example.com"]]` |
| Anywhere `Anywhere.http` 响应 | `[[name, value], ...]` 数组对 | 同上 |
| Anywhere `Anywhere.respond()` 参数 | `[[name, value], ...]` 数组对 | 同上 |
| Loon/Surge `$request.headers` | `{name: value}` 对象 | `{"Content-Type": "text/html", "Host": "example.com"}` |
| Loon/Surge `$httpClient` 回调 resp.headers | `{name: value}` 对象 | 同上 |
| BoxJS `Env.http` 响应 | `{name: value}` 对象 + `body: string` | 同上 |

### 转换规则

1. **改写执行模式** (`rewriteScriptAPI`): `$request.headers`/`$response.headers` → `_headersObj`（预转换变量，由 `wrapAsProcess` 注入）
2. **包装执行模式** (`encodeWrappedScript`): 构造 `$request`/`$response` 时用 `_headersToObject()` 转换
3. **`$httpClient` 回调** (`rewriteHttpClientCalls`): `buildCallbackVars` 中用内联 IIFE 转换 `res.headers`
4. **BoxJS `Env.http`** (`injectBoxJSPolyfill`): `_wrapBoxJSResponse()` 包装响应
5. **`_wrapHttp`** (`encodeWrappedScript`): 回调中用 `_headersToObject()` 转换

## 核心规范：body 格式差异

| 上下文 | 格式 | 说明 |
|--------|------|------|
| Anywhere `ctx.body` | `Uint8Array` | 二进制，只读（除赋值外） |
| Loon/Surge `$request.body`/`$response.body` | `string` | 字符串 |
| Loon `$response.bodyBytes`/`$request.bodyBytes` | `Uint8Array` | 二进制访问 |

### 转换规则

- `$request.body`/`$response.body` → `Anywhere.codec.utf8.decode(ctx.body)`（字符串）
- `$request.bodyBytes`/`$response.bodyBytes` → `ctx.body`（直接映射，都是 Uint8Array）
- **替换顺序**：必须先替换更长的标识符（`bodyBytes`），再替换短的（`body`），否则部分匹配导致语法错误
- `JSON.parse(ctx.body)` → `JSON.parse(Anywhere.codec.utf8.decode(ctx.body))`

## 核心规范：JavaScriptCore (JSCore) 注意事项

Anywhere 的脚本运行在 JSCore 上，与浏览器/Node.js 有重要差异：

1. **`var` 声明提升但赋值不提升**：`var URLSearchParams = globalThis.URLSearchParams;` 的声明提升到函数顶部，但赋值在原位置执行。因此必须在 polyfill 安装（`globalThis.URLSearchParams = function(...) {...}`）**之后**才声明 `var`，否则读到 undefined。

2. **`new Function()` 只能访问全局作用域**：`new Function(source)()` 创建的函数无法访问外层 `var` 变量。因此需要 `var XXX = globalThis.XXX;` 将全局属性映射为局部标识符。

3. **`function` 声明不会跨 `try` 块提升**：JSCore 中 `try { function foo() {} }` 的 `foo` 不会提升到外层函数作用域。因此 `_saveGlobals`/`_restoreGlobals` 等工具函数必须在 `try` 块之前定义。

## 脚本 API 映射表（完整）

| Loon/Surge API | Anywhere API | 说明 |
|----------------|-------------|------|
| `$request.url` | `ctx.url` | 请求 URL |
| `$request.method` | `ctx.method` | 请求方法 |
| `$request.headers` | `_headersObj` | 请求头（需从数组对转为对象） |
| `$request.body` | `Anywhere.codec.utf8.decode(ctx.body)` | 请求体（字符串） |
| `$request.bodyBytes` | `ctx.body` | 请求体（Uint8Array） |
| `$response.status` | `ctx.status` | 响应状态码 |
| `$response.statusCode` | `ctx.status` | 响应状态码（别名） |
| `$response.headers` | `_headersObj` | 响应头（需从数组对转为对象） |
| `$response.body` | `Anywhere.codec.utf8.decode(ctx.body)` | 响应体（字符串） |
| `$response.bodyBytes` | `ctx.body` | 响应体（Uint8Array） |
| `$done({})` | `Anywhere.done()` | 提交当前 ctx |
| `$done({body: x})` | `ctx.body = Anywhere.codec.utf8.encode(x); Anywhere.done()` | 设置 body 后提交 |
| `$done({response: {...}})` | `Anywhere.respond({status, headers, body})` | 请求阶段直接返回响应 |
| `$persistentStore.read(key)` | `Anywhere.store.getString(key, true)` | 持久化存储读取 |
| `$persistentStore.write(val, key)` | `Anywhere.store.set(key, val, true)` | 持久化存储写入 |
| `$persistentStore.write(null, key)` | `Anywhere.store.delete(key, true)` | 写入 null 时删除 |
| `$httpClient.get(url, cb)` | `await Anywhere.http.get(url).then(...)` | HTTP GET（需 async） |
| `$httpClient.post(url, opts, cb)` | `await Anywhere.http.post(url, opts).then(...)` | HTTP POST（需 async） |
| `$notification.post(title,sub,body)` | `Anywhere.log.info(...)` | 通知降级为日志 |
| `JSON.parse($response.body)` | `JSON.parse(Anywhere.codec.utf8.decode(ctx.body))` | body 需先 decode |

## 构建与测试

```bash
# Go 构建
make build

# Go 混淆构建
make garble

# Go 测试
make test

# Go 静态检查
make vet

# Go 格式化
make fmt

# EdgeOne 端点重建（修改 lib.js 后必须执行）
cd edgeone && node build.js
```

### 修改 lib.js 后的必做步骤

1. 修改 `edgeone/functions/lib.js`
2. 运行 `cd edgeone && node build.js` 重建端点文件
3. 同步修改 `converter/script.go`
4. 运行 `go build ./... && go vet ./...` 验证编译

## 代码风格

### Go

- 函数级注释（中文）
- `PascalCase` 导出函数，`camelCase` 内部函数
- `_` 前缀表示私有类成员
- 错误处理使用 `fmt.Errorf` 包装上下文

### JavaScript (EdgeOne)

- `lib.js` 是自包含文件，**不允许 import 任何模块**
- 所有共享函数通过文件末尾 `const lib = { ... }` 暴露
- `build.js` 将 lib.js 复制到每个端点文件，因此 lib.js 的修改会自动同步到所有端点
- 使用 `var` 而非 `let`/`const` 声明 polyfill 中的变量（JSCore 兼容性）

## 权威参考文档

- **README.md**: 完整的 Loon/Surge → Anywhere 映射关系（第 7 节最重要）
- **docs/MITM.md**: Anywhere MITM 系统开发者指南，定义了 `ctx` 对象和 `Anywhere` API 的精确规范
- **docs/Routing.md**: Anywhere 路由系统文档

## 常见 Bug 模式

1. **headers 格式不匹配**：忘记将 `[[name, value], ...]` 数组对转换为 `{name: value}` 对象
2. **body 格式不匹配**：忘记 `Anywhere.codec.utf8.decode(ctx.body)` 将 Uint8Array 转为字符串
3. **var 声明顺序**：在 polyfill 安装之前声明 `var XXX = globalThis.XXX`，读到 undefined
4. **替换顺序**：先替换短标识符（`$response.body`）导致长标识符（`$response.bodyBytes`）被部分匹配
5. **Go/JS 不同步**：修改了一侧忘记同步另一侧
6. **`new Function()` 作用域**：忘记 `var XXX = globalThis.XXX;` 映射全局属性为局部标识符
