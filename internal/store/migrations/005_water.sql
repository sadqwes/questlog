-- Трекер воды: сколько стаканов за день.
ALTER TABLE food_days ADD COLUMN water smallint NOT NULL DEFAULT 0 CHECK (water BETWEEN 0 AND 30);
