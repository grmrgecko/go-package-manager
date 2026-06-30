package pkgmgr

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

type Zypper struct {
	rpmBase
}

// Name is the package manager's name.
func (p *Zypper) Name() string {
	return "zypper"
}

// Format is the package format the manager installs.
func (p *Zypper) Format() string {
	return "rpm"
}

// Path is the resolved path to the zypper command, or "" if missing.
func (p *Zypper) Path() string {
	return findBinary("zypper")
}

// exec runs zypper with the given args, failing if zypper cannot be found.
func (p *Zypper) exec(ctx context.Context, args ...string) error {
	bin := p.Path()
	if bin == "" {
		return fmt.Errorf("unable to find zypper")
	}
	return p.Command(ctx, bin, args...).Run()
}

// Sync updates repository metadata.
func (p *Zypper) Sync(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(args, "refresh")...)
}

// Install installs packages from the repositories.
func (p *Zypper) Install(ctx context.Context, args []string, packages ...string) error {
	return p.exec(ctx, append(joinArgs(args, "install"), packages...)...)
}

// Remove removes packages.
func (p *Zypper) Remove(ctx context.Context, args []string, packages ...string) error {
	return p.exec(ctx, append(joinArgs(args, "remove"), packages...)...)
}

// Upgrade upgrades the named packages.
func (p *Zypper) Upgrade(ctx context.Context, args []string, packages ...string) error {
	return p.exec(ctx, append(joinArgs(args, "update"), packages...)...)
}

// InstallFile installs a package from a local file.
func (p *Zypper) InstallFile(ctx context.Context, args []string, packages ...string) error {
	return p.Install(ctx, args, packages...)
}

// UpgradeAll upgrades all packages with available updates.
func (p *Zypper) UpgradeAll(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(args, "update")...)
}

// Clean removes cached repository metadata and packages.
func (p *Zypper) Clean(ctx context.Context, args []string) error {
	return p.exec(ctx, joinArgs(args, "clean")...)
}

// ListUpgradable returns packages with an available update mapped to the
// candidate version.
func (p *Zypper) ListUpgradable(ctx context.Context, args []string) (map[string]string, error) {
	bin := p.Path()
	if bin == "" {
		return nil, fmt.Errorf("unable to find zypper")
	}

	// `zypper -q list-updates` prints a pipe-delimited table of available updates.
	args = joinArgs(args, "-q", "list-updates")
	return runParseList(p.Command(ctx, bin, args...), parseZypperUpgradable)
}

// parseZypperUpgradable parses zypper's pipe-delimited update table into a
// name->available-version map. Rows are "S | Repo | Name | Current | Available
// | Arch"; the header and separator rows are skipped.
func parseZypperUpgradable(r io.Reader) (map[string]string, error) {
	out := make(map[string]string)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "|") {
			continue
		}
		cols := strings.Split(line, "|")
		if len(cols) < 5 {
			continue
		}
		for i := range cols {
			cols[i] = strings.TrimSpace(cols[i])
		}
		// Skip the header row, identified by its literal "S" status column.
		if cols[0] == "S" || cols[2] == "Name" {
			continue
		}
		if cols[2] == "" {
			continue
		}
		out[cols[2]] = cols[4]
	}
	return out, scanner.Err()
}

// Search searches the repositories for packages matching query.
func (p *Zypper) Search(ctx context.Context, args []string, query string) ([]SearchResult, error) {
	bin := p.Path()
	if bin == "" {
		return nil, fmt.Errorf("unable to find zypper")
	}
	return runSearch(p.Command(ctx, bin, joinArgs(args, "search", query)...), parseZypperSearch)
}

// parseZypperSearch parses zypper's pipe-delimited search table into search
// results. Rows are "S | Name | Summary | Type"; the default search output does
// not report a version.
func parseZypperSearch(r io.Reader) ([]SearchResult, error) {
	var out []SearchResult
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "|") {
			continue
		}
		cols := strings.Split(line, "|")
		if len(cols) < 3 {
			continue
		}
		for i := range cols {
			cols[i] = strings.TrimSpace(cols[i])
		}
		// Skip the header row and any blank-name rows.
		if cols[1] == "Name" || cols[1] == "" {
			continue
		}
		out = append(out, SearchResult{Name: cols[1], Summary: cols[2]})
	}
	return out, scanner.Err()
}

// Info returns detailed information about the named packages.
func (p *Zypper) Info(ctx context.Context, args []string, packages ...string) (string, error) {
	bin := p.Path()
	if bin == "" {
		return "", fmt.Errorf("unable to find zypper")
	}
	return runCapture(p.Command(ctx, bin, append(joinArgs(args, "info"), packages...)...))
}
