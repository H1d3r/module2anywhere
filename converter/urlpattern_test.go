// converter 包：URL pattern 安全泛化测试。
package converter

import "testing"

// TestSafeGeneralizeHost 验证主机泛化的安全检查。
func TestSafeGeneralizeHost(t *testing.T) {
	cases := []struct {
		desc      string
		pattern   string
		hostnames []string
		want      string
	}{
		{
			desc:      "主机被 hostname 覆盖 → 泛化",
			pattern:   `^https://homepage-api\.smzdm\.com/v3/home`,
			hostnames: []string{"smzdm.com", "homepage-api.smzdm.com"},
			want:      `^https://[^/]+/v3/home`,
		},
		{
			desc:      "主机未被 hostname 覆盖 → 保留原始",
			pattern:   `^https://ad\.example\.com/api`,
			hostnames: []string{"smzdm.com"},
			want:      `^https://ad\.example\.com/api`,
		},
		{
			desc:      "主机是 hostname 的子域 → 泛化",
			pattern:   `^https://api\.smzdm\.com/v1`,
			hostnames: []string{"smzdm.com"},
			want:      `^https://[^/]+/v1`,
		},
		{
			desc:      "hostname 列表为空 → 保留原始",
			pattern:   `^https://ad\.example\.com/api`,
			hostnames: nil,
			want:      `^https://ad\.example\.com/api`,
		},
		{
			desc:      "已含 .* 通配符 → 视为已泛化，不处理",
			pattern:   `^https?://.*zdmimg\.com/cpm/api`,
			hostnames: []string{"zdmimg.com"},
			want:      `^https?://.*zdmimg\.com/cpm/api`,
		},
		{
			desc:      "已含 [^/]+ 通配符 → 视为已泛化，不处理",
			pattern:   `^https://[^/]+/v1`,
			hostnames: []string{"example.com"},
			want:      `^https://[^/]+/v1`,
		},
		{
			desc:      "alternation 主机全部被覆盖 → 泛化",
			pattern:   `^https://(?:a\.smzdm\.com|b\.smzdm\.com)/v1`,
			hostnames: []string{"smzdm.com"},
			want:      `^https://[^/]+/v1`,
		},
		{
			desc:      "alternation 主机部分未被覆盖 → 保留原始",
			pattern:   `^https://(?:a\.smzdm\.com|b\.other\.com)/v1`,
			hostnames: []string{"smzdm.com"},
			want:      `^https://(?:a\.smzdm\.com|b\.other\.com)/v1`,
		},
		{
			desc:      "HTTP pattern 同样处理",
			pattern:   `^http://api\.smzdm\.com/v1`,
			hostnames: []string{"smzdm.com"},
			want:      `^http://[^/]+/v1`,
		},
	}

	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			got := ConvertURLPatternWithHostnames(c.pattern, true, c.hostnames)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestGeneralizeHostDisabled 验证默认关闭时保留原始。
func TestGeneralizeHostDisabled(t *testing.T) {
	pattern := `^https://homepage-api\.smzdm\.com/v3/home`
	got := ConvertURLPatternWithHostnames(pattern, false, []string{"smzdm.com"})
	if got != pattern {
		t.Errorf("expected pattern unchanged when generalize=false, got %q", got)
	}
}

// TestEndOptionalQuery 验证结尾 \? 转 (?:\?|$)。
func TestEndOptionalQuery(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// 当前实现：仅做 \/ → / 与结尾 \? → (?:\?|$)，不反转义 \. 等其他转义
		{`^https://api\.example\.com/search\?`, `^https://api\.example\.com/search(?:\?|$)`},
		{`^https:\/\/api\.example\.com\/search\?`, `^https://api\.example\.com/search(?:\?|$)`},
	}
	for _, c := range cases {
		got := ConvertURLPattern(c.in, false)
		if got != c.want {
			t.Errorf("ConvertURLPattern(%q): got %q, want %q", c.in, got, c.want)
		}
	}
}
