package parser

import (
	"strings"

	"github.com/H1d3r/module2anywhere/ir"
)

// parseArgumentLine 解析 Loon/Surge [Argument] 单行定义。
func parseArgumentLine(line string) (ir.Argument, bool) {
	raw := strings.TrimSpace(line)
	idx := strings.Index(raw, "=")
	if idx <= 0 {
		return ir.Argument{}, false
	}
	name := strings.TrimSpace(raw[:idx])
	if name == "" || strings.ContainsAny(name, "{},") {
		return ir.Argument{}, false
	}
	fields := splitCSVFields(raw[idx+1:])
	if len(fields) == 0 {
		return ir.Argument{}, false
	}
	argType := strings.ToLower(strings.TrimSpace(fields[0]))
	knownType := isKnownArgumentType(argType)
	if !knownType {
		argType = "string"
	}

	defaultValue := ""
	if knownType {
		if len(fields) > 1 {
			defaultValue = normalizeArgumentDefault(fields[1], argType)
		}
	} else {
		defaultValue = normalizeArgumentDefault(fields[0], argType)
	}

	arg := ir.Argument{
		Key:          name,
		Value:        strings.TrimSpace(raw[idx+1:]),
		Type:         argType,
		DefaultValue: defaultValue,
		Raw:          raw,
	}

	optionStart := 0
	if knownType {
		optionStart = 1
	}
	var optionFields []string
	for _, field := range fields[optionStart:] {
		pairIdx := strings.Index(field, "=")
		if pairIdx > 0 {
			key := strings.ToLower(strings.TrimSpace(field[:pairIdx]))
			val := trimQuotes(strings.TrimSpace(field[pairIdx+1:]))
			switch key {
			case "tag":
				arg.Tag = val
			case "desc", "description":
				arg.Desc = val
			}
			continue
		}
		field = strings.TrimSpace(field)
		if field != "" {
			optionFields = append(optionFields, normalizeArgumentDefault(field, argType))
		}
	}

	switch argType {
	case "select":
		arg.Options = dedupArgumentOptions(optionFields)
	case "switch", "checkbox":
		arg.Options = dedupArgumentOptions(optionFields)
		if len(arg.Options) == 0 {
			arg.Options = []string{"true", "false"}
		} else if len(arg.Options) == 1 {
			if argumentStringEnabled(arg.Options[0]) {
				arg.Options = append(arg.Options, "false")
			} else {
				arg.Options = append(arg.Options, "true")
			}
		}
	}
	return arg, true
}

// parseMetadataArguments 解析 Surge/QX 常见 #!arguments 与 #!arguments-desc 元数据。
func parseMetadataArguments(rawArguments, rawDescriptions string) []ir.Argument {
	descriptions := parseMetadataArgumentDescriptions(rawDescriptions)
	fields := splitCSVFields(rawArguments)
	args := make([]ir.Argument, 0, len(fields))
	for _, field := range fields {
		idx := strings.Index(field, ":")
		if idx <= 0 {
			continue
		}
		name := trimQuotes(strings.TrimSpace(field[:idx]))
		if name == "" || strings.ContainsAny(name, "{}") {
			continue
		}
		defaultValue := trimQuotes(strings.TrimSpace(field[idx+1:]))
		argType := "string"
		if isBoolText(defaultValue) {
			argType = "switch"
			defaultValue = normalizeArgumentDefault(defaultValue, argType)
		}
		arg := ir.Argument{
			Key:          name,
			Value:        defaultValue,
			Type:         argType,
			DefaultValue: defaultValue,
			Tag:          name,
			Desc:         descriptions[name],
			Raw:          strings.TrimSpace(field),
		}
		if argType == "switch" {
			arg.Options = []string{"true", "false"}
		}
		args = append(args, arg)
	}
	return args
}

func parseMetadataArgumentDescriptions(raw string) map[string]string {
	out := make(map[string]string)
	text := strings.ReplaceAll(raw, `\n`, "\n")
	for _, line := range strings.Split(text, "\n") {
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		name := trimQuotes(strings.TrimSpace(line[:idx]))
		if name != "" {
			out[name] = strings.TrimSpace(line[idx+1:])
		}
	}
	return out
}

func isKnownArgumentType(argType string) bool {
	switch strings.ToLower(strings.TrimSpace(argType)) {
	case "switch", "input", "text", "string", "number", "select", "checkbox":
		return true
	default:
		return false
	}
}

func normalizeArgumentDefault(value, argType string) string {
	value = trimQuotes(strings.TrimSpace(value))
	switch strings.ToLower(strings.TrimSpace(argType)) {
	case "switch", "checkbox":
		if argumentStringEnabled(value) {
			return "true"
		}
		if isBoolText(value) {
			return "false"
		}
	}
	return value
}

func argumentStringEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

func isBoolText(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "false", "1", "0", "yes", "no", "on", "off":
		return true
	default:
		return false
	}
}

func dedupArgumentOptions(values []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func mergeArguments(groups ...[]ir.Argument) []ir.Argument {
	out := make([]ir.Argument, 0)
	positions := make(map[string]int)
	for _, group := range groups {
		for _, arg := range group {
			key := strings.TrimSpace(arg.Key)
			if key == "" {
				continue
			}
			if idx, ok := positions[key]; ok {
				out[idx] = arg
				continue
			}
			positions[key] = len(out)
			out = append(out, arg)
		}
	}
	return out
}
