package pkgmgr

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// APK_REPOSITORIES is the single file apk reads its repositories from.
	// There is no drop-in directory, so named repos are delimited with marker
	// comments so they can be located and removed later.
	APK_REPOSITORIES = "/etc/apk/repositories"
	// APK_KEYS_DIR is where apk reads repo signing keys from.
	APK_KEYS_DIR = "/etc/apk/keys"
)

// apkVersionSplit splits an "name-version" token into the package name and its
// version (pkgver plus the -rN pkgrel), which apk concatenates with hyphens.
var apkVersionSplit = regexp.MustCompile(`^(.+)-(\d[^-]*-r\d+)$`)

type Apk struct {
	baseManager
}

// Name is the package manager's name.
func (p *Apk) Name() string {
	return "apk"
}

// Format is the package format the manager installs.
func (p *Apk) Format() string {
	return "apk"
}

// Path is the resolved path to the apk command, or "" if missing.
func (p *Apk) Path() string {
	return findBinary("apk")
}

// exec runs apk with the given args, failing if apk cannot be found.
func (p *Apk) exec(ctx context.Context, args ...string) error {
	bin := p.Path()
	if bin == "" {
		return fmt.Errorf("unable to find apk")
	}
	return p.Command(ctx, bin, args...).Run()
}

// AddRepo adds a repo from a configuration string (one repository URL per
// line). The lines are stored in APK_REPOSITORIES delimited by markers.
func (p *Apk) AddRepo(name, config string) error {
	return setMarkerSection(APK_REPOSITORIES, name, config)
}

// AddRepoURL adds repoURL as a repository line. apk repositories are plain
// URLs, so repoURL is used directly rather than downloaded.
func (p *Apk) AddRepoURL(ctx context.Context, name, repoURL string) error {
	return setMarkerSection(APK_REPOSITORIES, name, repoURL)
}

// RemoveRepo removes a repo's lines from APK_REPOSITORIES.
func (p *Apk) RemoveRepo(name string) error {
	return removeMarkerSection(APK_REPOSITORIES, name)
}

// GetRepo returns a repo's configuration, or "" if it does not exist.
func (p *Apk) GetRepo(name string) string {
	return getMarkerSection(APK_REPOSITORIES, name)
}

// ListRepos returns every named repo section in APK_REPOSITORIES mapped
// name->configuration. The value matches what GetRepo returns for that name.
// Plain repository lines that are not enclosed in named markers have no name
// and so are not included.
func (p *Apk) ListRepos(ctx context.Context, args []string) (map[string]string, error) {
	return listMarkerSections(APK_REPOSITORIES)
}

// apkKeyDest returns the key file path for a key, named from its contents so
// re-adding the same key is idempotent.
func apkKeyDest(key []byte) string {
	sum := sha256.Sum256(key)
	return filepath.Join(APK_KEYS_DIR, "pkgmgr-"+hex.EncodeToString(sum[:8])+".rsa.pub")
}

// apkWriteKey installs a repo signing key into APK_KEYS_DIR.
func apkWriteKey(key []byte) error {
	if err := os.MkdirAll(APK_KEYS_DIR, 0755); err != nil {
		return err
	}
	return os.WriteFile(apkKeyDest(key), key, 0644)
}

// AddRepoKey adds a key for repo package verification.
func (p *Apk) AddRepoKey(ctx context.Context, key string) error {
	return apkWriteKey([]byte(key))
}

// AddRepoKeyFile adds a key for repo package verification from a file.
func (p *Apk) AddRepoKeyFile(ctx context.Context, keyFile string) error {
	data, err := os.ReadFile(keyFile)
	if err != nil {
		return err
	}
	return apkWriteKey(data)
}

// AddRepoKeyURL adds a key for repo package verification from a URL.
func (p *Apk) AddRepoKeyURL(ctx context.Context, keyURL string) error {
	var buf strings.Builder
	if err := download(ctx, keyURL, &buf); err != nil {
		return err
	}
	return apkWriteKey([]byte(buf.String()))
}

// Sync updates repository metadata.
func (p *Apk) Sync(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(args, "update")...)
}

// Install installs packages from the repositories.
func (p *Apk) Install(ctx context.Context, args []string, packages ...string) error {
	return p.exec(ctx, append(joinArgs(args, "add"), packages...)...)
}

// Remove removes packages.
func (p *Apk) Remove(ctx context.Context, args []string, packages ...string) error {
	return p.exec(ctx, append(joinArgs(args, "del"), packages...)...)
}

// Upgrade upgrades the named packages.
func (p *Apk) Upgrade(ctx context.Context, args []string, packages ...string) error {
	return p.exec(ctx, append(joinArgs(args, "add", "--upgrade"), packages...)...)
}

// InstallFile installs a package from a local file.
func (p *Apk) InstallFile(ctx context.Context, args []string, packages ...string) error {
	return p.Install(ctx, args, packages...)
}

// UpgradeAll upgrades all packages with available updates.
func (p *Apk) UpgradeAll(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(args, "upgrade")...)
}

// Clean removes cached package data. It requires a configured apk cache.
func (p *Apk) Clean(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(args, "cache", "clean")...)
}

// ListUpgradable returns packages with an available update mapped to the
// candidate version.
func (p *Apk) ListUpgradable(ctx context.Context, args []string) (map[string]string, error) {
	bin := p.Path()
	if bin == "" {
		return nil, fmt.Errorf("unable to find apk")
	}

	// `apk version -l '<'` lists installed packages older than the repositories
	// offer, as "name-oldver < newver".
	args = joinArgs(args, "version", "-l", "<")
	return runParseList(p.Command(ctx, bin, args...), parseApkUpgradable)
}

