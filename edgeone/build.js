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
  if (lib.isAddResourceURL && lib.isAddResourceURL(decodedURL)) {
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
      sourceURL: sourceURL,
      serviceURL: serviceURL,
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
  if (lib.isAddResourceURL && lib.isAddResourceURL(decodedURL)) {
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
      sourceURL: sourceURL,
      serviceURL: serviceURL,
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
  if (lib.isAddResourceURL && lib.isAddResourceURL(decodedURL)) {
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
      sourceURL: sourceURL,
      serviceURL: serviceURL,
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
];

for (const ep of endpoints) {
  const outPath = join(FUNCTIONS_DIR, ep.file);
  writeFileSync(outPath, buildEndpoint(ep.code));
  console.log('Built', ep.file);
}

console.log('All endpoint files regenerated successfully.');
