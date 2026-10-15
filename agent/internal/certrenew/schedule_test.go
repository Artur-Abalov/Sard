// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certrenew_test

import (
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/certrenew"
)

// docs/specs/agent/agent-certificate-renewal.feature, P17 and P19.

var t0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// Scenario: Порог считается от NotBefore по часам агента
func TestThresholdIsTwoThirdsOfTheLifetimeFromNotBefore(t *testing.T) {
	t.Parallel()
	rows := []struct {
		life time.Duration
		want time.Duration
	}{
		{10 * time.Minute, 6*time.Minute + 40*time.Second},
		{365 * 24 * time.Hour, 243*24*time.Hour + 8*time.Hour},
		{30 * 24 * time.Hour, 20 * 24 * time.Hour},
	}
	for _, row := range rows {
		if got := certrenew.Threshold(t0, t0.Add(row.life)); !got.Equal(t0.Add(row.want)) {
			t.Errorf("Threshold(%v) = %v, want %v", row.life, got, t0.Add(row.want))
		}
	}
}

func TestThresholdOfAVeryLongLifetimeDoesNotOverflow(t *testing.T) {
	t.Parallel()
	if got := certrenew.Threshold(time.Time{}, t0); got.After(t0) || got.Before(time.Time{}) {
		t.Errorf("Threshold = %v", got)
	}
}

// Scenario: Пауза растёт от минуты до часа со случайным разбросом
func TestDelayGrowsFromAMinuteToAnHourWithJitter(t *testing.T) {
	t.Parallel()
	rows := []struct {
		n    int
		rnd  float64
		want time.Duration
	}{
		{1, 0, 30 * time.Second},
		{1, 0.999999, time.Minute},
		{2, 0.999999, 2 * time.Minute},
		{3, 0.999999, 4 * time.Minute},
		{6, 0.999999, 32 * time.Minute},
		{7, 0.999999, time.Hour},
		{9, 0, 30 * time.Minute},
		{1000, 0.999999, time.Hour},
	}
	for _, row := range rows {
		got := certrenew.Delay(row.n, func() float64 { return row.rnd })
		if diff := got - row.want; diff > time.Second || diff < -time.Second {
			t.Errorf("Delay(%d, %v) = %v, want %v", row.n, row.rnd, got, row.want)
		}
	}
}
