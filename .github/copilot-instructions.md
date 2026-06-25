# module2anywhere — GitHub Copilot Instructions

## Project Overview
Loon/Surge module to Anywhere rule set converter. Go backend + EdgeOne Pages Functions frontend.

## Critical: Dual-Engine Sync
This project has two independent conversion engines that MUST stay in sync:
- **Go side**: `converter/script.go` (for CLI and HTTP API)
- **EdgeOne side**: `edgeone/functions/lib.js` (for online conversion)

Any change to script rewriting logic must be applied to BOTH sides. Key functions to sync:
- `RewriteScriptAPI` ↔ `rewriteScriptAPI`
- `rewriteDoneCalls` ↔ `rewriteDoneCalls`
- `rewriteHttpClientCalls` ↔ `rewriteHttpClientCalls`
- `injectBoxJSPolyfill` ↔ `injectBoxJSPolyfill`
- `BuildWrappedScript` ↔ `encodeWrappedScript`
- `wrapAsProcess` ↔ `wrapAsProcess`
- `needsAsync` detection conditions

## Headers Format (Most Common Bug Source)
- **Anywhere**: `[[name, value], ...]` array of pairs
- **Loon/Surge**: `{name: value}` plain object
- All headers from Anywhere APIs must be converted before exposing to Loon/Surge compatible code

## Body Format
- **Anywhere `ctx.body`**: `Uint8Array`
- **Loon/Surge `$request.body`/`$response.body`**: `string` (needs `Anywhere.codec.utf8.decode()`)
- **Loon `$response.bodyBytes`/`$request.bodyBytes`**: `Uint8Array` (maps directly to `ctx.body`)
- **Replacement order**: Replace longer identifiers first (`bodyBytes` before `body`)

## JavaScriptCore (JSCore) Pitfalls
1. `var` hoisting: declaration hoists but assignment does NOT — `var X = globalThis.X` must come AFTER `globalThis.X = ...`
2. `new Function()` only accesses global scope — needs `var X = globalThis.X` mapping
3. `function` declarations do NOT hoist across `try` blocks in JSCore

## Build Steps
1. After modifying `edgeone/functions/lib.js`: run `cd edgeone && node build.js`
2. After modifying Go code: run `go build ./... && go vet ./...`
3. `lib.js` is self-contained — NO imports allowed

## Reference Documents
- `README.md` Section 7: Script API mapping table
- `docs/MITM.md`: Anywhere MITM system developer guide
- `AGENTS.md`: Complete project guide
