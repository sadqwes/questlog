// Package api — HTTP API questlog. Им пользуются веб-интерфейс и наставник (Claude) через curl.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sadqwes/questlog/internal/game"
	"github.com/sadqwes/questlog/internal/model"
	"github.com/sadqwes/questlog/internal/photos"
	"github.com/sadqwes/questlog/internal/plan"
	"github.com/sadqwes/questlog/internal/store"
)

// Store — то, что API нужно от хранилища. Интерфейс позволяет тестировать без Postgres.
type Store interface {
	Ping(ctx context.Context) error
	Snapshot(ctx context.Context) (*model.Progress, error)
	SetMark(ctx context.Context, day, skill string, level int) error
	SetDay(ctx context.Context, day string, rest *bool, note *string) error
	SetWeekItem(ctx context.Context, week, item string, done bool) error
	SetReview(ctx context.Context, week string, r model.Review) error
	SetChapter(ctx context.Context, n int, read bool) error
	SetGuide(ctx context.Context, day, skill, content string) error
	DeleteGuide(ctx context.Context, day, skill string) error
	SetHero(ctx context.Context, name string) error
	CreateArena(ctx context.Context, a model.ArenaEntry) (model.ArenaEntry, error)
	UpdateArena(ctx context.Context, id int64, p store.ArenaPatch) (model.ArenaEntry, error)
	DeleteArena(ctx context.Context, id int64) error
	Meal(ctx context.Context, id int64) (model.Meal, error)
	CreateMeal(ctx context.Context, m model.Meal) (model.Meal, error)
	UpdateMeal(ctx context.Context, id int64, p store.MealPatch) (model.Meal, error)
	AddMealPhoto(ctx context.Context, id int64, key string) (model.Meal, error)
	DeleteMeal(ctx context.Context, id int64) ([]string, error)
	SetFoodDay(ctx context.Context, day, comment string) error
}

type Server struct {
	plan   *plan.Plan
	store  Store
	log    *slog.Logger
	web    fs.FS
	photos photos.Store // nil — фото отключены (не настроен S3)

	requests *prometheus.CounterVec
	latency  *prometheus.HistogramVec
}

func New(p *plan.Plan, s Store, web fs.FS, log *slog.Logger, reg prometheus.Registerer) *Server {
	srv := &Server{
		plan: p, store: s, web: web, log: log,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "questlog_http_requests_total",
			Help: "HTTP requests by route, method and status code.",
		}, []string{"route", "method", "code"}),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "questlog_http_request_duration_seconds",
			Help:    "HTTP request latency by route.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route"}),
	}
	reg.MustRegister(srv.requests, srv.latency)
	return srv
}

// WithPhotos включает загрузку фото еды.
func (s *Server) WithPhotos(ps photos.Store) { s.photos = ps }

// State — всё, что нужно интерфейсу для отрисовки.
type State struct {
	Plan     *plan.Plan      `json:"plan"`
	Calendar []plan.Day      `json:"calendar"`
	Progress *model.Progress `json:"progress"`
	Stats    game.Stats      `json:"stats"`
}

func (s *Server) State(ctx context.Context) (State, error) {
	pr, err := s.store.Snapshot(ctx)
	if err != nil {
		return State{}, err
	}
	return State{Plan: s.plan, Calendar: s.plan.Calendar(), Progress: pr, Stats: game.Compute(s.plan, pr)}, nil
}

// Register вешает маршруты API и интерфейса на mux. Авторизацию и заголовки безопасности
// добавляет вызывающий код (main), обернув mux целиком.
func (s *Server) Register(mux *http.ServeMux) {
	h := func(pattern string, fn http.HandlerFunc) {
		_, route, _ := strings.Cut(pattern, " ") // метка route без метода: "/api/marks/{date}/{skill}"
		mux.Handle(pattern, s.instrument(route, fn))
	}

	h("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	h("GET /readyz", s.readyz)

	h("GET /api/state", s.getState)
	h("GET /api/days/{date}", s.getDay)
	h("PUT /api/marks/{date}/{skill}", s.putMark)
	h("PATCH /api/days/{date}", s.patchDay)
	h("PUT /api/weeks/{week}/items/{item}", s.putWeekItem)
	h("PUT /api/weeks/{week}/review", s.putReview)
	h("PUT /api/chapters/{n}", s.putChapter)
	h("PUT /api/guides/{date}/{skill}", s.putGuide)
	h("DELETE /api/guides/{date}/{skill}", s.deleteGuide)
	h("PUT /api/hero", s.putHero)
	h("GET /api/arena", s.listArena)
	h("POST /api/arena", s.createArena)
	h("PATCH /api/arena/{id}", s.patchArena)
	h("DELETE /api/arena/{id}", s.deleteArena)
	h("POST /api/meals", s.createMeal)
	h("PATCH /api/meals/{id}", s.patchMeal)
	h("DELETE /api/meals/{id}", s.deleteMeal)
	h("POST /api/meals/{id}/photos", s.uploadPhoto)
	h("GET /api/photos/{key...}", s.getPhoto)
	h("PUT /api/food-days/{date}", s.putFoodDay)

	// no-cache: браузер каждый раз сверяется с сервером — после деплоя сразу новый интерфейс, а не старый из кеша
	static := http.FileServerFS(s.web)
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		static.ServeHTTP(w, r)
	}))
}

