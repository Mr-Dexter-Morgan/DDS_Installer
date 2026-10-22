# Источник gui-r4

Точный архив gui-r3 из Drive: 1APpNhr7jTLtouMnw5a2MyEKC24KjO4k1.
SHA-256 исходного r3: df72678a67f9f039faf6df155fe3ad77fb19a8b18a02d61123ea933f5a8a4af5 — скачан и независимо проверен до правок.

Работа велась в отдельной копии; исходный архив и распакованный r3 сохранены без изменений. Принятый GUI/ресурсы и механизмы размещения Program Files/UAC/shortcut не перепроектировались.

Официальные источники механизма (проверены 21.10.2026):
- https://github.com/BetterDiscord/cli/blob/main/README.md
- https://github.com/BetterDiscord/cli/blob/v1.0.0/internal/discord/install.go
- https://github.com/BetterDiscord/cli/blob/v1.0.0/internal/discord/injection.go
- https://github.com/BetterDiscord/cli/blob/v1.0.0/internal/discord/assets/app_index.js
- https://github.com/BetterDiscord/cli/blob/v1.0.0/internal/discord/assets/app_package.json

Повторный install восстанавливает внедрение; отдельный upstream RepairBD отключает плагины и не используется. Проверки файлов loader не называют выполнением кода. Срок/версия heartbeat проверяются относительно текущего запуска. Причина исходного сбоя у второго пользователя UNKNOWN.
