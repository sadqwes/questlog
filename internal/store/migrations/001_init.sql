CREATE TABLE marks (
    day        date        NOT NULL,
    skill      text        NOT NULL,
    level      smallint    NOT NULL CHECK (level BETWEEN 0 AND 2),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (day, skill)
);

CREATE TABLE days (
    day        date        PRIMARY KEY,
    rest       boolean     NOT NULL DEFAULT false,
    note       text        NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE week_items (
    week       text        NOT NULL,
    item       text        NOT NULL,
    done       boolean     NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (week, item)
);

CREATE TABLE reviews (
    week        text        PRIMARY KEY,
    good        text        NOT NULL DEFAULT '',
    hard        text        NOT NULL DEFAULT '',
    next_change text        NOT NULL DEFAULT '',
    story       text        NOT NULL DEFAULT '',
    book        text        NOT NULL DEFAULT '',
    weight      text        NOT NULL DEFAULT '',
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE chapters (
    n       int         PRIMARY KEY,
    read_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE guides (
    day        date        NOT NULL,
    skill      text        NOT NULL,
    content    text        NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (day, skill)
);

CREATE TABLE arena (
    id          bigserial   PRIMARY KEY,
    kind        text        NOT NULL CHECK (kind IN ('q', 'mock')),
    lang        text        NOT NULL DEFAULT 'ru' CHECK (lang IN ('ru', 'en')),
    topic       text        NOT NULL DEFAULT '',
    question    text        NOT NULL,
    answer      text,
    feedback    text,
    score       smallint    CHECK (score BETWEEN 0 AND 5),
    created_at  timestamptz NOT NULL DEFAULT now(),
    answered_at timestamptz,
    reviewed_at timestamptz
);

CREATE TABLE settings (
    key   text PRIMARY KEY,
    value text NOT NULL
);
