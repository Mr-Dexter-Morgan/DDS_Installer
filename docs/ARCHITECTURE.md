# DDS Installer Architecture

## Role in DDS

DDS is one product split into independently versioned components:

| Component | Responsibility |
|---|---|
| **DDS Plugin** | Discord/BetterDiscord capture |
| **DDS Companion** | Durable archive, media lifecycle/cache, Library and desktop UX |
| **DDS Installer** | Windows acquisition, validation, installation and repair orchestration |

Installer is a control plane. It must not become a second archive owner.

## Component order

The setup model treats the DDS stack as bounded floors:

1. Discord;
2. BetterDiscord;
3. DDS Plugin;
4. DDS Companion.

A selected operation may skip already healthy components, but dependencies are explicit. Plugin/Companion setup cannot silently pretend BetterDiscord integration is healthy when it still requires reinjection.

## Acquisition boundary

Installer acquires known packages, stages them in a temporary area, validates the staged artifacts, then applies only the selected components.

Network/download failures must stop the affected setup path instead of producing a partially trusted install.

## Application/data separation

Companion program files and user data are separate.

Program installation target:

```text
Program Files\DDS
```

User archive/data root:

```text
%LOCALAPPDATA%\DDS_Companion
```

Plugin capture root:

```text
%APPDATA%\BetterDiscord\DDS_Data
```

Installer maintenance must preserve those data roots unless the user explicitly requests data removal in a future supported uninstall flow.

## BetterDiscord / Plugin boundary

When DDS setup requires BetterDiscord, Installer uses the intended BetterDiscord installation/injection path and then validates the result.

DDS Plugin apply must:

- target only DDS Plugin;
- preserve unrelated BetterDiscord plugins/themes;
- avoid changing the plugin while Discord is actively using it;
- validate the selected plugin state after apply.

## Companion boundary

Installer places Companion program files and registration/shortcut state. Companion itself owns:

- SQLite migrations;
- archive semantics;
- media cache and re-cache;
- updater/rollback;
- desktop UI.

Installer must not edit Companion SQLite content as part of ordinary application installation.

## Windows privilege model

The installer GUI runs as the normal user. Elevation is bounded to operations that require it, such as writing program files under protected locations.

User-context Discord/BetterDiscord data must continue to resolve against the actual user profile rather than an elevated profile.

## Release discipline

Installer versions are independent from Plugin and Companion versions.

Current public state:

- Plugin: 0.5.4 Stable;
- Companion: 0.8.8 Stable;
- Installer: v0.9.0 Stable (accepted GUI r7).

Stable promotion requires more than a successful compile: test/vet/native build plus relevant live Windows validation are required. DDS Installer v0.9.0 was promoted after GUI r7 LIVE acceptance on October 28, 2026.
