// Package filter holds signal filters for hand positions.
package filter

import "math"

// OneEuro is the One Euro Filter (Casiez, Roussel, Vogel 2012): a low-pass
// filter whose cutoff rises with the speed of the signal, so that a resting
// hand is smoothed strongly and a moving hand is followed with little lag.
type OneEuro struct {
	// MinCutoff is the cutoff frequency in Hz for a signal at rest.
	MinCutoff float64
	// Beta is how much the cutoff rises per unit of speed (signal units per second).
	Beta float64
	// DCutoff is the cutoff frequency in Hz for the speed estimate.
	DCutoff float64

	started bool
	x       float64
	dx      float64
}

func smoothing(cutoff, dt float64) float64 {
	r := 2 * math.Pi * cutoff * dt
	return r / (r + 1)
}

// Reset makes the next sample the new starting point.
func (f *OneEuro) Reset() { f.started = false }

// Filter takes a sample and the time in seconds since the previous one and
// returns the smoothed value.
func (f *OneEuro) Filter(x, dt float64) float64 {
	if !f.started || dt <= 0 {
		f.started, f.x, f.dx = true, x, 0
		return x
	}
	dCutoff := f.DCutoff
	if dCutoff <= 0 {
		dCutoff = 1
	}
	dx := (x - f.x) / dt
	f.dx += smoothing(dCutoff, dt) * (dx - f.dx)
	cutoff := f.MinCutoff + f.Beta*math.Abs(f.dx)
	if cutoff <= 0 {
		cutoff = 1e-3
	}
	f.x += smoothing(cutoff, dt) * (x - f.x)
	return f.x
}

// Speed returns the current smoothed speed estimate in signal units per second.
func (f *OneEuro) Speed() float64 { return f.dx }
