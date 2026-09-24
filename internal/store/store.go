// Package store хранит прогресс в PostgreSQL.
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sadqwes/questlog/internal/model"
)

//go:embed migrations/*.sql
var migrations embed.FS

var ErrNotFound = errors.New("not found")

type Store struct {
	pool *pgxpool.Pool
}

// Open подключается к базе. Пустой url — значит, берём настройки из PGHOST, PGUSER, PGPASSWORD, PGDATABASE.
func Open(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse db config: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Migrate накатывает SQL-файлы из migrations/ по порядку, каждый — один раз.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, f := range files {
		var done bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, f).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		sql, err := migrations.ReadFile(f)
		if err != nil {
			return err
		}
		err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, f)
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %s: %w", f, err)
		}
	}
	return nil
}

// Snapshot читает весь прогресс разом — данных немного, так проще и честнее.
func (s *Store) Snapshot(ctx context.Context) (*model.Progress, error) {
	p := model.NewProgress()

	rows, err := s.pool.Query(ctx, `SELECT to_char(day, 'YYYY-MM-DD'), skill, level FROM marks WHERE level > 0`)
	if err != nil {
		return nil, err
	}
	if err := eachRow(rows, func() error {
		var day, skill string
		var level int
		if err := rows.Scan(&day, &skill, &level); err != nil {
			return err
		}
		if p.Marks[day] == nil {
			p.Marks[day] = map[string]int{}
		}
		p.Marks[day][skill] = level
		return nil
	}); err != nil {
		return nil, err
	}

	rows, err = s.pool.Query(ctx, `SELECT to_char(day, 'YYYY-MM-DD'), rest, note FROM days`)
	if err != nil {
		return nil, err
	}
	if err := eachRow(rows, func() error {
		var day string
		var m model.DayMeta
		if err := rows.Scan(&day, &m.Rest, &m.Note); err != nil {
			return err
		}
		p.Days[day] = m
		return nil
	}); err != nil {
		return nil, err
	}

	rows, err = s.pool.Query(ctx, `SELECT week, item, done FROM week_items`)
	if err != nil {
		return nil, err
	}
	if err := eachRow(rows, func() error {
		var week, item string
		var done bool
		if err := rows.Scan(&week, &item, &done); err != nil {
			return err
		}
		if p.WeekItems[week] == nil {
			p.WeekItems[week] = map[string]bool{}
		}
		p.WeekItems[week][item] = done
		return nil
	}); err != nil {
		return nil, err
	}

	rows, err = s.pool.Query(ctx, `SELECT week, good, hard, next_change, story, book, weight FROM reviews`)
	if err != nil {
		return nil, err
	}
	if err := eachRow(rows, func() error {
		var week string
		var r model.Review
		if err := rows.Scan(&week, &r.Good, &r.Hard, &r.Change, &r.Story, &r.Book, &r.Weight); err != nil {
			return err
		}
		p.Reviews[week] = r
		return nil
	}); err != nil {
		return nil, err
	}

	rows, err = s.pool.Query(ctx, `SELECT n FROM chapters`)
	if err != nil {
		return nil, err
	}
	if err := eachRow(rows, func() error {
		var n int
		if err := rows.Scan(&n); err != nil {
			return err
		}
		p.Chapters[n] = true
		return nil
	}); err != nil {
		return nil, err
	}

	rows, err = s.pool.Query(ctx, `SELECT to_char(day, 'YYYY-MM-DD'), skill, content, updated_at FROM guides`)
	if err != nil {
		return nil, err
	}
	if err := eachRow(rows, func() error {
		var day, skill string
		var g model.Guide
		if err := rows.Scan(&day, &skill, &g.Content, &g.UpdatedAt); err != nil {
			return err
		}
		if p.Guides[day] == nil {
			p.Guides[day] = map[string]model.Guide{}
		}
		p.Guides[day][skill] = g
		return nil
	}); err != nil {
		return nil, err
	}

	if p.Arena, err = s.Arena(ctx); err != nil {
		return nil, err
	}

	if p.Meals, err = s.Meals(ctx); err != nil {
		return nil, err
	}
	rows, err = s.pool.Query(ctx, `SELECT to_char(day, 'YYYY-MM-DD'), comment FROM food_days WHERE comment <> ''`)
	if err != nil {
		return nil, err
	}
	if err := eachRow(rows, func() error {
		var day, c string
		if err := rows.Scan(&day, &c); err != nil {
			return err
		}
		p.FoodDays[day] = c
		return nil
	}); err != nil {
		return nil, err
	}

	err = s.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = 'hero'`).Scan(&p.Hero)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	return p, nil
}

func eachRow(rows pgx.Rows, fn func() error) error {
	defer rows.Close()
	for rows.Next() {
		if err := fn(); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) SetMark(ctx context.Context, day, skill string, level int) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO marks (day, skill, level) VALUES ($1::date, $2, $3)
		ON CONFLICT (day, skill) DO UPDATE SET level = EXCLUDED.level, updated_at = now()`, day, skill, level)
	return err
}

