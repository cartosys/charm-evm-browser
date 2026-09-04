package main

import (
	"charm-wallet-tui/config"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *model) handleDappsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m, m.navigateTo(config.PageWallets)

	case "enter":
		if m.selectedDappIdx >= 0 && m.selectedDappIdx < len(m.dapps) {
			switch m.dapps[m.selectedDappIdx].Name {
			case "Terra Nullius":
				return m, m.navigateTo(config.PageTerraNullius)
			case "McClellan Oscillator":
				return m, m.navigateTo(config.PageOscillator)
			}
		}
		return m, m.navigateTo(config.PageUniswap)

	case "tab", "down", "right":
		if len(m.dapps) > 0 {
			m.selectedDappIdx = (m.selectedDappIdx + 1) % len(m.dapps)
		}
		return m, nil

	case "shift+tab", "up", "left":
		if len(m.dapps) > 0 {
			m.selectedDappIdx--
			if m.selectedDappIdx < 0 {
				m.selectedDappIdx = len(m.dapps) - 1
			}
		}
		return m, nil
	}
	return m, nil
}
