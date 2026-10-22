# DDS Installer

[Русский](#русский) · [English](#english)

---

## Русский

**DDS Installer** — графический установщик и средство обслуживания для **DDS (Discord Data Snatcher)** под Windows.

Он устанавливает и проверяет компоненты, необходимые для работы DDS:

- Discord Stable;
- BetterDiscord;
- DDS Plugin;
- DDS Companion.

### Текущая версия

Последний публичный предварительный выпуск: **v0.9.0-rc.1**, основанный на кандидате **0.9.0-gui-r4**.

Это **pre-release**, а не Stable.

### Что умеет

- Графический мастер установки на русском языке.
- Обнаружение уже установленных Discord, BetterDiscord, DDS Plugin и DDS Companion.
- Повторное внедрение BetterDiscord через официальный BetterDiscord CLI, если пользователь запускает установку DDS.
- Проверка и включение только DDS Plugin без сброса остальных плагинов и тем BetterDiscord.
- Установка DDS Companion с сохранением пользовательского архива, SQLite и настроек.
- Новая программная установка Companion по умолчанию в `Program Files\DDS`.
- Пользовательские данные остаются в `%LOCALAPPDATA%\DDS_Companion`.
- Опциональный ярлык на рабочем столе.
- Запуск выбранных приложений только после закрытия мастера установки.
- Проверка контрольных сумм и состояния устанавливаемых компонентов.

### Что уже проверено

Для `0.9.0-gui-r4` выполнен целевой живой Windows-тест исправления сценария, в котором BetterDiscord и DDS Plugin могли определяться по наличию файлов, но фактически не загружаться.

Публичный pre-release содержит:

- `DDSSetup.exe`;
- ZIP проверенного Windows-кандидата;
- неизменный source ZIP;
- `SHA256SUMS.txt`.

### Ограничения текущего pre-release

Пока не заявлены как полностью принятые:

- общий самостоятельный Repair DDS;
- Uninstall;
- Portable setup;
- отдельный полный live-gate чистой установки Companion в Program Files.

Installer, DDS Plugin и DDS Companion имеют **независимые версии**.

Текущий `DDSSetup.exe` не заявлен как Authenticode-подписанный.

### Установка

1. Открой раздел **Releases**.
2. Скачай `DDSSetup.exe` из последнего подходящего релиза.
3. Сверь SHA-256 с `SHA256SUMS.txt`.
4. Запусти установщик обычным двойным щелчком.
5. Следуй шагам мастера.

Перед установкой лучше сохранить черновики Discord и выйти из голосового канала: при необходимости Installer автоматически закрывает Discord для корректного внедрения BetterDiscord и DDS Plugin.

### Сборка из исходников

Требования:

- Windows x64;
- Go 1.23+.

Из корня репозитория:

```powershell
go test ./...
go vet ./...
go build -trimpath -ldflags="-H windowsgui -s -w" -o DDSSetup.exe ./cmd/dds-installer
```

### Структура репозитория

```text
.
├─ cmd/
│  └─ dds-installer/   # исходный код приложения и тесты
├─ docs/               # документация и исторические evidence-материалы
├─ tools/              # служебные генераторы ресурсов
├─ .github/            # GitHub Actions
├─ README.md
├─ CHANGELOG.md
└─ go.mod
```

Основной код собран в небольшое число файлов по функциональным областям. Исторические отчёты и checkpoint-документы вынесены из корня в `docs/history/`.

### История и доказательства

Подробные исторические changelog, validation, release-state, provenance и live-test документы сохранены в `docs/history/`.

Они нужны для трассируемости разработки, но не являются основной пользовательской документацией.

---

## English

**DDS Installer** is a Windows GUI installer and maintenance utility for **DDS (Discord Data Snatcher)**.

It installs and validates the components required by DDS:

- Discord Stable;
- BetterDiscord;
- DDS Plugin;
- DDS Companion.

### Current version

Latest public preview: **v0.9.0-rc.1**, based on the accepted **0.9.0-gui-r4** candidate.

This is a **pre-release**, not a Stable release.

### Features

- Native graphical installation wizard.
- Detects existing Discord, BetterDiscord, DDS Plugin, and DDS Companion installations.
- Re-injects BetterDiscord through the official BetterDiscord CLI when DDS setup is explicitly started.
- Validates and enables only DDS Plugin while preserving unrelated BetterDiscord plugins and themes.
- Installs DDS Companion while preserving the local archive, SQLite database, and user settings.
- New Companion program installations default to `Program Files\DDS`.
- User data remains under `%LOCALAPPDATA%\DDS_Companion`.
- Optional desktop shortcut.
- Selected applications are launched only after the installer wizard exits.
- Integrity and component-state validation during setup.

### Verified status

The targeted Windows live test for `0.9.0-gui-r4` confirmed the fix for the case where BetterDiscord and DDS Plugin could be detected from files while not actually loading at runtime.

The public pre-release contains:

- `DDSSetup.exe`;
- the verified Windows candidate ZIP;
- the immutable source ZIP;
- `SHA256SUMS.txt`.

### Current pre-release limitations

The following areas are not yet claimed as fully accepted:

- standalone full DDS Repair;
- Uninstall;
- Portable setup;
- a separate complete clean-install live gate for Companion under Program Files.

Installer, DDS Plugin, and DDS Companion use **independent version numbers**.

The current `DDSSetup.exe` is not claimed to be Authenticode-signed.

### Installation

1. Open **Releases**.
2. Download `DDSSetup.exe` from the appropriate latest release.
3. Verify its SHA-256 against `SHA256SUMS.txt`.
4. Launch the installer normally.
5. Follow the wizard.

Before installation, save Discord drafts and leave any voice call if needed. The Installer may close Discord automatically to perform a clean BetterDiscord / DDS Plugin integration.

### Building from source

Requirements:

- Windows x64;
- Go 1.23+.

From the repository root:

```powershell
go test ./...
go vet ./...
go build -trimpath -ldflags="-H windowsgui -s -w" -o DDSSetup.exe ./cmd/dds-installer
```

### Repository layout

```text
.
├─ cmd/
│  └─ dds-installer/   # application source and tests
├─ docs/               # documentation and historical evidence
├─ tools/              # resource-generation helpers
├─ .github/            # GitHub Actions
├─ README.md
├─ CHANGELOG.md
└─ go.mod
```

The application source is consolidated into a small number of files grouped by responsibility. Historical validation and checkpoint material lives under `docs/history/` instead of cluttering the repository root.

### History and evidence

Detailed historical changelogs, validation reports, release-state snapshots, provenance records, and live-test procedures are preserved under `docs/history/`.

They are retained for traceability and are not the primary user documentation.

---

Public visibility of this repository does not by itself grant redistribution rights unless a separate license is added.
