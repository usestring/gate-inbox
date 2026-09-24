package app

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/ui"
)

// startUI asks the build's UIProviders for their keys and badges and installs
// them on the model before the program runs. ctx is the board's: it is what
// every key's Run receives, and it ends when the board exits.
func startUI(ctx context.Context, registry *extension.Registry, model *ui.Model, send func(tea.Msg)) error {
	bridge := ui.NewExtensionBridge(registry.IDs())
	results, err := registry.StartUI(func(id string) extension.UIHost {
		return uiHost{id: id, bridge: bridge}
	})
	if err != nil {
		return err
	}
	uis := make([]ui.ExtensionUI, 0, len(results))
	for _, result := range results {
		if result.Err != nil {
			logging.Warn("extension added nothing to the board", "extension", result.ID,
				"version", result.Version, logging.Err(result.Err))
			continue
		}
		uis = append(uis, ui.ExtensionUI{Owner: result.ID, Keys: uiKeys(ctx, result.ID, result.UI.Keys)})
	}
	model.InstallExtensions(uis, bridge)
	bridge.Attach(send)
	return nil
}

func uiKeys(ctx context.Context, owner string, bindings []extension.KeyBinding) []ui.ExtensionKey {
	keys := make([]ui.ExtensionKey, 0, len(bindings))
	for _, binding := range bindings {
		if binding.Screen != "" && binding.Screen != extension.ScreenList {
			logging.Warn("extension key names a screen the board does not have",
				"extension", owner, "screen", binding.Screen, "action", binding.Action)
			continue
		}
		run := binding.Run
		keys = append(keys, ui.ExtensionKey{
			Action: binding.Action,
			Keys:   binding.Keys,
			Label:  binding.Label,
			Run: func(press ui.Press) error {
				if run == nil {
					return nil
				}
				return run(ctx, extension.Press{SessionID: press.SessionID, Group: press.Group})
			},
		})
	}
	return keys
}

// uiHost is one extension's handle on the board's rows and status bar.
type uiHost struct {
	id     string
	bridge *ui.ExtensionBridge
}

func (h uiHost) Decorate(sessionID string, badges ...extension.Badge) {
	out := make([]ui.Badge, 0, len(badges))
	for _, badge := range badges {
		out = append(out, ui.Badge{Text: badge.Text, Short: badge.Short, Tone: uiTone(badge.Tone)})
	}
	h.bridge.Decorate(h.id, sessionID, out)
}

func (h uiHost) Notify(text string) { h.bridge.Notify(h.id, text) }

func uiTone(tone extension.Tone) ui.Tone {
	switch tone {
	case extension.ToneAccent:
		return ui.ToneAccent
	case extension.ToneGood:
		return ui.ToneGood
	case extension.ToneWarn:
		return ui.ToneWarn
	case extension.ToneBad:
		return ui.ToneBad
	}
	return ui.ToneMuted
}
