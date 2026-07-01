/**
 * module2anywhere 共享库（自包含）
 *
 * 这个文件由 build.js 复制到每个 functions/*.js 端点文件，
 * 不允许 import 任何其他模块，所有依赖必须内置。
 * EdgeOne Pages dev 模式的 esbuild 编译只处理 1 层 import，
 * 任何嵌套的 import 都会导致 TypeError。
 *
 * 共享函数通过文件末尾的 `const lib = { ... }` 暴露。
 */

// ===================== 工具函数 =====================

const inlineSectionHeaderRE = /\s+(\[(?:Rule|Rewrite|URL Rewrite|Header Rewrite|Map Local|Script|MitM|MITM|mitm|Argument|Host|General)\])\s*/g;
const inlineMetadataRE = /\s+(#![A-Za-z0-9_-]+\s*=)/g;

function normalizeInlineSections(content) {
  return String(content || "")
    .replace(inlineMetadataRE, "\n$1")
    .replace(inlineSectionHeaderRE, "\n$1\n");
}

const splitSections = (content) => {
  const sections = [];
  let current = null;
  const bodyLines = [];
  for (const raw of normalizeInlineSections(content).split("\n")) {
    const line = raw.replace(/\r$/, "");
    const trimmed = line.trim();
    if (trimmed === "") {
      if (current) bodyLines.push(line);
      continue;
    }
    if (trimmed.startsWith("#!")) {
      if (!current) current = { name: "__meta__", body: "" };
      bodyLines.push(line);
      continue;
    }
    if (trimmed.startsWith("[") && trimmed.endsWith("]")) {
      if (current) {
        current.body = bodyLines.join("\n");
        sections.push(current);
      }
      current = { name: trimmed.slice(1, -1).trim(), body: "" };
      bodyLines.length = 0;
      continue;
    }
    bodyLines.push(line);
  }
  if (current) {
    current.body = bodyLines.join("\n");
    sections.push(current);
  }
  return sections;
};

const parseMeta = (body) => {
  const meta = {};
  for (const line of body.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed.startsWith("#!")) continue;
    const kv = trimmed.slice(2);
    const idx = kv.indexOf("=");
    if (idx < 0) continue;
    const key = kv.slice(0, idx).trim();
    const val = kv.slice(idx + 1).trim();
    meta[key] = val;
  }
  return meta;
};

const splitCSVFields = (line) => {
  const fields = [];
  let buf = "";
  let inQuote = false;
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (inQuote) {
      if (c === '"') {
        if (i + 1 < line.length && line[i + 1] === '"') {
          buf += '"';
          i++;
        } else {
          inQuote = false;
        }
      } else {
        buf += c;
      }
    } else if (c === '"') {
      inQuote = true;
    } else if (c === ",") {
      fields.push(buf.trim());
      buf = "";
    } else {
      buf += c;
    }
  }
  fields.push(buf.trim());
  return fields;
};

const parseKeyValueList = (tokens) => {
  const args = {};
  const positional = [];
  for (const t of tokens) {
    if (!t) continue;
    const idx = t.indexOf("=");
    if (idx > 0) {
      args[t.slice(0, idx).trim().toLowerCase()] = t.slice(idx + 1).trim();
    } else {
      positional.push(t);
    }
  }
  return { args, positional };
};

const trimQuotes = (s) => {
  s = s.trim();
  if (s.length >= 2 && s[0] === '"' && s[s.length - 1] === '"') {
    return s.slice(1, -1).replace(/""/g, '"');
  }
  return s;
};

const stripInlineComment = (s) => {
  s = String(s || "").trim();
  for (let i = 0; i < s.length; i++) {
    if (s[i] !== "#" && s[i] !== ";") continue;
    if (i === 0 || s[i - 1] === " " || s[i - 1] === "\t") {
      return s.slice(0, i).trim();
    }
  }
  return s;
};

const normalizeHostnames = (raw) => {
  const parts = raw.split(",");
  const out = [];
  const seen = new Set();
  for (let p of parts) {
    for (const host of normalizeHostnameCandidates(p)) {
      if (!host || seen.has(host)) continue;
      seen.add(host);
      out.push(host);
    }
  }
  return out;
};

function normalizeHostnameCandidates(raw) {
  let value = String(raw || "").trim().toLowerCase();
  value = value.replace(/^%append%/i, "").trim();
  if (!value || value.startsWith("-") || /[?/\s]/.test(value)) return [];
  const candidates = [];
  const add = (item) => {
    item = String(item || "").trim();
    item = item.replace(/:\d+$/, "").replace(/^\.+|\.+$/g, "");
    if (!item || /[*?]/.test(item) || isUnsafeHostnameSuffix(item) || !validHostnameSuffix(item)) return;
    if (!candidates.includes(item)) candidates.push(item);
  };
  if (value.startsWith("*.")) {
    add(value.slice(2));
  } else if (value.startsWith("*") && value.indexOf(".") >= 0) {
    add(value.slice(1));
    add(value.slice(value.indexOf(".") + 1));
  } else {
    const firstLabel = value.split(".", 1)[0] || "";
    if (firstLabel.indexOf("*") >= 0 && value.indexOf(".") >= 0) {
      add(value.slice(value.indexOf(".") + 1));
    } else {
      add(value);
    }
  }
  return candidates;
}

function validHostnameSuffix(host) {
  return /^[a-z0-9.-]+$/.test(host) && host.indexOf(".") >= 0;
}

function isUnsafeHostnameSuffix(host) {
  if (!host || host.indexOf(".") < 0) return true;
  return /^(?:com|net|org|top|cn|tv|cc|io|app|co|me|xyz|site|vip)$/.test(host);
}

// dedupStrings 字符串去重，保持顺序。
const dedupStrings = (arr) => {
  const seen = new Set();
  const out = [];
  for (const s of arr) {
    if (!s || seen.has(s)) continue;
    seen.add(s);
    out.push(s);
  }
  return out;
};

const splitFirstWhitespace = (s) => {
  s = s.replace(/^[\t ]+/, "");
  for (let i = 0; i < s.length; i++) {
    if (s[i] === " " || s[i] === "\t") {
      return [s.slice(0, i), s.slice(i + 1).replace(/^[\t ]+/, "")];
    }
  }
  return [s, ""];
};

const splitWhitespace = (s) => s.split(/\s+/).filter(Boolean);

const tokenizeKV = (s) => {
  const tokens = [];
  let buf = "";
  let inQuote = false;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (inQuote) {
      buf += c;
      if (c === '"') inQuote = false;
    } else if (c === '"') {
      inQuote = true;
      buf += c;
    } else if (c === " " || c === "\t") {
      if (buf) {
        tokens.push(buf);
        buf = "";
      }
    } else {
      buf += c;
    }
  }
  if (buf) tokens.push(buf);
  return tokens;
};

const parseKVArgs = (s, args) => {
  const tokens = tokenizeKV(s);
  for (const t of tokens) {
    const idx = t.indexOf("=");
    if (idx <= 0) continue;
    const key = t.slice(0, idx).trim().toLowerCase();
    const val = trimQuotes(t.slice(idx + 1));
    args[key] = val;
  }
};

// ===================== URL 模式转换 =====================

