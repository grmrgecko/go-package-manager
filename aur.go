package pkgmgr

import (
	"context"
	"fmt"
)

// aurBase provides the behavior shared by the AUR helper managers (yay, paru).
// The helpers are deliberate pacman drop-ins: they accept the same operation
// flags, read repos from PACMAN_CONF, and verify with pacman-key. So repo and
// key management are inherited from Pacman and only the package action verbs run
// the helper binary instead of pacman.
//
// AUR helpers refuse to run as root and escalate to pacman themselves, so their
// actions never use the sudo command wrapper; the embedded dropPrivilege runs
// them unprivileged, dropping to a user via `sudo -u` when the process is root.
// Construct a helper with NewYay or NewParu so its binary name is set.
type aurBase struct {
	Pacman
	dropPrivilege
	helper string // helper binary name, e.g. "yay" or "paru".
}

// Name is the package manager's name.
func (p *aurBase) Name() string {
	return p.helper
}

// Path is the resolved path to the helper command, or "" if missing.
func (p *aurBase) Path() string {
	if p.helper == "" {
		return ""
	}
	return findBinary(p.helper)
}

// aurExec runs the helper binary with args. It runs unprivileged via the
// embedded dropPrivilege, dropping with `sudo -u` only when the process is
// root, since the helper must run unprivileged and escalate to pacman itself.
func (p *aurBase) aurExec(ctx context.Context, args ...string) error {
	bin := p.Path()
	if bin == "" {
		return fmt.Errorf("unable to find %s", p.helper)
	}
	if err := p.runUnprivileged(ctx, bin, p.dropTarget(), args...); err != nil {
		return fmt.Errorf("%s: %w", p.helper, err)
	}
	return nil
}

// Sync updates repository metadata.
func (p *aurBase) Sync(ctx context.Context, args []string) error {
	return p.aurExec(ctx, joinArgs(args, "-Sy")...)
}

// Install installs packages from the repositories and the AUR.
func (p *aurBase) Install(ctx context.Context, args []string, packages ...string) error {
	return p.aurExec(ctx, append(joinArgs(p.confirmArgs(args, "--noconfirm"), "-S"), packages...)...)
}

// Remove removes packages.
func (p *aurBase) Remove(ctx context.Context, args []string, packages ...string) error {
	return p.aurExec(ctx, append(joinArgs(p.confirmArgs(args, "--noconfirm"), "-R"), packages...)...)
}

// Upgrade upgrades the named packages.
func (p *aurBase) Upgrade(ctx context.Context, args []string, packages ...string) error {
	return p.Install(ctx, args, packages...)
}

// InstallFile installs a package from a local file.
func (p *aurBase) InstallFile(ctx context.Context, args []string, packages ...string) error {
	return p.aurExec(ctx, append(joinArgs(p.confirmArgs(args, "--noconfirm"), "-U"), packages...)...)
}

// UpgradeAll upgrades all packages with available updates, including the AUR.
func (p *aurBase) UpgradeAll(ctx context.Context, args []string) error {
	return p.aurExec(ctx, joinArgs(p.confirmArgs(args, "--noconfirm"), "-Syu")...)
}

// Search searches the repositories and the AUR for packages matching query. The
// helper is a pacman drop-in, so its output is parsed like pacman's. It is run
// unprivileged since the helper refuses to run as root.
func (p *aurBase) Search(ctx context.Context, args []string, query string) ([]SearchResult, error) {
	bin := p.Path()
	if bin == "" {
		return nil, fmt.Errorf("unable to find %s", p.helper)
	}
	cmd, err := p.commandUnprivileged(ctx, bin, p.dropTarget(), joinArgs(args, "-Ss", query)...)
	if err != nil {
		return nil, err
	}
	return runSearch(cmd, parsePacmanSearch)
}

// Info returns detailed information about the named packages. It is run
// unprivileged since the helper refuses to run as root.
func (p *aurBase) Info(ctx context.Context, args []string, packages ...string) (string, error) {
	bin := p.Path()
	if bin == "" {
		return "", fmt.Errorf("unable to find %s", p.helper)
	}
	cmd, err := p.commandUnprivileged(ctx, bin, p.dropTarget(), append(joinArgs(args, "-Si"), packages...)...)
	if err != nil {
		return "", err
	}
	return runCapture(cmd)
}

// Clean removes cached packages, including the helper's AUR build cache.
func (p *aurBase) Clean(ctx context.Context, args []string) error {
	return p.aurExec(ctx, joinArgs(args, "-Sc")...)
}

// ListUpgradable returns repo and AUR packages with an available update mapped
// to the candidate version. The helper's `-Qu` covers the AUR, and like pacman
// it exits 1 when nothing is upgradable, which is treated as an empty result.
func (p *aurBase) ListUpgradable(ctx context.Context, args []string) (map[string]string, error) {
	bin := p.Path()
	if bin == "" {
		return nil, fmt.Errorf("unable to find %s", p.helper)
	}

	// The query needs no privilege, but the helper refuses to run as root, so it
	// is invoked unprivileged, dropping with `sudo -u` when the process is root.
	args = joinArgs(args, "-Qu")
	cmd, err := p.commandUnprivileged(ctx, bin, p.dropTarget(), args...)
	if err != nil {
		return nil, err
	}
	return runParseList(cmd, parsePacmanUpgradable, 1)
}

// Yay is the AUR helper manager backed by the yay command.
type Yay struct {
	aurBase
}

// NewYay returns a Yay manager with its helper binary name set.
func NewYay() *Yay {
	y := &Yay{}
	y.helper = "yay"
	return y
}

// Paru is the AUR helper manager backed by the paru command.
type Paru struct {
	aurBase
}

// NewParu returns a Paru manager with its helper binary name set.
func NewParu() *Paru {
	p := &Paru{}
	p.helper = "paru"
	return p
}
