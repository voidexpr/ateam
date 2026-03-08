package config

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// OrgConfig represents orgconfig.toml in the .ateamorg directory.
type OrgConfig struct {
	Projects map[string]string `toml:"projects"` // UUID → relative path from org root
}

func LoadOrgConfig(orgDir string) (*OrgConfig, error) {
	path := filepath.Join(orgDir, "orgconfig.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &OrgConfig{Projects: make(map[string]string)}, nil
		}
		return nil, fmt.Errorf("cannot read orgconfig.toml: %w", err)
	}
	var cfg OrgConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("cannot parse orgconfig.toml: %w", err)
	}
	if cfg.Projects == nil {
		cfg.Projects = make(map[string]string)
	}
	return &cfg, nil
}

func SaveOrgConfig(orgDir string, cfg *OrgConfig) error {
	path := filepath.Join(orgDir, "orgconfig.toml")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("cannot create orgconfig.toml: %w", err)
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

func (c *OrgConfig) Register(uuid, relPath string) {
	c.Projects[uuid] = relPath
}

// GenerateUUID returns a new UUID v4 string.
func GenerateUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("cannot generate UUID: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
