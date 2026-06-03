package container

import (
	"os"
	"strings"
)

// detectOSSandbox uses three cheap /proc reads to spot an outer Linux
// sandbox (bubblewrap, firejail, or similar). Order: strongest signal
// first.
//
//  1. User-namespace divergence — /proc/self/ns/user differs from
//     /proc/1/ns/user. Set by bwrap, firejail, Docker, Podman.
//  2. Seccomp filter active or NoNewPrivs flag set in
//     /proc/self/status. Default for bwrap and most sandbox wrappers.
//
// False-positive worth knowing: systemd-hardened user services can
// trip the Seccomp/NoNewPrivs check without an outer sandbox. Treating
// them as isolated is safe — the agent's inner sandbox may genuinely
// fail to nest under such hardening.
func detectOSSandbox() string {
	if selfNS, err := os.Readlink("/proc/self/ns/user"); err == nil {
		if pid1NS, err := os.Readlink("/proc/1/ns/user"); err == nil {
			if selfNS != "" && pid1NS != "" && selfNS != pid1NS {
				return "linux:userns-diverged"
			}
		}
	}

	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "Seccomp:"):
			v := strings.TrimSpace(strings.TrimPrefix(line, "Seccomp:"))
			if v != "" && v != "0" {
				return "linux:seccomp"
			}
		case strings.HasPrefix(line, "NoNewPrivs:"):
			v := strings.TrimSpace(strings.TrimPrefix(line, "NoNewPrivs:"))
			if v == "1" {
				return "linux:nnp"
			}
		}
	}
	return ""
}
