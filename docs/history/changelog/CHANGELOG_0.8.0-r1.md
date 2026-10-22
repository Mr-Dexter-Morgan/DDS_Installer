# DDS Installer 0.8.0-r1 — First Setup Integration

- Integrated existing Detect → Plan → Acquire → Verify → Stage → Apply → post-Detect into the **new menu item 8**, retaining existing menu operations and source ownership.
- Two explicit Russian confirmations: `ПОДГОТОВИТЬ` (download/stage) and `УСТАНОВИТЬ ВСЁ` (mutating installation). Nothing mutates installed components before the latter.
- Supported **Installed first install** only. Existing healthy components are skipped; ambiguous/PARTIAL/Portable states refuse automatic first install and require later Repair/profile workflow.
- Reuses official Discord Stable x64 staged installer with Windows Authenticode validation and post-install polling. No undocumented Discord silent flags.
- Uses officially documented BetterDiscord CLI `bdcli --silent install --channel stable`, with post-install detection, not exit code alone. If bdcli restarts Discord, user is asked to close Discord manually before touching the plugin; no forced process kill.
- Reuses verified DDS Plugin atomic Apply/Validate/backup/rollback and verified DDS Companion first-file placement under a new `%LOCALAPPDATA%\\DDS` directory.
- Verifies the **exact** preapproved version/path/SHA-256 at preflight and again immediately before each mutating step. Missing or tampered staging blocks work.
- Preserves existing DataRoot/SQLite/DDS_Data by scope. Component failure reports truthful partial status; no misleading all-or-nothing claims.
- The integrated result is `FILES_INTEGRATED_FIRST_START_PENDING` (NOT complete Setup): actual Companion GUI start, Plugin heartbeat, DDS_Data connectivity, full Windows Setup/Repair remain separate gates.
- Product version advanced to `0.8.0-r1` for a real new Installer functional floor. Earlier `0.7.1-r1` source/candidate/Windows evidence remain immutable.
