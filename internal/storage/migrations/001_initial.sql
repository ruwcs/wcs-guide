-- Источники, настройки получения и независимые состояния синхронизации.
CREATE TABLE sources (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    type TEXT NOT NULL CHECK (type = 'telegram_post'),
    external_ref TEXT NOT NULL CHECK (length(trim(external_ref)) > 0),
    fetch_params TEXT CHECK (
        fetch_params IS NULL OR (
            json_valid(fetch_params) AND json_type(fetch_params) = 'object'
        )
    ),

    update_interval_ns INTEGER NOT NULL CHECK (update_interval_ns >= 0),
    auto_refresh INTEGER NOT NULL DEFAULT 0 CHECK (auto_refresh IN (0, 1)),
    publication_blocked INTEGER NOT NULL DEFAULT 0
        CHECK (publication_blocked IN (0, 1)),

    last_attempt_at TEXT,
    last_success_at TEXT,
    last_error TEXT NOT NULL DEFAULT '',
    edit_revision INTEGER NOT NULL DEFAULT 1 CHECK (edit_revision > 0),
    sync_revision INTEGER NOT NULL DEFAULT 0 CHECK (sync_revision >= 0),

    UNIQUE (type, external_ref),
    CHECK (auto_refresh = 0 OR update_interval_ns > 0),
    CHECK (
        (
            last_attempt_at IS NULL
            AND last_success_at IS NULL
            AND last_error = ''
        ) OR (
            last_attempt_at IS NOT NULL AND (
                (
                    last_error = ''
                    AND last_success_at IS NOT NULL
                    AND last_success_at = last_attempt_at
                ) OR (
                    last_error <> '' AND (
                        last_success_at IS NULL
                        OR last_success_at <= last_attempt_at
                    )
                )
            )
        )
    )
) STRICT;

-- Сохраняет внешнюю идентичность уже созданного источника.
CREATE TRIGGER source_identity
BEFORE UPDATE OF type, external_ref ON sources
WHEN NEW.type <> OLD.type OR NEW.external_ref <> OLD.external_ref
BEGIN
    SELECT RAISE(ABORT, 'source identity is immutable');
END;

-- Снимки оригиналов с метаданными и принадлежностью источнику.
CREATE TABLE snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id INTEGER NOT NULL REFERENCES sources(id) ON DELETE RESTRICT,
    raw_content BLOB NOT NULL CHECK (length(raw_content) > 0),
    format TEXT NOT NULL CHECK (length(trim(format)) > 0),
    checksum TEXT NOT NULL CHECK (length(trim(checksum)) > 0),
    metadata TEXT CHECK (
        metadata IS NULL OR (
            json_valid(metadata) AND json_type(metadata) = 'object'
        )
    ),
    fetched_at TEXT NOT NULL,
    source_modified_at TEXT,
    UNIQUE (id, source_id)
) STRICT;

-- Ускоряет выбор последнего снимка конкретного источника.
CREATE INDEX snapshots_source ON snapshots(source_id, id DESC);

-- Страницы, редакции и даты редакторских и публичных изменений.
CREATE TABLE pages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    path TEXT UNIQUE CHECK (
        path IS NULL OR (length(path) > 0 AND substr(path, 1, 1) = '/')
    ),
    title TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    machine_context TEXT NOT NULL DEFAULT '',
    language TEXT NOT NULL DEFAULT '',
    status INTEGER NOT NULL DEFAULT 0 CHECK (status IN (0, 1)),

    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    published_at TEXT,
    public_updated_at TEXT,
    edit_revision INTEGER NOT NULL DEFAULT 1 CHECK (edit_revision > 0),

    CHECK (updated_at >= created_at),
    CHECK (
        published_at IS NULL OR (
            published_at >= created_at AND published_at <= updated_at
        )
    ),
    CHECK (
        status = 0 OR (
            path IS NOT NULL
            AND length(trim(title)) > 0
            AND length(trim(language)) > 0
            AND published_at IS NOT NULL
        )
    )
) STRICT;

-- Упорядоченные ручные и импортируемые блоки страницы.
CREATE TABLE blocks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    page_id INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position >= 0),
    type TEXT NOT NULL CHECK (type IN ('text', 'imported')),
    text TEXT NOT NULL DEFAULT '',
    source_id INTEGER REFERENCES sources(id) ON DELETE RESTRICT,

    start_anchor TEXT NOT NULL DEFAULT '',
    include_start INTEGER NOT NULL DEFAULT 0 CHECK (include_start IN (0, 1)),
    end_anchor TEXT NOT NULL DEFAULT '',
    include_end INTEGER NOT NULL DEFAULT 0 CHECK (include_end IN (0, 1)),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),

    UNIQUE (page_id, position),
    UNIQUE (id, source_id),
    CHECK (
        (
            type = 'text'
            AND source_id IS NULL
            AND start_anchor = ''
            AND end_anchor = ''
            AND include_start = 0
            AND include_end = 0
        ) OR (
            type = 'imported' AND source_id IS NOT NULL AND text = ''
        )
    )
) STRICT;

-- Ускоряет поиск страниц и блоков, использующих источник.
CREATE INDEX blocks_source ON blocks(source_id);

-- Последний успешный результат с применёнными правилами и происхождением.
CREATE TABLE prepared_contents (
    block_id INTEGER PRIMARY KEY REFERENCES blocks(id) ON DELETE CASCADE,
    source_id INTEGER NOT NULL,
    snapshot_id INTEGER NOT NULL,
    content TEXT NOT NULL,

    start_anchor TEXT NOT NULL DEFAULT '',
    include_start INTEGER NOT NULL DEFAULT 0 CHECK (include_start IN (0, 1)),
    end_anchor TEXT NOT NULL DEFAULT '',
    include_end INTEGER NOT NULL DEFAULT 0 CHECK (include_end IN (0, 1)),
    prepared_at TEXT NOT NULL,
    published_at TEXT,
    CHECK (published_at IS NULL OR published_at >= prepared_at),

    FOREIGN KEY (snapshot_id, source_id)
        REFERENCES snapshots(id, source_id) ON DELETE RESTRICT,
    FOREIGN KEY (block_id, source_id)
        REFERENCES blocks(id, source_id) DEFERRABLE INITIALLY DEFERRED
) STRICT;

-- Ускоряет поиск результатов, связанных со снимком.
CREATE INDEX prepared_snapshot ON prepared_contents(snapshot_id);
