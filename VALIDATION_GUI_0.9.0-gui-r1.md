# DDS Installer 0.9.0-gui-r1 — Validation

- Baseline: DDS Installer 0.8.0-r3 source archive SHA256 `5ef9a8b8f434bce8783e45389584d6aabaecfc54f2ed286f9432bb86c0c01cda`.
- `go test -count=1 ./...`: PASS, including GUI selection/dependency tests.
- `go test -race -count=1 ./...`: PASS (Linux-hosted core regression).
- `go vet ./...`: PASS.
- `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags='-H windowsgui -s -w'`: PASS.
- Result verified by `file`: PE32+ Windows x64 GUI subsystem; executable is not a console EXE.
- `.rsrc` inspected using `objdump`: seven RT_ICON and one RT_GROUP_ICON resources embedded from original DDS.ico.
- GUI rendering, actual click handling, Windows process handoff and end-to-end Setup on real Windows: **NOT YET VERIFIED**.
- Not a public release; no GitHub product changes.
