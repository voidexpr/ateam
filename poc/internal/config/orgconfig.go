package config

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

const maxStateKeyLen = 240

// PathToStateKey converts a relative project path to a safe directory name.
// Rules: _ → __, / → _S, . → _D. Reversible via StateKeyToPath.
// Paths longer than 240 chars after encoding are truncated with a hash suffix.
func PathToStateKey(relPath string) string {
	if relPath == "" {
		return ""
	}

	var b strings.Builder
	for _, c := range relPath {
		switch c {
		case '_':
			b.WriteString("__")
		case '/':
			b.WriteString("_S")
		case '.':
			b.WriteString("_D")
		default:
			b.WriteRune(c)
		}
	}

	key := b.String()
	if len(key) > maxStateKeyLen {
		h := sha256.Sum256([]byte(relPath))
		suffix := fmt.Sprintf("_%x", h[:4])
		key = key[:maxStateKeyLen-len(suffix)] + suffix
	}
	return key
}

// StateKeyToPath reverses PathToStateKey.
// Truncated keys (with hash suffix) cannot be reversed.
func StateKeyToPath(key string) string {
	if key == "" {
		return ""
	}

	var b strings.Builder
	i := 0
	for i < len(key) {
		if key[i] == '_' && i+1 < len(key) {
			switch key[i+1] {
			case '_':
				b.WriteByte('_')
			case 'S':
				b.WriteByte('/')
			case 'D':
				b.WriteByte('.')
			default:
				b.WriteByte('_')
				b.WriteByte(key[i+1])
			}
			i += 2
		} else {
			b.WriteByte(key[i])
			i++
		}
	}
	return b.String()
}
