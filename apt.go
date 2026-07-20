package pkgmgr

import (
	"context"
	"fmt"
	"io"
	"strings"
)

type Apt struct {
	aptBase
}

// Name is the package manager's name.
func (p *Apt) Name() string {
	return "apt"
}

// Format is the package format the manager installs.
func (p *Apt) Format() string {
	return "deb"
}

// Path is the resolved path to the apt command, or "" if missing.
func (p *Apt) Path() string {
	return findBinary("apt")
}

// exec runs apt with the given args, failing if apt cannot be found.
func (p *Apt) exec(ctx context.Context, args ...string) error {
	bin := p.Path()
	if bin == "" {
		return fmt.Errorf("unable to find apt")
	}
	return p.Command(ctx, bin, args...).Run()
}

// Sync updates repository metadata.
func (p *Apt) Sync(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(args, "update")...)
}

// Install installs packages from the repositories.
func (p *Apt) Install(ctx context.Context, args []string, packages ...string) error {
	return p.exec(ctx, append(joinArgs(p.confirmArgs(args, "-y"), "install"), packages...)...)
}

// Remove removes packages.
func (p *Apt) Remove(ctx context.Context, args []string, packages ...string) error {
	return p.exec(ctx, append(joinArgs(p.confirmArgs(args, "-y"), "remove"), packages...)...)
}

// Upgrade upgrades the named packages.
func (p *Apt) Upgrade(ctx context.Context, args []string, packages ...string) error {
	return p.Install(ctx, args, packages...)
}

// InstallFile installs a package from a local file.
func (p *Apt) InstallFile(ctx context.Context, args []string, packages ...string) error {
	return p.Install(ctx, args, packages...)
}

// UpgradeAll upgrades all packages with available updates.
func (p *Apt) UpgradeAll(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(p.confirmArgs(args, "-y"), "upgrade")...)
}

// Clean removes the local package cache.
func (p *Apt) Clean(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(args, "clean")...)
}

// Search searches the repositories for packages matching query.
func (p *Apt) Search(ctx context.Context, args []string, query string) ([]SearchResult, error) {
	bin := p.Path()
	if bin == "" {
		return nil, fmt.Errorf("unable to find apt")
	}
	return runSearch(p.Command(ctx, bin, joinArgs(args, "search", query)...), parseAptSearch)
}

// parseAptSearch parses the "name/suite version arch" headers and indented
// descriptions printed by `apt search` into search results.
func parseAptSearch(r io.Reader) ([]SearchResult, error) {
	return parseColumnarSearch(r, func(fields []string) (string, string, bool) {
		// Header rows look like "name/suite version arch [tags]".
		if len(fields) < 2 || !strings.Contains(fields[0], "/") {
			return "", "", false
		}
		name, _, _ := strings.Cut(fields[0], "/")
		return name, fields[1], true
	})
}

// Info returns detailed information about the named packages.
func (p *Apt) Info(ctx context.Context, args []string, packages ...string) (string, error) {
	bin := p.Path()
	if bin == "" {
		return "", fmt.Errorf("unable to find apt")
	}
	return runCapture(p.Command(ctx, bin, append(joinArgs(args, "show"), packages...)...))
}

// ListInstalled returns installed packages mapped to their version.
func (p *Apt) ListInstalled(ctx context.Context, args []string) (map[string]string, error) {
	dpkgQuery := findBinary("dpkg-query")
	if dpkgQuery == "" {
		return nil, fmt.Errorf("unable to find dpkg-query")
	}

	args = joinArgs(args, "-f", "${Package} := ${Version}\\n", "-W")
	return runVersionList(p.Command(ctx, dpkgQuery, args...), " := ", false)
}
