package parser

import "testing"

// TestParseSurgeURLRewriteDirtyFormats 验证 Surge URL Rewrite 常见脏格式兼容。
func TestParseSurgeURLRewriteDirtyFormats(t *testing.T) {
	body := `^https?://a\.example/path - reject-dict
^https?://b\.example/path header-del Cookie
(^https?:\/\/app\.biliintl.com\/intl\/.+)(&sim_code=\d+)(.+)-302$1$3
^https?://foo-reject\.example/path
^https?://foo-302\.example/path`
	rules := parseSurgeURLRewrites(body)
	if len(rules) != 5 {
		t.Fatalf("got %d rules, want 5", len(rules))
	}
	if rules[0].Action != "reject-dict" {
		t.Fatalf("rules[0].Action=%q, want reject-dict", rules[0].Action)
	}
	if rules[1].Action != "header-del" || rules[1].Args["header"] != "Cookie" {
		t.Fatalf("rules[1]=%+v, want header-del Cookie", rules[1])
	}
	if rules[2].Action != "302" || rules[2].Args["url"] != "$1$3" {
		t.Fatalf("rules[2]=%+v, want 302 with $1$3", rules[2])
	}
	if rules[3].Action != "" || rules[3].Pattern != `^https?://foo-reject\.example/path` {
		t.Fatalf("rules[3]=%+v, want untouched pattern containing -reject", rules[3])
	}
	if rules[4].Action != "" || rules[4].Pattern != `^https?://foo-302\.example/path` {
		t.Fatalf("rules[4]=%+v, want untouched pattern containing -302", rules[4])
	}
}

// TestParseSurgeHeaderRewriteAliases 验证 Header Rewrite 的简写别名。
func TestParseSurgeHeaderRewriteAliases(t *testing.T) {
	body := `^https?://a\.example/request request add "X-Test" "1 2"
^https?://b\.example/response response delete Cookie`
	rules := parseSurgeHeaderRewrites(body)
	if len(rules) != 2 {
		t.Fatalf("got %d rules, want 2", len(rules))
	}
	if rules[0].Phase != 0 || rules[0].Op != "add" || rules[0].Name != `X-Test` || rules[0].Value != `1 2` {
		t.Fatalf("rules[0]=%+v, want request add", rules[0])
	}
	if rules[1].Phase != 1 || rules[1].Op != "delete" || rules[1].Name != "Cookie" {
		t.Fatalf("rules[1]=%+v, want response delete Cookie", rules[1])
	}
}

// TestParseLoonRewriteHeaderDelAlias 验证 Loon Rewrite 的 _header-del 别名。
func TestParseLoonRewriteHeaderDelAlias(t *testing.T) {
	rules := parseLoonRewrites(`^https?://example\.com/ _header-del Cookie`)
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	if rules[0].Action != "header-del" || rules[0].Args["header"] != "Cookie" {
		t.Fatalf("rules[0]=%+v, want header-del Cookie", rules[0])
	}
}

// TestParseLoonRewriteInlineJSAliases 验证 Loon Rewrite 的下划线内联 JS 别名。
func TestParseLoonRewriteInlineJSAliases(t *testing.T) {
	rules := parseLoonRewrites(`^https?://example\.com/ _response-body console.log(1)`)
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	if rules[0].Action != "response-body" || rules[0].RawJS != "console.log(1)" {
		t.Fatalf("rules[0]=%+v, want response-body inline JS", rules[0])
	}
}

// TestParseSurgeURLRewriteInlineJSAliases 验证 Surge URL Rewrite 的无下划线 JS 别名。
func TestParseSurgeURLRewriteInlineJSAliases(t *testing.T) {
	rules := parseSurgeURLRewrites(`^https?://example\.com/ request-header console.log(1)`)
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	if rules[0].Action != "request-header" || rules[0].RawJS != "console.log(1)" {
		t.Fatalf("rules[0]=%+v, want request-header inline JS", rules[0])
	}
}

// TestParseSurgeURLRewriteUrlPrefix 验证 Surge URL Rewrite 兼容 Loon 风格 url 前缀。
func TestParseSurgeURLRewriteUrlPrefix(t *testing.T) {
	rules := parseSurgeURLRewrites(`^https?://example\.com/ url reject-dict`)
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	if rules[0].Action != "reject-dict" {
		t.Fatalf("rules[0]=%+v, want reject-dict", rules[0])
	}
}

// TestParseSurgeURLRewriteActionLast 验证 URL 在前、302/307 在后的老式写法。
func TestParseSurgeURLRewriteActionLast(t *testing.T) {
	rules := parseSurgeURLRewrites(`^https?://example\.com/ https://target.example.com 302`)
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	if rules[0].Action != "302" || rules[0].Args["url"] != "https://target.example.com" {
		t.Fatalf("rules[0]=%+v, want 302 with url", rules[0])
	}
}

