package pkgmgr

import (
	"context"
	"fmt"
)

type Yum struct {
	rpmBase
}

// Name is the package manager's name.
func (p *Yum) Name() string {
	return "yum"
}

// Format is the package format the manager installs.
func (p *Yum) Format() string {
	return "rpm"
}

// Path is the resolved path to the yum command, or "" if missing.
func (p *Yum) Path() string {
	return findBinary("yum")
}

// exec runs yum with the given args, failing if yum cannot be found.
func (p *Yum) exec(ctx context.Context, args ...string) error {
	bin := p.Path()
	if bin == "" {
		return fmt.Errorf("unable to find yum")
	}
	return p.Command(ctx, bin, args...).Run()
}

// Sync updates repository metadata.
func (p *Yum) Sync(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(args, "makecache")...)
}

// Install installs packages from the repositories.
func (p *Yum) Install(ctx context.Context, args []string, packages ...string) error {
	return p.exec(ctx, append(joinArgs(p.confirmArgs(args, "-y"), "install"), packages...)...)
}

// Remove removes packages.
func (p *Yum) Remove(ctx context.Context, args []string, packages ...string) error {
	return p.exec(ctx, append(joinArgs(p.confirmArgs(args, "-y"), "remove"), packages...)...)
}

// Upgrade upgrades the named packages.
func (p *Yum) Upgrade(ctx context.Context, args []string, packages ...string) error {
	return p.exec(ctx, append(joinArgs(p.confirmArgs(args, "-y"), "upgrade"), packages...)...)
}

// InstallFile installs a package from a local file.
func (p *Yum) InstallFile(ctx context.Context, args []string, packages ...string) error {
	return p.Install(ctx, args, packages...)
}

// UpgradeAll upgrades all packages with available updates.
func (p *Yum) UpgradeAll(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(p.confirmArgs(args, "-y"), "upgrade")...)
}

// Clean removes cached repository metadata and packages.
func (p *Yum) Clean(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(args, "clean", "all")...)
}

// ListUpgradable returns packages with an available update mapped to the
// candidate version.
func (p *Yum) ListUpgradable(ctx context.Context, args []string) (map[string]string, error) {
	bin := p.Path()
	if bin == "" {
		return nil, fmt.Errorf("unable to find yum")
	}

	// `yum -q list updates` prints "name.arch version repo" per upgrade and
	// exits 0 whether or not any are available.
	args = joinArgs(args, "-q", "list", "updates")
	return runParseList(p.Command(ctx, bin, args...), parseRpmUpgradable)
}

// Search searches the repositories for packages matching query.
func (p *Yum) Search(ctx context.Context, args []string, query string) ([]SearchResult, error) {
	bin := p.Path()
	if bin == "" {
		return nil, fmt.Errorf("unable to find yum")
	}
	return runSearch(p.Command(ctx, bin, joinArgs(args, "search", query)...), parseRpmSearch)
}

// Info returns detailed information about the named packages.
func (p *Yum) Info(ctx context.Context, args []string, packages ...string) (string, error) {
	bin := p.Path()
	if bin == "" {
		return "", fmt.Errorf("unable to find yum")
	}
	return runCapture(p.Command(ctx, bin, append(joinArgs(args, "info"), packages...)...))
}
