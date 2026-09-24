-- Дневник питания: приёмы пищи с фото (фото лежат в MinIO, здесь только ключи) и комментарий наставника за день.
CREATE TABLE meals (
    id          bigserial   PRIMARY KEY,
    day         date        NOT NULL,
    at          text        NOT NULL DEFAULT '',   -- время как его назвала пользовательница: "13:30"
    kind        text        NOT NULL CHECK (kind IN ('breakfast', 'lunch', 'dinner', 'snack', 'drink')),
    description text        NOT NULL,
    protein     boolean     NOT NULL DEFAULT false,
    veggies     boolean     NOT NULL DEFAULT false,
    comment     text        NOT NULL DEFAULT '',   -- мягкий комментарий наставника
    photos      text[]      NOT NULL DEFAULT '{}', -- ключи объектов в bucket
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX meals_day_idx ON meals (day);

CREATE TABLE food_days (
    day        date        PRIMARY KEY,
    comment    text        NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);
