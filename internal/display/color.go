package display

// ANSI styles for terminal output.
const (
	StyleBold    = "\033[1m"
	StyleDim     = "\033[2m"
	StyleRed     = "\033[31m"
	StyleGreen   = "\033[32m"
	StyleYellow  = "\033[33m"
	StyleMagenta = "\033[35m"
	StyleCyan    = "\033[36m"
	styleReset   = "\033[0m"
)

// Paint wraps s in the given styles when on is true.
func Paint(on bool, s string, styles ...string) string {
	if !on {
		return s
	}
	out := ""
	for _, style := range styles {
		out += style
	}
	return out + s + styleReset
}