// parseApkUpgradable parses apk's "name-oldver < newver" rows into a
// name->candidate-version map.
func parseApkUpgradable(r io.Reader) (map[string]string, error) {
	out := make(map[string]string)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		// Skip the header and any malformed rows; data rows have the "<" marker.
		if len(fields) < 3 || fields[1] != "<" {
			continue
		}
		m := apkVersionSplit.FindStringSubmatch(fields[0])
		if m == nil {
			continue
		}
		out[m[1]] = fields[2]
	}
	return out, scanner.Err()
}

// Search searches the repositories for packages matching query.
func (p *Apk) Search(ctx context.Context, args []string, query string) ([]SearchResult, error) {
	bin := p.Path()
	if bin == "" {
		return nil, fmt.Errorf("unable to find apk")
	}
	// `apk search -v` prints "name-version - description" per match.
	return runSearch(p.Command(ctx, bin, joinArgs(args, "search", "-v", query)...), parseApkSearch)
}

// parseApkSearch parses apk's "name-version - description" lines into search
// results.
func parseApkSearch(r io.Reader) ([]SearchResult, error) {
	var out []SearchResult
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		token, summary, _ := strings.Cut(scanner.Text(), " - ")
		m := apkVersionSplit.FindStringSubmatch(strings.TrimSpace(token))
		if m == nil {
			continue
		}
		out = append(out, SearchResult{Name: m[1], Version: m[2], Summary: strings.TrimSpace(summary)})
	}
	return out, scanner.Err()
}

// Info returns detailed information about the named packages.
func (p *Apk) Info(ctx context.Context, args []string, packages ...string) (string, error) {
	bin := p.Path()
	if bin == "" {
		return "", fmt.Errorf("unable to find apk")
	}
	return runCapture(p.Command(ctx, bin, append(joinArgs(args, "info"), packages...)...))
}

// ListInstalled returns installed packages mapped to their version.
func (p *Apk) ListInstalled(ctx context.Context, args []string) (map[string]string, error) {
	bin := p.Path()
	if bin == "" {
		return nil, fmt.Errorf("unable to find apk")
	}

	// `apk info -v` prints one "name-version" token per line.
	args = joinArgs(args, "info", "-v")
	cmd := p.Command(ctx, bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	out, perr := parseApkList(stdout)
	if werr := cmd.Wait(); werr != nil {
		return nil, werr
	}
	if perr != nil {
		return nil, perr
	}
	return out, nil
}

// parseApkList parses "name-version" tokens from apk into a name->version map.
func parseApkList(r io.Reader) (map[string]string, error) {
	out := make(map[string]string)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		m := apkVersionSplit.FindStringSubmatch(fields[0])
		if m == nil {
			continue
		}
		out[m[1]] = m[2]
	}
	return out, scanner.Err()
}

// markerSection returns the start and end comment markers for a named section.
func markerSection(name string) (string, string) {
	return "# >>> " + name, "# <<< " + name
}

// getMarkerSection returns the body between a named section's markers, or ""
// when the section or file is absent.
func getMarkerSection(file, name string) string {
	data, err := os.ReadFile(file)
	if err != nil {
		return ""
	}

	start, end := markerSection(name)
	var body []string
	in, found := false, false
	for _, line := range strings.Split(string(data), "\n") {
		switch strings.TrimSpace(line) {
		case start:
			in, found = true, true
			continue
		case end:
			in = false
			continue
		}
		if in {
			body = append(body, line)
		}
	}
	if !found {
		return ""
	}
	return strings.TrimSpace(strings.Join(body, "\n"))
}

// listMarkerSections returns every named marker section in file mapped
// name->body. Each body matches getMarkerSection. A missing file yields an empty
// result.
func listMarkerSections(file string) (map[string]string, error) {
	out := make(map[string]string)
	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}

	const startPrefix = "# >>> "
	var name string
	var body []string
	flush := func() {
		if name != "" {
			out[name] = strings.TrimSpace(strings.Join(body, "\n"))
		}
		name, body = "", nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, startPrefix):
			flush()
			name = strings.TrimPrefix(t, startPrefix)
		case strings.HasPrefix(t, "# <<< "):
			flush()
		default:
			if name != "" {
				body = append(body, line)
			}
		}
	}
	flush()
	return out, nil
}

// removeMarkerSection removes a named section (markers and body) from file,
// treating a missing file as success.
func removeMarkerSection(file, name string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	start, end := markerSection(name)
	var out []string
	skip := false
	for _, line := range strings.Split(string(data), "\n") {
		switch strings.TrimSpace(line) {
		case start:
			skip = true
			continue
		case end:
			skip = false
			continue
		}
		if skip {
			continue
		}
		out = append(out, line)
	}
	return os.WriteFile(file, []byte(strings.Join(out, "\n")), 0644)
}

// setMarkerSection inserts or replaces a named section in file with body.
func setMarkerSection(file, name, body string) error {
	if err := removeMarkerSection(file, name); err != nil {
		return err
	}

	data, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	start, end := markerSection(name)
	var b strings.Builder
	existing := strings.TrimRight(string(data), "\n")
	if existing != "" {
		b.WriteString(existing)
		b.WriteString("\n")
	}
	b.WriteString(start + "\n")
	if body = strings.TrimRight(body, "\n"); body != "" {
		b.WriteString(body + "\n")
	}
	b.WriteString(end + "\n")
	return os.WriteFile(file, []byte(b.String()), 0644)
}
