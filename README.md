# DDS Installer

A Windows GUI installer for DDS - Discord Data Snatcher, a personal local-first Discord archive companion.

## Preview v0.9.0-rc.1

The exact DDS Installer 0.9.0-gui-r4 candidate was accepted following a Windows live test on 21 October 2026. The test confirmed the BetterDiscord reinjection and DDS Plugin startup fix.

Use Releases to download DDSSetup.exe and verify its SHA-256 checksum.

## Features

- Russian GUI: Settings, Install, Finish; original DDS icon and mascot.
- Detect Discord Stable, BetterDiscord, DDS Plugin and DDS Companion.
- On explicitly started Setup, close Discord and run the official BetterDiscord CLI to reinject BetterDiscord even if files are already found.
- Validate and enable only DDS Plugin; preserve other plugins and themes.
- Install Companion while preserving the local archive, SQLite and settings.
- New program installation defaults to Program Files\DDS, with user data retained under %LOCALAPPDATA%\DDS_Companion.
- Optional desktop shortcut and post-exit launch of selected applications.

## Limitations

PRE-RELEASE / NOT STABLE. The targeted r4 fix passed Windows live testing. Full standalone Repair DDS, Uninstall, Portable setup, and a separate fresh-install Program Files live gate have not yet been accepted. Existing installed paths are not silently migrated.

Installer, DDS Plugin and DDS Companion have independent versions. The accepted GUI release candidate is not claimed to be Authenticode-signed.

## Build

The repository is populated from the immutable SHA256-verified 0.9.0-gui-r4 source archive. Go 1.23+ and Windows x64. Source ZIP SHA256: 4afa367bc69bb4dcccb45c7484763435a1a23b196df327c74ceee8e40577aad1.

No redistribution license is implied by publicly viewing this repository. Historical revision reports are retained as source snapshots.
