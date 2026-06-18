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

const splitSections = (content) => {
  const sections = [];
  let current = null;
  const bodyLines = [];
  for (const raw of content.split('\n')) {
    const line = raw.replace(/\r$/, '');
    const trimmed = line.trim();
    if (trimmed === '') {
      if (current) bodyLines.push(line);
      continue;
    }
    if (trimmed.startsWith('#!')) {
      if (!current) current = { name: '__meta__', body: '' };
      bodyLines.push(line);
      continue;
    }
    if (trimmed.startsWith('[') && trimmed.endsWith(']')) {
      if (current) {
        current.body = bodyLines.join('\n');
        sections.push(current);
      }
      current = { name: trimmed.slice(1, -1).trim(), body: '' };
      bodyLines.length = 0;
      continue;
    }
    bodyLines.push(line);
  }
  if (current) {
    current.body = bodyLines.join('\n');
    sections.push(current);
  }
  return sections;
};

const parseMeta = (body) => {
  const meta = {};
  for (const line of body.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed.startsWith('#!')) continue;
    const kv = trimmed.slice(2);
    const idx = kv.indexOf('=');
    if (idx < 0) continue;
    const key = kv.slice(0, idx).trim();
    const val = kv.slice(idx + 1).trim();
    meta[key] = val;
  }
  return meta;
};

const splitCSVFields = (line) => {
  const fields = [];
  let buf = '';
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
    } else if (c === ',') {
      fields.push(buf.trim());
      buf = '';
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
    const idx = t.indexOf('=');
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

const normalizeHostnames = (raw) => {
  const parts = raw.split(',');
  const out = [];
  const seen = new Set();
  for (let p of parts) {
    p = p.trim();
    if (!p) continue;
    p = p.replace(/^%APPEND%/i, '').trim();
    if (p.startsWith('*.')) {
      p = p.slice(2);
    } else if (p.startsWith('*') && !p.includes('.')) {
      p = p.slice(1);
    }
    p = p.trim();
    if (!p || seen.has(p)) continue;
    seen.add(p);
    out.push(p);
  }
  return out;
};

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
  s = s.replace(/^[\t ]+/, '');
  for (let i = 0; i < s.length; i++) {
    if (s[i] === ' ' || s[i] === '\t') {
      return [s.slice(0, i), s.slice(i + 1).replace(/^[\t ]+/, '')];
    }
  }
  return [s, ''];
};

const splitWhitespace = (s) => s.split(/\s+/).filter(Boolean);

