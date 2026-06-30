package pkgmgr

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// dropPrivilege carries the configuration for managers whose command must not
// run as root (Homebrew, AUR helpers). Such managers embed it to gain
// SetDropUser and the shared unprivileged-invocation logic.
type dropPrivilege struct {
	dropUser string // user to drop to when running as root; "" falls back to SUDO_USER.
}

// SetDropUser sets the unprivileged user the command drops to when the process
// runs as root. When unset, the SUDO_USER environment variable is used.
func (d *dropPrivilege) SetDropUser(user string) {
	d.dropUser = user
}

// dropTarget returns the unprivileged user to drop to, preferring an explicitly
// configured user over SUDO_USER. It returns "" when neither is set.
func (d *dropPrivilege) dropTarget() string {
	if d.dropUser != "" {
		return d.dropUser
	}
	return os.Getenv("SUDO_USER")
}

// nonRootInvocation resolves the command and args for running bin, which must
// not execute as root. When not running as root bin is run directly; when
// running as root it is run as dropUser via sudo so it never executes
// privileged. asRoot, the resolved binaries, and dropUser are passed in so the
// decision can be tested.
func nonRootInvocation(bin, sudoBin, dropUser string, asRoot bool, args []string) (string, []string, error) {
	if !asRoot {
		return bin, args, nil
	}
	if dropUser == "" {
		return "", nil, fmt.Errorf("running as root and no unprivileged user is set; set SUDO_USER or call SetDropUser")
	}
	if dropUser == "root" {
		return "", nil, fmt.Errorf("the configured drop user is root, which is not permitted for this manager")
	}
	if sudoBin == "" {
		return "", nil, fmt.Errorf("unable to find sudo to drop privileges")
	}
	// -H gives the command the target user's HOME so user-level tooling works.
	out := append([]string{"-u", dropUser, "-H", bin}, args...)
	return sudoBin, out, nil
}

// commandUnprivileged builds an *exec.Cmd for bin that runs unprivileged,
// dropping to dropUser via sudo when the process is root. It deliberately
// bypasses the sudo command wrapper, since these managers escalate (or refuse)
// on their own. The configured I/O streams are applied.
func (p *baseManager) commandUnprivileged(ctx context.Context, bin, dropUser string, args ...string) (*exec.Cmd, error) {
	command, cargs, err := nonRootInvocation(bin, findBinary("sudo"), dropUser, os.Geteuid() == 0, args)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, command, cargs...)
	cmd.Env = os.Environ()
	cmd.Stdin = p.stdinOrDefault()
	cmd.Stdout = p.stdoutOrDefault()
	cmd.Stderr = p.stderrOrDefault()
	return cmd, nil
}

// runUnprivileged runs bin with args unprivileged, dropping to dropUser via
// sudo when the process is root.
func (p *baseManager) runUnprivileged(ctx context.Context, bin, dropUser string, args ...string) error {
	cmd, err := p.commandUnprivileged(ctx, bin, dropUser, args...)
	if err != nil {
		return err
	}
	return cmd.Run()
}

// UseSudoWhenNeeded sets the command wrapper to sudo when the process is not
// already root, so privileged operations escalate. It is a no-op when running
// as root. Managers that must not run as root (brew, yay, paru) bypass the
// wrapper for their own command, so this only affects their privileged
// pacman-side helpers and the root-required managers.
func (p *baseManager) UseSudoWhenNeeded() {
	if os.Geteuid() != 0 {
		p.SetCmdWrapper([]string{"sudo"})
	}
}
