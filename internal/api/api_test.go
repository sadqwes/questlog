package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
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

func newTestServer(t *testing.T) (*httptest.Server, *memStore) {
	t.Helper()
	p, err := plan.Load("")
	if err != nil {
		t.Fatal(err)
	}
	mem := newMem()
	web := fstest.MapFS{"index.html": {Data: []byte("<title>questlog</title>")}}
	srv := New(p, mem, web, slog.New(slog.NewTextHandler(io.Discard, nil)), prometheus.NewRegistry())
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
	if rp := resp.Header.Get("Referrer-Policy"); rp != "same-origin" {
		t.Errorf("Referrer-Policy = %q, want same-origin", rp)
	}
}