// ---------- handlers ----------

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		http.Error(w, "db unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Write([]byte("ok"))
}

func (s *Server) getState(w http.ResponseWriter, r *http.Request) { s.respondState(w, r) }

func (s *Server) getDay(w http.ResponseWriter, r *http.Request) {
	day, ok := s.plan.Day(r.PathValue("date"))
	if !ok {
		s.fail(w, r, http.StatusNotFound, "день вне похода")
		return
	}
	pr, err := s.store.Snapshot(r.Context())
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"day":    day,
		"week":   s.plan.Weeks[day.Row],
		"marks":  pr.Marks[day.Date],
		"meta":   pr.Days[day.Date],
		"guides": pr.Guides[day.Date],
	})
}

func (s *Server) putMark(w http.ResponseWriter, r *http.Request) {
	date, skill := r.PathValue("date"), r.PathValue("skill")
	if !s.activeDay(date) || !s.plan.HasSkill(skill) {
		s.fail(w, r, http.StatusBadRequest, "неизвестный день или навык")
		return
	}
	var body struct {
		Level int `json:"level"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	if body.Level < model.LevelNone || body.Level > model.LevelFull {
		s.fail(w, r, http.StatusBadRequest, "level должен быть 0, 1 или 2")
		return
	}
	s.write(w, r, s.store.SetMark(r.Context(), date, skill, body.Level))
}

func (s *Server) patchDay(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	if !s.activeDay(date) {
		s.fail(w, r, http.StatusBadRequest, "неизвестный день")
		return
	}
	var body struct {
		Rest *bool   `json:"rest"`
		Note *string `json:"note"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	if body.Note != nil && len(*body.Note) > 2000 {
		s.fail(w, r, http.StatusBadRequest, "заметка длиннее 2000 символов")
		return
	}
	s.write(w, r, s.store.SetDay(r.Context(), date, body.Rest, body.Note))
}

var itemRe = regexp.MustCompile(`^([tl])(\d+)$`)

func (s *Server) putWeekItem(w http.ResponseWriter, r *http.Request) {
	week, ok := s.plan.Week(r.PathValue("week"))
	m := itemRe.FindStringSubmatch(r.PathValue("item"))
	if !ok || m == nil {
		s.fail(w, r, http.StatusBadRequest, "неизвестная неделя или пункт")
		return
	}
	i, _ := strconv.Atoi(m[2])
	if (m[1] == "t" && i >= len(week.Topics)) || (m[1] == "l" && i >= len(week.Lab)) {
		s.fail(w, r, http.StatusBadRequest, "такого пункта у босса нет")
		return
	}
	var body struct {
		Done bool `json:"done"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	s.write(w, r, s.store.SetWeekItem(r.Context(), week.ID, m[0], body.Done))
}

func (s *Server) putReview(w http.ResponseWriter, r *http.Request) {
	week, ok := s.plan.Week(r.PathValue("week"))
	if !ok {
		s.fail(w, r, http.StatusBadRequest, "неизвестная неделя")
		return
	}
	var body model.Review
	if !s.decode(w, r, &body) {
		return
	}
	s.write(w, r, s.store.SetReview(r.Context(), week.ID, body))
}

func (s *Server) putChapter(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n <= s.plan.ReadChapters || n > len(s.plan.Chapters) {
		s.fail(w, r, http.StatusBadRequest, fmt.Sprintf("глава должна быть от %d до %d", s.plan.ReadChapters+1, len(s.plan.Chapters)))
		return
	}
	var body struct {
		Read bool `json:"read"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	s.write(w, r, s.store.SetChapter(r.Context(), n, body.Read))
}

func (s *Server) putGuide(w http.ResponseWriter, r *http.Request) {
	date, skill := r.PathValue("date"), r.PathValue("skill")
	if !s.activeDay(date) || !s.plan.HasSkill(skill) {
		s.fail(w, r, http.StatusBadRequest, "неизвестный день или навык")
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Content) == "" || len(body.Content) > 64<<10 {
		s.fail(w, r, http.StatusBadRequest, "разбор пустой или длиннее 64 КБ")
		return
	}
	s.write(w, r, s.store.SetGuide(r.Context(), date, skill, body.Content))
}

func (s *Server) deleteGuide(w http.ResponseWriter, r *http.Request) {
	s.write(w, r, s.store.DeleteGuide(r.Context(), r.PathValue("date"), r.PathValue("skill")))
}

func (s *Server) putHero(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len([]rune(name)) > 30 {
		s.fail(w, r, http.StatusBadRequest, "имя от 1 до 30 символов")
		return
	}
	s.write(w, r, s.store.SetHero(r.Context(), name))
}

// listArena: ?status=new|answered|reviewed — удобно наставнику забрать ответы на разбор.
func (s *Server) listArena(w http.ResponseWriter, r *http.Request) {
	pr, err := s.store.Snapshot(r.Context())
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	status := r.URL.Query().Get("status")
	out := []model.ArenaEntry{}
	for _, a := range pr.Arena {
		if status == "" || a.Status() == status {
			out = append(out, a)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createArena(w http.ResponseWriter, r *http.Request) {
	var a model.ArenaEntry
	if !s.decode(w, r, &a) {
		return
	}
	if a.Lang == "" {
		a.Lang = "ru"
	}
	if msg := s.validateArena(a); msg != "" {
		s.fail(w, r, http.StatusBadRequest, msg)
		return
	}
	created, err := s.store.CreateArena(r.Context(), a)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) validateArena(a model.ArenaEntry) string {
	switch {
	case a.Kind != "q" && a.Kind != "mock":
		return "kind должен быть q или mock"
	case a.Lang != "ru" && a.Lang != "en":
		return "lang должен быть ru или en"
	case a.Kind == "q" && !s.plan.HasArenaTopic(a.Topic):
		return "topic должен быть из списка arenaTopics плана"
	case strings.TrimSpace(a.Question) == "" || len(a.Question) > 4000:
		return "question пустой или слишком длинный"
	case a.Score != nil && (*a.Score < 0 || *a.Score > 5):
		return "score от 0 до 5"
	}
	return ""
}

func (s *Server) patchArena(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "id должен быть числом")
		return
	}
	var p store.ArenaPatch
	if !s.decode(w, r, &p) {
		return
	}
	if p.Score != nil && (*p.Score < 0 || *p.Score > 5) {
		s.fail(w, r, http.StatusBadRequest, "score от 0 до 5")
		return
	}
	if p.Topic != nil && !s.plan.HasArenaTopic(*p.Topic) {
		s.fail(w, r, http.StatusBadRequest, "topic должен быть из списка arenaTopics плана")
		return
	}
	a, err := s.store.UpdateArena(r.Context(), id, p)
	if errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, http.StatusNotFound, "нет такого вопроса")
		return
	}
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) deleteArena(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "id должен быть числом")
		return
	}
	if err := s.store.DeleteArena(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, http.StatusNotFound, "нет такого вопроса")
		return
	} else if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- питание ----------

