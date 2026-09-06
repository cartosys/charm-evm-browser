package oscillator

import (
	"fmt"
	"math"
	"strings"

	"charm-wallet-tui/helpers"
	"charm-wallet-tui/styles"

	"github.com/charmbracelet/lipgloss"
)

// DaysNeeded is the minimum number of daily closes required before
// EMA39 (and therefore the oscillator) has any value at all.
const DaysNeeded = 39

// sparkRamp maps a normalized 0..1 value to a block-character height.
var sparkRamp = []rune("▁▂▃▄▅▆▇█")

// sparkline renders values as a single line of block characters, scaled
// between their own min/max. NaN entries render as a blank space.
func sparkline(values []float64) string {
	minV, maxV := math.Inf(1), math.Inf(-1)
	for _, v := range values {
		if math.IsNaN(v) {
			continue
		}
		minV = math.Min(minV, v)
		maxV = math.Max(maxV, v)
	}
	var b strings.Builder
	for _, v := range values {
		if math.IsNaN(v) {
			b.WriteRune(' ')
			continue
		}
		if maxV == minV {
			b.WriteRune(sparkRamp[len(sparkRamp)/2])
			continue
		}
		norm := (v - minV) / (maxV - minV)
		idx := int(norm * float64(len(sparkRamp)-1))
		b.WriteRune(sparkRamp[idx])
	}
	return b.String()
}

// Nav returns the navigation bar for the McClellan Oscillator page.
func Nav(width int, backfillActive bool) string {
	var status string
	if backfillActive {
		status = lipgloss.NewStyle().Foreground(styles.CAccent2).Render("scanning…")
	} else {
		status = styles.Key("r") + " re-resolve"
	}
	left := strings.Join([]string{
		styles.Key("Esc") + " back",
		status,
	}, "   ")
	return styles.NavStyle.Width(width).Render(left)
}

// Geometry is unused today (this page is read-only, no click targets) —
// kept for consistency with the other page-view packages' Nav/Render+Geometry shape.
type Geometry struct{}

// chartRows is the combo chart's height — an odd count so there's a single
// center row to use as the shared zero baseline for both overlaid series.
const chartRows = 9

// chartWindowDays is how many of the most recent days the combo chart plots.
// chartColWidth is how many character-columns each day gets — widened from
// 1 as chartWindowDays shrank from a prior 60-day window, so the chart keeps
// the same total on-screen width (60 = chartWindowDays*chartColWidth) while
// each day reads as a wider, easier-to-tell-apart bar/marker.
const (
	chartWindowDays = 20
	chartColWidth   = 3
)

