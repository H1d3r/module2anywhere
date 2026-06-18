/**
 * GET / — 前端展示页面
 *
 * 直接在边缘函数中返回 HTML，避免依赖 static/ 目录的部署。
 * EdgeOne Pages dev 模式不会自动服务 static 目录，必须用函数处理。
 */

const HTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>module2anywhere - 模块转换服务</title>
<style>
  :root { --bg: #0f172a; --card: #1e293b; --border: #334155; --text: #e2e8f0; --muted: #94a3b8; --accent: #38bdf8; --accent2: #818cf8; }
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: var(--bg); color: var(--text); min-height: 100vh; display: flex; flex-direction: column; align-items: center; padding: 2rem 1rem; }
  .container { max-width: 720px; width: 100%; }
  h1 { font-size: 1.75rem; font-weight: 700; margin-bottom: 0.5rem; background: linear-gradient(135deg, var(--accent), var(--accent2)); -webkit-background-clip: text; -webkit-text-fill-color: transparent; }
  .subtitle { color: var(--muted); font-size: 0.95rem; margin-bottom: 2rem; }
  .card { background: var(--card); border: 1px solid var(--border); border-radius: 12px; padding: 1.5rem; margin-bottom: 1.5rem; }
  label { display: block; font-size: 0.875rem; font-weight: 500; margin-bottom: 0.5rem; color: var(--muted); }
  input[type="text"] { width: 100%; padding: 0.75rem 1rem; background: var(--bg); border: 1px solid var(--border); border-radius: 8px; color: var(--text); font-size: 0.95rem; outline: none; transition: border-color 0.2s; }
  input[type="text"]:focus { border-color: var(--accent); }
  .btn-group { display: flex; gap: 0.75rem; margin-top: 1rem; flex-wrap: wrap; }
  button { padding: 0.65rem 1.5rem; border: none; border-radius: 8px; font-size: 0.9rem; font-weight: 600; cursor: pointer; transition: all 0.2s; }
  .btn-primary { background: var(--accent); color: #0f172a; }
  .btn-primary:hover { background: #7dd3fc; }
  .btn-secondary { background: var(--accent2); color: #fff; }
  .btn-secondary:hover { background: #a5b4fc; }
  .options { display: flex; gap: 1rem; margin-top: 1rem; flex-wrap: wrap; }
  .options label { display: flex; align-items: center; gap: 0.4rem; font-size: 0.85rem; color: var(--muted); cursor: pointer; }
  .options input[type="checkbox"] { accent-color: var(--accent); }
  .result { margin-top: 1.5rem; }
  .result-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 0.5rem; }
  .result-header h3 { font-size: 0.9rem; color: var(--muted); }
  .copy-btn { font-size: 0.8rem; padding: 0.3rem 0.8rem; background: var(--border); color: var(--text); border: none; border-radius: 6px; cursor: pointer; }
  .copy-btn:hover { background: #475569; }
  pre { background: var(--bg); border: 1px solid var(--border); border-radius: 8px; padding: 1rem; overflow-x: auto; font-size: 0.8rem; line-height: 1.5; max-height: 400px; overflow-y: auto; white-space: pre-wrap; word-break: break-all; }
  .error { color: #f87171; font-size: 0.9rem; margin-top: 0.5rem; }
  .links { margin-top: 2rem; font-size: 0.8rem; color: var(--muted); }
  .links a { color: var(--accent); text-decoration: none; }
  .links a:hover { text-decoration: underline; }
  .loading { opacity: 0.6; pointer-events: none; }
  .tag { display: inline-block; padding: 0.15rem 0.5rem; border-radius: 4px; font-size: 0.75rem; font-weight: 600; margin-left: 0.5rem; }
  .tag-mitm { background: #7c3aed33; color: #a78bfa; }
  .tag-rule { background: #0891b233; color: #22d3ee; }
  .tag-deeplink { background: #05966933; color: #34d399; }
  .btn-deeplink { background: #059669; color: #fff; }
  .btn-deeplink:hover { background: #34d399; color: #0f172a; }
</style>
</head>
<body>
<div class="container">
  <h1>module2anywhere</h1>
  <p class="subtitle">将 Loon .plugin / Surge .sgmodule 模块转换为 Anywhere 规则集</p>

  <div class="card">
    <label for="url-input">模块文件 URL</label>
    <input type="text" id="url-input" placeholder="https://raw.githubusercontent.com/.../bilibili.plugin">
    <div class="options">
      <label><input type="checkbox" id="opt-fetch" checked> 下载脚本</label>
      <label><input type="checkbox" id="opt-generalize"> 主机泛化</label>
      <label><input type="checkbox" id="opt-source" checked> 自动识别 Loon/Surge</label>
    </div>
    <div class="btn-group">
      <button class="btn-primary" onclick="doConvert('mitm')">转换 MITM 规则 <span class="tag tag-mitm">.amrs</span></button>
      <button class="btn-secondary" onclick="doConvert('rule')">转换路由规则 <span class="tag tag-rule">.arrs</span></button>
      <button class="btn-deeplink" onclick="doDeeplink()">导入 Anywhere <span class="tag tag-deeplink">deeplink</span></button>
    </div>
  </div>

  <div id="result-area" class="result" style="display:none;">
    <div class="result-header">
      <h3 id="result-title"></h3>
      <button class="copy-btn" onclick="copyResult()">复制</button>
    </div>
    <pre id="result-content"></pre>
    <div id="subscribe-info" style="margin-top:0.75rem; font-size:0.8rem; color:var(--muted);"></div>
  </div>

  <div id="error-area" class="error" style="display:none;"></div>

  <div class="links">
    <p>API 接口：<a href="/mitm?url=EXAMPLE">GET /mitm?url=...</a> | <a href="/rule?url=EXAMPLE">GET /rule?url=...</a> | <a href="/deeplink?url=EXAMPLE">GET /deeplink?url=...</a></p>
    <p style="margin-top:0.5rem;">参考文档：<a href="https://github.com/NodePassProject/Anywhere" target="_blank">Anywhere</a> | <a href="https://github.com/H1d3r/module2anywhere" target="_blank">module2anywhere</a></p>
  </div>
</div>

<script>
async function doConvert(type) {
  const urlInput = document.getElementById('url-input');
  const url = urlInput.value.trim();
  if (!url) { urlInput.focus(); return; }

  const fetchScripts = document.getElementById('opt-fetch').checked;
  const generalize = document.getElementById('opt-generalize').checked;
  const resultArea = document.getElementById('result-area');
  const errorArea = document.getElementById('error-area');
  const resultTitle = document.getElementById('result-title');
  const resultContent = document.getElementById('result-content');
  const subscribeInfo = document.getElementById('subscribe-info');

  resultArea.style.display = 'none';
  errorArea.style.display = 'none';

  const btn = event.target.closest('button');
  const origText = btn.textContent;
  btn.textContent = '转换中...';
  btn.classList.add('loading');

  try {
    const params = new URLSearchParams({ url: url, fetch: fetchScripts, generalize: generalize });
    const endpoint = type === 'mitm' ? '/mitm' : '/rule';
    const resp = await fetch(endpoint + '?' + params.toString());

    if (!resp.ok) {
      const errText = await resp.text();
      throw new Error(errText || '转换失败');
    }

    const text = await resp.text();
    resultTitle.textContent = type === 'mitm' ? 'MITM 规则 (.amrs)' : '路由规则 (.arrs)';
    resultContent.textContent = text;
    resultArea.style.display = 'block';

    const subURL = window.location.origin + endpoint + '?' + params.toString();
    subscribeInfo.innerHTML = 'Anywhere 订阅地址：<a href="' + subURL + '" target="_blank">' + subURL + '</a>';
  } catch (e) {
    errorArea.textContent = '错误：' + e.message;
    errorArea.style.display = 'block';
  } finally {
    btn.textContent = origText;
    btn.classList.remove('loading');
  }
}

function copyResult() {
  const content = document.getElementById('result-content').textContent;
  navigator.clipboard.writeText(content).then(() => {
    const btn = document.querySelector('.copy-btn');
    btn.textContent = '已复制';
    setTimeout(() => { btn.textContent = '复制'; }, 1500);
  });
}

function doDeeplink() {
  const urlInput = document.getElementById('url-input');
  const url = urlInput.value.trim();
  if (!url) { urlInput.focus(); return; }

  const fetchScripts = document.getElementById('opt-fetch').checked;
  const generalize = document.getElementById('opt-generalize').checked;
  const origin = window.location.origin;
  const params = 'url=' + encodeURIComponent(url) + '&fetch=' + fetchScripts + '&generalize=' + generalize;
  const ruleURL = origin + '/rule?' + params;
  const mitmURL = origin + '/mitm?' + params;

  // 直接构造 deeplink 并跳转，唤起 Anywhere app
  const deeplink = 'anywhere://add-rule-set?link=' + encodeURIComponent(ruleURL) + '&link=' + encodeURIComponent(mitmURL);
  window.location.href = deeplink;
}
</script>
</body>
</html>`;

export async function onRequest(context) {
    if (context.request.method === "OPTIONS") {
        return new Response(null, {
            status: 204,
            headers: {
                "Access-Control-Allow-Origin": "*",
                "Access-Control-Allow-Methods": "GET, OPTIONS",
                "Access-Control-Allow-Headers": "*",
            },
        });
    }
    return new Response(HTML, {
        status: 200,
        headers: {
            "Content-Type": "text/html; charset=utf-8",
            "Cache-Control": "public, max-age=300",
        },
    });
}
