# DDS Installer 0.8.0-r2 — numeric confirmations

- Added one shared fail-closed numeric confirmation function (`1` approve, `0` cancel; all other input cancels).
- Converted all Russian typed-action confirmations (`ПОДГОТОВИТЬ`, `УСТАНОВИТЬ ВСЁ`, `УСТАНОВИТЬ`, `ВОССТАНОВИТЬ`, `ПРОДОЛЖИТЬ`) to numeric approval.
- Preserved two separate user approvals for first Setup before staging and installation.
- Preserved input buffering across main menu → Setup → both approvals, and recovery manual-root prompt → approval.
- Updated integration/menu tests and added numeric-confirmation regression tests.
- No changes to Detect, network acquisition, external installers, Apply semantics, cleanup, DataRoot, SQLite, Plugin capture, or Companion runtime.
- Windows LIVE verification and full Setup/Repair are **not** claimed.
