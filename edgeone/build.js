#!/usr/bin/env node
/**
 * 构建脚本：把 functions/lib.js 的 IIFE 注入到每个端点文件 (mitm.js, rule.js, convert.js)
 * EdgeOne Pages dev 模式的 esbuild 只能处理 1 层 import，所以采用 inlining 方式。
 *
 * 使用方法： node build.js
 */

import { readFileSync, writeFileSync, existsSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const FUNCTIONS_DIR = join(__dirname, 'functions');

// 读取 lib.js（这是一个 IIFE 表达式，赋值给 globalThis.__module2anywhere）
const libPath = join(FUNCTIONS_DIR, 'lib.js');
if (!existsSync(libPath)) {
  console.error('Error: lib.js not found at', libPath);
  process.exit(1);
}
const libSource = readFileSync(libPath, 'utf8');

// 端点文件模板：把 lib.js 的全部内容放在 onRequest 之前，
// 文件作用域内的 `const lib = { ... }` 会被 onRequest 闭包捕获。
function buildEndpoint(endpointCode) {
  return `/**
 * 此文件由 build.js 自动生成
 * - 自动注入了 lib.js 的全部共享函数（直接复制，无任何 import）
 * - 共享函数通过文件作用域内的 \`const lib = { ... }\` 暴露
 * - EdgeOne dev 模式的 esbuild 只能处理 1 层 import，因此采用 inlining
 * - 修改 lib.js 后必须重新运行 \`node build.js\`
 */

${libSource}

${endpointCode}
`;
}

// 注意：不要使用 Object.fromEntries、?.、??= 等 ES2019+ 语法，
// EdgeOne V8 运行时可能不支持

const endpoints = [
  {
    file: 'mitm.js',
    code: `function buildCorsHeaders() {
  return {
    'Access-Control-Allow-Origin': '*',
    'Access-Control-Allow-Methods': 'GET, OPTIONS',
    'Access-Control-Allow-Headers': '*',
  };
}

function corsPreflight() {
  return new Response(null, { status: 204, headers: buildCorsHeaders() });
}

export async function onRequest(context) {
  if (context.request.method === 'OPTIONS') return corsPreflight();

  const url = new URL(context.request.url);
  const query = {};
  url.searchParams.forEach((v, k) => { query[k] = v; });

  const rawURL = query.url;
  if (!rawURL) {
    return new Response('Error: url parameter is required', {
      status: 400,
      headers: buildCorsHeaders(),
    });
  }

  let decodedURL;
  try {
    decodedURL = decodeURIComponent(rawURL);
  } catch {
    return new Response('Error: Invalid URL encoding', {
      status: 400,
      headers: buildCorsHeaders(),
    });
  }

  const name = query.name || '';
  const fetchScripts = query.fetch === 'true';
  const generalize = query.generalize !== 'false';
  const sourceHint = query.source || '';
  const initialUA = lib.getUserAgent(sourceHint);

  // 解析 quantumult.app 一键订阅协议，否则取原始 URL
  let sourceURL = decodedURL;
  let inputURLs = [decodedURL];
  let addResourceURL = '';
  if (lib.isAddResourceURL && lib.isAddResourceURL(decodedURL)) {
    addResourceURL = decodedURL;
    try {
      inputURLs = lib.extractAddResourceURLs(decodedURL);
      if (inputURLs.length === 0) inputURLs = [decodedURL];
    } catch (e) {
      return new Response(\`Error: add-resource 解析失败: \${e.message || e}\`, {
        status: 400,
        headers: buildCorsHeaders(),
      });
    }
  }

  // 构造本服务地址（用于注释 # this: ...）
  const serviceURL = url.origin + url.pathname;

  const allAmrs = [];
  const allArrs = [];
  for (const inputURL of inputURLs) {
    let content;
    try {
      content = await lib.fetchRemoteWithProxy(inputURL, initialUA);
    } catch (e) {
      return new Response(\`Error: Failed to fetch remote file: \${e.message || e}\`, {
        status: 500,
        headers: buildCorsHeaders(),
      });
    }

    const source = lib.detectSource(content, inputURL.split('/').pop() || '');
    const m = lib.parse(content, source);

    if (name) m.name = name;
    else if (!m.name) m.name = lib.deriveNameFromURL(inputURL);

    const opts = {
      ...lib.defaultConvertOptions(),
      generalizeHost: generalize,
      fetchScripts,
      sourceURL: inputURL,
      serviceURL: serviceURL,
      addResourceURL: addResourceURL,
    };

    try {
      const result = await lib.convert(m, opts);
      if (result.amrs) allAmrs.push(result.amrs);
      if (result.arrs) allArrs.push(result.arrs);
    } catch (e) {
      return new Response(\`Error: convert failed: \${e.message || e}\`, {
        status: 500,
        headers: buildCorsHeaders(),
      });
    }
  }

  const body = allAmrs.join('\\n');
  const filename = (name || 'module2anywhere') + '.amrs';
  if (!body) {
    return new Response('Error: no MITM rules in module', {
      status: 404,
      headers: buildCorsHeaders(),
    });
  }
  return new Response(body, {
    status: 200,
    headers: {
      ...buildCorsHeaders(),
      'Content-Type': 'text/plain; charset=utf-8',
      'Content-Disposition': \`inline; filename=\${filename}\`,
    },
  });
}
`,
  },
  {
    file: 'rule.js',
    code: `function buildCorsHeaders() {
  return {
    'Access-Control-Allow-Origin': '*',
    'Access-Control-Allow-Methods': 'GET, OPTIONS',
    'Access-Control-Allow-Headers': '*',
  };
}

function corsPreflight() {
  return new Response(null, { status: 204, headers: buildCorsHeaders() });
}

export async function onRequest(context) {
  if (context.request.method === 'OPTIONS') return corsPreflight();

  const url = new URL(context.request.url);
  const query = {};
  url.searchParams.forEach((v, k) => { query[k] = v; });

  const rawURL = query.url;
  if (!rawURL) {
    return new Response('Error: url parameter is required', {
      status: 400,
      headers: buildCorsHeaders(),
    });
  }

  let decodedURL;
  try {
    decodedURL = decodeURIComponent(rawURL);
  } catch {
    return new Response('Error: Invalid URL encoding', {
      status: 400,
      headers: buildCorsHeaders(),
    });
  }

  const name = query.name || '';
  const fetchScripts = query.fetch === 'true';
  const generalize = query.generalize !== 'false';
  const sourceHint = query.source || '';
  const initialUA = lib.getUserAgent(sourceHint);

  // 解析 quantumult.app 一键订阅协议，否则取原始 URL
  let sourceURL = decodedURL;
  let inputURLs = [decodedURL];
  let addResourceURL = '';
  if (lib.isAddResourceURL && lib.isAddResourceURL(decodedURL)) {
    addResourceURL = decodedURL;
    try {
      inputURLs = lib.extractAddResourceURLs(decodedURL);
      if (inputURLs.length === 0) inputURLs = [decodedURL];
    } catch (e) {
      return new Response(\`Error: add-resource 解析失败: \${e.message || e}\`, {
        status: 400,
        headers: buildCorsHeaders(),
      });
    }
  }

  // 构造本服务地址（用于注释 # this: ...）
  const serviceURL = url.origin + url.pathname;

  const allAmrs = [];
  const allArrs = [];
  for (const inputURL of inputURLs) {
    let content;
    try {
      content = await lib.fetchRemoteWithProxy(inputURL, initialUA);
    } catch (e) {
      return new Response(\`Error: Failed to fetch remote file: \${e.message || e}\`, {
        status: 500,
        headers: buildCorsHeaders(),
      });
    }

    const source = lib.detectSource(content, inputURL.split('/').pop() || '');
    const m = lib.parse(content, source);

    if (name) m.name = name;
    else if (!m.name) m.name = lib.deriveNameFromURL(inputURL);

    const opts = {
      ...lib.defaultConvertOptions(),
      generalizeHost: generalize,
      fetchScripts,
      sourceURL: inputURL,
      serviceURL: serviceURL,
      addResourceURL: addResourceURL,
    };

    try {
      const result = await lib.convert(m, opts);
      if (result.amrs) allAmrs.push(result.amrs);
      if (result.arrs) allArrs.push(result.arrs);
    } catch (e) {
      return new Response(\`Error: convert failed: \${e.message || e}\`, {
        status: 500,
        headers: buildCorsHeaders(),
      });
    }
  }

  const body = allArrs.join('\\n');
  const filename = (name || 'module2anywhere') + '.arrs';
  if (!body) {
    return new Response('Error: no routing rules in module', {
      status: 404,
      headers: buildCorsHeaders(),
    });
  }
  return new Response(body, {
    status: 200,
    headers: {
      ...buildCorsHeaders(),
      'Content-Type': 'text/plain; charset=utf-8',
      'Content-Disposition': \`inline; filename=\${filename}\`,
    },
  });
}
`,
  },
  {
    file: 'convert.js',
    code: `function buildCorsHeaders() {
  return {
    'Access-Control-Allow-Origin': '*',
    'Access-Control-Allow-Methods': 'GET, OPTIONS',
    'Access-Control-Allow-Headers': '*',
  };
}

function corsPreflight() {
  return new Response(null, { status: 204, headers: buildCorsHeaders() });
}

export async function onRequest(context) {
  if (context.request.method === 'OPTIONS') return corsPreflight();

  const url = new URL(context.request.url);
  const query = {};
  url.searchParams.forEach((v, k) => { query[k] = v; });

  const to = (query.to || 'mitm').toLowerCase().trim();
  let format;
  switch (to) {
    case 'mitm':
    case 'amrs':
      format = 'amrs';
      break;
    case 'rule':
    case 'arrs':
      format = 'arrs';
      break;
    default:
      return new Response("Error: Invalid 'to' parameter. Use: mitm/rule", {
        status: 400,
        headers: buildCorsHeaders(),
      });
  }

  const rawURL = query.url;
  if (!rawURL) {
    return new Response('Error: url parameter is required', {
      status: 400,
      headers: buildCorsHeaders(),
    });
  }

  let decodedURL;
  try {
    decodedURL = decodeURIComponent(rawURL);
  } catch {
    return new Response('Error: Invalid URL encoding', {
      status: 400,
      headers: buildCorsHeaders(),
    });
  }

  const name = query.name || '';
  const fetchScripts = query.fetch === 'true';
  const generalize = query.generalize !== 'false';
  const sourceHint = query.source || '';
  const initialUA = lib.getUserAgent(sourceHint);

  // 解析 quantumult.app 一键订阅协议，否则取原始 URL
  let sourceURL = decodedURL;
  let inputURLs = [decodedURL];
  let addResourceURL = '';
  if (lib.isAddResourceURL && lib.isAddResourceURL(decodedURL)) {
    addResourceURL = decodedURL;
    try {
      inputURLs = lib.extractAddResourceURLs(decodedURL);
      if (inputURLs.length === 0) inputURLs = [decodedURL];
    } catch (e) {
      return new Response(\`Error: add-resource 解析失败: \${e.message || e}\`, {
        status: 400,
        headers: buildCorsHeaders(),
      });
    }
  }

  // 构造本服务地址（用于注释 # this: ...）
  const serviceURL = url.origin + url.pathname;

  const allAmrs = [];
  const allArrs = [];
  for (const inputURL of inputURLs) {
    let content;
    try {
      content = await lib.fetchRemoteWithProxy(inputURL, initialUA);
    } catch (e) {
      return new Response(\`Error: Failed to fetch remote file: \${e.message || e}\`, {
        status: 500,
        headers: buildCorsHeaders(),
      });
    }

    const source = lib.detectSource(content, inputURL.split('/').pop() || '');
    const m = lib.parse(content, source);

    if (name) m.name = name;
    else if (!m.name) m.name = lib.deriveNameFromURL(inputURL);

    const opts = {
      ...lib.defaultConvertOptions(),
      generalizeHost: generalize,
      fetchScripts,
      sourceURL: inputURL,
      serviceURL: serviceURL,
      addResourceURL: addResourceURL,
    };

    try {
      const result = await lib.convert(m, opts);
      if (result.amrs) allAmrs.push(result.amrs);
      if (result.arrs) allArrs.push(result.arrs);
    } catch (e) {
      return new Response(\`Error: convert failed: \${e.message || e}\`, {
        status: 500,
        headers: buildCorsHeaders(),
      });
    }
  }

  const body = format === 'amrs' ? allAmrs.join('\\n') : allArrs.join('\\n');
  const filename = (name || 'module2anywhere') + (format === 'amrs' ? '.amrs' : '.arrs');
  if (!body) {
    return new Response(\`Error: no \${format} rules in module\`, {
      status: 404,
      headers: buildCorsHeaders(),
    });
  }
  return new Response(body, {
    status: 200,
    headers: {
      ...buildCorsHeaders(),
      'Content-Type': 'text/plain; charset=utf-8',
      'Content-Disposition': \`inline; filename=\${filename}\`,
    },
  });
}
`,
  },
  {
    file: 'deeplink.js',
    code: `function buildCorsHeaders() {
  return {
    'Access-Control-Allow-Origin': '*',
    'Access-Control-Allow-Methods': 'GET, OPTIONS',
    'Access-Control-Allow-Headers': '*',
  };
}

function corsPreflight() {
  return new Response(null, { status: 204, headers: buildCorsHeaders() });
}

export async function onRequest(context) {
  if (context.request.method === 'OPTIONS') return corsPreflight();

  var url = new URL(context.request.url);
  var query = {};
  url.searchParams.forEach(function (v, k) { query[k] = v; });

  var rawURL = query.url;
  if (!rawURL) {
    return new Response('Error: url parameter is required', {
      status: 400,
      headers: buildCorsHeaders(),
    });
  }

  // 解码 URL
  var decodedURL;
  try {
    decodedURL = decodeURIComponent(rawURL);
  } catch (e) {
    return new Response('Error: Invalid URL encoding', {
      status: 400,
      headers: buildCorsHeaders(),
    });
  }

  var name = query.name || '';
  var fetchScripts = query.fetch === 'true';
  var generalize = query.generalize !== 'false';
  var sourceHint = query.source || '';
  var format = (query.format || '').toLowerCase().trim();
  var initialUA = lib.getUserAgent(sourceHint);

  // 解析 quantumult.app 一键订阅协议
  var sourceURL = decodedURL;
  var inputURLs = [decodedURL];
  var addResourceURL = '';
  if (lib.isAddResourceURL && lib.isAddResourceURL(decodedURL)) {
    addResourceURL = decodedURL;
    try {
      inputURLs = lib.extractAddResourceURLs(decodedURL);
      if (inputURLs.length === 0) inputURLs = [decodedURL];
    } catch (e) {
      return new Response('Error: add-resource 解析失败: ' + (e.message || e), {
        status: 400,
        headers: buildCorsHeaders(),
      });
    }
  }

  var serviceURL = url.origin + url.pathname;
  var hasAmrs = false;
  var hasArrs = false;

  for (var idx = 0; idx < inputURLs.length; idx++) {
    var inputURL = inputURLs[idx];
    var content;
    try {
      content = await lib.fetchRemoteWithProxy(inputURL, initialUA);
    } catch (e) {
      return new Response('Error: Failed to fetch remote file: ' + (e.message || e), {
        status: 500,
        headers: buildCorsHeaders(),
      });
    }

    var source = lib.detectSource(content, inputURL.split('/').pop() || '');
    var m = lib.parse(content, source);
    if (name) m.name = name;
    else if (!m.name) m.name = lib.deriveNameFromURL(inputURL);

    var opts = {
      ...lib.defaultConvertOptions(),
      generalizeHost: generalize,
      fetchScripts: fetchScripts,
      sourceURL: inputURL,
      serviceURL: serviceURL,
      addResourceURL: addResourceURL,
    };

    try {
      var result = await lib.convert(m, opts);
      if (result.amrs) hasAmrs = true;
      if (result.arrs) hasArrs = true;
    } catch (e) {
      return new Response('Error: convert failed: ' + (e.message || e), {
        status: 500,
        headers: buildCorsHeaders(),
      });
    }
  }

  // 构造本服务的子链接 URL
  var origin = url.origin;
  var linkParams = 'url=' + encodeURIComponent(decodedURL) + '&fetch=' + fetchScripts + '&generalize=' + generalize;
  if (sourceHint) linkParams += '&source=' + encodeURIComponent(sourceHint);
  if (name) linkParams += '&name=' + encodeURIComponent(name);

  var links = [];
  if (hasAmrs) links.push(origin + '/mitm?' + linkParams);
  if (hasArrs) links.push(origin + '/rule?' + linkParams);

  if (links.length === 0) {
    return new Response('Error: no rules to import', {
      status: 404,
      headers: buildCorsHeaders(),
    });
  }

  // 构造 anywhere://add-rule-set deeplink
  var deeplink = 'anywhere://add-rule-set?';
  var linkParts = [];
  for (var i = 0; i < links.length; i++) {
    linkParts.push('link=' + encodeURIComponent(links[i]));
  }
  deeplink += linkParts.join('&');

  // format=text 返回纯文本；否则根据 Accept 头决定
  if (format === 'text') {
    return new Response(deeplink, {
      status: 200,
      headers: { ...buildCorsHeaders(), 'Content-Type': 'text/plain; charset=utf-8' },
    });
  }

  // 浏览器访问返回 HTML 页面
  var accept = (context.request.headers.get('Accept') || '');
  if (accept.indexOf('text/html') !== -1) {
    var html = '<!DOCTYPE html><html><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1.0">'
      + '<title>导入 Anywhere</title>'
      + '<style>body{font-family:-apple-system,sans-serif;background:#0f172a;color:#e2e8f0;display:flex;flex-direction:column;align-items:center;justify-content:center;min-height:100vh;padding:2rem}'
      + '.card{background:#1e293b;border:1px solid #334155;border-radius:12px;padding:2rem;max-width:480px;width:100%;text-align:center}'
      + 'h1{font-size:1.3rem;margin-bottom:1rem}'
      + 'a.btn{display:inline-block;padding:0.75rem 2rem;background:#38bdf8;color:#0f172a;border-radius:8px;text-decoration:none;font-weight:600;font-size:1rem;margin:0.5rem}'
      + 'a.btn:hover{background:#7dd3fc}'
      + '.links{margin-top:1rem;font-size:0.8rem;color:#94a3b8;word-break:break-all}'
      + '.links a{color:#38bdf8}</style></head>'
      + '<body><div class="card">'
      + '<h1>导入规则到 Anywhere</h1>'
      + '<a class="btn" href="' + deeplink + '">打开 Anywhere 导入</a>'
      + '<div class="links">';
    for (var j = 0; j < links.length; j++) {
      var label = links[j].indexOf('/mitm') !== -1 ? 'MITM 规则' : '路由规则';
      html += '<p>' + label + '：<a href="' + links[j] + '" target="_blank">' + links[j] + '</a></p>';
    }
    html += '</div></div></body></html>';
    return new Response(html, {
      status: 200,
      headers: { ...buildCorsHeaders(), 'Content-Type': 'text/html; charset=utf-8' },
    });
  }

  // 非 HTML 请求返回 302 重定向到 deeplink
  return Response.redirect(deeplink, 302);
}
`,
  },
];

for (const ep of endpoints) {
  const outPath = join(FUNCTIONS_DIR, ep.file);
  writeFileSync(outPath, buildEndpoint(ep.code));
  console.log('Built', ep.file);
}

console.log('All endpoint files regenerated successfully.');
