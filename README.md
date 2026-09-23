# xray-monitor

Небольшой Go-сервис для проверки VLESS + REALITY узлов из одной или нескольких subscription-ссылок. Он периодически перечитывает подписки, поднимает отдельный локальный SOCKS-вход Xray для каждого узла, выполняет реальный HTTP-запрос через каждый туннель и сохраняет результаты в SQLite.

Поддерживаемый в первой версии формат: `vless://` + `security=reality` + `type=raw` (также принимается старое имя `tcp`). UUID, public key и полные proxy URI не возвращаются через API и не записываются в SQLite.

## Требования

- Linux с systemd;
- установленный и доступный в `PATH` бинарник `xray`;
- для сборки — Go 1.25+.

Xray запускается дочерним процессом и обновляется независимо от монитора. Монитор не выполняет проверки простым TCP connect: успешной считается выдача HTTP-кода 2xx/3xx целевым URL через конкретный туннель.

## GitHub Actions и релизы

Workflow [.github/workflows/ci-release.yml](.github/workflows/ci-release.yml) автоматически:

- запускает форматирование, `go vet` и тесты с race detector для push/PR;
- при публикации тега `v*` собирает Linux amd64 и arm64;
- создаёт `checksums.txt`;
- публикует бинарники, checksum и `install.sh` в GitHub Releases.

Первый релиз создаётся так:

```bash
git tag v0.1.0
git push origin v0.1.0
```

Если репозиторий будет называться не `vfaddey/xray-monitor`, перед публикацией поменяйте `DEFAULT_REPOSITORY` в `install.sh`. Также репозиторий всегда можно передать установщику через `--repo OWNER/REPO`.

## Локальная сборка

```bash
make test
make linux-amd64
# или make linux-arm64
```

Результат появится в `dist/`. Сборка выполняется с `CGO_ENABLED=0`, поэтому для SQLite на сервере не нужны системные библиотеки.

## Автоматическая установка на сервер

После появления первого GitHub Release достаточно выполнить:

```bash
curl -fsSL \
  https://raw.githubusercontent.com/vfaddey/xray-monitor/main/install.sh \
  | sudo bash -s -- \
      --subscription 'https://example.com/sub/secret'
```

Скрипт определяет `amd64`/`arm64`, при необходимости ставит Xray официальным установщиком XTLS, скачивает последний релиз монитора, обязательно проверяет SHA-256 и запускает встроенную установку systemd. Для нескольких подписок повторите `--subscription`.

Если `--public-host` не указан, инсталлятор автоматически определит публичный IPv4 сервера через `https://api.ipify.org`. Явно передайте `--public-host monitor.example.com`, если нужен DNS или сервер находится за NAT с отдельной схемой маршрутизации.

Установка конкретной версии или из репозитория с другим именем:

```bash
sudo ./install.sh \
  --repo OWNER/REPOSITORY \
  --version v0.1.0 \
  --subscription 'https://example.com/sub/secret'
```

Если Xray управляется отдельно, используйте `--skip-xray`. `--force` обновляет существующую установку монитора и перезапускает unit.

## Ручная установка на сервер

Скопируйте подходящий бинарник и выполните от root:

```bash
chmod +x ./xray-monitor-linux-amd64
sudo ./xray-monitor-linux-amd64 install \
  -subscription 'https://example.com/sub/secret' \
  -public-host monitor.example.com
```

Для нескольких подписок повторите `-subscription`. Встроенный инсталлятор:

- проверит наличие Xray;
- создаст системного пользователя `xray-monitor`;
- установит бинарник и systemd unit;
- выберет свободный случайный TCP-порт;
- сгенерирует 256-битный Bearer token;
- запустит сервис и напечатает status URL, token и готовую команду `curl`.

Конфигурация хранится в `/etc/xray-monitor/config.json`, БД — в `/var/lib/xray-monitor/monitor.db`. Повторная установка требует явного флага `-force`.

Сгенерированный API использует HTTP. Не передавайте token через открытый интернет: ограничьте порт firewall/VPN либо поставьте перед сервисом HTTPS reverse proxy. Локальные SOCKS-порты всегда слушают только loopback-интерфейс.

## Ручной запуск

```bash
cp configs/config.example.json config.local.json
# заполните subscription URL и api_token
go run ./cmd/xray-monitor validate -config config.local.json
go run ./cmd/xray-monitor run -config config.local.json
```

## API

Краткий контракт для подключения нового сервера к VPN-админке: [docs/admin-integration.md](docs/admin-integration.md).

Все `/api/v1/*` методы требуют заголовок `Authorization: Bearer <token>`.

```bash
# текущее состояние всех узлов
curl -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/status"

# последние измерения одного узла
curl -H "Authorization: Bearer $TOKEN" \
  "$BASE/api/v1/checks?node_id=<id>&limit=100"

# немедленно обновить подписки / запустить проверку
curl -X POST -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/refresh"
curl -X POST -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/check"
```

`GET /healthz` — liveness процесса, `GET /readyz` — readiness Xray и наличие хотя бы одного узла. Эти два endpoint не требуют token.

## Поведение при ошибках

- Если одна подписка временно недоступна, используется её последний успешно полученный список в памяти; успешно обновившиеся подписки продолжают применяться.
- Новая конфигурация Xray сначала запускается и только затем заменяет старую. При ошибке старая конфигурация продолжает работать.
- Проверки выполняются с ограниченной конкуренцией. История старше 30 дней удаляется автоматически.
- После перезапуска список заново загружается из подписок; секретные VLESS URI намеренно не кешируются в БД.
