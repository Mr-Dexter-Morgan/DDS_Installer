# DDS Installer 0.9.0-gui-r1 — Changelog

2026-10-19

- Native Win32 graphical wizard; no console window.
- DDS mascot background; approved original DDS.ico embedded as actual executable RT_ICON/RT_GROUP_ICON resources.
- Full title `DDS - Discord Data Snatcher`; Russian UI.
- Selective component checkboxes, including explicit dependency validation and truthful skip/no-op results.
- Existing 0.8.0-r3 download, signature/digest validation, and Apply hooks retained.
- Finish-page optional launch redesigned: no automatic launch before Finish; after GUI closes, a helper waits for the installer process to exit and starts only selected apps.
- SQLite/DataRoot untouched by GUI code; Windows runtime acceptance pending.
