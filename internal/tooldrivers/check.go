package tooldrivers

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/usestring/gate-inbox/internal/config"
)

// Known reports whether style is built in or answered by a driver.
func Known(style string) bool {
	if slices.Contains(Builtin, style) {
		return true
	}
	_, ok, err := Lookup(style)
	return ok && err == nil
}

// CheckTool refuses a tool block that names an mcp or session_store style
// nothing in this build implements. Such a block used to launch as if it had
// said "none": the session came up without the board's tools, and nothing
// said why.
func CheckTool(name string, tool config.Tool) error {
	var errs []error
	for _, field := range []struct{ key, style string }{{"mcp", tool.MCP}, {"session_store", tool.SessionStore}} {
		if field.style == "" || slices.Contains(Builtin, field.style) {
			continue
		}
		_, ok, err := Lookup(field.style)
		if err != nil {
			return fmt.Errorf("tool %s: loading the build's tool drivers: %w", name, err)
		}
		if ok {
			continue
		}
		errs = append(errs, fmt.Errorf("tool %s: %s = %q is not a style this build has (built in: %s; from extensions: %s)",
			name, field.key, field.style, strings.Join(Builtin, ", "), orNone(Styles())))
	}
	return errors.Join(errs...)
}

// CheckTools is CheckTool over every configured tool, in name order.
func CheckTools(tools map[string]config.Tool) error {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	var errs []error
	for _, name := range names {
		if err := CheckTool(name, tools[name]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func orNone(styles []string) string {
	if len(styles) == 0 {
		return "none"
	}
	return strings.Join(styles, ", ")
}
