package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"

	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runtime"
	"github.com/ateam/internal/secret"
	"github.com/spf13/cobra"
)

var (
	secretScope            string
	secretStorage          string
	secretSet              bool
	secretDelete           bool
	secretGet              bool
	secretValue            string
	secretPrint            bool
	secretSaveProjectScope bool
)

var secretCmd = &cobra.Command{
	Use:   "secret [VARNAME]",
	Short: "Manage secrets for agent authentication",
	Long: `View, set, or delete secrets used by ateam agents.

Without arguments, lists all required secrets and their status.
With a VARNAME, shows its status and offers to set it if missing.

Examples:
  ateam secret                                    # list all secrets
  ateam secret ANTHROPIC_API_KEY                  # check/set a specific secret
  ateam secret ANTHROPIC_API_KEY --set            # set (reads value from stdin)
  ateam secret ANTHROPIC_API_KEY --get            # print raw value (for scripting)
  ateam secret ANTHROPIC_API_KEY --delete
  ateam secret ANTHROPIC_API_KEY --scope global
  ateam secret ANTHROPIC_API_KEY --storage file
  ateam secret --print                            # print all as KEY=VALUE (raw, for piping)
  ateam secret --save-project-scope               # write all to .ateam/secrets.env`,
	Args: cobra.ArbitraryArgs,
	RunE: runSecret,
}

func init() {
	secretCmd.Flags().StringVar(&secretScope, "scope", secret.ScopeGlobal, "secret scope: global, org, or project")
	secretCmd.Flags().StringVar(&secretStorage, "storage", "", "storage backend: keychain or file (default: keychain on macOS, file otherwise)")
	secretCmd.Flags().BoolVar(&secretSet, "set", false, "set the secret (reads value from stdin)")
	secretCmd.Flags().BoolVar(&secretGet, "get", false, "print raw value to stdout (for scripting)")
	secretCmd.Flags().StringVar(&secretValue, "value", "", "secret value (alternative to stdin)")
	secretCmd.Flags().BoolVar(&secretDelete, "delete", false, "delete the secret")
	secretCmd.Flags().BoolVar(&secretPrint, "print", false, "print raw KEY=VALUE to stdout (for piping/sourcing)")
	secretCmd.Flags().BoolVar(&secretSaveProjectScope, "save-project-scope", false, "resolve from any source and write to .ateam/secrets.env")
}

func runSecret(cmd *cobra.Command, args []string) error {
	backend := resolveBackend()

	env, _ := root.Lookup("", "")
	resolver := secretResolver(env, backend)

	var projectDir, orgDir string
	if env != nil {
		projectDir = env.ProjectDir
		orgDir = env.OrgDir
	}

	if secretPrint {
		return printSecrets(resolver, projectDir, orgDir, args)
	}

	if secretSaveProjectScope {
		return saveProjectScope(resolver, projectDir, orgDir, args)
	}

	if len(args) == 0 {
		return listSecrets(resolver, backend, projectDir, orgDir)
	}

	if len(args) > 1 {
		return fmt.Errorf("multiple names only supported with --print or --save-project-scope")
	}

	name := args[0]

	if secretDelete {
		return deleteSecret(resolver, backend, name)
	}

	if secretGet {
		return getSecret(resolver, name)
	}

	if secretSet {
		return setSecret(resolver, backend, name)
	}

	// Default: show status, offer to set if missing.
	return showSecret(resolver, backend, name)
}

func resolveBackend() secret.Backend {
	if secretStorage != "" {
		return secret.Backend(secretStorage)
	}
	return secret.DefaultBackend()
}

func listSecrets(resolver *secret.Resolver, backend secret.Backend, projectDir, orgDir string) error {
	fmt.Printf("Storage: %s\n\n", backendLabel(backend))

	rtCfg, err := runtime.Load(projectDir, orgDir)
	if err != nil {
		rtCfg = &runtime.Config{}
	}

	names := secret.CollectRequiredEnvNames(rtCfg)
	sort.Strings(names)

	if len(names) == 0 {
		fmt.Println("No required secrets found in runtime configuration.")
		return nil
	}

	for _, name := range names {
		result := resolver.Resolve(name)
		if result.Found {
			fmt.Printf("  %-30s set (%s, %s)\n", name, result.Source, result.Backend)
		} else {
			fmt.Printf("  %-30s not set\n", name)
		}
	}
	return nil
}

func getSecret(resolver *secret.Resolver, name string) error {
	result := resolver.Resolve(name)
	if !result.Found {
		return fmt.Errorf("%s: not set", name)
	}
	fmt.Print(result.Value)
	return nil
}

func showSecret(resolver *secret.Resolver, backend secret.Backend, name string) error {
	fmt.Printf("Storage: %s\n\n", backendLabel(backend))

	result := resolver.Resolve(name)
	if result.Found {
		masked := maskEnvVar(result.Value)
		fmt.Printf("%s=%s (%s, %s, %d bytes)\n", name, masked, result.Source, result.Backend, len(result.Value))
		return nil
	}

	fmt.Printf("%s: not set\n", name)
	fmt.Printf("\nSet it with: ateam secret %s --set\n", name)

	// If interactive, offer to set now.
	if isTerminal() {
		fmt.Print("\nPaste value (or press Enter to skip): ")
		val, err := readLine()
		if err != nil || val == "" {
			return nil
		}
		return writeSecret(resolver, backend, name, val)
	}
	return nil
}

