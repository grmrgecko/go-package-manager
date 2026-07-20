package pkgmgr

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

type AptGet struct {
	aptBase
}

// Name is the package manager's name.
func (p *AptGet) Name() string {
	return "apt-get"
}

// Format is the package format the manager installs.
func (p *AptGet) Format() string {
	return "deb"
}

// Path is the resolved path to the apt-get command, or "" if missing.
func (p *AptGet) Path() string {
	return findBinary("apt-get")
}

// exec runs apt-get with the given args, failing if apt-get cannot be found.
func (p *AptGet) exec(ctx context.Context, args ...string) error {
	bin := p.Path()
	if bin == "" {
		return fmt.Errorf("unable to find apt-get")
	}
	return p.Command(ctx, bin, args...).Run()
}

// Sync updates repository metadata.
func (p *AptGet) Sync(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(args, "update")...)
}

// Install installs packages from the repositories.
func (p *AptGet) Install(ctx context.Context, args []string, packages ...string) error {
	return p.exec(ctx, append(joinArgs(p.confirmArgs(args, "-y"), "install"), packages...)...)
}

// Remove removes packages.
func (p *AptGet) Remove(ctx context.Context, args []string, packages ...string) error {
	return p.exec(ctx, append(joinArgs(p.confirmArgs(args, "-y"), "remove"), packages...)...)
}

// Upgrade upgrades the named packages.
func (p *AptGet) Upgrade(ctx context.Context, args []string, packages ...string) error {
	return p.Install(ctx, args, packages...)
}

// InstallFile installs a package from a local file.
func (p *AptGet) InstallFile(ctx context.Context, args []string, packages ...string) error {
	return p.Install(ctx, args, packages...)
}

// UpgradeAll upgrades all packages with available updates.
func (p *AptGet) UpgradeAll(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(p.confirmArgs(args, "-y"), "upgrade")...)
}

// Clean removes the local package cache.
func (p *AptGet) Clean(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(args, "clean")...)
}

// Search searches the repositories for packages matching query. apt-get has no
// search subcommand, so apt-cache is used.
func (p *AptGet) Search(ctx context.Context, args []string, query string) ([]SearchResult, error) {
	aptCache := findBinary("apt-cache")
	if aptCache == "" {
		return nil, fmt.Errorf("unable to find apt-cache")
	}
	return runSearch(p.Command(ctx, aptCache, joinArgs(args, "search", query)...), parseAptCacheSearch)
}

// parseAptCacheSearch parses the "name - summary" lines printed by `apt-cache
// search` into search results. apt-cache does not report a version.
func parseAptCacheSearch(r io.Reader) ([]SearchResult, error) {
	var out []SearchResult
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		name, summary, ok := strings.Cut(scanner.Text(), " - ")
		if !ok {
			continue
		}
		out = append(out, SearchResult{Name: strings.TrimSpace(name), Summary: strings.TrimSpace(summary)})
	}
	return out, scanner.Err()
}

// Info returns detailed information about the named packages. apt-get has no
// show subcommand, so apt-cache is used.
func (p *AptGet) Info(ctx context.Context, args []string, packages ...string) (string, error) {
	aptCache := findBinary("apt-cache")
	if aptCache == "" {
		return "", fmt.Errorf("unable to find apt-cache")
	}
	return runCapture(p.Command(ctx, aptCache, append(joinArgs(args, "show"), packages...)...))
}

// ListInstalled returns installed packages mapped to their version.
func (p *AptGet) ListInstalled(ctx context.Context, args []string) (map[string]string, error) {
	dpkgQuery := findBinary("dpkg-query")
	if dpkgQuery == "" {
		return nil, fmt.Errorf("unable to find dpkg-query")
	}

	args = joinArgs(args, "-f", "${Package} := ${Version}\\n", "-W")
	return runVersionList(p.Command(ctx, dpkgQuery, args...), " := ", false)
}