// comboChart renders values (the oscillator) as a marker line overlaid on
// ethChange (ETH's day-over-day price delta) rendered as bars, sharing one
// zero-baseline row. The two series are normalized independently by their
// own max absolute value in the window, so each fills the same chartRows
// amplitude on screen regardless of their differing absolute units —
// keeping them visually comparable rather than letting one dwarf the other.
// Each day is drawn colWidth characters wide. values and ethChange must be
// the same length (one entry per day).
func comboChart(values, ethChange []float64, colWidth int) string {
	n := len(values)
	if n == 0 {
		return ""
	}
	half := chartRows / 2

	maxAbs := func(series []float64) float64 {
		m := 0.0
		for _, v := range series {
			if math.IsNaN(v) {
				continue
			}
			if a := math.Abs(v); a > m {
				m = a
			}
		}
		return m
	}
	rowOffset := func(v, scale float64) (int, bool) {
		if math.IsNaN(v) || scale == 0 {
			return 0, false
		}
		r := int(math.Round(v / scale * float64(half)))
		if r > half {
			r = half
		}
		if r < -half {
			r = -half
		}
		return r, true
	}

	oscScale := maxAbs(values)
	changeScale := maxAbs(ethChange)
	const zeroRow = chartRows / 2

	grid := make([][]string, chartRows)
	for r := range grid {
		grid[r] = make([]string, n)
		for c := range grid[r] {
			grid[r][c] = " "
		}
	}

	baselineStyle := lipgloss.NewStyle().Foreground(styles.CMuted)
	posStyle := lipgloss.NewStyle().Foreground(styles.CAccent)
	negStyle := lipgloss.NewStyle().Foreground(styles.CError)
	oscStyle := lipgloss.NewStyle().Bold(true).Foreground(styles.CAccent2)

	for c := 0; c < n; c++ {
		grid[zeroRow][c] = baselineStyle.Render("─")
	}

	for c := 0; c < len(ethChange); c++ {
		r, ok := rowOffset(ethChange[c], changeScale)
		if !ok || r == 0 {
			continue
		}
		style := posStyle
		if ethChange[c] < 0 {
			style = negStyle
		}
		if r > 0 {
			for row := zeroRow - r; row <= zeroRow; row++ {
				grid[row][c] = style.Render("█")
			}
		} else {
			for row := zeroRow; row <= zeroRow-r; row++ {
				grid[row][c] = style.Render("█")
			}
		}
	}

	for c := 0; c < n; c++ {
		r, ok := rowOffset(values[c], oscScale)
		if !ok {
			continue
		}
		grid[zeroRow-r][c] = oscStyle.Render("●")
	}

	lines := make([]string, chartRows)
	for r := range grid {
		var b strings.Builder
		for c := 0; c < n; c++ {
			b.WriteString(strings.Repeat(grid[r][c], colWidth))
		}
		lines[r] = b.String()
	}
	return strings.Join(lines, "\n")
}