// SetDay меняет только переданные поля: nil — оставить как есть.
func (s *Store) SetDay(ctx context.Context, day string, rest *bool, note *string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO days (day, rest, note) VALUES ($1::date, COALESCE($2, false), COALESCE($3, ''))
		ON CONFLICT (day) DO UPDATE SET
			rest = COALESCE($2, days.rest),
			note = COALESCE($3, days.note),
			updated_at = now()`, day, rest, note)
	return err
}

func (s *Store) SetWeekItem(ctx context.Context, week, item string, done bool) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO week_items (week, item, done) VALUES ($1, $2, $3)
		ON CONFLICT (week, item) DO UPDATE SET done = EXCLUDED.done, updated_at = now()`, week, item, done)
	return err
}

func (s *Store) SetReview(ctx context.Context, week string, r model.Review) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO reviews (week, good, hard, next_change, story, book, weight) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (week) DO UPDATE SET
			good = EXCLUDED.good, hard = EXCLUDED.hard, next_change = EXCLUDED.next_change,
			story = EXCLUDED.story, book = EXCLUDED.book, weight = EXCLUDED.weight, updated_at = now()`,
		week, r.Good, r.Hard, r.Change, r.Story, r.Book, r.Weight)
	return err
}

func (s *Store) SetChapter(ctx context.Context, n int, read bool) error {
	var err error
	if read {
		_, err = s.pool.Exec(ctx, `INSERT INTO chapters (n) VALUES ($1) ON CONFLICT DO NOTHING`, n)
	} else {
		_, err = s.pool.Exec(ctx, `DELETE FROM chapters WHERE n = $1`, n)
	}
	return err
}

func (s *Store) SetGuide(ctx context.Context, day, skill, content string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO guides (day, skill, content) VALUES ($1::date, $2, $3)
		ON CONFLICT (day, skill) DO UPDATE SET content = EXCLUDED.content, updated_at = now()`, day, skill, content)
	return err
}

func (s *Store) DeleteGuide(ctx context.Context, day, skill string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM guides WHERE day = $1::date AND skill = $2`, day, skill)
	return err
}

func (s *Store) SetHero(ctx context.Context, name string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO settings (key, value) VALUES ('hero', $1)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, name)
	return err
}

const arenaCols = `id, kind, lang, topic, question, answer, feedback, score, created_at, answered_at, reviewed_at`

func scanArena(row pgx.Row) (model.ArenaEntry, error) {
	var a model.ArenaEntry
	var score *int16
	err := row.Scan(&a.ID, &a.Kind, &a.Lang, &a.Topic, &a.Question, &a.Answer, &a.Feedback, &score, &a.CreatedAt, &a.AnsweredAt, &a.ReviewedAt)
	if score != nil {
		v := int(*score)
		a.Score = &v
	}
	return a, err
}

func (s *Store) Arena(ctx context.Context) ([]model.ArenaEntry, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+arenaCols+` FROM arena ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	out := []model.ArenaEntry{}
	err = eachRow(rows, func() error {
		a, err := scanArena(rows)
		out = append(out, a)
		return err
	})
	return out, err
}

