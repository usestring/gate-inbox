package app

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
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
		uis = append(uis, ui.ExtensionUI{Owner: result.ID, Keys: uiKeys(ctx, result.ID, result.UI.Keys),
			Filters: uiFilters(result.UI.Filters)})
	}
	model.InstallExtensions(uis, bridge)
	bridge.Attach(send)
	return nil
}

func uiKeys(ctx context.Context, owner string, bindings []extension.KeyBinding) []ui.ExtensionKey {
	keys := make([]ui.ExtensionKey, 0, len(bindings))
	for _, binding := range bindings {
		run := binding.Run
		keys = append(keys, ui.ExtensionKey{
			Screen: binding.Screen,
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

func (h uiHost) Group(sessionID string, header extension.Line) {
	h.bridge.Group(h.id, sessionID, uiLine(header))
}

func (h uiHost) Hide(sessionID string, hidden bool) { h.bridge.Hide(h.id, sessionID, hidden) }

func (h uiHost) Own(sessionID string, owned bool) { h.bridge.Own(h.id, sessionID, owned) }

func uiFilters(filters []extension.Filter) []ui.ExtensionFilter {
	out := make([]ui.ExtensionFilter, 0, len(filters))
	for _, filter := range filters {
		keep := filter.Keep
		listed := ui.ExtensionFilter{Action: filter.Action, Keys: filter.Keys, Label: filter.Label, Badge: filter.Badge}
		if keep != nil {
			listed.Keep = func(sess store.Session) bool { return keep(sessionInfo(sess)) }
		}
		out = append(out, listed)
	}
	return out
}

// sessionInfo is a row's session as an extension reads it.
func sessionInfo(sess store.Session) extension.SessionInfo {
	return extension.SessionInfo{
		ID:         sess.ID,
		Name:       sess.Name,
		Tool:       sess.Tool,
		Model:      sess.Model,
		Group:      sess.Group,
		Directory:  sess.Cwd,
		Status:     sess.Status,
		Running:    sess.Status != status.Dead && !sess.Archived,
		Archived:   sess.Archived,
		ParentID:   sess.ParentID,
		SpawnedBy:  store.SpawnerOf(sess),
		CreatedAt:  sess.CreatedAt,
		ArchivedAt: sess.ArchivedAt,
	}
}

func (h uiHost) Open(screen string, view extension.View) extension.ViewHandle {
	return h.bridge.Open(h.id, screen, uiView{view})
}

// uiView is an extension's view as the board draws it.
type uiView struct{ view extension.View }

func (v uiView) Title() string { return v.view.Title() }

func (v uiView) Render(width, height int) [][]ui.Span {
	lines := v.view.Render(width, height)
	out := make([][]ui.Span, 0, len(lines))
	for _, line := range lines {
		out = append(out, uiLine(line))
	}
	return out
}

func uiLine(line extension.Line) []ui.Span {
	row := make([]ui.Span, 0, len(line))
	for _, span := range line {
		row = append(row, ui.Span{Text: span.Text, Tone: uiTone(span.Tone), Bold: span.Bold})
	}
	return row
}

func (v uiView) Key(key ui.ViewKey) bool {
	return v.view.Key(extension.ViewKey{Action: key.Action, Key: key.Key, Text: key.Text})
}

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
