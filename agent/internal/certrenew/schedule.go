// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certrenew

import "time"

// Threshold is when the agent starts to renew: two thirds of the lifetime
// after NotBefore (П17).
func Threshold(notBefore, notAfter time.Time) time.Time {
	return notBefore.Add(notAfter.Sub(notBefore) / 3 * 2)
}

const (
	minDelay = time.Minute
	maxDelay = time.Hour
)

// Delay is the pause after the n-th failed attempt in a row (n from 1): a
// random time in [d/2, d] where d doubles from a minute up to an hour (П19).
func Delay(n int, rnd func() float64) time.Duration {
	d := maxDelay
	if n >= 1 && n <= 6 { // a minute · 2⁶ already exceeds the hour
		d = min(minDelay<<(n-1), maxDelay)
	}
	return time.Duration(float64(d) * (0.5 + rnd()/2))
}
