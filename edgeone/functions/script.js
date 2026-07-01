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

  const rawScript = query.script || '';
  if (!rawScript) {
    return new Response('Error: script parameter is required', {
      status: 400,
      headers: buildCorsHeaders(),
    });
  }

  const phase = query.phase === '1' ? 1 : 0;
  const wrap = query.wrap === 'true';
  const argument = query.argument || '';
  const maxScriptBytes = lib.positiveIntInput(query.maxScriptBytes, 1024 * 1024);
  const baseURL = query.base || '';
  const resolved = lib.resolveScriptPath(rawScript, baseURL);
  const userAgent = lib.getUserAgent(baseURL);

  try {
    const source = await lib.fetchAndRewriteScript(resolved, true, phase, false, userAgent, wrap, argument, maxScriptBytes);
    return new Response(source, {
      status: 200,
      headers: {
        ...buildCorsHeaders(),
        'Content-Type': 'application/javascript; charset=utf-8',
        'Cache-Control': 'public, max-age=3600',
      },
    });
  } catch (e) {
    return new Response('Error: ' + (e.message || e), {
      status: 400,
      headers: buildCorsHeaders(),
    });
  }
}

