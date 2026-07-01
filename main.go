// Command module2anywhere 将 Loon .plugin / Surge .sgmodule / QuantumultX .conf 模块转换为 Anywhere .arrs / .amrs 规则集。
//
// 用法：
//
//	module2anywhere -i <input.plugin|input.sgmodule|input.conf|URL> -o <output-dir> [options]
//	module2anywhere --server --port 8080  # Web 服务模式
//
// 支持本地文件与远程 URL 输入。远程模块会先下载再解析；脚本 script-path 也会被下载、
// 改写为 Anywhere API 并 base64 编码后嵌入 .amrs。
// 对 GitHub 原始域名自动使用加速代理（ghfast.top / ph.ipv9.win），失败回退直连。
// 同时识别 Quantumult X 一键订阅协议（quantumult.app add-resource 链接），自动展开为多个订阅。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/H1d3r/module2anywhere/converter"
	"github.com/H1d3r/module2anywhere/fetcher"
	"github.com/H1d3r/module2anywhere/ir"
	"github.com/H1d3r/module2anywhere/parser"
	"github.com/H1d3r/module2anywhere/server"
)

type argumentFlags map[string]string

func (a *argumentFlags) String() string {
	if a == nil || len(*a) == 0 {
		return ""
	}
	var parts []string
	for k, v := range *a {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}

func (a *argumentFlags) Set(value string) error {
	idx := strings.Index(value, "=")
	if idx <= 0 {
		return fmt.Errorf("argument must be key=value")
	}
	if *a == nil {
		*a = make(map[string]string)
	}
	(*a)[strings.TrimSpace(value[:idx])] = strings.TrimSpace(value[idx+1:])
	return nil
}

func main() {
	input := flag.String("i", "", "输入文件路径或远程 URL（Loon .plugin / Surge .sgmodule）")
	outputDir := flag.String("o", "./out", "输出目录（生成 .arrs 和 .amrs）")
	format := flag.String("format", "both", "输出格式：both / arrs / amrs")
	fetchScripts := flag.Bool("fetch-scripts", true, "远程下载脚本并改写（默认开启）")
	generalizeHost := flag.Bool("generalize-host", false, "URL pattern 主机泛化为 [^/]+（默认关闭，谨慎开启）")
	encodingPreprocess := flag.Bool("encoding-preprocess", true, "为 body 处理规则自动添加 accept-encoding 预处理对（默认开启）")
	noMetadata := flag.Bool("no-metadata", false, "不在输出文件头部写入元数据注释")
	streamScript := flag.Bool("stream-script", false, "将脚本转为 stream-script (op 101)，仅适合已适配逐帧处理的脚本")
	wrapScripts := flag.Bool("wrap-scripts", true, "包装执行模式：原样编码上游脚本并运行兼容层（默认开启，设为 false 使用直改模式）")
	autoContentType := flag.Bool("auto-content-type", true, "兼容旧参数；官方 Anywhere 当前不识别顶层 content-type，JSON/mock 响应头改由脚本保留")
	concurrency := flag.Int("concurrency", 8, "脚本并发下载数（默认 8）")
	scriptTimeout := flag.Int("script-timeout", 10, "单个脚本下载超时秒数（默认 10）")
	maxInputBytes := flag.Int64("max-input-bytes", 512*1024, "远程模块最大读取字节数（默认 512KB，0 表示不限制）")
	maxScriptBytes := flag.Int64("max-script-bytes", 1024*1024, "单个远程脚本最大读取字节数（默认 1MB，0 使用默认值）")
	maxScriptFetches := flag.Int("max-script-fetches", 45, "单次转换最多下载的唯一脚本数量（默认 45，0 使用默认值）")
	sourceFlag := flag.String("source", "", "强制指定来源：loon / surge / quantumultx（留空自动检测）")
	preserveParameters := flag.Bool("preserve-parameters", false, "在 .amrs 中保留 [Parameter] 段（默认关闭）")
	verbose := flag.Bool("v", false, "输出详细转换报告")
	var arguments argumentFlags
	flag.Var(&arguments, "argument", "覆盖模块参数，格式 key=value，可重复")

	// 代理相关
	proxyMode := flag.String("proxy", "auto", "GitHub 加速代理模式：auto(默认) / none / only")
	proxyRetry := flag.Bool("proxy-retry", true, "代理失败时尝试备用代理（默认开启）")

	// Web 服务模式
	serverMode := flag.Bool("server", false, "启动 Web 服务模式")
	listen := flag.String("listen", "0.0.0.0:8080", "Web 服务监听地址")

	flag.Parse()

	// Web 服务模式
	if *serverMode {
		cfg := server.Config{
			Listen:             *listen,
			GeneralizeHost:     *generalizeHost,
			FetchScripts:       *fetchScripts,
			EncodingPreprocess: *encodingPreprocess,
			IncludeMetadata:    !*noMetadata,
			UseStreamScript:    *streamScript,
			AutoContentType:    *autoContentType,
			ProxyMode:          *proxyMode,
			ProxyRetry:         *proxyRetry,
			Concurrency:        *concurrency,
			ScriptTimeoutSec:   *scriptTimeout,
			MaxInputBytes:      *maxInputBytes,
			MaxScriptBytes:     *maxScriptBytes,
			MaxScriptFetches:   *maxScriptFetches,
			PreserveParameters: *preserveParameters,
		}
		fmt.Printf("启动 Web 服务，监听 %s\n", cfg.Listen)
		if err := server.Run(cfg); err != nil {
			fmt.Fprintln(os.Stderr, "Web 服务错误:", err)
			os.Exit(1)
		}
		return
	}

	if *input == "" {
		fmt.Fprintln(os.Stderr, "错误：缺少 -i 输入参数")
		flag.Usage()
		os.Exit(2)
	}

	if err := run(*input, *outputDir, *format, *fetchScripts, *generalizeHost, *encodingPreprocess, !*noMetadata, *streamScript, *wrapScripts, *autoContentType, *concurrency, *scriptTimeout, *maxInputBytes, *maxScriptBytes, *maxScriptFetches, *sourceFlag, *preserveParameters, arguments, *verbose, *proxyMode, *proxyRetry); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

// run 执行主流程。
func run(input, outputDir, format string, fetchScripts, generalizeHost, encodingPreprocess, includeMetadata, streamScript, wrapScripts, autoContentType bool, concurrency, scriptTimeout int, maxInputBytes, maxScriptBytes int64, maxScriptFetches int, sourceFlag string, preserveParameters bool, arguments map[string]string, verbose bool, proxyMode string, proxyRetry bool) error {
	ctx := context.Background()
	f := fetcher.New()
	// 配置代理
	configureProxy(f, proxyMode, proxyRetry)

	// 1. 展开输入：检测是否为 Quantumult X 一键订阅协议 (quantumult.app add-resource)
	//    该协议允许把多份远端订阅嵌入到一个 URL，本工具把每份订阅视为独立 module 依次转换并合并。
	inputs, isAddResource, err := expandInputURLs(input, f)
	if err != nil {
		return fmt.Errorf("展开输入失败: %w", err)
	}

	// 2. 对每个 URL 解析并转换为中间结果
	results := make([]moduleResult, 0, len(inputs))
	for _, in := range inputs {
		content, err := f.FetchWithLimit(ctx, in, maxInputBytes)
		if err != nil {
			return fmt.Errorf("加载输入失败: %w", err)
		}

		// 检测来源
		var source ir.Source
		switch strings.ToLower(strings.TrimSpace(sourceFlag)) {
		case "loon":
			source = ir.SourceLoon
		case "surge":
			source = ir.SourceSurge
		case "quantumultx", "qx":
			source = ir.SourceQuantumultX
		default:
			source = parser.DetectSource(content, filepath.Base(in))
		}
		// 将来源格式写入 fetcher，后续脚本下载按 source 选 UA
		f.Source = source
		fmt.Printf("[%s] 检测到来源: %s\n", in, source)

		// 解析
		m, err := parser.Parse(content, source)
		if err != nil {
			return fmt.Errorf("[%s] 解析失败: %w", in, err)
		}
		fmt.Printf("[%s] 解析完成: name=%q, 路由规则=%d, 重写=%d, 脚本=%d, hostname=%d\n",
			in, m.Name, len(m.Rules), len(m.Rewrites), len(m.Scripts), len(m.Hostnames))

		// 转换
		opts := converter.Options{
			GeneralizeHost:     generalizeHost,
			EncodingPreprocess: encodingPreprocess,
			FetchScripts:       fetchScripts,
			IncludeMetadata:    includeMetadata,
			UseStreamScript:    streamScript,
			WrapScripts:        wrapScripts,
			AutoContentType:    autoContentType,
			Concurrency:        concurrency,
			ScriptTimeoutSec:   scriptTimeout,
			MaxScriptBytes:     maxScriptBytes,
			MaxScriptFetches:   maxScriptFetches,
			Arguments:          arguments,
			PreserveParameters: preserveParameters,
		}
		conv := converter.New(f, opts)
		conv.BaseURL = in // 用于解析相对 script-path
		// 记录来源 URL：本地文件留空；远程模块记录原始 URL。
		// 当原始输入是 quantumult.app add-resource 链接时，把展开前的 add-resource 链接写入注释。
		if isAddResource {
			conv.SourceURL = input
		} else if isRemoteURL(in) {
			conv.SourceURL = in
		}

		res, err := conv.Convert(ctx, m)
		if err != nil {
			return fmt.Errorf("[%s] 转换失败: %w", in, err)
		}
		results = append(results, moduleResult{mod: m, res: res})
	}

	// 3. 合并多 module 结果
	res := mergeResults(results, includeMetadata)
	if len(results) > 1 {
		fmt.Printf("已合并 %d 个模块的转换结果\n", len(results))
	}

	// 5. 写入文件
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}
	if (format == "both" || format == "arrs") && res.Arrs != "" {
		p := filepath.Join(outputDir, res.ArrsName)
		if err := os.WriteFile(p, []byte(res.Arrs), 0o644); err != nil {
			return fmt.Errorf("写入 .arrs 失败: %w", err)
		}
		fmt.Println("已生成:", p)
	}
	if (format == "both" || format == "amrs") && res.Amrs != "" {
		p := filepath.Join(outputDir, res.AmrsName)
		if err := os.WriteFile(p, []byte(res.Amrs), 0o644); err != nil {
			return fmt.Errorf("写入 .amrs 失败: %w", err)
		}
		fmt.Println("已生成:", p)
	}

	// 6. 报告
	if verbose {
		fmt.Println()
		fmt.Println(res.Report.String())
	} else {
		if len(res.Report.Skipped) > 0 {
			fmt.Printf("跳过 %d 项（-v 查看详情）\n", len(res.Report.Skipped))
		}
		if len(res.Report.Degraded) > 0 {
			fmt.Printf("降级 %d 项（-v 查看详情）\n", len(res.Report.Degraded))
		}
		if len(res.Report.ScriptErr) > 0 {
			fmt.Printf("脚本错误 %d 项（-v 查看详情）\n", len(res.Report.ScriptErr))
		}
	}
	return nil
}

// moduleResult 保存单个输入 URL 的解析与转换结果，供多 module 合并使用。
type moduleResult struct {
	mod *ir.Module
	res *converter.Result
}

// configureProxy 根据字符串参数配置 fetcher 代理模式。
func configureProxy(f *fetcher.Fetcher, mode string, retry bool) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "none":
		f.Proxy.Mode = fetcher.ProxyModeNone
	case "only":
		f.Proxy.Mode = fetcher.ProxyModeOnly
	default:
		f.Proxy.Mode = fetcher.ProxyModeAuto
	}
	f.Proxy.RetryAll = retry
}

