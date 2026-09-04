package helpers

import "math"

// NetAdvances returns, for each day after the first, the count of basket
// tokens whose close rose that day minus the count whose close fell, across
// days[1:]. closesByToken values must be aligned 1:1 to days (same length,
// same index) — the caller is responsible for day-alignment/forward-fill
// across tokens with gaps. A token's close of math.NaN() at an index is
// treated as unknown and never counts as an advance or decline.
func NetAdvances(days []string, closesByToken map[string][]float64) []float64 {
	if len(days) < 2 {
		return nil
	}
	out := make([]float64, len(days)-1)
	for i := 1; i < len(days); i++ {
		var advances, declines int
		for _, closes := range closesByToken {
			if i >= len(closes) {
				continue
			}
			prev, cur := closes[i-1], closes[i]
			if math.IsNaN(prev) || math.IsNaN(cur) {
				continue
			}
			switch {
			case cur > prev:
				advances++
			case cur < prev:
				declines++
			}
		}
		out[i-1] = float64(advances - declines)
	}
	return out
}

// EMA computes an n-period exponential moving average, seeded by a simple
// average of the first n input values (the standard McClellan convention).
// Entries before the first n values are math.NaN().
func EMA(series []float64, n int) []float64 {
	out := make([]float64, len(series))
	for i := range out {
		out[i] = math.NaN()
	}
	if n <= 0 || len(series) < n {
		return out
	}
	var sum float64
	for i := 0; i < n; i++ {
		sum += series[i]
	}
	prev := sum / float64(n)
	out[n-1] = prev
	alpha := 2.0 / float64(n+1)
	for i := n; i < len(series); i++ {
		prev = (series[i]-prev)*alpha + prev
		out[i] = prev
	}
	return out
}

// Oscillator returns EMA19(netAdvances) - EMA39(netAdvances), NaN wherever
// either input EMA is still NaN (i.e. before the 39th data point).
func Oscillator(netAdvances []float64) []float64 {
	ema19 := EMA(netAdvances, 19)
	ema39 := EMA(netAdvances, 39)
	out := make([]float64, len(netAdvances))
	for i := range out {
		if math.IsNaN(ema19[i]) || math.IsNaN(ema39[i]) {
			out[i] = math.NaN()
			continue
		}
		out[i] = ema19[i] - ema39[i]
	}
	return out
}
