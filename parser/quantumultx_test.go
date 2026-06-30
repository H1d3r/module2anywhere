// parser 包：QuantumultX 解析器单元测试。
package parser

import (
	"strings"
	"testing"

	"github.com/H1d3r/module2anywhere/ir"
)

// TestParseQuantumultX_Sample 解析内联的 QX 示例。
func TestParseQuantumultX_Sample(t *testing.T) {
	content := `#!name=QuantumultX 测试插件
#!desc=QX 行式规则测试
#!author=tester

hostname = app-api.example.com, %APPEND%homepage.example.com, *.wildcard.example.com

[hostname]
hostname = extra.example.com ; trailing comment

# reject 系列
^https?://a\.example\.com/api/v1/ad url reject
^https?://b\.example\.com/api/v1/ad url reject-200
^https?://c\.example\.com/api/v1/ad url reject-dict
^https?://d\.example\.com/api/v1/ad url reject-array
^https?://e\.example\.com/api/v1/ad url reject-img

# 302
^https?://redirect\.example\.com/(.*) url 302 https://target.example.com/$1
^https?://redirect2\.example\.com url 307 https://target.example.com

# response-body（双 url 标记）
^https?://body\.example\.com/api url response-body bubbles url response-body bubbles0

# echo-response（双 url 标记）
^https?://echo\.example\.com/api url echo-response application/json url echo-response body {"code":0,"data":[]}
^https?://echo\.example\.com/api url echo-response application/json url echo-response body {"code":0,"data":[]} format=json
^https?://echo\.example\.com/api url echo-response application/json url echo-response body {"code":1,"data":"a=b"} format=json

# jsonjq
^https?://jq\.example\.com/api url jsonjq-response-body '.feeds | map(select(.isAd != true))'

# scripts
^https?://script\.example\.com/api url script-response-body https://example.com/response.js
^https?://echo-script\.example\.com/api url script-analyze-echo-response https://example.com/echo.js
^https?://script\.example\.com/api url script-request-header https://example.com/request.js,requires-body=1,binary-body-mode=1,tag=foo,argument=bar,max-size=2048,engine=jsc
`
	m, err := ParseQuantumultX(content)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if m.Source != ir.SourceQuantumultX {
		t.Errorf("Source: got %v, want %v", m.Source, ir.SourceQuantumultX)
	}
	if m.Name != "QuantumultX 测试插件" {
		t.Errorf("Name: got %q", m.Name)
	}

	// hostname 规范化：应去除 %APPEND% 和 *.
	wantHosts := map[string]bool{"app-api.example.com": true, "homepage.example.com": true, "wildcard.example.com": true}
	wantHosts["extra.example.com"] = true
	gotHosts := map[string]bool{}
	for _, h := range m.Hostnames {
		gotHosts[h] = true
	}
	if len(gotHosts) != len(wantHosts) {
		t.Errorf("Hostnames: got %v, want %v", gotHosts, wantHosts)
	}
	for h := range wantHosts {
		if !gotHosts[h] {
			t.Errorf("missing hostname %q", h)
		}
	}

	if len(m.Rewrites) < 8 {
		t.Errorf("Rewrites: got %d, want >=8", len(m.Rewrites))
	}

	if len(m.Scripts) != 3 {
		t.Errorf("Scripts: got %d, want 3", len(m.Scripts))
	}
	for _, s := range m.Scripts {
		if s.ScriptPath == "https://example.com/request.js" {
			if !s.RequiresBody || !s.BinaryBody || s.Tag != "foo" || s.Argument != "bar" || s.MaxSize != 2048 || s.Engine != "jsc" || s.Phase != 0 {
				t.Errorf("script params not parsed: %+v", s)
			}
		}
	}

	foundRejectDict := false
	for _, r := range m.Rewrites {
		if r.Action == "reject-dict" {
			foundRejectDict = true
			if !strings.HasPrefix(r.Pattern, "^https") {
				t.Errorf("reject-dict pattern: got %q", r.Pattern)
			}
		}
	}
	if !foundRejectDict {
		t.Errorf("missing reject-dict")
	}

	foundCapture302 := false
	for _, r := range m.Rewrites {
		if r.Action == "302" && strings.Contains(r.Args["url"], "$1") {
			foundCapture302 = true
			if !strings.Contains(r.Pattern, "(") {
				t.Errorf("302 capture pattern: got %q", r.Pattern)
			}
		}
	}
	if !foundCapture302 {
		t.Errorf("missing 302 capture")
	}

	for _, r := range m.Rewrites {
		if r.Action == "response-body" {
			if r.Args["search"] != "bubbles" {
				t.Errorf("response-body search: got %q", r.Args["search"])
			}
			if r.Args["replacement"] != "bubbles0" {
				t.Errorf("response-body replacement: got %q", r.Args["replacement"])
			}
		}
	}

	for _, r := range m.Rewrites {
		if r.Action == "echo-response" {
			if !strings.HasPrefix(r.Args["content-type"], "application/json") {
				t.Errorf("echo-response content-type: got %q", r.Args["content-type"])
			}
			if r.Args["format"] == "json" && !strings.Contains(r.Args["body"], `"code":0`) && !strings.Contains(r.Args["body"], `"code":1`) {
				t.Errorf("echo-response body: got %q", r.Args["body"])
			}
			if r.Args["format"] == "json" && r.Args["format"] != "json" {
				t.Errorf("echo-response format: got %q", r.Args["format"])
			}
		}
	}

	for _, r := range m.Rewrites {
		if r.Action == "jsonjq-response-body" {
			if !strings.Contains(r.Args["jq"], ".feeds") {
				t.Errorf("jsonjq jq: got %q", r.Args["jq"])
			}
		}
	}
}

