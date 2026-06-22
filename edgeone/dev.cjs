/**
 * EdgeOne CLI 包装脚本
 *
 * 解决 EdgeOne dev server 的 HTTP 431 "Request Header Fields Too Large" 错误。
 *
 * 根因：EdgeOne CLI 内部通过 HTTP 将编译后的 edge function 代码发送到
 * edge function runtime（运行在 worker thread 中）。当编译后的 bundle 较大时，
 * HTTP 请求头超过 Node.js 默认的 maxHeaderSize（16KB），导致 431 错误。
 *
 * 本脚本通过以下方式修复：
 * 1. 检测当前进程的 maxHeaderSize，若不足则用正确的标志重启自身
 * 2. Monkey-patch child_process.spawn/fork，确保所有子进程继承 NODE_OPTIONS
 * 3. Monkey-patch worker_threads.Worker，确保 worker 线程也继承 NODE_OPTIONS
 */

const desiredSize = 131072;
const desiredFlag = '--max-http-header-size=' + desiredSize;

// 检测当前进程的 maxHeaderSize
const http = require('http');
const currentMaxHeaderSize = http.maxHeaderSize;

if (currentMaxHeaderSize < desiredSize) {
  // 当前进程的 maxHeaderSize 不足，需要用正确的标志重启
  console.log('[dev.cjs] 当前 maxHeaderSize=' + currentMaxHeaderSize + '，需要 ' + desiredSize);
  console.log('[dev.cjs] 使用 ' + desiredFlag + ' 重启...');

  const childProcess = require('child_process');
  const args = [desiredFlag, __filename, ...process.argv.slice(2)];
  const child = childProcess.spawn(process.execPath, args, {
    stdio: 'inherit',
    env: Object.assign({}, process.env, {
      NODE_OPTIONS: ((process.env.NODE_OPTIONS || '') + ' ' + desiredFlag).trim(),
    }),
  });

  child.on('exit', function (code) {
    process.exit(code || 0);
  });

  child.on('error', function (err) {
    console.error('[dev.cjs] 重启失败:', err);
    process.exit(1);
  });

  return;
}

console.log('[dev.cjs] maxHeaderSize=' + currentMaxHeaderSize + '，满足要求');

// 确保 NODE_OPTIONS 包含 --max-http-header-size
const existing = process.env.NODE_OPTIONS || '';
if (!existing.includes('--max-http-header-size')) {
  process.env.NODE_OPTIONS = (existing + ' ' + desiredFlag).trim();
}

// Monkey-patch child_process 以确保子进程继承 NODE_OPTIONS
const childProcess = require('child_process');
const originalSpawn = childProcess.spawn;
const originalFork = childProcess.fork;

/**
 * 包装 spawn 调用，确保子进程继承 NODE_OPTIONS 环境变量。
 */
childProcess.spawn = function patchedSpawn(command, args, options) {
  if (options && typeof options === 'object') {
    options.env = mergeNodeOptions(options.env);
  } else if (!options) {
    options = { env: mergeNodeOptions(undefined) };
  }
  return originalSpawn.call(this, command, args, options);
};

/**
 * 包装 fork 调用，确保子进程继承 NODE_OPTIONS 环境变量。
 */
childProcess.fork = function patchedFork(modulePath, args, options) {
  if (options && typeof options === 'object') {
    options.env = mergeNodeOptions(options.env);
  } else if (!options) {
    options = { env: mergeNodeOptions(undefined) };
  }
  return originalFork.call(this, modulePath, args, options);
};

// Monkey-patch worker_threads.Worker 以确保 worker 线程继承 NODE_OPTIONS
try {
  const workerThreads = require('worker_threads');
  const OriginalWorker = workerThreads.Worker;

  /**
   * 包装 Worker 构造函数，确保 worker 线程使用正确的 NODE_OPTIONS。
   */
  workerThreads.Worker = function PatchedWorker(filename, options) {
    options = options || {};
    // worker_threads.Worker 支持 execArgv 选项，用于传递 Node.js 标志
    if (!options.execArgv) {
      options.execArgv = [];
    }
    // 检查 execArgv 中是否已包含 max-http-header-size
    var hasFlag = false;
    for (var i = 0; i < options.execArgv.length; i++) {
      if (options.execArgv[i].indexOf('--max-http-header-size') === 0) {
        hasFlag = true;
        break;
      }
    }
    if (!hasFlag) {
      options.execArgv = options.execArgv.concat([desiredFlag]);
    }
    return new OriginalWorker(filename, options);
  };

  // 继承原型链
  workerThreads.Worker.prototype = OriginalWorker.prototype;
  workerThreads.Worker.__proto__ = OriginalWorker;

  console.log('[dev.cjs] 已 patch worker_threads.Worker');
} catch (e) {
  // worker_threads 不可用时忽略
}

/**
 * 合并 NODE_OPTIONS 到目标环境变量对象。
 * @param {object|undefined} env - 目标环境变量对象
 * @returns {object} 合并后的环境变量对象
 */
function mergeNodeOptions(env) {
  const result = Object.assign({}, process.env, env || {});
  if (!result.NODE_OPTIONS || !result.NODE_OPTIONS.includes('--max-http-header-size')) {
    result.NODE_OPTIONS = (result.NODE_OPTIONS || '') + ' ' + desiredFlag;
    result.NODE_OPTIONS = result.NODE_OPTIONS.trim();
  }
  return result;
}

// 加载真正的 EdgeOne CLI
const path = require('path');
const cliPath = path.resolve(__dirname, 'node_modules/edgeone/edgeone-bin/edgeone.js');
require(cliPath);