// TestParseSurgeURLRewriteActionTrailingURL 验证 action 在前、URL 在后的旧式写法。
func TestParseSurgeURLRewriteActionTrailingURL(t *testing.T) {
	rules := parseSurgeURLRewrites(`^https?://example\.com/ 302 https://target.example.com`)
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	if rules[0].Action != "302" || rules[0].Args["url"] != "https://target.example.com" {
		t.Fatalf("rules[0]=%+v, want 302 with url", rules[0])
	}
}

// TestParseSurgeMITMMultiHostname 验证多行 hostname 声明会累计去重。
func TestParseSurgeMITMMultiHostname(t *testing.T) {
	body := `hostname = a.com, %APPEND%b.com
hostname = c.com, *.d.com
hostname = a.com`
	hosts := parseSurgeMITM(body)
	want := map[string]bool{"a.com": true, "b.com": true, "c.com": true, "d.com": true}
	if len(hosts) != len(want) {
		t.Fatalf("got %v, want %v", hosts, want)
	}
	for _, host := range hosts {
		if !want[host] {
			t.Fatalf("unexpected host %q in %v", host, hosts)
		}
	}
}

// TestParseSurgeMapLocalAliases 验证 Map Local 字段别名与注释剥离。
func TestParseSurgeMapLocalAliases(t *testing.T) {
	body := `^https?://a\.example/ uri="https://example.com/a.json" headers="Content-Type: application/json" # note
^https?://b\.example/ data-url="https://example.com/b.json"
^https?://c\.example/ file="https://example.com/c.json"`
	rules := parseSurgeMapLocals(body)
	if len(rules) != 3 {
		t.Fatalf("got %d rules, want 3", len(rules))
	}
	if rules[0].DataURL != "https://example.com/a.json" || rules[0].Header != "Content-Type: application/json" {
		t.Fatalf("rules[0]=%+v, want url/header aliases", rules[0])
	}
	if rules[1].DataURL != "https://example.com/b.json" {
		t.Fatalf("rules[1]=%+v, want data-url alias", rules[1])
	}
	if rules[2].DataURL != "https://example.com/c.json" {
		t.Fatalf("rules[2]=%+v, want file alias", rules[2])
	}
}

// TestParseSurgeScriptMixedURLParams 验证 Surge Script 段兼容混入的 QX url script-* 写法。
func TestParseSurgeScriptMixedURLParams(t *testing.T) {
	line := `demo=type=http-response,pattern=^https?:\/\/api\.example\/v1 url script-response-body https://example.com/a.js,requires-body=1,binary-body-mode=1,max-size=2048`
	s, err := parseSurgeScriptLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if s == nil {
		t.Fatal("script rule is nil")
	}
	if s.Phase != 1 || s.Pattern == "" || s.ScriptPath != "https://example.com/a.js" {
		t.Fatalf("unexpected script rule: %+v", s)
	}
	if !s.RequiresBody || !s.BinaryBody || s.MaxSize != 2048 {
		t.Fatalf("mixed trailing args not parsed: %+v", s)
	}
}

// TestParseSurgeMixedURLScriptParamsHeader 验证混入 Surge Script 段的 url script-* 写法。
func TestParseSurgeMixedURLScriptParamsHeader(t *testing.T) {
	line := `demo=type=http-response,pattern=^https?:\/\/api\.example\/v1 url script-response-header https://example.com/a.js,requires-body=1,binary-body-mode=1,max-size=2048`
	s, err := parseSurgeScriptLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if s == nil {
		t.Fatal("script rule is nil")
	}
	if s.Phase != 1 || s.Pattern == "" || s.ScriptPath != "https://example.com/a.js" {
		t.Fatalf("unexpected script rule: %+v", s)
	}
	if !s.RequiresBody || !s.BinaryBody || s.MaxSize != 2048 {
		t.Fatalf("mixed trailing args not parsed: %+v", s)
	}
}

// TestParseSurgeMixedURLScriptParamsNegative 验证标准 Surge script 不被 mixed 误判。
func TestParseSurgeMixedURLScriptParamsNegative(t *testing.T) {
	line := `demo=type=http-response,pattern=^https?:\/\/api\.example\/v1,argument=foo url script-response-body bar,script-path=https://example.com/std.js`
	s, err := parseSurgeScriptLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if s == nil || s.ScriptPath != "https://example.com/std.js" || s.Argument != "foo url script-response-body bar" {
		t.Fatalf("standard script was misparsed as mixed: %+v", s)
	}
}
