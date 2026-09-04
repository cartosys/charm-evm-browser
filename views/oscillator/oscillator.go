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

// Render renders the McClellan Oscillator page: a "warming up" state while
// fewer than DaysNeeded daily closes have been collected, otherwise the
// current reading, a sparkline of recent values, and a short recent-values
// table.
func Render(width, height int, backfillActive bool, days []string, values []float64, statusErr string) (string, Geometry) {
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
		const maxSpark = 60
		if len(sparkWindow) > maxSpark {
			sparkWindow = sparkWindow[len(sparkWindow)-maxSpark:]
			sparkDays = sparkDays[len(sparkDays)-maxSpark:]
		}
		if len(sparkWindow) > 0 {
			parts = append(parts, "", lipgloss.NewStyle().Align(lipgloss.Center).Width(containerWidth).Foreground(styles.CAccent2).Render(sparkline(sparkWindow)))
			parts = append(parts, lipgloss.NewStyle().Align(lipgloss.Center).Width(containerWidth).Foreground(styles.CMuted).
				Render(sparkDays[0]+"  →  "+sparkDays[len(sparkDays)-1]))
		}

		const tableRows = 10
		start := len(values) - tableRows
		if start < 0 {
			start = 0
		}
		parts = append(parts, "")
		header := lipgloss.NewStyle().Foreground(styles.CMuted).Bold(true).Render(fmt.Sprintf("%-12s  %s", "Day", "Oscillator"))
		parts = append(parts, lipgloss.NewStyle().Align(lipgloss.Center).Width(containerWidth).Render(header))
		for i := len(values) - 1; i >= start; i-- {
			var valText string
			if math.IsNaN(values[i]) {
				valText = "—"
			} else {
				valText = fmt.Sprintf("%+.2f", values[i])
			}
			row := fmt.Sprintf("%-12s  %s", days[i], valText)
			parts = append(parts, lipgloss.NewStyle().Align(lipgloss.Center).Width(containerWidth).Foreground(styles.CText).Render(row))
		}
	}

	parts = append(parts, "", lipgloss.NewStyle().Foreground(styles.CMuted).Align(lipgloss.Center).Width(containerWidth).
		Render("A cached pool can go stale if liquidity migrates — press "+styles.Key("r")+" to re-resolve."))

	content := lipgloss.JoinVertical(lipgloss.Center, parts...)
	rendered := lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(content)
	return rendered, Geometry{}
}