const convertURLPattern = (pattern, generalize) => {
  if (!pattern) return pattern;
  pattern = pattern.replace(/\\\//g, "/");
  if (generalize) {
    pattern = generalizeHost(pattern);
  }
  if (pattern.endsWith("\\?")) {
    pattern = pattern.slice(0, -2) + "(?:\\?|$)";
  }
  return pattern;
};

function inferHostnameSuffixesFromPattern(pattern) {
  const out = [];
  for (const patternPart of splitTopLevelAlternation(String(pattern || ""))) {
    const p = unwrapNonCapture(patternPart.trim());
    let hostPart = urlPatternHostPart(p);
    if (!hostPart) continue;
    hostPart = unwrapNonCapture(hostPart.replace(/^\?/, ""));
    hostPart = unwrapCapture(hostPart);
    for (const part of expandLeadingHostGroup(hostPart)) {
      appendHostnameCandidate(out, inferHostnameSuffixFromHostPattern(part));
    }
  }
  return out;
}

function hasComplexHostnamePattern(pattern) {
  for (const patternPart of splitTopLevelAlternation(String(pattern || ""))) {
    const p = unwrapNonCapture(patternPart.trim());
    let hostPart = urlPatternHostPart(p);
    if (!hostPart) continue;
    hostPart = unwrapCapture(unwrapNonCapture(hostPart.replace(/^\?/, "")));
    if (/[*+?[\]()|]/.test(hostPart) || hostPart.indexOf("\\d") >= 0) return true;
  }
  return false;
}

function unwrapNonCapture(s) {
  s = String(s || "");
  if (s.startsWith("(?:") && s.endsWith(")")) {
    const close = findMatchingParen(s.slice(3));
    if (close === s.length - 4) return s.slice(3, -1);
  }
  return s;
}

function unwrapCapture(s) {
  s = String(s || "");
  if (s.startsWith("(") && s.endsWith(")") && !s.startsWith("(?")) {
    const close = findMatchingParen(s.slice(1));
    if (close === s.length - 2) return s.slice(1, -1);
  }
  return s;
}

function findMatchingParen(s) {
  let depth = 0;
  for (let i = 0; i < s.length; i++) {
    if (s[i] === "(") {
      depth++;
    } else if (s[i] === ")") {
      if (depth === 0) return i;
      depth--;
    }
  }
  return -1;
}

function expandLeadingHostGroup(s) {
  s = String(s || "");
  if (!s.startsWith("(")) return splitTopLevelAlternation(s);
  const innerStart = s.startsWith("(?:") ? 3 : 1;
  const close = findMatchingParen(s.slice(innerStart));
  if (close < 0) return splitTopLevelAlternation(s);
  const closeAbs = innerStart + close;
  const suffix = s.slice(closeAbs + 1);
  return splitTopLevelAlternation(s.slice(innerStart, closeAbs)).map((alt) => alt + suffix);
}

function urlPatternHostPart(pattern) {
  pattern = String(pattern || "").replace(/\\\\\//g, "/").replace(/\\\//g, "/");
  for (const marker of ["://", ":\\/\\/"]) {
    const idx = pattern.indexOf(marker);
    if (idx < 0) continue;
    const rest = pattern.slice(idx + marker.length);
    if (!rest) return "";
    if (rest.startsWith("(?:")) {
      const close = findMatchingParen(rest.slice(3));
      if (close >= 0) {
        let end = 3 + close + 1;
        if (end < rest.length && rest.slice(end, end + 2) === "\\.") {
          end += 2;
          while (end < rest.length && isHostPatternChar(rest[end])) end++;
        }
        if (end < rest.length && rest[end] === ":") {
          end++;
          while (end < rest.length && /[0-9\\d+]/.test(rest[end])) end++;
        }
        return rest.slice(0, end);
      }
    }
    const slash = rest.indexOf("/");
    return slash >= 0 ? rest.slice(0, slash) : rest;
  }
  return "";
}

function inferHostnameSuffixFromHostPattern(part) {
  part = String(part || "").trim();
  if (!part) return "";
  part = part.replace(/^\^/, "").replace(/\$$/, "");
  if (part.startsWith("(?:")) part = part.slice(3);
  if (part.endsWith(")")) part = part.slice(0, -1);
  part = stripRegexPort(part);
  if (!hasStaticHostnameTail(part)) return "";
  part = part
    .replace(/\\\./g, ".")
    .replace(/\\-/g, "-")
    .replace(/\\_/g, "_")
    .replace(/\[A-Za-z0-9-\]\+/g, "")
    .replace(/\[a-zA-Z0-9-\]\+/g, "")
    .replace(/\[a-z0-9-\]\+/g, "")
    .replace(/\[0-9\]\+/g, "")
    .replace(/\.\*/g, "")
    .replace(/\.\+/g, "")
    .replace(/[*+?]/g, "")
    .replace(/^\.+|\.+$/g, "");
  if (/[(){}|^$/]/.test(part)) {
    const idx = part.lastIndexOf(".");
    if (idx >= 0 && idx + 1 < part.length) part = part.slice(idx + 1);
    else return "";
  }
  const labels = part.split(".").map((v) => v.replace(/^-+|-+$/g, "")).filter(Boolean);
  if (labels.length < 2) return "";
  const tail = labels.slice(-2);
  if (labels.length === 2 && /[\\[\]()+*?]/.test(labels[0])) return "";
  for (const label of tail) {
    if (/[\\[\]()+*?]/.test(label)) return "";
  }
  const host = tail.join(".").toLowerCase();
  return isUnsafeHostnameSuffix(host) ? "" : host;
}

function hasStaticHostnameTail(hostPattern) {
  const labels = String(hostPattern || "")
    .replace(/\\\\\./g, ".")
    .replace(/\\\./g, ".")
    .replace(/\\-/g, "-")
    .split(".")
    .map((v) => v.trim())
    .filter(Boolean);
  if (labels.length < 2) return false;
  for (const label of labels.slice(-2)) {
    if (/[\\[\]()+*?]/.test(label)) return false;
  }
  return true;
}

function splitTopLevelAlternation(s) {
  const parts = [];
  let depth = 0;
  let start = 0;
  for (let i = 0; i < s.length; i++) {
    const ch = s[i];
    if (ch === "\\") {
      i++;
    } else if (ch === "(") {
      depth++;
    } else if (ch === ")") {
      if (depth > 0) depth--;
    } else if (ch === "|" && depth === 0) {
      parts.push(s.slice(start, i));
      start = i + 1;
    }
  }
  parts.push(s.slice(start));
  return parts;
}

function stripRegexPort(s) {
  const idx = s.lastIndexOf(":");
  if (idx < 0) return s;
  const port = s.slice(idx + 1);
  if (!port || !/^[0-9\\d+]+$/.test(port)) return s;
  return s.slice(0, idx);
}

function appendHostnameCandidate(hosts, host) {
  host = String(host || "").trim().toLowerCase().replace(/^\.+|\.+$/g, "");
  if (!host || /[*?/\\\s]/.test(host) || isUnsafeHostnameSuffix(host)) return;
  if (!hosts.includes(host)) hosts.push(host);
}

function isHostPatternChar(ch) {
  return /[a-zA-Z0-9.\\-]/.test(ch) || ch === "\\";
}

function generalizeHost(pattern) {
  if (!pattern.startsWith("^http")) return pattern;
  const idx = pattern.indexOf("://");
  if (idx < 0) return pattern;
  const rest = pattern.slice(idx + 3);
  const slash = rest.indexOf("/");
  if (slash < 0) return pattern;
  const hostPart = rest.slice(0, slash);
  if (containsCaptureGroup(hostPart)) return pattern;
  return pattern.slice(0, idx + 3) + "[^/]+" + rest.slice(slash);
}

function containsCaptureGroup(s) {
  for (let i = 0; i < s.length; i++) {
    if (s[i] === "(") {
      if (i + 2 < s.length && s[i + 1] === "?") continue;
      return true;
    }
  }
  return false;
}

const dotPathToJSONPath = (dotPath) => {
  dotPath = dotPath.trim();
  if (!dotPath) return "$";
  if (dotPath.startsWith("$.") || dotPath === "$") return dotPath;
  const parts = dotPath.split(".");
  let result = "$";
  for (const part of parts) {
    if (!part) continue;
    const bracketIdx = part.indexOf("[");
    if (bracketIdx >= 0) {
      const key = part.slice(0, bracketIdx);
      const brackets = part.slice(bracketIdx);
      if (key) result += "." + key;
      result += brackets;
      continue;
    }
    if (/^\d+$/.test(part)) {
      result += "[" + part + "]";
      continue;
    }
    result += "." + part;
  }
  return result;
};

const quoteField = (field) => {
  if (/[,"]/.test(field) || hasSignificantWhitespace(field)) {
    const escaped = field.replace(/"/g, '""');
    return '"' + escaped + '"';
  }
  return field;
};

function hasSignificantWhitespace(s) {
  if (!s) return false;
  return (
    s[0] === " " ||
    s[0] === "\t" ||
    s[s.length - 1] === " " ||
    s[s.length - 1] === "\t"
  );
}

const hasCaptureGroup = (s) => {
  for (let i = 0; i < s.length; i++) {
    if (
      s[i] === "$" &&
      i + 1 < s.length &&
      s[i + 1] >= "1" &&
      s[i + 1] <= "9"
    ) {
      return true;
    }
  }
  return false;
};

const tinyGIFBase64 = "R0lGODlhAQABAPAAAP///wAAACH5BAAAAAAALAAAAAABAAEAAAICRAEAOw==";

function jsStringLiteral(value) {
  return JSON.stringify(String(value || ""));
}

function encodeStaticRespondScript(status, headers, body, bodyEncoding) {
  status = parseStatusCode(status);
  const headerPairs = (headers || []).map(function (h) {
    return [String(h[0] || ""), String(h[1] || "")];
  });
  let bodyExpr = "Anywhere.codec.utf8.encode(" + jsStringLiteral(body) + ")";
  if (String(bodyEncoding || "").toLowerCase() === "base64") {
    bodyExpr = "Anywhere.codec.base64.decode(" + jsStringLiteral(body) + ")";
  }
  const js =
    'function process(ctx){\n  if (ctx.phase !== "request") return;\n  Anywhere.respond({status:' +
    status +
    ",headers:" +
    JSON.stringify(headerPairs) +
    ",body:" +
    bodyExpr +
    "});\n}";
  return btoa(unescape(encodeURIComponent(js)));
}

// ===================== 远程获取 =====================

const GITHUB_PROXIES = ["https://ghfast.top/", "https://ph.ipv9.win/"];
const GITHUB_HOSTS = [
  "raw.githubusercontent.com",
  "github.com",
  "gist.githubusercontent.com",
  "codeload.github.com",
];
const DEFAULT_USER_AGENT =
  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36";
const USER_AGENTS = {
  surge: "Shadowrocket/308 CFNetwork/1568.300 Darwin/24.1.0",
  loon: "Loon/750 CFNetwork/1568.300 Darwin/24.1.0",
  quantumultx: "Quantumult%20X%20Patched/1.0.30 (iPhone;iOS%2017.0)",
  unknown: DEFAULT_USER_AGENT,
};

const getUserAgent = (source) =>
  !source ? DEFAULT_USER_AGENT : USER_AGENTS[source] || DEFAULT_USER_AGENT;

function isGitHubURL(url) {
  // 通过解析 URL host 精确匹配，避免路径中含 github.com 子串导致误判
  // （如 example.com/redirect/github.com/foo 不应被识别为 GitHub URL）
  try {
    var u = new URL(url);
    var host = (u.hostname || "").toLowerCase();
    if (!host) return false;
    for (var i = 0; i < GITHUB_HOSTS.length; i++) {
      var h = GITHUB_HOSTS[i];
      if (host === h || host.endsWith("." + h)) return true;
    }
    return false;
  } catch (e) {
    return false;
  }
}

// 检测 URL 是否已经是代理 URL（以已知代理前缀开头）
function isProxyURL(url) {
  for (var i = 0; i < GITHUB_PROXIES.length; i++) {
    if (url.indexOf(GITHUB_PROXIES[i]) === 0) return true;
  }
  return false;
}

function proxyURLWithPrefix(proxyPrefix, rawURL) {
  return proxyPrefix + rawURL.replace(/^https:\/\//, "");
}

function githubRawToJsDelivr(rawURL) {
  try {
    var u = new URL(rawURL);
    if (u.protocol !== "https:" || u.hostname !== "raw.githubusercontent.com") return "";
    var parts = u.pathname.split("/").filter(Boolean);
    if (parts.length < 4) return "";
    var owner = parts[0];
    var repo = parts[1];
    var ref = parts[2];
    var pathStart = 3;
    if (parts[2] === "refs" && (parts[3] === "heads" || parts[3] === "tags") && parts[4]) {
      ref = parts[4];
      pathStart = 5;
    }
    var filePath = parts.slice(pathStart).join("/");
    if (!owner || !repo || !ref || !filePath) return "";
    return "https://cdn.jsdelivr.net/gh/" + owner + "/" + repo + "@" + ref + "/" + filePath + (u.search || "");
  } catch (e) {
    return "";
  }
}

function isBlockedFetchHost(hostname) {
  var host = String(hostname || "").replace(/^\[|\]$/g, "").toLowerCase();
  if (!host) return true;
  if (host === "localhost" || host.endsWith(".localhost") || host.endsWith(".local")) return true;
  var m = host.match(/^(\d+)\.(\d+)\.(\d+)\.(\d+)$/);
  if (m) {
    var a = Number(m[1]);
    var b = Number(m[2]);
    if (a === 0 || a === 10 || a === 127 || a >= 224) return true;
    if (a === 100 && b >= 64 && b <= 127) return true;
    if (a === 169 && b === 254) return true;
    if (a === 172 && b >= 16 && b <= 31) return true;
    if (a === 192 && b === 168) return true;
  }
  if (host === "::1" || host === "::" || /^fe80:/i.test(host) || /^fc/i.test(host) || /^fd/i.test(host) || /^ff/i.test(host)) return true;
  return false;
}

function validateFetchURL(rawURL) {
  var u = new URL(rawURL);
  if (u.protocol !== "http:" && u.protocol !== "https:") throw new Error("只允许 http/https URL: " + rawURL);
  if (isBlockedFetchHost(u.hostname)) throw new Error("不允许拉取 localhost、内网或链路本地地址: " + u.hostname);
  return u;
}

// fetchRaw 带超时（10 秒），避免代理卡死整个请求
// 使用 Promise.race + setTimeout 实现超时
async function fetchRaw(url, userAgent, maxBytes) {
  validateFetchURL(url);
  var ua = userAgent || DEFAULT_USER_AGENT;
  var fetchPromise = fetch(url, {
    headers: { "User-Agent": ua, Accept: "*/*" },
  });
  var timeoutPromise = new Promise(function (_, reject) {
    setTimeout(function () {
      reject(new Error("timeout 10s: " + url));
    }, 10000);
  });
  var resp = await Promise.race([fetchPromise, timeoutPromise]);
  if (!resp.ok) throw new Error("请求 " + url + " 返回状态码 " + resp.status);
  var limit = Number(maxBytes || 0);
  var contentLength = Number(resp.headers.get("content-length") || "0");
  if (limit > 0 && contentLength > limit) throw new Error("远程资源超过大小限制 " + limit + " bytes: " + url);
  var text = await resp.text();
  if (limit > 0 && new TextEncoder().encode(text).length > limit) throw new Error("远程资源超过大小限制 " + limit + " bytes: " + url);
  return text;
}

async function fetchRemoteWithProxy(url, userAgent, maxBytes) {
  validateFetchURL(url);
  var ua = userAgent || DEFAULT_USER_AGENT;
  // 非 GitHub URL 直接 fetch
  if (!isGitHubURL(url)) return fetchRaw(url, ua, maxBytes);
  // 如果 URL 已经是代理 URL（用户手动加了代理前缀），直接 fetch
  if (isProxyURL(url)) return fetchRaw(url, ua, maxBytes);
  // 原始 GitHub URL：依次尝试代理，最后 fallback 直连
  var lastError = null;
  for (var i = 0; i < GITHUB_PROXIES.length; i++) {
    var proxyURL = proxyURLWithPrefix(GITHUB_PROXIES[i], url);
    try {
      var data = await fetchRaw(proxyURL, ua, maxBytes);
      if (data) return data;
    } catch (e) {
      lastError = e;
      /* 代理失败，继续尝试下一个 */
    }
  }
  var jsdelivr = githubRawToJsDelivr(url);
  if (jsdelivr) {
    try {
      return await fetchRaw(jsdelivr, ua, maxBytes);
    } catch (e) {
      lastError = e;
    }
  }
  try {
    return await fetchRaw(url, ua, maxBytes);
  } catch (e) {
    throw lastError || e;
  }
}

function isRemote(path) {
  const lower = path.toLowerCase();
  return lower.startsWith("http://") || lower.startsWith("https://");
}

function resolveScriptPath(scriptPath, baseURL) {
  if (isRemote(scriptPath)) return scriptPath;
  if (!baseURL) return scriptPath;
  if (isRemote(baseURL) && !scriptPath.startsWith("/")) {
    const idx = baseURL.lastIndexOf("/");
    if (idx > 0) return baseURL.slice(0, idx + 1) + scriptPath;
  }
  return scriptPath;
}

// ===================== 脚本改写 =====================

const scriptCache = new Map();

function scriptCacheKey(
  scriptPath,
  fetchScripts,
  phase,
  useStreamScript,
  wrap,
  argument,
) {
  return [
    scriptPath || "",
    fetchScripts ? "1" : "0",
    String(phase),
    useStreamScript ? "1" : "0",
    wrap ? "1" : "0",
    String(argument || ""),
  ].join("|");
}

function scriptLoaderURL(baseURL, scriptPath, moduleBaseURL, phase, wrap, argument, maxScriptBytes) {
  const u = new URL(baseURL);
  u.searchParams.set("script", scriptPath || "");
  if (moduleBaseURL) u.searchParams.set("base", moduleBaseURL);
  u.searchParams.set("phase", String(phase || 0));
  if (wrap) u.searchParams.set("wrap", "true");
  if (String(argument || "").trim()) u.searchParams.set("argument", String(argument || ""));
  if (maxScriptBytes > 0) u.searchParams.set("maxScriptBytes", String(maxScriptBytes));
  return u.toString();
}

function encodeLoaderScript(scriptURL) {
  const js = `async function process(ctx) {
  var key = ${JSON.stringify(String(scriptURL || ""))};
  globalThis.__m2aLoaderCache = globalThis.__m2aLoaderCache || {};
  var fn = globalThis.__m2aLoaderCache[key];
  if (!fn) {
    var res = await Anywhere.http.get(key);
    var source = Anywhere.codec.utf8.decode(res.body || new Uint8Array());
    fn = new Function(source + "\\n; return process;")();
    globalThis.__m2aLoaderCache[key] = fn;
  }
  return await fn(ctx);
}
`;
  return btoa(unescape(encodeURIComponent(js)));
}

// isLikelyNonJSScript 检测下载的脚本内容是否实际上不是 JS 文件（而是模块配置文件）。
// 上游模块的 script-path 可能误指向 .conf/.plugin/.sgmodule 等配置文件，
// 直接执行这些文件会导致 JSCore 语法错误（如 "Unexpected token '*'"）。
// 检测特征：
//   1. 文件后缀为 .conf/.plugin/.sgmodule/.list
//   2. 非 .js 文件：内容含 [General]/[Rule]/[Rewrite]/[MITM]/[Script] 等模块段头
//   3. 非 .js 文件：内容含 hostname= 开头的行（QuantumultX/Loon 配置特征）
// 注意：.js 文件跳过内容检测，因为部分上游 .js 文件是混合格式（开头有 [rewrite_local] 配置 + JS 代码），
// 内容检测会误报。.js 文件只靠后缀判断。
function isLikelyNonJSScript(path, content) {
  // 1. 文件后缀检测（总是生效）
  var lower = String(path || "").toLowerCase();
  if (lower.endsWith(".conf") || lower.endsWith(".plugin") ||
      lower.endsWith(".sgmodule") || lower.endsWith(".list")) {
    return true;
  }
  // .js 文件跳过内容检测，避免混合格式文件误报
  if (lower.endsWith(".js")) {
    return false;
  }
  // 2. 内容特征检测：模块段头（仅对无后缀或未知后缀文件）
  if (content.indexOf("[General]") >= 0 || content.indexOf("[Rule]") >= 0 ||
      content.indexOf("[Rewrite]") >= 0 || content.indexOf("[MITM]") >= 0 ||
      content.indexOf("[Script]") >= 0 || content.indexOf("[Host]") >= 0 ||
      content.indexOf("[URL Rewrite]") >= 0 || content.indexOf("[Header Rewrite]") >= 0) {
    return true;
  }
  // 3. QuantumultX 配置特征：hostname= 开头的行
  var lines = content.split("\n");
  for (var i = 0; i < lines.length; i++) {
    var trimmed = lines[i].trim();
    if (trimmed.startsWith("hostname=") || trimmed.startsWith("hostname =")) {
      return true;
    }
  }
  return false;
}

async function fetchAndEncodeScript(
  scriptPath,
  fetchScripts,
  phase,
  useStreamScript,
  userAgent,
  wrap,
  argument,
  maxScriptBytes,
) {
  const finalSrc = await fetchAndRewriteScript(
    scriptPath,
    fetchScripts,
    phase,
    useStreamScript,
    userAgent,
    wrap,
    argument,
    maxScriptBytes,
  );
  return btoa(unescape(encodeURIComponent(finalSrc)));
}

async function fetchAndRewriteScript(
  scriptPath,
  fetchScripts,
  phase,
  useStreamScript,
  userAgent,
  wrap,
  argument,
  maxScriptBytes,
) {
  const cacheKey = scriptCacheKey(
    scriptPath,
    fetchScripts,
    phase,
    useStreamScript,
    wrap,
    argument,
  );
  if (scriptCache.has(cacheKey)) return scriptCache.get(cacheKey);
  if (!fetchScripts) {
    // 占位符：scriptPath 用 JSON.stringify 转义，防止引号/反斜杠注入
    const placeholder =
      'function process(ctx){Anywhere.log.warning("script not fetched: " + ' +
      JSON.stringify(String(scriptPath)) +
      ");}";
    scriptCache.set(cacheKey, placeholder);
    return placeholder;
  }
  try {
    const src = await fetchRemoteWithProxy(scriptPath, userAgent, maxScriptBytes);
    // 检测上游 script-path 误用 .conf/.plugin/.sgmodule 等非 JS 文件
    // 这些文件是 QuantumultX/Loon/Surge 模块配置，不是 JS 脚本，直接执行会导致语法错误
    if (isLikelyNonJSScript(scriptPath, src)) {
      console.warn(`[警告] script-path "${scriptPath}" 可能不是 JS 文件（含模块配置特征），转换后可能语法错误`);
    }
    // 包装执行模式：不做字符串替换，直接 base64 编码上游脚本
    if (wrap) {
      const wrapped = buildWrappedScript(src, phase, argument || "");
      scriptCache.set(cacheKey, wrapped);
      return wrapped;
    }
    const rewritten = rewriteScriptAPI(src, phase, argument || "");
    const finalSrc = useStreamScript
      ? wrapAsStreamScript(rewritten, phase)
      : rewritten;
    scriptCache.set(cacheKey, finalSrc);
    return finalSrc;
  } catch (e) {
    throw new Error(`下载脚本失败 "${scriptPath}": ${e}`);
  }
}

function encodeInlineScript(rawJS, phase, wrap) {
  // 包装执行模式：将上游脚本源码 base64 编码，在 process(ctx) 中用 new Function()() 执行
  if (wrap) {
    return btoa(unescape(encodeURIComponent(buildWrappedScript(rawJS, phase, ""))));
  }
  const rewritten = rewriteScriptAPI(rawJS, phase, "");
  return btoa(unescape(encodeURIComponent(rewritten)));
}

/**
 * encodeWrappedScript 将上游脚本源码 base64 编码存储，
 * 生成一个包装器 process(ctx) 函数，在运行时构造 $request/$response/$persistentStore/$done 等
 * Loon/Surge 兼容全局变量，然后用 new Function(source)() 执行上游脚本。
 * 这种方式不做字符串替换，能最大程度保持上游脚本的原始逻辑，
 * 适用于 wloc.js 等自包含跨平台脚本。
 */
function buildWrappedScript(rawJS, phase, argument) {
  const phaseCheck = phase === 1 ? "response" : "request";
  // 检测是否需要 async（与 rewriteScriptAPI 保持一致）
  const needsAsync =
    rawJS.includes("$httpClient") ||
    rawJS.includes("$.http") ||
    rawJS.includes("$env.http") ||
    rawJS.includes("await ") ||
    rawJS.includes("async ") ||
    rawJS.includes("$done({response:");

  // 将上游脚本源码 base64 编码
  const upstreamB64 = btoa(unescape(encodeURIComponent(rawJS)));

  // 生成包装器脚本
  const wrapper = `${needsAsync ? "async " : ""}function process(ctx) {
  if (ctx.phase !== "${phaseCheck}") return;
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
      // $argument 先注入原始字符串，供脚本自行解析；空 argument 兜底为空对象 {}，
      // 防止上游脚本使用 Object.keys($argument) 或读取属性时 TypeError（与 Go 侧 BuildWrappedScript 保持一致）
      globalThis.$argument = ${String(argument || "").trim() ? JSON.stringify(String(argument)) : "{}"};
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
        var _upstreamSource = decodeURIComponent(escape(atob("${upstreamB64}")));
        var _polyfillVars = "var URLSearchParams = globalThis.URLSearchParams; var URL = globalThis.URL; var console = globalThis.console; var atob = globalThis.atob; var btoa = globalThis.btoa; var $env = globalThis.$env || { isBoxJS: false, isAnywhere: true };";
        new Function(_polyfillVars + "\\n" + _upstreamSource)();
      } catch (e) {
        Anywhere.log.error("[wrap] upstream script error: " + e);
        finish({});
      }
    }).then(function(out) {
      var response = out.response || out;
      var body = _boxBytes(response.bodyBytes || response.rawBody || response.body);
      if (body.length > 0) ctx.body = body;
    });
  } finally {
    for (var _ti = 0; _ti < _requestTimers.length; _ti++) { if (_requestTimers[_ti]) _requestTimers[_ti].active = false; }
    var _tsIdx = globalThis._requestTimersStack ? globalThis._requestTimersStack.indexOf(_requestTimers) : -1; if (_tsIdx >= 0) globalThis._requestTimersStack.splice(_tsIdx, 1);
    _restoreGlobals(_globalsSnapshot);
  }
}

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
if (typeof globalThis.JSON !== 'undefined') {
  if (typeof globalThis.JSON.parse !== 'function') {}
}
`;
  return wrapper;
}

function encodeWrappedScript(rawJS, phase, argument) {
  return btoa(unescape(encodeURIComponent(buildWrappedScript(rawJS, phase, argument))));
}

function encodeInlineRewriteJS(rawJS, phase) {
  let js = rawJS.trim();
  if (js.startsWith("{") && js.endsWith("}")) js = js.slice(1, -1).trim();
  return encodeInlineScript(js, phase);
}

function rewriteScriptAPI(src, phase, argument) {
  // 检测是否需要 async（含 $httpClient / await / async / $done({response: 等）
  // 注意：需与 encodeWrappedScript 的检测条件保持一致
  const needsAsync =
    src.includes("$httpClient") ||
    src.includes("$.http") ||
    src.includes("$env.http") ||
    src.includes("await ") ||
    src.includes("async ") ||
    src.includes("$done({response:");
  let out = src;
  out = out.replace(/\$request\.url/g, "ctx.url");
  out = out.replace(/\$request\.method/g, "ctx.method");
  // 注意：ctx.headers 是 [[name, value], ...] 数组对格式（per MITM.md），
  // Loon/Surge 的 $request.headers/$response.headers 是 {name: value} 对象格式。
  // 替换为预转换变量 _headersObj（由 wrapAsProcess 在 process 函数体开头注入）
  out = out.replace(/\$request\.headers/g, "_headersObj");
  out = out.replace(/\$response\.headers/g, "_headersObj");
  // 注意：必须先替换更长的标识符（statusCode/bodyBytes），否则会被 $response.status/$response.body 部分匹配
  // $response.statusCode 是 $response.status 的别名 → ctx.status
  out = out.replace(/\$response\.statusCode/g, "ctx.status");
  out = out.replace(/\$response\.status/g, "ctx.status");
  // $response.bodyBytes / $request.bodyBytes 是 Loon 的二进制 body API，直接映射为 ctx.body（Uint8Array）
  out = out.replace(/\$request\.bodyBytes/g, "ctx.body");
  out = out.replace(/\$response\.bodyBytes/g, "ctx.body");
  out = out.replace(/\$request\.body/g, "Anywhere.codec.utf8.decode(ctx.body)");
  out = out.replace(
    /\$response\.body/g,
    "Anywhere.codec.utf8.decode(ctx.body)",
  );
  out = rewriteDoneCalls(out);
  out = out.replace(
    /\$persistentStore\.read\(\s*([^)]+?)\s*\)/g,
    "Anywhere.store.getString($1, true)",
  );
  // $persistentStore.write(val, key) — 处理 null/undefined 的删除语义
  out = out.replace(
    /\$persistentStore\.write\(\s*(null|undefined)\s*,\s*([^)]+?)\s*\)/g,
    "Anywhere.store.delete($2, true)",
  );
  out = out.replace(
    /\$persistentStore\.write\(\s*([^,]+?)\s*,\s*([^)]+?)\s*\)/g,
    (match, val, key) => {
      // 如果 val 已经是 null/undefined 字面量，上面已经处理
      if (val === "null" || val === "undefined") return match;
      // 否则生成运行时判断代码
      return `((${val} === null || ${val} === undefined) ? Anywhere.store.delete(${key}, true) : Anywhere.store.set(${key}, String(${val}), true))`;
    },
  );
  out = out.replace(
    /\$notification\.post\(\s*([^,]+?)\s*,\s*([^,]*?)\s*,\s*([^)]+?)\s*\)/g,
    'Anywhere.log.info($1 + " " + $2 + " " + $3)',
  );
  out = rewriteHttpClientCalls(out);
  out = out.replace(
    /JSON\.parse\(ctx\.body\)/g,
    "JSON.parse(Anywhere.codec.utf8.decode(ctx.body))",
  );
  // 注入 BoxJS Env 兼容层（如果脚本使用了 Env 类或 $.xxx API）
  out = injectBoxJSPolyfill(out);
  out = wrapAsProcess(out, phase, needsAsync, argument || "");
  return out;
}

// boxjsEnvPattern 匹配脚本中使用了 BoxJS Env 类或 $.xxx API 或常见缺失 Web API 的特征。
// 注意：JavaScript 不支持 (?i) 内联标志，使用 /i 全局标志替代（与 Go 侧 boxjsEnvPattern 的 (?i) 语义一致）。
const boxjsEnvPattern = /new\s+Env\s*\(|\$\.(getdata|setdata|getjson|setjson|msg|log|logErr|http|fetch|request|notify|runScript|toURL|setvalue|getvalue|isQuanX|isSurge|isLoon|isNode|wait|done|name)|\$env\s*\.|URLSearchParams|new\s+URL\s*\(|\bfetch\s*\(|\bTextEncoder\b|\bTextDecoder\b|\bHeaders\b|\bRequest\b|\bResponse\b|\bsetTimeout\s*\(|\bsetInterval\s*\(|\bclearTimeout\s*\(|\bclearInterval\s*\(|console\.(log|warn|error|info|debug|assert|trace|table)/i;

// boxjsOnlyPattern 只匹配 BoxJS Env 特征（不包含 Web API 模式），用于细粒度 polyfill 注入检测。
const boxjsOnlyPattern = /new\s+Env\s*\(|\$\.(getdata|setdata|getjson|setjson|msg|log|logErr|http|fetch|request|notify|runScript|toURL|setvalue|getvalue|isQuanX|isSurge|isLoon|isNode|wait|done|name)|\$env\s*\./i;

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

`;

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
    } else if (init && typeof init === 'object' && Array.isArray(init) === false) {
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

`;

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

`;

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

`;

// polyfillTimer 在 needTimer 时注入：setTimeout/clearTimeout/setInterval/clearInterval + console.assert/trace/table。
// 所有 timer 句柄注册到 globalThis._requestTimersStack 栈顶（由 wrapAsProcess/encodeWrappedScript 在 process 开头压栈），
// process() 返回后 finally 块自动标记所有未清除的 timer 为 inactive 并出栈，防止 setInterval 递归 Promise 链无限延续。
// 栈式隔离确保多个规则并发执行时，各自定时器互不干扰。
const polyfillTimer = `if (typeof globalThis.setTimeout === 'undefined') globalThis.setTimeout = function(fn, ms) { var h = { active: true }; var _s = globalThis._requestTimersStack; if (_s && _s.length) _s[_s.length - 1].push(h); Anywhere.wait(ms || 0).then(function() { if (h.active) fn(); }); return h; };
if (typeof globalThis.clearTimeout === 'undefined') globalThis.clearTimeout = function(h) { if (h) h.active = false; };
if (typeof globalThis.setInterval === 'undefined') globalThis.setInterval = function(fn, ms) { var h = { active: true }; var _s = globalThis._requestTimersStack; if (_s && _s.length) _s[_s.length - 1].push(h); (function tick(){ if (!h.active) return; Anywhere.wait(ms || 0).then(function(){ if (!h.active) return; fn(); tick(); }); })(); return h; };
if (typeof globalThis.clearInterval === 'undefined') globalThis.clearInterval = function(h) { if (h) h.active = false; };
if (typeof globalThis.console.assert === 'undefined') globalThis.console.assert = function(cond) { if (!cond) globalThis.console.warn('Assertion failed'); };
if (typeof globalThis.console.trace === 'undefined') globalThis.console.trace = function() { globalThis.console.warn('trace'); };
if (typeof globalThis.console.table === 'undefined') globalThis.console.table = function(obj) { globalThis.console.info(JSON.stringify(obj)); };

`;

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

`;

// polyfillLocalVarsBase 总是注入：console/atob/btoa 的局部变量映射（必须在所有 polyfill 安装之后）。
const polyfillLocalVarsBase = `// 局部变量映射：将 globalThis 上的 polyfill 映射为局部标识符
// 注意：必须在所有 globalThis.XXX 赋值之后执行，否则读到的是 undefined
// （JSCore 中 var 声明会提升，但赋值在原位置执行）
var console = globalThis.console;
var atob = globalThis.atob;
var btoa = globalThis.btoa;
`;

// polyfillLocalVarsURL 在 needURL 时注入：URLSearchParams/URL 的局部变量映射。
const polyfillLocalVarsURL = `var URLSearchParams = globalThis.URLSearchParams;
var URL = globalThis.URL;
`;

// polyfillFooter 总是注入：结束标记（wrapAsProcess 用正则匹配此标记提取 polyfill 代码块）。
const polyfillFooter = `
// === BoxJS Env 兼容层 + Web API Polyfill 结束 ===
`;

/**
 * injectBoxJSPolyfill 为使用 BoxJS Env 类（$.getdata/$.setdata/$.msg 等）的脚本注入兼容层。
 * BoxJS 脚本通常使用 `const $ = new Env('name')` 创建 Env 实例，
 * 然后通过 $.getdata/$.setdata/$.msg/$.http/$.log 等方法与 BoxJS 交互。
 * Anywhere 没有内置 Env 类，因此需要在脚本头部注入一个轻量 polyfill，
 * 将这些调用映射到 Anywhere 的 Anywhere.store/Anywhere.log/Anywhere.http 等 API。
 * 同时注入常用 Web API polyfill（URLSearchParams/URL/console/atob/btoa 等），
 * 因为 Anywhere 的 JavaScriptCore 运行时不提供这些浏览器 API。
 */
function injectBoxJSPolyfill(src) {
  if (!boxjsEnvPattern.test(src)) {
    return src;
  }

  // 细粒度检测：根据脚本使用的 API 特征决定注入哪些 polyfill 模块，减少不必要的体积
  var needURL = src.indexOf('URLSearchParams') !== -1 || src.indexOf('new URL(') !== -1 || src.indexOf('.searchParams') !== -1;
  var needFetch = src.indexOf('fetch(') !== -1 || src.indexOf('TextEncoder') !== -1 || src.indexOf('TextDecoder') !== -1 || src.indexOf('Headers') !== -1 || src.indexOf('Request') !== -1 || src.indexOf('Response') !== -1;
  var needTimer = src.indexOf('setTimeout') !== -1 || src.indexOf('setInterval') !== -1 || src.indexOf('clearTimeout') !== -1 || src.indexOf('clearInterval') !== -1;
  var needBoxJS = boxjsOnlyPattern.test(src);

  // 按需拼接 polyfill 模块（顺序确保依赖关系正确）
  var b = '';
  b += polyfillBase;
  if (needURL) { b += polyfillURL; }
  if (needFetch || needBoxJS) { b += polyfillHelpers; }
  if (needFetch) { b += polyfillFetch; }
  if (needTimer) { b += polyfillTimer; }
  if (needBoxJS) { b += polyfillBoxJS; }
  // 局部变量映射：必须在所有 polyfill 安装之后（JSCore var 声明提升但赋值不提升）
  b += polyfillLocalVarsBase;
  if (needURL) { b += polyfillLocalVarsURL; }
  b += polyfillFooter;

  return src + "\n" + b;
}

function rewriteHttpClientCalls(src) {
  let out = src;
  // 将 $httpClient 回调式调用转为 await + .then() 模式
  // $httpClient.get(url, function(err, resp, body) { ... })
  // → await Anywhere.http.get(url).then(function(res) { var err=null; var resp={status:res.status,headers:res.headers}; var body=Anywhere.codec.utf8.decode(res.body||new Uint8Array()); ... })

  // 构造回调参数变量声明的辅助函数
  // 注意：Anywhere.http 返回的 res.headers 是 [[name, value], ...] 数组对格式，
  // Loon/Surge 的 $httpClient 回调中 resp.headers 是 {name: value} 对象格式，需要转换
  function buildCallbackVars(params) {
    var paramList = params
      .split(",")
      .map(function (p) {
        return p.trim();
      })
      .filter(function (p) {
        return p;
      });
    var varDecls = "";
    if (paramList.length > 0) varDecls += "var " + paramList[0] + " = null;";
    if (paramList.length > 1)
      varDecls +=
        "var " +
        paramList[1] +
        ' = {status: res.status, headers: (function(h){var o={};if(h&&h.forEach){h.forEach(function(p){o[String(p[0]||"")]=String(p[1]||"");});}return o;})(res.headers)};';
    if (paramList.length > 2)
      varDecls +=
        "var " +
        paramList[2] +
        " = Anywhere.codec.utf8.decode(res.body || new Uint8Array());";
    return varDecls;
  }

  // $httpClient.get/put/delete(url, function(err, resp, body) { ... })
  out = out.replace(
    /\$httpClient\.(get|put|delete)\(\s*([^,]+?)\s*,\s*function\s*\(([^)]*)\)\s*\{/g,
    function (match, method, url, params) {
      return (
        "await Anywhere.http." +
        method +
        "(" +
        url +
        ").then(function(res) {" +
        buildCallbackVars(params)
      );
    },
  );
  // 箭头函数形式: $httpClient.get/put/delete(url, (err, resp, body) => {
  out = out.replace(
    /\$httpClient\.(get|put|delete)\(\s*([^,]+?)\s*,\s*\(([^)]*)\)\s*=>\s*\{/g,
    function (match, method, url, params) {
      return (
        "await Anywhere.http." +
        method +
        "(" +
        url +
        ").then(function(res) {" +
        buildCallbackVars(params)
      );
    },
  );
  // 单参数箭头函数: $httpClient.get/put/delete(url, err => {
  out = out.replace(
    /\$httpClient\.(get|put|delete)\(\s*([^,]+?)\s*,\s*(\w+)\s*=>\s*\{/g,
    function (match, method, url, paramName) {
      return (
        "await Anywhere.http." +
        method +
        "(" +
        url +
        ").then(function(res) {var " +
        paramName +
        " = null;"
      );
    },
  );

  // $httpClient.post(url, opts, function(err, resp, body) { ... })
  out = out.replace(
    /\$httpClient\.post\(\s*([^,]+?)\s*,\s*([^,]+?)\s*,\s*function\s*\(([^)]*)\)\s*\{/g,
    function (match, url, opts, params) {
      return (
        "await Anywhere.http.post(" +
        url +
        ", " +
        opts +
        ").then(function(res) {" +
        buildCallbackVars(params)
      );
    },
  );
  // 箭头函数形式
  out = out.replace(
    /\$httpClient\.post\(\s*([^,]+?)\s*,\s*([^,]+?)\s*,\s*\(([^)]*)\)\s*=>\s*\{/g,
    function (match, url, opts, params) {
      return (
        "await Anywhere.http.post(" +
        url +
        ", " +
        opts +
        ").then(function(res) {" +
        buildCallbackVars(params)
      );
    },
  );
  // 单参数箭头函数
  out = out.replace(
    /\$httpClient\.post\(\s*([^,]+?)\s*,\s*([^,]+?)\s*,\s*(\w+)\s*=>\s*\{/g,
    function (match, url, opts, paramName) {
      return (
        "await Anywhere.http.post(" +
        url +
        ", " +
        opts +
        ").then(function(res) {var " +
        paramName +
        " = null;"
      );
    },
  );

  // $httpClient.request(opts, function(err, resp, body) { ... })
  out = out.replace(
    /\$httpClient\.request\(\s*([^,]+?)\s*,\s*function\s*\(([^)]*)\)\s*\{/g,
    function (match, opts, params) {
      return (
        "await Anywhere.http.request(" +
        opts +
        ").then(function(res) {" +
        buildCallbackVars(params)
      );
    },
  );
  // 箭头函数形式
  out = out.replace(
    /\$httpClient\.request\(\s*([^,]+?)\s*,\s*\(([^)]*)\)\s*=>\s*\{/g,
    function (match, opts, params) {
      return (
        "await Anywhere.http.request(" +
        opts +
        ").then(function(res) {" +
        buildCallbackVars(params)
      );
    },
  );

  return out;
}

function rewriteDoneCalls(src) {
  let out = src;
  // $done({}) → Anywhere.done()
  out = out.replace(/\$done\(\s*\{\s*\}\s*\)/g, "Anywhere.done()");
  // $done() → Anywhere.done()
  out = out.replace(/\$done\(\s*\)/g, "Anywhere.done()");
  // $done({body: xxx}) → ctx.body = Anywhere.codec.utf8.encode(xxx); Anywhere.done()
  out = out.replace(
    /\$done\(\s*\{\s*body\s*:\s*([^}]+?)\s*\}\s*\)/g,
    "ctx.body = Anywhere.codec.utf8.encode($1); Anywhere.done()",
  );
  // $done({ body }) ES6 shorthand
  out = out.replace(
    /\$done\(\s*\{\s*body\s*\}\s*\)/g,
    "ctx.body = Anywhere.codec.utf8.encode(body); Anywhere.done()",
  );

  // $done({response: {...}}) — 需要处理嵌套大括号
  // 使用标记替换法来正确匹配嵌套大括号
  out = out.replace(
    /\$done\(\s*\{\s*response\s*:\s*/g,
    "__DONE_RESPONSE_START__",
  );
  out = out.replace(
    /__DONE_RESPONSE_START__(\{[\s\S]*?\}\s*\})\s*\)/g,
    function (match, responseObj) {
      // 验证大括号是否平衡
      var depth = 0;
      var endIdx = -1;
      for (var i = 0; i < responseObj.length; i++) {
        if (responseObj[i] === "{") depth++;
        else if (responseObj[i] === "}") depth--;
        if (depth === 0) {
          endIdx = i;
          break;
        }
      }
      if (endIdx >= 0) {
        var inner = responseObj.slice(0, endIdx + 1);
        return "Anywhere.respond(" + inner + ")";
      }
      return match;
    },
  );

  // $done({...}) 其他情况 → Anywhere.done()
  out = out.replace(/\$done\(\s*\{/g, "__DONE_OBJECT_START__{");
  out = out.replace(
    /__DONE_OBJECT_START__\{[\s\S]*?\}\s*\)/g,
    function (match) {
      var inner = match.slice("__DONE_OBJECT_START__".length, -1).trim();
      var depth = 0;
      var balanced = true;
      for (var i = 0; i < inner.length; i++) {
        if (inner[i] === "{") depth++;
        else if (inner[i] === "}") depth--;
        if (depth < 0) {
          balanced = false;
          break;
        }
      }
      if (balanced && depth === 0) return "Anywhere.done()";
      return match;
    },
  );

  // $done(variable) → _doneVar(variable)
  // 处理剩余的 $done 调用：参数不是对象字面量（如 $done(r)、$done(result)、$done(null)）
  // 这些无法静态分析，需要运行时适配函数 _doneVar 处理 body/headers/status/response 字段
  // 注意：此正则不匹配 $done() 和 $done({...})（已在前面的步骤中处理完毕）
  out = out.replace(/\$done\(\s*([^){}\s][^)]*?)\s*\)/g, "_doneVar($1)");

  return out;
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
`;

// doneVarAdapter 是注入到 rewrite 模式脚本中的 $done(variable) 运行时适配函数。
// 当上游脚本使用 $done(r) 形式（r 是运行时变量）时，rewriteDoneCalls 会将其改写为 _doneVar(r)。
// _doneVar 负责将 Loon/Surge 的 {body, headers, status, response} 对象语义映射到 Anywhere API。
// 注意：headers 需要从 {name: value} 对象转换为 [[name, value], ...] 数组对格式，复用 _headersToPairs。
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
`;

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
`;

function wrapAsProcess(src, phase, needsAsync, argument) {
  const trimmed = src.trim();
  const asyncKw = needsAsync ? "async " : "";
  const phaseCheck = phase === 1 ? "response" : "request";

  // 检测是否注入了 BoxJS polyfill（由 injectBoxJSPolyfill 添加的标记）
  const hasPolyfill = trimmed.includes("_BoxJS_Env_injected");

  // 局部变量映射已移至 polyfill 字符串末尾（在 globalThis.XXX 赋值之后）
  // 注意：之前在这里注入 var URLSearchParams = globalThis.URLSearchParams; 会导致
  //       赋值在 polyfill 安装之前执行，读到 undefined。
  //       现在在 polyfill 字符串中所有 globalThis.XXX 赋值之后才声明 var，顺序正确。
  const localVarMappings = "";

  // headers 预转换：将 ctx.headers（[[name, value], ...] 数组对）转换为 {name: value} 对象
  // Loon/Surge 的 $request.headers/$response.headers 是对象格式，rewriteScriptAPI 中替换为 _headersObj
  // 注意：必须在 process 函数体最开头执行，确保上游脚本使用前已初始化
  // 如果需要注入 $request/$response 对象，也需要 _headersObj（因为 headers 字段要用转换后的对象）
  const needsHeadersObj = trimmed.includes("_headersObj") || trimmed.includes("$request") || trimmed.includes("$response");
  const headersInject = needsHeadersObj
    ? '  var _headersObj = (function(h){var o={};if(h&&h.forEach){h.forEach(function(p){o[String(p[0]||"")]=String(p[1]||"");});}return o;})(ctx.headers);\n'
    : "";
  const argumentInject = String(argument || "").trim()
    ? `  globalThis.$argument = ${JSON.stringify(String(argument || ""))};\n  var $argument = globalThis.$argument;\n`
    : "";

  // 检测是否使用了 _doneVar（由 rewriteDoneCalls 将 $done(variable) 改写而来）
  // 如果是，注入运行时适配函数，将 {body, headers, status, response} 映射到 Anywhere API
  const doneVarInject = trimmed.includes("_doneVar(") ? doneVarAdapter : "";

  // 检测是否需要注入 $httpClient 适配对象
  // rewriteHttpClientCalls 的正则只能匹配内联 function/箭头函数形式，
  // 上游脚本若使用 $httpClient.get(var, var) 等变量参数形式则无法改写。
  // 此时注入兼容的 $httpClient 对象定义，让上游脚本直接调用。
  // 检测条件：只要 $httpClient 标识符还存在（说明有未改写的调用），就注入 adapter。
  // 注意：不能用 !includes("Anywhere.http") 判断，因为 BoxJS polyfill 中也会出现 Anywhere.http，
  // 与上游 $httpClient 改写无关，会导致漏注入。
  const needsHttpClientVar = trimmed.includes("$httpClient");
  const httpClientInject = needsHttpClientVar ? httpClientAdapter : "";

  // headersHelpersInject：doneVarAdapter 和 httpClientAdapter 共享 _headersToObj/_headersToPairs。
  // 只要其中任一被注入，就必须先注入共享 helper（顺序：helpers 在 adapter 之前）。
  const headersHelpersInject =
    doneVarInject !== "" || httpClientInject !== "" ? headersHelpers : "";

  // 检测是否需要注入 $request/$response 对象定义
  // 上游脚本可能使用 typeof $request、$response.hasOwnProperty(...) 等形式，
  // 这些形式中 $request/$response 作为变量本身出现，无法通过属性替换处理。
  // 需要 注入对象定义，使这些引用能正常工作。
  // 注意：_headersObj 必须在此注入之前已定义（headersInject 已处理）。
  const needsReqRespVar = trimmed.includes("$request") || trimmed.includes("$response");
  const reqRespInject = needsReqRespVar
    ? `  var $request = {
    url: ctx.url || '',
    method: ctx.method || 'GET',
    headers: typeof _headersObj !== 'undefined' ? _headersObj : {},
    body: Anywhere.codec.utf8.decode(ctx.body || new Uint8Array()),
    bodyBytes: ctx.body
  };
  var $response = {
    status: ctx.status || 200,
    statusCode: ctx.status || 200,
    headers: typeof _headersObj !== 'undefined' ? _headersObj : {},
    body: Anywhere.codec.utf8.decode(ctx.body || new Uint8Array()),
    bodyBytes: ctx.body,
    rawBody: ctx.body
  };
`
    : "";

  // 检测是否需要 globalThis 隔离（上游脚本可能往 globalThis 写 $loon/$environment 等）
  const needsIsolation =
    trimmed.includes("$loon") ||
    trimmed.includes("$environment") ||
    trimmed.includes("$script") ||
    trimmed.includes("$argument") ||
    trimmed.includes("globalThis.$");

  // 检测是否需要 timer 清理（setInterval 递归 Promise 链在 process 返回后可能无限延续）
  const needsTimerCleanup = trimmed.includes("setTimeout") || trimmed.includes("setInterval");

  // 检测是否需要 GC nudge（脚本会产生大量 JSCore 堆对象，如 JS 字符串/JSON 对象）
  // Anywhere 的 JSGarbageCollect 只在 mitmScriptTypedArrayBytes >= 16MB 时触发，
  // 但 JS 字符串（utf8.decode 结果）不计入此预算，JSCore 堆增长依赖引擎自身 GC。
  // 注意：原 GC nudge（finally 中分配 1MB randomBytes 触发 JSGarbageCollect）已移除，
  // 因为在接近 50MB 内存临界值时，1MB 分配会加剧峰值压力，反而触发 VPN 进程重启。

  // 清理代码：globalThis 动态快照 + request-scoped timer 清理
  // 关键改进：
  //   1. 动态快照 Object.getOwnPropertyNames 替代固定 10 个名称，捕获上游脚本所有 globalThis 写入
  //   2. _POLYFILL_NAMES 排除列表保护 polyfill 安装的属性，避免每次请求重新安装
  //   3. _requestTimers 注册表在 finally 中批量清理，防止 setInterval 泄漏
  const needsCleanup = needsIsolation || needsTimerCleanup;
  var isoPrefix = "";
  var isoSuffix = "";
  if (needsCleanup) {
    isoPrefix = "  var _requestTimers = [];\n";
    isoPrefix += "  globalThis._requestTimersStack = globalThis._requestTimersStack || [];\n";
    isoPrefix += "  globalThis._requestTimersStack.push(_requestTimers);\n";
    if (needsIsolation) {
      isoPrefix += "  var _globalsSnapshot = {};\n";
      isoPrefix += '  var _POLYFILL_NAMES = ["console", "URLSearchParams", "URL", "TextEncoder", "TextDecoder", "Headers", "Request", "Response", "fetch", "setTimeout", "clearTimeout", "setInterval", "clearInterval", "atob", "btoa", "Env", "_wrapBoxJSResponse", "_wrapBoxJSRequest", "_boxBytes", "_boxHeaderPairs", "_boxRequest", "$env", "_requestTimersStack"];\n';
      isoPrefix += "  function _saveGlobals(snapshot) { var names = Object.getOwnPropertyNames(globalThis); for (var i = 0; i < names.length; i++) { var name = names[i]; if (_POLYFILL_NAMES.indexOf(name) >= 0) continue; snapshot[name] = globalThis[name]; } }\n";
      isoPrefix += "  function _restoreGlobals(snapshot) { var names = Object.getOwnPropertyNames(globalThis); for (var i = 0; i < names.length; i++) { var name = names[i]; if (_POLYFILL_NAMES.indexOf(name) >= 0) continue; if (!snapshot.hasOwnProperty(name)) delete globalThis[name]; else globalThis[name] = snapshot[name]; } }\n";
      isoPrefix += "  _saveGlobals(_globalsSnapshot);\n";
    }
    isoPrefix += "  try {\n";

    isoSuffix = "\n  } finally {\n";
    if (needsTimerCleanup) {
      isoSuffix += "    for (var _ti = 0; _ti < _requestTimers.length; _ti++) { if (_requestTimers[_ti]) _requestTimers[_ti].active = false; }\n";
      isoSuffix += "    var _tsIdx = globalThis._requestTimersStack ? globalThis._requestTimersStack.indexOf(_requestTimers) : -1; if (_tsIdx >= 0) globalThis._requestTimersStack.splice(_tsIdx, 1);\n";
    }
    if (needsIsolation) {
      isoSuffix += "    _restoreGlobals(_globalsSnapshot);\n";
    }
    isoSuffix += "  }\n";
  }

  // 已有 process 函数定义时，将 polyfill 和局部变量映射注入到函数体内部
  if (/^function\s+process\s*\(\s*ctx\s*\)/m.test(trimmed)) {
    let out = trimmed;
    if (needsAsync && !out.startsWith("async ")) out = "async " + out;
    // 注入 headers 预转换变量、headers 共享 helper、$request/$response 对象、$argument、_doneVar 适配函数、$httpClient 适配对象（必须在最开头，polyfill 之前）
    // 顺序：headersHelpers 必须在 doneVarInject/httpClientInject 之前（adapter 引用 helper）
    const injectPrefix = headersInject + headersHelpersInject + reqRespInject + argumentInject + doneVarInject + httpClientInject;
    if (injectPrefix) {
      out = out.replace(
        /(function\s+process\s*\(\s*ctx\s*\)\s*\{)/,
        "$1\n" + injectPrefix,
      );
    }
    // 将 polyfill 代码（process 函数外部）移到 process 函数体内部
    if (hasPolyfill) {
      // 提取 polyfill 部分（从 _BoxJS_Env_injected 到 === 结束标记）
      const polyfillMatch = out.match(
        /\/\/ === BoxJS Env 兼容层[\s\S]*?\/\/ === BoxJS Env 兼容层 \+ Web API Polyfill 结束 ===\n/,
      );
      const polyfillCode = polyfillMatch ? polyfillMatch[0] : "";
      // 从原位置移除 polyfill
      if (polyfillCode) out = out.replace(polyfillCode, "");
      // 注入到 process 函数体开头
      const injectCode =
        localVarMappings +
        (polyfillCode ? polyfillCode.replace(/\n/g, "\n  ") : "");
      out = out.replace(
        /(function\s+process\s*\(\s*ctx\s*\)\s*\{)/,
        "$1\n" + injectCode,
      );
    }
    if (isoPrefix) {
      out = out.replace(
        /(function\s+process\s*\(\s*ctx\s*\)\s*\{)/,
        "$1\n" + isoPrefix,
      );
      const lastBrace = out.lastIndexOf("}");
      if (lastBrace > 0)
        out = out.slice(0, lastBrace) + isoSuffix + out.slice(lastBrace);
    }
    return out;
  }
  if (/^async\s+function\s+process\s*\(\s*ctx\s*\)/m.test(trimmed)) {
    let out = trimmed;
    // 注入 headers 预转换变量、headers 共享 helper、$request/$response 对象、$argument、_doneVar 适配函数、$httpClient 适配对象
    // 顺序：headersHelpers 必须在 doneVarInject/httpClientInject 之前（adapter 引用 helper）
    const injectPrefix = headersInject + headersHelpersInject + reqRespInject + argumentInject + doneVarInject + httpClientInject;
    if (injectPrefix) {
      out = out.replace(
        /(async\s+function\s+process\s*\(\s*ctx\s*\)\s*\{)/,
        "$1\n" + injectPrefix,
      );
    }
    if (hasPolyfill) {
      const polyfillMatch = out.match(
        /\/\/ === BoxJS Env 兼容层[\s\S]*?\/\/ === BoxJS Env 兼容层 \+ Web API Polyfill 结束 ===\n/,
      );
      const polyfillCode = polyfillMatch ? polyfillMatch[0] : "";
      if (polyfillCode) out = out.replace(polyfillCode, "");
      const injectCode =
        localVarMappings +
        (polyfillCode ? polyfillCode.replace(/\n/g, "\n  ") : "");
      out = out.replace(
        /(async\s+function\s+process\s*\(\s*ctx\s*\)\s*\{)/,
        "$1\n" + injectCode,
      );
    }
    if (isoPrefix) {
      out = out.replace(
        /(async\s+function\s+process\s*\(\s*ctx\s*\)\s*\{)/,
        "$1\n" + isoPrefix,
      );
      const lastBrace = out.lastIndexOf("}");
      if (lastBrace > 0)
        out = out.slice(0, lastBrace) + isoSuffix + out.slice(lastBrace);
    }
    return out;
  }
  if (/^function\s+run\s*\(\s*\)/m.test(trimmed)) {
    return `${asyncKw}function process(ctx) {
  if (ctx.phase !== "${phaseCheck}") return;
${headersInject}${headersHelpersInject}${reqRespInject}${argumentInject}${doneVarInject}${httpClientInject}${localVarMappings}${isoPrefix}  try { run(); } catch (e) { Anywhere.log.warning("script error: " + e); }${isoSuffix}
}
${trimmed}`;
  }
  return `${asyncKw}function process(ctx) {
  if (ctx.phase !== "${phaseCheck}") return;
${headersInject}${headersHelpersInject}${reqRespInject}${argumentInject}${doneVarInject}${httpClientInject}${localVarMappings}${isoPrefix}  try {
${indent(trimmed, "    ")}
  } catch (e) {
    Anywhere.log.warning("script error: " + e);
  }${isoSuffix}
  Anywhere.done();
}`;
}

function indent(s, prefix) {
  return s
    .split("\n")
    .map((l) => (l ? prefix + l : l))
    .join("\n");
}

function buildRedirectScript(pattern, captureURL, _status) {
  const js = `function process(ctx) {
  if (ctx.phase !== "request" || !ctx.url) return;
  var m = ctx.url.match(${jsRegexLiteral(pattern)});
  if (m) {
    var url = ${jsCaptureReplace(captureURL)};
    Anywhere.respond({ status: 302, headers: [["Location", url]] });
  }
}`;
  return btoa(unescape(encodeURIComponent(js)));
}

function jsRegexLiteral(pattern) {
  const escaped = pattern.replace(/\//g, "\\/");
  return "/" + escaped + "/";
}

function jsCaptureReplace(url) {
  let buf = '"';
  let i = 0;
  while (i < url.length) {
    if (
      url[i] === "$" &&
      i + 1 < url.length &&
      url[i + 1] >= "1" &&
      url[i + 1] <= "9"
    ) {
      buf += '" + m[' + url[i + 1] + '] + "';
      i += 2;
    } else {
      if (url[i] === '"' || url[i] === "\\") buf += "\\";
      buf += url[i];
      i++;
    }
  }
  buf += '"';
  return buf;
}

function wrapAsStreamScript(rewrittenSrc, phase) {
  const phaseCheck = phase === 1 ? "response" : "request";
  const inner = extractProcessBody(rewrittenSrc) || rewrittenSrc;
  return `async function process(ctx) {
  if (ctx.phase !== "${phaseCheck}" || !ctx.body) return;
  try {
${indent(inner, "    ")}
  } catch (e) { Anywhere.log.warning("stream process failed: " + e); }
}`;
}

function extractProcessBody(src) {
  const trimmed = src.trim();
  if (!trimmed.includes("function process(ctx)")) return "";
  const firstBrace = trimmed.indexOf("{");
  const lastBrace = trimmed.lastIndexOf("}");
  if (firstBrace < 0 || lastBrace < 0 || lastBrace <= firstBrace) return "";
  return trimmed.slice(firstBrace + 1, lastBrace).trim();
}

// ===================== 解析器 =====================

function detectSource(content, filename) {
  const lowerName = filename.toLowerCase();
  if (lowerName.endsWith(".plugin")) return "loon";
  if (lowerName.endsWith(".sgmodule")) return "surge";
  if (lowerName.endsWith(".conf")) return "quantumultx";
  const lowerContent = content.toLowerCase();
  if (
    lowerContent.includes("[url rewrite]") ||
    lowerContent.includes("[header rewrite]") ||
    lowerContent.includes("[map local]")
  )
    return "surge";
  if (lowerContent.includes("[rewrite]") || lowerContent.includes("[argument]"))
    return "loon";
  if (content.includes("[MitM]")) return "loon";
  if (content.includes("[MITM]")) return "surge";
  // 无段头且含 QX 行式规则特征时识别为 QuantumultX
  if (
    /^\S+\s+url\s+(reject|script-response-body|script-request-body|echo-response|jsonjq-response-body|response-body|302|307)/im.test(
      content,
    )
  )
    return "quantumultx";
  return "loon";
}

function parseLoonRules(body) {
  const rules = [];
  for (const line of body.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#") || trimmed.startsWith("//"))
      continue;
    const fields = splitCSVFields(trimmed);
    if (fields.length < 2) continue;
    const r = {
      raw: trimmed,
      type: fields[0].toUpperCase().trim(),
      value: fields[1].trim(),
      action: "",
      options: [],
    };
    if (fields.length >= 3) r.action = fields[2].toUpperCase().trim();
    if (fields.length > 3) r.options = fields.slice(3);
    rules.push(r);
  }
  return rules;
}

function parseLoonRewriteAction(rest) {
  const args = {};
  let [action, remain] = splitFirstWhitespace(rest);
  action = action.toLowerCase().trim();
  if (action === "url") {
    [action, remain] = splitFirstWhitespace(remain);
    action = action.toLowerCase().trim();
  }
  // 兼容紧贴写法：302$1$3 / 307https://... → 动作与目标 URL 之间补出空格语义
  if (action.startsWith("302") && action.length > 3) {
    remain = action.slice(3) + (remain ? " " + remain.trim() : "");
    action = "302";
  } else if (action.startsWith("307") && action.length > 3) {
    remain = action.slice(3) + (remain ? " " + remain.trim() : "");
    action = "307";
  }
  // 跳过 - / _ 占位符（部分模块写成 "pattern - reject" 或 "pattern _ reject"）
  if (action === "-" || action === "_") {
    [action, remain] = splitFirstWhitespace(remain);
    action = action.toLowerCase().trim();
  }
  switch (action) {
    case "reject":
    case "reject-200":
    case "reject-dict":
    case "reject-array":
    case "reject-img":
      return { action, args, rawJS: "" };
    case "reject-data":
      args.data = remain.trim();
      return { action, args, rawJS: "" };
    case "302":
    case "307":
      args.url = remain.trim();
      return { action, args, rawJS: "" };
    case "transparent":
    case "rewrite":
      args.url = remain.trim();
      return { action, args, rawJS: "" };
    case "mock-response-body":
      parseKVArgs(remain, args);
      return { action, args, rawJS: "" };
    case "response-body-json-del":
    case "response-body-json-add":
    case "response-body-json-replace": {
      const tokens = splitWhitespace(remain);
      if (tokens.length >= 1) args.path = tokens[0];
      if (tokens.length >= 2) args.value = tokens.slice(1).join(" ");
      return { action, args, rawJS: "" };
    }
    case "response-body-json-delete-recursive": {
      const tokens = splitWhitespace(remain);
      if (tokens.length >= 1) args.key = tokens[0];
      return { action, args, rawJS: "" };
    }
    case "response-body-json-replace-recursive": {
      const tokens = splitWhitespace(remain);
      if (tokens.length >= 1) args.key = tokens[0];
      if (tokens.length >= 2) args.value = tokens.slice(1).join(" ");
      return { action, args, rawJS: "" };
    }
    case "response-body-json-remove-where-key-exists": {
      const tokens = splitWhitespace(remain);
      if (tokens.length >= 1) args.path = tokens[0];
      if (tokens.length >= 2) args.key = tokens[1];
      return { action, args, rawJS: "" };
    }
    case "response-body-json-remove-where-field-in": {
      const tokens = splitWhitespace(remain);
      if (tokens.length >= 1) args.path = tokens[0];
      if (tokens.length >= 2) args.field = tokens[1];
      if (tokens.length >= 3) args.values = tokens.slice(2).join(" ");
      return { action, args, rawJS: "" };
    }
    case "request-header":
    case "request-body":
    case "response-body":
      return { action, args, rawJS: remain.trim() };
    case "header-add":
    case "header-replace":
    case "header-del":
    case "request-header-add":
    case "request-header-replace":
    case "request-header-del":
    case "response-header-add":
    case "response-header-replace":
    case "response-header-del":
    case "_header-add":
    case "_header-replace":
    case "_header-del":
    case "_request-header-add":
    case "_request-header-replace":
    case "_request-header-del":
    case "_response-header-add":
    case "_response-header-replace":
    case "_response-header-del":
      return parseHeaderRewriteShortcut(action, remain, args);
    case "response-body-replace-regex": {
      const [search, repl] = splitFirstWhitespace(remain);
      args.search = trimQuotes(search);
      args.replacement = trimQuotes(repl);
      return { action, args, rawJS: "" };
    }
    default:
      args._raw = remain.trim();
      return { action, args, rawJS: "" };
  }
}

function parseLoonRewrites(body) {
  const rules = [];
  for (const line of body.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#") || trimmed.startsWith("//"))
      continue;
    const r = {
      raw: trimmed,
      pattern: "",
      action: "",
      args: {},
      rawJS: "",
    };
    const [pattern, rest] = splitRewritePatternAndRest(trimmed);
    r.pattern = pattern;
    if (!rest) {
      rules.push(r);
      continue;
    }
    const { action, args, rawJS } = parseLoonRewriteAction(rest);
    r.action = action;
    r.args = args;
    r.rawJS = rawJS;
    rules.push(r);
  }
  return rules;
}

function parseLoonScriptLine(line) {
  const [phase, rest] = splitFirstWhitespace(line);
  if (!rest) return null;
  const [pattern, params] = splitFirstWhitespace(rest);
  const s = {
    raw: line,
    pattern,
    phase: 0,
    scriptPath: "",
    requiresBody: false,
    binaryBody: false,
    argument: "",
    tag: "",
    maxSize: 0,
    engine: "",
  };
  switch (phase.toLowerCase().trim()) {
    case "http-request":
      s.phase = 0;
      break;
    case "http-response":
      s.phase = 1;
      break;
    case "cron":
      return null;
    default:
      return null;
  }
  const tokens = splitCSVFields(params);
  const { args } = parseKeyValueList(tokens);
  if (!args["script-path"]) {
    const mixed = parseMixedURLScriptParams(args, line);
    if (mixed) return mixed;
  }
  s.scriptPath = args["script-path"] || "";
  s.tag = args.tag || "";
  s.argument = args.argument || "";
  s.engine = args.engine || "";
  if (args["requires-body"])
    s.requiresBody =
      args["requires-body"].toLowerCase() === "true" ||
      args["requires-body"] === "1";
  if (args["binary-body-mode"])
    s.binaryBody =
      args["binary-body-mode"].toLowerCase() === "true" ||
      args["binary-body-mode"] === "1";
  if (args["max-size"]) {
    const n = parseInt(args["max-size"], 10);
    if (!isNaN(n)) s.maxSize = n;
  }
  return s;
}

function parseMixedURLScriptParams(args, raw) {
  const patternValue = String(args.pattern || "").trim();
  const lower = patternValue.toLowerCase();
  const marker = " url ";
  const idx = lower.indexOf(marker);
  if (idx < 0) return null;
  const pattern = patternValue.slice(0, idx).trim();
  const right = patternValue.slice(idx + marker.length).trim();
  if (!String(args.type || "").trim() || !String(args.pattern || "").trim())
    return null;
  const tokens = splitWhitespace(right);
  if (tokens.length < 2) return null;
  const action = tokens[0].toLowerCase().trim();
  const parsedScript = splitScriptPathAndTrailingArgs(tokens[1] || "");
  const scriptPath = parsedScript.scriptPath;
  const trailingTokens = parsedScript.trailingArgs.slice();
  for (const token of tokens.slice(2)) {
    if (token.includes("=")) trailingTokens.push(token);
  }
  if (trailingTokens.length) {
    const extra = parseKeyValueList(trailingTokens).args;
    Object.assign(args, extra);
  }
  if (!scriptPath) return null;
  const s = {
    raw,
    pattern,
    phase: 0,
    scriptPath,
    requiresBody:
      args["requires-body"] === "1" ||
      String(args["requires-body"] || "").toLowerCase() === "true",
    binaryBody:
      args["binary-body-mode"] === "1" ||
      String(args["binary-body-mode"] || "").toLowerCase() === "true",
    argument: args.argument || "",
    tag: args.tag || "",
    maxSize: 0,
    engine: args.engine || "",
  };
  if (args["max-size"]) {
    const n = parseInt(args["max-size"], 10);
    if (!isNaN(n)) s.maxSize = n;
  }
  if (action === "script-request-body" || action === "script-request-header") {
    s.phase = 0;
    return s;
  }
  if (
    action === "script-response-body" ||
    action === "script-response-header" ||
    action === "script-analyze-echo-response"
  ) {
    s.phase = 1;
    return s;
  }
  return null;
}

function parseLoonScripts(body) {
  const scripts = [];
  for (const line of body.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#") || trimmed.startsWith("//"))
      continue;
    const s = parseLoonScriptLine(trimmed);
    if (s) scripts.push(s);
  }
  return scripts;
}

function parseLoonArguments(body) {
  const args = [];
  for (const line of body.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#") || trimmed.startsWith("//"))
      continue;
    const arg = parseArgumentLine(trimmed);
    if (arg) args.push(arg);
  }
  return args;
}

function parseArgumentLine(line) {
  const raw = String(line || "").trim();
  const idx = raw.indexOf("=");
  if (idx <= 0) return null;
  const name = raw.slice(0, idx).trim();
  if (!name || /[{},]/.test(name)) return null;
  const fields = splitCSVFields(raw.slice(idx + 1));
  const first = String(fields[0] || "").trim().toLowerCase();
  const knownType = /^(?:switch|input|text|string|number|select|checkbox)$/i.test(first);
  const type = knownType ? first : "string";
  const defaultValue = normalizeArgumentValueForType(knownType ? (fields[1] || "") : (fields[0] || ""), type);
  const arg = {
    key: name,
    value: raw.slice(idx + 1).trim(),
    type,
    defaultValue,
    options: [],
    tag: "",
    desc: "",
    raw,
  };
  const optionFields = [];
  for (const field of fields.slice(knownType ? 1 : 0)) {
    const pairIdx = String(field || "").indexOf("=");
    if (pairIdx > 0) {
      const key = field.slice(0, pairIdx).trim().toLowerCase();
      const val = trimQuotes(field.slice(pairIdx + 1).trim());
      if (key === "tag") arg.tag = val;
      if (key === "desc" || key === "description") arg.desc = val;
      continue;
    }
    const value = normalizeArgumentValueForType(field, type);
    if (String(value).trim()) optionFields.push(value);
  }
  if (type === "select") {
    arg.options = dedupStrings(optionFields);
  } else if (type === "switch" || type === "checkbox") {
    arg.options = dedupStrings(optionFields);
    if (!arg.options.length) arg.options = ["true", "false"];
    else if (arg.options.length === 1) arg.options.push(argumentEnabled(arg.options[0]) ? "false" : "true");
  }
  return arg;
}

function parseMetadataArguments(rawArguments, rawDescriptions) {
  const descriptions = parseMetadataArgumentDescriptions(rawDescriptions || "");
  const out = [];
  for (const field of splitCSVFields(String(rawArguments || ""))) {
    const idx = field.indexOf(":");
    if (idx <= 0) continue;
    const name = trimQuotes(field.slice(0, idx).trim());
    if (!name || /[{}]/.test(name)) continue;
    let defaultValue = trimQuotes(field.slice(idx + 1).trim());
    let type = "string";
    if (/^(?:true|false|1|0|yes|no|on|off)$/i.test(defaultValue)) {
      type = "switch";
      defaultValue = normalizeArgumentValueForType(defaultValue, type);
    }
    out.push({
      key: name,
      value: defaultValue,
      type,
      defaultValue,
      options: type === "switch" ? ["true", "false"] : [],
      tag: name,
      desc: descriptions[name] || "",
      raw: field.trim(),
    });
  }
  return out;
}

function parseMetadataArgumentDescriptions(raw) {
  const out = {};
  for (const line of String(raw || "").replace(/\\n/g, "\n").split("\n")) {
    const idx = line.indexOf(":");
    if (idx <= 0) continue;
    const name = trimQuotes(line.slice(0, idx).trim());
    if (name) out[name] = line.slice(idx + 1).trim();
  }
  return out;
}

function parseLoonMitM(body) {
  for (const line of body.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    const idx = trimmed.indexOf("=");
    if (idx <= 0) continue;
    const key = trimmed.slice(0, idx).trim().toLowerCase();
    if (key === "hostname") return normalizeHostnames(trimmed.slice(idx + 1));
  }
  return [];
}

function parseLoon(content) {
  const m = {
    source: "loon",
    name: "",
    desc: "",
    author: "",
    homepage: "",
    date: "",
    rawMeta: {},
    hostnames: [],
    contentType: "",
    rules: [],
    rewrites: [],
    scripts: [],
    headerRWs: [],
    mapLocals: [],
    arguments: [],
  };
  for (const sec of splitSections(content)) {
    switch (sec.name) {
      case "__meta__": {
        const meta = parseMeta(sec.body);
        m.rawMeta = meta;
        if (meta.name) m.name = meta.name;
        if (meta.desc) m.desc = meta.desc;
        if (meta.author) m.author = meta.author;
        if (meta.homepage) m.homepage = meta.homepage;
        if (meta.date) m.date = meta.date;
        m.arguments = mergeArguments(m.arguments, parseMetadataArguments(meta.arguments, meta["arguments-desc"]));
        break;
      }
      case "Rule":
        m.rules.push(...parseLoonRules(sec.body));
        break;
      case "Rewrite":
        m.rewrites.push(...parseLoonRewrites(sec.body));
        break;
      case "Script":
        m.scripts.push(...parseLoonScripts(sec.body));
        break;
      case "Argument":
        m.arguments.push(...parseLoonArguments(sec.body));
        break;
      case "MitM":
        m.hostnames.push(...parseLoonMitM(sec.body));
        break;
    }
  }
  return m;
}

function parseSurgeURLRewriteAction(rest) {
  const args = {};
  let [action, remain] = splitFirstWhitespace(rest);
  action = action.toLowerCase().trim();
  if (action === "url") {
    [action, remain] = splitFirstWhitespace(remain);
    action = action.toLowerCase().trim();
  }
  // 兼容紧贴写法：302$1$3 / 307https://... → 动作与目标 URL 之间补出空格语义
  if (action.startsWith("302") && action.length > 3) {
    remain = action.slice(3) + (remain ? " " + remain.trim() : "");
    action = "302";
  } else if (action.startsWith("307") && action.length > 3) {
    remain = action.slice(3) + (remain ? " " + remain.trim() : "");
    action = "307";
  }
  // 跳过 - / _ 占位符（部分模块写成 "pattern - reject" 或 "pattern _ reject"）
  if (action === "-" || action === "_") {
    [action, remain] = splitFirstWhitespace(remain);
    action = action.toLowerCase().trim();
  }
  switch (action) {
    case "reject":
    case "reject-200":
    case "reject-dict":
    case "reject-array":
    case "reject-img":
      return { action, args, rawJS: "" };
    case "302":
    case "307":
      args.url = remain.trim();
      return { action, args, rawJS: "" };
    case "request-header":
    case "request-body":
    case "response-body":
      return { action, args, rawJS: remain.trim() };
    case "_request-header":
    case "_request-body":
    case "_response-body":
      return { action: action.slice(1), args, rawJS: remain.trim() };
    case "header-add":
    case "header-replace":
    case "header-del":
    case "request-header-add":
    case "request-header-replace":
    case "request-header-del":
    case "response-header-add":
    case "response-header-replace":
    case "response-header-del":
    case "_header-add":
    case "_header-replace":
    case "_header-del":
    case "_request-header-add":
    case "_request-header-replace":
    case "_request-header-del":
    case "_response-header-add":
    case "_response-header-replace":
    case "_response-header-del":
      return parseHeaderRewriteShortcut(action, remain, args);
    default: {
      // Surge [URL Rewrite] 中无动作前缀的纯 URL 替换是 transparent rewrite
      const trimmedRemain = remain.trim();
      if (
        trimmedRemain.startsWith("http://") ||
        trimmedRemain.startsWith("https://")
      ) {
        args.url = trimmedRemain;
        return { action: "transparent", args, rawJS: "" };
      }
      if (
        trimmedRemain.startsWith("request-header ") ||
        trimmedRemain.startsWith("request-body ") ||
        trimmedRemain.startsWith("response-body ")
      ) {
        const parts = splitFirstWhitespace(trimmedRemain);
        return { action: parts[0], args, rawJS: parts[1].trim() };
      }
      if (trimmedRemain.toLowerCase().startsWith("header-del ")) {
        args.header = trimmedRemain.slice("header-del ".length).trim();
        return { action: "header-del", args, rawJS: "" };
      }
      for (const prefix of [
        "header-add ",
        "header-replace ",
        "request-header-add ",
        "request-header-replace ",
        "request-header-del ",
        "response-header-add ",
        "response-header-replace ",
        "response-header-del ",
      ]) {
        if (trimmedRemain.toLowerCase().startsWith(prefix)) {
          return parseHeaderRewriteShortcut(
            prefix.trim(),
            trimmedRemain.slice(prefix.length).trim(),
            args,
          );
        }
      }
      {
        if ((action.startsWith("http://") || action.startsWith("https://")) &&
            (trimmedRemain === "302" || trimmedRemain === "307")) {
          args.url = action;
          return { action: trimmedRemain, args, rawJS: "" };
        }
        const parts = splitFirstWhitespace(trimmedRemain);
        if (parts[0] && (parts[1] === "302" || parts[1] === "307")) {
          args.url = parts[0];
          return { action: parts[1], args, rawJS: "" };
        }
      }
      args._raw = trimmedRemain;
      return { action, args, rawJS: "" };
    }
  }
}

function parseHeaderRewriteShortcut(action, remain, args) {
  action = String(action || "").toLowerCase().trim().replace(/^_/, "");
  let phase = "0";
  if (action.startsWith("response-header-")) {
    phase = "1";
    action = action.slice("response-header-".length);
  } else if (action.startsWith("request-header-")) {
    action = action.slice("request-header-".length);
  } else if (action.startsWith("header-")) {
    action = action.slice("header-".length);
  }
  args.phase = phase;
  const parts = splitFirstWhitespace(remain);
  args.header = trimQuotes(parts[0] || "");
  if (parts[1]) args.value = trimQuotes(parts[1]);
  return { action: "header-" + action, args, rawJS: "" };
}

function parseSurgeURLRewrites(body) {
  const rules = [];
  for (const line of body.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#") || trimmed.startsWith("//"))
      continue;
    const r = {
      raw: trimmed,
      pattern: "",
      action: "",
      args: {},
      rawJS: "",
    };
    const [pattern, rest] = splitRewritePatternAndRest(trimmed);
    r.pattern = pattern;
    if (!rest) {
      rules.push(r);
      continue;
    }
    const { action, args, rawJS } = parseSurgeURLRewriteAction(rest);
    r.action = action;
    r.args = args;
    r.rawJS = rawJS;
    rules.push(r);
  }
  return rules;
}

function splitRewritePatternAndRest(line) {
  const [pattern, rest] = splitFirstWhitespace(line);
  if (rest) return [pattern, rest];
  for (const marker of [
    "-302",
    "-307",
    "_302",
    "_307",
    "-reject-200",
    "_reject-200",
    "-reject-dict",
    "_reject-dict",
    "-reject-array",
    "_reject-array",
    "-reject-img",
    "_reject-img",
    "-reject",
    "_reject",
  ]) {
    const idx = line.toLowerCase().lastIndexOf(marker);
    if (
      idx > 0 &&
      isTightRewriteActionSuffix(line.slice(idx + marker.length), marker)
    ) {
      return [line.slice(0, idx).trim(), line.slice(idx + 1).trim()];
    }
  }
  return [pattern, rest];
}

function isTightRewriteActionSuffix(suffix, marker) {
  const action = marker.replace(/^[-_]/, "");
  suffix = suffix.trim();
  if (action.startsWith("reject")) return suffix === "";
  if (action === "302" || action === "307") {
    return (
      suffix !== "" &&
      (suffix.startsWith("$") ||
        suffix.startsWith("http://") ||
        suffix.startsWith("https://"))
    );
  }
  return false;
}

function parseSurgeHeaderRewrites(body) {
  const rules = [];
  for (const line of body.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#") || trimmed.startsWith("//"))
      continue;
    const r = {
      raw: trimmed,
      pattern: "",
      phase: 0,
      op: "",
      name: "",
      value: "",
    };
    const [pattern, rest] = splitFirstWhitespace(trimmed);
    r.pattern = pattern;
    if (!rest) continue;
    const [target, rest2] = splitFirstWhitespace(rest);
      switch (target.toLowerCase().trim()) {
      case "request-header":
      case "request":
        r.phase = 0;
        break;
      case "response-header":
      case "response":
        r.phase = 1;
        break;
      default:
        continue;
    }
    const [op, rest3] = splitFirstWhitespace(rest2);
    r.op = op.toLowerCase().trim();
    const tokens = splitCSVFields(rest3);
    if (tokens.length >= 1) r.name = trimQuotes(tokens[0]);
    if (tokens.length >= 2) r.value = trimQuotes(tokens.slice(1).join(" "));
    rules.push(r);
  }
  return rules;
}

function parseSurgeMapLocals(body) {
  const rules = [];
  for (const line of body.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#") || trimmed.startsWith("//"))
      continue;
    const r = { raw: trimmed, pattern: "", dataURL: "", header: "", dataType: "", statusCode: "" };
    const [pattern, rest] = splitFirstWhitespace(trimmed);
    r.pattern = pattern;
    const tokens = tokenizeKV(rest);
    for (const t of tokens) {
      const idx = t.indexOf("=");
      if (idx <= 0) continue;
      const key = t.slice(0, idx).trim().toLowerCase();
      const val = stripInlineComment(trimQuotes(t.slice(idx + 1)));
      if (key === "data" || key === "url" || key === "file" || key === "body" || key === "uri" || key === "data-url") r.dataURL = val;
      else if (key === "header" || key === "headers") r.header = val;
      else if (key === "data-type" || key === "datatype" || key === "format" || key === "type") r.dataType = val;
      else if (key === "status-code" || key === "status") r.statusCode = val;
    }
    rules.push(r);
  }
  return rules;
}

function parseSurgeScriptLine(line) {
  const idx = line.indexOf("=");
  if (idx <= 0) return null;
  const params = line.slice(idx + 1).trim();
  const tokens = splitCSVFields(params);
  const { args } = parseKeyValueList(tokens);
  if (!args["script-path"]) {
    // 兼容混合写法：type=http-response,pattern=... url script-response-header <script-url>
    // 这类规则本质上是 QX/Loon 的 url script-* 语法混入 Surge 模块中，需归一化为 ScriptRule。
    const mixed = parseMixedURLScriptParams(args, line);
    if (mixed) return mixed;
  }
  const s = {
    raw: line,
    pattern: args.pattern || "",
    phase: 0,
    scriptPath: args["script-path"] || "",
    requiresBody: false,
    binaryBody: false,
    argument: args.argument || "",
    tag: args.tag || "",
    maxSize: 0,
    engine: args.engine || "",
  };
  switch ((args.type || "").toLowerCase().trim()) {
    case "http-request":
      s.phase = 0;
      break;
    case "http-response":
      s.phase = 1;
      break;
    case "cron":
      return null;
    default:
      return null;
  }
  if (args["requires-body"])
    s.requiresBody =
      args["requires-body"] === "1" ||
      args["requires-body"].toLowerCase() === "true";
  if (args["binary-body-mode"])
    s.binaryBody =
      args["binary-body-mode"] === "1" ||
      args["binary-body-mode"].toLowerCase() === "true";
  if (args["max-size"]) {
    const n = parseInt(args["max-size"], 10);
    if (!isNaN(n)) s.maxSize = n;
  }
  return s;
}


function splitScriptPathAndTrailingArgs(raw) {
  const parts = splitCSVFields(String(raw || "").trim());
  if (!parts.length) return { scriptPath: "", trailingArgs: [] };
  return { scriptPath: parts[0].trim(), trailingArgs: parts.slice(1) };
}

function parseSurgeScripts(body) {
  const scripts = [];
  for (const line of body.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#") || trimmed.startsWith("//"))
      continue;
    const s = parseSurgeScriptLine(trimmed);
    if (s) scripts.push(s);
  }
  return scripts;
}

function parseSurgeMITM(body) {
  const hostnames = [];
  for (const line of body.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    const idx = trimmed.indexOf("=");
    if (idx <= 0) continue;
    const key = trimmed.slice(0, idx).trim().toLowerCase();
    if (key === "hostname") hostnames.push(...normalizeHostnames(trimmed.slice(idx + 1)));
  }
  return dedupStrings(hostnames);
}

function parseSurge(content) {
  const m = {
    source: "surge",
    name: "",
    desc: "",
    author: "",
    homepage: "",
    date: "",
    rawMeta: {},
    hostnames: [],
    contentType: "",
    rules: [],
    rewrites: [],
    scripts: [],
    headerRWs: [],
    mapLocals: [],
    arguments: [],
  };
  for (const sec of splitSections(content)) {
    switch (sec.name) {
      case "__meta__": {
        const meta = parseMeta(sec.body);
        m.rawMeta = meta;
        if (meta.name) m.name = meta.name;
        if (meta.desc) m.desc = meta.desc;
        if (meta.author) m.author = meta.author;
        if (meta.homepage) m.homepage = meta.homepage;
        if (meta.date) m.date = meta.date;
        m.arguments = mergeArguments(m.arguments, parseMetadataArguments(meta.arguments, meta["arguments-desc"]));
        break;
      }
      case "Rule":
        m.rules.push(...parseLoonRules(sec.body));
        break;
      case "URL Rewrite":
        m.rewrites.push(...parseSurgeURLRewrites(sec.body));
        break;
      case "Header Rewrite":
        m.headerRWs.push(...parseSurgeHeaderRewrites(sec.body));
        break;
      case "Map Local":
        m.mapLocals.push(...parseSurgeMapLocals(sec.body));
        break;
      case "Script":
        m.scripts.push(...parseSurgeScripts(sec.body));
        break;
      case "Argument":
      case "Arguments":
        m.arguments = mergeArguments(m.arguments, parseLoonArguments(sec.body));
        break;
      case "MITM":
        m.hostnames.push(...parseSurgeMITM(sec.body));
        break;
    }
  }
  return m;
}

function parseQuantumultX(content) {
  const m = {
    source: "quantumultx",
    name: "",
    desc: "",
    author: "",
    homepage: "",
    date: "",
    rawMeta: {},
    hostnames: [],
    contentType: "",
    rules: [],
    rewrites: [],
    scripts: [],
    headerRWs: [],
    mapLocals: [],
    arguments: [],
  };
  applyUserScriptMeta(m, content);

  const rawLines = content.split("\n");
  const hostnames = [];

  // 第一遍：提取 hostname
  for (let i = 0; i < rawLines.length; i++) {
    const line = rawLines[i].trim();
    if (line === "") continue;
    if (line.startsWith("#!")) {
      const kv = line.slice(2);
      const idx = kv.indexOf("=");
      if (idx > 0) {
        const key = kv.slice(0, idx).trim();
        const val = kv.slice(idx + 1).trim();
        m.rawMeta[key] = val;
        if (key === "name") m.name = val;
        if (key === "desc") m.desc = val;
        if (key === "author") m.author = val;
        if (key === "homepage") m.homepage = val;
        if (key === "date") m.date = val;
      }
    }
    const hs = extractQXHostnameValue(line);
    if (hs !== "") {
      hostnames.push(...normalizeHostnames(hs));
      continue;
    }
    // 注释行后可能紧跟 hostname
    if (line.startsWith("#") || line.startsWith("//")) {
      const next = (rawLines[i + 1] || "").trim();
      const nhs = extractQXHostnameValue(next);
      if (nhs !== "") hostnames.push(...normalizeHostnames(nhs));
    }
  }
  m.hostnames = dedupStrings(hostnames);
  m.arguments = mergeArguments(parseMetadataArguments(m.rawMeta.arguments, m.rawMeta["arguments-desc"]), m.arguments);

  // 第二遍：解析行式规则
  for (const raw of rawLines) {
    const line = raw.trim();
    if (line === "" || line.startsWith("#") || line.startsWith("//")) continue;
    if (line.startsWith("#!")) continue;
    if (extractQXHostnameValue(line) !== "") continue;
    if (line.startsWith("[") && line.endsWith("]")) continue;

    const rule = parseQuantumultXLine(line);
    if (rule) {
      if (rule.kind === "rewrite") {
        m.rewrites.push(rule.rewrite);
      } else if (rule.kind === "script") {
        m.scripts.push(rule.script);
      }
      continue;
    }
    // 路由规则：TYPE,value,action[,options...]
    var rr = parseQXRoutingRule(line);
    if (rr) m.rules.push(rr);
  }

  return m;
}

function applyUserScriptMeta(m, content) {
  const lines = content.split("\n");
  let inBlock = false;
  for (const raw of lines) {
    const line = raw.trim();
    if (line === "// ==UserScript==") {
      inBlock = true;
      continue;
    }
    if (line === "// ==/UserScript==") break;
    if (!inBlock) continue;
    if (!line.startsWith("// @")) continue;
    const rest = line.slice("// @".length).trim();
    const idx = rest.search(/\s/);
    if (idx <= 0) continue;
    const key = rest.slice(0, idx).trim();
    const val = rest.slice(idx).trim();
    if (!key || !val) continue;
    m.rawMeta[key] = val;
    const lk = key.toLowerCase();
    if (lk === "scriptname" && !m.name) m.name = val;
    else if (lk === "author" && !m.author) m.author = val;
    else if ((lk === "function" || lk === "description") && !m.desc)
      m.desc = val;
    else if (lk === "updatetime" && !m.date) m.date = val;
    else if ((lk === "homepage" || lk === "homepageurl") && !m.homepage)
      m.homepage = val;
  }
}

function extractQXHostnameValue(line) {
  if (!line) return "";
  const lower = line.toLowerCase();
  const idx = lower.indexOf("hostname");
  if (idx < 0) return "";
  let rest = line.slice(idx + "hostname".length);
  rest = rest.replace(/^[ \t]+/, "");
  if (!rest || rest[0] !== "=") return "";
  rest = rest.slice(1).replace(/^[ \t]+/, "");
  return rest;
}

function parseQuantumultXLine(line) {
  // QX 格式：pattern url action [args...]
  const tokens = splitQXTokens(line);
  if (tokens.length < 3) return null;
  const pattern = tokens[0];
  if (tokens[1].toLowerCase() !== "url") return null;
  const action = tokens[2].toLowerCase();

  // 脚本类
  if (
    action.startsWith("script-") ||
    action === "script-analyze-echo-response"
  ) {
    let phase = 1;
    if (action === "script-request-body" || action === "script-request-header")
      phase = 0;
    const scriptPath = tokens[3] || "";
    if (!scriptPath) return null;
    const extraArgs = parseKeyValueList(tokens.slice(4)).args;
    return {
      kind: "script",
      script: {
        raw: line,
        pattern,
        phase,
        scriptPath,
        requiresBody: extraArgs["requires-body"] === "1" || String(extraArgs["requires-body"] || "").toLowerCase() === "true",
        binaryBody: extraArgs["binary-body-mode"] === "1" || String(extraArgs["binary-body-mode"] || "").toLowerCase() === "true",
        argument: extraArgs.argument || "",
        tag: extraArgs.tag || "",
        maxSize: extraArgs["max-size"] ? parseInt(extraArgs["max-size"], 10) || 0 : 0,
        engine: extraArgs.engine || "",
      },
    };
  }

  // echo-response / jsonjq-response-body / response-body 双 url 标记
  if (action === "echo-response") {
    const r = { raw: line, pattern, action, args: {}, rawJS: "" };
    if (tokens.length >= 4) r.args["content-type"] = tokens[3];
    if (tokens.length >= 7) {
      let body = tokens.slice(6).join(" ");
      if (body.startsWith("body ")) body = body.slice(5);
      r.args.body = body;
    }
    return { kind: "rewrite", rewrite: r };
  }
  if (action === "response-body") {
    const r = { raw: line, pattern, action, args: {}, rawJS: "" };
    if (tokens.length >= 4) r.args.search = tokens[3];
    if (tokens.length >= 7) r.args.replacement = tokens[6];
    return { kind: "rewrite", rewrite: r };
  }
  if (action === "jsonjq-response-body") {
    const r = { raw: line, pattern, action, args: {}, rawJS: "" };
    if (tokens.length >= 4) r.args.jq = trimQuotes(tokens.slice(3).join(" "));
    return { kind: "rewrite", rewrite: r };
  }

  // reject / 302 等：复用 Loon rewrite action 解析（去掉 url token 后的 rest）
  const rest = tokens.slice(2).join(" ");
  const parsed = parseLoonRewriteAction(rest);
  const r = {
    raw: line,
    pattern,
    action: parsed.action,
    args: parsed.args,
    rawJS: parsed.rawJS,
  };
  // Loon 解析器会把 302 的 url 放到 args.url，与 QX 一致
  return { kind: "rewrite", rewrite: r };
}

function splitQXTokens(line) {
  const tokens = [];
  let buf = "";
  let inSingle = false;
  let inDouble = false;
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (inSingle) {
      buf += c;
      if (c === "'") inSingle = false;
    } else if (inDouble) {
      buf += c;
      if (c === '"') inDouble = false;
    } else if (c === "'") {
      inSingle = true;
      buf += c;
    } else if (c === '"') {
      inDouble = true;
      buf += c;
    } else if (c === " " || c === "\t") {
      if (buf) {
        tokens.push(buf);
        buf = "";
      }
    } else {
      buf += c;
    }
  }
  if (buf) tokens.push(buf);
  return tokens;
}

// parseQXRoutingRule 解析 QX 行式路由规则。
// 格式：TYPE,value,action[,options...]，例如 DOMAIN-SUFFIX,example.com,DIRECT。
// 不是路由规则时返回 null。
function parseQXRoutingRule(line) {
  if (line[0] === "^") return null;
  var fields = splitCSVFields(line);
  if (fields.length < 3) return null;
  var ruleType = fields[0].toUpperCase().trim();
  var validTypes = [
    "DOMAIN",
    "DOMAIN-SUFFIX",
    "DOMAIN-KEYWORD",
    "DOMAIN-WILDCARD",
    "HOST",
    "HOST-SUFFIX",
    "HOST-KEYWORD",
    "HOST-WILDCARD",
    "DOMAIN-SET",
    "RULE-SET",
    "IP-CIDR",
    "IP-CIDR6",
    "IP6-CIDR",
    "GEOIP",
    "USER-AGENT",
    "DEST-PORT",
    "SRC-PORT",
    "SRC-IP",
    "SRC-IP-CIDR",
    "PROCESS-NAME",
    "SUBNET",
    "CELLULAR-RADIO",
  ];
  if (validTypes.indexOf(ruleType) < 0) return null;
  var options = [];
  for (var i = 3; i < fields.length; i++) options.push(fields[i].trim());
  return {
    raw: line,
    type: ruleType,
    value: fields[1].trim(),
    action: fields[2].toUpperCase().trim(),
    options: options,
  };
}

function parse(content, source) {
  switch (source) {
    case "loon":
      return parseLoon(content);
    case "surge":
      return parseSurge(content);
    case "quantumultx":
      return parseQuantumultX(content);
    default:
      return parseLoon(content);
  }
}

function mergeArguments() {
  const out = [];
  const positions = {};
  for (let i = 0; i < arguments.length; i++) {
    const group = arguments[i] || [];
    for (const arg of group) {
      const key = String((arg && arg.key) || "").trim();
      if (!key) continue;
      if (Object.prototype.hasOwnProperty.call(positions, key)) out[positions[key]] = arg;
      else {
        positions[key] = out.length;
        out.push(arg);
      }
    }
  }
  return out;
}

function resolveArgumentValues(args, overrides) {
  const out = {};
  for (const arg of args || []) {
    const key = String(arg.key || "").trim();
    if (!key) continue;
    out[key] = normalizeArgumentValueForType(arg.defaultValue || arg.value || "", arg.type || "");
  }
  for (const key of Object.keys(overrides || {})) {
    if (!/^[A-Za-z_][A-Za-z0-9_-]*$/.test(key)) continue;
    const found = (args || []).find((arg) => arg.key === key);
    out[key] = normalizeArgumentValueForType(overrides[key], found ? found.type : "");
  }
  return out;
}

function normalizeArgumentValueForType(value, type) {
  const text = String(value == null ? "" : value).trim();
  const normalizedType = String(type || "").toLowerCase().trim();
  if (normalizedType === "switch" || normalizedType === "checkbox") {
    if (argumentEnabled(text)) return "true";
    if (/^(?:false|0|no|off)$/i.test(text)) return "false";
  }
  return text;
}

function argumentEnabled(value) {
  return /^(?:true|1|yes|on)$/i.test(String(value == null ? "" : value).trim());
}

function applyArgumentsToModule(m, opts, report) {
  const values = resolveArgumentValues(m.arguments || [], opts.arguments || {});
  const parameters = opts.preserveParameters ? buildAmrsParameters(m.arguments || [], values, report) : [];
  if (!Object.keys(values).length) return { module: m, values, parameters };

  const out = { ...m };
  out.hostnames = (m.hostnames || []).map((v) => substituteArguments(v, values));
  out.rules = [];
  for (const r of m.rules || []) {
    if (!argumentRuleEnabled(r.raw, values)) {
      report.skipped.push("参数 enable 关闭，已跳过: " + r.raw);
      continue;
    }
    out.rules.push({
      ...r,
      type: substituteArguments(r.type, values),
      value: substituteArguments(r.value, values),
      action: substituteArguments(r.action, values),
      raw: substituteArguments(r.raw, values),
      options: (r.options || []).map((v) => substituteArguments(v, values)),
    });
  }
  out.rewrites = [];
  for (const r of m.rewrites || []) {
    if (!argumentRuleEnabled(r.raw, values)) {
      report.skipped.push("参数 enable 关闭，已跳过: " + r.raw);
      continue;
    }
    const args = {};
    for (const key of Object.keys(r.args || {})) args[key] = substituteArguments(r.args[key], values);
    out.rewrites.push({
      ...r,
      pattern: substituteArguments(r.pattern, values),
      action: substituteArguments(r.action, values),
      args,
      rawJS: substituteArguments(r.rawJS, values),
      raw: substituteArguments(r.raw, values),
    });
  }
  out.scripts = [];
  for (const s of m.scripts || []) {
    if (!argumentRuleEnabled(s.raw, values)) {
      report.skipped.push("参数 enable 关闭，已跳过: " + s.raw);
      continue;
    }
    out.scripts.push({
      ...s,
      pattern: substituteArguments(s.pattern, values),
      scriptPath: substituteArguments(s.scriptPath, values),
      argument: substituteArguments(s.argument, values),
      tag: substituteArguments(s.tag, values),
      engine: substituteArguments(s.engine, values),
      raw: substituteArguments(s.raw, values),
    });
  }
  out.headerRWs = [];
  for (const h of m.headerRWs || []) {
    if (!argumentRuleEnabled(h.raw, values)) {
      report.skipped.push("参数 enable 关闭，已跳过: " + h.raw);
      continue;
    }
    out.headerRWs.push({
      ...h,
      pattern: substituteArguments(h.pattern, values),
      op: substituteArguments(h.op, values),
      name: substituteArguments(h.name, values),
      value: substituteArguments(h.value, values),
      raw: substituteArguments(h.raw, values),
    });
  }
  out.mapLocals = [];
  for (const ml of m.mapLocals || []) {
    if (!argumentRuleEnabled(ml.raw, values)) {
      report.skipped.push("参数 enable 关闭，已跳过: " + ml.raw);
      continue;
    }
    out.mapLocals.push({
      ...ml,
      pattern: substituteArguments(ml.pattern, values),
      dataURL: substituteArguments(ml.dataURL, values),
      header: substituteArguments(ml.header, values),
      dataType: substituteArguments(ml.dataType, values),
      statusCode: substituteArguments(ml.statusCode, values),
      raw: substituteArguments(ml.raw, values),
    });
  }
  return { module: out, values, parameters };
}

function argumentRuleEnabled(raw, values) {
  const match = String(raw || "").match(/\benable\s*=\s*(?:\{([A-Za-z_][A-Za-z0-9_-]*)\}|([A-Za-z_][A-Za-z0-9_-]*|true|false|1|0|yes|no|on|off))/i);
  if (!match) return true;
  const key = match[1] || match[2] || "";
  return argumentEnabled(Object.prototype.hasOwnProperty.call(values, key) ? values[key] : key);
}

function substituteArguments(value, values) {
  if (!value || !values || !Object.keys(values).length) return value || "";
  return String(value).replace(/\{\{\{([^{}]+)\}\}\}|\{\{([^{}]+)\}\}|\{([A-Za-z_][A-Za-z0-9_-]*)\}/g, function(match, tripleName, doubleName, singleName) {
    const name = String(tripleName || doubleName || singleName || "").trim();
    return Object.prototype.hasOwnProperty.call(values, name) ? String(values[name]) : match;
  });
}

function buildAmrsParameters(args, values, report) {
  const out = [];
  const seen = {};
  const nameMap = {};
  for (let i = 0; i < (args || []).length; i++) {
    const arg = args[i];
    const sourceName = String(arg.key || "").trim();
    if (!sourceName || seen[sourceName]) continue;
    seen[sourceName] = true;
    const name = safeParameterName(sourceName, i, nameMap);
    nameMap[sourceName] = name;
    if (name !== sourceName) report.warnings.push(`参数 ${sourceName} 已映射为 Anywhere 参数名 ${name}`);
    const label = arg.tag || sourceName;
    let description = arg.desc || "";
    if (name !== sourceName) description = (description ? description + "；" : "") + `来自上游 "${sourceName}" 参数`;
    const current = Object.prototype.hasOwnProperty.call(values, sourceName) ? values[sourceName] : (arg.defaultValue || "");
    const param = { type: 0, dataType: 0, name, label, description, defaultValue: String(current), options: [] };
    const argType = String(arg.type || "").toLowerCase();
    if (argType === "select") {
      const options = ensureParameterOptions(arg.options || [], String(current));
      if (options.length) {
        param.type = 1;
        param.options = options;
        if (!options.includes(param.defaultValue)) param.defaultValue = options[0];
      }
    } else if (argType === "switch" || argType === "checkbox") {
      param.type = 1;
      param.defaultValue = argumentEnabled(current) ? "true" : "false";
      param.options = ["true", "false"];
    }
    out.push(param);
  }
  return out;
}

function safeParameterName(sourceName, index, nameMap) {
  const raw = String(sourceName || "").trim();
  let base = raw.replace(/-/g, "_");
  if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(base)) base = "ARG_" + stableNameHash(raw).toUpperCase();
  if (!/^[A-Za-z_]/.test(base)) base = "arg_" + base;
  base = base.replace(/[^A-Za-z0-9_]/g, "_") || ("arg_" + (index + 1));
  const used = {};
  for (const key of Object.keys(nameMap || {})) used[nameMap[key]] = true;
  let name = base;
  let suffix = 2;
  while (used[name]) {
    name = base + "_" + suffix;
    suffix++;
  }
  return name;
}

function stableNameHash(value) {
  let hash = 2166136261;
  for (const char of String(value || "")) {
    hash ^= char.codePointAt(0);
    hash = Math.imul(hash, 16777619) >>> 0;
  }
  return hash.toString(16);
}

function ensureParameterOptions(values, defaultValue) {
  const out = [];
  const seen = {};
  for (const value of values || []) {
    const text = String(value == null ? "" : value).trim();
    if (!text || seen[text]) continue;
    seen[text] = true;
    out.push(text);
  }
  if (defaultValue && !seen[defaultValue]) out.push(defaultValue);
  return out;
}

// ===================== 核心转换器 =====================

function isRejectAction(action) {
  return [
    "REJECT",
    "REJECT-DICT",
    "REJECT-ARRAY",
    "REJECT-IMG",
    "REJECT-200",
    "REJECT-DATA",
  ].includes(action);
}

function normalizeRoutingRuleType(ruleType) {
  const t = String(ruleType || "").toUpperCase().trim();
  switch (t) {
    case "HOST":
      return "DOMAIN";
    case "HOST-SUFFIX":
      return "DOMAIN-SUFFIX";
    case "HOST-KEYWORD":
      return "DOMAIN-KEYWORD";
    case "HOST-WILDCARD":
      return "DOMAIN-WILDCARD";
    case "IP6-CIDR":
      return "IP-CIDR6";
    default:
      return t;
  }
}

function normalizeWildcardDomain(value) {
  let v = String(value || "").trim();
  v = v.replace(/^"|"$/g, "");
  v = v.replace(/\.$/, "");
  v = v.replace(/\\\./g, ".");
  v = v.replace(/^\+\./, "");
  v = v.replace(/^\./, "");
  v = v.replace(/^\*\./, "");
  v = v.replace(/^\*/, "");
  if (!v) return "";
  if (/[*?]/.test(v)) {
    const labels = v.split(".");
    for (let i = 0; i < labels.length; i++) {
      if (/[*?]/.test(labels[i])) {
        if (i + 1 >= labels.length) return "";
        return labels.slice(i + 1).join(".");
      }
    }
  }
  return v;
}

/** appendByAction 按 action 类型将规则行分配到对应的 arrs 分组 */
function appendByAction(action, line, directLines, rejectLines, otherLines) {
  if (action && action.toUpperCase() === "DIRECT") {
    directLines.push(line);
  } else if (isRejectAction(action)) {
    rejectLines.push(line);
  } else {
    otherLines.push(line);
  }
}

function defaultConvertOptions() {
  return {
    generalizeHost: false,
    encodingPreprocess: true,
    fetchScripts: true,
    includeMetadata: true,
    useStreamScript: false,
    autoContentType: true,
    addResourceURL: "",
    wrapScripts: false,
    arguments: {},
    preserveParameters: false,
    scriptMode: "inline",
    scriptBaseURL: "",
    maxScriptBytes: 1024 * 1024,
    maxScriptFetches: 45,
  };
}

async function convert(m, opts) {
  const options = { ...defaultConvertOptions(), ...opts };
  const report = { skipped: [], degraded: [], warnings: [], scriptErr: [] };
  const argumentState = applyArgumentsToModule(m, options, report);
  m = argumentState.module;
  const baseName = m.name || "module2anywhere";

  const cleanedHosts = [];
  for (const h of m.hostnames) {
    if (/[?*]/.test(h)) {
      report.warnings.push(`hostname 含通配符无法静态展开，已跳过: ${h}`);
      continue;
    }
    cleanedHosts.push(h);
  }
  m.hostnames = cleanedHosts;

  const ref = convertRoutingRules(m.rules, options, report);
  var directLines = ref[0];
  var rejectLines = ref[1];
  var otherLines = ref[2];
  var amrsFromRules = ref[3];

  // 生成分组 arrs
  var arrsGroups = [];
  if (directLines.length > 0) {
    arrsGroups.push({
      content: generateArrs(baseName + "-Direct", directLines, m, options, 1),
      name: baseName + "-Direct.arrs",
      routing: 1,
      endpoint: "/direct.arrs",
    });
  }
  if (rejectLines.length > 0) {
    arrsGroups.push({
      content: generateArrs(baseName + "-Reject", rejectLines, m, options, 2),
      name: baseName + "-Reject.arrs",
      routing: 2,
      endpoint: "/reject.arrs",
    });
  }
  if (otherLines.length > 0) {
    arrsGroups.push({
      content: generateArrs(baseName, otherLines, m, options, 0),
      name: baseName + ".arrs",
      routing: 0,
      endpoint: "/rule.arrs",
    });
  }

  const amrsLines = [
    ...amrsFromRules,
    ...convertRewriteRules(m, options, report),
    ...convertHeaderRules(m.headerRWs, options, report),
    ...(await convertMapLocals(m.mapLocals, options, report, m.source)),
    ...(await convertScriptRules(m, options, report, m.source)),
  ];
  m.hostnames = addInferredHostnames(m.hostnames, amrsLines, report);
  addMemoryRiskWarnings(amrsLines, options, report);

  let finalAmrsLines = amrsLines;
  if (options.encodingPreprocess)
    finalAmrsLines = addEncodingPreprocess(amrsLines);

  // 兼容旧接口：合并所有 arrs 行
  var allArrsLines = [].concat(directLines, rejectLines, otherLines);

  return {
    arrs: generateArrs(baseName, allArrsLines, m, options, 0),
    amrs: generateAmrs(baseName, m.hostnames, finalAmrsLines, m, options, argumentState.parameters),
    arrsName: baseName + ".arrs",
    amrsName: baseName + ".amrs",
    arrsGroups: arrsGroups,
    report,
  };
}

function addInferredHostnames(hostnames, lines, report) {
  const seen = new Set();
  const out = [];
  for (const host of hostnames || []) {
    const h = String(host || "").trim().toLowerCase();
    if (!h || seen.has(h)) continue;
    seen.add(h);
    out.push(h);
  }
  const added = [];
  let complexUncovered = 0;
  for (const line of lines || []) {
    const fields = splitAmrsFields(line);
    if (fields.length < 3) continue;
    const inferred = inferHostnameSuffixesFromPattern(fields[2]);
    if (inferred.length === 0 && hasComplexHostnamePattern(fields[2])) complexUncovered++;
    for (const host of inferred) {
      if (seen.has(host)) continue;
      seen.add(host);
      out.push(host);
      added.push(host);
    }
  }
  if (added.length > 0) {
    report.warnings.push(`已从 MITM 规则 URL pattern 推断补充 ${added.length} 个 hostname 后缀: ${added.join(", ")}`);
  }
  if (complexUncovered > 0) {
    report.warnings.push(`存在 ${complexUncovered} 条 MITM 规则的主机正则过于复杂，Anywhere hostname 只能按后缀匹配，可能需要手动补充具体域名或更宽的安全后缀`);
  }
  return out;
}

function convertRoutingRules(rules, opts, report) {
  var directLines = [];
  var rejectLines = [];
  var otherLines = [];
  var amrsLines = [];
  for (var i = 0; i < rules.length; i++) {
    var r = rules[i];
    var ruleType = normalizeRoutingRuleType(r.type);
    switch (ruleType) {
      case "DOMAIN-SUFFIX":
      case "DOMAIN": {
        var line = "2, " + r.value;
        appendByAction(r.action, line, directLines, rejectLines, otherLines);
        break;
      }
      case "DOMAIN-KEYWORD": {
        var line = "3, " + r.value;
        appendByAction(r.action, line, directLines, rejectLines, otherLines);
        break;
      }
      case "DOMAIN-WILDCARD": {
        var value = normalizeWildcardDomain(r.value);
        if (!value) {
          report.skipped.push(r.type + " 无法转换为安全域名后缀: " + r.raw);
          break;
        }
        report.degraded.push(r.type + " 按 Anywhere 后缀匹配近似转换，匹配范围可能扩大: " + r.raw);
        var line = "2, " + value;
        appendByAction(r.action, line, directLines, rejectLines, otherLines);
        break;
      }
      case "IP-CIDR": {
        var line = "0, " + r.value;
        appendByAction(r.action, line, directLines, rejectLines, otherLines);
        break;
      }
      case "IP-CIDR6": {
        var line = "1, " + r.value;
        appendByAction(r.action, line, directLines, rejectLines, otherLines);
        break;
      }
      case "URL-REGEX":
        if (isRejectAction(r.action)) {
          var line = convertURLRegexReject(r, opts, report);
          if (line) amrsLines.push(line);
        } else {
          report.skipped.push("URL-REGEX 非 REJECT 类不可转换: " + r.raw);
        }
        break;
      case "GEOIP":
      case "PROCESS-NAME":
      case "DEST-PORT":
      case "SRC-PORT":
      case "SRC-IP":
      case "SRC-IP-CIDR":
      case "CELLULAR-RADIO":
      case "SUBNET":
        report.skipped.push(ruleType + " 不可转换: " + r.raw);
        break;
      case "DOMAIN-SET":
      case "RULE-SET":
        report.warnings.push("DOMAIN-SET/RULE-SET 需单独下载展开: " + r.raw);
        break;
      default:
        report.skipped.push("未知规则类型 " + r.type + ": " + r.raw);
    }
  }
  return [directLines, rejectLines, otherLines, amrsLines];
}

function convertURLRegexReject(r, opts, report) {
  const pattern = convertURLPattern(r.value, opts.generalizeHost);
  switch (r.action) {
    case "REJECT":
    case "REJECT-200":
      return `0, 0, ${pattern}, 2`;
    case "REJECT-DICT":
      return `0, 0, ${pattern}, 2, {}`;
    case "REJECT-ARRAY":
      return `0, 0, ${pattern}, 2, []`;
    case "REJECT-IMG":
      return `0, 0, ${pattern}, 3`;
    default:
      report.skipped.push(`URL-REGEX 未知 REJECT 动作 ${r.action}: ${r.raw}`);
      return "";
  }
}

function convertRewriteRules(m, opts, report) {
  const lines = [];
  for (const r of m.rewrites) {
    const line = convertRewriteRule(r, m, opts, report);
    if (line) lines.push(line);
  }
  return lines;
}

function convertRewriteRule(r, m, opts, report) {
  const pattern = convertURLPattern(r.pattern, opts.generalizeHost);
  switch (r.action) {
    case "reject":
    case "reject-200":
      return `0, 0, ${pattern}, 2`;
    case "reject-dict":
      return `0, 0, ${pattern}, 2, {}`;
    case "reject-array":
      return `0, 0, ${pattern}, 2, []`;
    case "reject-img":
      return `0, 0, ${pattern}, 3`;
    case "302": {
      const url = r.args.url || "";
      // Anywhere rewrite sub-mode 1 原生支持 $1 捕获引用，直接输出
      return `0, 0, ${pattern}, 1, ${url}`;
    }
    case "307": {
      const url = r.args.url || "";
      report.degraded.push(`307 降级为 302: ${r.raw}`);
      return `0, 0, ${pattern}, 1, ${url}`;
    }
    // 透明 URL 重写：Anywhere rewrite sub-mode 0 原生支持 $1 捕获引用
    case "transparent":
    case "rewrite": {
      const url = r.args.url || "";
      if (!url) {
        report.skipped.push(`transparent rewrite 缺少 url: ${r.raw}`);
        return "";
      }
      return `0, 0, ${pattern}, 0, ${url}`;
    }
    // reject-data：返回 base64 二进制数据
    case "reject-data": {
      const data = r.args.data || "";
      if (data) return `0, 0, ${pattern}, 4, ${quoteField(data)}`;
      return `0, 0, ${pattern}, 4`;
    }
    case "mock-response-body":
      const mockStatus = parseStatusCode(r.args["status-code"]);
      {
        const dataType = String(r.args["data-type"] || "").toLowerCase().trim();
        if (dataType === "json") {
          report.degraded.push(`mock-response-body data-type=json 已转为脚本以保留 Content-Type: ${r.raw}`);
          return `0, 100, ${pattern}, ${encodeStaticRespondScript(mockStatus, [["Content-Type", "application/json; charset=utf-8"]], r.args.data || "", "utf8")}`;
        }
        if (mockStatus !== 200) {
          report.degraded.push(`mock-response-body status-code=${mockStatus} 已转为脚本保留: ${r.raw}`);
          let encoding = "utf8";
          let scriptBody = r.args.data || "";
          if (dataType === "base64") encoding = "base64";
          else if (dataType === "tiny-gif" || dataType === "gif") {
            encoding = "base64";
            scriptBody = tinyGIFBase64;
          }
          return `0, 100, ${pattern}, ${encodeStaticRespondScript(mockStatus, [], scriptBody, encoding)}`;
        }
        if (dataType === "base64") {
          return `0, 0, ${pattern}, 4, ${quoteField(r.args.data || "")}`;
        }
        if (dataType === "tiny-gif" || dataType === "gif") {
          return `0, 0, ${pattern}, 3`;
        }
      }
      return `0, 0, ${pattern}, 2, ${quoteField(r.args.data || "")}`;
    case "response-body-json-del":
      return `1, 5, ${pattern}, delete, ${dotPathToJSONPath(r.args.path || "")}`;
    case "response-body-json-add":
      return `1, 5, ${pattern}, add, ${dotPathToJSONPath(r.args.path || "")}, ${quoteField(r.args.value || "")}`;
    case "response-body-json-replace":
      return `1, 5, ${pattern}, replace, ${dotPathToJSONPath(r.args.path || "")}, ${quoteField(r.args.value || "")}`;
    // body-json 递归操作（Anywhere 原生支持）
    case "response-body-json-delete-recursive":
      return `1, 5, ${pattern}, delete-recursive, ${quoteField(r.args.key || "")}`;
    case "response-body-json-replace-recursive":
      return `1, 5, ${pattern}, replace-recursive, ${quoteField(r.args.key || "")}, ${quoteField(r.args.value || "")}`;
    case "response-body-json-remove-where-key-exists":
      return `1, 5, ${pattern}, remove-where-key-exists, ${dotPathToJSONPath(r.args.path || "")}, ${quoteField(r.args.key || "")}`;
    case "response-body-json-remove-where-field-in":
      return `1, 5, ${pattern}, remove-where-field-in, ${dotPathToJSONPath(r.args.path || "")}, ${quoteField(r.args.field || "")}, ${quoteField(r.args.values || "")}`;
    case "request-header":
    case "request-body":
      return `0, 100, ${pattern}, ${encodeInlineRewriteJS(r.rawJS, 0)}`;
    case "response-body":
      return `1, 100, ${pattern}, ${encodeInlineRewriteJS(r.rawJS, 1)}`;
    case "_request-header":
    case "_request-body":
      return `0, 100, ${pattern}, ${encodeInlineRewriteJS(r.rawJS, 0)}`;
    case "_response-body":
      return `1, 100, ${pattern}, ${encodeInlineRewriteJS(r.rawJS, 1)}`;
    case "header-add":
    case "header-replace":
    case "header-del": {
      const phase = r.args.phase === "1" ? 1 : 0;
      const headerName = r.args.header || "";
      if (!headerName) return "";
      if (r.action === "header-add") {
        return `${phase}, 1, ${pattern}, ${quoteField(headerName)}, ${quoteField(r.args.value || "")}`;
      }
      if (r.action === "header-replace") {
        return `${phase}, 3, ${pattern}, ${quoteField(headerName)}, ${quoteField(r.args.value || "")}`;
      }
      return `${phase}, 2, ${pattern}, ${quoteField(headerName)}`;
    }
    case "response-body-replace-regex": {
      const search = r.args.search || "";
      const replacement = r.args.replacement || "";
      if (!search) return "";
      return `1, 4, ${pattern}, ${quoteField(search)}, ${quoteField(replacement)}`;
    }
    case "echo-response": {
      const body = r.args.body || "";
      const ct = r.args["content-type"] || "application/json; charset=utf-8";
      if (!body) {
        report.skipped.push(`echo-response 缺少 body: ${r.raw}`);
        return "";
      }
      report.degraded.push(`echo-response 已转为脚本以保留 Content-Type: ${r.raw}`);
      return `0, 100, ${pattern}, ${encodeStaticRespondScript(200, [["Content-Type", ct]], body, "utf8")}`;
    }
    case "jsonjq-response-body": {
      const jq = r.args.jq || "";
      if (!jq) {
        report.skipped.push(`jsonjq-response-body 缺少 jq: ${r.raw}`);
        return "";
      }
      return `1, 5, ${pattern}, ${quoteField(jq)}`;
    }
    default:
      report.skipped.push(`未知重写动作 ${r.action}: ${r.raw}`);
      return "";
  }
}

function convertHeaderRules(rules, opts, report) {
  const lines = [];
  for (const r of rules) {
    const pattern = convertURLPattern(r.pattern, opts.generalizeHost);
    switch (r.op) {
      case "add":
        lines.push(
          `${r.phase}, 1, ${pattern}, ${quoteField(r.name)}, ${quoteField(r.value)}`,
        );
        break;
      case "replace":
        lines.push(
          `${r.phase}, 3, ${pattern}, ${quoteField(r.name)}, ${quoteField(r.value)}`,
        );
        break;
      case "delete":
        lines.push(`${r.phase}, 2, ${pattern}, ${quoteField(r.name)}`);
        break;
      default:
        report.skipped.push(`未知 header 操作 ${r.op}: ${r.raw}`);
    }
  }
  return lines;
}

async function convertMapLocals(rules, opts, report, source) {
  const lines = [];
  const userAgent = getUserAgent(source);
  for (const r of rules) {
    const pattern = convertURLPattern(r.pattern, opts.generalizeHost);
    if (!r.dataURL) {
      report.skipped.push(`Map Local 无 data: ${r.raw}`);
      continue;
    }
    let headerName = "";
    let headerValue = "";
    if (r.header && String(r.header).trim()) {
      const parts = String(r.header).split(":", 2);
      if (parts.length !== 2) {
        report.warnings.push(`Map Local header 格式非法，已忽略: ${r.raw}`);
      } else {
        headerName = parts[0].trim();
        headerValue = parts[1].trim();
        if (!headerName) {
          report.warnings.push(`Map Local header 名称为空，已忽略: ${r.raw}`);
          headerName = "";
          headerValue = "";
        }
      }
    }
    const status = parseStatusCode(r.statusCode);
    let body = r.dataURL;
    if (r.dataURL.startsWith("http")) {
      try {
        body = await fetchRemoteWithProxy(r.dataURL, userAgent);
      } catch (e) {
        report.scriptErr.push(`Map Local 下载 data 失败 ${r.dataURL}: ${e}`);
        continue;
      }
    }
    const dataType = String(r.dataType || "").toLowerCase().trim();
    const headers = [];
    if (headerName) headers.push([headerName, headerValue]);
    if (dataType === "json" && !headerName) {
      headers.push(["Content-Type", "application/json; charset=utf-8"]);
    }
    const needsScript = status !== 200 || headers.length > 0;
    if (dataType === "base64") {
      if (needsScript) {
        report.degraded.push(`Map Local 已转为脚本以保留 status/header: ${r.raw}`);
        lines.push(`0, 100, ${pattern}, ${encodeStaticRespondScript(status, headers, body, "base64")}`);
      } else {
        lines.push(`0, 0, ${pattern}, 4, ${quoteField(body)}`);
      }
    } else if (dataType === "tiny-gif" || dataType === "gif") {
      if (needsScript) {
        report.degraded.push(`Map Local 已转为脚本以保留 status/header: ${r.raw}`);
        lines.push(`0, 100, ${pattern}, ${encodeStaticRespondScript(status, headers, tinyGIFBase64, "base64")}`);
      } else {
        lines.push(`0, 0, ${pattern}, 3`);
      }
    } else if (needsScript) {
      report.degraded.push(`Map Local 已转为脚本以保留 status/header: ${r.raw}`);
      lines.push(`0, 100, ${pattern}, ${encodeStaticRespondScript(status, headers, body, "utf8")}`);
    } else {
      lines.push(`0, 0, ${pattern}, 2, ${quoteField(body)}`);
    }
  }
  return lines;
}

function parseStatusCode(raw) {
  if (raw === undefined || raw === null || String(raw).trim() === "") return 200;
  const n = parseInt(String(raw).trim(), 10);
  if (!n || n < 100 || n > 999) return 200;
  return n;
}

function scriptMergeKey(s) {
  return JSON.stringify([
    s.phase || 0,
    s.scriptPath || "",
    s.argument || "",
    !!s.requiresBody,
    !!s.binaryBody,
    s.maxSize || 0,
    s.engine || "",
  ]);
}

function unionAMRSPattern(patterns) {
  const seen = {};
  const uniq = [];
  for (const pattern of patterns || []) {
    if (seen[pattern]) continue;
    seen[pattern] = true;
    uniq.push(pattern);
  }
  if (uniq.length === 0) return "";
  if (uniq.length === 1) return uniq[0];
  return uniq.map((pattern) => "(?:" + pattern + ")").join("|");
}

function mergeScriptRulesForConversion(scripts, opts, report) {
  const tasks = [];
  const byKey = {};
  for (let i = 0; i < scripts.length; i++) {
    const s = scripts[i];
    const pattern = convertURLPattern(s.pattern, opts && opts.generalizeHost);
    if (!s.scriptPath) {
      tasks.push({ index: i, s: s, pattern: pattern, count: 1, patterns: [pattern] });
      continue;
    }
    const key = scriptMergeKey(s);
    if (Object.prototype.hasOwnProperty.call(byKey, key)) {
      const task = tasks[byKey[key]];
      task.patterns.push(pattern);
      task.pattern = unionAMRSPattern(task.patterns);
      task.count++;
      continue;
    }
    byKey[key] = tasks.length;
    tasks.push({ index: i, s: s, pattern: pattern, count: 1, patterns: [pattern] });
  }
  let mergedRules = 0;
  for (const task of tasks) {
    if (task.count > 1) {
      mergedRules += task.count - 1;
      report.warnings.push(`同 phase/script-path/argument 的 ${task.count} 条脚本规则已合并为 1 条 URL union 规则: ${task.s.scriptPath}`);
    }
  }
  if (mergedRules > 0) {
    report.warnings.push(`脚本规则合并减少了 ${mergedRules} 条重复脚本上下文`);
  }
  return tasks;
}

async function convertScriptRules(m, opts, report, source) {
  const userAgent = getUserAgent(source);
  // 与 Go 侧 Concurrency 默认值（8）对齐，限制并发下载数
  const CONCURRENCY = (opts && opts.concurrency > 0) ? opts.concurrency : 8;
  const scripts = m.scripts || [];

  // 统计相同 scriptPath 的引用次数，提示输出文件被放大的程度
  // （fetchAndEncodeScript 有进程内缓存避免重复下载，但 .amrs 格式要求每行独立包含完整 base64，
  //   运行时每条规则会创建独立脚本上下文，N 条相同 script-path = N 份内存副本）
  const pathCounts = {};
  for (const s of scripts) {
    if (s.scriptPath) pathCounts[s.scriptPath] = (pathCounts[s.scriptPath] || 0) + 1;
  }
  let dupTotal = 0;
  for (const path in pathCounts) {
    const count = pathCounts[path];
    if (count > 1) {
      dupTotal += count;
      report.warnings.push(`脚本 ${path} 被 ${count} 条原始规则引用，转换器会尝试合并同 phase/script-path/argument 的规则`);
    }
  }
  if (dupTotal > 0) {
    report.warnings.push(`共 ${dupTotal} 条原始规则引用了重复的 script-path，若 phase/argument 等选项不同仍会保留独立脚本上下文`);
  }
  const maxScriptBytes = opts && opts.maxScriptBytes > 0 ? opts.maxScriptBytes : 1024 * 1024;
  const maxScriptFetches = opts && opts.maxScriptFetches > 0 ? opts.maxScriptFetches : 45;
  const scriptBudgetSkipped = {};
  const seenScriptPaths = {};
  let uniqueScriptCount = 0;
  for (const s of scripts) {
    if (!s.scriptPath || seenScriptPaths[s.scriptPath]) continue;
    seenScriptPaths[s.scriptPath] = true;
    if (uniqueScriptCount >= maxScriptFetches) {
      const msg = `脚本下载数量超过上限 ${maxScriptFetches}，已跳过: ${s.scriptPath}`;
      scriptBudgetSkipped[s.scriptPath] = msg;
      report.scriptErr.push(msg);
    }
    uniqueScriptCount++;
  }

  const tasks = mergeScriptRulesForConversion(scripts, opts, report);

  // 预计算每条脚本的 pattern 与有效性，保留合并后首条规则的输出顺序
  // results 按 tasks 长度分配，skipped 的位置保持 undefined
  const results = new Array(tasks.length);
  const queue = [];
  for (let i = 0; i < tasks.length; i++) {
    const task = tasks[i];
    const s = task.s;
    if (!s.scriptPath) {
      report.skipped.push(`脚本无 script-path: ${s.raw}`);
      continue;
    }
    queue.push({ index: i, s: s, pattern: task.pattern });
  }

  // 简单 worker 池：从队列拉取任务执行，限制并发下载数
  async function worker() {
    while (queue.length > 0) {
      const t = queue.shift();
      try {
        let b64;
        if (scriptBudgetSkipped[t.s.scriptPath]) {
          const placeholder =
            'function process(ctx){Anywhere.log.warning("script not fetched: " + ' +
            JSON.stringify(String(t.s.scriptPath)) +
            ");}";
          b64 = btoa(unescape(encodeURIComponent(placeholder)));
          const op = opts.useStreamScript ? "101" : "100";
          results[t.index] = `${t.s.phase}, ${op}, ${t.pattern}, ${b64}`;
          continue;
        }
        if (String(opts.scriptMode || "").toLowerCase() === "loader" && opts.scriptBaseURL && !opts.useStreamScript) {
          const loaderURL = scriptLoaderURL(
            opts.scriptBaseURL,
            t.s.scriptPath,
            opts.sourceURL || "",
            t.s.phase,
            opts.wrapScripts,
            t.s.argument || "",
            maxScriptBytes,
          );
          b64 = encodeLoaderScript(loaderURL);
        } else {
          const resolved = resolveScriptPath(t.s.scriptPath, opts.sourceURL || m.name);
          b64 = await fetchAndEncodeScript(
            resolved,
            opts.fetchScripts,
            t.s.phase,
            opts.useStreamScript,
            userAgent,
            opts.wrapScripts,
            t.s.argument || "",
            maxScriptBytes,
          );
        }
        const op = opts.useStreamScript ? "101" : "100";
        results[t.index] = `${t.s.phase}, ${op}, ${t.pattern}, ${b64}`;
      } catch (e) {
        report.scriptErr.push(`脚本下载失败 ${t.s.scriptPath}: ${e}`);
        results[t.index] = null;
      }
    }
  }

  // 启动 CONCURRENCY 个 worker 并等待全部完成
  const workers = [];
  for (let i = 0; i < CONCURRENCY && i < queue.length; i++) {
    workers.push(worker());
  }
  await Promise.all(workers);

  // 按原顺序输出，跳过 skipped 和 failed 的位置
  const lines = [];
  for (let i = 0; i < results.length; i++) {
    if (results[i]) lines.push(results[i]);
  }
  return lines;
}

function addEncodingPreprocess(lines) {
  const patterns = new Set();
  for (const line of lines) {
    const fields = splitAmrsFields(line);
    if (fields.length < 2) continue;
    if (fields[0] !== "1") continue;
    if (["4", "5", "100"].includes(fields[1]) && fields.length >= 3)
      patterns.add(fields[2]);
  }
  if (patterns.size === 0) return lines;
  const pre = [];
  for (const p of patterns) {
    pre.push(`0, 2, ${p}, accept-encoding`);
    pre.push(`0, 1, ${p}, accept-encoding, identity`);
    pre.push(`0, 2, ${p}, if-none-match`);
    pre.push(`0, 2, ${p}, if-modified-since`);
  }
  return [...pre, ...lines];
}

function addMemoryRiskWarnings(lines, opts, report) {
  let bufferedBodyRules = 0;
  let streamRules = 0;
  for (const line of lines) {
    const fields = splitAmrsFields(line);
    if (fields.length < 2 || fields[0] !== "1") continue;
    if (["4", "5", "100"].includes(fields[1])) bufferedBodyRules++;
    else if (fields[1] === "101") streamRules++;
  }
  if (bufferedBodyRules > 0) {
    report.warnings.push(`响应阶段存在 ${bufferedBodyRules} 条缓冲 body 规则（op 4/5/100），Anywhere 会持有完整响应体并可能解压，iOS VPN 扩展内存峰值会升高`);
  }
  if (streamRules > 0 && opts && opts.useStreamScript) {
    report.warnings.push("已启用 stream-script (op 101)：转换器不再默认累积跨帧 body；如脚本自行把 ctx.body 存入 ctx.state，长连接仍可能涨内存");
  }
}

function splitAmrsFields(line) {
  const fields = [];
  let rest = line;
  for (let i = 0; i < 3 && rest; i++) {
    const idx = rest.indexOf(",");
    if (idx < 0) {
      fields.push(rest.trim());
      rest = "";
      break;
    }
    fields.push(rest.slice(0, idx).trim());
    rest = rest.slice(idx + 1);
  }
  if (rest) fields.push(rest.trim());
  return fields;
}

function generateArrs(name, lines, m, opts, routing) {
  if (lines.length === 0) return "";
  const parts = [];
  if (opts.includeMetadata) parts.push(metadataComments(m, opts));
  parts.push("name = " + name);
  if (routing && routing > 0) parts.push("routing = " + routing);
  parts.push("");
  parts.push(...lines);
  return parts.join("\n") + "\n";
}

function generateAmrs(name, hostnames, lines, m, opts, parameters) {
  parameters = parameters || [];
  if (lines.length === 0 && hostnames.length === 0 && parameters.length === 0) return "";
  const parts = [];
  if (opts.includeMetadata) parts.push(metadataComments(m, opts));
  parts.push(`name = ${name}`);
  if (hostnames.length > 0) parts.push(`hostname = ${hostnames.join(", ")}`);
  parts.push("");
  if (parameters.length > 0) {
    parts.push("[Parameter]");
    for (const parameter of parameters) parts.push(formatAmrsParameter(parameter));
    parts.push("", "[Rule]");
  }
  parts.push(...lines);
  return parts.join("\n") + "\n";
}

function formatAmrsParameter(parameter) {
  const fields = [
    String(parameter.type || 0),
    String(parameter.dataType || 0),
    parameter.name || "",
    parameter.label || "",
    parameter.description || "",
    parameter.defaultValue || "",
  ];
  if (parameter.type === 1 && parameter.options && parameter.options.length) {
    fields.push("[" + parameter.options.map(String).join(", ") + "]");
  }
  return fields.map((field) => quoteField(String(field))).join(", ");
}

function inferContentType(lines) {
  for (const line of lines) {
    if (line.startsWith("0, 0, ")) {
      const rest = line.slice(6);
      const idx = rest.indexOf(", 2, ");
      if (idx >= 0) {
        const content = rest.slice(idx + 5).trim();
        if (
          content.startsWith("{") ||
          content.startsWith("[") ||
          content.startsWith('"{"') ||
          content.startsWith('"["') ||
          content.includes('"code"')
        )
          return "application/json; charset=utf-8";
      }
    }
  }
  return "";
}

function metadataComments(m, opts) {
  opts = opts || {};
  const parts = ["# 由 module2anywhere 从 " + m.source + " 模块转换"];
  if (opts.sourceURL) parts.push("# source: " + opts.sourceURL);
  // 如果是从 quantumult.app add-resource 链接提取的，添加解码后的原始地址
  if (opts.addResourceURL) parts.push("# add-resource: " + opts.addResourceURL);
  if (opts.serviceURL) parts.push("# this: " + opts.serviceURL);
  if (m.desc) parts.push("# desc: " + m.desc);
  if (m.author) parts.push("# author: " + m.author);
  if (m.homepage) parts.push("# homepage: " + m.homepage);
  if (m.date) parts.push("# date: " + m.date);
  parts.push("");
  return parts.join("\n");
}

function deriveNameFromURL(rawURL) {
  try {
    const url = new URL(rawURL);
    const parts = url.pathname.split("/");
    let filename = parts[parts.length - 1];
    for (const ext of [".plugin", ".sgmodule", ".lpx", ".conf", ".list"]) {
      if (filename.endsWith(ext)) filename = filename.slice(0, -ext.length);
    }
    return filename || "Unnamed";
  } catch {
    return "Unnamed";
  }
}

// isAddResourceURL 判断 URL 是否为 Quantumult X 的 add-resource 一键订阅协议。
// 形式：https://quantumult.app/x/open-app/add-resource?remote-resource=<encoded-json>
function isAddResourceURL(rawURL) {
  try {
    const u = new URL(rawURL);
    const host = (u.hostname || "").toLowerCase();
    if (!host.endsWith("quantumult.app")) return false;
    let p = u.pathname;
    if (p.endsWith("/")) p = p.slice(0, -1);
    return p === "/x/open-app/add-resource";
  } catch {
    return false;
  }
}

// extractAddResourceURLs 从 quantumult.app add-resource 链接展开远端订阅 URL 列表。
// remote-resource 既可能直接是 JSON，也可能被 URL 编码一次；返回的列表只保留以 http(s):// 开头的纯 URL。
function extractAddResourceURLs(rawURL) {
  const u = new URL(rawURL);
  let raw = u.searchParams.get("remote-resource") || "";
  if (!raw) throw new Error("缺少 remote-resource 参数");
  try {
    raw = decodeURIComponent(raw);
  } catch (e) {
    /* 保留原值 */
  }
  let payload;
  try {
    payload = JSON.parse(raw);
  } catch (e) {
    throw new Error("remote-resource JSON 解析失败: " + e.message);
  }
  const keys = [
    "rewrite_remote",
    "server_remote",
    "filter_remote",
    "task_remote",
  ];
  const all = [];
  for (const k of keys) {
    const arr = payload[k];
    if (Array.isArray(arr)) all.push(...arr);
  }
  const urls = [];
  for (const entry of all) {
    if (typeof entry !== "string") continue;
    let s = entry.trim();
    if (!s) continue;
    const idx = s.indexOf(",");
    if (idx > 0) s = s.slice(0, idx).trim();
    if (s.startsWith("http://") || s.startsWith("https://")) urls.push(s);
  }
  return urls;
}

// ===================== 共享库对象 =====================
// 端点文件通过 `const lib = { ... }` 直接访问这些函数
// 不需要 import 任何模块
// ===================== 缓存 =====================

// SimpleTTLCache 带过期时间的内存缓存，用于缓存转换结果。
// EdgeOne 边缘节点上全局变量在实例生命周期内持久，可跨请求复用。
const _cacheStore = new Map();
const CACHE_TTL_MS = 5 * 60 * 1000; // 5 分钟
const CACHE_MAX_SIZE = 256;

// cacheGet 读取缓存，命中且未过期返回 { hit: true, value }，否则 { hit: false }。
function cacheGet(key) {
  var entry = _cacheStore.get(key);
  if (!entry) return { hit: false };
  if (Date.now() > entry.expiresAt) {
    _cacheStore.delete(key);
    return { hit: false };
  }
  return { hit: true, value: entry.value };
}

// cachePut 写入缓存。
function cachePut(key, value) {
  // 容量超限时淘汰过期条目
  if (_cacheStore.size >= CACHE_MAX_SIZE) {
    for (var [k, v] of _cacheStore) {
      if (Date.now() > v.expiresAt) _cacheStore.delete(k);
    }
    // 仍超限则删除最早的
    if (_cacheStore.size >= CACHE_MAX_SIZE) {
      var firstKey = _cacheStore.keys().next().value;
      if (firstKey !== undefined) _cacheStore.delete(firstKey);
    }
  }
  _cacheStore.set(key, {
    value: value,
    expiresAt: Date.now() + CACHE_TTL_MS,
  });
}

// cacheKey 生成缓存键。
function normalizeScriptMode(value) {
  return String(value || "").toLowerCase().trim() === "loader" ? "loader" : "inline";
}

function cacheKey(url, name, fetchScripts, generalize, preserveParameters, args, scriptMode, wrapScripts, maxInputBytes, maxScriptBytes, maxScriptFetches) {
  const parts = [];
  const source = args || {};
  for (const key of Object.keys(source).sort()) {
    parts.push(key + "=" + source[key]);
  }
  return url + "|" + name + "|" + fetchScripts + "|" + generalize + "|" + !!preserveParameters + "|" + !!wrapScripts + "|" + normalizeScriptMode(scriptMode) + "|" + Number(maxInputBytes || 0) + "|" + Number(maxScriptBytes || 0) + "|" + Number(maxScriptFetches || 0) + "|" + parts.join("&");
}

function truthyInput(value) {
  return /^(?:1|true|yes|on)$/i.test(String(value || "").trim());
}

function positiveIntInput(value, fallback) {
  const n = parseInt(String(value || "").trim(), 10);
  return Number.isFinite(n) && n > 0 ? n : fallback;
}

function queryArguments(query) {
  const out = {};
  query = query || {};
  for (const key of Object.keys(query)) {
    let name = "";
    if (key.indexOf("argument.") === 0) name = key.slice("argument.".length);
    else if (key.indexOf("arg.") === 0) name = key.slice("arg.".length);
    if (!/^[A-Za-z_][A-Za-z0-9_-]*$/.test(name)) continue;
    out[name] = String(query[key]);
  }
  return out;
}

const lib = {
  detectSource,
  parse,
  deriveNameFromURL,
  defaultConvertOptions,
  convert,
  appendByAction,
  fetchRemoteWithProxy,
  getUserAgent,
  isAddResourceURL,
  extractAddResourceURLs,
  cacheGet,
  cachePut,
  cacheKey,
  truthyInput,
  normalizeScriptMode,
  positiveIntInput,
  queryArguments,
  fetchAndRewriteScript,
};

// 将所有函数通过命名导出暴露
export {
  splitSections,
  parseMeta,
  splitCSVFields,
  parseKeyValueList,
  trimQuotes,
  normalizeHostnames,
  stripInlineComment,
  dedupStrings,
  splitFirstWhitespace,
  splitWhitespace,
  tokenizeKV,
  parseKVArgs,
  convertURLPattern,
  generalizeHost,
  dotPathToJSONPath,
  quoteField,
  hasCaptureGroup,
  GITHUB_PROXIES,
  GITHUB_HOSTS,
  DEFAULT_USER_AGENT,
  USER_AGENTS,
  getUserAgent,
  isGitHubURL,
  isProxyURL,
  proxyURLWithPrefix,
  fetchRemoteWithProxy,
  isRemote,
  resolveScriptPath,
  scriptLoaderURL,
  encodeLoaderScript,
  fetchAndEncodeScript,
  fetchAndRewriteScript,
  encodeInlineScript,
  encodeInlineRewriteJS,
  buildWrappedScript,
  encodeWrappedScript,
  rewriteScriptAPI,
  rewriteHttpClientCalls,
  rewriteDoneCalls,
  wrapAsProcess,
  indent,
  buildRedirectScript,
  jsRegexLiteral,
  jsCaptureReplace,
  wrapAsStreamScript,
  extractProcessBody,
  detectSource,
  parseLoonRules,
  parseLoonRewriteAction,
  parseLoonRewrites,
  parseLoonScriptLine,
  parseLoonScripts,
  parseLoonArguments,
  parseLoonMitM,
  parseLoon,
  parseSurgeURLRewriteAction,
  parseSurgeURLRewrites,
  parseSurgeHeaderRewrites,
  parseSurgeMapLocals,
  parseSurgeScriptLine,
  parseSurgeScripts,
  parseSurgeMITM,
  parseSurge,
  parseQuantumultX,
  applyUserScriptMeta,
  extractQXHostnameValue,
  parseQuantumultXLine,
  splitQXTokens,
  parseQXRoutingRule,
  parse,
  isRejectAction,
  normalizeRoutingRuleType,
  normalizeWildcardDomain,
  defaultConvertOptions,
  convert,
  convertRoutingRules,
  appendByAction,
  convertURLRegexReject,
  convertRewriteRules,
  convertRewriteRule,
  convertHeaderRules,
  addEncodingPreprocess,
  splitAmrsFields,
  generateArrs,
  generateAmrs,
  inferContentType,
  metadataComments,
  deriveNameFromURL,
  isAddResourceURL,
  extractAddResourceURLs,
  cacheGet,
  cachePut,
  cacheKey,
  normalizeScriptMode,
  truthyInput,
  positiveIntInput,
  queryArguments,
  // 也导出 lib 对象本身，方便端点文件使用 lib.xxx 形式
  lib,
};
