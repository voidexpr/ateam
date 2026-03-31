package display

import "fmt"

func FmtTokens(n int64) string {
	if n <= 0 {
		return ""
	}
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func FmtCost(cost float64) string {
	if cost <= 0 {
		return ""
	}
	return fmt.Sprintf("$%.2f", cost)
}
