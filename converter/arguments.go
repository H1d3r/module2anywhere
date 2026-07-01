package converter

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"

	"github.com/H1d3r/module2anywhere/ir"
)

type argumentState struct {
	values     map[string]string
	parameters []amrsParameter
}

type amrsParameter struct {
	Type         int
	DataType     int
	Name         string
	Label        string
	Description  string
	DefaultValue string
	Options      []string
}

var (
	argumentPlaceholderRE = regexp.MustCompile(`\{\{\{([^{}]+)\}\}\}|\{\{([^{}]+)\}\}|\{([A-Za-z_][A-Za-z0-9_-]*)\}`)
	enableArgumentRE      = regexp.MustCompile(`(?i)\benable\s*=\s*(?:\{([A-Za-z_][A-Za-z0-9_-]*)\}|([A-Za-z_][A-Za-z0-9_-]*|true|false|1|0|yes|no|on|off))`)
)

// applyArguments 解析模块参数并把默认值/覆盖值应用到规则字段。
func (c *Converter) applyArguments(m *ir.Module, report *Report) (*ir.Module, argumentState) {
	values := resolveArgumentValues(m.Arguments, c.Opts.Arguments)
	state := argumentState{values: values}
	if c.Opts.PreserveParameters {
		state.parameters = buildAMRSParameters(m.Arguments, values, report)
	}
	if len(values) == 0 {
		return m, state
	}

	out := *m
	out.Hostnames = substituteStringSlice(m.Hostnames, values)
	out.Rules = make([]ir.RoutingRule, 0, len(m.Rules))
	for _, r := range m.Rules {
		if !argumentRuleEnabled(r.Raw, values) {
			report.AddSkipped(fmt.Sprintf("参数 enable 关闭，已跳过: %s", r.Raw))
			continue
		}
		r.Type = substituteArguments(r.Type, values)
		r.Value = substituteArguments(r.Value, values)
		r.Action = substituteArguments(r.Action, values)
		r.Raw = substituteArguments(r.Raw, values)
		r.Options = substituteStringSlice(r.Options, values)
		out.Rules = append(out.Rules, r)
	}

	out.Rewrites = make([]ir.RewriteRule, 0, len(m.Rewrites))
	for _, r := range m.Rewrites {
		if !argumentRuleEnabled(r.Raw, values) {
			report.AddSkipped(fmt.Sprintf("参数 enable 关闭，已跳过: %s", r.Raw))
			continue
		}
		r.Pattern = substituteArguments(r.Pattern, values)
		r.Action = substituteArguments(r.Action, values)
		r.RawJS = substituteArguments(r.RawJS, values)
		r.Raw = substituteArguments(r.Raw, values)
		args := make(map[string]string, len(r.Args))
		for k, v := range r.Args {
			args[k] = substituteArguments(v, values)
		}
		r.Args = args
		out.Rewrites = append(out.Rewrites, r)
	}

	out.Scripts = make([]ir.ScriptRule, 0, len(m.Scripts))
	for _, s := range m.Scripts {
		if !argumentRuleEnabled(s.Raw, values) {
			report.AddSkipped(fmt.Sprintf("参数 enable 关闭，已跳过: %s", s.Raw))
			continue
		}
		s.Pattern = substituteArguments(s.Pattern, values)
		s.ScriptPath = substituteArguments(s.ScriptPath, values)
		s.Argument = substituteArguments(s.Argument, values)
		s.Tag = substituteArguments(s.Tag, values)
		s.Engine = substituteArguments(s.Engine, values)
		s.Raw = substituteArguments(s.Raw, values)
		out.Scripts = append(out.Scripts, s)
	}

	out.HeaderRWs = make([]ir.HeaderRule, 0, len(m.HeaderRWs))
	for _, h := range m.HeaderRWs {
		if !argumentRuleEnabled(h.Raw, values) {
			report.AddSkipped(fmt.Sprintf("参数 enable 关闭，已跳过: %s", h.Raw))
			continue
		}
		h.Pattern = substituteArguments(h.Pattern, values)
		h.Op = substituteArguments(h.Op, values)
		h.Name = substituteArguments(h.Name, values)
		h.Value = substituteArguments(h.Value, values)
		h.Raw = substituteArguments(h.Raw, values)
		out.HeaderRWs = append(out.HeaderRWs, h)
	}

	out.MapLocals = make([]ir.MapLocalRule, 0, len(m.MapLocals))
	for _, ml := range m.MapLocals {
		if !argumentRuleEnabled(ml.Raw, values) {
			report.AddSkipped(fmt.Sprintf("参数 enable 关闭，已跳过: %s", ml.Raw))
			continue
		}
		ml.Pattern = substituteArguments(ml.Pattern, values)
		ml.DataURL = substituteArguments(ml.DataURL, values)
		ml.Header = substituteArguments(ml.Header, values)
		ml.DataType = substituteArguments(ml.DataType, values)
		ml.StatusCode = substituteArguments(ml.StatusCode, values)
		ml.Raw = substituteArguments(ml.Raw, values)
		out.MapLocals = append(out.MapLocals, ml)
	}
	return &out, state
}

