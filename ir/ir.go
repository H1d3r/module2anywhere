// Package ir 定义 Loon/Surge 模块解析后的统一中间表示。
//
// 无论是 Loon .plugin 还是 Surge .sgmodule，解析后都归一化为 Module 结构，
// 供后续转换器消费，生成 Anywhere 的 .arrs 与 .amrs 规则集。
package ir

// Source 标识模块来源格式。
type Source int

const (
	SourceUnknown Source = iota
	SourceLoon
	SourceSurge
)

// String 返回来源的可读名称。
func (s Source) String() string {
	switch s {
	case SourceLoon:
		return "loon"
	case SourceSurge:
		return "surge"
	default:
		return "unknown"
	}
}

// Module 表示解析后的 Loon/Surge 模块。
type Module struct {
	Source    Source            // 来源格式
	Name      string            // #!name
	Desc      string            // #!desc
	Author    string            // #!author
	Homepage  string            // #!homepage
	Date      string            // #!date
	RawMeta   map[string]string // 其他元数据（icon/tag/category 等会被丢弃，但保留原始以便日志）
	Hostnames []string          // MITM hostname 列表（已规范化，去除 *. 与 %APPEND%）
	Rules     []RoutingRule     // [Rule] 段
	Rewrites  []RewriteRule     // [Rewrite] / [URL Rewrite] 段
	Scripts   []ScriptRule      // [Script] 段
	HeaderRWs []HeaderRule      // [Header Rewrite] 段（Surge）
	MapLocals []MapLocalRule    // [Map Local] 段（Surge）
	Arguments []Argument        // [Argument] 段（Loon，仅记录，不参与转换）
}

// RoutingRule 路由规则（[Rule] 段）。
type RoutingRule struct {
	Type    string   // DOMAIN-SUFFIX / DOMAIN-KEYWORD / DOMAIN / IP-CIDR / IP-CIDR6 / URL-REGEX / GEOIP / PROCESS-NAME ...
	Value   string   // 规则值
	Action  string   // DIRECT / REJECT / REJECT-DICT / PROXY / URL-REGEX 的 REJECT 变体等
	Options []string // no-resolve / force-remote-dns 等附加选项
	Raw     string   // 原始行（用于错误报告）
}

// RewriteRule 重写规则（[Rewrite] / [URL Rewrite] 段）。
type RewriteRule struct {
	Pattern string            // URL 正则
	Action  string            // reject / reject-dict / 302 / mock-response-body / response-body-json-del / request-header 等
	Args    map[string]string // 通用参数容器：url / data / status-code / data-type / path / value 等
	RawJS   string            // 内联 JS（request-header / response-body / _request-header 等动作）
	Raw     string            // 原始行
}

// ScriptRule 脚本规则（[Script] 段）。
type ScriptRule struct {
	Phase        int    // 0=request, 1=response
	Pattern      string // URL 正则
	ScriptPath   string // 脚本 URL（远程）
	RequiresBody bool   // 是否需要 body
	BinaryBody   bool   // 二进制 body 模式（protobuf）
	Argument     string // Loon argument 参数
	Tag          string // 显示标签
	MaxSize      int    // 最大处理大小
	Engine       string // 脚本引擎（保留，Anywhere 忽略）
	Raw          string // 原始行
}

// HeaderRule 头部重写规则（Surge [Header Rewrite]）。
type HeaderRule struct {
	Pattern string // URL 正则
	Phase   int    // 0=request, 1=response
	Op      string // add / replace / delete
	Name    string // header 名
	Value   string // header 值（delete 时为空）
	Raw     string // 原始行
}

// MapLocalRule Surge [Map Local] 规则。
type MapLocalRule struct {
	Pattern string // URL 正则
	DataURL string // data="..." 指向的本地/远程文件
	Header  string // header="..." 头部
	Raw     string // 原始行
}

// Argument Loon [Argument] 段条目（仅记录，不参与转换）。
type Argument struct {
	Key   string
	Value string
	Raw   string
}

// RoutingType 表示 Anywhere .arrs 的规则类型 ID。
type RoutingType int

const (
	RoutingIPv4CIDR     RoutingType = 0
	RoutingIPv6CIDR     RoutingType = 1
	RoutingDomainSuffix RoutingType = 2
	RoutingDomainKey    RoutingType = 3
)

// IsRejectAction 判断动作是否为 REJECT 类（用于决定 URL-REGEX 是否转入 .amrs）。
func IsRejectAction(action string) bool {
	switch action {
	case "REJECT", "REJECT-DICT", "REJECT-ARRAY", "REJECT-IMG", "REJECT-200":
		return true
	}
	return false
}
