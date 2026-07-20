package pkgmgr

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// All managers must satisfy the Manager interface.
var (
	_ Manager = (*Apt)(nil)
	_ Manager = (*AptGet)(nil)
	_ Manager = (*Dnf)(nil)
	_ Manager = (*Yum)(nil)
	_ Manager = (*Zypper)(nil)
	_ Manager = (*Pacman)(nil)
	_ Manager = (*Apk)(nil)
	_ Manager = (*Brew)(nil)
	_ Manager = (*Yay)(nil)
	_ Manager = (*Paru)(nil)
)

func TestCommandWrapper(t *testing.T) {
	cases := []struct {
		name    string
		wrapper []string
		want    []string
	}{
		{"none", nil, []string{"apt", "install", "vim"}},
		{"single", []string{"sudo"}, []string{"sudo", "apt", "install", "vim"}},
		{"multi", []string{"sudo", "-n"}, []string{"sudo", "-n", "apt", "install", "vim"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b baseManager
			b.SetCmdWrapper(tc.wrapper)
			cmd := b.Command(context.Background(), "apt", "install", "vim")
			assert.Equal(t, tc.want, cmd.Args)
		})
	}
}

func TestCommandIO(t *testing.T) {
	// With no overrides the os streams are used.
	var def baseManager
	cmd := def.Command(context.Background(), "echo")
	assert.Same(t, os.Stdout, cmd.Stdout)
	assert.Same(t, os.Stderr, cmd.Stderr)
	assert.Same(t, os.Stdin, cmd.Stdin)

	// SetIO overrides each stream.
	var b baseManager
	var out, errBuf bytes.Buffer
	in := strings.NewReader("input")
	b.SetIO(in, &out, &errBuf)
	cmd = b.Command(context.Background(), "echo")
	assert.Same(t, &out, cmd.Stdout)
	assert.Same(t, &errBuf, cmd.Stderr)
	assert.Same(t, in, cmd.Stdin)
}

func TestJoinArgsDoesNotMutate(t *testing.T) {
	base := make([]string, 1, 8) // spare capacity would let append clobber base
	base[0] = "-y"

	got := joinArgs(base, "install")
	got = append(got, "vim")

	assert.Equal(t, []string{"-y"}, base, "joinArgs mutated its input")
	assert.Equal(t, []string{"-y", "install", "vim"}, got)
}

func TestConfirmArgs(t *testing.T) {
	var b baseManager

	// Off by default: args pass through untouched.
	assert.Equal(t, []string{"install"}, b.confirmArgs([]string{"install"}, "-y"))

	b.AssumeYes()

	// On: the flag is prepended so it precedes the subcommand.
	assert.Equal(t, []string{"-y", "install"}, b.confirmArgs([]string{"install"}, "-y"))

	// A flag the caller already supplied is not duplicated.
	assert.Equal(t, []string{"-y", "install"}, b.confirmArgs([]string{"-y", "install"}, "-y"))

	// Multiple flags are all injected, preserving order.
	assert.Equal(t, []string{"-a", "-b", "-S"}, b.confirmArgs([]string{"-S"}, "-a", "-b"))

	// The caller's slice is not mutated, even with spare capacity.
	in := make([]string, 1, 8)
	in[0] = "install"
	_ = b.confirmArgs(in, "-y")
	assert.Equal(t, []string{"install"}, in, "confirmArgs mutated its input")
}

// TestAssumeYesInjectsConfirmFlag verifies that, with AssumeYes enabled, each
// manager places its own non-interactive confirmation flag ahead of the
// subcommand so an unattended install does not stall on a prompt. A fake binary
// on PATH records the arguments it receives.
func TestAssumeYesInjectsConfirmFlag(t *testing.T) {
	cases := []struct {
		name string
		bin  string
		newM func() Manager
		want []string
	}{
		{"dnf", "dnf", func() Manager { return &Dnf{} }, []string{"-y", "install", "vim"}},
		{"yum", "yum", func() Manager { return &Yum{} }, []string{"-y", "install", "vim"}},
		{"apt", "apt", func() Manager { return &Apt{} }, []string{"-y", "install", "vim"}},
		{"apt-get", "apt-get", func() Manager { return &AptGet{} }, []string{"-y", "install", "vim"}},
		{"pacman", "pacman", func() Manager { return &Pacman{} }, []string{"--noconfirm", "-S", "vim"}},
		// zypper's --non-interactive is a global option that must precede the
		// subcommand, which prepending guarantees.
		{"zypper", "zypper", func() Manager { return &Zypper{} }, []string{"--non-interactive", "install", "vim"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "args")
			// The fake binary records each argument on its own line, then exits 0.
			script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + marker + "\n"
			require.NoError(t, os.WriteFile(filepath.Join(dir, tc.bin), []byte(script), 0755))
			t.Setenv("PATH", dir)

			m := tc.newM()
			m.AssumeYes()
			require.NoError(t, m.Install(context.Background(), nil, "vim"))

			data, err := os.ReadFile(marker)
			require.NoError(t, err, "manager did not invoke the binary")
			assert.Equal(t, tc.want, strings.Fields(string(data)))
		})
	}
}