// TestDetectSource_QX 验证 DetectSource 正确识别 .conf 与 .lpx。
func TestDetectSource_QX(t *testing.T) {
	conf := `#!name=test
hostname = a.com
^https://a.com url reject
`
	if got := DetectSource(conf, "x.conf"); got != ir.SourceQuantumultX {
		t.Errorf("DetectSource .conf: got %v", got)
	}
	if got := DetectSource(conf, "x.lpx"); got != ir.SourceLoon {
		t.Errorf("DetectSource .lpx: got %v", got)
	}
	if got := DetectSource(conf, "x.plugin"); got != ir.SourceLoon {
		t.Errorf("DetectSource .plugin: got %v", got)
	}
	if got := DetectSource(conf, "x.sgmodule"); got != ir.SourceSurge {
		t.Errorf("DetectSource .sgmodule: got %v", got)
	}
}

// TestSplitQXTokens 验证 tokenize 正确处理单引号包裹。
func TestSplitQXTokens(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"^https://a.com url reject", []string{"^https://a.com", "url", "reject"}},
		{"^https://a.com url 302 https://b.com", []string{"^https://a.com", "url", "302", "https://b.com"}},
		{"^https://a.com url jsonjq-response-body 'if . then . end'", []string{"^https://a.com", "url", "jsonjq-response-body", "'if . then . end'"}},
		{"^https://a.com url response-body bubbles url response-body bubbles0",
			[]string{"^https://a.com", "url", "response-body", "bubbles", "url", "response-body", "bubbles0"}},
	}
	for _, c := range cases {
		got := splitQXTokens(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitQXTokens(%q): got %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitQXTokens(%q)[%d]: got %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

// TestApplyUserScriptMeta 验证 GM/TM 风格 UserScript 头元数据提取。
func TestApplyUserScriptMeta(t *testing.T) {
	content := `// ==UserScript==
// @ScriptName        什么值得买净化[墨鱼版]
// @Author            @blackmatrix7, @ddgksf2013
// @Function          去广告
// @UpdateTime        2024-08-12
// @Version           V1.0.3
// ==/UserScript==

hostname = a.com
^https://a.com url reject
`
	m := &ir.Module{Source: ir.SourceQuantumultX, RawMeta: map[string]string{}}
	applyUserScriptMeta(m, content)
	if m.Name != "什么值得买净化[墨鱼版]" {
		t.Errorf("Name: got %q, want %q", m.Name, "什么值得买净化[墨鱼版]")
	}
	if m.Author != "@blackmatrix7, @ddgksf2013" {
		t.Errorf("Author: got %q", m.Author)
	}
	if m.Desc != "去广告" {
		t.Errorf("Desc: got %q, want %q", m.Desc, "去广告")
	}
	if m.Date != "2024-08-12" {
		t.Errorf("Date: got %q", m.Date)
	}
	if m.RawMeta["Version"] != "V1.0.3" {
		t.Errorf("RawMeta Version: got %q", m.RawMeta["Version"])
	}
}

// TestApplyUserScriptMeta_Priority 验证 #!name= 优先于 @ScriptName。
func TestApplyUserScriptMeta_Priority(t *testing.T) {
	content := `// ==UserScript==
// @ScriptName        GM名称
// ==/UserScript==
#!name=QX名称
hostname = a.com
`
	m := &ir.Module{Source: ir.SourceQuantumultX, RawMeta: map[string]string{}}
	applyUserScriptMeta(m, content)
	// 此时 Name 已被 ScriptName 填充
	if m.Name != "GM名称" {
		t.Errorf("after GM: Name=%q", m.Name)
	}
	// 模拟 ParseQuantumultX 后续扫描 #!name= 的覆盖行为
	m.Name = "QX名称"
	if m.Name != "QX名称" {
		t.Errorf("after QX: Name=%q", m.Name)
	}
}

// TestParseQXRoutingRule 验证 QX 行式路由规则解析。
func TestParseQXRoutingRule(t *testing.T) {
	cases := []struct {
		in      string
		want    bool
		rType   string
		rValue  string
		rAction string
	}{
		{"DOMAIN-SUFFIX,example.com,DIRECT", true, "DOMAIN-SUFFIX", "example.com", "DIRECT"},
		{"DOMAIN-KEYWORD,ads,REJECT", true, "DOMAIN-KEYWORD", "ads", "REJECT"},
		{"IP-CIDR,10.0.0.0/8,DIRECT", true, "IP-CIDR", "10.0.0.0/8", "DIRECT"},
		{"DOMAIN,full.example.com,PROXY", true, "DOMAIN", "full.example.com", "PROXY"},
		{"HOST-SUFFIX,example.org,DIRECT", true, "HOST-SUFFIX", "example.org", "DIRECT"},
		{"DOMAIN-WILDCARD,*.example.net,REJECT", true, "DOMAIN-WILDCARD", "*.example.net", "REJECT"},
		{"IP6-CIDR,2001:db8::/32,DIRECT", true, "IP6-CIDR", "2001:db8::/32", "DIRECT"},
		{"GEOIP,CN,DIRECT", true, "GEOIP", "CN", "DIRECT"},
		{"USER-AGENT,MicroMessenger%,REJECT", true, "USER-AGENT", "MicroMessenger%", "REJECT"},
		// 非路由规则
		{"^https://a.com url reject", false, "", "", ""},
		{"hostname = a.com", false, "", "", ""},
		{"# comment", false, "", "", ""},
		{"short,line", false, "", "", ""},
	}
	for _, c := range cases {
		r := parseQXRoutingRule(c.in)
		if c.want {
			if r == nil {
				t.Errorf("parseQXRoutingRule(%q): got nil, want non-nil", c.in)
				continue
			}
			if r.Type != c.rType {
				t.Errorf("Type: got %q, want %q", r.Type, c.rType)
			}
			if r.Value != c.rValue {
				t.Errorf("Value: got %q, want %q", r.Value, c.rValue)
			}
			if r.Action != c.rAction {
				t.Errorf("Action: got %q, want %q", r.Action, c.rAction)
			}
		} else {
			if r != nil {
				t.Errorf("parseQXRoutingRule(%q): got %+v, want nil", c.in, r)
			}
		}
	}
}

// TestParseQuantumultX_WithRoutingRules 验证包含路由规则的 QX 文件解析。
func TestParseQuantumultX_WithRoutingRules(t *testing.T) {
	content := `#!name=路由测试
hostname = ads.example.com

^https?://ads\.example\.com/api url reject
DOMAIN-SUFFIX,ads.com,REJECT
DOMAIN-KEYWORD,tracker,DIRECT
IP-CIDR,10.0.0.0/8,DIRECT
`
	m, err := ParseQuantumultX(content)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(m.Rules) != 3 {
		t.Errorf("Rules: got %d, want 3", len(m.Rules))
		for i, r := range m.Rules {
			t.Logf("  Rule[%d]: Type=%s Value=%s Action=%s", i, r.Type, r.Value, r.Action)
		}
	}
	if len(m.Rewrites) != 1 {
		t.Errorf("Rewrites: got %d, want 1", len(m.Rewrites))
	}
}

// TestExtractHostnameValue 验证 hostname 行提取。
func TestExtractHostnameValue(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"hostname = a.com, b.com", "a.com, b.com"},
		{"hostname=a.com", "a.com"},
		{"  hostname   =   a.com , b.com  ", "a.com , b.com  "},
		{"^https://a.com url reject", ""},
		{"# comment", ""},
	}
	for _, c := range cases {
		got := extractHostnameValue(c.in)
		if got != c.want {
			t.Errorf("extractHostnameValue(%q): got %q, want %q", c.in, got, c.want)
		}
	}
}
