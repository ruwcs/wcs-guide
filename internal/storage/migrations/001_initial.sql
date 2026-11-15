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

    auto_refresh INTEGER NOT NULL DEFAULT 0 CHECK (auto_refresh IN (0, 1)),
    publication_blocked INTEGER NOT NULL DEFAULT 0
        CHECK (publication_blocked IN (0, 1)),

    last_attempt_at TEXT,
    last_success_at TEXT,
    last_error TEXT NOT NULL DEFAULT '',

    UNIQUE (type, external_ref),
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
                    last_error <> ''
                )
            )
        )
    )
) STRICT;

-- Неизменяемая внешняя идентичность источника.
CREATE TRIGGER source_identity
BEFORE UPDATE OF type, external_ref ON sources
WHEN NEW.type <> OLD.type OR NEW.external_ref <> OLD.external_ref
BEGIN
    SELECT RAISE(ABORT, 'source identity is immutable');
END;

-- Страницы и даты редакторских и публичных изменений.
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
    imported_content TEXT,
    imported_at TEXT,
    import_error TEXT NOT NULL DEFAULT '',
    source_id INTEGER REFERENCES sources(id) ON DELETE RESTRICT,

    start_anchor TEXT NOT NULL DEFAULT '',
    include_start INTEGER NOT NULL DEFAULT 0 CHECK (include_start IN (0, 1)),
    end_anchor TEXT NOT NULL DEFAULT '',
    include_end INTEGER NOT NULL DEFAULT 0 CHECK (include_end IN (0, 1)),

    CHECK ((imported_content IS NULL) = (imported_at IS NULL)),
    CHECK (type = 'imported' OR imported_content IS NULL),
    UNIQUE (page_id, position),
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

-- Поиск страниц и блоков источника.
CREATE INDEX blocks_source ON blocks(source_id);