func validDate(d string) bool {
	_, err := time.Parse(plan.DateLayout, d)
	return err == nil
}

func (s *Server) createMeal(w http.ResponseWriter, r *http.Request) {
	var m model.Meal
	if !s.decode(w, r, &m) {
		return
	}
	switch {
	case !validDate(m.Day):
		s.fail(w, r, http.StatusBadRequest, "day в формате YYYY-MM-DD")
		return
	case !model.MealKinds[m.Kind]:
		s.fail(w, r, http.StatusBadRequest, "kind: breakfast, lunch, dinner, snack или drink")
		return
	case strings.TrimSpace(m.Description) == "" || len(m.Description) > 2000 || len(m.At) > 16 || len(m.Comment) > 8000:
		s.fail(w, r, http.StatusBadRequest, "описание пустое или слишком длинное")
		return
	}
	created, err := s.store.CreateMeal(r.Context(), m)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) mealID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "id должен быть числом")
		return 0, false
	}
	return id, true
}

func (s *Server) patchMeal(w http.ResponseWriter, r *http.Request) {
	id, ok := s.mealID(w, r)
	if !ok {
		return
	}
	var p store.MealPatch
	if !s.decode(w, r, &p) {
		return
	}
	if p.Kind != nil && !model.MealKinds[*p.Kind] {
		s.fail(w, r, http.StatusBadRequest, "kind: breakfast, lunch, dinner, snack или drink")
		return
	}
	m, err := s.store.UpdateMeal(r.Context(), id, p)
	if errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, http.StatusNotFound, "нет такой записи")
		return
	}
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) deleteMeal(w http.ResponseWriter, r *http.Request) {
	id, ok := s.mealID(w, r)
	if !ok {
		return
	}
	keys, err := s.store.DeleteMeal(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, http.StatusNotFound, "нет такой записи")
		return
	}
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	for _, k := range keys {
		if s.photos == nil {
			break
		}
		if err := s.photos.Delete(r.Context(), k); err != nil {
			s.log.Warn("photo delete failed", "key", k, "err", err) // запись уже удалена — фото уберём вручную
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) uploadPhoto(w http.ResponseWriter, r *http.Request) {
	if s.photos == nil {
		s.fail(w, r, http.StatusServiceUnavailable, "хранилище фото не настроено")
		return
	}
	id, ok := s.mealID(w, r)
	if !ok {
		return
	}
	if _, err := s.store.Meal(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, http.StatusNotFound, "нет такой записи")
		return
	} else if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, photos.MaxUpload+(1<<20))
	file, _, err := r.FormFile("photo")
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "нужен файл в поле photo, не больше 15 МБ")
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(file)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "файл не прочитался")
		return
	}
	jpg, err := photos.Normalize(raw)
	if errors.Is(err, photos.ErrNotImage) {
		s.fail(w, r, http.StatusBadRequest, "это не фото: нужен JPEG, PNG или WebP")
		return
	} else if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	key := photos.NewKey(id)
	if err := s.photos.Put(r.Context(), key, jpg); err != nil {
		s.fail(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	m, err := s.store.AddMealPhoto(r.Context(), id, key)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) getPhoto(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if s.photos == nil || !photos.ValidKey(key) {
		http.NotFound(w, r)
		return
	}
	body, size, err := s.photos.Get(r.Context(), key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Cache-Control", "private, max-age=604800, immutable") // ключ случайный и не меняется
	io.Copy(w, body)
}

func (s *Server) putFoodDay(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	if !validDate(date) {
		s.fail(w, r, http.StatusBadRequest, "день в формате YYYY-MM-DD")
		return
	}
	var body struct {
		Comment string `json:"comment"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	if len(body.Comment) > 16000 {
		s.fail(w, r, http.StatusBadRequest, "комментарий слишком длинный")
		return
	}
	s.write(w, r, s.store.SetFoodDay(r.Context(), date, body.Comment))
}

// ---------- helpers ----------

func (s *Server) activeDay(date string) bool {
	d, ok := s.plan.Day(date)
	return ok && !d.Pre
}

// write завершает изменяющий запрос: при успехе отдаёт свежее состояние, чтобы интерфейсу не нужен был второй запрос.
func (s *Server) write(w http.ResponseWriter, r *http.Request, err error) {
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	s.respondState(w, r)
}

func (s *Server) respondState(w http.ResponseWriter, r *http.Request) {
	st, err := s.State(r.Context())
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		s.fail(w, r, http.StatusBadRequest, "некорректный JSON: "+err.Error())
		return false
	}
	return true
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, code int, msg string) {
	if code >= 500 {
		s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", msg)
		msg = "внутренняя ошибка, подробности в логах"
	}
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) instrument(route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.requests.WithLabelValues(route, r.Method, strconv.Itoa(rec.code)).Inc()
		s.latency.WithLabelValues(route).Observe(time.Since(start).Seconds())
	})
}

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		// same-origin, а не no-referrer: при no-referrer браузер шлёт формы с "Origin: null",
		// и проверка CSRF в auth не может узнать свой сайт
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' https://fonts.googleapis.com; font-src https://fonts.gstatic.com; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}
