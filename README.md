# go-package-manager

[![Go Reference](https://pkg.go.dev/badge/github.com/grmrgecko/go-package-manager.svg)](https://pkg.go.dev/github.com/grmrgecko/go-package-manager)

`pkgmgr` is a Go library that abstracts system package managers behind a single
[`Manager`](managers.go) interface. One code path can install, upgrade, search,
and manage repositories on Debian, Fedora, openSUSE, Arch, Alpine, macOS, and
more, without each caller hard-coding `apt`/`dnf`/`pacman` invocations.

API documentation is available on [pkg.go.dev](https://pkg.go.dev/github.com/grmrgecko/go-package-manager).

## Supported managers

| Manager   | Type      | Format  | Notes                                            |
| --------- | --------- | ------- | ------------------------------------------------ |
| zypper    | root      | rpm     | openSUSE                                         |
| dnf       | root      | rpm     | Fedora / RHEL                                    |
| yum       | root      | rpm     | Legacy RHEL                                      |
| apt       | root      | deb     | Debian / Ubuntu                                  |
| apt-get   | root      | deb     | Debian / Ubuntu                                  |
| pacman    | root      | pacman  | Arch                                             |
| apk       | root      | apk     | Alpine                                           |
| brew      | unpriv.   | bottle  | Homebrew; refuses to run as root                 |
| yay       | unpriv.   | pacman  | AUR helper; pacman drop-in                       |
| paru      | unpriv.   | pacman  | AUR helper; pacman drop-in                       |

Root-type managers escalate privileged commands through a configurable wrapper
(`sudo` by default). Homebrew and the AUR helpers must not run as root, so they
bypass the wrapper and instead drop to an unprivileged user via `sudo -u` when
the process is running as root.

## Installation

```bash
go get github.com/grmrgecko/go-package-manager
```

## Usage

Resolve the active system manager, then drive it through the interface. The
example below installs a package, escalating with `sudo` when not already root.

```go
package main

import (
	"context"
	"log"

	pkgmgr "github.com/grmrgecko/go-package-manager"
)

func main() {
	m := pkgmgr.GetSystemManager()
	if m == nil {
		log.Fatal("no supported package manager found")
	}

	// Wrap privileged commands with sudo when not running as root.
	m.UseSudoWhenNeeded()

	ctx := context.Background()
	if err := m.Sync(ctx, nil); err != nil {
		log.Fatal(err)
	}
	if err := m.Install(ctx, nil, "htop"); err != nil {
		log.Fatal(err)
	}

	results, err := m.Search(ctx, nil, "editor")
	if err != nil {
		log.Fatal(err)
	}
	for _, r := range results {
		log.Printf("%s %s - %s", r.Name, r.Version, r.Summary)
	}
}
```

### Selecting a manager explicitly

`GetSystemManager` auto-detects in priority order. To target a specific manager,
construct it directly:

```go
m := &pkgmgr.Apt{}
m.UseSudoWhenNeeded()
```

AUR helpers must be constructed through their constructors so the helper binary
name is set:

```go
yay := pkgmgr.NewYay()
paru := pkgmgr.NewParu()
```

## Interface

The `Manager` interface covers the full package lifecycle. Methods that perform
I/O take a `context.Context` for cancellation and timeouts.

- **Identity:** `Name`, `Format`, `Path`
- **Privilege:** `SetCmdWrapper`, `UseSudoWhenNeeded`
- **Prompts:** `AssumeYes` (answer confirmation prompts automatically)
- **I/O:** `SetIO` (override stdin/stdout/stderr of spawned commands)
- **Repos:** `AddRepo`, `AddRepoURL`, `RemoveRepo`, `GetRepo`, `ListRepos`
- **Keys:** `AddRepoKey`, `AddRepoKeyFile`, `AddRepoKeyURL`
- **Packages:** `Sync`, `Install`, `Remove`, `Upgrade`, `InstallFile`,
  `UpgradeAll`, `Clean`
- **Queries:** `Search`, `Info`, `ListInstalled`, `ListUpgradable`

`Search` returns structured `SearchResult` values; `Info` returns the underlying
tool's native text output; `ListInstalled` and `ListUpgradable` return
`name -> version` maps. `ListRepos` returns every configured repo as a
`name -> configuration` map, where each value matches what `GetRepo` returns for
that name.

## Privilege handling

Two mechanisms cover opposite requirements:

- **Root managers** (`apt`, `dnf`, `pacman`, ...) need root for mutating
  operations. `UseSudoWhenNeeded` sets the wrapper to `sudo` when the process is
  not already root.
- **Non-root managers** (`brew`, `yay`, `paru`) refuse to run as root. They use
  an embedded `dropPrivilege` to run their command as an unprivileged user,
  dropping via `sudo -u` only when the process is root. The drop user defaults to
  `SUDO_USER` and can be set explicitly with `SetDropUser`.

A custom command wrapper (for example `[]string{"sudo", "-n"}`) can be supplied
through `SetCmdWrapper`.

## Unattended operation

By default the managers run their tools interactively, so a mutating operation
prompts for confirmation and aborts when no terminal is attached. `AssumeYes`
enables unattended mode: each manager injects its own non-interactive
confirmation flag (`-y` for `dnf`/`yum`/`apt`, `--noconfirm` for `pacman`/AUR
helpers, `--non-interactive` for `zypper`) into `Install`, `Remove`, `Upgrade`,
`InstallFile`, and `UpgradeAll`. A flag supplied explicitly through a method's
`args` is not duplicated.

## Testing

```bash
go test ./...
```

The suite verifies that every manager satisfies the `Manager` interface and
exercises the command wrapper, I/O overrides, privilege drop logic, and the
output parsers.
