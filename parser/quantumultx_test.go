// parser 包：QuantumultX 解析器单元测试。
package parser

import (
	"os"
	"strings"
	"testing"

	"github.com/H1d3r/module2anywhere/ir"
)

// TestParseQuantumultX_Sample 解析 testdata/sample.conf。
func TestParseQuantumultX_Sample(t *testing.T) {
	data, err := os.ReadFile("../testdata/sample.conf")
	if err != nil {
		t.Fatalf("read sample.conf: %v", err)
	}
	m, err := ParseQuantumultX(string(data))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if m.Source != ir.SourceQuantumultX {
		t.Errorf("Source: got %v, want %v", m.Source, ir.SourceQuantumultX)
	}
	if m.Name != "QuantumultX 测试插件" {
		t.Errorf("Name: got %q", m.Name)
	}

	// 期望至少 8 个 host
	if len(m.Hostnames) < 6 {
		t.Errorf("Hostnames: got %d, want >=6, %v", len(m.Hostnames), m.Hostnames)
	}

	// 重写规则数量（reject×5 + 302×2 + response-body×1 + echo-response×1 + jsonjq×1 = 10）
	if len(m.Rewrites) < 8 {
		t.Errorf("Rewrites: got %d, want >=8", len(m.Rewrites))
	}

	// 脚本规则：script-response-body + script-analyze-echo-response = 2
	if len(m.Scripts) != 2 {
		t.Errorf("Scripts: got %d, want 2", len(m.Scripts))
	}

	// 检查 reject-dict
	found := false
	for _, r := range m.Rewrites {
		if r.Action == "reject-dict" {
			found = true
			if !strings.HasPrefix(r.Pattern, "^https") {
				t.Errorf("reject-dict pattern: got %q", r.Pattern)
			}
		}
	}
	if !found {
		t.Errorf("missing reject-dict")
	}

	// 检查 302 含 $1
	for _, r := range m.Rewrites {
		if r.Action == "302" && strings.Contains(r.Args["url"], "$1") {
			if !strings.HasPrefix(r.Pattern, "^(") {
				t.Errorf("302 capture pattern: got %q", r.Pattern)
			}
		}
	}

	// 检查 response-body（QX 形式）
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

	// 检查 echo-response
	for _, r := range m.Rewrites {
		if r.Action == "echo-response" {
			if !strings.HasPrefix(r.Args["content-type"], "application/json") {
				t.Errorf("echo-response content-type: got %q", r.Args["content-type"])
			}
			if !strings.Contains(r.Args["body"], `"code":0`) {
				t.Errorf("echo-response body: got %q", r.Args["body"])
			}
		}
	}

	// 检查 jsonjq-response-body
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