const tokenizeKV = (s) => {
  const tokens = [];
  let buf = '';
  let inQuote = false;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (inQuote) {
      buf += c;
      if (c === '"') inQuote = false;
    } else if (c === '"') {
      inQuote = true;
      buf += c;
    } else if (c === ' ' || c === '\t') {
      if (buf) {
        tokens.push(buf);
        buf = '';
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
    const idx = t.indexOf('=');
    if (idx <= 0) continue;
    const key = t.slice(0, idx).trim().toLowerCase();
    const val = trimQuotes(t.slice(idx + 1));
    args[key] = val;
  }
};

// ===================== URL 模式转换 =====================

const convertURLPattern = (pattern, generalize) => {
  if (!pattern) return pattern;
  pattern = pattern.replace(/\\\//g, '/');
  if (generalize) {
    pattern = generalizeHost(pattern);
  }
  if (pattern.endsWith('\\?')) {
    pattern = pattern.slice(0, -2) + '(?:\\?|$)';
  }
  return pattern;
};

function generalizeHost(pattern) {
  if (!pattern.startsWith('^http')) return pattern;
  const idx = pattern.indexOf('://');
  if (idx < 0) return pattern;
  const rest = pattern.slice(idx + 3);
  const slash = rest.indexOf('/');
  if (slash < 0) return pattern;
  const hostPart = rest.slice(0, slash);
  if (containsCaptureGroup(hostPart)) return pattern;
  return pattern.slice(0, idx + 3) + '[^/]+' + rest.slice(slash);
}

function containsCaptureGroup(s) {
  for (let i = 0; i < s.length; i++) {
    if (s[i] === '(') {
      if (i + 2 < s.length && s[i + 1] === '?') continue;
      return true;
    }
  }
  return false;
}

const dotPathToJSONPath = (dotPath) => {
  dotPath = dotPath.trim();
  if (!dotPath) return '$';
  if (dotPath.startsWith('$.') || dotPath === '$') return dotPath;
  const parts = dotPath.split('.');
  let result = '$';
  for (const part of parts) {
    if (!part) continue;
    const bracketIdx = part.indexOf('[');
    if (bracketIdx >= 0) {
      const key = part.slice(0, bracketIdx);
      const brackets = part.slice(bracketIdx);
      if (key) result += '.' + key;
      result += brackets;
      continue;
    }
    if (/^\d+$/.test(part)) {
      result += '[' + part + ']';
      continue;
    }
    result += '.' + part;
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
  return s[0] === ' ' || s[0] === '\t' || s[s.length - 1] === ' ' || s[s.length - 1] === '\t';
}

const hasCaptureGroup = (s) => {
  for (let i = 0; i < s.length; i++) {
    if (s[i] === '$' && i + 1 < s.length && s[i + 1] >= '1' && s[i + 1] <= '9') {
      return true;
    }
  }
  return false;
};

// ===================== 远程获取 =====================

const GITHUB_PROXIES = ['https://ghfast.top/', 'https://ph.ipv9.win/'];
const GITHUB_HOSTS = ['raw.githubusercontent.com', 'github.com', 'gist.githubusercontent.com', 'codeload.github.com'];
const DEFAULT_USER_AGENT = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36';
const USER_AGENTS = {
  surge: 'Shadowrocket/308 CFNetwork/1568.300 Darwin/24.1.0',
  loon: 'Loon/750 CFNetwork/1568.300 Darwin/24.1.0',
  quantumultx: 'Quantumult%20X%20Patched/1.0.30 (iPhone;iOS%2017.0)',
  unknown: DEFAULT_USER_AGENT,
};

const getUserAgent = (source) => (!source ? DEFAULT_USER_AGENT : USER_AGENTS[source] || DEFAULT_USER_AGENT);

function isGitHubURL(url) {
  return GITHUB_HOSTS.some((host) => url.includes(host));
}

// 检测 URL 是否已经是代理 URL（以已知代理前缀开头）
function isProxyURL(url) {
  for (var i = 0; i < GITHUB_PROXIES.length; i++) {
    if (url.indexOf(GITHUB_PROXIES[i]) === 0) return true;
  }
  return false;
}

function proxyURLWithPrefix(proxyPrefix, rawURL) {
  return proxyPrefix + rawURL.replace(/^https:\/\//, '');
}

// fetchRaw 带超时（10 秒），避免代理卡死整个请求
// 使用 Promise.race + setTimeout 实现超时
async function fetchRaw(url, userAgent) {
  var ua = userAgent || DEFAULT_USER_AGENT;
  var fetchPromise = fetch(url, {
    headers: { 'User-Agent': ua, Accept: '*/*' },
  });
  var timeoutPromise = new Promise(function (_, reject) {
    setTimeout(function () { reject(new Error('timeout 10s: ' + url)); }, 10000);
  });
  var resp = await Promise.race([fetchPromise, timeoutPromise]);
  if (!resp.ok) throw new Error('请求 ' + url + ' 返回状态码 ' + resp.status);
  return await resp.text();
}

async function fetchRemoteWithProxy(url, userAgent) {
  var ua = userAgent || DEFAULT_USER_AGENT;
  // 非 GitHub URL 直接 fetch
  if (!isGitHubURL(url)) return fetchRaw(url, ua);
  // 如果 URL 已经是代理 URL（用户手动加了代理前缀），直接 fetch
  if (isProxyURL(url)) return fetchRaw(url, ua);
  // 原始 GitHub URL：依次尝试代理，最后 fallback 直连
  for (var i = 0; i < GITHUB_PROXIES.length; i++) {
    var proxyURL = proxyURLWithPrefix(GITHUB_PROXIES[i], url);
    try {
      var data = await fetchRaw(proxyURL, ua);
      if (data) return data;
    } catch (e) { /* 代理失败，继续尝试下一个 */ }
  }
  return fetchRaw(url, ua);
}

function isRemote(path) {
  const lower = path.toLowerCase();
  return lower.startsWith('http://') || lower.startsWith('https://');
}

function resolveScriptPath(scriptPath, baseURL) {
  if (isRemote(scriptPath)) return scriptPath;
  if (!baseURL) return scriptPath;
  if (isRemote(baseURL) && !scriptPath.startsWith('/')) {
    const idx = baseURL.lastIndexOf('/');
    if (idx > 0) return baseURL.slice(0, idx + 1) + scriptPath;
  }
  return scriptPath;
}

// ===================== 脚本改写 =====================

async function fetchAndEncodeScript(scriptPath, fetchScripts, phase, useStreamScript, userAgent) {
  if (!fetchScripts) {
    const placeholder = `function process(ctx){Anywhere.log.warning("script not fetched: ${scriptPath}");}`;
    return btoa(unescape(encodeURIComponent(placeholder)));
  }
  try {
    const src = await fetchRemoteWithProxy(scriptPath, userAgent);
    const rewritten = rewriteScriptAPI(src, phase);
    const finalSrc = useStreamScript ? wrapAsStreamScript(rewritten, phase) : rewritten;
    return btoa(unescape(encodeURIComponent(finalSrc)));
  } catch (e) {
    throw new Error(`下载脚本失败 "${scriptPath}": ${e}`);
  }
}

function encodeInlineScript(rawJS, phase) {
  const rewritten = rewriteScriptAPI(rawJS, phase);
  return btoa(unescape(encodeURIComponent(rewritten)));
}

function encodeInlineRewriteJS(rawJS, phase) {
  let js = rawJS.trim();
  if (js.startsWith('{') && js.endsWith('}')) js = js.slice(1, -1).trim();
  return encodeInlineScript(js, phase);
}

function rewriteScriptAPI(src, phase) {
  const needsAsync = src.includes('$httpClient') || src.includes('$done({response:');
  let out = src;
  out = out.replace(/\$request\.url/g, 'ctx.url');
  out = out.replace(/\$request\.method/g, 'ctx.method');
  out = out.replace(/\$request\.headers/g, 'ctx.headers');
  out = out.replace(/\$response\.status/g, 'ctx.status');
  out = out.replace(/\$response\.headers/g, 'ctx.headers');
  out = out.replace(/\$request\.body/g, 'ctx.body');
  out = out.replace(/\$response\.body/g, 'ctx.body');
  out = rewriteDoneCalls(out);
  out = out.replace(/\$persistentStore\.read\(\s*([^)]+?)\s*\)/g, 'Anywhere.store.getString($1, true)');
  out = out.replace(/\$persistentStore\.write\(\s*([^,]+?)\s*,\s*([^)]+?)\s*\)/g, 'Anywhere.store.set($2, $1, true)');
  out = out.replace(/\$notification\.post\(\s*([^,]+?)\s*,\s*([^,]*?)\s*,\s*([^)]+?)\s*\)/g, 'Anywhere.log.info($1 + " " + $2 + " " + $3)');
  out = rewriteHttpClientCalls(out);
  out = out.replace(/JSON\.parse\(ctx\.body\)/g, 'JSON.parse(Anywhere.codec.utf8.decode(ctx.body))');
  out = out.replace(/JSON\.parse\(\$response\.body\)/g, 'JSON.parse(Anywhere.codec.utf8.decode(ctx.body))');
  out = wrapAsProcess(out, phase, needsAsync);
  return out;
}

function rewriteHttpClientCalls(src) {
  let out = src;
  out = out.replace(/\$httpClient\.get\(\s*([^,]+?)\s*,/g, 'await Anywhere.http.get($1');
  out = out.replace(/\$httpClient\.post\(\s*([^,]+?)\s*,\s*([^,]+?)\s*,/g, 'await Anywhere.http.post($1, $2');
  out = out.replace(/\$httpClient\.put\(\s*([^,]+?)\s*,/g, 'await Anywhere.http.put($1');
  out = out.replace(/\$httpClient\.delete\(\s*([^,]+?)\s*,/g, 'await Anywhere.http.delete($1');
  out = out.replace(/\$httpClient\.request\(\s*([^,]+?)\s*,/g, 'await Anywhere.http.request($1');
  return out;
}

function rewriteDoneCalls(src) {
  let out = src;
  out = out.replace(/\$done\(\s*\{\s*\}\s*\)/g, 'Anywhere.done()');
  out = out.replace(/\$done\(\s*\)/g, 'Anywhere.done()');
  out = out.replace(/\$done\(\s*\{\s*body\s*:\s*([^}]+?)\s*\}\s*\)/g, 'ctx.body = $1; Anywhere.done()');
  out = out.replace(/\$done\(\s*\{\s*response\s*:\s*(\{[^}]*\})\s*\}\s*\)/g, 'Anywhere.respond($1)');
  out = out.replace(/\$done\(\s*\{[^}]*\}\s*\)/g, 'Anywhere.done()');
  return out;
}

function wrapAsProcess(src, phase, needsAsync) {
  const trimmed = src.trim();
  const asyncKw = needsAsync ? 'async ' : '';
  const phaseCheck = phase === 1 ? 'response' : 'request';
  if (/^function\s+process\s*\(\s*ctx\s*\)/m.test(trimmed)) {
    if (needsAsync && !trimmed.startsWith('async ')) return 'async ' + trimmed;
    return trimmed;
  }
  if (/^async\s+function\s+process\s*\(\s*ctx\s*\)/m.test(trimmed)) return trimmed;
  if (/^function\s+run\s*\(\s*\)/m.test(trimmed)) {
    return `${asyncKw}function process(ctx) {
  if (ctx.phase !== "${phaseCheck}") return;
  try { run(); } catch (e) { Anywhere.log.warning("script error: " + e); }
}
${trimmed}`;
  }
  return `${asyncKw}function process(ctx) {
  if (ctx.phase !== "${phaseCheck}") return;
  try {
${indent(trimmed, '    ')}
  } catch (e) { Anywhere.log.warning("script error: " + e); }
  Anywhere.done();
}`;
}

function indent(s, prefix) {
  return s.split('\n').map((l) => (l ? prefix + l : l)).join('\n');
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
  const escaped = pattern.replace(/\//g, '\\/');
  return '/' + escaped + '/';
}

function jsCaptureReplace(url) {
  let buf = '"';
  let i = 0;
  while (i < url.length) {
    if (url[i] === '$' && i + 1 < url.length && url[i + 1] >= '1' && url[i + 1] <= '9') {
      buf += '" + m[' + url[i + 1] + '] + "';
      i += 2;
    } else {
      if (url[i] === '"' || url[i] === '\\') buf += '\\';
      buf += url[i];
      i++;
    }
  }
  buf += '"';
  return buf;
}

function wrapAsStreamScript(rewrittenSrc, phase) {
  const phaseCheck = phase === 1 ? 'response' : 'request';
  const inner = extractProcessBody(rewrittenSrc) || rewrittenSrc;
  return `async function process(ctx) {
  if (ctx.phase !== "${phaseCheck}" || !ctx.body) return;
  if (!ctx.state.buf) ctx.state.buf = [];
  if (!ctx.state.text) ctx.state.text = "";
  ctx.state.buf.push(ctx.body);
  try { ctx.state.text += Anywhere.codec.utf8.decode(ctx.body); } catch (e) { Anywhere.log.warning("decode frame failed: " + e); }
  if (!ctx.frame || !ctx.frame.end) return;
  try {
    ctx.body = Anywhere.codec.utf8.encode(ctx.state.text);
${indent(inner, '    ')}
  } catch (e) { Anywhere.log.warning("stream process failed: " + e); }
  Anywhere.done();
}`;
}

function extractProcessBody(src) {
  const trimmed = src.trim();
  if (!trimmed.includes('function process(ctx)')) return '';
  const firstBrace = trimmed.indexOf('{');
  const lastBrace = trimmed.lastIndexOf('}');
  if (firstBrace < 0 || lastBrace < 0 || lastBrace <= firstBrace) return '';
  return trimmed.slice(firstBrace + 1, lastBrace).trim();
}

// ===================== 解析器 =====================

function detectSource(content, filename) {
  const lowerName = filename.toLowerCase();
  if (lowerName.endsWith('.plugin')) return 'loon';
  if (lowerName.endsWith('.sgmodule')) return 'surge';
  if (lowerName.endsWith('.conf')) return 'quantumultx';
  const lowerContent = content.toLowerCase();
  if (lowerContent.includes('[url rewrite]') || lowerContent.includes('[header rewrite]') || lowerContent.includes('[map local]')) return 'surge';
  if (lowerContent.includes('[rewrite]') || lowerContent.includes('[argument]')) return 'loon';
  if (content.includes('[MitM]')) return 'loon';
  if (content.includes('[MITM]')) return 'surge';
  // 无段头且含 QX 行式规则特征时识别为 QuantumultX
  if (/^\S+\s+url\s+(reject|script-response-body|script-request-body|echo-response|jsonjq-response-body|response-body|302|307)/im.test(content)) return 'quantumultx';
  return 'loon';
}

function parseLoonRules(body) {
  const rules = [];
  for (const line of body.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#') || trimmed.startsWith('//')) continue;
    const fields = splitCSVFields(trimmed);
    if (fields.length < 2) continue;
    const r = { raw: trimmed, type: fields[0].toUpperCase().trim(), value: fields[1].trim(), action: '', options: [] };
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
  if (action === 'url') { [action, remain] = splitFirstWhitespace(remain); action = action.toLowerCase().trim(); }
  switch (action) {
    case 'reject': case 'reject-200': case 'reject-dict': case 'reject-array': case 'reject-img':
      return { action, args, rawJS: '' };
    case '302': case '307':
      args.url = remain.trim();
      return { action, args, rawJS: '' };
    case 'mock-response-body':
      parseKVArgs(remain, args);
      return { action, args, rawJS: '' };
    case 'response-body-json-del': case 'response-body-json-add': case 'response-body-json-replace': {
      const tokens = splitWhitespace(remain);
      if (tokens.length >= 1) args.path = tokens[0];
      if (tokens.length >= 2) args.value = tokens.slice(1).join(' ');
      return { action, args, rawJS: '' };
    }
    case 'request-header': case 'request-body': case 'response-body':
      return { action, args, rawJS: remain.trim() };
    case 'header-del':
      args.header = remain.trim();
      return { action, args, rawJS: '' };
    case 'response-body-replace-regex': {
      const [search, repl] = splitFirstWhitespace(remain);
      args.search = trimQuotes(search);
      args.replacement = trimQuotes(repl);
      return { action, args, rawJS: '' };
    }
    default:
      args._raw = remain.trim();
      return { action, args, rawJS: '' };
  }
}

function parseLoonRewrites(body) {
  const rules = [];
  for (const line of body.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#') || trimmed.startsWith('//')) continue;
    const r = { raw: trimmed, pattern: '', action: '', args: {}, rawJS: '' };
    const [pattern, rest] = splitFirstWhitespace(trimmed);
    r.pattern = pattern;
    if (!rest) { rules.push(r); continue; }
    const { action, args, rawJS } = parseLoonRewriteAction(rest);
    r.action = action; r.args = args; r.rawJS = rawJS;
    rules.push(r);
  }
  return rules;
}

function parseLoonScriptLine(line) {
  const [phase, rest] = splitFirstWhitespace(line);
  if (!rest) return null;
  const [pattern, params] = splitFirstWhitespace(rest);
  const s = { raw: line, pattern, phase: 0, scriptPath: '', requiresBody: false, binaryBody: false, argument: '', tag: '', maxSize: 0, engine: '' };
  switch (phase.toLowerCase().trim()) {
    case 'http-request': s.phase = 0; break;
    case 'http-response': s.phase = 1; break;
    case 'cron': return null;
    default: return null;
  }
  const tokens = splitCSVFields(params);
  const { args } = parseKeyValueList(tokens);
  s.scriptPath = args['script-path'] || '';
  s.tag = args.tag || '';
  s.argument = args.argument || '';
  s.engine = args.engine || '';
  if (args['requires-body']) s.requiresBody = args['requires-body'].toLowerCase() === 'true' || args['requires-body'] === '1';
  if (args['binary-body-mode']) s.binaryBody = args['binary-body-mode'].toLowerCase() === 'true' || args['binary-body-mode'] === '1';
  if (args['max-size']) { const n = parseInt(args['max-size'], 10); if (!isNaN(n)) s.maxSize = n; }
  return s;
}

function parseLoonScripts(body) {
  const scripts = [];
  for (const line of body.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#') || trimmed.startsWith('//')) continue;
    const s = parseLoonScriptLine(trimmed);
    if (s) scripts.push(s);
  }
  return scripts;
}

function parseLoonArguments(body) {
  const args = [];
  for (const line of body.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#') || trimmed.startsWith('//')) continue;
    const idx = trimmed.indexOf('=');
    if (idx <= 0) continue;
    args.push({ key: trimmed.slice(0, idx).trim(), value: trimmed.slice(idx + 1).trim(), raw: trimmed });
  }
  return args;
}

function parseLoonMitM(body) {
  for (const line of body.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#')) continue;
    const idx = trimmed.indexOf('=');
    if (idx <= 0) continue;
    const key = trimmed.slice(0, idx).trim().toLowerCase();
    if (key === 'hostname') return normalizeHostnames(trimmed.slice(idx + 1));
  }
  return [];
}

function parseLoon(content) {
  const m = { source: 'loon', name: '', desc: '', author: '', homepage: '', date: '', rawMeta: {}, hostnames: [], contentType: '', rules: [], rewrites: [], scripts: [], headerRWs: [], mapLocals: [], arguments: [] };
  for (const sec of splitSections(content)) {
    switch (sec.name) {
      case '__meta__': {
        const meta = parseMeta(sec.body);
        m.rawMeta = meta;
        if (meta.name) m.name = meta.name;
        if (meta.desc) m.desc = meta.desc;
        if (meta.author) m.author = meta.author;
        if (meta.homepage) m.homepage = meta.homepage;
        if (meta.date) m.date = meta.date;
        break;
      }
      case 'Rule': m.rules.push(...parseLoonRules(sec.body)); break;
      case 'Rewrite': m.rewrites.push(...parseLoonRewrites(sec.body)); break;
      case 'Script': m.scripts.push(...parseLoonScripts(sec.body)); break;
      case 'Argument': m.arguments.push(...parseLoonArguments(sec.body)); break;
      case 'MitM': m.hostnames.push(...parseLoonMitM(sec.body)); break;
    }
  }
  return m;
}

function parseSurgeURLRewriteAction(rest) {
  const args = {};
  let [action, remain] = splitFirstWhitespace(rest);
  action = action.toLowerCase().trim();
  switch (action) {
    case 'reject': case 'reject-200': case 'reject-dict': case 'reject-array': case 'reject-img':
      return { action, args, rawJS: '' };
    case '302': case '307':
      args.url = remain.trim();
      return { action, args, rawJS: '' };
    case '_request-header': case '_request-body': case '_response-body':
      return { action, args, rawJS: remain.trim() };
    case '_header-del':
      args.header = remain.trim();
      return { action: 'header-del', args, rawJS: '' };
    default:
      args._raw = remain.trim();
      return { action, args, rawJS: '' };
  }
}

function parseSurgeURLRewrites(body) {
  const rules = [];
  for (const line of body.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#') || trimmed.startsWith('//')) continue;
    const r = { raw: trimmed, pattern: '', action: '', args: {}, rawJS: '' };
    const [pattern, rest] = splitFirstWhitespace(trimmed);
    r.pattern = pattern;
    if (!rest) { rules.push(r); continue; }
    const { action, args, rawJS } = parseSurgeURLRewriteAction(rest);
    r.action = action; r.args = args; r.rawJS = rawJS;
    rules.push(r);
  }
  return rules;
}

function parseSurgeHeaderRewrites(body) {
  const rules = [];
  for (const line of body.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#') || trimmed.startsWith('//')) continue;
    const r = { raw: trimmed, pattern: '', phase: 0, op: '', name: '', value: '' };
    const [pattern, rest] = splitFirstWhitespace(trimmed);
    r.pattern = pattern;
    if (!rest) continue;
    const [target, rest2] = splitFirstWhitespace(rest);
    switch (target.toLowerCase().trim()) {
      case 'request-header': r.phase = 0; break;
      case 'response-header': r.phase = 1; break;
      default: continue;
    }
    const [op, rest3] = splitFirstWhitespace(rest2);
    r.op = op.toLowerCase().trim();
    const tokens = splitWhitespace(rest3);
    if (tokens.length >= 1) r.name = tokens[0];
    if (tokens.length >= 2) r.value = tokens.slice(1).join(' ');
    rules.push(r);
  }
  return rules;
}

function parseSurgeMapLocals(body) {
  const rules = [];
  for (const line of body.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#') || trimmed.startsWith('//')) continue;
    const r = { raw: trimmed, pattern: '', dataURL: '', header: '' };
    const [pattern, rest] = splitFirstWhitespace(trimmed);
    r.pattern = pattern;
    const tokens = tokenizeKV(rest);
    for (const t of tokens) {
      const idx = t.indexOf('=');
      if (idx <= 0) continue;
      const key = t.slice(0, idx).trim().toLowerCase();
      const val = trimQuotes(t.slice(idx + 1));
      if (key === 'data') r.dataURL = val;
      else if (key === 'header') r.header = val;
    }
    rules.push(r);
  }
  return rules;
}

function parseSurgeScriptLine(line) {
  const idx = line.indexOf('=');
  if (idx <= 0) return null;
  const params = line.slice(idx + 1).trim();
  const tokens = splitCSVFields(params);
  const { args } = parseKeyValueList(tokens);
  const s = { raw: line, pattern: args.pattern || '', phase: 0, scriptPath: args['script-path'] || '', requiresBody: false, binaryBody: false, argument: args.argument || '', tag: args.tag || '', maxSize: 0, engine: args.engine || '' };
  switch ((args.type || '').toLowerCase().trim()) {
    case 'http-request': s.phase = 0; break;
    case 'http-response': s.phase = 1; break;
    case 'cron': return null;
    default: return null;
  }
  if (args['requires-body']) s.requiresBody = args['requires-body'] === '1' || args['requires-body'].toLowerCase() === 'true';
  if (args['binary-body-mode']) s.binaryBody = args['binary-body-mode'] === '1' || args['binary-body-mode'].toLowerCase() === 'true';
  if (args['max-size']) { const n = parseInt(args['max-size'], 10); if (!isNaN(n)) s.maxSize = n; }
  return s;
}

function parseSurgeScripts(body) {
  const scripts = [];
  for (const line of body.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#') || trimmed.startsWith('//')) continue;
    const s = parseSurgeScriptLine(trimmed);
    if (s) scripts.push(s);
  }
  return scripts;
}

function parseSurgeMITM(body) {
  for (const line of body.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#')) continue;
    const idx = trimmed.indexOf('=');
    if (idx <= 0) continue;
    const key = trimmed.slice(0, idx).trim().toLowerCase();
    if (key === 'hostname') return normalizeHostnames(trimmed.slice(idx + 1));
  }
  return [];
}

function parseSurge(content) {
  const m = { source: 'surge', name: '', desc: '', author: '', homepage: '', date: '', rawMeta: {}, hostnames: [], contentType: '', rules: [], rewrites: [], scripts: [], headerRWs: [], mapLocals: [], arguments: [] };
  for (const sec of splitSections(content)) {
    switch (sec.name) {
      case '__meta__': {
        const meta = parseMeta(sec.body);
        m.rawMeta = meta;
        if (meta.name) m.name = meta.name;
        if (meta.desc) m.desc = meta.desc;
        if (meta.author) m.author = meta.author;
        if (meta.homepage) m.homepage = meta.homepage;
        if (meta.date) m.date = meta.date;
        break;
      }
      case 'Rule': m.rules.push(...parseLoonRules(sec.body)); break;
      case 'URL Rewrite': m.rewrites.push(...parseSurgeURLRewrites(sec.body)); break;
      case 'Header Rewrite': m.headerRWs.push(...parseSurgeHeaderRewrites(sec.body)); break;
      case 'Map Local': m.mapLocals.push(...parseSurgeMapLocals(sec.body)); break;
      case 'Script': m.scripts.push(...parseSurgeScripts(sec.body)); break;
      case 'MITM': m.hostnames.push(...parseSurgeMITM(sec.body)); break;
    }
  }
  return m;
}

function parseQuantumultX(content) {
  const m = { source: 'quantumultx', name: '', desc: '', author: '', homepage: '', date: '', rawMeta: {}, hostnames: [], contentType: '', rules: [], rewrites: [], scripts: [], headerRWs: [], mapLocals: [], arguments: [] };
  applyUserScriptMeta(m, content);

  const rawLines = content.split('\n');
  const hostnames = [];

  // 第一遍：提取 hostname
  for (let i = 0; i < rawLines.length; i++) {
    const line = rawLines[i].trim();
    if (line === '') continue;
    const hs = extractQXHostnameValue(line);
    if (hs !== '') {
      hostnames.push(...normalizeHostnames(hs));
      continue;
    }
    // 注释行后可能紧跟 hostname
    if (line.startsWith('#') || line.startsWith('//')) {
      const next = (rawLines[i + 1] || '').trim();
      const nhs = extractQXHostnameValue(next);
      if (nhs !== '') hostnames.push(...normalizeHostnames(nhs));
    }
  }
  m.hostnames = dedupStrings(hostnames);

  // 第二遍：解析行式规则
  for (const raw of rawLines) {
    const line = raw.trim();
    if (line === '' || line.startsWith('#') || line.startsWith('//')) continue;
    if (line.startsWith('#!')) continue;
    if (extractQXHostnameValue(line) !== '') continue;
    if (line.startsWith('[') && line.endsWith(']')) continue;

    const rule = parseQuantumultXLine(line);
    if (!rule) continue;
    if (rule.kind === 'rewrite') {
      m.rewrites.push(rule.rewrite);
    } else if (rule.kind === 'script') {
      m.scripts.push(rule.script);
    }
  }

  return m;
}

function applyUserScriptMeta(m, content) {
  const lines = content.split('\n');
  let inBlock = false;
  for (const raw of lines) {
    const line = raw.trim();
    if (line === '// ==UserScript==') { inBlock = true; continue; }
    if (line === '// ==/UserScript==') break;
    if (!inBlock) continue;
    if (!line.startsWith('// @')) continue;
    const rest = line.slice('// @'.length).trim();
    const idx = rest.search(/\s/);
    if (idx <= 0) continue;
    const key = rest.slice(0, idx).trim();
    const val = rest.slice(idx).trim();
    if (!key || !val) continue;
    m.rawMeta[key] = val;
    const lk = key.toLowerCase();
    if (lk === 'scriptname' && !m.name) m.name = val;
    else if (lk === 'author' && !m.author) m.author = val;
    else if ((lk === 'function' || lk === 'description') && !m.desc) m.desc = val;
    else if (lk === 'updatetime' && !m.date) m.date = val;
    else if ((lk === 'homepage' || lk === 'homepageurl') && !m.homepage) m.homepage = val;
  }
}

function extractQXHostnameValue(line) {
  if (!line) return '';
  const lower = line.toLowerCase();
  const idx = lower.indexOf('hostname');
  if (idx < 0) return '';
  let rest = line.slice(idx + 'hostname'.length);
  rest = rest.replace(/^[ \t]+/, '');
  if (!rest || rest[0] !== '=') return '';
  rest = rest.slice(1).replace(/^[ \t]+/, '');
  return rest;
}

function parseQuantumultXLine(line) {
  // QX 格式：pattern url action [args...]
  const tokens = splitQXTokens(line);
  if (tokens.length < 3) return null;
  const pattern = tokens[0];
  if (tokens[1].toLowerCase() !== 'url') return null;
  const action = tokens[2].toLowerCase();

  // 脚本类
  if (action.startsWith('script-') || action === 'script-analyze-echo-response') {
    let phase = 1;
    if (action === 'script-request-body' || action === 'script-request-header') phase = 0;
    const scriptPath = tokens[3] || '';
    if (!scriptPath) return null;
    return { kind: 'script', script: { raw: line, pattern, phase, scriptPath, requiresBody: false, binaryBody: false, argument: '', tag: '', maxSize: 0, engine: '' } };
  }

  // echo-response / jsonjq-response-body / response-body 双 url 标记
  if (action === 'echo-response') {
    const r = { raw: line, pattern, action, args: {}, rawJS: '' };
    if (tokens.length >= 4) r.args['content-type'] = tokens[3];
    if (tokens.length >= 7) r.args.body = tokens[6];
    return { kind: 'rewrite', rewrite: r };
  }
  if (action === 'response-body') {
    const r = { raw: line, pattern, action, args: {}, rawJS: '' };
    if (tokens.length >= 4) r.args.search = tokens[3];
    if (tokens.length >= 7) r.args.replacement = tokens[6];
    return { kind: 'rewrite', rewrite: r };
  }
  if (action === 'jsonjq-response-body') {
    const r = { raw: line, pattern, action, args: {}, rawJS: '' };
    if (tokens.length >= 4) r.args.jq = trimQuotes(tokens.slice(3).join(' '));
    return { kind: 'rewrite', rewrite: r };
  }

  // reject / 302 等：复用 Loon rewrite action 解析（去掉 url token 后的 rest）
  const rest = tokens.slice(2).join(' ');
  const parsed = parseLoonRewriteAction(rest);
  const r = { raw: line, pattern, action: parsed.action, args: parsed.args, rawJS: parsed.rawJS };
  // Loon 解析器会把 302 的 url 放到 args.url，与 QX 一致
  return { kind: 'rewrite', rewrite: r };
}

function splitQXTokens(line) {
  const tokens = [];
  let buf = '';
  let inSingle = false;
  let inDouble = false;
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (inSingle) { buf += c; if (c === "'") inSingle = false; }
    else if (inDouble) { buf += c; if (c === '"') inDouble = false; }
    else if (c === "'") { inSingle = true; buf += c; }
    else if (c === '"') { inDouble = true; buf += c; }
    else if (c === ' ' || c === '\t') { if (buf) { tokens.push(buf); buf = ''; } }
    else { buf += c; }
  }
  if (buf) tokens.push(buf);
  return tokens;
}

function parse(content, source) {
  switch (source) {
    case 'loon': return parseLoon(content);
    case 'surge': return parseSurge(content);
    case 'quantumultx': return parseQuantumultX(content);
    default: return parseLoon(content);
  }
}

// ===================== 核心转换器 =====================

function isRejectAction(action) {
  return ['REJECT', 'REJECT-DICT', 'REJECT-ARRAY', 'REJECT-IMG', 'REJECT-200'].includes(action);
}

function defaultConvertOptions() {
  return { generalizeHost: true, encodingPreprocess: true, fetchScripts: true, includeMetadata: true, useStreamScript: false, autoContentType: true, addResourceURL: '' };
}

async function convert(m, opts) {
  const options = { ...defaultConvertOptions(), ...opts };
  const report = { skipped: [], degraded: [], warnings: [], scriptErr: [] };
  const baseName = m.name || 'module2anywhere';

  const cleanedHosts = [];
  for (const h of m.hostnames) {
    if (/[?*]/.test(h)) { report.warnings.push(`hostname 含通配符无法静态展开，已跳过: ${h}`); continue; }
    cleanedHosts.push(h);
  }
  m.hostnames = cleanedHosts;

  const [arrsLines, amrsFromRules] = convertRoutingRules(m.rules, options, report);
  const amrsLines = [
    ...amrsFromRules,
    ...convertRewriteRules(m, options, report),
    ...convertHeaderRules(m.headerRWs, options, report),
    ...await convertMapLocals(m.mapLocals, options, report, m.source),
    ...await convertScriptRules(m, options, report, m.source),
  ];

  let finalAmrsLines = amrsLines;
  if (options.encodingPreprocess) finalAmrsLines = addEncodingPreprocess(amrsLines);
  if (options.autoContentType && !m.contentType) {
    const ct = inferContentType(finalAmrsLines);
    if (ct) m.contentType = ct;
  }

  return {
    arrs: generateArrs(baseName, arrsLines, m, options),
    amrs: generateAmrs(baseName, m.hostnames, finalAmrsLines, m, options),
    arrsName: baseName + '.arrs',
    amrsName: baseName + '.amrs',
    report,
  };
}

function convertRoutingRules(rules, opts, report) {
  const arrsLines = [];
  const amrsLines = [];
  for (const r of rules) {
    switch (r.type) {
      case 'DOMAIN-SUFFIX': case 'DOMAIN': arrsLines.push(`2, ${r.value}`); break;
      case 'DOMAIN-KEYWORD': arrsLines.push(`3, ${r.value}`); break;
      case 'IP-CIDR': arrsLines.push(`0, ${r.value}`); break;
      case 'IP-CIDR6': arrsLines.push(`1, ${r.value}`); break;
      case 'URL-REGEX':
        if (isRejectAction(r.action)) { const line = convertURLRegexReject(r, opts, report); if (line) amrsLines.push(line); }
        else report.skipped.push(`URL-REGEX 非 REJECT 类不可转换: ${r.raw}`);
        break;
      case 'GEOIP': case 'PROCESS-NAME': case 'DEST-PORT': case 'SRC-PORT': case 'SRC-IP': case 'SRC-IP-CIDR': case 'CELLULAR-RADIO': case 'SUBNET':
        report.skipped.push(`${r.type} 不可转换: ${r.raw}`); break;
      case 'DOMAIN-SET': case 'RULE-SET':
        report.warnings.push(`DOMAIN-SET/RULE-SET 需单独下载展开: ${r.raw}`); break;
      default: report.skipped.push(`未知规则类型 ${r.type}: ${r.raw}`);
    }
  }
  return [arrsLines, amrsLines];
}

function convertURLRegexReject(r, opts, report) {
  const pattern = convertURLPattern(r.value, opts.generalizeHost);
  switch (r.action) {
    case 'REJECT': case 'REJECT-200': return `0, 0, ${pattern}, 2`;
    case 'REJECT-DICT': return `0, 0, ${pattern}, 2, {}`;
    case 'REJECT-ARRAY': return `0, 0, ${pattern}, 2, []`;
    case 'REJECT-IMG': return `0, 0, ${pattern}, 3`;
    default: report.skipped.push(`URL-REGEX 未知 REJECT 动作 ${r.action}: ${r.raw}`); return '';
  }
}

function convertRewriteRules(m, opts, report) {
  const lines = [];
  for (const r of m.rewrites) { const line = convertRewriteRule(r, m, opts, report); if (line) lines.push(line); }
  return lines;
}

function convertRewriteRule(r, m, opts, report) {
  const pattern = convertURLPattern(r.pattern, opts.generalizeHost);
  switch (r.action) {
    case 'reject': case 'reject-200': return `0, 0, ${pattern}, 2`;
    case 'reject-dict': return `0, 0, ${pattern}, 2, {}`;
    case 'reject-array': return `0, 0, ${pattern}, 2, []`;
    case 'reject-img': return `0, 0, ${pattern}, 3`;
    case '302': {
      const url = r.args.url || '';
      if (hasCaptureGroup(url)) {
        report.degraded.push(`302 带捕获组转为脚本: ${r.raw}`);
        return `0, 100, ${pattern}, ${buildRedirectScript(pattern, url, 302)}`;
      }
      return `0, 0, ${pattern}, 1, ${url}`;
    }
    case '307': {
      const url = r.args.url || '';
      if (hasCaptureGroup(url)) {
        report.degraded.push(`307 带捕获组转为脚本(降级302): ${r.raw}`);
        return `0, 100, ${pattern}, ${buildRedirectScript(pattern, url, 307)}`;
      }
      report.degraded.push(`307 降级为 302: ${r.raw}`);
      return `0, 0, ${pattern}, 1, ${url}`;
    }
    case 'mock-response-body': return `0, 0, ${pattern}, 2, ${quoteField(r.args.data || '')}`;
    case 'response-body-json-del': return `1, 5, ${pattern}, delete, ${dotPathToJSONPath(r.args.path || '')}`;
    case 'response-body-json-add': return `1, 5, ${pattern}, add, ${dotPathToJSONPath(r.args.path || '')}, ${quoteField(r.args.value || '')}`;
    case 'response-body-json-replace': return `1, 5, ${pattern}, replace, ${dotPathToJSONPath(r.args.path || '')}, ${quoteField(r.args.value || '')}`;
    case 'request-header': case 'request-body': return `0, 100, ${pattern}, ${encodeInlineRewriteJS(r.rawJS, 0)}`;
    case 'response-body': return `1, 100, ${pattern}, ${encodeInlineRewriteJS(r.rawJS, 1)}`;
    case '_request-header': case '_request-body': return `0, 100, ${pattern}, ${encodeInlineRewriteJS(r.rawJS, 0)}`;
    case '_response-body': return `1, 100, ${pattern}, ${encodeInlineRewriteJS(r.rawJS, 1)}`;
    case 'header-del': {
      const headerName = r.args.header || '';
      if (!headerName) return '';
      return `0, 2, ${pattern}, ${quoteField(headerName)}`;
    }
    case 'response-body-replace-regex': {
      const search = r.args.search || '';
      const replacement = r.args.replacement || '';
      if (!search) return '';
      return `1, 4, ${pattern}, ${quoteField(search)}, ${quoteField(replacement)}`;
    }
    case 'echo-response': {
      const body = r.args.body || '';
      const ct = r.args['content-type'] || 'application/json; charset=utf-8';
      if (!body) { report.skipped.push(`echo-response 缺少 body: ${r.raw}`); return ''; }
      // 通过模块级 content-type 传递响应类型
      if (m && !m.contentType) m.contentType = ct;
      return `1, 0, ${pattern}, 2, ${quoteField(body)}`;
    }
    case 'jsonjq-response-body': {
      const jq = r.args.jq || '';
      if (!jq) { report.skipped.push(`jsonjq-response-body 缺少 jq: ${r.raw}`); return ''; }
      return `1, 5, ${pattern}, ${quoteField(jq)}`;
    }
    default: report.skipped.push(`未知重写动作 ${r.action}: ${r.raw}`); return '';
  }
}

function convertHeaderRules(rules, opts, report) {
  const lines = [];
  for (const r of rules) {
    const pattern = convertURLPattern(r.pattern, opts.generalizeHost);
    switch (r.op) {
      case 'add': lines.push(`${r.phase}, 1, ${pattern}, ${quoteField(r.name)}, ${quoteField(r.value)}`); break;
      case 'replace': lines.push(`${r.phase}, 3, ${pattern}, ${quoteField(r.name)}, ${quoteField(r.value)}`); break;
      case 'delete': lines.push(`${r.phase}, 2, ${pattern}, ${quoteField(r.name)}`); break;
      default: report.skipped.push(`未知 header 操作 ${r.op}: ${r.raw}`);
    }
  }
  return lines;
}

async function convertMapLocals(rules, opts, report, source) {
  const lines = [];
  const userAgent = getUserAgent(source);
  for (const r of rules) {
    const pattern = convertURLPattern(r.pattern, opts.generalizeHost);
    if (!r.dataURL) { report.skipped.push(`Map Local 无 data: ${r.raw}`); continue; }
    let body = r.dataURL;
    if (r.dataURL.startsWith('http')) {
      try { body = await fetchRemoteWithProxy(r.dataURL, userAgent); }
      catch (e) { report.scriptErr.push(`Map Local 下载 data 失败 ${r.dataURL}: ${e}`); continue; }
    }
    lines.push(`0, 0, ${pattern}, 2, ${quoteField(body)}`);
  }
  return lines;
}

async function convertScriptRules(m, opts, report, source) {
  const lines = [];
  const userAgent = getUserAgent(source);
  for (const s of m.scripts) {
    const pattern = convertURLPattern(s.pattern, opts.generalizeHost);
    if (!s.scriptPath) { report.skipped.push(`脚本无 script-path: ${s.raw}`); continue; }
    try {
      const resolved = resolveScriptPath(s.scriptPath, m.name);
      const b64 = await fetchAndEncodeScript(resolved, opts.fetchScripts, s.phase, opts.useStreamScript, userAgent);
      const op = opts.useStreamScript ? '101' : '100';
      lines.push(`${s.phase}, ${op}, ${pattern}, ${b64}`);
    } catch (e) { report.scriptErr.push(`脚本下载失败 ${s.scriptPath}: ${e}`); }
  }
  return lines;
}

function addEncodingPreprocess(lines) {
  const patterns = new Set();
  for (const line of lines) {
    const fields = splitAmrsFields(line);
    if (fields.length < 2) continue;
    if (fields[0] !== '1') continue;
    if (['4', '5', '100', '101'].includes(fields[1]) && fields.length >= 3) patterns.add(fields[2]);
  }
  if (patterns.size === 0) return lines;
  const pre = [];
  for (const p of patterns) { pre.push(`0, 2, ${p}, accept-encoding`); pre.push(`0, 1, ${p}, accept-encoding, identity`); }
  return [...pre, ...lines];
}

function splitAmrsFields(line) {
  const fields = [];
  let rest = line;
  for (let i = 0; i < 3 && rest; i++) {
    const idx = rest.indexOf(',');
    if (idx < 0) { fields.push(rest.trim()); rest = ''; break; }
    fields.push(rest.slice(0, idx).trim());
    rest = rest.slice(idx + 1);
  }
  if (rest) fields.push(rest.trim());
  return fields;
}

function generateArrs(name, lines, m, opts) {
  if (lines.length === 0) return '';
  const parts = [];
  if (opts.includeMetadata) parts.push(metadataComments(m, opts));
  parts.push(`name = ${name}`);
  parts.push('');
  parts.push(...lines);
  return parts.join('\n') + '\n';
}

function generateAmrs(name, hostnames, lines, m, opts) {
  if (lines.length === 0 && hostnames.length === 0) return '';
  const parts = [];
  if (opts.includeMetadata) parts.push(metadataComments(m, opts));
  parts.push(`name = ${name}`);
  if (hostnames.length > 0) parts.push(`hostname = ${hostnames.join(', ')}`);
  if (m.contentType) parts.push(`content-type = ${m.contentType}`);
  parts.push('');
  parts.push(...lines);
  return parts.join('\n') + '\n';
}

function inferContentType(lines) {
  for (const line of lines) {
    if (line.startsWith('0, 0, ')) {
      const rest = line.slice(6);
      const idx = rest.indexOf(', 2, ');
      if (idx >= 0) {
        const content = rest.slice(idx + 5).trim();
        if (content.startsWith('{') || content.startsWith('"{"') || content.includes('"code"')) return 'application/json; charset=utf-8';
      }
    }
  }
  return '';
}

function metadataComments(m, opts) {
  opts = opts || {};
  const parts = ['# 由 module2anywhere 从 ' + m.source + ' 模块转换'];
  if (opts.sourceURL) parts.push('# source: ' + opts.sourceURL);
  // 如果是从 quantumult.app add-resource 链接提取的，添加解码后的原始地址
  if (opts.addResourceURL) parts.push('# add-resource: ' + opts.addResourceURL);
  if (opts.serviceURL) parts.push('# this: ' + opts.serviceURL);
  if (m.desc) parts.push('# desc: ' + m.desc);
  if (m.author) parts.push('# author: ' + m.author);
  if (m.homepage) parts.push('# homepage: ' + m.homepage);
  if (m.date) parts.push('# date: ' + m.date);
  parts.push('');
  return parts.join('\n');
}

function deriveNameFromURL(rawURL) {
  try {
    const url = new URL(rawURL);
    const parts = url.pathname.split('/');
    let filename = parts[parts.length - 1];
    for (const ext of ['.plugin', '.sgmodule', '.lpx', '.conf', '.list']) {
      if (filename.endsWith(ext)) filename = filename.slice(0, -ext.length);
    }
    return filename || 'Unnamed';
  } catch { return 'Unnamed'; }
}

// isAddResourceURL 判断 URL 是否为 Quantumult X 的 add-resource 一键订阅协议。
// 形式：https://quantumult.app/x/open-app/add-resource?remote-resource=<encoded-json>
function isAddResourceURL(rawURL) {
  try {
    const u = new URL(rawURL);
    const host = (u.hostname || '').toLowerCase();
    if (!host.endsWith('quantumult.app')) return false;
    let p = u.pathname;
    if (p.endsWith('/')) p = p.slice(0, -1);
    return p === '/x/open-app/add-resource';
  } catch { return false; }
}

// extractAddResourceURLs 从 quantumult.app add-resource 链接展开远端订阅 URL 列表。
// remote-resource 既可能直接是 JSON，也可能被 URL 编码一次；返回的列表只保留以 http(s):// 开头的纯 URL。
function extractAddResourceURLs(rawURL) {
  const u = new URL(rawURL);
  let raw = u.searchParams.get('remote-resource') || '';
  if (!raw) throw new Error('缺少 remote-resource 参数');
  try { raw = decodeURIComponent(raw); } catch (e) { /* 保留原值 */ }
  let payload;
  try {
    payload = JSON.parse(raw);
  } catch (e) {
    throw new Error('remote-resource JSON 解析失败: ' + e.message);
  }
  const keys = ['rewrite_remote', 'server_remote', 'filter_remote', 'task_remote'];
  const all = [];
  for (const k of keys) {
    const arr = payload[k];
    if (Array.isArray(arr)) all.push(...arr);
  }
  const urls = [];
  for (const entry of all) {
    if (typeof entry !== 'string') continue;
    let s = entry.trim();
    if (!s) continue;
    const idx = s.indexOf(',');
    if (idx > 0) s = s.slice(0, idx).trim();
    if (s.startsWith('http://') || s.startsWith('https://')) urls.push(s);
  }
  return urls;
}

// ===================== 共享库对象 =====================
// 端点文件通过 `const lib = { ... }` 直接访问这些函数
// 不需要 import 任何模块
const lib = {
  detectSource, parse, deriveNameFromURL, defaultConvertOptions, convert,
  fetchRemoteWithProxy, getUserAgent, isAddResourceURL, extractAddResourceURLs,
};
