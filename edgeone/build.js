#!/usr/bin/env node
/**
 * 构建脚本：使用 esbuild 预编译 EdgeOne 端点文件。
 *
 * 为什么需要预编译：
 * EdgeOne CLI 的 esbuild 会将所有端点打包成一个 bundle，但不会做 module deduplication，
 * 导致 lib.js 的代码在每个端点中重复一份（约 55KB × 8 端点 ≈ 440KB）。
 * 预编译后每个端点只包含实际使用的 lib.js 函数（tree-shaking），总 bundle 大小大幅减小。
 *
 * 使用方法： node build.js
 */

import { readFileSync, writeFileSync, existsSync, mkdirSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const FUNCTIONS_DIR = join(__dirname, 'functions');
const SRC_DIR = join(__dirname, 'functions-src');

// 确认 lib.js 存在（权威来源是 functions/lib.js）
const libPath = join(FUNCTIONS_DIR, 'lib.js');
if (!existsSync(libPath)) {
  console.error('Error: lib.js not found at', libPath);
  process.exit(1);
}

// 确保 functions 和 functions-src 目录存在
if (!existsSync(FUNCTIONS_DIR)) {
  mkdirSync(FUNCTIONS_DIR, { recursive: true });
}
if (!existsSync(SRC_DIR)) {
  mkdirSync(SRC_DIR, { recursive: true });
}

// 端点文件模板：使用 import 引用 lib.js
function buildEndpoint(endpointCode) {
  return `/**
 * 此文件由 build.js 自动生成
 * - 使用 import 引用 lib.js 共享函数
 * - 修改 lib.js 后重新运行 node build.js 即可生效
 */

import { lib } from './lib.js';

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
  const fetchScripts = query.fetch !== 'false';
  const generalize = query.generalize === 'true';
  const sourceHint = query.source || '';
  const initialUA = lib.getUserAgent(sourceHint);

  // 检查缓存
  var ck = lib.cacheKey(decodedURL, name, fetchScripts, generalize);
  var cached = lib.cacheGet(ck + ':amrs');
  if (cached.hit) {
    return new Response(cached.value, {
      status: 200,
      headers: { ...buildCorsHeaders(), 'Content-Type': 'text/plain; charset=utf-8', 'X-Cache': 'HIT' },
    });
  }

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
  lib.cachePut(ck + ':amrs', body);
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
  const fetchScripts = query.fetch !== 'false';
  const generalize = query.generalize === 'true';
  const sourceHint = query.source || '';
  const initialUA = lib.getUserAgent(sourceHint);

  // 检查缓存
  var ck = lib.cacheKey(decodedURL, name, fetchScripts, generalize);
  var cached = lib.cacheGet(ck + ':arrs');
  if (cached.hit) {
    return new Response(cached.value, {
      status: 200,
      headers: { ...buildCorsHeaders(), 'Content-Type': 'text/plain; charset=utf-8', 'X-Cache': 'HIT' },
    });
  }

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
  lib.cachePut(ck + ':arrs', body);
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
    file: 'mitm.amrs.js',
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
  const fetchScripts = query.fetch !== 'false';
  const generalize = query.generalize === 'true';
  const sourceHint = query.source || '';
  const initialUA = lib.getUserAgent(sourceHint);

  // 检查缓存
  var ck = lib.cacheKey(decodedURL, name, fetchScripts, generalize);
  var cached = lib.cacheGet(ck + ':amrs');
  if (cached.hit) {
    return new Response(cached.value, {
      status: 200,
      headers: { ...buildCorsHeaders(), 'Content-Type': 'text/plain; charset=utf-8', 'X-Cache': 'HIT' },
    });
  }

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
  lib.cachePut(ck + ':amrs', body);
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
    file: 'rule.arrs.js',
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
  const fetchScripts = query.fetch !== 'false';
  const generalize = query.generalize === 'true';
  const sourceHint = query.source || '';
  const initialUA = lib.getUserAgent(sourceHint);

  // 检查缓存
  var ck = lib.cacheKey(decodedURL, name, fetchScripts, generalize);
  var cached = lib.cacheGet(ck + ':rule');
  if (cached.hit) {
    return new Response(cached.value, {
      status: 200,
      headers: { ...buildCorsHeaders(), 'Content-Type': 'text/plain; charset=utf-8', 'X-Cache': 'HIT' },
    });
  }

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

  const serviceURL = url.origin + url.pathname;

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
      // 从 arrsGroups 中查找 routing=0 的分组（其他/PROXY 类规则）
      var arrsGroup = null;
      if (result.arrsGroups) {
        for (var gi = 0; gi < result.arrsGroups.length; gi++) {
          if (result.arrsGroups[gi].routing === 0) { arrsGroup = result.arrsGroups[gi]; break; }
        }
      }
      if (arrsGroup && arrsGroup.content) {
        allArrs.push(arrsGroup.content);
      } else if (result.arrs) {
        allArrs.push(result.arrs);
      }
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
  lib.cachePut(ck + ':rule', body);
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
    file: 'direct.arrs.js',
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
  const fetchScripts = query.fetch !== 'false';
  const generalize = query.generalize === 'true';
  const sourceHint = query.source || '';
  const initialUA = lib.getUserAgent(sourceHint);

  // 检查缓存
  var ck = lib.cacheKey(decodedURL, name, fetchScripts, generalize);
  var cached = lib.cacheGet(ck + ':direct');
  if (cached.hit) {
    return new Response(cached.value, {
      status: 200,
      headers: { ...buildCorsHeaders(), 'Content-Type': 'text/plain; charset=utf-8', 'X-Cache': 'HIT' },
    });
  }

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

  const serviceURL = url.origin + url.pathname;

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
      // 转换后获取 DIRECT 分组
      var arrsGroup = null;
      if (result.arrsGroups) {
        for (var gi = 0; gi < result.arrsGroups.length; gi++) {
          if (result.arrsGroups[gi].routing === 1) { arrsGroup = result.arrsGroups[gi]; break; }
        }
      }
      if (arrsGroup && arrsGroup.content) {
        allArrs.push(arrsGroup.content);
      }
    } catch (e) {
      return new Response(\`Error: convert failed: \${e.message || e}\`, {
        status: 500,
        headers: buildCorsHeaders(),
      });
    }
  }

  const body = allArrs.join('\\n');
  if (!body) {
    return new Response('Error: no DIRECT routing rules in module', {
      status: 404,
      headers: buildCorsHeaders(),
    });
  }
  var filename = (name || 'module2anywhere') + '-Direct.arrs';
  lib.cachePut(ck + ':direct', body);
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
    file: 'reject.arrs.js',
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
  const fetchScripts = query.fetch !== 'false';
  const generalize = query.generalize === 'true';
  const sourceHint = query.source || '';
  const initialUA = lib.getUserAgent(sourceHint);

  // 检查缓存
  var ck = lib.cacheKey(decodedURL, name, fetchScripts, generalize);
  var cached = lib.cacheGet(ck + ':reject');
  if (cached.hit) {
    return new Response(cached.value, {
      status: 200,
      headers: { ...buildCorsHeaders(), 'Content-Type': 'text/plain; charset=utf-8', 'X-Cache': 'HIT' },
    });
  }

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

  const serviceURL = url.origin + url.pathname;

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
      // 转换后获取 REJECT 分组
      var arrsGroup = null;
      if (result.arrsGroups) {
        for (var gi = 0; gi < result.arrsGroups.length; gi++) {
          if (result.arrsGroups[gi].routing === 2) { arrsGroup = result.arrsGroups[gi]; break; }
        }
      }
      if (arrsGroup && arrsGroup.content) {
        allArrs.push(arrsGroup.content);
      }
    } catch (e) {
      return new Response(\`Error: convert failed: \${e.message || e}\`, {
        status: 500,
        headers: buildCorsHeaders(),
      });
    }
  }

  const body = allArrs.join('\\n');
  if (!body) {
    return new Response('Error: no REJECT routing rules in module', {
      status: 404,
      headers: buildCorsHeaders(),
    });
  }
  var filename = (name || 'module2anywhere') + '-Reject.arrs';
  lib.cachePut(ck + ':reject', body);
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
  const fetchScripts = query.fetch !== 'false';
  const generalize = query.generalize === 'true';
  const sourceHint = query.source || '';
  const initialUA = lib.getUserAgent(sourceHint);

  // 检查缓存
  var ck = lib.cacheKey(decodedURL, name, fetchScripts, generalize);
  var cached = lib.cacheGet(ck + ':' + format);
  if (cached.hit) {
    return new Response(cached.value, {
      status: 200,
      headers: { ...buildCorsHeaders(), 'Content-Type': 'text/plain; charset=utf-8', 'X-Cache': 'HIT' },
    });
  }

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
  lib.cachePut(ck + ':' + format, body);
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
  var fetchScripts = query.fetch !== 'false';
  var generalize = query.generalize === 'true';
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
  var arrsGroupEndpoints = [];

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
      if (result.arrsGroups) {
        for (var gi = 0; gi < result.arrsGroups.length; gi++) {
          if (result.arrsGroups[gi].content) {
            if (arrsGroupEndpoints.indexOf(result.arrsGroups[gi].endpoint) === -1) {
              arrsGroupEndpoints.push(result.arrsGroups[gi].endpoint);
            }
          }
        }
      }
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
  if (hasAmrs) links.push(origin + '/mitm.amrs?' + linkParams);
  // 按 routing 分组生成 arrs 子链接
  if (arrsGroupEndpoints && arrsGroupEndpoints.length > 0) {
    for (var gi = 0; gi < arrsGroupEndpoints.length; gi++) {
      links.push(origin + arrsGroupEndpoints[gi] + '?' + linkParams);
    }
  } else if (hasArrs) {
    links.push(origin + '/rule.arrs?' + linkParams);
  }

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
      var label;
      if (links[j].indexOf('/mitm') !== -1) label = 'MITM 规则';
      else if (links[j].indexOf('/direct') !== -1) label = '直连规则';
      else if (links[j].indexOf('/reject') !== -1) label = '拒绝规则';
      else label = '路由规则';
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

// ===================== 预编译步骤 =====================
// 使用 esbuild 将每个端点文件预编译，实现 tree-shaking 和 minify。
// 这样 EdgeOne CLI 拿到的已经是编译后的文件，不需要再处理 import。

async function precompileWithEsbuild() {
  // 尝试加载 esbuild
  let esbuild;
  const esbuildPaths = [
    // EdgeOne CLI 内置的 esbuild
    join(__dirname, 'node_modules', 'edgeone', 'node_modules', 'esbuild', 'lib', 'main.js'),
    join(__dirname, 'node_modules', 'esbuild', 'lib', 'main.js'),
  ];

  for (const p of esbuildPaths) {
    try {
      esbuild = await import(`file://${p}`);
      console.log('使用 esbuild:', p);
      break;
    } catch { /* 继续尝试下一个路径 */ }
  }

  if (!esbuild) {
    console.log('未找到 esbuild，跳过预编译（将使用原始 import 方式）');
    return false;
  }

  // 1. 先将源文件写入 functions-src 目录
  if (!existsSync(SRC_DIR)) {
    mkdirSync(SRC_DIR, { recursive: true });
  }

  // 复制 lib.js 到 functions-src
  const libContent = readFileSync(join(FUNCTIONS_DIR, 'lib.js'), 'utf-8');
  writeFileSync(join(SRC_DIR, 'lib.js'), libContent);

  // 生成端点源文件到 functions-src
  for (const ep of endpoints) {
    const srcPath = join(SRC_DIR, ep.file);
    writeFileSync(srcPath, buildEndpoint(ep.code));
  }

  // 2. 使用 esbuild 编译每个端点
  for (const ep of endpoints) {
    const srcPath = join(SRC_DIR, ep.file);
    const outPath = join(FUNCTIONS_DIR, ep.file);

    try {
      const result = await esbuild.build({
        entryPoints: [srcPath],
        bundle: true,
        outfile: outPath,
        format: 'esm',
        platform: 'browser',
        target: ['es2020'],
        minify: true,
        treeShaking: true,
        external: [], // 不排除任何模块，全部内联
      });

      if (result.errors && result.errors.length > 0) {
        console.error(`编译 ${ep.file} 出错:`, result.errors);
        // 回退到原始方式
        writeFileSync(outPath, buildEndpoint(ep.code));
      } else {
        const size = readFileSync(outPath).length;
        console.log(`编译 ${ep.file}: ${size} 字节`);
      }
    } catch (e) {
      console.error(`编译 ${ep.file} 失败:`, e.message);
      // 回退到原始方式
      writeFileSync(outPath, buildEndpoint(ep.code));
    }
  }

  return true;
}

// 运行构建
const precompiled = await precompileWithEsbuild();

if (!precompiled) {
  // 没有预编译，使用原始 import 方式生成端点文件
  for (const ep of endpoints) {
    const outPath = join(FUNCTIONS_DIR, ep.file);
    writeFileSync(outPath, buildEndpoint(ep.code));
    console.log('Built', ep.file);
  }
}

// 复制不参与预编译的静态文件
const staticFiles = ['health.js', 'index.js'];
for (const f of staticFiles) {
  const srcPath = join(SRC_DIR, f);
  const outPath = join(FUNCTIONS_DIR, f);
  // 如果 functions-src 中有该文件，复制过去；否则跳过
  if (existsSync(srcPath)) {
    writeFileSync(outPath, readFileSync(srcPath));
    console.log('Copied', f);
  } else if (!existsSync(outPath)) {
    // health.js 和 index.js 可能已经在 functions 目录中
    console.log('Skipped', f, '(not found in functions-src)');
  }
}

console.log('All endpoint files regenerated successfully.');
