# DDS Installer 0.8.0-r1 — sandbox validation

Environment: Linux amd64, Go 1.23.2. Full Go unit suite, race detector, go vet, and GOOS=windows GOARCH=amd64 CGO_ENABLED=0 PE32+ build run in the isolated sandbox. No external Windows installer was run.

Tests exercise: four-floor sequence and idempotent second run; selected-stage requirement before mutations; stop after failed/missing post-detection at each floor; partial success; rejection of PARTIAL, uncertain and Portable modes; exact SHA-256 stage identity and tampering refusal; cancellation, absent callbacks, guarded external OS execution, two-line buffered interactive input, explicit user-closed Discord prompt and bounded output.

**Windows LIVE remains pending**, including actual official Discord and BetterDiscord installations, Companion first GUI startup, archive validation, Setup/Repair integration. Source archive and Windows candidate hash values provided separately with files.
