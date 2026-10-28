# DDS Installer v0.9.0 Stable — GUI r7

Дата LIVE-приёмки: 28 октября 2026 года.

## Подтверждено
- Установка выбранных компонентов завершена; мастер показал успешный результат.
- Discord успешно закрыт перед операцией BetterDiscord и запущен после завершения мастера.
- Появились ярлыки Discord и DDS; пользователь подтвердил результат.
- `go test ./...`, race detector, `go vet` и Windows GUI build проходили в песочнице; изменения перенесены в GitHub с сохранением чистой структуры репозитория.

## Исправления относительно v0.9.0-rc.1 (GUI r4)
- r5: проверка актуальности локального staging относительно текущих upstream Stable version/SHA-256.
- r6: безопасная миграция только принадлежащего установщику ярлыка DDS.
- r7: надёжное завершение Discord с раздельной проверкой процессов, ограниченным ожиданием и отображением диагностики taskkill.

## Границы принятия
- Windows LIVE PASS относится к наблюдавшемуся Setup-сценарию, не ко всем возможным конфигурациям Windows.
- Свежий DDS Plugin heartbeat, длительная работа архива/Companion, весь Runtime DDS не подтверждены независимыми логами этого теста.
- Отдельные режимы Uninstall, полный Repair DDS и Portable не объявлены готовыми.
- EXE не заявлен как Authenticode-подписанный.

Публичный исторический pre-release `v0.9.0-rc.1` (GUI r4) сохраняется без изменений. Публикация этого релиза возможна только с точным проверенным DDSSetup.exe и приведёнными в приложенном SHA256SUMS.txt активами.

## Verified stable asset SHA-256

```text
839f68aafc94beface43adcc20f722d7f76c76e457d7643d1b9b7d23f7fb53a8  DDSSetup.exe
3b1bb0a7c69f5bdd398b506047d4bad38505b3bdf01e5c846d64b4836e24c10c  DDS-Installer-v0.9.0-windows-x64.zip
e1bad3e6355cad323a01f940c9ab7035aff317204fbaaa537075500af03feb2c  DDS-Installer-v0.9.0-source.zip
```

Release publication status: **STAGED / NOT YET PUBLISHED**. Do not confuse a source PR with a GitHub Release.
