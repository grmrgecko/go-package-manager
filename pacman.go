package pkgmgr

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// PACMAN_CONF is the configuration file pacman reads its repos from. pacman has
// no drop-in repo directory by default, so repos are managed as [name] sections
// within this file.
const PACMAN_CONF = "/etc/pacman.conf"

type Pacman struct {
	baseManager
}

// Name is the package manager's name.
func (p *Pacman) Name() string {
	return "pacman"
}

// Format is the package format the manager installs.
func (p *Pacman) Format() string {
	return "pkg"
}

// Path is the resolved path to the pacman command, or "" if missing.
func (p *Pacman) Path() string {
	return findBinary("pacman")
}

// exec runs pacman with the given args, failing if pacman cannot be found.
func (p *Pacman) exec(ctx context.Context, args ...string) error {
	bin := p.Path()
	if bin == "" {
		return fmt.Errorf("unable to find pacman")
	}
	return p.Command(ctx, bin, args...).Run()
}

// AddRepo adds a repo as a [name] section in PACMAN_CONF.
func (p *Pacman) AddRepo(name, config string) error {
	return setIniSection(PACMAN_CONF, name, config)
}

// AddRepoURL adds a repo whose configuration is downloaded from repoURL.
func (p *Pacman) AddRepoURL(ctx context.Context, name, repoURL string) error {
	var buf strings.Builder
	if err := download(ctx, repoURL, &buf); err != nil {
		return err
	}
	return setIniSection(PACMAN_CONF, name, buf.String())
}

// RemoveRepo removes a repo's section from PACMAN_CONF.
func (p *Pacman) RemoveRepo(name string) error {
	return removeIniSection(PACMAN_CONF, name)
}

// GetRepo returns a repo's configuration, or "" if it does not exist.
func (p *Pacman) GetRepo(name string) string {
	return getIniSection(PACMAN_CONF, name)
}

// ListRepos returns every repository section in PACMAN_CONF mapped name->body.
// The value matches what GetRepo returns for that name. pacman treats every
// section other than [options] as a repository, so [options] is excluded.
func (p *Pacman) ListRepos(ctx context.Context, args []string) (map[string]string, error) {
	return listIniSections(PACMAN_CONF, "options")
}

// pacmanKey runs pacman-key with the given args.
func (p *Pacman) pacmanKey(ctx context.Context, args ...string) error {
	bin := findBinary("pacman-key")
	if bin == "" {
		return fmt.Errorf("unable to find pacman-key")
	}
	return p.Command(ctx, bin, args...).Run()
}

// AddRepoKey adds a key for repo package verification.
func (p *Pacman) AddRepoKey(ctx context.Context, key string) error {
	fd, err := os.CreateTemp("", "GPG")
	if err != nil {
		return err
	}
	name := fd.Name()
	_, werr := fd.WriteString(key)
	cerr := fd.Close()
	if werr != nil {
		os.Remove(name)
		return werr
	}
	if cerr != nil {
		os.Remove(name)
		return cerr
	}

	err = p.pacmanKey(ctx, "--add", name)
	os.Remove(name)
	return err
}

// AddRepoKeyFile adds a key for repo package verification from a file.
func (p *Pacman) AddRepoKeyFile(ctx context.Context, keyFile string) error {
	return p.pacmanKey(ctx, "--add", keyFile)
}

// AddRepoKeyURL adds a key for repo package verification from a URL.
func (p *Pacman) AddRepoKeyURL(ctx context.Context, keyURL string) error {
	tmp, err := downloadToTemp(ctx, keyURL, "GPG")
	if err != nil {
		return err
	}
	err = p.pacmanKey(ctx, "--add", tmp)
	os.Remove(tmp)
	return err
}

// Sync updates repository metadata.
func (p *Pacman) Sync(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(args, "-Sy")...)
}

// Install installs packages from the repositories.
func (p *Pacman) Install(ctx context.Context, args []string, packages ...string) error {
	return p.exec(ctx, append(joinArgs(args, "-S"), packages...)...)
}

// Remove removes packages.
func (p *Pacman) Remove(ctx context.Context, args []string, packages ...string) error {
	return p.exec(ctx, append(joinArgs(args, "-R"), packages...)...)
}

// Upgrade upgrades the named packages.
func (p *Pacman) Upgrade(ctx context.Context, args []string, packages ...string) error {
	return p.Install(ctx, args, packages...)
}

// InstallFile installs a package from a local file.
func (p *Pacman) InstallFile(ctx context.Context, args []string, packages ...string) error {
	return p.exec(ctx, append(joinArgs(args, "-U"), packages...)...)
}

// UpgradeAll upgrades all packages with available updates.
func (p *Pacman) UpgradeAll(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(args, "-Syu")...)
}

// Clean removes cached packages from the package cache.
func (p *Pacman) Clean(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(args, "-Sc")...)
}

// ListUpgradable returns packages with an available update mapped to the
// candidate version.
func (p *Pacman) ListUpgradable(ctx context.Context, args []string) (map[string]string, error) {
	bin := p.Path()
	if bin == "" {
		return nil, fmt.Errorf("unable to find pacman")
	}

	// `pacman -Qu` prints "name oldver -> newver" and exits 1 when nothing is
	// upgradable, which is treated as an empty result rather than an error.
	args = joinArgs(args, "-Qu")
	return runParseList(p.Command(ctx, bin, args...), parsePacmanUpgradable, 1)
}

