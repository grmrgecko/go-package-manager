package pkgmgr

import (
	"context"
	"io"
	"os"
	"os/exec"
)

// SearchResult is a single package match returned by Search. Version and
// Summary are best-effort: a field is left empty when the underlying tool does
// not report it in its search output.
type SearchResult struct {
	Name    string
	Version string
	Summary string
}

// Manager is the interface for working with a system package manager. Methods
// that run a subprocess or perform network I/O take a context.Context so the
// caller can apply cancellation and timeouts.
type Manager interface {
	// Name is the package manager's name.
	Name() string
	// Format is the package format the manager installs (deb, rpm, ...).
	Format() string
	// Path is the resolved path to the manager's command, or "" if missing.
	Path() string
	// SetCmdWrapper sets a command wrapper, for example []string{"sudo"}.
	SetCmdWrapper(wrapper []string)
	// UseSudoWhenNeeded wraps privileged commands with sudo when not already
	// root. It is a no-op as root, and managers that must not run as root
	// (brew, yay, paru) bypass the wrapper for their own command.
	UseSudoWhenNeeded()
	// AssumeYes enables unattended operation so state-changing commands answer
	// their confirmation prompt automatically. It is required when the manager
	// runs without a controlling terminal, where an unanswered prompt aborts
	// the operation.
	AssumeYes()
	// SetIO overrides the stdin, stdout, and stderr used by spawned commands.
	// Passing nil for a stream restores its os default.
	SetIO(stdin io.Reader, stdout, stderr io.Writer)
	// AddRepo adds a repo from a configuration string.
	AddRepo(name, config string) error
	// AddRepoURL adds a repo whose configuration is downloaded from repoURL.
	AddRepoURL(ctx context.Context, name, repoURL string) error
	// RemoveRepo removes a repo.
	RemoveRepo(name string) error
	// GetRepo returns a repo's configuration, or "" if it does not exist.
	GetRepo(name string) string
	// ListRepos returns all configured repos mapped name->configuration. The
	// value for each name matches what GetRepo returns for it.
	ListRepos(ctx context.Context, args []string) (map[string]string, error)
	// AddRepoKey adds a key for repo package verification.
	AddRepoKey(ctx context.Context, key string) error
	// AddRepoKeyFile adds a key for repo package verification from a file.
	AddRepoKeyFile(ctx context.Context, keyFile string) error
	// AddRepoKeyURL adds a key for repo package verification from a URL.
	AddRepoKeyURL(ctx context.Context, keyURL string) error
	// Sync updates repository metadata.
	Sync(ctx context.Context, args []string) error
	// Install installs packages from the repositories.
	Install(ctx context.Context, args []string, packages ...string) error
	// Remove removes packages.
	Remove(ctx context.Context, args []string, packages ...string) error
	// Upgrade upgrades the named packages.
	Upgrade(ctx context.Context, args []string, packages ...string) error
	// InstallFile installs a package from a local file.
	InstallFile(ctx context.Context, args []string, packages ...string) error
	// UpgradeAll upgrades all packages with available updates.
	UpgradeAll(ctx context.Context, args []string) error
	// Clean removes cached package data to reclaim disk space.
	Clean(ctx context.Context, args []string) error
	// Search searches the repositories for packages matching query and returns
	// the matches.
	Search(ctx context.Context, args []string, query string) ([]SearchResult, error)
	// Info returns detailed information about the named packages as the
	// underlying tool's native text output.
	Info(ctx context.Context, args []string, packages ...string) (string, error)
	// ListInstalled returns installed packages mapped to their version.
	ListInstalled(ctx context.Context, args []string) (map[string]string, error)
	// ListUpgradable returns packages with an available update mapped to the
	// candidate version, without applying the updates.
	ListUpgradable(ctx context.Context, args []string) (map[string]string, error)
}

// GetSystemManager finds the system manager with a priority of zypper, dnf,
// yum, apt, apt-get, pacman, apk, brew. It returns nil when none are found.
func GetSystemManager() Manager {
	managers := []Manager{
		&Zypper{},
		&Dnf{},
		&Yum{},
		&Apt{},
		&AptGet{},
		&Pacman{},
		&Apk{},
		&Brew{},
	}
	for _, manager := range managers {
		if manager.Path() != "" {
			return manager
		}
	}
	return nil
}

// baseManager holds state and helpers shared by every manager implementation.
type baseManager struct {
	wrapper   []string
	assumeYes bool
	stdin     io.Reader
	stdout    io.Writer
	stderr    io.Writer
}

// SetCmdWrapper sets a command wrapper, for example []string{"sudo"}.
func (p *baseManager) SetCmdWrapper(wrapper []string) {
	p.wrapper = wrapper
}

// SetIO overrides the stdin, stdout, and stderr used by spawned commands.
func (p *baseManager) SetIO(stdin io.Reader, stdout, stderr io.Writer) {
	p.stdin = stdin
	p.stdout = stdout
	p.stderr = stderr
}

// AssumeYes enables unattended operation so state-changing commands answer
// their confirmation prompt automatically. It is an opt-in toggle honored by
// each manager's Install, Remove, Upgrade, InstallFile, and UpgradeAll methods.
func (p *baseManager) AssumeYes() {
	p.assumeYes = true
}

// confirmArgs prepends the manager's non-interactive confirmation flags to args
// when AssumeYes is enabled, so an unattended run does not stall on a prompt. It
// returns args unchanged when the toggle is off, and never duplicates a flag the
// caller already supplied.
func (p *baseManager) confirmArgs(args []string, flags ...string) []string {
	if !p.assumeYes {
		return args
	}
	var prefix []string
	for _, f := range flags {
		if !containsArg(args, f) {
			prefix = append(prefix, f)
		}
	}
	if len(prefix) == 0 {
		return args
	}
	return append(prefix, args...)
}

// stdinOrDefault returns the configured stdin, or os.Stdin when unset.
func (p *baseManager) stdinOrDefault() io.Reader {
	if p.stdin != nil {
		return p.stdin
	}
	return os.Stdin
}

// stdoutOrDefault returns the configured stdout, or os.Stdout when unset.
func (p *baseManager) stdoutOrDefault() io.Writer {
	if p.stdout != nil {
		return p.stdout
	}
	return os.Stdout
}

// stderrOrDefault returns the configured stderr, or os.Stderr when unset.
func (p *baseManager) stderrOrDefault() io.Writer {
	if p.stderr != nil {
		return p.stderr
	}
	return os.Stderr
}

// Command builds an *exec.Cmd for the manager, applying the configured command
// wrapper and I/O streams. The command is bound to ctx for cancellation.
func (p *baseManager) Command(ctx context.Context, command string, args ...string) *exec.Cmd {
	if len(p.wrapper) == 1 {
		args = append([]string{command}, args...)
		command = p.wrapper[0]
	} else if len(p.wrapper) > 1 {
		argsA := append([]string{}, p.wrapper[1:]...)
		argsA = append(argsA, command)
		args = append(argsA, args...)
		command = p.wrapper[0]
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = os.Environ()
	cmd.Stdin = p.stdinOrDefault()
	cmd.Stdout = p.stdoutOrDefault()
	cmd.Stderr = p.stderrOrDefault()
	return cmd
}
