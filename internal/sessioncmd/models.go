package sessioncmd

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/tracing"
)

// modelsTimeout bounds the CLI we shell out to. Listing models is a local
// read for some CLIs and a network call for others, and a caller waiting on
// this is a caller not yet able to spawn anything.
const modelsTimeout = 30 * time.Second

// modelsCap bounds one tool's section. opencode alone lists 839 models here,
// which is not an answer a caller reads -- it is a wall that pushes the names
// they were looking for out of their own context. The cap keeps the shape of
// the answer visible and the count tells them a filter is what gets the rest.
const modelsCap = 40

// Models answers what create_session's model argument accepts for a tool,
// asking the CLI itself where it has a command to ask (ModelsCommand) and
// falling back to the names written into the config (Models).
//
// A caller with no way to find this out guesses, and a guessed model is not a
// wrong answer but a refused launch: the CLI exits on an unknown name and the
// session is dead before its first turn. Naming one tool answers for that
// tool; naming none lists every tool that can answer at all, which is the
// call worth making before choosing which CLI to spawn.
func Models(configDir, tool, filter string) (string, error) {
	cfg, err := config.LoadDir(configDir)
	if err != nil {
		return "", err
	}
	if tool != "" {
		spec, ok := cfg.Tools[tool]
		if !ok {
			return "", fmt.Errorf("unknown tool %q (configured: %s)", tool, strings.Join(toolNames(cfg), ", "))
		}
		return modelsFor(tool, spec, filter), nil
	}
	var b strings.Builder
	for _, name := range toolNames(cfg) {
		spec := cfg.Tools[name]
		if spec.ModelsCommand == "" && len(spec.Models) == 0 {
			continue
		}
		b.WriteString(modelsFor(name, spec, filter))
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		return "no configured tool can list its models; name a model the CLI documents", nil
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func toolNames(cfg config.Config) []string {
	names := make([]string, 0, len(cfg.Tools))
	for name := range cfg.Tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// modelsFor is one tool's section: a header naming where the answer came
// from, then the names. A tool that cannot answer says so in place rather
// than being dropped, so a caller reading one tool's section is never left
// wondering whether the empty output meant "none" or "not asked".
func modelsFor(name string, spec config.Tool, filter string) string {
	if !launch.TakesModel(name, spec) {
		return name + ": cannot be launched on a chosen model (no model flag)\n"
	}
	var names []string
	source := ""
	switch {
	case spec.ModelsCommand != "":
		out, err := runModelsCommand(spec.ModelsCommand)
		if err != nil {
			return fmt.Sprintf("%s: %q failed: %v\n", name, spec.ModelsCommand, err)
		}
		names, source = strings.Split(out, "\n"), fmt.Sprintf("from %q", spec.ModelsCommand)
	case len(spec.Models) > 0:
		names, source = spec.Models, "from config, may lag the CLI"
	default:
		return name + ": no way to list its models; name one the CLI documents\n"
	}
	names = matching(names, filter)
	if len(names) == 0 {
		if filter != "" {
			return fmt.Sprintf("%s: no model matching %q (%s)\n", name, filter, source)
		}
		return fmt.Sprintf("%s: listed no models (%s)\n", name, source)
	}
	shown, more := names, 0
	if len(shown) > modelsCap {
		shown, more = shown[:modelsCap], len(names)-modelsCap
	}
	section := fmt.Sprintf("%s (%d, %s):\n%s\n", name, len(names), source, strings.Join(shown, "\n"))
	if more > 0 {
		section += fmt.Sprintf("... and %d more; pass filter to narrow\n", more)
	}
	return section
}

// matching drops the blank lines a CLI's own output arrives with. An empty
// filter keeps every name, so the cap above is what stands between the caller
// and opencode's 839 lines.
func matching(names []string, filter string) []string {
	filter = strings.ToLower(strings.TrimSpace(filter))
	kept := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if filter == "" || strings.Contains(strings.ToLower(n), filter) {
			kept = append(kept, n)
		}
	}
	return kept
}

func runModelsCommand(command string) (names string, err error) {
	// A shell, and for some CLIs a network call behind it, with a caller
	// blocked on the answer before it can spawn anything. The command comes
	// from the config rather than from anyone's keyboard, so naming it on the
	// span is safe and is the only way to tell one tool's listing from
	// another's.
	defer start("sessioncmd.models", tracing.Attr{Key: "command", Value: command}).done(&err)
	ctx, cancel := context.WithTimeout(context.Background(), modelsTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sh", "-c", command).Output()
	if err != nil {
		// A killed command reports "signal: killed", which reads as a crash
		// rather than as the deadline this function set.
		if ctx.Err() != nil {
			return "", fmt.Errorf("no answer within %s", modelsTimeout)
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exit.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