// Render renders the McClellan Oscillator page: a "warming up" state while
// fewer than DaysNeeded daily closes have been collected, otherwise the
// current reading, a chart of recent values (overlaid with ETH's daily price
// change when available), and a short recent-values table.
func Render(width, height int, backfillActive bool, days []string, values []float64, ethCloses []float64, ethChange []float64, statusErr string) (string, Geometry) {
	containerWidth := helpers.Min(80, width-4)

	titleStyle := lipgloss.NewStyle().
		Foreground(styles.CAccent2).
		Bold(true).
		Align(lipgloss.Center).
		Width(containerWidth)
	subtitleStyle := lipgloss.NewStyle().
		Foreground(styles.CMuted).
		Align(lipgloss.Center).
		Width(containerWidth)

	title := titleStyle.Render("📈  McClellan Oscillator")
	subtitle := subtitleStyle.Render("Mainnet watchlist basket · V2/V3/V4 swap history")

	var parts []string
	parts = append(parts, title, subtitle, "")

	if statusErr != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(styles.CError).Align(lipgloss.Center).Width(containerWidth).Render("Error: "+statusErr))
		content := lipgloss.JoinVertical(lipgloss.Center, parts...)
		return lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(content), Geometry{}
	}

	daysCollected := len(days)
	if backfillActive {
		parts = append(parts, lipgloss.NewStyle().Foreground(styles.CAccent2).Align(lipgloss.Center).Width(containerWidth).
			Render(fmt.Sprintf("Backscanning basket history… (%d days collected so far)", daysCollected)))
	} else if daysCollected < DaysNeeded {
		parts = append(parts, lipgloss.NewStyle().Foreground(styles.CWarn).Align(lipgloss.Center).Width(containerWidth).
			Render(fmt.Sprintf("Warming up — %d/%d days collected (need ≥%d for a valid EMA39 reading)", daysCollected, DaysNeeded, DaysNeeded)))
	}

	if daysCollected > 0 {
		latestIdx := -1
		for i := len(values) - 1; i >= 0; i-- {
			if !math.IsNaN(values[i]) {
				latestIdx = i
				break
			}
		}

		parts = append(parts, "")
		if latestIdx >= 0 {
			valStyle := lipgloss.NewStyle().Bold(true).Foreground(styles.CAccent)
			if values[latestIdx] < 0 {
				valStyle = valStyle.Foreground(styles.CError)
			}
			parts = append(parts, lipgloss.NewStyle().Align(lipgloss.Center).Width(containerWidth).Render(
				lipgloss.NewStyle().Foreground(styles.CMuted).Render("Current reading ("+days[latestIdx]+"): ")+
					valStyle.Render(fmt.Sprintf("%.2f", values[latestIdx])),
			))
		} else {
			parts = append(parts, lipgloss.NewStyle().Foreground(styles.CMuted).Align(lipgloss.Center).Width(containerWidth).
				Render("No valid reading yet"))
		}

		sparkWindow := values
		sparkDays := days
		ethChangeWindow := ethChange
		if len(sparkWindow) > chartWindowDays {
			start := len(sparkWindow) - chartWindowDays
			sparkWindow = sparkWindow[start:]
			sparkDays = sparkDays[start:]
			if len(ethChangeWindow) == len(values) {
				ethChangeWindow = ethChangeWindow[start:]
			}
		}
		hasChange := len(ethChangeWindow) == len(sparkWindow)
		if len(sparkWindow) > 0 {
			parts = append(parts, "")
			if hasChange {
				parts = append(parts, lipgloss.NewStyle().Align(lipgloss.Center).Width(containerWidth).Render(comboChart(sparkWindow, ethChangeWindow, chartColWidth)))
				legend := lipgloss.NewStyle().Bold(true).Foreground(styles.CAccent2).Render("●") + " Oscillator    " +
					lipgloss.NewStyle().Foreground(styles.CAccent).Render("█") + " ETH daily Δ"
				parts = append(parts, lipgloss.NewStyle().Align(lipgloss.Center).Width(containerWidth).Render(legend))
			} else {
				parts = append(parts, lipgloss.NewStyle().Align(lipgloss.Center).Width(containerWidth).Foreground(styles.CAccent2).Render(sparkline(sparkWindow)))
			}
			parts = append(parts, lipgloss.NewStyle().Align(lipgloss.Center).Width(containerWidth).Foreground(styles.CMuted).
				Render(sparkDays[0]+"  →  "+sparkDays[len(sparkDays)-1]))
		}

		hasEth := len(ethCloses) == len(values)
		const tableRows = 10
		start := len(values) - tableRows
		if start < 0 {
			start = 0
		}
		parts = append(parts, "")
		var headerText string
		if hasEth {
			headerText = fmt.Sprintf("%-12s  %-10s  %s", "Day", "Oscillator", "ETH Close")
		} else {
			headerText = fmt.Sprintf("%-12s  %s", "Day", "Oscillator")
		}
		header := lipgloss.NewStyle().Foreground(styles.CMuted).Bold(true).Render(headerText)
		parts = append(parts, lipgloss.NewStyle().Align(lipgloss.Center).Width(containerWidth).Render(header))
		for i := len(values) - 1; i >= start; i-- {
			var valText string
			if math.IsNaN(values[i]) {
				valText = "—"
			} else {
				valText = fmt.Sprintf("%+.2f", values[i])
			}
			var row string
			if hasEth {
				ethText := "—"
				if !math.IsNaN(ethCloses[i]) {
					ethText = fmt.Sprintf("$%.2f", ethCloses[i])
				}
				row = fmt.Sprintf("%-12s  %-10s  %s", days[i], valText, ethText)
			} else {
				row = fmt.Sprintf("%-12s  %s", days[i], valText)
			}
			parts = append(parts, lipgloss.NewStyle().Align(lipgloss.Center).Width(containerWidth).Foreground(styles.CText).Render(row))
		}
	}

	parts = append(parts, "", lipgloss.NewStyle().Foreground(styles.CMuted).Align(lipgloss.Center).Width(containerWidth).
		Render("A cached pool can go stale if liquidity migrates — press "+styles.Key("r")+" to re-resolve."))

	content := lipgloss.JoinVertical(lipgloss.Center, parts...)
	rendered := lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(content)
	return rendered, Geometry{}
}
