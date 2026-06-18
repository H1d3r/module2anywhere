// Command loon2anywhere 将 Loon .plugin / Surge .sgmodule 模块转换为 Anywhere .arrs / .amrs 规则集。
//
// 用法：
//
//	loon2anywhere -i <input.plugin|input.sgmodule|URL> -o <output-dir> [options]
//
// 支持本地文件与远程 URL 输入。远程模块会先下载再解析；脚本 script-path 也会被下载、
// 改写为 Anywhere API 并 base64 编码后嵌入 .amrs。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Loon2Anywhere/loon2anywhere/converter"
	"github.com/Loon2Anywhere/loon2anywhere/fetcher"
	"github.com/Loon2Anywhere/loon2anywhere/ir"
	"github.com/Loon2Anywhere/loon2anywhere/parser"
)

func main() {
	input := flag.String("i", "", "输入文件路径或远程 URL（Loon .plugin / Surge .sgmodule）")
	outputDir := flag.String("o", "./out", "输出目录（生成 .arrs 和 .amrs）")
	format := flag.String("format", "both", "输出格式：both / arrs / amrs")
	fetchScripts := flag.Bool("fetch-scripts", true, "远程下载脚本并改写（默认开启）")
	generalizeHost := flag.Bool("generalize-host", true, "URL pattern 主机泛化为 [^/]+（默认开启）")
	encodingPreprocess := flag.Bool("encoding-preprocess", true, "为 body 处理规则自动添加 accept-encoding 预处理对（默认开启）")
	noMetadata := flag.Bool("no-metadata", false, "不在输出文件头部写入元数据注释")
	sourceFlag := flag.String("source", "", "强制指定来源：loon / surge（留空自动检测）")
	verbose := flag.Bool("v", false, "输出详细转换报告")
	flag.Parse()

	if *input == "" {
		fmt.Fprintln(os.Stderr, "错误：缺少 -i 输入参数")
		flag.Usage()
		os.Exit(2)
	}

	if err := run(*input, *outputDir, *format, *fetchScripts, *generalizeHost, *encodingPreprocess, !*noMetadata, *sourceFlag, *verbose); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

// run 执行主流程。
func run(input, outputDir, format string, fetchScripts, generalizeHost, encodingPreprocess, includeMetadata bool, sourceFlag string, verbose bool) error {
	ctx := context.Background()
	f := fetcher.New()

	// 1. 加载模块内容
	content, err := f.Fetch(ctx, input)
	if err != nil {
		return fmt.Errorf("加载输入失败: %w", err)
	}

	// 2. 检测来源
	var source ir.Source
	switch strings.ToLower(strings.TrimSpace(sourceFlag)) {
	case "loon":
		source = ir.SourceLoon
	case "surge":
		source = ir.SourceSurge
	default:
		source = parser.DetectSource(content, filepath.Base(input))
	}
	fmt.Printf("检测到来源: %s\n", source)

	// 3. 解析
	m, err := parser.Parse(content, source)
	if err != nil {
		return fmt.Errorf("解析失败: %w", err)
	}
	fmt.Printf("解析完成: name=%q, 路由规则=%d, 重写=%d, 脚本=%d, hostname=%d\n",
		m.Name, len(m.Rules), len(m.Rewrites), len(m.Scripts), len(m.Hostnames))

	// 4. 转换
	opts := converter.Options{
		GeneralizeHost:     generalizeHost,
		EncodingPreprocess: encodingPreprocess,
		FetchScripts:       fetchScripts,
		IncludeMetadata:    includeMetadata,
	}
	conv := converter.New(f, opts)
	conv.BaseURL = input // 用于解析相对 script-path

	res, err := conv.Convert(ctx, m)
	if err != nil {
		return fmt.Errorf("转换失败: %w", err)
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
