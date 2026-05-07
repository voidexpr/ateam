package cmd

import (
	"strings"
	"testing"
	"time"
)

func TestFormatBuildTime(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		ts       string
		wantSubs []string // substrings the result must contain
		wantRaw  bool     // when true, expect raw passthrough
	}{
		{
			name:     "hours when under 48h",
			ts:       "1778129999.5", // ~7h before now
			wantSubs: []string{"1778129999.5", "(", ") 7h ago"},
		},
		{
			name:     "days when at least 48h",
			ts:       "1777982400", // 2d before now
			wantSubs: []string{"1777982400", ") 2d ago"},
		},
		{
			name:    "raw passthrough on unparseable",
			ts:      "unknown",
			wantRaw: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatBuildTime(tt.ts, now)
			if tt.wantRaw {
				if got != tt.ts {
					t.Errorf("got %q, want raw %q", got, tt.ts)
				}
				return
			}
			for _, sub := range tt.wantSubs {
				if !strings.Contains(got, sub) {
					t.Errorf("FormatBuildTime(%q) = %q, missing %q", tt.ts, got, sub)
				}
			}
		})
	}
}
