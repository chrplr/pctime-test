// Copyright (2026) Christophe Pallier <christophe@pallier.org>
// Licensed under the Apache License, Version 2.0 (see LICENSE.txt).

package main

import (
	"math"
	"slices"
)

// fit is a least-squares line through (x, y).
type fit struct {
	n             int
	slope, se     float64 // slope and its standard error
	residSD, span float64 // residual SD, in the unit of y; range of x
}

// lineFit fits y = a + b x. With fewer than 3 points it returns n and NaNs.
func lineFit(x, y []float64) fit {
	n := len(x)
	f := fit{n: n, slope: math.NaN(), se: math.NaN(), residSD: math.NaN()}
	if n < 3 {
		return f
	}
	var mx, my float64
	for i := range x {
		mx += x[i]
		my += y[i]
	}
	mx /= float64(n)
	my /= float64(n)
	var sxx, sxy float64
	for i := range x {
		sxx += (x[i] - mx) * (x[i] - mx)
		sxy += (x[i] - mx) * (y[i] - my)
	}
	if sxx == 0 {
		return f
	}
	f.slope = sxy / sxx
	var ssr float64
	for i := range x {
		r := y[i] - my - f.slope*(x[i]-mx)
		ssr += r * r
	}
	f.residSD = math.Sqrt(ssr / float64(n-2))
	f.se = f.residSD / math.Sqrt(sxx)
	f.span = slices.Max(x) - slices.Min(x)
	return f
}

// median of v, NaN if empty.
func median(v []float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	s := slices.Clone(v)
	slices.Sort(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}
