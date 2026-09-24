package extensionhost

import (
	"context"
	"slices"
	"sort"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/launch"
)

func (b *Board) Tools(ctx context.Context) ([]extension.ToolInfo, error) {
	return tools(ctx, b.configDir)
}

func (h *Host) Tools(ctx context.Context) ([]extension.ToolInfo, error) {
	return tools(ctx, h.configDir)
}

// tools reads the configured CLIs out of configDir's config.
func tools(ctx context.Context, configDir string) ([]extension.ToolInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cfg, err := config.LoadDir(configDir)
	if err != nil {
		return nil, err
	}
	names := cfg.ToolNames()
	sort.Strings(names)
	out := make([]extension.ToolInfo, 0, len(names))
	for _, name := range names {
		tool := cfg.Tools[name]
		out = append(out, extension.ToolInfo{
			Name:       name,
			Shell:      tool.Shell,
			TakesModel: launch.TakesModel(name, tool),
			Models:     slices.Clone(tool.Models),
			HookStatus: tool.StatusSource == hooks.StatusSourceClaude,
		})
	}
	return out, nil
}
