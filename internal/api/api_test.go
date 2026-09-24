package api

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sadqwes/questlog/internal/model"
	"github.com/sadqwes/questlog/internal/plan"
	"github.com/sadqwes/questlog/internal/store"
)

// memStore — хранилище в памяти, только для тестов.
type memStore struct {
	p      *model.Progress
	nextID int64
}

func newMem() *memStore { return &memStore{p: model.NewProgress()} }

func (m *memStore) Ping(context.Context) error                        { return nil }
func (m *memStore) Snapshot(context.Context) (*model.Progress, error) { return m.p, nil }
func (m *memStore) SetMark(_ context.Context, day, skill string, level int) error {
	if m.p.Marks[day] == nil {
		m.p.Marks[day] = map[string]int{}
	}
	m.p.Marks[day][skill] = level
	return nil
}
func (m *memStore) SetDay(_ context.Context, day string, rest *bool, note *string) error {
	d := m.p.Days[day]
	if rest != nil {
		d.Rest = *rest
	}
	if note != nil {
		d.Note = *note
	}
	m.p.Days[day] = d
	return nil
}
func (m *memStore) SetWeekItem(_ context.Context, week, item string, done bool) error {
	if m.p.WeekItems[week] == nil {
		m.p.WeekItems[week] = map[string]bool{}
	}
	m.p.WeekItems[week][item] = done
	return nil
}
func (m *memStore) SetReview(_ context.Context, week string, r model.Review) error {
	m.p.Reviews[week] = r
	return nil
}
func (m *memStore) SetChapter(_ context.Context, n int, read bool) error {
	m.p.Chapters[n] = read
	return nil
}
func (m *memStore) SetGuide(_ context.Context, day, skill, content string) error {
	if m.p.Guides[day] == nil {
		m.p.Guides[day] = map[string]model.Guide{}
	}
	m.p.Guides[day][skill] = model.Guide{Content: content}
	return nil
}
func (m *memStore) DeleteGuide(_ context.Context, day, skill string) error {
	delete(m.p.Guides[day], skill)
	return nil
}
func (m *memStore) SetHero(_ context.Context, name string) error { m.p.Hero = name; return nil }
func (m *memStore) CreateArena(_ context.Context, a model.ArenaEntry) (model.ArenaEntry, error) {
	m.nextID++
	a.ID = m.nextID
	m.p.Arena = append(m.p.Arena, a)
	return a, nil
}
func (m *memStore) UpdateArena(_ context.Context, id int64, p store.ArenaPatch) (model.ArenaEntry, error) {
	for i := range m.p.Arena {
		a := &m.p.Arena[i]
		if a.ID != id {
			continue
		}
		if p.Answer != nil {
			a.Answer = p.Answer
		}
		if p.Feedback != nil {
			a.Feedback = p.Feedback
		}
		if p.Score != nil {
			a.Score = p.Score
		}
		return *a, nil
	}
	return model.ArenaEntry{}, store.ErrNotFound
}
func (m *memStore) DeleteArena(context.Context, int64) error { return store.ErrNotFound }
func (m *memStore) Meal(_ context.Context, id int64) (model.Meal, error) {
	for _, x := range m.p.Meals {
		if x.ID == id {
			return x, nil
		}
	}
	return model.Meal{}, store.ErrNotFound
}
func (m *memStore) CreateMeal(_ context.Context, meal model.Meal) (model.Meal, error) {
	m.nextID++
	meal.ID, meal.Photos = m.nextID, []string{}
	m.p.Meals = append(m.p.Meals, meal)
	return meal, nil
}
func (m *memStore) UpdateMeal(_ context.Context, id int64, p store.MealPatch) (model.Meal, error) {
	for i := range m.p.Meals {
		if m.p.Meals[i].ID == id {
			if p.Comment != nil {
				m.p.Meals[i].Comment = *p.Comment
			}
			return m.p.Meals[i], nil
		}
	}
	return model.Meal{}, store.ErrNotFound
}
func (m *memStore) AddMealPhoto(_ context.Context, id int64, key string) (model.Meal, error) {
	for i := range m.p.Meals {
		if m.p.Meals[i].ID == id {
			m.p.Meals[i].Photos = append(m.p.Meals[i].Photos, key)
			return m.p.Meals[i], nil
		}
	}
	return model.Meal{}, store.ErrNotFound
}
func (m *memStore) DeleteMeal(_ context.Context, id int64) ([]string, error) {
	for i, x := range m.p.Meals {
		if x.ID == id {
			m.p.Meals = append(m.p.Meals[:i], m.p.Meals[i+1:]...)
			return x.Photos, nil
		}
	}
	return nil, store.ErrNotFound
}
func (m *memStore) SetFoodDay(_ context.Context, day, c string) error {
	m.p.FoodDays[day] = c
	return nil
}

