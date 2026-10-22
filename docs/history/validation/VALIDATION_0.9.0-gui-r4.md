# Проверка gui-r4 — 21.10.2026

**AUTOMATED PASS / WINDOWS X64 CROSS-BUILD PASS / WINDOWS LIVE PENDING.**

- Проверен SHA-256 точного архива r3; отдельная неизменяемая копия базы.
- До правок исходные Linux-тесты r3 PASS.
- Целевые проверки: обязательный BD Repair при существующих файлах; no-reinstall для здоровых Discord/Companion; остановка до/после CLI, ошибка CLI не пропускает Plugin; loader не затенён app.asar; только DDS включается; heartbeat текущего запуска, не старый; независимость Finish-галочек и ожидание выхода родителя — PASS.
- Финальная Linux regression: **140 top-level + 78 subtests PASS**, 0 FAIL.
- go test -race ./... — PASS; go vet -buildvcs=false ./... — PASS.
- Go 1.23.12 linux/amd64, без внешних Go-модулей. Windows x64 GUI: PE32+ GUI build PASS; весь Windows test package compile PASS. Нативный Windows test EXE не исполнялся, нового живого Win32-render не было.
- Команда продукта: GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags='-H=windowsgui -s -w' -o DDSSetup.exe .
- -buildvcs=false нужен для standalone source ZIP без репозитория; Git history/commit не создавались.
- SHA-256 продукта: 3b2dcef674e6c5f8970c54b2134e47a013864087e0ec4b877a0fc44ea9d188b0.

Существующие ресурсы GUI (иконка, фон, manifest) побайтно сохранены. Целенаправленно изменены только подписи текущих действий; внешний вид не переработан. Сборка содержит asInvoker manifest r3. Повторная проверка извлечённого source ZIP и соответствие сборки фиксируются в PACKAGING_VALIDATION_0.9.0-gui-r4.json рядом с архивами.

Известные границы: реальная загрузка BetterDiscord, свежий heartbeat на машине с дефектом, обнаружение Plugin интерфейсом Companion, GUI shutdown/Finish timing и свежая установка в Program Files требуют одного пользовательского Windows теста. Исходный сбой Максима не диагностирован до точной первопричины. Полный runtime PASS не заявлен.
