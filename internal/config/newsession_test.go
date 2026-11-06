package config

import (
	"math"
	"testing"
)

func TestNewSessionMemoryThresholdValidation(t *testing.T) {
	for _, threshold := range []float64{-1, 101, math.NaN(), math.Inf(1)} {
		cfg := Config{NewSession: NewSession{MemoryAvailableBelow: threshold}}
		cfg.applyDefaults()
		if err := cfg.validate(); err == nil {
			t.Fatalf("accepted threshold %v", threshold)
		}
	}
	for _, threshold := range []float64{0, 15, 100} {
		cfg := Config{NewSession: NewSession{MemoryAvailableBelow: threshold}}
		cfg.applyDefaults()
		if err := cfg.validate(); err != nil {
			t.Fatal(err)
		}
	}
}
