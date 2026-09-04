package main

import (
	"charm-wallet-tui/config"

	tea "github.com/charmbracelet/bubbletea"
)

// handleOscillatorKey handles key input on the McClellan Oscillator page.
func (m *model) handleOscillatorKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.oscillatorCancel != nil {
			m.oscillatorCancel()
			m.oscillatorCancel = nil
		}
		m.oscillatorBackfillActive = false
		return m, m.navigateTo(config.PageDappBrowser)

	case "r":
		// Manual re-resolve: clear cached pool refs (a V3 pool can go stale
		// if liquidity migrates fee tiers; a V4 pool_id likewise if a newer
		// pool supersedes it — see helpers/oscillator.go) and rescan.
		if m.oscillatorBackfillActive || m.eventStore == nil {
			return m, nil
		}
		for _, t := range m.oscillatorBasket() {
			_ = m.eventStore.DeleteOscillatorPoolRef(t.Address)
		}
		m.oscillatorDays = nil
		m.oscillatorSeries = nil
		m.oscillatorSeriesErr = ""
		return m, m.startOscillatorBackscan()
	}
	return m, nil
}