func (s *Store) CreateArena(ctx context.Context, a model.ArenaEntry) (model.ArenaEntry, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO arena (kind, lang, topic, question, answer, feedback, score,
			answered_at, reviewed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7,
			CASE WHEN $5::text IS NULL THEN NULL ELSE now() END,
			CASE WHEN $6::text IS NULL THEN NULL ELSE now() END)
		RETURNING `+arenaCols, a.Kind, a.Lang, a.Topic, a.Question, a.Answer, a.Feedback, a.Score)
	return scanArena(row)
}

// ArenaPatch: nil-поля не меняются. Ответ ставит answered_at, разбор — reviewed_at.
type ArenaPatch struct {
	Answer   *string `json:"answer"`
	Feedback *string `json:"feedback"`
	Score    *int    `json:"score"`
	Topic    *string `json:"topic"`
}

func (s *Store) UpdateArena(ctx context.Context, id int64, p ArenaPatch) (model.ArenaEntry, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE arena SET
			answer      = COALESCE($2, answer),
			answered_at = CASE WHEN $2::text IS NULL THEN answered_at ELSE now() END,
			feedback    = COALESCE($3, feedback),
			reviewed_at = CASE WHEN $3::text IS NULL THEN reviewed_at ELSE now() END,
			score       = COALESCE($4, score),
			topic       = COALESCE($5, topic)
		WHERE id = $1
		RETURNING `+arenaCols, id, p.Answer, p.Feedback, p.Score, p.Topic)
	a, err := scanArena(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

func (s *Store) DeleteArena(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM arena WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ---------- сессии входа ----------

// CreateSession сохраняет хеш токена и заодно чистит истёкшие сессии.
func (s *Store) CreateSession(ctx context.Context, tokenHash []byte, expires time.Time) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO sessions (token_hash, expires_at) VALUES ($1, $2)`, tokenHash, expires)
	return err
}

func (s *Store) SessionValid(ctx context.Context, tokenHash []byte) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sessions WHERE token_hash = $1 AND expires_at > now())`, tokenHash).Scan(&ok)
	return ok, err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

// ---------- дневник питания ----------

const mealCols = `id, to_char(day, 'YYYY-MM-DD'), at, kind, description, protein, veggies, comment, photos, created_at`

func scanMeal(row pgx.Row) (model.Meal, error) {
	var m model.Meal
	err := row.Scan(&m.ID, &m.Day, &m.At, &m.Kind, &m.Description, &m.Protein, &m.Veggies, &m.Comment, &m.Photos, &m.CreatedAt)
	if m.Photos == nil {
		m.Photos = []string{}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

func (s *Store) Meals(ctx context.Context) ([]model.Meal, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+mealCols+` FROM meals ORDER BY day, at, id`)
	if err != nil {
		return nil, err
	}
	out := []model.Meal{}
	err = eachRow(rows, func() error {
		m, err := scanMeal(rows)
		out = append(out, m)
		return err
	})
	return out, err
}

func (s *Store) Meal(ctx context.Context, id int64) (model.Meal, error) {
	return scanMeal(s.pool.QueryRow(ctx, `SELECT `+mealCols+` FROM meals WHERE id = $1`, id))
}

func (s *Store) CreateMeal(ctx context.Context, m model.Meal) (model.Meal, error) {
	return scanMeal(s.pool.QueryRow(ctx, `
		INSERT INTO meals (day, at, kind, description, protein, veggies, comment)
		VALUES ($1::date, $2, $3, $4, $5, $6, $7)
		RETURNING `+mealCols, m.Day, m.At, m.Kind, m.Description, m.Protein, m.Veggies, m.Comment))
}

// MealPatch: nil-поля не меняются.
type MealPatch struct {
	At          *string `json:"at"`
	Kind        *string `json:"kind"`
	Description *string `json:"description"`
	Protein     *bool   `json:"protein"`
	Veggies     *bool   `json:"veggies"`
	Comment     *string `json:"comment"`
}

func (s *Store) UpdateMeal(ctx context.Context, id int64, p MealPatch) (model.Meal, error) {
	return scanMeal(s.pool.QueryRow(ctx, `
		UPDATE meals SET
			at          = COALESCE($2, at),
			kind        = COALESCE($3, kind),
			description = COALESCE($4, description),
			protein     = COALESCE($5, protein),
			veggies     = COALESCE($6, veggies),
			comment     = COALESCE($7, comment)
		WHERE id = $1
		RETURNING `+mealCols, id, p.At, p.Kind, p.Description, p.Protein, p.Veggies, p.Comment))
}

func (s *Store) AddMealPhoto(ctx context.Context, id int64, key string) (model.Meal, error) {
	return scanMeal(s.pool.QueryRow(ctx, `
		UPDATE meals SET photos = array_append(photos, $2) WHERE id = $1
		RETURNING `+mealCols, id, key))
}

// DeleteMeal удаляет запись и возвращает её фото, чтобы вызывающий удалил их из хранилища.
func (s *Store) DeleteMeal(ctx context.Context, id int64) ([]string, error) {
	var photos []string
	err := s.pool.QueryRow(ctx, `DELETE FROM meals WHERE id = $1 RETURNING photos`, id).Scan(&photos)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return photos, err
}

func (s *Store) SetFoodDay(ctx context.Context, day, comment string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO food_days (day, comment) VALUES ($1::date, $2)
		ON CONFLICT (day) DO UPDATE SET comment = EXCLUDED.comment, updated_at = now()`, day, comment)
	return err
}
