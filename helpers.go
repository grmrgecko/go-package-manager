package pkgmgr

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// downloadAttempts is the number of times a download is retried before failing.
const downloadAttempts = 3

// downloadRetryDelay is how long to wait between download attempts.
var downloadRetryDelay = 10 * time.Second

// binaryFallbackDirs are the directories searched, in order, when a command is
// not found on PATH. They cover the common locations package managers and their
// helpers install to but that a reduced PATH (cron, system services) may omit.
var binaryFallbackDirs = []string{"/usr/bin", "/usr/local/bin", "/opt/homebrew/bin"}

// findBinary looks up a command by name in PATH, falling back to a fixed set of
// well-known directories. It returns an empty string when the command cannot be
// found.
func findBinary(name string) string {
	// First find the path in the environment.
	if p, err := exec.LookPath(name); err == nil {
		return p
	}

	// If the path isn't in the environment, check the well-known directories.
	for _, dir := range binaryFallbackDirs {
		fallback := filepath.Join(dir, name)
		if _, err := os.Stat(fallback); err == nil {
			return fallback
		}
	}

	// The binary could not be found.
	return ""
}

// download fetches url, retrying on transient failures, and writes the
// response body to w. It honors cancellation via ctx and treats any non-2xx
// HTTP status as a failure so error pages are never mistaken for content.
func download(ctx context.Context, url string, w io.Writer) error {
	client := &http.Client{Timeout: 120 * time.Second}

	var lastErr error
	for tries := 0; tries < downloadAttempts; tries++ {
		// Back off between attempts, but bail out early if cancelled.
		if tries != 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(downloadRetryDelay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}

		// Perform the request.
		res, err := client.Do(req)
		if err != nil {
			lastErr = err
			log.Debugf("failed to fetch %s, trying again: %v", url, err)
			continue
		}

		// Reject error pages so they are not written as real content.
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			res.Body.Close()
			lastErr = fmt.Errorf("unexpected status %s fetching %s", res.Status, url)
			log.Printf("%v, trying again", lastErr)
			continue
		}

		// Copy the body to the destination writer.
		_, err = io.Copy(w, res.Body)
		res.Body.Close()
		return err
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("failed to download %s", url)
	}
	return lastErr
}

// downloadToTemp downloads url into a new temporary file and returns its path.
// The caller is responsible for removing the file.
func downloadToTemp(ctx context.Context, url, pattern string) (string, error) {
	fd, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}

	if err := download(ctx, url, fd); err != nil {
		fd.Close()
		os.Remove(fd.Name())
		return "", err
	}

	if err := fd.Close(); err != nil {
		os.Remove(fd.Name())
		return "", err
	}
	return fd.Name(), nil
}

// joinArgs returns a new slice of args followed by extra, leaving the caller's
// args slice untouched (plain append can mutate a shared backing array).
func joinArgs(args []string, extra ...string) []string {
	out := make([]string, 0, len(args)+len(extra))
	out = append(out, args...)
	out = append(out, extra...)
	return out
}

// writeRepoFile writes config to filePath, creating parent directories.
func writeRepoFile(filePath, config string) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return err
	}
	return os.WriteFile(filePath, []byte(config), 0644)
}

// downloadRepoFile downloads repoURL into filePath, creating parent
// directories and cleaning up a partial file on failure.
func downloadRepoFile(ctx context.Context, filePath, repoURL string) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return err
	}

	fd, err := os.Create(filePath)
	if err != nil {
		return err
	}

	if err := download(ctx, repoURL, fd); err != nil {
		fd.Close()
		os.Remove(filePath)
		return err
	}
	return fd.Close()
}

// removeRepoFile removes filePath, treating a missing file as success.
func removeRepoFile(filePath string) error {
	err := os.Remove(filePath)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// readRepoFile returns the contents of filePath, or "" if it cannot be read.
func readRepoFile(filePath string) string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}
	return string(data)
}

