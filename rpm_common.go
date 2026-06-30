package pkgmgr

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// YUM_REPO_DIR is where rpm-based managers (dnf, yum, zypper) write repo files.
const YUM_REPO_DIR = "/etc/yum.repos.d"

// rpmBase provides the repo, signing-key, and package-listing behavior shared
// by the rpm-based managers. Each concrete manager embeds it and supplies its
// own Name, Format, Path, and action subcommands.
type rpmBase struct {
	baseManager
}

func (p *rpmBase) repoPath(name string) string {
	return filepath.Join(YUM_REPO_DIR, name+".repo")
}

// AddRepo adds a repo from a configuration string.
func (p *rpmBase) AddRepo(name, config string) error {
	return writeRepoFile(p.repoPath(name), config)
}

// AddRepoURL adds a repo whose configuration is downloaded from repoURL.
func (p *rpmBase) AddRepoURL(ctx context.Context, name, repoURL string) error {
	return downloadRepoFile(ctx, p.repoPath(name), repoURL)
}

// RemoveRepo removes a repo.
func (p *rpmBase) RemoveRepo(name string) error {
	return removeRepoFile(p.repoPath(name))
}

// GetRepo returns a repo's configuration, or "" if it does not exist.
func (p *rpmBase) GetRepo(name string) string {
	return readRepoFile(p.repoPath(name))
}

// ListRepos returns every repo file in YUM_REPO_DIR keyed by file stem. The
// value matches what GetRepo returns for that name.
func (p *rpmBase) ListRepos(ctx context.Context, args []string) (map[string]string, error) {
	return listRepoFiles(YUM_REPO_DIR, ".repo")
}

// importKey runs `rpm --import` on a key file.
func (p *rpmBase) importKey(ctx context.Context, keyFile string) error {
	rpm := findBinary("rpm")
	if rpm == "" {
		return fmt.Errorf("unable to find rpm")
	}
	return p.Command(ctx, rpm, "--import", keyFile).Run()
}

// AddRepoKey adds a key for repo package verification.
func (p *rpmBase) AddRepoKey(ctx context.Context, key string) error {
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

	err = p.importKey(ctx, name)
	os.Remove(name)
	return err
}

// AddRepoKeyFile adds a key for repo package verification from a file.
func (p *rpmBase) AddRepoKeyFile(ctx context.Context, keyFile string) error {
	return p.importKey(ctx, keyFile)
}

// AddRepoKeyURL adds a key for repo package verification from a URL.
func (p *rpmBase) AddRepoKeyURL(ctx context.Context, keyURL string) error {
	tmp, err := downloadToTemp(ctx, keyURL, "GPG")
	if err != nil {
		return err
	}
	err = p.importKey(ctx, tmp)
	os.Remove(tmp)
	return err
}

// ListInstalled returns installed packages mapped to their version. It queries
// rpm directly so the listing is consistent across dnf, yum, and zypper.
func (p *rpmBase) ListInstalled(ctx context.Context, args []string) (map[string]string, error) {
	rpm := findBinary("rpm")
	if rpm == "" {
		return nil, fmt.Errorf("unable to find rpm")
	}

	args = joinArgs(args, "-qa", "--queryformat", "%{NAME} := %|EPOCH?{%{EPOCH}:}:{}|%{VERSION}-%{RELEASE}\\n")
	return runVersionList(p.Command(ctx, rpm, args...), " := ", true)
}

// parseRpmSearch parses the "name.arch : summary" lines printed by `dnf search`
// and `yum search` into search results, skipping the "==== ... ====" section
// headers. Neither tool reports a version in its search output.
func parseRpmSearch(r io.Reader) ([]SearchResult, error) {
	var out []SearchResult
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "==") {
			continue
		}
		name, summary, ok := strings.Cut(line, " : ")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		// The arch is the final dot-separated component of the name.
		if i := strings.LastIndex(name, "."); i != -1 {
			name = name[:i]
		}
		out = append(out, SearchResult{Name: name, Summary: strings.TrimSpace(summary)})
	}
	return out, scanner.Err()
}

// parseRpmUpgradable parses the "name.arch version repo" rows printed by
// `dnf list --upgrades` and `yum list updates` into a name->version map. The
// trailing ".arch" qualifier is stripped from the package name.
func parseRpmUpgradable(r io.Reader) (map[string]string, error) {
	out := make(map[string]string)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		// Data rows have exactly three columns; headers and notices do not.
		if len(fields) != 3 {
			continue
		}
		name := fields[0]
		// The arch is always the final dot-separated component of the name.
		if i := strings.LastIndex(name, "."); i != -1 {
			name = name[:i]
		}
		out[name] = fields[1]
	}
	return out, scanner.Err()
}
