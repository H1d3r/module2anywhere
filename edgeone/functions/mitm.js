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
      return new Response(`Error: add-resource 解析失败: ${e.message || e}`, {
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
      sourceURL: inputURL,
      serviceURL: serviceURL,
      addResourceURL: addResourceURL,
    };

    try {
      const result = await lib.convert(m, opts);
      if (result.amrs) allAmrs.push(result.amrs);
      if (result.arrs) allArrs.push(result.arrs);
    } catch (e) {
      return new Response(`Error: convert failed: ${e.message || e}`, {
        status: 500,
        headers: buildCorsHeaders(),
      });
    }
  }

  const body = allAmrs.join('\n');
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
      'Content-Disposition': `inline; filename=${filename}`,
    },
  });
}