// parsePacmanUpgradable parses the "name oldver -> newver" rows printed by
// pacman and its AUR-helper drop-ins into a name->candidate-version map.
func parsePacmanUpgradable(r io.Reader) (map[string]string, error) {
	out := make(map[string]string)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		// Rows are "name oldver -> newver"; the candidate follows the arrow.
		if len(fields) < 4 || fields[2] != "->" {
			continue
		}
		out[fields[0]] = fields[3]
	}
	return out, scanner.Err()
}

// Search searches the repositories for packages matching query.
func (p *Pacman) Search(ctx context.Context, args []string, query string) ([]SearchResult, error) {
	bin := p.Path()
	if bin == "" {
		return nil, fmt.Errorf("unable to find pacman")
	}
	return runSearch(p.Command(ctx, bin, joinArgs(args, "-Ss", query)...), parsePacmanSearch)
}

// parsePacmanSearch parses the "repo/name version" headers and indented
// descriptions printed by `pacman -Ss` and its AUR-helper drop-ins into search
// results.
func parsePacmanSearch(r io.Reader) ([]SearchResult, error) {
	return parseColumnarSearch(r, func(fields []string) (string, string, bool) {
		// Header rows look like "repo/name version [tags]".
		if len(fields) < 2 || !strings.Contains(fields[0], "/") {
			return "", "", false
		}
		_, name, _ := strings.Cut(fields[0], "/")
		return name, fields[1], true
	})
}

// Info returns detailed information about the named packages.
func (p *Pacman) Info(ctx context.Context, args []string, packages ...string) (string, error) {
	bin := p.Path()
	if bin == "" {
		return "", fmt.Errorf("unable to find pacman")
	}
	return runCapture(p.Command(ctx, bin, append(joinArgs(args, "-Si"), packages...)...))
}

// ListInstalled returns installed packages mapped to their version.
func (p *Pacman) ListInstalled(ctx context.Context, args []string) (map[string]string, error) {
	bin := p.Path()
	if bin == "" {
		return nil, fmt.Errorf("unable to find pacman")
	}

	// `pacman -Q` prints one "name version" pair per line.
	args = joinArgs(args, "-Q")
	return runVersionList(p.Command(ctx, bin, args...), " ", false)
}

// isIniHeader reports whether line is the [name] section header.
func isIniHeader(line, name string) bool {
	return strings.TrimSpace(line) == "["+name+"]"
}

// isAnyIniHeader reports whether line is any [section] header.
func isAnyIniHeader(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]")
}

// getIniSection returns the body of the [name] section in file, or "" when the
// section or file is absent. The header line itself is not included.
func getIniSection(file, name string) string {
	data, err := os.ReadFile(file)
	if err != nil {
		return ""
	}

	var body []string
	found, in := false, false
	for _, line := range strings.Split(string(data), "\n") {
		if isAnyIniHeader(line) {
			if in {
				break
			}
			in = isIniHeader(line, name)
			found = found || in
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

// listIniSections returns every [section] in file mapped name->body, excluding
// any section whose name is in exclude. Each body matches getIniSection. A
// missing file yields an empty result.
func listIniSections(file string, exclude ...string) (map[string]string, error) {
	out := make(map[string]string)
	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}

	skip := make(map[string]bool, len(exclude))
	for _, name := range exclude {
		skip[name] = true
	}

	var name string
	var body []string
	flush := func() {
		if name != "" && !skip[name] {
			out[name] = strings.TrimSpace(strings.Join(body, "\n"))
		}
		name, body = "", nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		if isAnyIniHeader(line) {
			flush()
			t := strings.TrimSpace(line)
			name = strings.TrimSuffix(strings.TrimPrefix(t, "["), "]")
			continue
		}
		if name != "" {
			body = append(body, line)
		}
	}
	flush()
	return out, nil
}

// removeIniSection removes the [name] section (header and body) from file,
// treating a missing file as success.
func removeIniSection(file, name string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var out []string
	skip := false
	for _, line := range strings.Split(string(data), "\n") {
		if isAnyIniHeader(line) {
			skip = isIniHeader(line, name)
		}
		if skip {
			continue
		}
		out = append(out, line)
	}
	return os.WriteFile(file, []byte(strings.Join(out, "\n")), 0644)
}

// setIniSection inserts or replaces the [name] section in file with body.
func setIniSection(file, name, body string) error {
	if err := removeIniSection(file, name); err != nil {
		return err
	}

	data, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	var b strings.Builder
	existing := strings.TrimRight(string(data), "\n")
	if existing != "" {
		b.WriteString(existing)
		b.WriteString("\n\n")
	}
	b.WriteString("[" + name + "]\n")
	if body = strings.TrimRight(body, "\n"); body != "" {
		b.WriteString(body)
		b.WriteString("\n")
	}
	return os.WriteFile(file, []byte(b.String()), 0644)
}