// listRepoFiles enumerates the files in dir whose names end in suffix and
// returns them keyed by filename stem, with each value holding the file's
// contents. It backs the ListRepos implementations for managers that store each
// repo as its own file (apt's .list, rpm's .repo). A missing directory yields an
// empty result. The value is read with readRepoFile so a single unreadable file
// does not fail the whole listing.
func listRepoFiles(dir, suffix string) (map[string]string, error) {
	out := make(map[string]string)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, suffix) {
			continue
		}
		stem := strings.TrimSuffix(name, suffix)
		out[stem] = readRepoFile(filepath.Join(dir, name))
	}
	return out, nil
}

// parseVersionList reads "name<sep>version" lines from r and returns them as a
// map. When trimEpoch is set, a leading "0:" epoch is stripped from versions.
func parseVersionList(r io.Reader, sep string, trimEpoch bool) (map[string]string, error) {
	out := make(map[string]string)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		name, version, ok := strings.Cut(scanner.Text(), sep)
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		version = strings.TrimSpace(version)
		if trimEpoch {
			version = strings.TrimPrefix(version, "0:")
		}
		if name == "" {
			continue
		}
		out[name] = version
	}
	return out, scanner.Err()
}

// exitCodeAllowed reports whether err is an exec exit error whose status code is
// one of the allowed codes. It lets callers tolerate the non-zero exit some
// managers use to signal "nothing to report" (e.g. pacman -Qu exits 1).
func exitCodeAllowed(err error, allowed []int) bool {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return false
	}
	return slices.Contains(allowed, ee.ExitCode())
}

// runParse runs cmd and parses its stdout with parse, returning the result. It
// wires the command's stdout to a pipe regardless of any configured output
// writer so the listing can be captured. Exit codes in okExit are treated as
// success, which accommodates managers that exit non-zero when there is nothing
// to list.
func runParse[T any](cmd *exec.Cmd, parse func(io.Reader) (T, error), okExit ...int) (T, error) {
	var zero T

	// StdoutPipe requires Stdout be unset; the output is captured through the
	// pipe here, so clear any writer a command builder may have attached.
	cmd.Stdout = nil
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return zero, err
	}

	if err := cmd.Start(); err != nil {
		return zero, err
	}

	out, perr := parse(stdout)
	if werr := cmd.Wait(); werr != nil && !exitCodeAllowed(werr, okExit) {
		return zero, werr
	}
	if perr != nil {
		return zero, perr
	}
	return out, nil
}

// runParseList runs cmd and parses its stdout into a name->value map.
func runParseList(cmd *exec.Cmd, parse func(io.Reader) (map[string]string, error), okExit ...int) (map[string]string, error) {
	return runParse(cmd, parse, okExit...)
}

// runVersionList runs cmd, parses its stdout as a "name<sep>version" listing,
// and returns the resulting map.
func runVersionList(cmd *exec.Cmd, sep string, trimEpoch bool) (map[string]string, error) {
	return runParseList(cmd, func(r io.Reader) (map[string]string, error) {
		return parseVersionList(r, sep, trimEpoch)
	})
}

// runSearch runs cmd and parses its stdout into a slice of search results.
func runSearch(cmd *exec.Cmd, parse func(io.Reader) ([]SearchResult, error)) ([]SearchResult, error) {
	return runParse(cmd, parse)
}

// runCapture runs cmd and returns its stdout as a string, overriding any
// configured stdout writer so the output can be returned to the caller.
func runCapture(cmd *exec.Cmd) (string, error) {
	var buf bytes.Buffer
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// parseColumnarSearch parses the "<header line> then indented description"
// search layout shared by apt and pacman. nameVer extracts the package name and
// version from a header line's fields, returning ok=false for non-header lines;
// the first indented line that follows a header becomes that entry's summary.
func parseColumnarSearch(r io.Reader, nameVer func(fields []string) (name, version string, ok bool)) ([]SearchResult, error) {
	var out []SearchResult
	var cur *SearchResult
	flush := func() {
		if cur != nil {
			out = append(out, *cur)
			cur = nil
		}
	}

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		// Indented lines describe the current entry.
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			if cur != nil && cur.Summary == "" {
				cur.Summary = strings.TrimSpace(line)
			}
			continue
		}
		name, version, ok := nameVer(strings.Fields(line))
		if !ok {
			continue
		}
		flush()
		cur = &SearchResult{Name: name, Version: version}
	}
	flush()
	return out, scanner.Err()
}
