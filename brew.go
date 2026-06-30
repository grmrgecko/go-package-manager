package pkgmgr

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// brewPaths are the common locations Homebrew installs its command, checked
// when brew is not already on PATH.
var brewPaths = []string{
	"/opt/homebrew/bin/brew",
	"/usr/local/bin/brew",
	"/home/linuxbrew/.linuxbrew/bin/brew",
}

// Brew is the Homebrew manager. Homebrew refuses to run as root, so its command
// is never sudo-wrapped; the embedded dropPrivilege runs it unprivileged and
// drops to a user via `sudo -u` when the process is root.
type Brew struct {
	baseManager
	dropPrivilege
}

// Name is the package manager's name.
func (p *Brew) Name() string {
	return "brew"
}

// Format is the package format the manager installs.
func (p *Brew) Format() string {
	return "bottle"
}

// Path is the resolved path to the brew command, or "" if missing.
func (p *Brew) Path() string {
	if bin, err := exec.LookPath("brew"); err == nil {
		return bin
	}
	for _, candidate := range brewPaths {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// exec runs brew with the given args, failing if brew cannot be found. brew is
// run unprivileged since Homebrew must not run as root.
func (p *Brew) exec(ctx context.Context, args ...string) error {
	bin := p.Path()
	if bin == "" {
		return fmt.Errorf("unable to find brew")
	}
	return p.runUnprivileged(ctx, bin, p.dropTarget(), args...)
}

// AddRepo adds a repo by tapping it. config, when set, is the tap's clone URL.
func (p *Brew) AddRepo(name, config string) error {
	args := []string{"tap", name}
	if config != "" {
		args = append(args, config)
	}
	return p.exec(context.Background(), args...)
}

// AddRepoURL adds a repo by tapping name from repoURL.
func (p *Brew) AddRepoURL(ctx context.Context, name, repoURL string) error {
	return p.exec(ctx, "tap", name, repoURL)
}

// RemoveRepo removes a repo by untapping it.
func (p *Brew) RemoveRepo(name string) error {
	return p.exec(context.Background(), "untap", name)
}

// GetRepo returns tap information for name, or "" if it is not tapped.
func (p *Brew) GetRepo(name string) string {
	bin := p.Path()
	if bin == "" {
		return ""
	}
	// Output captures stdout itself, so this bypasses the configured writers.
	out, err := exec.CommandContext(context.Background(), bin, "tap-info", name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ListRepos returns every tapped repo mapped name->tap-info. The value for each
// name matches what GetRepo returns for it. brew is run unprivileged since
// Homebrew must not run as root.
func (p *Brew) ListRepos(ctx context.Context, args []string) (map[string]string, error) {
	bin := p.Path()
	if bin == "" {
		return nil, fmt.Errorf("unable to find brew")
	}
	cmd, err := p.commandUnprivileged(ctx, bin, p.dropTarget(), joinArgs(args, "tap")...)
	if err != nil {
		return nil, err
	}
	// Capture stdout directly; the listing is parsed rather than streamed.
	cmd.Stdout = nil
	var buf bytes.Buffer
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	out := make(map[string]string)
	scanner := bufio.NewScanner(&buf)
	for scanner.Scan() {
		name := strings.TrimSpace(scanner.Text())
		if name == "" {
			continue
		}
		out[name] = p.GetRepo(name)
	}
	return out, scanner.Err()
}

// AddRepoKey is a no-op: Homebrew verifies downloads itself and has no
// user-managed signing keyring.
func (p *Brew) AddRepoKey(ctx context.Context, key string) error {
	return nil
}

// AddRepoKeyFile is a no-op: Homebrew manages verification itself.
func (p *Brew) AddRepoKeyFile(ctx context.Context, keyFile string) error {
	return nil
}

// AddRepoKeyURL is a no-op: Homebrew manages verification itself.
func (p *Brew) AddRepoKeyURL(ctx context.Context, keyURL string) error {
	return nil
}

// Sync updates repository metadata.
func (p *Brew) Sync(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(args, "update")...)
}

// Install installs packages from the repositories.
func (p *Brew) Install(ctx context.Context, args []string, packages ...string) error {
	return p.exec(ctx, append(joinArgs(args, "install"), packages...)...)
}

// Remove removes packages.
func (p *Brew) Remove(ctx context.Context, args []string, packages ...string) error {
	return p.exec(ctx, append(joinArgs(args, "uninstall"), packages...)...)
}

// Upgrade upgrades the named packages.
func (p *Brew) Upgrade(ctx context.Context, args []string, packages ...string) error {
	return p.exec(ctx, append(joinArgs(args, "upgrade"), packages...)...)
}

// InstallFile installs a package from a local formula or cask file.
func (p *Brew) InstallFile(ctx context.Context, args []string, packages ...string) error {
	return p.Install(ctx, args, packages...)
}

// UpgradeAll upgrades all packages with available updates.
func (p *Brew) UpgradeAll(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(args, "upgrade")...)
}

// Clean removes stale downloads and old installed versions. brew is run
// unprivileged since Homebrew must not run as root.
func (p *Brew) Clean(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(args, "cleanup")...)
}

// ListUpgradable returns packages with an available update mapped to the
// candidate version. brew is run unprivileged since Homebrew must not run as
// root.
func (p *Brew) ListUpgradable(ctx context.Context, args []string) (map[string]string, error) {
	bin := p.Path()
	if bin == "" {
		return nil, fmt.Errorf("unable to find brew")
	}

	// `brew outdated --verbose` prints "name (oldver) < newver" per package.
	args = joinArgs(args, "outdated", "--verbose")
	cmd, err := p.commandUnprivileged(ctx, bin, p.dropTarget(), args...)
	if err != nil {
		return nil, err
	}
	return runParseList(cmd, parseBrewUpgradable)
}

// parseBrewUpgradable parses brew's "name (oldver) < newver" rows into a
// name->candidate-version map.
func parseBrewUpgradable(r io.Reader) (map[string]string, error) {
	out := make(map[string]string)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		// Rows are "name (oldver) < newver"; the candidate is the final field.
		if len(fields) < 4 || fields[len(fields)-2] != "<" {
			continue
		}
		out[fields[0]] = fields[len(fields)-1]
	}
	return out, scanner.Err()
}