func TestParseVersionList(t *testing.T) {
	in := "git := 1:2.39.0\nfoo := 0:1.0\nmalformed line\n\n  bar  :=  2.0 \n"
	got, err := parseVersionList(strings.NewReader(in), " := ", true)
	require.NoError(t, err)

	want := map[string]string{
		"git": "1:2.39.0", // a non-zero epoch is preserved
		"foo": "1.0",      // a 0: epoch is trimmed
		"bar": "2.0",      // surrounding whitespace is trimmed
	}
	assert.Equal(t, want, got)
}

func TestParseApkList(t *testing.T) {
	in := "busybox-1.36.1-r5\npy3-foo-1.2.3-r0\nlibc6-compat-1.2-r0\ngarbage\n"
	got, err := parseApkList(strings.NewReader(in))
	require.NoError(t, err)

	want := map[string]string{
		"busybox":      "1.36.1-r5",
		"py3-foo":      "1.2.3-r0",
		"libc6-compat": "1.2-r0",
	}
	assert.Equal(t, want, got)
}

func TestAptKeyDest(t *testing.T) {
	armored := []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----\nabc\n")
	assert.Equal(t, ".asc", filepath.Ext(aptKeyDest(armored)), "armored key should use .asc")

	binary := []byte{0x99, 0x01, 0x02}
	assert.Equal(t, ".gpg", filepath.Ext(aptKeyDest(binary)), "binary key should use .gpg")

	// The destination is stable for the same key and lives in the keyrings dir.
	assert.Equal(t, aptKeyDest(armored), aptKeyDest(armored), "aptKeyDest should be deterministic")
	assert.Equal(t, APT_KEYRINGS_DIR, filepath.Dir(aptKeyDest(armored)), "key should live in the keyrings dir")
}

func TestIniSectionRoundTrip(t *testing.T) {
	file := filepath.Join(t.TempDir(), "pacman.conf")
	require.NoError(t, os.WriteFile(file, []byte("[options]\nHoldPkg = pacman\n"), 0644))

	require.NoError(t, setIniSection(file, "myrepo", "Server = https://example.test/repo"))
	assert.Equal(t, "Server = https://example.test/repo", getIniSection(file, "myrepo"))
	// Existing sections are preserved.
	assert.Equal(t, "HoldPkg = pacman", getIniSection(file, "options"), "options section damaged")

	// Re-adding replaces rather than duplicates.
	require.NoError(t, setIniSection(file, "myrepo", "Server = https://example.test/other"))
	assert.Equal(t, "Server = https://example.test/other", getIniSection(file, "myrepo"))

	require.NoError(t, removeIniSection(file, "myrepo"))
	assert.Empty(t, getIniSection(file, "myrepo"), "removed section still present")
	assert.Equal(t, "HoldPkg = pacman", getIniSection(file, "options"), "options section lost after remove")
}

func TestMarkerSectionRoundTrip(t *testing.T) {
	file := filepath.Join(t.TempDir(), "repositories")
	require.NoError(t, os.WriteFile(file, []byte("https://dl-cdn.alpinelinux.org/alpine/v3.20/main\n"), 0644))

	require.NoError(t, setMarkerSection(file, "extra", "https://example.test/alpine"))
	assert.Equal(t, "https://example.test/alpine", getMarkerSection(file, "extra"))

	// The pre-existing line is still present.
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Contains(t, string(data), "dl-cdn.alpinelinux.org", "original repository line was lost")

	require.NoError(t, removeMarkerSection(file, "extra"))
	assert.Empty(t, getMarkerSection(file, "extra"), "removed marker section still present")
}

func TestListRepoFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker.list"), []byte("deb https://download.docker.com/linux/ubuntu stable\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hashicorp.list"), []byte("deb https://apt.releases.hashicorp.com stable main\n"), 0644))
	// A non-matching suffix, a subdirectory whose name ends in the suffix, and a
	// missing directory must all be ignored rather than listed or erroring.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ignore.repo"), []byte("noise\n"), 0644))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub.list"), 0755))

	got, err := listRepoFiles(dir, ".list")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"docker":    "deb https://download.docker.com/linux/ubuntu stable\n",
		"hashicorp": "deb https://apt.releases.hashicorp.com stable main\n",
	}, got)

	// The listed value matches readRepoFile for each stem, which is the same
	// value GetRepo returns for the corresponding manager.
	assert.Equal(t, readRepoFile(filepath.Join(dir, "docker.list")), got["docker"], "listRepoFiles value does not match readRepoFile")

	// A missing directory is an empty result, not an error.
	got, err = listRepoFiles(filepath.Join(dir, "missing"), ".list")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestListIniSections(t *testing.T) {
	file := filepath.Join(t.TempDir(), "pacman.conf")
	content := "[options]\nHoldPkg = pacman\n\n" +
		"[core]\nSigLevel = Required\nServer = https://example.test/core\n\n" +
		"[extra]\nServer = https://example.test/extra\n"
	require.NoError(t, os.WriteFile(file, []byte(content), 0644))

	got, err := listIniSections(file, "options")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"core":  "SigLevel = Required\nServer = https://example.test/core",
		"extra": "Server = https://example.test/extra",
	}, got)

	// The listing matches getIniSection per name, which is what Pacman.GetRepo
	// returns, and the excluded section is omitted.
	assert.Equal(t, getIniSection(file, "core"), got["core"], "listIniSections value does not match getIniSection")
	assert.NotContains(t, got, "options", "the options section should be excluded from the listing")

	// A missing file is an empty result, not an error.
	got, err = listIniSections(filepath.Join(t.TempDir(), "missing"))
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestListMarkerSections(t *testing.T) {
	file := filepath.Join(t.TempDir(), "repositories")
	content := "https://dl-cdn.alpinelinux.org/alpine/v3.20/main\n" +
		"# >>> extra\n" +
		"https://example.test/alpine\n" +
		"# <<< extra\n" +
		"# >>> testing\n" +
		"https://example.test/testing\n" +
		"# <<< testing\n"
	require.NoError(t, os.WriteFile(file, []byte(content), 0644))

	got, err := listMarkerSections(file)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"extra":   "https://example.test/alpine",
		"testing": "https://example.test/testing",
	}, got)

	// The listing matches getMarkerSection per name, which is what Apk.GetRepo
	// returns, and unmarked repository lines are not included.
	assert.Equal(t, getMarkerSection(file, "extra"), got["extra"], "listMarkerSections value does not match getMarkerSection")

	// A missing file is an empty result, not an error.
	got, err = listMarkerSections(filepath.Join(t.TempDir(), "missing"))
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestAptKeyPrefersAptKeyBinary verifies that, for backward compatibility with
// older releases (e.g. Ubuntu 14.04) where apt-key is the only supported method
// of adding keys, AddRepoKey uses apt-key when it is present on PATH rather than
// falling back to the modern keyring directory.
func TestAptKeyPrefersAptKeyBinary(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "stdin")

	// A fake apt-key that records the key it receives on stdin. It uses only
	// shell builtins so it does not depend on PATH, which the test overrides.
	script := "#!/bin/sh\nread key\necho \"$key\" > " + marker + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "apt-key"), []byte(script), 0755))
	t.Setenv("PATH", dir)

	var a Apt
	require.NoError(t, a.AddRepoKey(context.Background(), "MYKEY"))

	data, err := os.ReadFile(marker)
	require.NoError(t, err, "apt-key was not invoked")
	assert.Equal(t, "MYKEY", strings.TrimSpace(string(data)))
}

func TestNonRootInvocation(t *testing.T) {
	args := []string{"-S", "some-aur-pkg"}

	// As a normal user the command runs directly, unwrapped.
	cmd, got, err := nonRootInvocation("/usr/bin/yay", "/usr/bin/sudo", "alice", false, args)
	require.NoError(t, err)
	assert.Equal(t, "/usr/bin/yay", cmd)
	assert.Equal(t, args, got)

	// As root the command drops to the target user via sudo -u.
	cmd, got, err = nonRootInvocation("/usr/bin/yay", "/usr/bin/sudo", "alice", true, args)
	require.NoError(t, err)
	assert.Equal(t, "/usr/bin/sudo", cmd)
	assert.Equal(t, []string{"-u", "alice", "-H", "/usr/bin/yay", "-S", "some-aur-pkg"}, got)

	// As root with no user to drop to, it fails rather than running as root.
	_, _, err = nonRootInvocation("/usr/bin/yay", "/usr/bin/sudo", "", true, args)
	assert.Error(t, err, "expected an error when running as root with no drop user")

	// As root the drop user must not itself be root.
	_, _, err = nonRootInvocation("/usr/bin/yay", "/usr/bin/sudo", "root", true, args)
	assert.Error(t, err, "expected an error when the drop user is root")

	// As root with no sudo available, it cannot drop privileges.
	_, _, err = nonRootInvocation("/usr/bin/yay", "", "alice", true, args)
	assert.Error(t, err, "expected an error when sudo is unavailable")
}

