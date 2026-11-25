package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/openclaw/beam/internal/clean"
)

type options struct {
	values   map[string]string
	args     []string
	adapters []string
	keep     []int
	clean    clean.Options
}

var optionsFor = map[string]string{
	"capabilities": "json", "doctor": "json adapter profile-dir endpoint", "adapter": "in adapter profile-dir endpoint",
	"list": "from cursor limit json adapter profile-dir endpoint", "pack": "from session branch out omit redact json adapter profile-dir endpoint",
	"adapter test": "in adapter fixture session mode out state json profile-dir endpoint",
	"plan":         "in mode expect-digest json adapter",
	"review":       "keep omit expect-digest out json",
	"clean":        "protocol omit redact", "inspect": "json", "convert": "packet-version out json", "render": "out",
	"open":    "in message mode operation-id request-id expect-digest json adapter out state",
	"push":    "from session branch to mode message operation-id request-id expect-digest dry-run omit redact out json adapter state profile-dir endpoint",
	"resolve": "in handle json adapter", "send": "in handle request-id message json adapter state",
	"lookup": "in operation-id json adapter state",
}

func parseOptions(cmd string, args []string) (options, error) {
	o := options{values: map[string]string{}}
	allowed := make(map[string]bool)
	for _, f := range strings.Fields(optionsFor[cmd]) {
		allowed[f] = true
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			o.args = append(o.args, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "--") {
			o.args = append(o.args, arg)
			continue
		}
		name, value, equals := strings.Cut(arg[2:], "=")
		if !allowed[name] {
			return o, fmt.Errorf("unknown --%s for %s", name, cmd)
		}
		if name == "json" || name == "dry-run" {
			if equals {
				return o, fmt.Errorf("--%s takes no value", name)
			}
			value = "true"
		} else if !equals {
			if i+1 == len(args) || strings.HasPrefix(args[i+1], "--") {
				return o, fmt.Errorf("--%s requires a value", name)
			}
			i++
			value = args[i]
		}
		if value == "" {
			return o, fmt.Errorf("--%s requires a value", name)
		}
		switch name {
		case "adapter":
			o.adapters = append(o.adapters, value)
		case "keep", "omit":
			n, err := strconv.Atoi(value)
			if err != nil || n <= 0 {
				return o, fmt.Errorf("--%s takes a 1-based item number", name)
			}
			if name == "keep" {
				o.keep = append(o.keep, n)
			} else {
				o.clean.OmitItems = append(o.clean.OmitItems, n)
			}
		case "redact":
			o.clean.RedactLiterals = append(o.clean.RedactLiterals, value)
		default:
			if _, ok := o.values[name]; ok {
				return o, fmt.Errorf("duplicate --%s", name)
			}
			o.values[name] = value
		}
	}
	return o, nil
}

func (o options) require(keys ...string) error {
	for _, k := range keys {
		if o.values[k] == "" {
			return fmt.Errorf("--%s is required", k)
		}
	}
	return nil
}
