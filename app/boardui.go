package app

import (
	"cmp"
	"context"
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/notify"
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
		keys, filters := uiKeys(ctx, result.ID, result.UI.Keys), uiFilters(result.UI.Filters)
		applyAliases(result.ID, result.UI.Aliases, keys, filters)
		uis = append(uis, ui.ExtensionUI{Owner: result.ID, Keys: keys, Filters: filters})
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

// applyAliases hangs each alias on the key or filter it names. One naming
// neither is logged and dropped: an extension can carry over only what it
// declares itself.
func applyAliases(owner string, aliases extension.Aliases, keys []ui.ExtensionKey, filters []ui.ExtensionFilter) {
	for _, alias := range aliases.Settings {
		i := slices.IndexFunc(filters, func(f ui.ExtensionFilter) bool { return f.Action == alias.Filter })
		if i < 0 || alias.From == "" {
			logging.Warn("extension setting alias names no filter of its own", "extension", owner,
				"from", alias.From, "filter", alias.Filter)
			continue
		}
		filters[i].CarriedFrom = append(filters[i].CarriedFrom, alias.From)
	}
	for _, alias := range aliases.Actions {
		screen := alias.Screen
		if screen == "" {
			screen = extension.ScreenList
		}
		if i := slices.IndexFunc(keys, func(k ui.ExtensionKey) bool {
			return k.Action == alias.To && cmp.Or(k.Screen, extension.ScreenList) == screen
		}); i >= 0 {
			keys[i].Aliases = append(keys[i].Aliases, alias.From)
			continue
		}
		if i := slices.IndexFunc(filters, func(f ui.ExtensionFilter) bool { return f.Action == alias.To }); i >= 0 && screen == extension.ScreenList {
			filters[i].Aliases = append(filters[i].Aliases, alias.From)
			continue
		}
		logging.Warn("extension action alias names no action of its own", "extension", owner,
			"screen", screen, "from", alias.From, "to", alias.To)
	}
}

// uiHost is one extension's handle on the board's rows and status bar.
type uiHost struct {
	id     string
	bridge *ui.ExtensionBridge
}

func (h uiHost) Decorate(sessionID string, badges ...extension.Badge) {
	out := make([]ui.Badge, 0, len(badges))
	for _, badge := range badges {
		rungs := make([][]ui.Span, 0, len(badge.Rungs))
		for _, rung := range badge.Rungs {
			rungs = append(rungs, uiLine(rung))
		}
		out = append(out, ui.Badge{Rungs: rungs, Text: badge.Text, Short: badge.Short, Tone: uiTone(badge.Tone),
			AfterName: badge.Placement == extension.PlaceAfterName})
	}
	h.bridge.Decorate(h.id, sessionID, out)
}

func (h uiHost) ConfirmOpen(sessionID, warning string) {
	h.bridge.ConfirmOpen(h.id, sessionID, warning)
}

func (h uiHost) Notify(text string) { h.bridge.Notify(h.id, text) }

func (h uiHost) Alert(alert extension.Alert) {
	if !notify.Post(notify.Note{Subject: alert.Title, Body: alert.Body, Kind: alertKind(alert.Tone)}) {
		logging.Warn("extension alert dropped: too many in flight", "extension", h.id)
	}
}

func alertKind(tone extension.Tone) notify.Kind {
	switch tone {
	case extension.ToneWarn:
		return notify.Waiting
	case extension.ToneGood:
		return notify.Finished
	case extension.ToneBad:
		return notify.Errored
	}
	return notify.Plain
}

func (h uiHost) Group(sessionID string, header extension.Line) {
	h.bridge.Group(h.id, sessionID, uiLine(header))
}

func (h uiHost) Hide(sessionID string, hidden bool) { h.bridge.Hide(h.id, sessionID, hidden) }

func (h uiHost) Own(sessionID string, owned bool) { h.bridge.Own(h.id, sessionID, owned) }

func (h uiHost) Attention(sessionID string, attention extension.Attention) {
	h.bridge.Attention(h.id, sessionID, ui.Attention{NeedsPerson: attention.NeedsPerson, Rank: uiRank(attention.Rank)})
}

// uiRank is a rank as the board orders it. One it does not know leaves the
// status to decide.
func uiRank(rank extension.AttentionRank) ui.AttentionRank {
	switch rank {
	case extension.RankWaiting:
		return ui.AttentionWaiting
	case extension.RankBlocked:
		return ui.AttentionBlocked
	case extension.RankErrored:
		return ui.AttentionErrored
	case extension.RankFinished:
		return ui.AttentionFinished
	case extension.RankIdle:
		return ui.AttentionIdle
	}
	return ui.AttentionByStatus
}

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
		ReplacedBy: sess.ReplacedBy,
	}
}

func (h uiHost) Open(screen string, view extension.View) extension.ViewHandle {
	if form, ok := view.(extension.Form); ok {
		return h.bridge.Open(h.id, screen, uiForm{uiView{view}, form})
	}
	return h.bridge.Open(h.id, screen, uiView{view})
}

// uiForm is an extension's form as the board draws it.
type uiForm struct {
	uiView
	form extension.Form
}

func (f uiForm) Fields() []ui.ViewField {
	fields := f.form.Fields()
	out := make([]ui.ViewField, 0, len(fields))
	for _, field := range fields {
		out = append(out, ui.ViewField{
			ID: field.ID, Label: field.Label, Value: field.Value, Placeholder: field.Placeholder,
			Multiline: field.Multiline, Height: field.Height, Limit: field.Limit, Choices: field.Choices,
		})
	}
	return out
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
	return v.view.Key(extension.ViewKey{Action: key.Action, Key: key.Key, Text: key.Text,
		Field: key.Field, Values: key.Values})
}

// Closed tells an extension's view that implements Closer why the board
// closed it. The board's reasons are spelled as the extension package's.
func (v uiView) Closed(reason ui.CloseReason) {
	if closer, ok := v.view.(extension.Closer); ok {
		closer.Closed(extension.CloseReason(reason))
	}
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
