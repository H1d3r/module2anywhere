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
  var wrapScripts = lib.defaultTrueInput(query.wrap);
  var sourceHint = query.source || '';
  var format = (query.format || '').toLowerCase().trim();
  var maxInputBytes = lib.positiveIntInput(query.maxInputBytes, 512 * 1024);
  var maxScriptBytes = lib.positiveIntInput(query.maxScriptBytes, 1024 * 1024);
  var maxScriptFetches = lib.positiveIntInput(query.maxScriptFetches, 45);
  var preserveParameters = lib.truthyInput(query.preserveParameters || query.preserveArguments);
  var scriptMode = lib.normalizeScriptMode(query.scriptMode);
  var argumentsMap = lib.queryArguments(query);
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
      content = await lib.fetchRemoteWithProxy(inputURL, initialUA, maxInputBytes);
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
  if (preserveParameters) linkParams += '&preserveParameters=true';
  if (scriptMode === 'loader') linkParams += '&scriptMode=loader';
  if (maxInputBytes !== 512 * 1024) linkParams += '&maxInputBytes=' + encodeURIComponent(maxInputBytes);
  if (maxScriptBytes !== 1024 * 1024) linkParams += '&maxScriptBytes=' + encodeURIComponent(maxScriptBytes);
  if (maxScriptFetches !== 45) linkParams += '&maxScriptFetches=' + encodeURIComponent(maxScriptFetches);
  for (var ak in argumentsMap) {
    if (Object.prototype.hasOwnProperty.call(argumentsMap, ak)) {
      linkParams += '&argument.' + encodeURIComponent(ak) + '=' + encodeURIComponent(argumentsMap[ak]);
    }
  }
  if (!wrapScripts) linkParams += '&wrap=false';
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

