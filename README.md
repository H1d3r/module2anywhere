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

> rewrite 始终为 request 阶段，无论 phase 列写什么。第一条匹配的 rewrite 规则胜出。替换是字面量，无 `$1` 捕获展开。

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
| `302` | `pattern 302 <url>` | 302 重定向（支持 `$1` 捕获） |
| `307` | `pattern 307 <url>` | 307 重定向（支持 `$1` 捕获） |
| `mock-response-body` | `pattern mock-response-body data-type=<type> data="<body>" status-code=<code>` | 模拟响应 |
| `request-header` | `pattern request-header <JS>` | JS 改写请求头 |
| `request-body` | `pattern request-body <JS>` | JS 改写请求体 |
| `response-body` | `pattern response-body <JS>` | JS 改写响应体 |
| `response-body-json-del` | `pattern response-body-json-del <dot-path>` | 删除响应 JSON 路径 |
| `response-body-json-add` | `pattern response-body-json-add <dot-path> <value>` | 添加响应 JSON 路径 |
| `response-body-json-replace` | `pattern response-body-json-replace <dot-path> <value>` | 替换响应 JSON 路径 |

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
| Loon `[MitM] hostname = a.com, *.b.com` | `hostname = a.com, b.com` | 去除 `*.` 通配符（Anywhere 用后缀匹配，`b.com` 已覆盖 `*.b.com`） |
| Surge `[MITM] hostname = %APPEND% a.com, b.com` | `hostname = a.com, b.com` | 去除 `%APPEND%` 前缀 |
| Surge `[MITM] hostname = a.com, b.com` | `hostname = a.com, b.com` | 直接映射 |

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

### 6.2 重定向类

| Loon/Surge 动作 | Anywhere 操作 | 转换结果 | 说明 |
|----------------|--------------|---------|------|
| `302 <url>` | rewrite sub-mode 1 | `0, 0, pattern, 1, <url>` | 302 重定向 |
| `307 <url>` | rewrite sub-mode 1 | `0, 0, pattern, 1, <url>` | **近似**：Anywhere 仅支持 302，307 降级为 302 |
| `302 $1` (带捕获) | script | `0, 100, pattern, <base64>` | Anywhere rewrite 不支持 `$1`，需用脚本实现 |
| `307 $1` (带捕获) | script | `0, 100, pattern, <base64>` | 同上 |

> **重要限制**：Anywhere rewrite sub-mode 0/1 的 URL 替换是**字面量**，无 `$1` 捕获展开。带捕获组的重定向需转为脚本，用 `Anywhere.respond({status:302, headers:[["Location", newUrl]]})` 实现。

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

> **dot-path → JSONPath 转换**：`data.common_equip` → `$.data.common_equip`；`data.items.0.id` → `$.data.items[0].id`

### 6.5 头部重写类

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
| `$request.headers` | `ctx.headers`（`[[name,value],...]`） | 请求头（只读） |
| `$request.body` | `ctx.body`（`Uint8Array`，phase=0） | 请求体 |
| `$response.status` | `ctx.status` | 响应状态码 |
| `$response.headers` | `ctx.headers` | 响应头（只读） |
| `$response.body` | `ctx.body`（`Uint8Array`，phase=1） | 响应体 |
| `$done({})` | `Anywhere.done()` | 提交当前 ctx，跳过后续规则 |
| `$done({body: x})` | `ctx.body = x; Anywhere.done()` | 设置 body 后提交 |
| `$done({response: {...}})` | `Anywhere.respond({status, headers, body})` | 请求阶段直接返回响应 |
| `$persistentStore.read(key)` | `Anywhere.store.getString(key, true)` | 持久化存储读取 |
| `$persistentStore.write(val, key)` | `Anywhere.store.set(key, val, true)` | 持久化存储写入 |
| `$httpClient.get(url, cb)` | `await Anywhere.http.get(url)` | HTTP 请求（需 async） |
| `$httpClient.post(url, opts, cb)` | `await Anywhere.http.post(url, opts)` | HTTP POST |
| `$notification.post(title,sub,body)` | `Anywhere.log.info(...)` | Anywhere 无通知，降级为日志 |
| `JSON.parse($response.body)` | `JSON.parse(Anywhere.codec.utf8.decode(ctx.body))` | body 需先 decode |
| `body = JSON.stringify(obj)` | `ctx.body = Anywhere.codec.utf8.encode(JSON.stringify(obj))` | body 需 encode |

### 7.4 脚本改写模板

**Loon/Surge 原始脚本**：
```javascript
function run() {
  var body = $response.body;
  var obj = JSON.parse(body);
  obj.ad_removed = true;
  $done({ body: JSON.stringify(obj) });
}
```

**Anywhere 改写后**：
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

### 7.5 protobuf 脚本特殊处理

