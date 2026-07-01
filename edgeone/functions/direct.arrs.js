/**
 * 此文件由 build.js 自动生成
 * - 使用 import 引用 lib.js 共享函数
 * - 修改 lib.js 后重新运行 edgeone makers dev 即可生效
 */

import { lib } from './lib.js';

function buildCorsHeaders() {
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
  const wrapScripts = query.wrap === 'true';
  const maxInputBytes = lib.positiveIntInput(query.maxInputBytes, 512 * 1024);
  const maxScriptBytes = lib.positiveIntInput(query.maxScriptBytes, 1024 * 1024);
  const maxScriptFetches = lib.positiveIntInput(query.maxScriptFetches, 45);
  const preserveParameters = lib.truthyInput(query.preserveParameters || query.preserveArguments);
  const scriptMode = lib.normalizeScriptMode(query.scriptMode);
  const argumentsMap = lib.queryArguments(query);
  const initialUA = lib.getUserAgent(sourceHint);

  // 检查缓存
  var ck = lib.cacheKey(decodedURL, name, fetchScripts, generalize, preserveParameters, argumentsMap, scriptMode, wrapScripts, maxInputBytes, maxScriptBytes, maxScriptFetches);
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
      return new Response(`Error: add-resource 解析失败: ${e.message || e}`, {
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
      content = await lib.fetchRemoteWithProxy(inputURL, initialUA, maxInputBytes);
    } catch (e) {
      return new Response(`Error: Failed to fetch remote file: ${e.message || e}`, {
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
      wrapScripts: wrapScripts,
      sourceURL: inputURL,
      serviceURL: serviceURL,
      addResourceURL: addResourceURL,
      arguments: argumentsMap,
      preserveParameters: preserveParameters,
      scriptMode: scriptMode,
      scriptBaseURL: url.origin + '/script.js',
      maxScriptBytes: maxScriptBytes,
      maxScriptFetches: maxScriptFetches,
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
      return new Response(`Error: convert failed: ${e.message || e}`, {
        status: 500,
        headers: buildCorsHeaders(),
      });
    }
  }

  const body = allArrs.join('\n');
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
      'Content-Disposition': `inline; filename=${filename}`,
    },
  });
}

