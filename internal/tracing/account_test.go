package tracing

import (
	"testing"
	"time"
)

func TestSubscriptionAccountingIsNeverSampledAway(t *testing.T) {
	t.Setenv(SampleEnv, "1000000")
	now := time.Now()
	for _, name := range []string{"account.selection", "account.launch", "account.usage", "account.context"} {
		for range 100 {
			ok, attrs := keep([]Span{{Name: name, Start: now, End: now}})
			if !ok {
				t.Fatalf("dropped %s", name)
			}
			if attrs[1].Value != 1 {
				t.Fatalf("wrong sample weight: %v", attrs)
			}
		}
	}
}
