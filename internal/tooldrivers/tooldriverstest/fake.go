// Package tooldriverstest stands a scripted extension driver in for the ones
// a build would supply.
package tooldriverstest

import (
	"context"
	"testing"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/tooldrivers"
)

// Driver answers each capability with its func, or errors.ErrUnsupported
// where the func is nil.
type Driver struct {
	extension.UnsupportedToolDriver
	Name       string
	Register   func(extension.MCPRequest) (extension.MCPLaunch, error)
	Capture    func(extension.CaptureRequest) (string, error)
	File       func(id string) (string, error)
	Transcript func(extension.TranscriptRequest) (extension.Transcript, error)
}

func (d *Driver) Style() string { return d.Name }

func (d *Driver) RegisterMCP(ctx context.Context, req extension.MCPRequest) (extension.MCPLaunch, error) {
	if d.Register == nil {
		return d.UnsupportedToolDriver.RegisterMCP(ctx, req)
	}
	return d.Register(req)
}

func (d *Driver) CaptureSession(ctx context.Context, req extension.CaptureRequest) (string, error) {
	if d.Capture == nil {
		return d.UnsupportedToolDriver.CaptureSession(ctx, req)
	}
	return d.Capture(req)
}

func (d *Driver) SessionFile(ctx context.Context, id string) (string, error) {
	if d.File == nil {
		return d.UnsupportedToolDriver.SessionFile(ctx, id)
	}
	return d.File(id)
}

func (d *Driver) MigrateTranscript(ctx context.Context, req extension.TranscriptRequest) (extension.Transcript, error) {
	if d.Transcript == nil {
		return d.UnsupportedToolDriver.MigrateTranscript(ctx, req)
	}
	return d.Transcript(req)
}

// Install makes drivers the build's for the rest of the test.
func Install(t testing.TB, drivers ...*Driver) {
	t.Helper()
	byStyle := map[string]extension.ToolDriver{}
	for _, d := range drivers {
		byStyle[d.Name] = d
	}
	t.Cleanup(tooldrivers.Use(func() (map[string]extension.ToolDriver, error) { return byStyle, nil }))
}