func TestNonRootInvocationDoesNotMutateArgs(t *testing.T) {
	args := []string{"-S", "pkg"}
	_, _, err := nonRootInvocation("/usr/bin/yay", "/usr/bin/sudo", "alice", true, args)
	require.NoError(t, err)
	assert.Equal(t, []string{"-S", "pkg"}, args, "nonRootInvocation mutated its input")
}

func TestParseVersionListMissingSeparator(t *testing.T) {
	// Lines without the separator are skipped rather than erroring.
	got, err := parseVersionList(strings.NewReader("nosephere\na := b\n"), " := ", false)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"a": "b"}, got)
}

func TestParseAptUpgradable(t *testing.T) {
	// A simulated `apt-get -s upgrade`: an Inst line with a prior version, one
	// without (a newly pulled dependency), a Conf line, and noise.
	in := "Reading package lists...\n" +
		"Inst vim [2:8.1-1] (2:8.2-1 Ubuntu:22.04 [amd64])\n" +
		"Inst libfoo (1.1 Ubuntu:22.04 [amd64])\n" +
		"Conf vim (2:8.2-1 Ubuntu:22.04 [amd64])\n"
	got, err := parseAptUpgradable(strings.NewReader(in))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"vim": "2:8.2-1", "libfoo": "1.1"}, got)
}

func TestParseRpmUpgradable(t *testing.T) {
	// `dnf -q list --upgrades` output with a header and a dotted package name.
	in := "Available Upgrades\n" +
		"vim-enhanced.x86_64    2:9.0.1-1.fc39    updates\n" +
		"python3.11.x86_64      3.11.7-1.fc39     updates\n" +
		"garbage line here that is not three columns wide\n"
	got, err := parseRpmUpgradable(strings.NewReader(in))
	require.NoError(t, err)
	// The ".arch" qualifier is stripped, including for names that contain dots.
	assert.Equal(t, map[string]string{"vim-enhanced": "2:9.0.1-1.fc39", "python3.11": "3.11.7-1.fc39"}, got)
}

func TestParseZypperUpgradable(t *testing.T) {
	in := "S | Repository | Name | Current Version | Available Version | Arch\n" +
		"--+------------+------+-----------------+-------------------+------\n" +
		"v | repo-oss   | vim  | 9.0.1-1.1       | 9.0.2-1.1         | x86_64\n"
	got, err := parseZypperUpgradable(strings.NewReader(in))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"vim": "9.0.2-1.1"}, got)
}

func TestParsePacmanUpgradable(t *testing.T) {
	// Includes an "[ignored]" suffix and a malformed line.
	in := "vim 9.0.1-1 -> 9.0.2-1\nlinux 6.6.1-1 -> 6.6.2-1 [ignored]\nnot an upgrade line\n"
	got, err := parsePacmanUpgradable(strings.NewReader(in))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"vim": "9.0.2-1", "linux": "6.6.2-1"}, got)
}

func TestParseApkUpgradable(t *testing.T) {
	in := "Installed:                Available:\nbusybox-1.36.1-r5 < 1.36.1-r6\nmusl-1.2.4-r2 < 1.2.5-r0\n"
	got, err := parseApkUpgradable(strings.NewReader(in))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"busybox": "1.36.1-r6", "musl": "1.2.5-r0"}, got)
}

func TestParseBrewUpgradable(t *testing.T) {
	in := "git (2.39.0) < 2.43.0\nwget (1.21.3) < 1.21.4\nnot outdated output\n"
	got, err := parseBrewUpgradable(strings.NewReader(in))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"git": "2.43.0", "wget": "1.21.4"}, got)
}