// memPhotos — хранилище фото в памяти.
type memPhotos struct{ m map[string][]byte }

func (p *memPhotos) Put(_ context.Context, k string, b []byte) error { p.m[k] = b; return nil }
func (p *memPhotos) Get(_ context.Context, k string) (io.ReadCloser, int64, error) {
	b, ok := p.m[k]
	if !ok {
		return nil, 0, store.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), int64(len(b)), nil
}
func (p *memPhotos) Delete(_ context.Context, k string) error { delete(p.m, k); return nil }

func newTestServer(t *testing.T) (*httptest.Server, *memStore) {
	t.Helper()
	p, err := plan.Load("")
	if err != nil {
		t.Fatal(err)
	}
	mem := newMem()
	web := fstest.MapFS{"index.html": {Data: []byte("<title>questlog</title>")}}
	srv := New(p, mem, web, slog.New(slog.NewTextHandler(io.Discard, nil)), prometheus.NewRegistry())
	srv.WithPhotos(&memPhotos{m: map[string][]byte{}})
	mux := http.NewServeMux()
	srv.Register(mux)
	ts := httptest.NewServer(SecurityHeaders(mux))
	t.Cleanup(ts.Close)
	return ts, mem
}

func do(t *testing.T, ts *httptest.Server, method, path, body string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestPutMarkReturnsFreshState(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, body := do(t, ts, "PUT", "/api/marks/2026-09-24/cka", `{"level":2}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var st State
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatal(err)
	}
	if st.Stats.Skills["cka"].XP != 25 {
		t.Errorf("cka xp = %d, want 25", st.Stats.Skills["cka"].XP)
	}
}

func TestValidation(t *testing.T) {
	ts, _ := newTestServer(t)
	cases := []struct{ method, path, body string }{
		{"PUT", "/api/marks/2026-09-24/cka", `{"level":3}`},   // уровень вне 0..2
		{"PUT", "/api/marks/2026-09-22/cka", `{"level":1}`},   // день до старта
		{"PUT", "/api/marks/2026-09-24/chess", `{"level":1}`}, // нет такого навыка
		{"PUT", "/api/marks/2026-09-24/cka", `{"lvl":1}`},     // лишнее поле
		{"PUT", "/api/weeks/w1/items/t99", `{"done":true}`},   // нет такого пункта
		{"PUT", "/api/chapters/5", `{"read":true}`},           // глава уже прочитана до похода
		{"POST", "/api/arena", `{"kind":"q","topic":"Шахматы","question":"?"}`},
		{"POST", "/api/arena", `{"kind":"q","topic":"Linux","question":"  "}`},
	}
	for _, c := range cases {
		if resp, body := do(t, ts, c.method, c.path, c.body); resp.StatusCode != 400 {
			t.Errorf("%s %s %s: status %d, want 400 (%s)", c.method, c.path, c.body, resp.StatusCode, body)
		}
	}
}

func TestArenaFlow(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, body := do(t, ts, "POST", "/api/arena", `{"kind":"q","lang":"en","topic":"Linux","question":"What is an inode?"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}
	var a model.ArenaEntry
	json.Unmarshal(body, &a)

	do(t, ts, "PATCH", "/api/arena/1", `{"answer":"metadata of a file"}`)
	_, body = do(t, ts, "GET", "/api/arena?status=answered", "")
	var waiting []model.ArenaEntry
	json.Unmarshal(body, &waiting)
	if len(waiting) != 1 || waiting[0].Question != a.Question {
		t.Fatalf("answered list = %s", body)
	}

	do(t, ts, "PATCH", "/api/arena/1", `{"feedback":"## Что хорошо\n...","score":3}`)
	_, body = do(t, ts, "GET", "/api/state", "")
	var st State
	json.Unmarshal(body, &st)
	if st.Stats.Counters.ArenaDone != 1 || st.Stats.Skills["eng"].XP != 10 {
		t.Errorf("counters=%+v eng=%+v", st.Stats.Counters, st.Stats.Skills["eng"])
	}
}

func TestServesUI(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, body := do(t, ts, "GET", "/", "")
	if resp.StatusCode != 200 || !strings.Contains(string(body), "questlog") {
		t.Fatalf("GET / = %d %s", resp.StatusCode, body)
	}
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Error("missing CSP header")
	}
	// с no-referrer браузер отправляет форму входа с "Origin: null", и CSRF-проверка её отклоняет
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("static Cache-Control = %q, want no-cache (otherwise a deploy shows the old UI)", cc)
	}
	if rp := resp.Header.Get("Referrer-Policy"); rp != "same-origin" {
		t.Errorf("Referrer-Policy = %q, want same-origin", rp)
	}
}

