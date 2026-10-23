# DDS Installer — Discord Data Snatcher

**Current public release:** **v0.9.0-rc.1 Preview**  
Source baseline in that preview: **0.9.0-gui-r4**.

DDS Installer is the Windows setup/repair control plane for the DDS project. It detects the required components, acquires verified packages, applies installation/repair steps and preserves DDS user data across application maintenance.

## DDS components

| Component | Current public version | Role |
|---|---|---|
| **DDS Plugin** | **0.5.4 Stable** | Discord/BetterDiscord capture layer |
| **DDS Companion** | **0.8.8 Stable** | SQLite archive, Library, media lifecycle/cache, ZIP export, desktop UI |
| **DDS Installer** | **v0.9.0-rc.1 Preview** | Windows install/repair orchestration |

Repositories:

- Plugin: https://github.com/Mr-Dexter-Morgan/DDS_BD_Plugin
- Companion: https://github.com/Mr-Dexter-Morgan/DDS_Companion
- Installer: https://github.com/Mr-Dexter-Morgan/DDS_Installer

The three components use **independent version numbers**.

## DDS architecture

Primary data path:

```text
Discord Desktop → BetterDiscord → DDS Plugin → DDS_Data → DDS Companion → SQLite archive
```

Installer sits beside this path:

```text
DDS Installer
  ├─ Discord
  ├─ BetterDiscord
  ├─ DDS Plugin
  └─ DDS Companion
```

Installer does **not** own the archive. Its job is to place/repair application components without destroying user data.

## Current preview capabilities

- Native Windows installation wizard.
- Detects Discord, BetterDiscord, DDS Plugin and DDS Companion.
- Re-injects BetterDiscord when selected DDS installation requires it.
- Validates/enables DDS Plugin without resetting unrelated BetterDiscord plugins/themes.
- Installs Companion program files separately from Companion user data.
- New Companion program installations default to `Program Files\DDS`.
- Optional desktop shortcut.
- Verified package acquisition and SHA/integrity checks.
- Selected post-install application launch after the installer exits.

## User-data boundaries

Installer must preserve these by default:

DDS Plugin data:

```text
%APPDATA%\BetterDiscord\DDS_Data
```

DDS Companion data:

```text
%LOCALAPPDATA%\DDS_Companion
```

That includes the Companion SQLite archive and user settings.

## Preview limitations

The public `v0.9.0-rc.1` release is a **pre-release**, not Stable.

Not yet claimed as fully stable/publicly accepted in this release line:

- standalone full DDS Repair;
- Uninstall;
- Portable setup;
- complete clean-install acceptance across all supported Windows states.

The current executable is not claimed to be Authenticode-signed.

## Installation

1. Open the [Installer Releases](https://github.com/Mr-Dexter-Morgan/DDS_Installer/releases).
2. Download `DDSSetup.exe` from the desired release.
3. Verify SHA-256 against the release checksum file when provided.
4. Start Installer normally.
5. Follow the wizard.

Save Discord drafts and leave voice calls before maintenance; BetterDiscord/plugin integration can require Discord to close.

## Build from source

Requirements:

- Windows x64;
- Go 1.23+.

```powershell
go test ./...
go vet ./...
go build -trimpath -ldflags="-H windowsgui -s -w" -o DDSSetup.exe ./cmd/dds-installer
```

GitHub CI performs the same test/vet/native-build gate.

## Repository layout

```text
cmd/dds-installer/        application source, tests and embedded GUI resources
tools/                    reproducible resource generators
docs/ARCHITECTURE.md      active Installer architecture/boundaries
docs/releases/            current public release notes
.github/workflows/        Windows CI
README.md
go.mod
```

The binary resource files under `cmd/dds-installer` are intentional: the GUI background is embedded into the executable and the `.syso` resource supplies Windows icon/manifest data. Their source assets and generators are retained for reproducibility.

Current release notes: [docs/releases/RELEASE_NOTES_0.9.0-rc.1.md](docs/releases/RELEASE_NOTES_0.9.0-rc.1.md)

## License

No separate open-source license has been granted. Public repository visibility alone does not grant general redistribution rights.
