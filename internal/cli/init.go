package cli

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/spf13/cobra"

	"github.com/cliwright/smith/internal/config"
	"github.com/cliwright/smith/internal/lock"
)

// templates holds the embedded files smith init writes verbatim (after
// template substitution).
//
//go:embed templates
var templates embed.FS

const (
	registryName    = "cliwright"
	typesGitURL     = "https://github.com/cliwright/smith-project-types"
	templatesGitURL = "https://github.com/cliwright/smith-project-templates"
	cacheDirName    = ".smith"
	repoYMLName     = "repo.yml"
	lockJSONName    = "lock.json"
)

// userHomeDir locates the home directory; it is a variable so tests can
// point the global cache root at a temp dir instead of the real $HOME.
var userHomeDir = os.UserHomeDir

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize the current directory as a Smith repository",
		Long: `Initialize the current directory as a Smith repository.

Creates .smith/repo.yml (repository configuration), .smith/lock.json (empty,
ready to commit), and the ~/.smith cache root for fetched project types.
If the directory is already initialized, does nothing.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInit(cmd)
		},
	}
}

func runInit(cmd *cobra.Command) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("smith init: %w", err)
	}
	repoName := filepath.Base(cwd)
	smithDir := filepath.Join(cwd, cacheDirName)
	repoYML := filepath.Join(smithDir, repoYMLName)
	lockJSON := filepath.Join(smithDir, lockJSONName)

	if _, err := os.Stat(repoYML); err == nil {
		cmd.Printf("%s is already a Smith repository (%s exists). Nothing to do.\n",
			cwd, filepath.Join(cacheDirName, repoYMLName))
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking %s: %w", repoYML, err)
	}

	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", smithDir, err)
	}
	if err := writeRepoYML(repoYML, repoName); err != nil {
		return err
	}
	if err := writeLockJSON(lockJSON); err != nil {
		return err
	}

	cacheRoot, err := ensureCacheRoot()
	if err != nil {
		return err
	}

	// Self-check: the artifacts we just wrote must load back through their
	// packages, which also validates them against the embedded schemas.
	// This fails loudly if the template and the schemas ever drift apart.
	if _, err := config.Load(repoYML); err != nil {
		return fmt.Errorf("self-check after init: %w", err)
	}
	if _, err := lock.Load(lockJSON); err != nil {
		return fmt.Errorf("self-check after init: %w", err)
	}

	cmd.Printf("Initialized Smith repository %q\n\n", repoName)
	cmd.Println("Created:")
	cmd.Printf("  %s    repository configuration\n", filepath.Join(cacheDirName, repoYMLName))
	cmd.Printf("  %s   lock file (nothing pinned until the first sync)\n", filepath.Join(cacheDirName, lockJSONName))
	cmd.Printf("  %s   global cache root for project types\n", cacheRoot)
	cmd.Println()
	cmd.Println("Next steps:")
	cmd.Println("  - commit .smith/ so everyone shares this configuration")
	cmd.Println("  - run smith sync when you need project types")
	return nil
}

// writeRepoYML renders the embedded repo.yml template with the repository
// name and writes it to path.
func writeRepoYML(path, repoName string) error {
	tmpl, err := template.ParseFS(templates, "templates/repo.yml")
	if err != nil {
		return fmt.Errorf("parsing repo.yml template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string{"Name": repoName}); err != nil {
		return fmt.Errorf("rendering repo.yml template: %w", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// writeLockJSON writes the pre-sync lock: the cliwright registry recorded
// without revisions, and no pinned types.
func writeLockJSON(path string) error {
	lk := lock.Lock{
		Version: 1,
		Sources: map[string]lock.SourceSet{
			registryName: {
				Types:     lock.Source{Git: typesGitURL},
				Templates: lock.Source{Git: templatesGitURL},
			},
		},
		Types: map[string]lock.TypePin{},
	}
	data, err := lock.Marshal(&lk)
	if err != nil {
		return fmt.Errorf("rendering lock.json: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// ensureCacheRoot creates ~/.smith if missing and returns its path. An
// existing directory is left untouched.
func ensureCacheRoot() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home directory: %w", err)
	}
	cacheRoot := filepath.Join(home, cacheDirName)
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", cacheRoot, err)
	}
	return cacheRoot, nil
}
