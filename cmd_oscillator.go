package main

import (
	"fmt"
	"math"
	"math/big"
	"sort"

	"charm-wallet-tui/helpers"
	"charm-wallet-tui/rpc"
	"charm-wallet-tui/store"

	tea "github.com/charmbracelet/bubbletea"
)

// waitForOscillatorLine reads one progress line off the backscan channel
// started by store.IndexOscillatorBackfill, following the same
// channel-pump-then-re-arm idiom as cmd_indexer.go's waitFor* Cmd factories.
func waitForOscillatorLine(ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		line, ok := <-ch
		if !ok {
			return oscillatorBackscanDoneMsg{}
		}
		return oscillatorBackscanLineMsg{line: line}
	}
}

// loadOscillatorSeriesCmd recomputes the McClellan Oscillator from whatever
// swap history has been persisted so far — day-aligning each basket token's
// daily closes (forward-filling gaps, NaN before a token's first known
// close) before handing them to helpers.NetAdvances/Oscillator.
func loadOscillatorSeriesCmd(s *store.Store, tokens []rpc.WatchedToken) tea.Cmd {
	return func() tea.Msg {
		if s == nil {
			return oscillatorSeriesMsg{err: fmt.Errorf("event store unavailable")}
		}

		perToken := make(map[string][]store.DailyPrice)
		daySet := make(map[string]struct{})
		for _, t := range tokens {
			closes, err := s.OscillatorDailyCloses(t.Address)
			if err != nil || len(closes) == 0 {
				continue
			}
			perToken[t.Address.Hex()] = closes
			for _, c := range closes {
				daySet[c.Day] = struct{}{}
			}
		}
		if len(daySet) == 0 {
			return oscillatorSeriesMsg{}
		}

		days := make([]string, 0, len(daySet))
		for d := range daySet {
			days = append(days, d)
		}
		sort.Strings(days)

		aligned := make(map[string][]float64, len(perToken))
		for key, closes := range perToken {
			series := make([]float64, len(days))
			idx := 0
			last := math.NaN()
			for i, day := range days {
				for idx < len(closes) && closes[idx].Day == day {
					last = closes[idx].Close
					idx++
				}
				series[i] = last
			}
			aligned[key] = series
		}

		netAdvances := helpers.NetAdvances(days, aligned)
		oscillator := helpers.Oscillator(netAdvances)
		msg := oscillatorSeriesMsg{days: days[1:], values: oscillator}

		weth := helpers.UniswapAddressesForChain(big.NewInt(1)).WETH
		for _, t := range tokens {
			if t.Address != weth {
				continue
			}
			wethSeries, ok := aligned[t.Address.Hex()]
			if !ok {
				break
			}
			msg.ethCloses = wethSeries[1:]
			ethChange := make([]float64, len(wethSeries)-1)
			for i := 1; i < len(wethSeries); i++ {
				ethChange[i-1] = wethSeries[i] - wethSeries[i-1]
			}
			msg.ethChange = ethChange
			break
		}

		return msg
	}
}
