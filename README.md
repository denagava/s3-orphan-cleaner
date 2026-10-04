# s3-orphan-cleaner

[![CI](https://github.com/denagava/s3-orphan-cleaner/actions/workflows/ci.yml/badge.svg)](https://github.com/denagava/s3-orphan-cleaner/actions/workflows/ci.yml)
![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go)

CLI-утилита на Go для очистки S3-совместимого хранилища (AWS S3, MinIO) от **файлов-сирот** — объектов, на которые больше нет ни одной ссылки в базе данных PostgreSQL.

Рассчитана на большие объёмы: таблицы на десятки миллионов строк и бакеты с миллионами объектов. Работу можно прервать в любой момент и продолжить с того же места.

## Возможности

- **Трёхфазный алгоритм** — инвентаризация S3 → сверка с БД → удаление.
- **Возобновление после прерывания** — прогресс сохраняется в `state.json` после каждой страницы.
- **Cursor-based пагинация** (`WHERE id > $last ORDER BY id`) вместо `OFFSET` — стабильное время запроса на любом объёме.
- **Dry-run режим** — посмотреть, что будет удалено, ничего не удаляя.
- **Защита от массового удаления** — если в БД найдено меньше `min_db_assets` записей (например, DSN указывает не на ту базу), удаление не запускается.
- **Устойчивость к сбоям** — частичные ошибки `DeleteObjects` логируются в отдельный файл, прогон продолжается.
- **Фильтр по возрасту** — `older_than` не трогает свежезагруженные объекты.
- **Чистая архитектура** — бизнес-логика зависит только от интерфейсов, хранилища подменяются моками.

## Как это работает

```
            ┌──────────────┐
config.yaml │    main      │
     ──────►│   (wiring)   │
            └──────┬───────┘
                   ▼
           ┌───────────────┐      ┌──────────────┐
           │ CleanerService│─────►│  state.json  │  чекпоинты
           └───┬───────┬───┘      └──────────────┘
               │       │
               ▼       ▼
        ┌──────────┐ ┌──────────┐
        │PostgreSQL│ │ S3/MinIO │
        └──────────┘ └──────────┘
```

```
Phase 1 — S3 scan
  ListObjectsV2 по всем префиксам из конфига
  → все ключи пишутся во временную таблицу scan_keys (referenced = false)

Phase 2 — DB scan
  SELECT по таблице assets с cursor-пагинацией
  → из JSONB-поля metadata извлекаются S3-ключи
  → UPDATE scan_keys SET referenced = true WHERE key IN (...)

Phase 3 — Delete
  SELECT FROM scan_keys WHERE referenced = false
  → DeleteObjects батчами по 1000 (лимит S3 API)
```

### Модель данных

Утилита ожидает таблицу `assets`:

| Колонка | Тип | Описание |
|---|---|---|
| `id` | `UUID` | первичный ключ, используется как курсор |
| `kind` | `TEXT` | тип ассета |
| `metadata` | `JSONB` | метаданные со ссылками на объекты в S3 |

Какие поля `metadata` считаются ссылками:

| `kind` | Поля |
|---|---|
| `IMAGE` | `image.renditions.small`, `image.renditions.original`, `image.renditions.medium` |
| `IMAGE` (старый формат) | `storageKey` |
| `DOCUMENT`, `PDF`, `PRESENTATION`, `SPREADSHEET` | `document.sourceKey`, `document.pages[].key`, `document.pages[].renditions.small`, `document.pages[].renditions.medium` |

Записи остальных типов игнорируются. Полные URL (`https://bucket.s3.../path/file.jpg`) нормализуются до ключа.

## Ключевые решения

- **Временная таблица в PostgreSQL вместо памяти.** Список ключей S3 может не поместиться в RAM; через таблицу сверка делается обычным `UPDATE ... WHERE key IN (...)`, а частичный индекс `WHERE referenced = false` ускоряет фазу удаления.
- **Атомарная запись чекпоинта.** `state.json` пишется во временный файл и переименовывается — файл не бывает записан наполовину даже при `kill -9`.
- **Идемпотентность.** `BulkInsert` использует `ON CONFLICT DO NOTHING`, повторная пометка ключа безопасна, удаление несуществующего объекта в S3 возвращает успех. Любую фазу можно безопасно перезапустить.
- **Чистый старт.** При запуске без `state.json` временная таблица пересоздаётся, чтобы данные прошлого прогона не привели к ложным удалениям.

## Быстрый старт (Docker)

```bash
# 1. Поднять PostgreSQL + MinIO (таблица и тестовые записи создаются автоматически)
docker compose -f docker/docker-compose.yml up -d postgres minio

# 2. Залить в MinIO тестовые объекты: 9 с ссылками + 5 сирот
go run ./docker/seeds/seed.go

# 3. Запустить утилиту
docker compose -f docker/docker-compose.yml run --rm app
```

Ожидаемый результат: 5 объектов-сирот удалены, 9 объектов со ссылками на месте.

<details>
<summary>Пример вывода</summary>

```
level=INFO msg="s3-orphan-cleaner starting" dry_run=false db_page_size=1000
level=INFO msg="database connected"
level=INFO msg="s3 client ready" bucket=media
level=INFO msg="starting fresh run"
level=INFO msg="phase 1: scanning S3" paths="[uploads/ documents/]"
level=INFO msg="prefix scanned" prefix=uploads/ keys=8
level=INFO msg="prefix scanned" prefix=documents/ keys=6
level=INFO msg="phase 1 complete" keys_found=14
level=INFO msg="phase 2: scanning assets table" cursor=""
level=INFO msg="page processed" rows=5 keys_marked=9 total_scanned=5
level=INFO msg="phase 2 complete" rows_scanned=5
level=INFO msg="phase 3: deleting unreferenced objects"
level=INFO msg="delete batch" batch=1 count=5 total_deleted=5
level=INFO msg="run complete" deleted=5 db_rows_scanned=5 s3_keys_found=14
```
</details>

Остановить и удалить данные: `docker compose -f docker/docker-compose.yml down -v`.

## Локальный запуск

```bash
go build -o bin/s3-orphan-cleaner ./cmd/cleaner
./bin/s3-orphan-cleaner                     # читает ./config.yaml
CONFIG_PATH=/path/to/config.yaml ./bin/s3-orphan-cleaner
```

- **Перед первым боевым запуском** включите `dry_run: true` и проверьте логи.
- **Продолжить после прерывания** — просто запустите снова, `state.json` подхватится автоматически.
- **Начать заново** — удалите `state.json`.

## Конфигурация

```yaml
database:
  dsn: "postgres://postgres:postgres@localhost:5432/cleaner?sslmode=disable"

s3:
  endpoint: "http://localhost:9000"   # пусто — настоящий AWS S3
  region: "us-east-1"
  bucket: "media"
  access_key_id: "minioadmin"         # пусто — стандартная цепочка AWS (env, IAM role)
  secret_access_key: "minioadmin"
  use_path_style: true                # нужно для MinIO
  older_than: "24h"                   # не трогать объекты моложе указанного возраста
  paths:                              # какие префиксы сканировать
    - "uploads/"
    - "documents/"

cleaner:
  db_page_size: 1000                  # строк за один SELECT
  s3_delete_batch_size: 1000          # объектов за один DeleteObjects (макс. 1000)
  state_file: "state.json"            # чекпоинт для возобновления
  stash_file: "failed_deletions.jsonl" # объекты, которые не удалось удалить
  dry_run: false                      # true — только посчитать, ничего не удалять
  min_db_assets: 1                    # не удалять, если в БД меньше N записей (0 — выкл.)
```

## Тесты

```bash
# Unit-тесты: парсинг метаданных, конфиг, все фазы сервиса на моках
go test ./internal/... -v

# Интеграционные тесты: PostgreSQL и MinIO поднимаются через testcontainers-go (нужен Docker)
go test -tags integration ./tests/integration/... -v -timeout=5m
```

| Тест | Что проверяет |
|---|---|
| `TestExtractS3Keys` | извлечение ключей для всех типов, старый формат, битый JSON |
| `TestNormaliseKey` | приведение полного URL к ключу |
| `TestRun_FullRun` | полный прогон трёх фаз |
| `TestRun_SkipsCompletedPhases` | возобновление: завершённые фазы не повторяются |
| `TestRun_DryRun` | в dry-run удаление не вызывается |
| `TestRun_PartialDeleteError` | частичная ошибка удаления не останавливает прогон |
| `TestRun_SavesStateAfterEachPage` | курсор сохраняется после каждой страницы |
| `TestIntegration_Pagination` | корректная работа на нескольких страницах в реальной БД |
| `TestIntegration_ResumeFromCheckpoint` | продолжение с чекпоинта на реальных PostgreSQL и MinIO |

## Производительность

Для больших таблиц заранее создайте индекс под выборку фазы 2:

```sql
CREATE INDEX CONCURRENTLY idx_assets_kind_id
    ON assets (kind, id)
    WHERE kind IN ('IMAGE', 'DOCUMENT', 'PDF', 'PRESENTATION', 'SPREADSHEET');
```

`CONCURRENTLY` строит индекс без блокировки записи в таблицу.

## Структура проекта

```
cmd/cleaner/            точка входа, сборка зависимостей
internal/
  config/               загрузка и валидация config.yaml
  domain/               модели, интерфейсы, извлечение ключей из metadata
  service/              CleanerService — три фазы, чекпоинты, защита
  state/                атомарное сохранение state.json
  repository/postgres/  assets + временная таблица scan_keys (bun)
  repository/s3/        листинг и удаление объектов (aws-sdk-go-v2)
tests/integration/      e2e-тесты на testcontainers
docker/                 Dockerfile, docker-compose, тестовые данные
migrations/             SQL временной таблицы
```

## Стек

Go 1.25 · PostgreSQL · [bun](https://github.com/uptrace/bun) · [aws-sdk-go-v2](https://github.com/aws/aws-sdk-go-v2) · MinIO · Docker · [testcontainers-go](https://github.com/testcontainers/testcontainers-go)
