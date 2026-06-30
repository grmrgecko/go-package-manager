package pkgmgr

import (
	"context"
	"fmt"
)

type Dnf struct {
	rpmBase
}

// Name is the package manager's name.
func (p *Dnf) Name() string {
	return "dnf"
}

// Format is the package format the manager installs.
func (p *Dnf) Format() string {
	return "rpm"
}

// Path is the resolved path to the dnf command, or "" if missing.
func (p *Dnf) Path() string {
	return findBinary("dnf")
}

// exec runs dnf with the given args, failing if dnf cannot be found.
func (p *Dnf) exec(ctx context.Context, args ...string) error {
	bin := p.Path()
	if bin == "" {
		return fmt.Errorf("unable to find dnf")
	}
	return p.Command(ctx, bin, args...).Run()
}

// Sync updates repository metadata.
func (p *Dnf) Sync(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(args, "makecache")...)
}

// Install installs packages from the repositories.
func (p *Dnf) Install(ctx context.Context, args []string, packages ...string) error {
	return p.exec(ctx, append(joinArgs(args, "install"), packages...)...)
}

// Remove removes packages.
func (p *Dnf) Remove(ctx context.Context, args []string, packages ...string) error {
	return p.exec(ctx, append(joinArgs(args, "remove"), packages...)...)
}

// Upgrade upgrades the named packages.
func (p *Dnf) Upgrade(ctx context.Context, args []string, packages ...string) error {
	return p.exec(ctx, append(joinArgs(args, "upgrade"), packages...)...)
}

// InstallFile installs a package from a local file.
func (p *Dnf) InstallFile(ctx context.Context, args []string, packages ...string) error {
	return p.Install(ctx, args, packages...)
}

// UpgradeAll upgrades all packages with available updates.
func (p *Dnf) UpgradeAll(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(args, "upgrade")...)
}

// Clean removes cached repository metadata and packages.
func (p *Dnf) Clean(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(args, "clean", "all")...)
}

// ListUpgradable returns packages with an available update mapped to the
// candidate version.
func (p *Dnf) ListUpgradable(ctx context.Context, args []string) (map[string]string, error) {
	bin := p.Path()
	if bin == "" {
		return nil, fmt.Errorf("unable to find dnf")
	}

	// `dnf -q list --upgrades` prints "name.arch version repo" per upgrade and
	// exits 0 whether or not any are available.
	args = joinArgs(args, "-q", "list", "--upgrades")
	return runParseList(p.Command(ctx, bin, args...), parseRpmUpgradable)
}

// Search searches the repositories for packages matching query.
func (p *Dnf) Search(ctx context.Context, args []string, query string) ([]SearchResult, error) {
	bin := p.Path()
	if bin == "" {
		return nil, fmt.Errorf("unable to find dnf")
	}
	return runSearch(p.Command(ctx, bin, joinArgs(args, "search", query)...), parseRpmSearch)
}

// Info returns detailed information about the named packages.
func (p *Dnf) Info(ctx context.Context, args []string, packages ...string) (string, error) {
	bin := p.Path()
	if bin == "" {
		return "", fmt.Errorf("unable to find dnf")
	}
	return runCapture(p.Command(ctx, bin, append(joinArgs(args, "info"), packages...)...))
}
