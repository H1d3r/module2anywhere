package parser

import "testing"

// TestParseSurgeURLRewriteDirtyFormats 验证 Surge URL Rewrite 常见脏格式兼容。
func TestParseSurgeURLRewriteDirtyFormats(t *testing.T) {
	body := `^https?://a\.example/path - reject-dict
(^https?:\/\/app\.biliintl.com\/intl\/.+)(&sim_code=\d+)(.+)-302$1$3
^https?://foo-reject\.example/path
^https?://foo-302\.example/path`
	rules := parseSurgeURLRewrites(body)
	if len(rules) != 4 {
		t.Fatalf("got %d rules, want 4", len(rules))
	}
	if rules[0].Action != "reject-dict" {
		t.Fatalf("rules[0].Action=%q, want reject-dict", rules[0].Action)
	}
	if rules[1].Action != "302" || rules[1].Args["url"] != "$1$3" {
		t.Fatalf("rules[1]=%+v, want 302 with $1$3", rules[1])
	}
	if rules[2].Action != "" || rules[2].Pattern != `^https?://foo-reject\.example/path` {
		t.Fatalf("rules[2]=%+v, want untouched pattern containing -reject", rules[2])
	}
	if rules[3].Action != "" || rules[3].Pattern != `^https?://foo-302\.example/path` {
		t.Fatalf("rules[3]=%+v, want untouched pattern containing -302", rules[3])
	}
}

// TestParseSurgeMixedURLScriptParams 验证混入 Surge Script 段的 url script-* 写法。
func TestParseSurgeMixedURLScriptParams(t *testing.T) {
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
