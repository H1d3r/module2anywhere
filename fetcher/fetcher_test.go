// fetcher 包：quantumult.app add-resource 链接解析测试。
package fetcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIsQuantumultXAddResource_URL 验证 URL 检测。
func TestIsQuantumultXAddResource_URL(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://quantumult.app/x/open-app/add-resource?remote-resource=%7B%22rewrite_remote%22%3A%5B%5D%7D", true},
		{"https://www.quantumult.app/x/open-app/add-resource?remote-resource=xx", true},
		{"https://quantumult.app/x/open-app/add-resource/?remote-resource=xx", true},
		{"https://example.com/x/open-app/add-resource?remote-resource=xx", false},
		{"https://quantumult.app/x/other?remote-resource=xx", false},
		{"", false},
	}
	for _, c := range cases {
		got := IsQuantumultXAddResourceURL(c.in)
		if got != c.want {
			t.Errorf("IsQuantumultXAddResourceURL(%q): got %v, want %v", c.in, got, c.want)
		}
	}
}

// TestExtractQuantumultXResourceURLs 验证从 add-resource 链接提取 URL 列表。
func TestExtractQuantumultXResourceURLs(t *testing.T) {
	// 用户提供的真实 URL
	realURL := "https://quantumult.app/x/open-app/add-resource?remote-resource=%7B%22rewrite_remote%22%3A%5B%22https%3A%2F%2Fddgksf2013.top%2Fscripts%2Fnicegram.vip.js%2C%20tag%3DNicegram%E4%BC%9A%E5%91%98%E8%A7%A3%E9%94%81%40ddgksf2013%2C%20update-interval%3D86400%2C%20opt-parser%3Dtrue%2C%20enabled%3Dtrue%22%5D%7D"

	urls, err := ExtractQuantumultXResourceURLs(realURL)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(urls) != 1 {
		t.Fatalf("got %d urls, want 1: %v", len(urls), urls)
	}
	want := "https://ddgksf2013.top/scripts/nicegram.vip.js"
	if urls[0] != want {
		t.Errorf("got %q, want %q", urls[0], want)
	}
}

// TestExtractQuantumultXResourceURLs_Multiple 验证多个 key 的情况。
func TestExtractQuantumultXResourceURLs_Multiple(t *testing.T) {
	raw := `{
		"rewrite_remote": [
			"https://a.com/r1.js, tag=rewrite1",
			"https://a.com/r2.js, tag=rewrite2, update-interval=3600"
		],
		"server_remote": [
			"https://b.com/s1.list, tag=server1"
		],
		"filter_remote": [],
		"task_remote": [
			"https://c.com/t1.js"
		]
	}`
	// 模拟 add-resource 链接（payload 部分 URL 编码）
	import_func := func() {
		// 用真实编码过的 URL 形式触发提取
	}
	_ = import_func

	// 直接构造 query（payload 不再 URL 编码）
	got, err := ExtractQuantumultXResourceURLs("https://quantumult.app/x/open-app/add-resource?remote-resource=" + urlEncode(raw))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	want := []string{
		"https://a.com/r1.js",
		"https://a.com/r2.js",
		"https://b.com/s1.list",
		"https://c.com/t1.js",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d urls, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

// TestExtractQuantumultXResourceURLs_Invalid 验证错误处理。
func TestExtractQuantumultXResourceURLs_Invalid(t *testing.T) {
	// 缺少 remote-resource
	_, err := ExtractQuantumultXResourceURLs("https://quantumult.app/x/open-app/add-resource")
	if err == nil {
		t.Errorf("expected error for missing param")
	}
	// remote-resource 不是 JSON
	_, err = ExtractQuantumultXResourceURLs("https://quantumult.app/x/open-app/add-resource?remote-resource=not-json")
	if err == nil {
		t.Errorf("expected error for bad JSON")
	}
}

func TestFetchWithLimitRejectsLargeResponse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.plugin")
	if err := os.WriteFile(path, []byte("hello world!"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := New().FetchWithLimit(context.Background(), path, 5)
	if err == nil || !strings.Contains(err.Error(), "超过大小限制") {
		t.Fatalf("FetchWithLimit error = %v, want size limit error", err)
	}
}

func TestFetchBlocksLocalhost(t *testing.T) {
	_, err := New().Fetch(context.Background(), "http://127.0.0.1/test.plugin")
	if err == nil || !strings.Contains(err.Error(), "不允许拉取") {
		t.Fatalf("Fetch localhost error = %v, want blocked host error", err)
	}
}

// urlEncode URL 编码辅助（仅本测试用）。
func urlEncode(s string) string {
	const hex = "0123456789ABCDEF"
	out := make([]byte, 0, len(s)*3)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			out = append(out, c)
		} else {
			out = append(out, '%', hex[c>>4], hex[c&0x0F])
		}
	}
	return string(out)
}