func setSecret(resolver *secret.Resolver, backend secret.Backend, name string) error {
	fmt.Printf("Storage: %s, scope: %s\n", backendLabel(backend), secretScope)

	val := secretValue
	if val == "" {
		if isTerminal() {
			fmt.Printf("Paste value for %s: ", name)
		}
		var err error
		val, err = readLine()
		if err != nil {
			return fmt.Errorf("reading value: %w", err)
		}
	}
	if val == "" {
		return fmt.Errorf("empty value")
	}

	return writeSecret(resolver, backend, name, val)
}

func deleteSecret(resolver *secret.Resolver, backend secret.Backend, name string) error {
	scope := resolver.ScopeForName(secretScope)

	if backend == secret.BackendKeychain {
		account := secret.KeychainAccount(scope.Name, scope.KeychainKey, name)
		if err := secret.KeychainDelete(account); err != nil {
			fmt.Printf("Keychain: %s not found in %s scope\n", name, scope.Name)
		} else {
			fmt.Printf("Deleted %s from keychain (%s)\n", name, scope.Name)
		}
		return nil
	}

	store := &secret.FileStore{Path: scope.EnvFile}
	if deleted, _ := store.Delete(name); deleted {
		fmt.Printf("Deleted %s from %s\n", name, scope.EnvFile)
	} else {
		fmt.Printf("%s not found in %s\n", name, scope.EnvFile)
	}
	return nil
}

func writeSecret(resolver *secret.Resolver, backend secret.Backend, name, value string) error {
	scope := resolver.ScopeForName(secretScope)

	if backend == secret.BackendKeychain {
		account := secret.KeychainAccount(scope.Name, scope.KeychainKey, name)
		if err := secret.KeychainSet(account, value); err != nil {
			return fmt.Errorf("keychain write failed: %w\nTry: ateam secret %s --storage file --set", err, name)
		}
		fmt.Printf("Saved %s to keychain (%s)\n", name, scope.Name)
		return nil
	}

	store := &secret.FileStore{Path: scope.EnvFile}
	if err := store.Set(name, value); err != nil {
		return fmt.Errorf("cannot write %s: %w", scope.EnvFile, err)
	}
	fmt.Printf("Saved %s to %s\n", name, scope.EnvFile)
	return nil
}

func readLine() (string, error) {
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text()), nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", nil
}

func collectSecretNames(resolver *secret.Resolver, projectDir, orgDir string, args []string) []string {
	if len(args) > 0 {
		return args
	}
	rtCfg, err := runtime.Load(projectDir, orgDir)
	if err != nil {
		return nil
	}
	names := secret.CollectRequiredEnvNames(rtCfg)
	sort.Strings(names)
	return names
}

func printSecrets(resolver *secret.Resolver, projectDir, orgDir string, args []string) error {
	names := collectSecretNames(resolver, projectDir, orgDir, args)
	for _, name := range names {
		result := resolver.Resolve(name)
		if result.Found {
			fmt.Printf("%s=%s\n", name, result.Value)
		}
	}
	return nil
}

func saveProjectScope(resolver *secret.Resolver, projectDir, orgDir string, args []string) error {
	if projectDir == "" {
		return fmt.Errorf("--save-project-scope requires a project context (.ateam/ directory)")
	}

	names := collectSecretNames(resolver, projectDir, orgDir, args)
	if len(names) == 0 {
		fmt.Println("No secrets to save.")
		return nil
	}

	envFile := filepath.Join(projectDir, "secrets.env")
	store := &secret.FileStore{Path: envFile}

	var added, changed, same, written int
	for _, name := range names {
		result := resolver.Resolve(name)
		if !result.Found {
			continue
		}

		existing, hasExisting, err := store.Get(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [error]   %s: %v\n", name, err)
			continue
		}
		if hasExisting && existing == result.Value {
			fmt.Printf("  [same]    %s\n", name)
			same++
			continue
		}

		if err := store.Set(name, result.Value); err != nil {
			fmt.Fprintf(os.Stderr, "  [error]   %s: %v\n", name, err)
			continue
		}

		if hasExisting {
			fmt.Printf("  [changed] %s\n", name)
			changed++
		} else {
			fmt.Printf("  [added]   %s\n", name)
			added++
		}
		written++
	}

	total := written + same
	if total > 0 {
		fmt.Printf("Wrote %d secret(s) to %s", total, envFile)
		if changed > 0 || added > 0 {
			fmt.Printf(" (%d added, %d changed, %d unchanged)", added, changed, same)
		}
		fmt.Println()
	} else {
		fmt.Println("No secrets found to save.")
	}
	return nil
}

func backendLabel(b secret.Backend) string {
	switch b {
	case secret.BackendKeychain:
		var name string
		switch goruntime.GOOS {
		case "darwin":
			name = "macOS Keychain"
		case "linux":
			name = "Secret Service (D-Bus)"
		case "windows":
			name = "Windows Credential Manager"
		default:
			name = "OS keychain"
		}
		if secret.DefaultBackend() == secret.BackendKeychain {
			return fmt.Sprintf("%s (default for %s)", name, goruntime.GOOS)
		}
		return name
	case secret.BackendFile:
		return "file (.env)"
	default:
		return string(b)
	}
}
