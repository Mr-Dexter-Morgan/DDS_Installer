# Происхождение GUI r2

Загружен сохранённый Google Drive файл `DDS-Installer-0.9.0-gui-r1-source.zip`, ID `12-V_BI9shOQka0-8WtthPjH-bDqLrRa3`.

SHA-256 проверен до распаковки: `0b8ecd956042fd45f4bd415fa1f7ff2c0e449706e2ae0ee421b57c329374d74a`.

r2 продолжает этот снимок. Брендовые DDS.ico, DDS_mascot.png и background.bgra сохранены. Добавлен asInvoker-манифест и пересобран ресурс `.syso` с исходной иконкой.

Рабочая область: отдельный каталог `DDS_DEV/installer/gui-r2`. GitHub не являлся исходником Installer и не изменялся. Из опубликованного Companion 0.8.5 прочитаны только относящиеся к совместимости файлы Updater/launcher; их код и бинарники не модифицировались.

Воспроизведение Windows x64 сборки: Go 1.23+ (фактически использован go1.27.2 windows/amd64), Python 3 для `python build_icon_resource.py`, затем `go build -trimpath -ldflags="-H windowsgui -s -w" -o DDSSetup.exe .`.