Loon/Surge 的 `binary-body-mode=true` 脚本（如 bilibili protobuf 去广告）处理 protobuf 二进制数据。Anywhere 中：
- `ctx.body` 始终为 `Uint8Array`，天然支持二进制
- 可用 `Anywhere.codec.protobuf.decode(ctx.body)` 解码 protobuf
- 可用 `Anywhere.codec.gzip.decode(ctx.body)` 解压

### 7.6 argument 参数传递

Loon `[Script]` 的 `argument=[{showUpList}]` 参数在 Anywhere 中无直接对应。转换方案：
- 将参数硬编码到脚本中
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

Anywhere 的 `.amrs` 已有 `hostname` 头部字段做主机拦截门控，URL pattern 中的主机部分可泛化为 `[^/]+` 以简化匹配：

| 源 pattern | 转换后 pattern | 说明 |
|-----------|--------------|------|
| `^https:\/\/app\.bilibili\.com\/x\/v2\/feed\/index` | `^https://[^/]+/x/v2/feed/index(?:\?|$)` | 主机泛化 + 结尾锚定 |
| `^https:\/\/api\.bilibili\.com\/x\/v2\/dm\/qoe\/show\?` | `^https://[^/]+/x/v2/dm/qoe/show(?:\?|$)` | `\?` 结尾 → `(?:\?|$)` |

**泛化规则**：
1. `^https:\/\/<host>\/` → `^https://[^/]+/`（主机由 hostname 字段门控）
2. `\/` → `/`（去除不必要的转义）
3. 结尾 `\?` → `(?:\?|$)`（匹配查询参数开头或行尾）
4. 结尾 `$` 保持不变
5. 多主机 alternation `(?:app\.bilibili\.com|grpc\.biliapi\.net)` → `[^/]+`（如果所有主机都在 hostname 中）

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
| `302/307 $1`（带捕获的重定向） | 转为脚本 + `Anywhere.respond` |
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

目标 (.amrs) — 需转为脚本：
0, 100, ^https://live\.bilibili\.com/\d+, <base64 of script>

脚本内容：
function process(ctx) {
  if (ctx.phase !== "request" || !ctx.url) return;
  var url = ctx.url;
  var m = url.match(/^(https:\/\/live\.bilibili\.com\/\d+)/);
  if (m) {
    Anywhere.respond({ status: 302, headers: [["Location", m[1]]] });
  }
}

推导：
- 307 带捕获组 $1 → Anywhere rewrite 不支持捕获 → 必须用脚本
- 307 降级为 302（Anywhere respond 支持 302）
- 脚本用 ctx.url.match 提取捕获组
- Anywhere.respond 合成重定向响应
```

### 10.9 路由规则转换 → .arrs

```
源 (Loon [Rule]):
URL-REGEX,"^http:\/\/upos-sz-static\.bilivideo\.com\/ssaxcode\/\w{2}\/\w{2}\/\w{32}-1-SPLASH",REJECT-DICT

目标 (.amrs) — URL-REGEX REJECT 需转入 MITM：
0, 0, ^http://upos-sz-static\.bilivideo\.com/ssaxcode/\w{2}/\w{2}/\w{32}-1-SPLASH, 2, {}

目标 (.arrs) — 纯域名 REJECT 规则：
源 (无对应域名规则，BilibiliReject.arrs 为独立域名拒绝集)
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

// FetchAndEncodeScript 下载脚本，改写 API，base64 编码
func FetchAndEncodeScript(scriptPath string) (string, error) {
    // 1. 下载 JS
    source, err := fetchURL(scriptPath)
    if err != nil {
        return "", err
    }
    // 2. 改写 API（$request → ctx, $done → Anywhere.done 等）
    rewritten := RewriteScriptAPI(source)
    // 3. base64 编码
    return base64.StdEncoding.EncodeToString([]byte(rewritten)), nil
}

// RewriteScriptAPI 将 Loon/Surge 脚本 API 改写为 Anywhere API
func RewriteScriptAPI(source string) string {
    // 这是最复杂的部分，可能需要：
    // - 正则替换 $request.url → ctx.url
    // - 正则替换 $response.body → ctx.body (需 codec 转换)
    // - 正则替换 $done({body:x}) → ctx.body=x; Anywhere.done()
    // - 包装为 function process(ctx) {...}
    // - 添加 codec encode/decode
    // 注意：复杂脚本可能需要人工审核
    return rewritten
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
loon2anywhere -i <input.plugin|input.sgmodule> -o <output-dir> [--fetch-scripts] [--generalize-host] [--no-encoding-preprocess]
```

| 参数 | 说明 |
|------|------|
| `-i` | 输入文件路径（Loon .plugin 或 Surge .sgmodule） |
| `-o` | 输出目录（生成 .arrs 和 .amrs） |
| `--fetch-scripts` | 远程下载脚本并改写（默认只生成占位符） |
| `--generalize-host` | URL pattern 主机泛化为 `[^/]+`（默认开启） |
| `--no-encoding-preprocess` | 不自动添加 accept-encoding 预处理对 |
| `--format` | 输出格式：`both`(默认) / `arrs` / `amrs` |

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