// Search searches the repositories for packages matching query. brew is run
// unprivileged since Homebrew must not run as root.
func (p *Brew) Search(ctx context.Context, args []string, query string) ([]SearchResult, error) {
	bin := p.Path()
	if bin == "" {
		return nil, fmt.Errorf("unable to find brew")
	}
	cmd, err := p.commandUnprivileged(ctx, bin, p.dropTarget(), joinArgs(args, "search", query)...)
	if err != nil {
		return nil, err
	}
	return runSearch(cmd, parseBrewSearch)
}

// parseBrewSearch parses brew's plain list of matching names into search
// results, skipping the "==> Formulae" / "==> Casks" section headers. brew
// search reports neither a version nor a summary.
func parseBrewSearch(r io.Reader) ([]SearchResult, error) {
	var out []SearchResult
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "==>") {
			continue
		}
		out = append(out, SearchResult{Name: line})
	}
	return out, scanner.Err()
}

// Info returns detailed information about the named packages. brew is run
// unprivileged since Homebrew must not run as root.
func (p *Brew) Info(ctx context.Context, args []string, packages ...string) (string, error) {
	bin := p.Path()
	if bin == "" {
		return "", fmt.Errorf("unable to find brew")
	}
	cmd, err := p.commandUnprivileged(ctx, bin, p.dropTarget(), append(joinArgs(args, "info"), packages...)...)
	if err != nil {
		return "", err
	}
	return runCapture(cmd)
}

// ListInstalled returns installed packages mapped to their version.
func (p *Brew) ListInstalled(ctx context.Context, args []string) (map[string]string, error) {
	bin := p.Path()
	if bin == "" {
		return nil, fmt.Errorf("unable to find brew")
	}

	// `brew list --versions` prints "name version [version...]" per line. brew is
	// run unprivileged since Homebrew must not run as root.
	args = joinArgs(args, "list", "--versions")
	cmd, err := p.commandUnprivileged(ctx, bin, p.dropTarget(), args...)
	if err != nil {
		return nil, err
	}
	return runVersionList(cmd, " ", false)
}
