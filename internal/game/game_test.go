package game

import (
	"testing"

	"github.com/sadqwes/questlog/internal/model"
	"github.com/sadqwes/questlog/internal/plan"
)

func mustPlan(t *testing.T) *plan.Plan {
	t.Helper()
	p, err := plan.Load("")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLevelOf(t *testing.T) {
	cases := []struct{ xp, want int }{
		{0, 1}, {99, 1}, {100, 2}, {299, 2}, {300, 3}, {600, 4},
	}
	for _, c := range cases {
		if got := LevelOf(c.xp, 100); got != c.want {
			t.Errorf("LevelOf(%d) = %d, want %d", c.xp, got, c.want)
		}
		l := LevelOf(c.xp, 100)
		if c.xp < Threshold(l, 100) || c.xp >= Threshold(l+1, 100) {
			t.Errorf("xp %d outside [%d, %d) of level %d", c.xp, Threshold(l, 100), Threshold(l+1, 100), l)
		}
	}
}

func TestComputeCountsMarksAndRest(t *testing.T) {
	p := mustPlan(t)
	pr := model.NewProgress()
	pr.Marks["2026-09-24"] = map[string]int{"cka": 2, "book": 1, "eng": 1, "body": 1, "mind": 1}
	pr.Marks["2026-09-22"] = map[string]int{"cka": 2} // до старта — не считается
	pr.Days["2026-09-25"] = model.DayMeta{Rest: true}

	s := Compute(p, pr)

	if got := s.Skills["cka"].XP; got != XPFull {
		t.Errorf("cka xp = %d, want %d", got, XPFull)
	}
	want := XPFull + 4*XPMin + XPRest
	if s.Hero.XP != want {
		t.Errorf("hero xp = %d, want %d", s.Hero.XP, want)
	}
	if !s.Counters.AllFive || s.Counters.Mins != 4 || s.Counters.Rests != 1 {
		t.Errorf("counters = %+v", s.Counters)
	}
}

func TestSkipsNeverCostXP(t *testing.T) {
	p := mustPlan(t)
	pr := model.NewProgress()
	pr.Marks["2026-09-24"] = map[string]int{"cka": 1}
	before := Compute(p, pr).Hero.XP
	// пустая неделя после отметки не должна ничего отнять
	for _, d := range []string{"2026-09-28", "2026-09-29", "2026-09-30"} {
		pr.Days[d] = model.DayMeta{}
	}
	if after := Compute(p, pr).Hero.XP; after != before {
		t.Errorf("xp changed from %d to %d on empty days", before, after)
	}
}

func TestBossAndArena(t *testing.T) {
	p := mustPlan(t)
	pr := model.NewProgress()
	w := p.Weeks[0]
	pr.WeekItems[w.ID] = map[string]bool{}
	for i := range w.Topics {
		pr.WeekItems[w.ID][TopicItem(i)] = true
	}
	for i := range w.Lab {
		pr.WeekItems[w.ID][LabItem(i)] = true
	}
	ans, fb, score := "ответ", "разбор", 4
	pr.Arena = []model.ArenaEntry{
		{Kind: "q", Lang: "en", Topic: "Linux", Answer: &ans, Feedback: &fb, Score: &score},
		{Kind: "q", Lang: "ru", Topic: "Linux", Answer: &ans}, // ждёт разбора — опыта ещё нет
		{Kind: "q", Lang: "ru", Topic: "Сети"},
	}

	s := Compute(p, pr)

	if b := s.Bosses[w.ID]; !b.Down || b.Left != 0 {
		t.Errorf("boss %s = %+v, want down", w.ID, b)
	}
	if s.Counters.ArenaDone != 1 || s.Counters.ArenaWait != 1 || s.Counters.ArenaNew != 1 {
		t.Errorf("arena counters = %+v", s.Counters)
	}
	if s.Skills["eng"].XP != XPArenaEN {
		t.Errorf("eng xp = %d, want %d", s.Skills["eng"].XP, XPArenaEN)
	}
	if ts := s.Topics["Linux"]; ts.Count != 1 || ts.Avg != 4 {
		t.Errorf("Linux topic = %+v", ts)
	}
}

func TestCalendarQuests(t *testing.T) {
	p := mustPlan(t)
	d, ok := p.Day("2026-09-24")
	if !ok || d.Pre || d.Chapter != 10 {
		t.Fatalf("2026-09-24 = %+v, want chapter 10 day", d)
	}
	if len(d.Quests) != 5 {
		t.Errorf("quests = %d, want 5", len(d.Quests))
	}
	if d, _ := p.Day("2026-09-21"); !d.Pre || len(d.Quests) != 0 {
		t.Errorf("2026-09-21 should be before start with no quests: %+v", d)
	}
	if d, _ := p.Day("2026-09-26"); !d.Weekend || len(d.Quests) != 0 {
		t.Errorf("2026-09-26 should be a quest-free weekend: %+v", d)
	}
}