func TestFoodDiaryWithPhoto(t *testing.T) {
	ts, mem := newTestServer(t)
	resp, body := do(t, ts, "POST", "/api/meals", `{"day":"2026-09-24","at":"13:30","kind":"lunch","description":"ролл с грудкой","protein":true,"veggies":true}`)
	if resp.StatusCode != 201 {
		t.Fatalf("create meal: %d %s", resp.StatusCode, body)
	}
	if r, b := do(t, ts, "POST", "/api/meals", `{"day":"24.09","kind":"lunch","description":"x"}`); r.StatusCode != 400 {
		t.Errorf("bad date accepted: %d %s", r.StatusCode, b)
	}
	if r, _ := do(t, ts, "POST", "/api/meals", `{"day":"2026-09-24","kind":"feast","description":"x"}`); r.StatusCode != 400 {
		t.Errorf("bad kind accepted")
	}

	// загрузка фото: PNG превращается в JPEG и попадает в хранилище
	var img bytes.Buffer
	png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 10, 10)))
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	fw, _ := mw.CreateFormFile("photo", "lunch.png")
	fw.Write(img.Bytes())
	mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/meals/1/photos", &form)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	up, err := http.DefaultClient.Do(req)
	if err != nil || up.StatusCode != 201 {
		t.Fatalf("upload: %v %v", err, up.StatusCode)
	}
	up.Body.Close()
	key := mem.p.Meals[0].Photos[0]
	if r, b := do(t, ts, "GET", "/api/photos/"+key, ""); r.StatusCode != 200 || r.Header.Get("Content-Type") != "image/jpeg" || len(b) == 0 {
		t.Errorf("get photo: %d %q", r.StatusCode, r.Header.Get("Content-Type"))
	}
	if r, _ := do(t, ts, "GET", "/api/photos/../secret.jpg", ""); r.StatusCode == 200 {
		t.Error("path traversal served a file")
	}

	_, body = do(t, ts, "GET", "/api/state", "")
	var st State
	json.Unmarshal(body, &st)
	if st.Stats.Counters.Meals != 1 || st.Stats.Counters.FoodDays != 1 {
		t.Errorf("food counters = %+v", st.Stats.Counters)
	}

	if r, _ := do(t, ts, "DELETE", "/api/meals/1", ""); r.StatusCode != 204 {
		t.Errorf("delete meal = %d", r.StatusCode)
	}
	if r, _ := do(t, ts, "GET", "/api/photos/"+key, ""); r.StatusCode != 404 {
		t.Error("photo still served after meal was deleted")
	}
}
