package wallets

import (
	"charm-wallet-tui/config"
	"charm-wallet-tui/helpers"
	"charm-wallet-tui/styles"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ClickableArea represents a clickable region for mouse support
type ClickableArea struct {
	X, Y          int
	Width, Height int
	Address       string
}

// Nav returns the navigation bar for wallets view
func Nav(width int) string {
	left := strings.Join([]string{
		styles.Key("↑/↓") + " move",
		styles.Key("Enter") + " open",
		styles.Key("Space") + " activate",
		styles.Key("a") + " add",
		styles.Key("d") + " delete",
		styles.Key("h") + " home",
		styles.Key("s") + " settings",
		styles.Key("b") + " dApps",
		styles.Key("l") + " debug log",
		styles.Key("Esc") + " quit",
	}, "   ")

	return styles.NavStyle.Width(width).Render(left)
}

// RenderList renders the wallet list
func RenderList(wallets []config.WalletEntry, selectedIdx int) (string, []ClickableArea, int) {
	var listItems []string
	var clickableAreas []ClickableArea
	currentY := 9 // Starting Y position

	if len(wallets) == 0 {
		listItems = append(listItems, lipgloss.NewStyle().Foreground(styles.CMuted).Render("No wallets added yet. Press 'a' to add one."))
		return strings.Join(listItems, "\n\n"), clickableAreas, currentY
	}

	for i, wallet := range wallets {
		var itemStyle lipgloss.Style
		var marker string
		var fullAddr, shortAddr string

		if i == selectedIdx {
			marker = lipgloss.NewStyle().Foreground(styles.CAccent2).Bold(true).Render("▶ ")
			itemStyle = lipgloss.NewStyle().Foreground(styles.CAccent2).Bold(true)
			fullAddr = lipgloss.NewStyle().Foreground(styles.CText).Render(wallet.Address)
			shortAddr = helpers.ShortenAddr(wallet.Address)
		} else {
			marker = "  "
			itemStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#e1a2aa"))
			fullAddr = lipgloss.NewStyle().Foreground(lipgloss.Color("#ba3fd7")).Render(helpers.FadeString(wallet.Address, "#7D5AFC", "#FF87D7"))
			shortAddr = helpers.FadeString(helpers.ShortenAddr(wallet.Address), "#F25D94", "#EDFF82")
		}

		// Add name if present
		if wallet.Name != "" {
			shortAddr = wallet.Name + " - " + shortAddr
		}
		// Add active indicator
		if wallet.Active {
			shortAddr = "✓ " + shortAddr
		}
		listItems = append(listItems, marker+itemStyle.Render(shortAddr)+"\n  "+fullAddr)

		// Register clickable areas
		clickableAreas = append(clickableAreas, ClickableArea{
			X:       4,
			Y:       currentY,
			Width:   lipgloss.Width(shortAddr) + 2,
			Height:  1,
			Address: wallet.Address,
		})
		currentY++

		clickableAreas = append(clickableAreas, ClickableArea{
			X:       4,
			Y:       currentY,
			Width:   42,
			Height:  1,
			Address: wallet.Address,
		})
		currentY += 2
	}

	return strings.Join(listItems, "\n\n"), clickableAreas, currentY
}

// Render renders the full wallets view
func Render(wallets []config.WalletEntry, selectedIdx int, addError string) (string, []ClickableArea) {
	header := styles.TitleStyle.Render("Account List")
	subtitle := lipgloss.NewStyle().Foreground(styles.CMuted).Render("Browse accounts and addresses")

	listView, clickableAreas, _ := RenderList(wallets, selectedIdx)

	statusBar := lipgloss.NewStyle().Foreground(styles.CMuted).Render(
		fmt.Sprintf("%d wallets", len(wallets)),
	)

	content := header + "\n" + subtitle + "\n\n" + listView + "\n\n" + statusBar
	return content, clickableAreas
}
