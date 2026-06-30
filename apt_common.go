package pkgmgr

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// APT_SOURCES_DIR is where apt repo files are written.
	APT_SOURCES_DIR = "/etc/apt/sources.list.d"
	// APT_KEYRINGS_DIR is where apt repo signing keys are written when apt-key
	// is unavailable. It is only used as a fallback on modern systems (apt has
	// removed apt-key), so the armored ".asc" form written here is always read
	// by the apt that is present.
	APT_KEYRINGS_DIR = "/etc/apt/trusted.gpg.d"
)

// aptBase provides the repo and signing-key behavior shared by the apt and
// apt-get managers. Each concrete manager embeds it and supplies its own Name,
// Format, Path, and action subcommands.
type aptBase struct {
	baseManager
}

func (p *aptBase) repoPath(name string) string {
	return filepath.Join(APT_SOURCES_DIR, name+".list")
}

// AddRepo adds a repo from a configuration string.
func (p *aptBase) AddRepo(name, config string) error {
	return writeRepoFile(p.repoPath(name), config)
}

// AddRepoURL adds a repo whose configuration is downloaded from repoURL.
func (p *aptBase) AddRepoURL(ctx context.Context, name, repoURL string) error {
	return downloadRepoFile(ctx, p.repoPath(name), repoURL)
}

// RemoveRepo removes a repo.
func (p *aptBase) RemoveRepo(name string) error {
	return removeRepoFile(p.repoPath(name))
}

// GetRepo returns a repo's configuration, or "" if it does not exist.
func (p *aptBase) GetRepo(name string) string {
	return readRepoFile(p.repoPath(name))
}

// ListRepos returns every repo file in APT_SOURCES_DIR keyed by file stem. The
// value matches what GetRepo returns for that name. Only the .list drop-in files
// this manager reads and writes are enumerated; the main sources.list and
// deb822 .sources files are not included.
func (p *aptBase) ListRepos(ctx context.Context, args []string) (map[string]string, error) {
	return listRepoFiles(APT_SOURCES_DIR, ".list")
}

// AddRepoKey adds a key for repo package verification. apt-key is used when it
// is present, which keeps older releases (where it is the only supported
// method) working. On modern systems that have removed apt-key, a drop-in
// keyring file is written instead.
func (p *aptBase) AddRepoKey(ctx context.Context, key string) error {
	if aptKey := findBinary("apt-key"); aptKey != "" {
		return p.aptKeyAdd(ctx, aptKey, strings.NewReader(key))
	}
	return aptWriteKey([]byte(key))
}

// AddRepoKeyFile adds a key for repo package verification from a file.
func (p *aptBase) AddRepoKeyFile(ctx context.Context, keyFile string) error {
	if aptKey := findBinary("apt-key"); aptKey != "" {
		return p.Command(ctx, aptKey, "add", keyFile).Run()
	}
	data, err := os.ReadFile(keyFile)
	if err != nil {
		return err
	}
	return aptWriteKey(data)
}

// AddRepoKeyURL adds a key for repo package verification from a URL.
func (p *aptBase) AddRepoKeyURL(ctx context.Context, keyURL string) error {
	var buf bytes.Buffer
	if err := download(ctx, keyURL, &buf); err != nil {
		return err
	}
	if aptKey := findBinary("apt-key"); aptKey != "" {
		return p.aptKeyAdd(ctx, aptKey, &buf)
	}
	return aptWriteKey(buf.Bytes())
}

// aptKeyAdd runs `apt-key add -`, feeding the key in through stdin.
func (p *aptBase) aptKeyAdd(ctx context.Context, aptKey string, key io.Reader) error {
	cmd := p.Command(ctx, aptKey, "add", "-")
	cmd.Stdin = key
	return cmd.Run()
}

// aptKeyDest returns the keyring file path for a key. The fallback path only
// runs on modern apt, which reads both armored (.asc) and binary (.gpg) keys,
// so the suffix is chosen from the key's contents. The name is derived from the
// key so re-adding the same key is idempotent.
func aptKeyDest(key []byte) string {
	sum := sha256.Sum256(key)
	name := "pkgmgr-" + hex.EncodeToString(sum[:8])
	ext := ".gpg"
	if bytes.Contains(key, []byte("-----BEGIN PGP")) {
		ext = ".asc"
	}
	return filepath.Join(APT_KEYRINGS_DIR, name+ext)
}

// aptWriteKey installs a repo signing key into APT_KEYRINGS_DIR.
func aptWriteKey(key []byte) error {
	if err := os.MkdirAll(APT_KEYRINGS_DIR, 0755); err != nil {
		return err
	}
	return os.WriteFile(aptKeyDest(key), key, 0644)
}

// ListUpgradable returns packages with an available update mapped to the
// candidate version. It simulates an upgrade with apt-get, which both apt and
// apt-get ship, so the listing is consistent and never prompts.
func (p *aptBase) ListUpgradable(ctx context.Context, args []string) (map[string]string, error) {
	aptGet := findBinary("apt-get")
	if aptGet == "" {
		return nil, fmt.Errorf("unable to find apt-get")
	}

	// `apt-get -s upgrade` prints an "Inst <name> [old] (<new> ...)" line for
	// each package that would be upgraded, without changing the system.
	args = joinArgs(args, "-s", "upgrade")
	return runParseList(p.Command(ctx, aptGet, args...), parseAptUpgradable)
}

// parseAptUpgradable parses the "Inst <name> [old] (<new> ...)" lines from a
// simulated apt-get upgrade into a name->candidate-version map.
func parseAptUpgradable(r io.Reader) (map[string]string, error) {
	out := make(map[string]string)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 || fields[0] != "Inst" {
			continue
		}
		// The candidate version is the parenthesized token after the name.
		for _, f := range fields[2:] {
			if strings.HasPrefix(f, "(") {
				out[fields[1]] = strings.TrimPrefix(f, "(")
				break
			}
		}
	}
	return out, scanner.Err()
}