func resolveArgumentValues(args []ir.Argument, overrides map[string]string) map[string]string {
	out := make(map[string]string)
	for _, arg := range args {
		key := strings.TrimSpace(arg.Key)
		if key == "" {
			continue
		}
		value := arg.DefaultValue
		if value == "" {
			value = arg.Value
		}
		out[key] = normalizeArgumentValueForType(value, arg.Type)
	}
	for key, value := range overrides {
		if !validArgumentName(key) {
			continue
		}
		argType := ""
		for _, arg := range args {
			if arg.Key == key {
				argType = arg.Type
				break
			}
		}
		out[key] = normalizeArgumentValueForType(value, argType)
	}
	return out
}

func argumentRuleEnabled(raw string, values map[string]string) bool {
	match := enableArgumentRE.FindStringSubmatch(raw)
	if match == nil {
		return true
	}
	key := match[1]
	if key == "" {
		key = match[2]
	}
	if value, ok := values[key]; ok {
		return argumentEnabled(value)
	}
	return argumentEnabled(key)
}

func substituteArguments(value string, values map[string]string) string {
	if value == "" || len(values) == 0 {
		return value
	}
	return argumentPlaceholderRE.ReplaceAllStringFunc(value, func(match string) string {
		parts := argumentPlaceholderRE.FindStringSubmatch(match)
		if parts == nil {
			return match
		}
		name := strings.TrimSpace(firstNonEmpty(parts[1], parts[2], parts[3]))
		if value, ok := values[name]; ok {
			return value
		}
		return match
	})
}

func substituteStringSlice(values []string, args map[string]string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, substituteArguments(value, args))
	}
	return out
}

func buildAMRSParameters(args []ir.Argument, values map[string]string, report *Report) []amrsParameter {
	seen := make(map[string]bool)
	nameMap := make(map[string]string)
	params := make([]amrsParameter, 0, len(args))
	for idx, arg := range args {
		sourceName := strings.TrimSpace(arg.Key)
		if sourceName == "" || seen[sourceName] {
			continue
		}
		seen[sourceName] = true
		name := safeParameterName(sourceName, idx, nameMap)
		nameMap[sourceName] = name
		if name != sourceName {
			report.AddWarning(fmt.Sprintf("参数 %s 已映射为 Anywhere 参数名 %s", sourceName, name))
		}
		current := values[sourceName]
		label := arg.Tag
		if label == "" {
			label = sourceName
		}
		desc := arg.Desc
		if name != sourceName {
			if desc != "" {
				desc += "；"
			}
			desc += fmt.Sprintf("来自上游 %q 参数", sourceName)
		}
		param := amrsParameter{
			Type:         0,
			DataType:     0,
			Name:         name,
			Label:        label,
			Description:  desc,
			DefaultValue: current,
		}
		switch strings.ToLower(strings.TrimSpace(arg.Type)) {
		case "select":
			options := ensureParameterOptions(arg.Options, current)
			if len(options) > 0 {
				param.Type = 1
				param.Options = options
				if !containsString(options, current) {
					param.DefaultValue = options[0]
				}
			}
		case "switch", "checkbox":
			param.Type = 1
			param.DefaultValue = "false"
			if argumentEnabled(current) {
				param.DefaultValue = "true"
			}
			param.Options = []string{"true", "false"}
		}
		params = append(params, param)
	}
	return params
}

func normalizeArgumentValueForType(value, argType string) string {
	text := strings.TrimSpace(value)
	switch strings.ToLower(strings.TrimSpace(argType)) {
	case "switch", "checkbox":
		if argumentEnabled(text) {
			return "true"
		}
		if isFalseArgument(text) {
			return "false"
		}
	}
	return text
}

func argumentEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

func isFalseArgument(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "false", "0", "no", "off":
		return true
	default:
		return false
	}
}

func safeParameterName(sourceName string, index int, nameMap map[string]string) string {
	base := strings.ReplaceAll(strings.TrimSpace(sourceName), "-", "_")
	if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(base) {
		base = parameterNameHash(sourceName)
	}
	if base == "" {
		base = fmt.Sprintf("arg_%d", index+1)
	}
	if !regexp.MustCompile(`^[A-Za-z_]`).MatchString(base) {
		base = "arg_" + base
	}
	base = regexp.MustCompile(`[^A-Za-z0-9_]`).ReplaceAllString(base, "_")
	name := base
	used := make(map[string]bool)
	for _, value := range nameMap {
		used[value] = true
	}
	for suffix := 2; used[name]; suffix++ {
		name = fmt.Sprintf("%s_%d", base, suffix)
	}
	return name
}

func parameterNameHash(value string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(value))
	return fmt.Sprintf("ARG_%x", h.Sum32())
}

func ensureParameterOptions(values []string, defaultValue string) []string {
	out := make([]string, 0, len(values)+1)
	seen := make(map[string]bool)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	if defaultValue != "" && !seen[defaultValue] {
		out = append(out, defaultValue)
	}
	return out
}

func validArgumentName(name string) bool {
	return regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`).MatchString(name)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
