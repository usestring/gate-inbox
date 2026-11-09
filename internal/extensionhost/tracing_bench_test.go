package extensionhost

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/usestring/gate-inbox/internal/tracing"
)

func benchExtensionSpan(b *testing.B, sample string) {
	b.Setenv(tracing.SampleEnv, sample)
	t, err := tracing.Start("file:" + filepath.Join(b.TempDir(), "spans.jsonl"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { t.Close() })
	tr := tracer{scope: "bench"}
	b.ReportAllocs()
	for b.Loop() {
		tr.Start("work", slog.String("tool", "Bash"), slog.Int("n", 3)).End(nil, slog.Bool("ok", true))
	}
}

// An extension's span on the ordinary, sampled-away path.
func BenchmarkExtensionSpanSampledAway(b *testing.B) { benchExtensionSpan(b, "1000000") }

// An extension's span that is kept and queued for export.
func BenchmarkExtensionSpanKept(b *testing.B) { benchExtensionSpan(b, "1") }