func TestParseAptSearch(t *testing.T) {
	in := "Sorting...\nFull Text Search...\n" +
		"vim/jammy,now 2:8.2.3995-1ubuntu2 amd64 [installed]\n" +
		"  Vi IMproved - enhanced vi editor\n\n" +
		"xxd/jammy 2:8.2.3995-1ubuntu2 amd64\n" +
		"  tool to make (or reverse) a hex dump\n"
	got, err := parseAptSearch(strings.NewReader(in))
	require.NoError(t, err)
	assert.Equal(t, []SearchResult{
		{Name: "vim", Version: "2:8.2.3995-1ubuntu2", Summary: "Vi IMproved - enhanced vi editor"},
		{Name: "xxd", Version: "2:8.2.3995-1ubuntu2", Summary: "tool to make (or reverse) a hex dump"},
	}, got)
}

func TestParseAptCacheSearch(t *testing.T) {
	in := "vim - Vi IMproved - enhanced vi editor\nxxd - make a hexdump\nnodashline\n"
	got, err := parseAptCacheSearch(strings.NewReader(in))
	require.NoError(t, err)
	// Only the first " - " splits name from summary, so summaries may contain " - ".
	assert.Equal(t, []SearchResult{
		{Name: "vim", Summary: "Vi IMproved - enhanced vi editor"},
		{Name: "xxd", Summary: "make a hexdump"},
	}, got)
}

func TestParseRpmSearch(t *testing.T) {
	in := "====== Name Exactly Matched: vim ======\n" +
		"vim-enhanced.x86_64 : A version of the VIM editor\n" +
		"====== Name & Summary Matched: vim ======\n" +
		"python3.11-foo.noarch : A library\n"
	got, err := parseRpmSearch(strings.NewReader(in))
	require.NoError(t, err)
	assert.Equal(t, []SearchResult{
		{Name: "vim-enhanced", Summary: "A version of the VIM editor"},
		{Name: "python3.11-foo", Summary: "A library"},
	}, got)
}

func TestParseZypperSearch(t *testing.T) {
	in := "S | Name | Summary | Type\n" +
		"--+------+---------+--------\n" +
		"  | vim  | Vi IMproved | package\n" +
		"i | vim-data | VIM runtime files | package\n"
	got, err := parseZypperSearch(strings.NewReader(in))
	require.NoError(t, err)
	assert.Equal(t, []SearchResult{
		{Name: "vim", Summary: "Vi IMproved"},
		{Name: "vim-data", Summary: "VIM runtime files"},
	}, got)
}

func TestParsePacmanSearch(t *testing.T) {
	in := "extra/vim 9.0.2-1 [installed]\n" +
		"    Vi Improved, a programmer's text editor\n" +
		"extra/neovim 0.9.4-1\n" +
		"    Fork of Vim aiming to improve user experience\n"
	got, err := parsePacmanSearch(strings.NewReader(in))
	require.NoError(t, err)
	assert.Equal(t, []SearchResult{
		{Name: "vim", Version: "9.0.2-1", Summary: "Vi Improved, a programmer's text editor"},
		{Name: "neovim", Version: "0.9.4-1", Summary: "Fork of Vim aiming to improve user experience"},
	}, got)
}

func TestParseApkSearch(t *testing.T) {
	in := "vim-9.0.2127-r0 - The VIM editor\nbusybox-1.36.1-r5 - Size optimized toolbox\ngarbage\n"
	got, err := parseApkSearch(strings.NewReader(in))
	require.NoError(t, err)
	assert.Equal(t, []SearchResult{
		{Name: "vim", Version: "9.0.2127-r0", Summary: "The VIM editor"},
		{Name: "busybox", Version: "1.36.1-r5", Summary: "Size optimized toolbox"},
	}, got)
}

func TestParseBrewSearch(t *testing.T) {
	in := "==> Formulae\nvim\nneovim\n==> Casks\nmacvim\n"
	got, err := parseBrewSearch(strings.NewReader(in))
	require.NoError(t, err)
	assert.Equal(t, []SearchResult{
		{Name: "vim"},
		{Name: "neovim"},
		{Name: "macvim"},
	}, got)
}

func TestExitCodeAllowed(t *testing.T) {
	// A real exit-1 error, as pacman -Qu produces when nothing is upgradable.
	exitErr := exec.Command("sh", "-c", "exit 1").Run()
	require.Error(t, exitErr, "expected a non-nil exit error")

	assert.True(t, exitCodeAllowed(exitErr, []int{1}), "exit code 1 should be allowed when listed")
	assert.False(t, exitCodeAllowed(exitErr, []int{2}), "exit code 1 should not be allowed when only 2 is listed")
	assert.False(t, exitCodeAllowed(exitErr, nil), "no allowed codes means nothing is tolerated")
	// A non-exit error is never an allowed exit code.
	assert.False(t, exitCodeAllowed(errors.New("boom"), []int{1}), "a non-exit error should never be allowed")
}
