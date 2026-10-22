# DDS Installer 0.8.0-r2 — validation

- Base source ZIP SHA256 verified against project evidence: `40443d845392840c4131d41e37ac6c6a96fe2b53447deb46fa731403298a523d`.
- `go test ./...` — PASS (Linux sandbox, Go 1.23.2).
- `go test -race ./...` — PASS (Linux sandbox).
- `go vet ./...` — PASS.
- `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w'` — PASS; PE32+ Windows x86-64 console binary.
- Windows UI/live interaction — PENDING. No external installer executed and no production Windows component or DataRoot touched during this patch.