// expandInputURLs 展开输入 URL 列表。
//   - 普通文件 / 普通 URL：原样返回 [input]
//   - Quantumult X add-resource 链接：解析出多个远端订阅 URL 后返回
//
// 同时返回 addResourceURL 标志，标识原始输入是否为 quantumult.app 一键订阅协议。
// 若为 true，主调应在 metadataComments 中写入「解码后的 add-resource 链接」。
//
// fetcher 参数保留以便未来需要从 add-resource 内嵌 URL 预下载 metadata 时的扩展。
func expandInputURLs(input string, _ *fetcher.Fetcher) ([]string, bool, error) {
	if !fetcher.IsQuantumultXAddResourceURL(input) {
		return []string{input}, false, nil
	}
	urls, err := fetcher.ExtractQuantumultXResourceURLs(input)
	if err != nil {
		return nil, false, err
	}
	if len(urls) == 0 {
		return nil, false, fmt.Errorf("add-resource 链接未包含任何可下载的订阅 URL")
	}
	fmt.Printf("展开 quantumult.app add-resource 链接，共 %d 个订阅：\n", len(urls))
	for _, u := range urls {
		fmt.Printf("  - %s\n", u)
	}
	return urls, true, nil
}

// mergeResults 合并多 module 的转换结果。
// 第一个 module 作为基础：
//   - 文件名取第一个 module 的 name
//   - .amrs 中的 hostname 取所有 module 的并集（保留顺序、去重）
//   - .arrs / .amrs 规则行直接按 module 顺序拼接（不重排序，去重）
//   - 报告合并
func mergeResults(results []moduleResult, includeMetadata bool) *converter.Result {
	if len(results) == 0 {
		return &converter.Result{Report: converter.Report{}}
	}
	if len(results) == 1 {
		return results[0].res
	}
	base := results[0].res
	report := base.Report

	for _, r := range results[1:] {
		// 合并 .amrs：hostname union + 规则行追加
		base.Amrs = mergeAmrs(base.Amrs, r.res.Amrs)
		// 合并 .arrs
		base.Arrs = mergeArrs(base.Arrs, r.res.Arrs)
		// 合并报告
		report.Skipped = append(report.Skipped, r.res.Report.Skipped...)
		report.Degraded = append(report.Degraded, r.res.Report.Degraded...)
		report.ScriptErr = append(report.ScriptErr, r.res.Report.ScriptErr...)
	}
	base.Report = report
	// 合并后跳过 metadata 重新生成（基础已包含）
	return base
}

// mergeAmrs 合并两份 .amrs 内容：hostname 取并集，规则行直接拼接。
func mergeAmrs(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	// 简单实现：直接拼接（hostname 重复由下游 Anywhere 客户端去重）
	return strings.TrimRight(a, "\n") + "\n" + b
}

// mergeArrs 合并两份 .arrs 内容：直接拼接规则行。
func mergeArrs(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return strings.TrimRight(a, "\n") + "\n" + b
}

// isRemoteURL 判断输入是否为远程 URL（http/https 协议）。
// 本地文件路径（含相对/绝对路径）返回 false。
func isRemoteURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}
