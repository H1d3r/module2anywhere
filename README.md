# Loon / Surge 模块 → Anywhere 规则语法映射

本文档推导 **Loon `.plugin`** 与 **Surge `.sgmodule`** 模块到 **Anywhere `.arrs` / `.amrs`** 规则集的语法映射关系，供后续 Go 转换程序直接参考。

> **权威来源**
> - Anywhere Routing: https://github.com/NodePassProject/Anywhere/blob/main/Documentations/Routing.md
> - Anywhere MITM: https://github.com/NodePassProject/Anywhere/blob/main/Documentations/MITM.md
> - 转换实例参考: https://github.com/H1d3r/anywhere-rules、https://github.com/chikacya/anywhere-rules
> - 上游模块参考: https://github.com/kokoryh/Script

---

## 目录

1. [Anywhere 规则格式规范（目标格式）](#1-anywhere-规则格式规范目标格式)
2. [Loon 插件格式规范（源格式 A）](#2-loon-插件格式规范源格式-a)
3. [Surge 模块格式规范（源格式 B）](#3-surge-模块格式规范源格式-b)
4. [元数据映射](#4-元数据映射)
5. [路由规则映射 → `.arrs`](#5-路由规则映射--arrs)
6. [MITM 重写规则映射 → `.amrs`](#6-mitm-重写规则映射--amrs)
7. [脚本规则映射 → `.amrs` script](#7-脚本规则映射--amrs-script)
8. [URL 模式转换规则](#8-url-模式转换规则)
9. [限制与不可转换项](#9-限制与不可转换项)
10. [实例推导：Bilibili 去广告](#10-实例推导bilibili-去广告)
11. [Go 程序设计建议](#11-go-程序设计建议)

---

## 1. Anywhere 规则格式规范（目标格式）

Anywhere 有两种规则集文件，动作（DIRECT/REJECT/PROXY）在 App 内分配，不写入文件。

### 1.1 `.arrs` — 路由规则集

纯域名/IP 匹配，不含 URL 正则、不含 MITM。

```
# 注释行（# 或 //）
name = 规则集名称
<type>, <value>
```

| 类型 ID | 含义 | value 示例 | 匹配对象 |
|---------|------|-----------|---------|
| `0` | IPv4 CIDR | `10.0.0.0/8` | 目标 IP |
| `1` | IPv6 CIDR | `fe80::/10` | 目标 IP |
| `2` | Domain Suffix（标签对齐后缀） | `example.com` | 目标域名 |
| `3` | Domain Keyword（子串匹配） | `example` | 目标域名 |

**特性**：
- 后缀匹配是标签对齐的：`example.com` 匹配 `www.example.com` 但不匹配 `myexample.com`
- 裸 IP 自动补 `/32`(v4) 或 `/128`(v6)
- 单集上限 10,000 条规则
- 解析宽松：无法识别的行静默丢弃

### 1.2 `.amrs` — MITM 规则集

TLS 拦截 + HTTP 重写，支持 URL 正则、请求/响应改写、脚本。

```
# 注释行
name = 规则集名称
hostname = example.com, api.example.org
<phase>, <operation>, <url-pattern> [, <field2> [, <field3> ...]]
```

**头部字段**：

| key | 含义 |
|-----|------|
| `name` | 规则集显示名称（必填） |
| `hostname` | 逗号分隔的域名后缀，决定哪些主机被拦截 |

> 注意：官方 iOS 项目当前导入器只识别 `name` 和 `hostname` 头。历史文档/手工规则里出现过 `content-type = ...`，但当前官方 `MITMRuleSetParser` 不会保留它。转换器遇到需要固定响应 `Content-Type`、自定义响应头或非 200 状态码的 mock/map-local/echo-response 时，会生成 request 阶段 `script (op 100)` 并用 `Anywhere.respond()` 保真，而不是输出顶层 `content-type`。

**规则行格式**：`<phase>, <operation>, <url-pattern> [, fields...]`

| phase | 含义 |
|-------|------|
| `0` | 请求阶段（request） |
| `1` | 响应阶段（response） |

| op ID | 操作名 | 适用阶段 | 字段 |
|-------|--------|---------|------|
| `0` | rewrite（重写/拒绝/重定向） | 仅 request | `url-pattern, sub-mode, <sub-mode args>` |
| `1` | header-add | 两者 | `url-pattern, name, value` |
| `2` | header-delete | 两者 | `url-pattern, name` |
| `3` | header-replace | 两者 | `url-pattern, name, value` |
| `4` | body-replace | 两者 | `url-pattern, search-regex, replacement` |
| `5` | body-json | 两者 | `url-pattern, action, <action args>` |
| `100` | script | 两者 | `url-pattern, base64` |
| `101` | stream-script | 两者 | `url-pattern, base64` |

**rewrite (op 0) 子模式**：

| sub-mode | 名称 | 参数 | 效果 |
|----------|------|------|------|
| `0` | transparent | `<full-url>` | 透明替换整个请求 URL（拨号到新 host） |
| `1` | 302 redirect | `<full-url>` | 返回 302 Found，Location 为指定 URL |
| `2` | reject 200 text | `[<content>]` | 返回 200 OK，text/plain 正文为 content |
| `3` | reject 200 gif | 无 | 返回 200 OK，内嵌 1×1 GIF |
| `4` | reject 200 data | `[<base64>]` | 返回 200 OK，application/octet-stream 正文为 base64 解码 |

> rewrite 始终为 request 阶段，无论 phase 列写什么。第一条匹配的 rewrite 规则胜出。sub-mode 0 和 1 的 URL 替换**支持 `$1`-style 捕获引用**（`$0` 为整个匹配，`$1`…`$9` 为捕获组，`$$` 为字面量 `$`），无需转为脚本。

**body-json (op 5) 动作**：

| action | 后随字段 | 效果 |
|--------|---------|------|
| `add` | `path, value` | 在 path 处 upsert（创建或覆盖；数组末尾追加） |
| `replace` | `path, value` | 仅当 path 已存在时覆盖 |
| `delete` | `path` | 移除 path 处的成员/元素 |
| `replace-recursive` | `key, value` | 覆盖任意深度中名为 key 的所有属性 |
| `delete-recursive` | `key` | 移除任意深度中名为 key 的所有属性 |
| `remove-where-key-exists` | `path, key` | 在 path 数组中，丢弃含 key 的对象 |
| `remove-where-field-in` | `path, field, values` | 在 path 数组中，丢弃 field ∈ values 的对象 |

**字段引号规则**：
- 字段以 `,` 分隔，非引号字段两端空白被 trim
- 以 `"` 开头的字段读到匹配的 `"`，内部 `""` 表示字面量 `"`
- 含逗号或显著首尾空白的字段必须加引号

**脚本 (op 100/101)**：
- 字段为 base64 编码的 UTF-8 JS 源码，须定义 `function process(ctx)`
- `ctx.body` 为 `Uint8Array`，可读写
- `ctx.url` / `ctx.method` / `ctx.status` / `ctx.headers` 只读
- 控制指令：`Anywhere.done()`、`Anywhere.exit()`、`Anywhere.respond({status,headers,body})`
- API：`Anywhere.codec`（utf8/base64/hex/gzip/protobuf 等）、`Anywhere.json`、`Anywhere.http`、`Anywhere.store`、`Anywhere.log`、`Anywhere.crypto`

---

## 2. Loon 插件格式规范（源格式 A）

文件扩展名 `.plugin`，INI 风格分段。

```ini
#!name=插件名称
#!desc=描述
#!author=作者
#!icon=图标URL
#!tag=标签
#!homepage=主页URL

[Rule]
URL-REGEX,"^https?://...",REJECT-DICT
DOMAIN-SUFFIX,bilibili.com,DIRECT
IP-CIDR,192.168.0.0/16,DIRECT

[Rewrite]
^https://... reject-dict
^https://... reject-array
^https://... reject-img
^https://... reject
^https://... 302 https://new.url
^https://... 307 https://new.url
^https://... response-body-json-del data.path
^https://... response-body-json-add data.path value
^https://... response-body-json-replace data.path value
^https://... mock-response-body data-type=json data="..." status-code=200
^https://... request-header {...JS...}
^https://... response-body {...JS...}
^https://... request-body {...JS...}

[Script]
http-request ^https://... script-path=https://.../script.js,requires-body=true,tag=名称
http-response ^https://... script-path=https://.../script.js,requires-body=true,binary-body-mode=true,tag=名称

[Argument]
key = select,"opt1","opt2",tag=标签,desc=描述

[MitM]
hostname = example.com, *.example.com
```

### Loon [Rewrite] 动作清单

| 动作 | 语法 | 效果 |
|------|------|------|
| `reject` | `pattern reject` | 返回 200 空响应 |
| `reject-dict` | `pattern reject-dict` | 返回 200 + `{}` |
| `reject-array` | `pattern reject-array` | 返回 200 + `[]` |
| `reject-img` | `pattern reject-img` | 返回 200 + 1×1 GIF |
| `reject-200` | `pattern reject-200` | 返回 200 空响应 |
| `reject-data` | `pattern reject-data <base64>` | 返回 200 + base64 解码的二进制数据 |
| `302` | `pattern 302 <url>` | 302 重定向（支持 `$1` 捕获） |
| `307` | `pattern 307 <url>` | 307 重定向（支持 `$1` 捕获） |
| `transparent` | `pattern transparent <url>` | 透明 URL 重写（拨号到新 host） |
| `mock-response-body` | `pattern mock-response-body data-type=<type> data="<body>" status-code=<code>` | 模拟响应 |
| `request-header` | `pattern request-header <JS>` | JS 改写请求头 |
| `header-del` | `pattern header-del <header-name>` | 删除指定请求头（仅请求阶段） |
| `response-body` | `pattern response-body <JS>` | JS 改写响应体 |
| `request-body` | `pattern request-body <JS>` | JS 改写请求体 |
| `response-body-json-del` | `pattern response-body-json-del <dot-path>` | 删除响应 JSON 路径 |
| `response-body-json-add` | `pattern response-body-json-add <dot-path> <value>` | 添加响应 JSON 路径 |
| `response-body-json-replace` | `pattern response-body-json-replace <dot-path> <value>` | 替换响应 JSON 路径 |
| `response-body-replace-regex` | `pattern response-body-replace-regex <search-regex> <replacement>` | 用正则搜索响应 body 并替换（需引号包裹含空格的 pattern） |

### Loon [Script] 参数

| 参数 | 含义 |
|------|------|
| `script-path` | 脚本 URL |
| `requires-body` | 是否需要请求/响应体 |
| `binary-body-mode` | 二进制体模式（protobuf） |
| `tag` | 显示标签 |
| `argument` | 传入参数（`[{var}]` 格式） |
| `max-size` | 最大处理大小 |
| `engine` | 脚本引擎（Loon 特有） |

### Loon 脚本 API（与 Anywhere 不同）

```javascript
// Loon 脚本入口
function run() {
  var url = $request.url;
  var body = $response.body;     // http-response 时
  var headers = $request.headers;
  // ...处理...
  $done({ body: newBody });       // 或 $done({}) 表示不改
}
```

---

## 3. Surge 模块格式规范（源格式 B）

文件扩展名 `.sgmodule`，INI 风格分段。

```ini
#!name=模块名称
#!desc=描述
#!category=分类

[Rule]
DOMAIN-SUFFIX,bilibili.com,DIRECT
URL-REGEX,^https?://...,REJECT

[URL Rewrite]
^https://... reject
^https://... reject-dict
^https://... reject-array
^https://... reject-img
^https://... 302 https://new.url
^https://... 307 https://new.url
^https://... _response-body <JS>
^https://... _request-header <JS>

[Header Rewrite]
^https://... request-header add X-Header value
^https://... response-header replace Content-Type application/json
^https://... request-header delete Cookie

[Map Local]
^https://... data="https://.../file.json" header="Content-Type: application/json"

[Script]
name1 = type=http-response,pattern=^https://...,requires-body=1,script-path=https://.../script.js
name2 = type=http-request,pattern=^https://...,requires-body=1,script-path=https://.../script.js

[MITM]
hostname = %APPEND% example.com, api.example.com
```

### Surge [URL Rewrite] 动作清单

| 动作 | 语法 | 效果 |
|------|------|------|
| `reject` | `pattern reject` | 拒绝（返回空响应） |
| `reject-dict` | `pattern reject-dict` | 拒绝 + `{}` |
| `reject-array` | `pattern reject-array` | 拒绝 + `[]` |
| `reject-img` | `pattern reject-img` | 拒绝 + GIF |
| `reject-200` | `pattern reject-200` | 200 空响应 |
| `302` | `pattern 302 <url>` | 302 重定向（支持 `$1`） |
| `307` | `pattern 307 <url>` | 307 重定向（支持 `$1`） |
| transparent rewrite | `pattern <new-url>` | 透明替换 URL（无动作前缀，直接写目标 URL） |
| `_request-header` | `pattern _request-header <JS>` | JS 改写请求头 |
| `_request-body` | `pattern _request-body <JS>` | JS 改写请求体 |
| `_response-body` | `pattern _response-body <JS>` | JS 改写响应体 |

### Surge [Header Rewrite] 操作

| 操作 | 语法 |
|------|------|
| add | `pattern request-header add <name> <value>` |
| replace | `pattern request-header replace <name> <value>` |
| delete | `pattern request-header delete <name>` |
| add | `pattern response-header add <name> <value>` |
| replace | `pattern response-header replace <name> <value>` |
| delete | `pattern response-header delete <name>` |

### Surge [Map Local]

```
^https://pattern data="<file-url>" header="<Header: value>"
```
用本地/远程文件内容模拟响应。

### Surge [Script] 参数

| 参数 | 含义 |
|------|------|
| `type` | `http-request` 或 `http-response` |
| `pattern` | URL 正则 |
| `requires-body` | `1` 需要 body，`0` 不需要 |
| `script-path` | 脚本 URL |
| `max-size` | 最大处理大小 |
| `binary-body-mode` | 二进制模式 |
| `engine` | 脚本引擎 |

### Surge 脚本 API（与 Anywhere 不同）

```javascript
// Surge 脚本入口
function run() {
  var url = $request.url;
  var body = $response.body;
  // ...处理...
  $done({ body: newBody });
}
```

---

## 4. 元数据映射

| 源字段 (Loon/Surge) | 目标字段 (.amrs/.arrs) | 转换说明 |
|---------------------|----------------------|---------|
| `#!name=xxx` | `name = xxx` | 直接映射 |
| `#!desc=xxx` | `# desc: xxx` | 转为注释 |
| `#!author=xxx` | `# author: xxx` | 转为注释 |
| `#!homepage=xxx` | `# homepage: xxx` | 转为注释 |
| `#!icon=xxx` | 丢弃 | Anywhere 规则集无图标字段 |
| `#!tag=xxx` | 丢弃 | — |
| `#!category=xxx` | 丢弃 | — |
| `#!system=xxx` | 丢弃 | — |
| `#!loon_version=xxx` | 丢弃 | — |
| `#!date=xxx` | `# date: xxx` | 转为注释 |

### hostname 映射

| 源 | 目标 | 转换说明 |
|----|------|---------|
| Loon `[MitM] hostname = a.com, *.b.com` | `hostname = a.com, b.com` | 去除 `*.` 前缀（Anywhere 用 base domain 已覆盖所有子域名） |
| Surge `[MITM] hostname = %APPEND% a.com, b.com` | `hostname = a.com, b.com` | 去除 `%APPEND%` 前缀 |
| Surge `[MITM] hostname = a.com, b.com` | `hostname = a.com, b.com` | 直接映射 |

### 4.1 hostname 通配符展开（重要）

Loon/Surge 的 hostname 字段支持 `*` 和 `?` 通配符，而 Anywhere **不支持通配符**，使用 **label-aligned suffix** 匹配。转换时需要将通配符展开为具体的域名条目。

#### Anywhere label-aligned suffix 匹配规则

 Anywhere hostname 的 suffix 匹配是**标签对齐**的：

- `example.com` → 匹配 `www.example.com`（因为 `example.com` 是 `www.example.com` 的后缀标签）
- `api.bilibili.com` → **只匹配** `api.bilibili.com`，**不匹配** `api01.bilibili.com`（因为 `api01` 整体是一个标签，`api` 不是它的后缀标签）
- `bilibili.com` → 匹配 `api.bilibili.com`、`app.bilibili.com`、`api01.bilibili.com` 等所有 bilibili.com 的子域名

#### `*` 通配符展开

`*` 匹配零个或多个字符（可以跨越标签边界）。

| Loon/Surge 写法 | 展开目标 | 转换结果 |
|-----------------|---------|---------|
| `*bilibili.com` | 覆盖所有 bilibili.com 子域名 | `bilibili.com` |
| `*.bilibili.com` | 同上（`*` 匹配任意前缀） | `bilibili.com` |
| `api.*.bilibili.com` | `api` 后接任意内容再接 `.bilibili.com` | 展开为多个常见子域名 |
| `*live.bilibili.com` | `live.bilibili.com` 及其所有子域名 | `live.bilibili.com, www.live.bilibili.com` |

**结论**：`*.xxx.com` / `*xxx.com` → 直接用 base domain `xxx.com`。这是因为 Anywhere 的 suffix 匹配中，写 `xxx.com` 就已经覆盖了所有 `xxx.com` 的子域名。

#### `?` 通配符展开

`?` 匹配**单个任意字符**（不跨越标签边界，即每个 `?` 在一个标签内）。

| Loon/Surge 写法 | 展开目标 | 转换结果 |
|-----------------|---------|---------|
| `api?.bilibili.com` | `api0`~`api9`、`apiA`~`apiZ` 等单个字符变体 | `api0.bilibili.com, api1.bilibili.com, ..., api9.bilibili.com` |
| `app?.bilibili.com` | `app1`~`app9` 等 | `app1.bilibili.com, app2.bilibili.com, ..., app9.bilibili.com` |
| `ap?.bilibili.com` | `ap0`~`ap9` | `ap0.bilibili.com, ap1.bilibili.com, ..., ap9.bilibili.com` |

**常见 `?` 展开的字符集**：数字 `0-9`、字母 `a-z`（实际应用中字母较少见）

**实际案例**（来自 bilibili.plugin）：

```
源 Loon: hostname=ap?.bilibili.com
转换: hostname=api.bilibili.com, app.bilibili.com
```

> **为什么 `ap?` 只展开为 `api` 和 `app`？**
> 在 bilibili 的实际场景中，`?` 最常用于匹配数字后缀（`api0`~`api9`）和区分不同域名变体（`api`/`app`）。由于无法预知远程服务器具体使用了哪些域名，最安全的方案是：
> 1. 尝试常见变体（`api`、`app`、`ap0`~`ap9`）
> 2. 如不确定，可直接用 base domain `bilibili.com` 覆盖全部
> 3. 保留原始 `?` 模式作为注释，便于用户后续调整

#### 转换策略总结

| 源通配符 | 转换策略 | 示例 |
|---------|---------|------|
| `*.domain.com` / `*domain.com` | 展开为 base domain | `*bilibili.com` → `bilibili.com` |
| `*sub.domain.com` | 展开为 base + 常见子域名 | `*live.bilibili.com` → `live.bilibili.com, www.live.bilibili.com` |
| `prefix?.domain.com` | 展开为 `prefix0`~`prefix9` | `api?.bilibili.com` → `api0.bilibili.com, ..., api9.bilibili.com` |
| 字符类不确定 | 用 base domain 覆盖 | `ap?.bilibili.com` → `bilibili.com` |

#### 不兼容的替代方案（脚本兜底）

如果展开后的 hostname 仍然无法完全覆盖原始意图，可以：

1. **扩展 hostname**：将 base domain 加入（如 `bilibili.com`），确保所有相关子域名都被拦截
2. **脚本兜底**：对于 hostname 未覆盖到的域名，如果仍有重写规则需要生效，可在脚本中添加域名检查：

```javascript
// 如果原始 pattern 包含不在 hostname 中的域名，在此做额外检查
// 但注意：Anywhere 的 hostname 拦截在 TLS 层完成，
// 脚本无法处理未进入 MITM 拦截的流量
function process(ctx) {
    // ctx.url 在这里可用，但域名本身应在 hostname 中声明
    // 脚本层无法补获 hostname 未拦截的域名
}
```

> **限制**：Anywhere 的 hostname 拦截发生在 TLS 层，在脚本之前。如果某个域名不在 hostname 字段中，流量根本不会进入 MITM 流程，脚本无法处理。所以 **hostname 必须尽可能完整展开**。

---

## 5. 路由规则映射 → `.arrs`

Loon `[Rule]` / Surge `[Rule]` 中**基于域名和 IP** 的规则可转为 `.arrs`。
**注意**：动作（DIRECT/REJECT/PROXY）不写入文件，需在 App 内分配。

| Loon/Surge 规则类型 | 示例 | Anywhere type | 转换结果 | 说明 |
|---------------------|------|--------------|---------|------|
| `DOMAIN-SUFFIX` | `DOMAIN-SUFFIX,bilibili.com,DIRECT` | `2` | `2, bilibili.com` | 直接映射 |
| `DOMAIN-KEYWORD` | `DOMAIN-KEYWORD,bilibili,DIRECT` | `3` | `3, bilibili` | 直接映射 |
| `DOMAIN` | `DOMAIN,api.bilibili.com,DIRECT` | `2` | `2, api.bilibili.com` | 精确域名按后缀处理 |
| `DOMAIN-SET` | `DOMAIN-SET,xxx.list,DIRECT` | — | 逐行展开 | 需读取远程列表文件，逐条转换 |
| `IP-CIDR` | `IP-CIDR,192.168.0.0/16,DIRECT,no-resolve` | `0` | `0, 192.168.0.0/16` | 去除 `no-resolve` 等选项 |
| `IP-CIDR6` | `IP-CIDR6,fe80::/10,DIRECT` | `1` | `1, fe80::/10` | 直接映射 |
| `GEOIP` | `GEOIP,CN,DIRECT` | — | 不可转换 | Anywhere 无 GeoIP 规则类型 |
| `URL-REGEX` (REJECT 类) | `URL-REGEX,"^http://ad...",REJECT-DICT` | — | → `.amrs` | 需 URL 匹配的拒绝应转入 MITM 规则集 |
| `URL-REGEX` (非 REJECT) | `URL-REGEX,"^http://...",PROXY` | — | 不可转换 | Anywhere 路由不支持 URL 正则 |
| `PROCESS-NAME` | `PROCESS-NAME,xxx,DIRECT` | — | 不可转换 | Anywhere 无进程匹配 |
| `DEST-PORT` | `DEST-PORT,443,DIRECT` | — | 不可转换 | Anywhere 无端口匹配 |
| `SRC-IP` | `SRC-IP,xxx,DIRECT` | — | 不可转换 | Anywhere 无源 IP 匹配 |

### REJECT 类 URL-REGEX 的特殊处理

Loon/Surge 中 `URL-REGEX,"pattern",REJECT-DICT` 这类规则需要匹配 URL 而非域名，应转入 `.amrs` 文件作为 rewrite reject 规则：

| 源规则 | 目标 (.amrs) |
|--------|-------------|
| `URL-REGEX,"^http://ad\.example\.com/",REJECT` | `0, 0, ^http://ad\.example\.com/, 2` |
| `URL-REGEX,"^http://ad\.example\.com/",REJECT-DICT` | `0, 0, ^http://ad\.example\.com/, 2, {}` |
| `URL-REGEX,"^http://ad\.example\.com/",REJECT-ARRAY` | `0, 0, ^http://ad\.example\.com/, 2, []` |
| `URL-REGEX,"^http://ad\.example\.com/",REJECT-IMG` | `0, 0, ^http://ad\.example\.com/, 3` |

---

## 6. MITM 重写规则映射 → `.amrs`

Loon `[Rewrite]` / Surge `[URL Rewrite]` 中的重写规则转为 `.amrs`。

### 6.1 拒绝类

| Loon/Surge 动作 | Anywhere 操作 | 转换结果 | 说明 |
|----------------|--------------|---------|------|
| `reject` | rewrite sub-mode 2 | `0, 0, pattern, 2` | 200 text/plain 空内容 |
| `reject-200` | rewrite sub-mode 2 | `0, 0, pattern, 2` | 同上 |
| `reject-dict` | rewrite sub-mode 2 | `0, 0, pattern, 2, {}` | 200 + `{}` |
| `reject-array` | rewrite sub-mode 2 | `0, 0, pattern, 2, []` | 200 + `[]` |
| `reject-img` | rewrite sub-mode 3 | `0, 0, pattern, 3` | 200 + 内置 1×1 GIF |
| `reject-data` | rewrite sub-mode 4 | `0, 0, pattern, 4, <base64>` | 200 + application/octet-stream（base64 解码后正文） |

### 6.2 重定向类

| Loon/Surge 动作 | Anywhere 操作 | 转换结果 | 说明 |
|----------------|--------------|---------|------|
| `302 <url>` | rewrite sub-mode 1 | `0, 0, pattern, 1, <url>` | 302 重定向（Anywhere 原生支持 `$1` 捕获引用） |
| `307 <url>` | rewrite sub-mode 1 | `0, 0, pattern, 1, <url>` | **近似**：Anywhere 仅支持 302，307 降级为 302 |
| `302 $1` (带捕获) | rewrite sub-mode 1 | `0, 0, pattern, 1, <url>` | Anywhere 原生支持 `$1`/`$2` 捕获引用，无需脚本 |
| `307 $1` (带捕获) | rewrite sub-mode 1 | `0, 0, pattern, 1, <url>` | 同上（307 降级为 302） |
| `transparent <url>` | rewrite sub-mode 0 | `0, 0, pattern, 0, <url>` | 透明替换整个请求 URL（拨号到新 host） |
| `transparent $1` (带捕获) | rewrite sub-mode 0 | `0, 0, pattern, 0, <url>` | Anywhere 原生支持 `$1`/`$2` 捕获引用，无需脚本 |

> **重要说明**：Anywhere rewrite sub-mode 0 和 1 **原生支持** `$1`-style 捕获引用（`$0` 为整个匹配，`$1`…`$9` 为捕获组，`$$` 为字面量 `$`）。因此带 `$1` 的重定向和透明重写**无需转为脚本**，可直接输出。这与早期理解不同——Anywhere 的 rewrite URL 替换**不是**纯字面量，而是支持捕获组展开的。

### 6.3 模拟响应类

| Loon/Surge 动作 | Anywhere 操作 | 转换结果 | 说明 |
|----------------|--------------|---------|------|
| `mock-response-body data-type=json data="<body>" status-code=200` | rewrite sub-mode 2 | `0, 0, pattern, 2, "<body>"` | status-code 固定为 200（sub-mode 2 即 200） |
| Surge `[Map Local] data="<file-url>"` | rewrite sub-mode 2 或 script | `0, 0, pattern, 2, "<body>"` | 需先拉取 file-url 内容嵌入；或用脚本 + `Anywhere.http` 动态获取 |

### 6.4 JSON 体重写类

| Loon 动作 | Anywhere 操作 | 转换结果 | 说明 |
|----------|--------------|---------|------|
| `response-body-json-del data.path` | body-json delete | `1, 5, pattern, delete, $.data.path` | dot-path → JSONPath（加 `$.` 前缀） |
| `response-body-json-add data.path value` | body-json add | `1, 5, pattern, add, $.data.path, value` | — |
| `response-body-json-replace data.path value` | body-json replace | `1, 5, pattern, replace, $.data.path, value` | — |
| `response-body-json-delete-recursive key` | body-json delete-recursive | `1, 5, pattern, delete-recursive, key` | 移除任意深度中名为 key 的所有属性 |
| `response-body-json-replace-recursive key value` | body-json replace-recursive | `1, 5, pattern, replace-recursive, key, value` | 覆盖任意深度中名为 key 的所有属性 |
| `response-body-json-remove-where-key-exists data.path key` | body-json remove-where-key-exists | `1, 5, pattern, remove-where-key-exists, $.data.path, key` | 在 path 数组中，丢弃含 key 的对象 |
| `response-body-json-remove-where-field-in data.path field values` | body-json remove-where-field-in | `1, 5, pattern, remove-where-field-in, $.data.path, field, values` | 在 path 数组中，丢弃 field ∈ values 的对象 |

> **dot-path → JSONPath 转换**：`data.common_equip` → `$.data.common_equip`；`data.items.0.id` → `$.data.items[0].id`

### 6.5 头部重写类

**Loon `[Rewrite]` header 操作**：

| Loon 动作 | Anywhere 操作 | 转换结果 | 说明 |
|----------|--------------|---------|------|
| `pattern header-del <name>` | header-delete (phase 0) | `0, 2, pattern, name` | 删除请求头 |
| Surge `_header-del <name>` | header-delete (phase 0) | `0, 2, pattern, name` | 同上（Surge 用 `_` 前缀区分 rewrite 和 header 操作） |

> 注意：Surge 的 `_header-del` 等属于 `[URL Rewrite]` 段，而非 `[Header Rewrite]` 段。二者在 Surge 中行为一致——`_header-del` 是简写，行为等同于 `[Header Rewrite] request-header delete <name>`。

**Surge `[Header Rewrite]`**：

| Surge `[Header Rewrite]` | Anywhere 操作 | 转换结果 | 说明 |
|-------------------------|--------------|---------|------|
| `pattern request-header add <name> <value>` | header-add (phase 0) | `0, 1, pattern, name, value` | — |
| `pattern request-header replace <name> <value>` | header-replace (phase 0) | `0, 3, pattern, name, value` | — |
| `pattern request-header delete <name>` | header-delete (phase 0) | `0, 2, pattern, name` | — |
| `pattern response-header add <name> <value>` | header-add (phase 1) | `1, 1, pattern, name, value` | — |
| `pattern response-header replace <name> <value>` | header-replace (phase 1) | `1, 3, pattern, name, value` | — |
| `pattern response-header delete <name>` | header-delete (phase 1) | `1, 2, pattern, name` | — |

### 6.6 JS 体重写类（request-header / request-body / response-body）

Loon `request-header <JS>` / `response-body <JS>` / `request-body <JS>` 和 Surge `_request-header <JS>` / `_response-body <JS>` / `_request-body <JS>` 使用内联 JS。

| 源动作 | Anywhere 操作 | phase | 说明 |
|--------|--------------|-------|------|
| `request-header <JS>` | script (op 100) | `0` | JS 需改写为 `process(ctx)` 形式 |
| `request-body <JS>` | script (op 100) | `0` | 同上 |
| `response-body <JS>` | script (op 100) | `1` | 同上 |

> **注意**：这些内联 JS 使用 Loon/Surge 的 `$request`/`$response`/`$done` API，需改写为 Anywhere 的 `ctx`/`Anywhere.done()` API（见第 7 节）。

### 6.7 正则替换响应体

| Loon 动作 | Anywhere 操作 | 转换结果 | 说明 |
|----------|--------------|---------|------|
| `response-body-replace-regex <search> <replacement>` | body-replace (op 4) | `1, 4, pattern, search, replacement` | 在响应 body 中搜索正则并替换；phase 始终为 1（response） |

> **search/replacement 格式**：正则中的引号需双写：`"pattern"` → `""pattern""`；整个字段若含逗号或含空白首尾须加引号。
> **示例**：`response-body-replace-regex "list":\[.+\] "list":[]` → `1, 4, ^https://..., ""list"":\[.+\], ""list"":[]`

---

## 7. 脚本规则映射 → `.amrs` script

### 7.1 规则行映射

| 源 (Loon [Script]) | 源 (Surge [Script]) | Anywhere | 说明 |
|---------------------|---------------------|----------|------|
| `http-request <pattern> script-path=<url>,requires-body=true` | `name = type=http-request,pattern=<regex>,requires-body=1,script-path=<url>` | `0, 100, <pattern>, <base64>` | 请求阶段脚本 |
| `http-response <pattern> script-path=<url>,requires-body=true` | `name = type=http-response,pattern=<regex>,requires-body=1,script-path=<url>` | `1, 100, <pattern>, <base64>` | 响应阶段脚本 |
| `http-response ... binary-body-mode=true` | `... binary-body-mode=1` | `1, 100, <pattern>, <base64>` | 二进制模式：Anywhere 中 `ctx.body` 始终为 `Uint8Array`，无需特殊标记 |

### 7.2 脚本获取与编码

转换步骤：
1. 从 `script-path` URL 下载 JS 源码
2. 将 Loon/Surge API 改写为 Anywhere API（见下表）
3. Base64 编码（UTF-8）
4. 嵌入 `.amrs` 规则行

### 7.3 脚本 API 映射

| Loon/Surge API | Anywhere API | 说明 |
|----------------|-------------|------|
| `$request.url` | `ctx.url` | 请求 URL |
| `$request.method` | `ctx.method` | 请求方法 |
| `$request.headers` | `_headersObj`（`{name:value}` 对象，由 `wrapAsProcess` 预转换） | 请求头（保持 Loon/Surge 对象格式） |
| `$request.body` | `Anywhere.codec.utf8.decode(ctx.body)` | 请求体（字符串，phase=0） |
| `$request.bodyBytes` | `ctx.body`（`Uint8Array`） | 请求体二进制 |
| `$response.status` | `ctx.status` | 响应状态码 |
| `$response.statusCode` | `ctx.status` | 响应状态码别名 |
| `$response.headers` | `_headersObj`（`{name:value}` 对象，由 `wrapAsProcess` 预转换） | 响应头（保持 Loon/Surge 对象格式） |
| `$response.body` | `Anywhere.codec.utf8.decode(ctx.body)` | 响应体（字符串，phase=1） |
| `$response.bodyBytes` | `ctx.body`（`Uint8Array`） | 响应体二进制 |
| `$done({})` | `Anywhere.done()` | 提交当前 ctx，跳过后续规则 |
| `$done({body: x})` | `ctx.body = x; Anywhere.done()` | 设置 body 后提交 |
| `$done({response: {...}})` | `Anywhere.respond({status, headers, body})` | 请求阶段直接返回响应 |
| `$done({response: {headers}})` | `headers: [[name,value],...]` | 响应头保持数组对格式 |
| `$persistentStore.read(key)` | `Anywhere.store.getString(key, true)` | 持久化存储读取 |
| `$persistentStore.write(val, key)` | `Anywhere.store.set(key, val, true)` | 持久化存储写入 |
| `$persistentStore.write(null, key)` | `Anywhere.store.delete(key, true)` | 写入 null 时删除存储（Surge/Loon 语义） |
| `$httpClient.get(url, cb)` | `await Anywhere.http.get(url)` | HTTP GET（需 async） |
| `$httpClient.post(url, opts, cb)` | `await Anywhere.http.post(url, opts)` | HTTP POST（需 async） |
| `$httpClient.request(opts, cb)` | `await Anywhere.http.request(opts)` | 通用 HTTP 请求（需 async） |
| `$notification.post(title,sub,body)` | `Anywhere.log.info(...)` | Anywhere 无通知，降级为日志 |
| `JSON.parse($response.body)` | `JSON.parse(Anywhere.codec.utf8.decode(ctx.body))` | body 需先 decode |
| `body = JSON.stringify(obj)` | `ctx.body = Anywhere.codec.utf8.encode(JSON.stringify(obj))` | body 需 encode |

#### 7.3.1 BoxJS Env 类兼容

大量 Surge/Loon/QX 脚本使用 BoxJS 的 `Env` 类（`const $ = new Env('name')`），通过 `$.getdata`/`$.setdata`/`$.msg` 等 API 与 BoxJS 交互。Anywhere 没有内置 Env 类，转换器会自动检测并注入一个轻量 **Env polyfill**，将这些调用映射到 Anywhere 原生 API：

| BoxJS Env API | Anywhere 映射 | 说明 |
|---------------|-------------|------|
| `$.getdata(key)` | `Anywhere.store.getString(key, true)` | 持久化存储读取 |
| `$.setdata(val, key)` | `Anywhere.store.set(key, String(val), true)` | 持久化存储写入 |
| `$.setdata(null, key)` | `Anywhere.store.delete(key, true)` | 写入 null 时删除存储（BoxJS 语义） |
| `$.getjson(key, default)` | `JSON.parse($.getdata(key))` | JSON 持久化读取 |
| `$.setjson(val, key)` | `$.setdata(JSON.stringify(val), key)` | JSON 持久化写入 |
| `$.msg(title, subtitle, body)` | `Anywhere.log.info(...)` | 通知降级为日志 |
| `$.log(msg)` | `Anywhere.log.info(msg)` | 日志输出 |
| `$.logErr(msg)` | `Anywhere.log.warning(msg)` | 错误日志 |
| `$.http.get/post/put/delete(opts)` | `Anywhere.http.get/post/put/delete(...)` | HTTP 请求 |
| `$.http.request(opts)` | `Anywhere.http.request(...)` | 通用 HTTP 请求 |
| `$.isQuanX()` / `$.isSurge()` / `$.isLoon()` | `return false` | 环境检测（Anywhere 非 QX/Surge/Loon） |
| `$.wait(ms)` | `new Promise(resolve => setTimeout(resolve, ms))` | 延时 |
| `$.done()` | `Anywhere.done()` | 完成 |
| `$env` | `{ isBoxJS: false, isAnywhere: true }` | QX 环境变量兼容 |

> 说明：`$.msg` 在 Anywhere 中降级为日志输出，不弹系统通知；`$.http` 返回的 `headers` 为对象，和 BoxJS 页面上常见的数组对格式不同。
> 兼容别名：已补齐 `$.fetch` / `$.request` / `$.notify` / `$.runScript` / `$.toURL` / `$.setvalue` / `$.getvalue`，以及 `fetch` / `TextEncoder` / `TextDecoder` / `Headers` / `Request` / `Response` / 定时器等常见 Web API。

#### 7.3.2 Web API Polyfill

Anywhere 的 JavaScriptCore 运行时不提供浏览器环境 API，BoxJS 脚本常用的以下 Web API 会触发 `ReferenceError`。转换器在检测到使用时自动注入 polyfill：

| Web API | Polyfill 实现 | 说明 |
|---------|-------------|------|
| `URLSearchParams` | 纯 JS 实现，支持 `get/set/has/append/delete/toString/forEach` 等方法 | 解析 URL 查询参数 |
| `URL` | 基于 `String.match()` 的轻量实现，支持 `protocol/hostname/port/pathname/search/hash/searchParams` | URL 解析构造 |
| `console.log/warn/error/info/debug` | 映射到 `Anywhere.log.info/warning/error/info/debug` | 日志输出 |
| `atob(str)` / `btoa(str)` | 基于 `Anywhere.codec.base64` + `Anywhere.codec.utf8` | Base64 编解码 |
| `TextEncoder` / `TextDecoder` | 纯 JS 轻量实现 | UTF-8 编解码 |
| `fetch` | 基于 `Anywhere.http.request` 的轻量封装 | 常见浏览器式请求 |
| `Headers` / `Request` / `Response` | 轻量实现 | 为 `fetch` / Web API 脚本提供最小兼容 |
| `setTimeout` / `setInterval` / `clearTimeout` / `clearInterval` | 运行时原生或兼容实现 | 定时器 |

> **检测逻辑**：转换器自动检测脚本中是否包含 `new Env(`、`$.getdata`/`$.setdata`、`URLSearchParams`、`new URL(`、`console.log`、`fetch`、`TextEncoder`、`TextDecoder` 等特征，仅在检测到时注入 polyfill，不影响不使用这些 API 的脚本。所有 polyfill 均使用 `if (typeof XXX === 'undefined')` 守卫，不会覆盖 Anywhere 运行时已有的原生实现。

#### 7.3.3 globalThis 污染隔离

上游脚本（如 wloc.js 等跨平台脚本）会往 `globalThis` 上写入 `$loon`、`$environment`、`$script`、`$argument` 等全局变量，污染 Anywhere 运行时环境，导致后续规则执行异常。转换器在检测到这些全局变量使用时，自动在 `process(ctx)` 函数体内注入 `_saveGlobals`/`_restoreGlobals` 隔离代码：

```javascript
// 自动注入的隔离代码（示例）
async function process(ctx) {
  var _globalsSnapshot = {}; _saveGlobals(_globalsSnapshot);
  try {
    // ... 上游脚本逻辑 ...
  } finally { _restoreGlobals(_globalsSnapshot); }
}
```

> **检测逻辑**：当脚本中包含 `$loon`、`$environment`、`$script`、`$argument`、`globalThis.$` 等特征时自动启用隔离。隔离工具函数由 BoxJS Env polyfill 一并注入。

**Anywhere.http.request 完整参数**（用于高级代理重写）：

```javascript
await Anywhere.http.request({
    url: string,           // 必填：目标 URL
    method: string,        // 可选，默认 "GET"
    headers: [[name,value],...], // 可选，发送的请求头
    body: Uint8Array,       // 可选，请求体
    timeout: number,        // 可选，超时 ms（默认 8000）
    redirect: "follow" | "manual" | "error"  // 可选，是否跟随重定向
})
// 返回 {status, headers: [[name,value],...], body: Uint8Array, url: string}
```

**Anywhere.respond 完整参数**（用于合成响应或代理上游）：

```javascript
Anywhere.respond({
    status: number,         // 必填：HTTP 状态码
    headers: [[name,value],...], // 可选，响应头
    body: Uint8Array        // 可选，响应体
})
// 请求阶段直接返回，不走上游
```

> `ctx.headers`、`Anywhere.respond.headers`、`Anywhere.http` 返回的 `headers` 都统一使用 `[[name, value], ...]` 数组对格式；只有 BoxJS/Surge/Loon 脚本内部访问时，才会在兼容层里临时转为对象。

**Anywhere.json 静态函数**（bytes-in/bytes-out，无需 decode/encode）：

```javascript
Anywhere.json.add(body: Uint8Array, path: string, value: any): Uint8Array
Anywhere.json.replace(body: Uint8Array, path: string, value: any): Uint8Array
Anywhere.json.delete(body: Uint8Array, path: string): Uint8Array
Anywhere.json.replaceRecursive(body: Uint8Array, key: string, value: any): Uint8Array
Anywhere.json.deleteRecursive(body: Uint8Array, key: string): Uint8Array
// path 为 JSONPath 格式，如 $.data.user.is_premium
// 与 body-json (op 5) 等价，但可在脚本中灵活调用
```

**ctx.state 跨帧持久化**（用于 stream-script 累积数据）：

```javascript
ctx.state        // 对象，在多次调用间共享状态
// 示例：
if (!ctx.state.buf) ctx.state.buf = [];
ctx.state.buf.push(ctx.body);
// 在 frame.end=true 时处理累积数据
```

### 7.4 脚本改写模板

**Loon/Surge 原始脚本**（简单 body 修改）：

```javascript
function run() {
  var body = $response.body;
  var obj = JSON.parse(body);
  obj.ad_removed = true;
  $done({ body: JSON.stringify(obj) });
}
```

**Anywhere 改写后（同步版）**：

```javascript
function process(ctx) {
  if (ctx.phase !== "response" || !ctx.body) return;
  try {
    var obj = JSON.parse(Anywhere.codec.utf8.decode(ctx.body));
    obj.ad_removed = true;
    ctx.body = Anywhere.codec.utf8.encode(JSON.stringify(obj));
  } catch (e) {
    Anywhere.log.warning("parse failed: " + e);
  }
  Anywhere.done();
}
```

**Anywhere 改写后（async/await 版，用于网络请求）**：

> 当脚本需要向上游发请求（如 URL 替换、代理重写）时，必须使用 `async` 函数 + `await Anywhere.http.request()` + `Anywhere.respond()`。

```javascript
async function process(ctx) {
  if (ctx.phase !== "request" || !ctx.url) return;
  // 过滤掉不可转发的头部（Host, Content-Length, Connection 等）
  var headers = [];
  (ctx.headers || []).forEach(function(h) {
    var name = String(h[0] || "");
    var lower = name.toLowerCase();
    if (!name || lower === "host" || lower === "content-length" || lower === "connection") return;
    headers.push([name, String(h[1] || "")]);
  });
  try {
    // 向上游发请求（如修改 URL）
    var modifiedUrl = ctx.url.replace(/(?:[?&]?)platform=iphone/, "&platform=ipad");
    var res = await Anywhere.http.request({
      url: modifiedUrl,
      method: ctx.method || "GET",
      headers: headers,
      timeout: 8000,
      redirect: "follow"
    });
    // 用上游响应替代原始响应
    Anywhere.respond({
      status: res.status || 200,
      headers: res.headers || [],
      body: res.body || new Uint8Array()
    });
  } catch (e) {
    Anywhere.log.warning("proxy failed: " + e);
  }
}
```

**Anywhere json 静态函数改写模板**：

```javascript
function process(ctx) {
  if (ctx.phase !== "response" || !ctx.body) return;
  try {
    // 直接操作 body，无需手动 decode/encode
    ctx.body = Anywhere.json.replace(ctx.body, "$.user.is_premium", true);
    ctx.body = Anywhere.json.delete(ctx.body, "$.data.ads");
  } catch (e) {
    Anywhere.log.warning("json failed: " + e);
  }
  Anywhere.done();
}
```

### 7.5 stream-script 映射 → op 101

`stream-script` 用于处理流式响应（分块传输），通过 `ctx.frame` 控制帧边界，`ctx.state` 累积数据。

| 源类型 | Anywhere op | phase | 说明 |
|--------|------------|-------|------|
| 流式处理脚本（Surge/Loon 不直接支持） | `stream-script` (101) | 0 或 1 | 用于处理大文件、分块响应 |

**stream-script ctx 额外字段**：

```javascript
ctx.frame      // {index: number, end: boolean}
// index: 当前帧序号（从 0 开始）
// end: 当前帧是否为最后一帧

ctx.state      // {}，跨帧共享状态对象
// 用于在多帧间累积数据
```

**stream-script 改写模板（以京东价格比对为例）**：

```javascript
async function process(ctx) {
  if (ctx.phase !== "response" || !ctx.body) return;
  // 初始化累积状态
  if (!ctx.state.buf) ctx.state.buf = [];
  if (!ctx.state.cb) ctx.state.cb = "";

  // 拼接 body
  ctx.state.cb += Anywhere.codec.utf8.decode(ctx.body);

  // 非最后一帧：保存状态后继续
  if (!ctx.frame.end) {
    ctx.state.buf.push(ctx.body);
    return; // 不调用 done，等待后续帧
  }

  // 最后一帧：查找 JSON 中的价格字段并注入
  var m = ctx.state.cb.match(/"lowestPrice":"(\d+)"/);
  if (m) {
    var targetPrice = parseInt(m[1]) - 1;
    var newBody = ctx.state.cb.replace(
      /"lowestPrice":"\d+"/g,
      '"lowestPrice":"' + targetPrice + '"'
    );
    ctx.body = Anywhere.codec.utf8.encode(newBody);
  }
  Anywhere.done();
}
```

> **何时用 script (100) vs stream-script (101)**：
> - `100`：整个 body 可一次性获取，或只需最终处理结果。多数场景用此。
> - `101`：body 分块传输（如大文件、分段 JSON 流），需逐帧处理后再组装。转换时可将 Loon/Surge 的普通脚本先转为 `100`，如有流式需求再手动优化为 `101`。

### 7.6 脚本上传与 base64 编码

转换脚本时需注意：

1. **编码格式**：Base64(UTF-8(JS源码))，不得包含换行符
2. **function 声明**：必须为 `function process(ctx)` 或 `async function process(ctx)`
3. **不可用的 API**：Loon/Surge 的 `$notification`、`$prefs`、`$task`、`$done({raw:...})` 在 Anywhere 中无对应，需移除或降级
4. **base64 填充**：Base64 字符串末尾可能需要 `=` 填充；Go 程序应使用标准库 base64 编码自动处理
5. **URL 截断**：`base64` 字段在规则行末尾，长 URL pattern 后的 base64 可能被某些文本工具截断；Go 程序应确保完整输出

```go
// Go base64 编码示例
import "encoding/base64"
jsSource := `async function process(ctx) { ... }`
encoded := base64.StdEncoding.EncodeToString([]byte(jsSource))
```

### 7.7 protobuf 脚本特殊处理

Loon/Surge 的 `binary-body-mode=true` 脚本（如 bilibili protobuf 去广告）处理 protobuf 二进制数据。Anywhere 中：
- `ctx.body` 始终为 `Uint8Array`，天然支持二进制
- 可用 `Anywhere.codec.protobuf.decode(ctx.body)` 解码 protobuf
- 可用 `Anywhere.codec.gzip.decode(ctx.body)` 解压

### 7.8 argument 参数传递

Loon `[Script]` 的 `argument=[{showUpList}]` 参数在 Anywhere 中无直接对应。转换方案：
- 将参数硬编码到脚本中（推荐）
- 或使用 `Anywhere.store` 预存参数，脚本运行时读取

---

## 8. URL 模式转换规则

### 8.1 正则语法差异

| 特性 | Loon/Surge | Anywhere | 转换 |
|------|-----------|----------|------|
| 斜杠转义 | `\/`（常见） | `/`（无需转义，但 `\/` 也能工作） | 可选去除 `\/` → `/` |
| 正则引擎 | PCRE/ICU | NSRegularExpression (Unicode) | 大部分兼容，极少数 PCRE 特有语法不支持 |
| 主机匹配 | 精确主机 `api\.bilibili\.com` | 可用 `[^/]+` 泛化 | 见下文 |

### 8.2 主机泛化策略

Anywhere 的 `.amrs` 已有 `hostname` 头部字段做主机拦截门控，URL pattern 中的主机部分可泛化为 `[^/]+` 以简化匹配。

⚠️ **安全警告**：默认 **不** 泛化主机（`--generalize-host=false`）。原因：
- `^https?://example\.com/...` 泛化为 `^https?://[^/]+/...` 后会匹配 **所有 HTTPS 域名**
- 即便 hostname 字段做门控，rewrite 规则评估仍需先匹配 pattern
- 若 QX 模块缺少 `hostname = ...` 行或不完整，所有 HTTPS 域名都会进入 MITM 评估
- 用户手工往 hostname 字段加新域名时，已有泛化规则会立刻扩展作用范围

**何时可以开启 `--generalize-host`**：
- 已确认模块的 hostname 字段完整覆盖所有 pattern 主机
- 显式确认安全后再加 `--generalize-host` 标志

**安全泛化条件**（仅当以下全部满足时才会执行）：
1. 用户显式传 `--generalize-host`
2. pattern 主机段不含通配形式（`.*` / `[^/]+` / `*` / `+` 等）
3. pattern 中每个具体主机都已被 hostname 列表覆盖（精确匹配或子域匹配）
4. pattern 不含真正捕获组（`(?:...)` 非捕获组除外）

否则保留原始主机。

> **示例**：多主机 alternation `(?:app\.bilibili\.com|grpc\.biliapi\.net)` → `[^/]+`（如果所有主机都在 hostname 中）。

> **注意**：泛化是可选的优化，保留原始主机正则也能工作。但当多个主机共享同一 path 时，泛化可大幅简化规则。

### 8.3 不应泛化的场景

- URL pattern 中包含主机特定的捕获组时
- 不同主机需要不同处理逻辑时
- hostname 字段未包含该主机时（需保留精确匹配）

---

## 9. 限制与不可转换项

### 9.1 完全不可转换

| 源特性 | 原因 |
|--------|------|
| `GEOIP,CN,DIRECT` | Anywhere 路由无 GeoIP 规则类型 |
| `PROCESS-NAME,xxx,DIRECT` | Anywhere 无进程匹配 |
| `DEST-PORT` / `SRC-PORT` | Anywhere 无端口匹配 |
| `SRC-IP` / `SRC-IP-CIDR` | Anywhere 无源 IP 匹配 |
| `CELLULAR-RADIO` / `SUBNET` | Anywhere 无网络类型匹配 |
| Loon `[Argument]` 交互式参数 | Anywhere 规则集无 UI 参数；需硬编码或用 store |
| Surge `engine=jsc/webview` | Anywhere 使用自身 JS 引擎，engine 字段无意义 |

### 9.2 需降级处理

| 源特性 | 降级方案 |
|--------|---------|
| `307 <url>` | 降级为 302（Anywhere rewrite sub-mode 1） |
| `302/307 $1`（带捕获的重定向） | Anywhere 原生支持 `$1` 捕获引用，无需降级 |
| `$notification.post(...)` | 降级为 `Anywhere.log.info(...)` |
| `mock-response-body status-code=非200` | Anywhere rewrite sub-mode 2 固定 200；非 200 需脚本 + `Anywhere.respond` |
| `reject-200` with custom status | 同上 |
| Surge `[Map Local]` 远程文件 | 需下载文件内容嵌入，或用脚本 + `Anywhere.http` 动态获取 |

### 9.3 脚本 API 差异需手动改写

- Loon/Surge `$request`/`$response`/`$done` → Anywhere `ctx`/`Anywhere.done()`
- Loon/Surge `$httpClient` 回调式 → Anywhere `Anywhere.http` Promise/async
- Loon/Surge `$persistentStore` → Anywhere `Anywhere.store`
- body 类型：Loon/Surge string → Anywhere `Uint8Array`（需 `Anywhere.codec.utf8` 转换）

### 9.4 body 处理差异

| 特性 | Loon/Surge | Anywhere |
|------|-----------|----------|
| 自动解压 | 是 | 是（script/body-replace/body-json 自动解码 gzip/deflate/br） |
| body 上限 | 可配置 | 4 MiB（超出则 passthrough 或截断） |
| 二进制 body | 需 `binary-body-mode=true` | 始终为 `Uint8Array`，无需标记 |
| Content-Length | 自动修正 | 自动修正 |

---

## 10. 实例推导：Bilibili 去广告

### 10.1 源文件对照

以 `kokoryh/Script` 的 Loon `bilibili.plugin` 和 Surge `bilibili.sgmodule` 为源，`H1d3r/anywhere-rules` 的 `BilibiliBlockAD.amrs` + `BilibiliReject.arrs` 为转换目标。

### 10.2 元数据转换

```
源 (Loon):                          目标 (.amrs):
#!name=哔哩哔哩去广告          →    name = Bilibili去广告
#!desc=...                        →    # desc: ...
#!author=Maasea,...               →    # 上游: kokoryh/Sparkle + kokoryh/Script
#!icon=...                        →    (丢弃)
#!tag=去广告                      →    (丢弃)
```

### 10.3 hostname 转换

```
源 (Loon [MitM]):                   目标 (.amrs):
hostname=ap?.bilibili.com,         →    hostname = grpc.biliapi.net, app.bilibili.com,
         grpc.biliapi.net,                    api.bilibili.com, api.live.bilibili.com,
         www.bilibili.com,                    line3-h5-mobile-api.biligame.com
         m.bilibili.com,
         *live.bilibili.com

转换说明：
- ap?.bilibili.com (通配符) → 展开为 app.bilibili.com, api.bilibili.com
- *live.bilibili.com → 去除 * 前缀，用后缀匹配 live.bilibili.com
- www/m.bilibili.com → 合并（实际转换中按需选取）
```

### 10.4 reject-dict 转换

```
源 (Loon [Rewrite]):
^https:\/\/api\.live\.bilibili\.com\/xlive\/e-commerce-interface\/v1\/ecommerce-user\/get_shopping_info\? reject-dict

目标 (.amrs):
0, 0, ^https://[^/]+/xlive/e-commerce-interface/v1/ecommerce-user/get_shopping_info(?:\?|$), 2, {}

推导：
- reject-dict → rewrite op=0, sub-mode=2, content={}
- phase=0 (rewrite 始终 request)
- URL 主机泛化：api\.live\.bilibili\.com → [^/]+（hostname 已含 api.live.bilibili.com）
- \/ → /
- \? 结尾 → (?:\?|$)
- content {} 需引号包裹（含特殊字符时）：实际 {} 不含逗号，可直接写
```

### 10.5 mock-response-body 转换

```
源 (Loon [Rewrite]):
^https:\/\/app\.bilibili\.com\/x\/resource\/top\/activity\? mock-response-body data-type = json data="{"code":-404,"message":"啥都木有","ttl":1,"data":null}" status-code = 200

目标 (.amrs):
0, 0, ^https://[^/]+/x/(?:resource/(?:top/activity|patch/tab(?:/v2)?)|v2/search/square|vip/ads/materials)(?:\?|$), 2, "{""code"":-404,""message"":""-404"",""ttl"":1,""data"":null}"

推导：
- mock-response-body → rewrite op=0, sub-mode=2, content=data值
- status-code=200 → sub-mode 2 即 200，无需额外字段
- data 中的 JSON 含逗号 → 必须用引号包裹字段，内部 " 双写为 ""
- 多个相似 URL 合并为一条（可选优化）：top/activity | patch/tab | search/square | vip/ads/materials
```

### 10.6 response-body-json-del 转换

```
源 (Loon [Rewrite]):
^https:\/\/app\.bilibili\.com\/x\/resource\/show\/skin\? response-body-json-del data.common_equip

目标 (.amrs):
1, 5, ^https://[^/]+/x/resource/show/tab/v2(?:\?|$), delete, $.data.splash
（注：这是另一条同类规则，skin 的转换类似）
1, 5, ^https://[^/]+/x/resource/show/skin(?:\?|$), delete, $.data.common_equip

推导：
- response-body-json-del → body-json op=5, action=delete
- phase=1 (response)
- dot-path data.common_equip → JSONPath $.data.common_equip
```

### 10.7 脚本转换

```
源 (Loon [Script]):
http-response ^https:\/\/(?:app\.bilibili\.com|grpc\.biliapi\.net)\/bilibili\.app\.dynamic\.v2\.Dynamic\/DynAll$ script-path=https://raw.githubusercontent.com/kokoryh/Script/master/js/bilibili.protobuf.js,requires-body=true,binary-body-mode=true,argument=[{showUpList}],tag=移除动态页面广告

目标 (.amrs):
1, 100, ^https://[^/]+/bilibili\.app\.dynamic\.v2\.Dynamic/DynAll(?:\?|$), <base64 of rewritten script>

推导：
- http-response → phase=1
- script-path → 下载 JS → 改写 API → base64 编码
- binary-body-mode=true → 无需特殊处理（ctx.body 始终 Uint8Array）
- argument=[{showUpList}] → 硬编码到脚本中或用 Anywhere.store
- URL 主机泛化：(?:app\.bilibili\.com|grpc\.biliapi\.net) → [^/]+
```

### 10.8 307 重定向转换

```
源 (Loon [Rewrite]):
(^https:\/\/live\.bilibili\.com\/\d+)(\/?\?.*) 307 $1

目标 (.amrs) — Anywhere 原生支持 $1 捕获引用：
0, 0, ^https://live\.bilibili\.com/\d+, 1, $1

推导：
- 307 降级为 302（Anywhere 仅支持 302）
- $1 捕获引用在 Anywhere rewrite sub-mode 1 中原生支持，无需脚本
- pattern 中的捕获组会被 Anywhere 的 url-pattern 正则匹配，$1 展开为匹配结果
```

### 10.9 路由规则转换 → .arrs

Loon `[Rule]` 中的域名/IP 规则转入 `.arrs`，动作（DIRECT/REJECT/PROXY）在 App 内分配。

```
源 (Loon [Rule]):
URL-REGEX,"^http:\/\/upos-sz-static\.bilivideo\.com\/ssaxcode\/\w{2}\/\w{2}\/\w{32}-1-SPLASH",REJECT-DICT

目标 (.amrs) — URL-REGEX REJECT 需转入 MITM：
0, 0, ^http://upos-sz-static\.bilivideo\.com/ssaxcode/\w{2}/\w{2}/\w{32}-1-SPLASH, 2, {}

目标 (.arrs) — 纯域名 REJECT 规则：
name = Bilibili Reject
2, api.biliapi.com
2, app.biliapi.com
2, api.biliapi.net
2, app.biliapi.net
2, chat.bilibili.com
```

### 10.10 accept-encoding 预处理（Anywhere 专有优化）

在 `BilibiliBlockAD.amrs` 中可见成对的 header 规则：
```
0, 2, ^https://[^/]+/x/v2/feed/index(?:/story)?(?:\?|$), accept-encoding
0, 1, ^https://[^/]+/x/v2/feed/index(?:/story)?(?:\?|$), accept-encoding, identity
```

**作用**：删除请求的 `accept-encoding` 头，再设为 `identity`，强制服务器返回未压缩响应。
**原因**：虽然 Anywhere 的 script/body 规则会自动解压，但对某些需要精确控制 body 的场景，预先强制 identity 编码可避免边缘问题。
**转换策略**：对每个有 body 处理脚本/JSON 编辑的 URL，可选择性添加此预处理对。

### 10.11 Spotify 案例 — header-del + async 代理脚本

**源 (app2smile bilibili.plugin)**：无直接对应（Spotify 去除在 bilibili 上游）
**实际上游 (app2smile rules)**：`SpotifyUnlock.amrs` ← `spotify.plugin`

| 上游特征 | 转换结果 |
|---------|---------|
| `header-del if-none-match` | `0, 2, ^..., if-none-match` |
| `header-add-replace Content-Type` | `0, 1, ^..., Content-Type, application/json` |
| header-add 时 value 含逗号 | `content-type = application/json; charset=utf-8` 头部字段 |
| `http-response script binary-body-mode` | `1, 100, pattern, <base64>` |
| `http-response script` 内含 `await $httpClient` | `async function process(ctx)` + `await Anywhere.http.request()` |
| 脚本中用上游响应替代原始响应 | `Anywhere.respond({status, headers, body})` |

**Spotify 脚本改写要点**：
1. 过滤 Host/Content-Length/Connection 头部（不可转发）
2. 修改 URL（如 `platform=iphone` → `platform=ipad`）
3. `await Anywhere.http.request({..., redirect:"follow"})` 向上游发请求
4. `Anywhere.respond(...)` 合成响应

### 10.12 京东价格解锁 — stream-script (op 101)

**源**：JDPriceUnlock.amrs ← (京东价格对比上游)
**关键特征**：
- 使用 `stream-script` (op 101) 而非普通 `script` (op 100)
- `ctx.frame.end` 检测最后一帧
- `ctx.state` 跨帧累积数据
- `ctx.frame.index` 处理帧序号

```
1, 101, ^https://..., <base64>
// ctx.state.buf 累积 body
// ctx.frame.end 时处理累积 JSON
// 正则搜索 "lowestPrice":"\d+" 并替换
```

### 10.13 夸克/广告拦截 — header-delete + content-type 组合

**源**：KuanBlockAD.amrs ← (夸克广告拦截上游)
**关键特征**：
- 大量使用 `header-delete` (op 2) 配合 phase 0
- `header-delete accept-encoding` 强制 identity 编码（与 Bilibili 相同）
- `header-add` 设置特定 Content-Type
- 多个 URL 合并为一条正则（URL 合并优化）

```
0, 2, ^https://[^/]+/query, accept-encoding
0, 1, ^https://[^/]+/query, content-type, application/json
1, 4, ^https://[^/]+/query, "ad":\[.*?\], "ad":[]
```

### 10.14 拼多多 — response-body-replace-regex

**源**：PinduoduoBlockAD.amrs ← Pinduoduo.lpx (fmz200)
**关键特征**：
- `response-body-replace-regex` 替换 JSON 中的 ad 数组为 `[]`
- 多条 URL 用一个正则覆盖
- `.amrs` 中 `1, 4` (body-replace) 等价于 Loon 的 `response-body-replace-regex`

```
源 (Pinduoduo.lpx):
^https?://mobile.yangkeduo.com/proxy/api/api/express/... response-body-replace-regex "list":\[.+\] "list":[]

目标 (.amrs):
1, 4, ^https://[^/]+/proxy/api/api/express/post/waybill/red_packet/goods_list, ""list"":\[.+\], ""list":[]
```

**转换注意**：
- 逗号分隔：`"list":[.+]` → `"list":[.+]`（引号内逗号无需引号）
- 两段均为 `key:value` 格式，无含逗号字段，无需额外引号
- 实际文件中多对 URL 被合并

### 10.15 知乎/Pixiv — reject-img 精确替换

**源**：PixivBlockAD.amrs ← pixiv.plugin (chaoscard/proxyrules)
**关键特征**：
- `reject-img` → `0, 0, pattern, 3`
- 多个 ad 域名合并为一条 reject-img
- hostname 合并了多个上游（pixiv 域名 + 知乎域名）

### 10.16 BankBlockAD — replace-recursive 深层替换

**源**：BankBlockAD.amrs ← Bank.plugin (luestr/ProxyResource)
**关键特征**：
- `body-json replace-recursive` 深度替换所有 ad 字段
- `1, 5, pattern, replace-recursive, ad, null`（替换为 null 即删除效果）
- 配合 `header-delete accept-encoding` 确保 body 可读

### 10.17 GoodbilityUnlock — Content-Type 保真

**源**：GoodbilityUnlock.amrs ← Goodbility.vip.js (ddgksf2013)
**关键特征**：
- 历史手工规则可能使用 `content-type = application/json; charset=utf-8` 头部字段
- 官方 iOS 导入器当前不识别该顶层字段，转换器不会依赖它
- 需要固定响应 Content-Type 时，转换器生成 `script (op 100)`，通过 `Anywhere.respond({headers:[["Content-Type", "..."]]})` 保留响应头
- 配合 `header-add` 设置空值（删除效果）和 `header-replace` 改写

**Content-Type 说明**：如果后续官方 Anywhere 增加顶层 `content-type` 支持，可重新启用全局头输出；在当前版本中，脚本响应是更可靠的保真方式。

---

## 11. Go 程序设计建议

### 11.1 程序架构

```
输入: Loon .plugin 文件 或 Surge .sgmodule 文件
  ↓
解析器 (Parser) → 统一中间表示 (IR)
  ↓
转换器 (Converter) → Anywhere 规则
  ↓
输出: .arrs 文件 + .amrs 文件
```

### 11.2 统一中间表示 (IR)

```go
// Module 表示解析后的 Loon/Surge 模块
type Module struct {
    Name        string
    Desc        string
    Author      string
    Hostnames   []string      // MITM hostname 列表
    Rules       []RoutingRule // [Rule] 段
    Rewrites    []RewriteRule // [Rewrite]/[URL Rewrite] 段
    Scripts     []ScriptRule  // [Script] 段
    HeaderRWs   []HeaderRule  // [Header Rewrite] 段 (Surge)
    MapLocals   []MapLocalRule// [Map Local] 段 (Surge)
    Arguments   []Argument    // [Argument] 段 (Loon)
}

// RoutingRule 路由规则
type RoutingRule struct {
    Type    string // DOMAIN-SUFFIX, IP-CIDR, URL-REGEX, ...
    Value   string
    Action  string // DIRECT, REJECT, REJECT-DICT, PROXY, ...
    Options []string // no-resolve 等
}

// RewriteRule 重写规则
type RewriteRule struct {
    Pattern string
    Action  string // reject-dict, 302, mock-response-body, response-body-json-del, ...
    Args    map[string]string // data, status-code, data-type 等
    RawJS   string // JS 内联脚本（request-header/response-body 等）
}

// ScriptRule 脚本规则
type ScriptRule struct {
    Phase         int    // 0=request, 1=response
    Pattern       string
    ScriptPath    string
    RequiresBody  bool
    BinaryBody    bool
    Argument      string
    Tag           string
}
```

### 11.3 转换器核心逻辑

```go
// ConvertRoutingRule 将路由规则转为 .arrs 行
// 返回空字符串表示不可转换
func ConvertRoutingRule(r RoutingRule) string {
    switch r.Type {
    case "DOMAIN-SUFFIX", "DOMAIN":
        return fmt.Sprintf("2, %s", r.Value)
    case "DOMAIN-KEYWORD":
        return fmt.Sprintf("3, %s", r.Value)
    case "IP-CIDR":
        return fmt.Sprintf("0, %s", r.Value)
    case "IP-CIDR6":
        return fmt.Sprintf("1, %s", r.Value)
    case "URL-REGEX":
        // REJECT 类需转 .amrs，非 REJECT 不可转换
        return "" // 由调用方处理
    default:
        return "" // GEOIP, PROCESS-NAME 等不可转换
    }
}

// ConvertRewriteRule 将重写规则转为 .amrs 行
func ConvertRewriteRule(r RewriteRule) string {
    pattern := ConvertURLPattern(r.Pattern)
    switch r.Action {
    case "reject", "reject-200":
        return fmt.Sprintf("0, 0, %s, 2", pattern)
    case "reject-dict":
        return fmt.Sprintf("0, 0, %s, 2, {}", pattern)
    case "reject-array":
        return fmt.Sprintf("0, 0, %s, 2, []", pattern)
    case "reject-img":
        return fmt.Sprintf("0, 0, %s, 3", pattern)
    case "302":
        url := r.Args["url"]
        if hasCaptureGroup(url) {
            return convertToRedirectScript(pattern, url, 302)
        }
        return fmt.Sprintf("0, 0, %s, 1, %s", pattern, url)
    case "307":
        url := r.Args["url"]
        if hasCaptureGroup(url) {
            return convertToRedirectScript(pattern, url, 307)
        }
        return fmt.Sprintf("0, 0, %s, 1, %s", pattern, url) // 降级为 302
    case "mock-response-body":
        body := r.Args["data"]
        return fmt.Sprintf("0, 0, %s, 2, %s", pattern, QuoteField(body))
    case "response-body-json-del":
        path := DotPathToJSONPath(r.Args["path"])
        return fmt.Sprintf("1, 5, %s, delete, %s", pattern, path)
    case "response-body-json-add":
        path := DotPathToJSONPath(r.Args["path"])
        value := r.Args["value"]
        return fmt.Sprintf("1, 5, %s, add, %s, %s", pattern, path, QuoteField(value))
    case "response-body-json-replace":
        path := DotPathToJSONPath(r.Args["path"])
        value := r.Args["value"]
        return fmt.Sprintf("1, 5, %s, replace, %s, %s", pattern, path, QuoteField(value))
    case "header-del":
        // Loon header-del: 删除请求头
        // Surge _header-del: 同上
        headerName := r.Args["header"]
        return fmt.Sprintf("0, 2, %s, %s", pattern, headerName)
    case "response-body-replace-regex":
        // Loon response-body-replace-regex: 用正则搜索响应 body 并替换
        search := r.Args["search"]
        replacement := r.Args["replacement"]
        return fmt.Sprintf("1, 4, %s, %s, %s", pattern, QuoteField(search), QuoteField(replacement))
    default:
        return "" // JS 类需特殊处理
    }
}
```

### 11.4 关键函数

```go
// ConvertURLPattern 转换 URL 正则模式
func ConvertURLPattern(pattern string) string {
    // 1. \/ → /
    pattern = strings.ReplaceAll(pattern, `\/`, `/`)
    // 2. 主机泛化（可选）：^https://<host>/ → ^https://[^/]+/
    pattern = regexp.MustCompile(`\^https?://(?:\?:[^/)]+\.)?[^/]+\.`).
        ReplaceAllStringFunc(pattern, func(m string) string {
            return "^https://[^/]+/"
        })
    // 3. 结尾 \? → (?:\?|$)
    pattern = strings.TrimSuffix(pattern, `\?`) + "(?:\\?|$)"
    return pattern
}

// DotPathToJSONPath 将 dot-path 转为 JSONPath
// data.common_equip → $.data.common_equip
// data.items.0.id   → $.data.items[0].id
func DotPathToJSONPath(dotPath string) string {
    parts := strings.Split(dotPath, ".")
    result := "$"
    for _, part := range parts {
        if n, err := strconv.Atoi(part); err == nil {
            result += fmt.Sprintf("[%d]", n)
        } else {
            result += "." + part
        }
    }
    return result
}

// QuoteField 对 .amrs 字段加引号（含逗号或引号时）
func QuoteField(field string) string {
    if strings.ContainsAny(field, ",\"") {
        escaped := strings.ReplaceAll(field, `"`, `""`)
        return fmt.Sprintf(`"%s"`, escaped)
    }
    return field
}

// ExpandHostnameWildcards 将 Loon/Surge 的 hostname 通配符展开为 Anywhere 兼容格式
// Loon/Surge 支持 * 和 ? 通配符，Anywhere 不支持通配符，需展开为具体域名
// 规则：
//   *.domain.com / *domain.com → domain.com
//   prefix?.domain.com         → prefix0...prefix9.domain.com（数字展开）
//   已知变体                   → 直接展开（如 api/app）
func ExpandHostnameWildcards(hostname string) []string {
    var result []string

    // 按逗号分割（Loon/Surge hostname 格式）
    parts := strings.Split(hostname, ",")
    for _, part := range parts {
        part = strings.TrimSpace(part)
        if part == "" {
            continue
        }

        // 去除 %APPEND% 前缀（Surge）
        part = strings.TrimPrefix(part, "%APPEND%")
        part = strings.TrimSpace(part)

        // 去除首尾空白
        part = strings.TrimSpace(part)
        if part == "" {
            continue
        }

        // 处理 *.domain.com / *domain.com
        if strings.HasPrefix(part, "*.") {
            // *.example.com → example.com（base domain 覆盖全部子域名）
            baseDomain := strings.TrimPrefix(part, "*.")
            result = append(result, baseDomain)
            continue
        }
        if strings.HasPrefix(part, "*") && !strings.Contains(part, ".") {
            // *example.com → example.com
            baseDomain := strings.TrimPrefix(part, "*")
            result = append(result, baseDomain)
            continue
        }

        // 处理 prefix?.domain.com（? 通配符）
        if strings.Contains(part, "?") {
            // 尝试展开为常见变体
            expanded := expandQuestionMark(part)
            result = append(result, expanded...)
            continue
        }

        // 普通域名直接添加
        result = append(result, part)
    }

    // 去重
    seen := make(map[string]bool)
    unique := []string{}
    for _, h := range result {
        if !seen[h] {
            seen[h] = true
            unique = append(unique, h)
        }
    }
    return unique
}

// expandQuestionMark 展开 ? 通配符
// 例如：api?.bilibili.com → [api0.bilibili.com, api1.bilibili.com, ..., api9.bilibili.com]
func expandQuestionMark(pattern string) []string {
    // 已知常见应用的域名变体映射（经验值，可扩展）
    knownVariants := map[string][]string{
        // bilibili
        "ap?.bilibili.com":  {"api.bilibili.com", "app.bilibili.com"},
        "app?.bilibili.com": {"app.bilibili.com", "app0.bilibili.com", "app1.bilibili.com", "app2.bilibili.com", "app3.bilibili.com", "app4.bilibili.com", "app5.bilibili.com", "app6.bilibili.com", "app7.bilibili.com", "app8.bilibili.com", "app9.bilibili.com"},
        // 可按需添加更多应用的已知变体
    }

    // 先检查已知变体映射
    if variants, ok := knownVariants[pattern]; ok {
        return variants
    }

    // 通用展开：统计 ? 的数量，生成所有组合
    questionCount := strings.Count(pattern, "?")
    if questionCount == 0 {
        return []string{pattern}
    }

    // 限制展开数量（最多 10 个组合，避免过多）
    var results []string
    chars := []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9"}

    var expand func(pos int, current string)
    expand = func(pos int, current string) {
        if pos == len(pattern) {
            results = append(results, current)
            return
        }
        if string(pattern[pos]) == "?" {
            for _, c := range chars {
                expand(pos+1, current+c)
                // 限制数量
                if len(results) >= 10 {
                    return
                }
            }
        } else {
            expand(pos+1, current+string(pattern[pos]))
        }
    }

    expand(0, "")
    return results
}

// FetchURLWithProxy 下载远程文件，对 GitHub 原始域名使用加速代理
// 代理优先级：ghfast.top → ph.ipv9.win → 原始 URL
func FetchURLWithProxy(rawURL string) ([]byte, error) {
    // GitHub 加速代理列表
    githubProxies := []string{
        "https://ghfast.top/",
        "https://ph.ipv9.win/",
    }

    // 检测是否为 GitHub 原始内容域名
    githubHosts := []string{
        "raw.githubusercontent.com",
        "github.com",
        "gist.githubusercontent.com",
        "codeload.github.com",
    }

    isGitHub := false
    for _, host := range githubHosts {
        if strings.Contains(rawURL, host) {
            isGitHub = true
            break
        }
    }

    // 非 GitHub URL 直接请求
    if !isGitHub {
        return fetchRawURL(rawURL)
    }

    // GitHub URL：依次尝试代理
    for _, proxy := range githubProxies {
        // 构造代理 URL：proxy + 原始 URL（去除 https:// 前缀）
        // 示例：https://raw.githubusercontent.com/... → https://ghfast.top/raw.githubusercontent.com/...
        proxyURL := proxy + strings.TrimPrefix(rawURL, "https://")
        data, err := fetchRawURL(proxyURL)
        if err == nil && len(data) > 0 {
            return data, nil
        }
        // 代理失败，继续尝试下一个
    }

    // 所有代理失败，回退到原始 URL
    return fetchRawURL(rawURL)
}

// fetchRawURL 执行实际的 HTTP GET 请求
func fetchRawURL(url string) ([]byte, error) {
    client := &http.Client{
        Timeout: 30 * time.Second,
    }

    resp, err := client.Get(url)
    if err != nil {
        return nil, fmt.Errorf("fetch %s failed: %w", url, err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("fetch %s returned status %d", url, resp.StatusCode)
    }

    data, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("read %s body failed: %w", url, err)
    }

    return data, nil
}

// FetchAndEncodeScript 下载脚本，改写 API，base64 编码
// 使用 FetchURLWithProxy 自动处理 GitHub 加速
func FetchAndEncodeScript(scriptPath string) (string, error) {
    data, err := FetchURLWithProxy(scriptPath)
    if err != nil {
        return "", err
    }
    source := string(data)
    rewritten := RewriteScriptAPI(source)
    return base64.StdEncoding.EncodeToString([]byte(rewritten)), nil
}

// RewriteScriptAPI 将 Loon/Surge 脚本 API 改写为 Anywhere API
// 支持：$request/$response → ctx；$done → Anywhere.done()
//       $httpClient → Anywhere.http（自动包装为 async）；自动包装为 async function process(ctx)
// 注意：复杂脚本可能需要人工审核
func RewriteScriptAPI(source string) string {
    // 1. 包装为 async function process(ctx)
    // 检测是否含 $httpClient（需要 async）
    needsAsync := strings.Contains(source, "$httpClient")
    funcDecl := "function process(ctx)"
    if needsAsync {
        funcDecl = "async " + funcDecl
    }
    source = funcDecl + " {\n" + source + "\n}"

    // 2. $request / $response 替换
    source = strings.ReplaceAll(source, "$request.url", "ctx.url")
    source = strings.ReplaceAll(source, "$request.method", "ctx.method")
    source = strings.ReplaceAll(source, "$request.headers", "ctx.headers")
    source = strings.ReplaceAll(source, "$request.body", "ctx.body")
    source = strings.ReplaceAll(source, "$response.status", "ctx.status")
    source = strings.ReplaceAll(source, "$response.headers", "ctx.headers")
    source = strings.ReplaceAll(source, "$response.body", "ctx.body")

    // 3. $done 替换
    source = rewriteDone(source)

    // 4. $httpClient 替换为 Anywhere.http.request
    source = rewriteHttpClient(source, needsAsync)

    // 5. JSON parse/encode 包装
    source = wrapJSONOps(source)

    // 6. $persistentStore 替换
    source = strings.ReplaceAll(source, "$persistentStore.read", "Anywhere.store.getString")
    source = strings.ReplaceAll(source, "$persistentStore.write", "Anywhere.store.set")

    // 7. $notification 降级为日志
    source = rewriteNotification(source)

    return source
}

// rewriteDone 处理 $done 调用转换
func rewriteDone(s string) string {
    // {body: x} → ctx.body = x; Anywhere.done()
    re := regexp.MustCompile(`\$done\(\{body:\s*([^}]+)\}\)`)
    s = re.ReplaceAllString(s, "ctx.body = $1; Anywhere.done()")
    // {response: {status, headers, body}} → Anywhere.respond({...})
    re2 := regexp.MustCompile(`\$done\(\{response:\s*\{([^}]+)\}\}\)`)
    s = re2.ReplaceAllString(s, "Anywhere.respond({$1})")
    // 裸 $done() / $done({})
    s = strings.ReplaceAll(s, "$done({})", "Anywhere.done()")
    s = strings.ReplaceAll(s, "$done()", "Anywhere.done()")
    return s
}

// rewriteHttpClient 将 $httpClient 回调式调用转为 async/await
func rewriteHttpClient(s string, async bool) string {
    re := regexp.MustCompile(`\$httpClient\.get\(\s*([^,]+),\s*([\w$]+)\s*\)`)
    s = re.ReplaceAllString(s, `await Anywhere.http.get($1)`)
    re2 := regexp.MustCompile(`\$httpClient\.post\(\s*([^,]+),\s*([^,]+),\s*([\w$]+)\s*\)`)
    s = re2.ReplaceAllString(s, `await Anywhere.http.post($1, $2)`)
    return s
}

// wrapJSONOps 为 JSON.parse/JSON.stringify 添加 codec 转换
func wrapJSONOps(s string) string {
    re := regexp.MustCompile(`JSON\.parse\(\s*([^)]+)\)`)
    s = re.ReplaceAllString(s, `JSON.parse(Anywhere.codec.utf8.decode($1))`)
    re2 := regexp.MustCompile(`JSON\.stringify\(\s*([^)]+)\)`)
    s = re2.ReplaceAllString(s, `Anywhere.codec.utf8.encode(JSON.stringify($1))`)
    return s
}

// rewriteNotification 将 $notification 降级为 Anywhere.log
func rewriteNotification(s string) string {
    re := regexp.MustCompile(`\$notification\.post\([^)]+\)`)
    return re.ReplaceAllString(s, "// notification removed")
}
```

### 11.5 输出文件生成

```go
// GenerateArrs 生成 .arrs 路由规则集文件
func GenerateArrs(name string, rules []string) string {
    var buf strings.Builder
    buf.WriteString(fmt.Sprintf("name = %s\n", name))
    for _, r := range rules {
        buf.WriteString(r + "\n")
    }
    return buf.String()
}

// GenerateAmrs 生成 .amrs MITM 规则集文件
func GenerateAmrs(name string, hostnames []string, rules []string) string {
    var buf strings.Builder
    buf.WriteString(fmt.Sprintf("name = %s\n", name))
    buf.WriteString(fmt.Sprintf("hostname = %s\n", strings.Join(hostnames, ", ")))
    for _, r := range rules {
        buf.WriteString(r + "\n")
    }
    return buf.String()
}
```

### 11.6 转换流程

1. **解析输入**：识别 Loon `.plugin` 或 Surge `.sgmodule`，按 INI 段解析
2. **提取元数据**：`#!name` 等 → `name` 字段 + 注释
3. **提取 hostname**：`[MitM]` / `[MITM]` → `hostname` 字段（去除通配符和 `%APPEND%`）
4. **分流路由规则**：
   - 域名/IP 类 → `.arrs`
   - URL-REGEX REJECT 类 → `.amrs` (rewrite reject)
   - 不可转换类 → 记录警告
5. **转换重写规则**：`[Rewrite]` / `[URL Rewrite]` → `.amrs` 各操作
6. **转换脚本规则**：`[Script]` → 下载 → 改写 → base64 → `.amrs` script
7. **转换头部规则**：Surge `[Header Rewrite]` → `.amrs` header 操作
8. **生成输出**：`.arrs` + `.amrs` 文件 + 转换报告（跳过/降级项）

### 11.7 CLI 参数建议

```
module2anywhere -i <input.plugin|input.sgmodule> -o <output-dir> [--fetch-scripts] [--generalize-host] [--no-encoding-preprocess] [--proxy ghfast.top]
```

| 参数 | 说明 |
|------|------|
| `-i` | 输入文件路径（Loon .plugin 或 Surge .sgmodule） |
| `-o` | 输出目录（生成 .arrs 和 .amrs） |
| `--fetch-scripts` | 远程下载脚本并改写（默认只生成占位符） |
| `--generalize-host` | URL pattern 主机泛化为 `[^/]+`（**默认关闭**，谨慎开启） |
| `--no-encoding-preprocess` | 不自动添加 accept-encoding 预处理对 |
| `--format` | 输出格式：`both`(默认) / `arrs` / `amrs` |
| `--proxy` | GitHub 加速代理：`ghfast.top`(默认) / `ph.ipv9.win` / `none`（直连） |
| `--proxy-retry` | 代理失败时尝试备用代理（默认开启，依次尝试 ghfast.top → ph.ipv9.win → 直连） |

**代理使用说明**：

程序在下载远程文件（`.plugin`/`.sgmodule` 模块文件、JS 脚本文件）时，会自动检测 URL 是否为 GitHub 原始域名：

- `raw.githubusercontent.com`
- `github.com`
- `gist.githubusercontent.com`
- `codeload.github.com`

对于 GitHub URL，默认通过加速代理下载，提高国内访问成功率：

```
原始 URL：
https://raw.githubusercontent.com/kokoryh/Script/master/js/bilibili.protobuf.js

代理 URL（优先）：
https://ghfast.top/raw.githubusercontent.com/kokoryh/Script/master/js/bilibili.protobuf.js

备用代理：
https://ph.ipv9.win/raw.githubusercontent.com/kokoryh/Script/master/js/bilibili.protobuf.js
```

**代理优先级**（默认）：
1. `ghfast.top`（首选）
2. `ph.ipv9.win`（备用）
3. 原始 URL（直连回退）

使用 `--proxy none` 可禁用代理，直接请求原始 URL。

### 11.8 Web 服务中转功能

程序支持部署为 Web 服务，用户可通过 HTTP GET 请求转换远程模块文件，**直接返回规则文件内容**，支持 Anywhere 直接订阅导入。

#### 11.8.1 启动 Web 服务

```bash
# 默认端口 8080
module2anywhere --server

# 指定端口
module2anywhere --server --listen 0.0.0.0:8080
```

#### 11.8.2 API 接口

**GET /mitm.amrs** — 返回 MITM 规则（`.amrs` 格式）

> Anywhere 订阅要求 URL path 以 `.amrs` 结尾，请优先使用 `/mitm.amrs`。`/mitm` 仍可访问（兼容旧版）。

| 参数 | 必填 | 说明 |
|------|------|------|
| `url` | 是 | 远程模块文件的 URL（需 URL 编码） |
| `name` | 否 | 规则集名称（默认从 URL 推导） |
| `fetch` | 否 | 是否下载远程脚本：`true` / `false`（默认 `true`） |
| `generalize` | 否 | 是否泛化主机：`true` / `false`（默认 `false`） |

**GET /rule.arrs** — 返回路由规则（`.arrs` 格式）

> Anywhere 订阅要求 URL path 以 `.arrs` 结尾，请优先使用 `/rule.arrs`。`/rule` 仍可访问（兼容旧版）。

| 参数 | 必填 | 说明 |
|------|------|------|
| `url` | 是 | 远程模块文件的 URL（需 URL 编码） |
| `name` | 否 | 规则集名称（默认从 URL 推导） |

**GET /convert** — 统一转换接口（兼容旧版）

| 参数 | 必填 | 说明 |
|------|------|------|
| `url` | 是 | 远程模块文件的 URL（需 URL 编码） |
| `to` | 是 | 转换类型：`mitm` / `rule` |
| `name` | 否 | 规则集名称（默认从 URL 推导） |
| `fetch` | 否 | 是否下载远程脚本：`true` / `false`（默认 `true`） |
| `generalize` | 否 | 是否泛化主机：`true` / `false`（默认 `false`） |

**GET /deeplink** — 返回 Anywhere 深度链接（`anywhere://add-rule-set`）

先执行完整转换，根据结果自动生成包含 MITM 和/或路由规则链接的深度链接。浏览器访问时返回 HTML 页面，非浏览器请求返回 302 重定向到深度链接。

| 参数 | 必填 | 说明 |
|------|------|------|
| `url` | 是 | 远程模块文件的 URL（需 URL 编码） |
| `name` | 否 | 规则集名称（默认从 URL 推导） |
| `fetch` | 否 | 是否下载远程脚本：`true` / `false`（默认 `true`） |
| `generalize` | 否 | 是否泛化主机：`true` / `false`（默认 `false`） |
| `format` | 否 | 输出格式：`text` 返回纯文本深度链接；默认根据 Accept 头决定（浏览器 → HTML，其他 → 302 重定向） |

**深度链接格式**：

```
anywhere://add-rule-set?link=<mitm-url>&link=<rule-url>
```

每个 `link` 是本服务 `/mitm.amrs` 或 `/rule.arrs` 端点的完整 URL（已 percent-encode）。URL path 以 `.amrs` / `.arrs` 结尾，满足 Anywhere 订阅的路径校验要求。仅包含有内容的规则类型（若模块无路由规则则不包含 rule link）。

**请求示例**：

```bash
# 获取纯文本深度链接
curl "http://localhost:8080/deeplink?url=https%3A%2F%2Fraw.githubusercontent.com%2Fkokoryh%2FScript%2Fmaster%2FLoon%2Fplugin%2Fbilibili.plugin&format=text"

# 浏览器访问（返回 HTML 页面，含「打开 Anywhere 导入」按钮）
# http://localhost:8080/deeplink?url=https%3A%2F%2Fraw.githubusercontent.com%2Fkokoryh%2FScript%2Fmaster%2FLoon%2Fplugin%2Fbilibili.plugin

# 非 HTML 请求（返回 302 重定向到深度链接，Anywhere 可直接打开）
curl -L "http://localhost:8080/deeplink?url=https%3A%2F%2Fraw.githubusercontent.com%2Fkokoryh%2FScript%2Fmaster%2FLoon%2Fplugin%2Fbilibili.plugin"
```

**URL 编码说明**：

由于是 GET 请求，`url` 参数中的特殊字符需要进行 URL 编码：

| 字符 | 编码 |
|------|------|
| `&` | `%26` |
| `=` | `%3D` |
| `?` | `%3F` |

**请求示例**：

```bash
# 获取 MITM 规则（可直接在 Anywhere 中订阅）
curl "http://localhost:8080/mitm?url=https%3A%2F%2Fraw.githubusercontent.com%2Fkokoryh%2FScript%2Fmaster%2FLoon%2Fplugin%2Fbilibili.plugin&name=Bilibili%E5%8E%BB%E5%B9%BF&fetch=true"

# 获取路由规则
curl "http://localhost:8080/rule?url=https%3A%2F%2Fraw.githubusercontent.com%2Fxxx%2Frules%2Fmain%2Fbilibili.sgmodule&name=Bilibili"

# 统一接口
curl "http://localhost:8080/convert?url=https%3A%2F%2Fxxx&to=mitm&name=MyRule"
```

**Anywhere 订阅示例**：

在 Anywhere 中添加订阅：
```
# MITM 规则订阅地址
http://your-server:8080/mitm?url=https%3A%2F%2Fraw.githubusercontent.com%2Fkokoryh%2FScript%2Fmaster%2FLoon%2Fplugin%2Fbilibili.plugin&fetch=true

# 路由规则订阅地址
http://your-server:8080/rule?url=https%3A%2F%2Fraw.githubusercontent.com%2Fxxx%2Frules%2Fmain%2Fbilibili.sgmodule
```

**响应格式**：

直接返回规则文件内容（`text/plain; charset=utf-8`）：

```
# Bilibili去广告 - 转换自远程模块
name = Bilibili去广告
hostname = api.bilibili.com, app.bilibili.com
0, 0, ^https://[^/]+/x/v2/feed/index(?:/story)?(?:\?|$), 2, {}
0, 0, ^https://[^/]+/x/v2/splash/list(?:\?|$), 2, {}
...
```

**错误响应**：

当参数错误或转换失败时，返回 HTTP 错误码 + 错误信息：

```
Error: url parameter is required
```

#### 11.8.3 服务端代码实现

```go
package main

import (
    "fmt"
    "net/http"
    "net/url"
    "strings"
)

func main() {
    http.HandleFunc("/mitm", handleMitm)
    http.HandleFunc("/rule", handleRule)
    http.HandleFunc("/convert", handleConvert)
    fmt.Println("Server started on :8080")
    http.ListenAndServe(":8080", nil)
}

// handleMitm 返回 MITM 规则（.amrs 格式）
func handleMitm(w http.ResponseWriter, r *http.Request) {
    rawURL := r.URL.Query().Get("url")
    name := r.URL.Query().Get("name")
    fetchScripts := r.URL.Query().Get("fetch") == "true"
    generalizeHost := r.URL.Query().Get("generalize") != "false"

    if rawURL == "" {
        http.Error(w, "Error: url parameter is required", http.StatusBadRequest)
        return
    }

    decodedURL, err := url.QueryUnescape(rawURL)
    if err != nil {
        http.Error(w, "Error: Invalid URL encoding", http.StatusBadRequest)
        return
    }

    data, err := FetchURLWithProxy(decodedURL)
    if err != nil {
        http.Error(w, fmt.Sprintf("Error: Failed to fetch remote file: %v", err), http.StatusInternalServerError)
        return
    }

    module, err := ParseModule(data)
    if err != nil {
        http.Error(w, fmt.Sprintf("Error: Failed to parse module: %v", err), http.StatusBadRequest)
        return
    }

    if name != "" {
        module.Name = name
    } else if module.Name == "" {
        module.Name = deriveNameFromURL(decodedURL)
    }

    result := GenerateAmrs(module.Name, module.Hostnames, convertRules(module, generalizeHost, fetchScripts))

    // 设置响应头，支持直接订阅
    w.Header().Set("Content-Type", "text/plain; charset=utf-8")
    w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%s.amrs", module.Name))
    w.WriteHeader(http.StatusOK)
    w.Write([]byte(result))
}

// handleRule 返回路由规则（.arrs 格式）
func handleRule(w http.ResponseWriter, r *http.Request) {
    rawURL := r.URL.Query().Get("url")
    name := r.URL.Query().Get("name")

    if rawURL == "" {
        http.Error(w, "Error: url parameter is required", http.StatusBadRequest)
        return
    }

    decodedURL, err := url.QueryUnescape(rawURL)
    if err != nil {
        http.Error(w, "Error: Invalid URL encoding", http.StatusBadRequest)
        return
    }

    data, err := FetchURLWithProxy(decodedURL)
    if err != nil {
        http.Error(w, fmt.Sprintf("Error: Failed to fetch remote file: %v", err), http.StatusInternalServerError)
        return
    }

    module, err := ParseModule(data)
    if err != nil {
        http.Error(w, fmt.Sprintf("Error: Failed to parse module: %v", err), http.StatusBadRequest)
        return
    }

    if name != "" {
        module.Name = name
    } else if module.Name == "" {
        module.Name = deriveNameFromURL(decodedURL)
    }

    result := GenerateArrs(module.Name, convertRoutingRules(module))

    w.Header().Set("Content-Type", "text/plain; charset=utf-8")
    w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%s.arrs", module.Name))
    w.WriteHeader(http.StatusOK)
    w.Write([]byte(result))
}

// handleConvert 统一转换接口（兼容旧版）
func handleConvert(w http.ResponseWriter, r *http.Request) {
    rawURL := r.URL.Query().Get("url")
    convertTo := r.URL.Query().Get("to")
    name := r.URL.Query().Get("name")
    fetchScripts := r.URL.Query().Get("fetch") == "true"
    generalizeHost := r.URL.Query().Get("generalize") != "false"

    if rawURL == "" {
        http.Error(w, "Error: url parameter is required", http.StatusBadRequest)
        return
    }

    if convertTo == "" {
        convertTo = "mitm"
    }

    decodedURL, err := url.QueryUnescape(rawURL)
    if err != nil {
        http.Error(w, "Error: Invalid URL encoding", http.StatusBadRequest)
        return
    }

    data, err := FetchURLWithProxy(decodedURL)
    if err != nil {
        http.Error(w, fmt.Sprintf("Error: Failed to fetch remote file: %v", err), http.StatusInternalServerError)
        return
    }

    module, err := ParseModule(data)
    if err != nil {
        http.Error(w, fmt.Sprintf("Error: Failed to parse module: %v", err), http.StatusBadRequest)
        return
    }

    if name != "" {
        module.Name = name
    } else if module.Name == "" {
        module.Name = deriveNameFromURL(decodedURL)
    }

    var result string
    var filename string

    switch convertTo {
    case "mitm":
        result = GenerateAmrs(module.Name, module.Hostnames, convertRules(module, generalizeHost, fetchScripts))
        filename = module.Name + ".amrs"
    case "rule":
        result = GenerateArrs(module.Name, convertRoutingRules(module))
        filename = module.Name + ".arrs"
    default:
        http.Error(w, "Error: Invalid 'to' parameter. Use: mitm/rule", http.StatusBadRequest)
        return
    }

    w.Header().Set("Content-Type", "text/plain; charset=utf-8")
    w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%s", filename))
    w.WriteHeader(http.StatusOK)
    w.Write([]byte(result))
}

func deriveNameFromURL(rawURL string) string {
    parsed, err := url.Parse(rawURL)
    if err != nil {
        return "Unnamed"
    }
    path := parsed.Path
    parts := strings.Split(path, "/")
    filename := parts[len(parts)-1]
    filename = strings.TrimSuffix(filename, ".plugin")
    filename = strings.TrimSuffix(filename, ".sgmodule")
    filename = strings.TrimSuffix(filename, ".lpx")
    return filename
}
```

#### 11.8.4 Anywhere 订阅配置

在 Anywhere 中添加规则集订阅：

1. **打开 Anywhere** → **Routing Rules** → **Add Rule Set** → **Add from URL**
2. **输入订阅地址**：
   ```
   http://your-server:8080/mitm?url=https%3A%2F%2Fraw.githubusercontent.com%2Fkokoryh%2FScript%2Fmaster%2FLoon%2Fplugin%2Fbilibili.plugin&fetch=true
   ```
3. **选择策略**：根据需求选择 `DIRECT`、`REJECT` 或代理策略

#### 11.8.5 部署建议

**使用 systemd 部署（Linux）**：

```ini
[Unit]
Description=module2anywhere Conversion Service
After=network.target

[Service]
Type=simple
User=www-data
ExecStart=/usr/local/bin/module2anywhere --server --listen 127.0.0.1:8080
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

**配合 Nginx 反向代理**：

```nginx
server {
    listen 80;
    server_name convert.example.com;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        # 设置合理的超时时间
        proxy_connect_timeout 30s;
        proxy_read_timeout 60s;
    }
}
```

**安全注意事项**：

1. **URL 白名单**：建议对 `url` 参数设置域名白名单
2. **请求频率限制**：使用 Nginx limit_req 限制请求频率
3. **HTTPS**：生产环境应启用 HTTPS
4. **缓存控制**：可添加 `Cache-Control` 头控制客户端缓存

---

## 附录 A：操作类型速查表

### .arrs 规则类型

| ID | 类型 | 示例 |
|----|------|------|
| 0 | IPv4 CIDR | `0, 10.0.0.0/8` |
| 1 | IPv6 CIDR | `1, fe80::/10` |
| 2 | Domain Suffix | `2, example.com` |
| 3 | Domain Keyword | `3, example` |

### .amrs 操作类型

| phase | op | 操作 | 格式 |
|-------|-----|------|------|
| 0 | 0 | rewrite transparent | `0, 0, pattern, 0, <full-url>` |
| 0 | 0 | rewrite 302 | `0, 0, pattern, 1, <full-url>` |
| 0 | 0 | rewrite reject text | `0, 0, pattern, 2, [<content>]` |
| 0 | 0 | rewrite reject gif | `0, 0, pattern, 3` |
| 0 | 0 | rewrite reject data | `0, 0, pattern, 4, [<base64>]` |
| 0/1 | 1 | header-add | `<phase>, 1, pattern, name, value` |
| 0/1 | 2 | header-delete | `<phase>, 2, pattern, name` |
| 0/1 | 3 | header-replace | `<phase>, 3, pattern, name, value` |
| 0/1 | 4 | body-replace | `<phase>, 4, pattern, search, replacement` |
| 0/1 | 5 | body-json | `<phase>, 5, pattern, action, <args>` |
| 0/1 | 100 | script | `<phase>, 100, pattern, base64` |
| 0/1 | 101 | stream-script | `<phase>, 101, pattern, base64` |

### body-json action 速查

| action | 字段 | 示例 |
|--------|------|------|
| add | path, value | `1, 5, ^/api, add, $.vip, true` |
| replace | path, value | `1, 5, ^/api, replace, $.tier, "gold"` |
| delete | path | `1, 5, ^/api, delete, $.data.ad` |
| replace-recursive | key, value | `1, 5, ^/api, replace-recursive, token, "***"` |
| delete-recursive | key | `1, 5, ^/api, delete-recursive, ad_info` |
| remove-where-key-exists | path, key | `1, 5, ^/api, remove-where-key-exists, $.items, ad` |
| remove-where-field-in | path, field, values | `1, 5, ^/api, remove-where-field-in, $.items, status, expired` |

---

## 附录 B：Anywhere 脚本 API 速查

```javascript
// ctx 对象
ctx.phase    // "request" | "response" (只读)
ctx.method   // string | null (只读)
ctx.url      // string | null (只读)
ctx.status   // number | null (只读, response 阶段)
ctx.headers  // [[name, value], ...] (只读)
ctx.body     // Uint8Array (可读写)

// stream-script 额外字段
ctx.frame    // { index, end }
ctx.state    // {} (跨帧持久化)

// Anywhere.codec
Anywhere.codec.utf8.encode(str) → Uint8Array
Anywhere.codec.utf8.decode(bytes) → string
Anywhere.codec.base64.encode(bytes) → string
Anywhere.codec.base64.decode(str) → Uint8Array
Anywhere.codec.hex.encode(bytes) → string
Anywhere.codec.hex.decode(str) → Uint8Array
Anywhere.codec.gzip.encode(bytes) → Uint8Array
Anywhere.codec.gzip.decode(bytes) → Uint8Array
Anywhere.codec.protobuf.decode(bytes) → [{field, wire, value}]
Anywhere.codec.protobuf.encode(entries) → Uint8Array

// Anywhere.json (bytes-in/bytes-out)
Anywhere.json.add(body, path, value) → Uint8Array
Anywhere.json.replace(body, path, value) → Uint8Array
Anywhere.json.delete(body, path) → Uint8Array
Anywhere.json.replaceRecursive(body, key, value) → Uint8Array
Anywhere.json.deleteRecursive(body, key) → Uint8Array

// Anywhere.http (仅 script, 需 async)
await Anywhere.http.get(url, options) → {status, headers, body, url}
await Anywhere.http.post(url, options) → {status, headers, body, url}
await Anywhere.http.request(options) → {status, headers, body, url}

// Anywhere.store
Anywhere.store.get(key, onDisk) → Uint8Array | undefined
Anywhere.store.getString(key, onDisk) → string | undefined
Anywhere.store.set(key, value, onDisk)
Anywhere.store.delete(key, onDisk)
Anywhere.store.keys(onDisk) → [string]

// Anywhere.log
Anywhere.log.info(msg)
Anywhere.log.warning(msg)
Anywhere.log.error(msg)
Anywhere.log.debug(msg)

// 控制指令
Anywhere.done()    // 提交 ctx，跳过后续规则
Anywhere.exit()    // 丢弃修改，恢复原始消息
Anywhere.respond({status, headers, body})  // 请求阶段直接返回响应
```
