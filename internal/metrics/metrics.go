// Package metrics отдаёт прогресс похода в Prometheus: опыт, уровни, HP боссов, оценки арены.
// Значения считаются в момент scrape, поэтому в Grafana видно ровно то же, что в интерфейсе.
package metrics

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sadqwes/questlog/internal/game"
)

type Source func(ctx context.Context) (game.Stats, error)

type Collector struct {
	src Source
	log *slog.Logger

	up, heroXP, heroLevel, skillXP, skillLevel, bossHP, chapters, arena, topicScore, achievements, meals *prometheus.Desc
}

func New(src Source, log *slog.Logger) *Collector {
	d := func(name, help string, labels ...string) *prometheus.Desc {
		return prometheus.NewDesc("questlog_"+name, help, labels, nil)
	}
	return &Collector{
		src: src, log: log,
		up:           d("progress_up", "1 if progress was read from the database on this scrape."),
		heroXP:       d("hero_xp", "Total character XP."),
		heroLevel:    d("hero_level", "Character level."),
		skillXP:      d("skill_xp", "XP per skill.", "skill"),
		skillLevel:   d("skill_level", "Level per skill.", "skill"),
		bossHP:       d("boss_hp", "Remaining HP of the weekly boss (unchecked items).", "week"),
		chapters:     d("book_chapters_read", "New chapters of Kubernetes in Action read during the quest."),
		arena:        d("arena_questions", "Interview arena questions by status.", "status"),
		topicScore:   d("arena_topic_score", "Average interview score per topic, 0-5.", "topic"),
		achievements: d("achievements_unlocked", "Number of unlocked achievements."),
		meals:        d("meals_logged", "Meals logged in the food diary."),
	}
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.up, c.heroXP, c.heroLevel, c.skillXP, c.skillLevel, c.bossHP, c.chapters, c.arena, c.topicScore, c.achievements, c.meals} {
		ch <- d
	}
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	g := func(d *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := c.src(ctx)
	if err != nil {
		c.log.Warn("metrics: cannot read progress", "err", err)
		g(c.up, 0)
		return
	}
	g(c.up, 1)
	g(c.heroXP, float64(s.Hero.XP))
	g(c.heroLevel, float64(s.Hero.Level))
	for id, b := range s.Skills {
		g(c.skillXP, float64(b.XP), id)
		g(c.skillLevel, float64(b.Level), id)
	}
	for id, b := range s.Bosses {
		g(c.bossHP, float64(b.Left), id)
	}
	g(c.chapters, float64(s.Counters.Chapters))
	g(c.arena, float64(s.Counters.ArenaNew), "new")
	g(c.arena, float64(s.Counters.ArenaWait), "answered")
	g(c.arena, float64(s.Counters.ArenaDone+s.Counters.Mocks), "reviewed")
	for t, ts := range s.Topics {
		if ts.Count > 0 {
			g(c.topicScore, ts.Avg, t)
		}
	}
	got := 0
	for _, a := range s.Achievements {
		if a.Got {
			got++
		}
	}
	g(c.achievements, float64(got))
	g(c.meals, float64(s.Counters.Meals))
}
