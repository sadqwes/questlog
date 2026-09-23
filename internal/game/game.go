// Package game превращает прогресс в опыт, уровни и достижения.
// Правило одно: опыт только прибавляется, пропуски ничего не отнимают.
package game

import (
	"strconv"

	"github.com/sadqwes/questlog/internal/model"
	"github.com/sadqwes/questlog/internal/plan"
)

const (
	XPMin     = 10 // квест по минимуму
	XPFull    = 25 // квест полностью
	XPRest    = 5  // честный привал
	XPChapter = 50 // новая глава книги
	XPTopic   = 15 // пункт CKA у босса
	XPLab     = 30 // пункт лабы у босса
	XPReview  = 20 // пятничная хроника
	XPArena   = 15 // разобранный вопрос арены
	XPArenaEN = 10 // ...и ещё красноречию, если по-английски
	XPMock    = 60 // пробное собеседование
	XPMockEN  = 20
	heroBase  = 100
	skillBase = 40
)

// LevelOf: уровень n+1 открывается, когда опыта набралось base*n*(n+1)/2.
func LevelOf(xp, base int) int {
	n := 0
	for base*(n+1)*(n+2)/2 <= xp {
		n++
	}
	return n + 1
}

// Threshold — сколько опыта нужно, чтобы дойти до уровня level.
func Threshold(level, base int) int {
	n := level - 1
	return base * n * (n + 1) / 2
}

type Bar struct {
	XP    int `json:"xp"`
	Level int `json:"level"`
	From  int `json:"from"` // порог текущего уровня
	To    int `json:"to"`   // порог следующего
}

func bar(xp, base int) Bar {
	l := LevelOf(xp, base)
	return Bar{XP: xp, Level: l, From: Threshold(l, base), To: Threshold(l+1, base)}
}

type Boss struct {
	Left  int  `json:"left"`
	Total int  `json:"total"`
	Down  bool `json:"down"`
}

type Achievement struct {
	ID   string `json:"id"`
	Icon string `json:"icon"`
	Name string `json:"name"`
	Desc string `json:"desc"`
	Got  bool   `json:"got"`
}

type TopicScore struct {
	Count int     `json:"count"`
	Avg   float64 `json:"avg"`
}

type Counters struct {
	Mins       int  `json:"mins"`
	Rests      int  `json:"rests"`
	BonusWE    int  `json:"bonusWeekend"`
	AllFive    bool `json:"allFive"`
	EngDays    int  `json:"engDays"`
	Reviews    int  `json:"reviews"`
	BossesDown int  `json:"bossesDown"`
	Chapters   int  `json:"chapters"`
	AnyMark    bool `json:"anyMark"`
	ArenaDone  int  `json:"arenaDone"`
	ArenaWait  int  `json:"arenaWaiting"` // отвечены, ждут разбора
	ArenaNew   int  `json:"arenaNew"`     // заданы, ждут ответа
	Mocks      int  `json:"mocks"`
	EnAnswers  int  `json:"enAnswers"`
}

type Stats struct {
	Hero         Bar                   `json:"hero"`
	Skills       map[string]Bar        `json:"skills"`
	Bosses       map[string]Boss       `json:"bosses"`
	Topics       map[string]TopicScore `json:"topics"`
	Counters     Counters              `json:"counters"`
	Achievements []Achievement         `json:"achievements"`
}

func Compute(p *plan.Plan, pr *model.Progress) Stats {
	xp := map[string]int{}
	extra := 0
	var c Counters

	for _, d := range p.Calendar() {
		if d.Pre {
			continue
		}
		marks := pr.Marks[d.Date]
		n := 0
		for _, s := range p.Skills {
			switch marks[s.ID] {
			case model.LevelMin:
				xp[s.ID] += XPMin
				c.Mins++
			case model.LevelFull:
				xp[s.ID] += XPFull
			default:
				continue
			}
			n++
			c.AnyMark = true
			if d.Weekend {
				c.BonusWE++
			}
		}
		if n == len(p.Skills) {
			c.AllFive = true
		}
		if marks["eng"] > 0 {
			c.EngDays++
		}
		if pr.Days[d.Date].Rest {
			extra += XPRest
			c.Rests++
		}
	}

	for n, read := range pr.Chapters {
		if read && n > p.ReadChapters {
			xp["book"] += XPChapter
			c.Chapters++
		}
	}

	bosses := map[string]Boss{}
	for _, w := range p.Weeks {
		items := pr.WeekItems[w.ID]
		b := Boss{Total: len(w.Topics) + len(w.Lab)}
		for i := range w.Topics {
			if items[TopicItem(i)] {
				xp["cka"] += XPTopic
			} else {
				b.Left++
			}
		}
		for i := range w.Lab {
			if items[LabItem(i)] {
				xp["cka"] += XPLab
			} else {
				b.Left++
			}
		}
		b.Down = b.Left == 0
		if b.Down {
			c.BossesDown++
		}
		bosses[w.ID] = b
		if pr.Reviews[w.ID].Filled() {
			extra += XPReview
			c.Reviews++
		}
	}

	topics := map[string]TopicScore{}
	sums := map[string]int{}
	for _, t := range p.ArenaTopics {
		topics[t] = TopicScore{}
	}
	for _, a := range pr.Arena {
		switch a.Status() {
		case "new":
			c.ArenaNew++
			continue
		case "answered":
			c.ArenaWait++
			continue
		}
		en := a.Lang == "en"
		if a.Kind == "mock" {
			c.Mocks++
			extra += XPMock
			if en {
				xp["eng"] += XPMockEN
			}
		} else {
			c.ArenaDone++
			extra += XPArena
			if en {
				xp["eng"] += XPArenaEN
			}
			if ts, ok := topics[a.Topic]; ok && a.Score != nil {
				ts.Count++
				sums[a.Topic] += *a.Score
				ts.Avg = float64(sums[a.Topic]) / float64(ts.Count)
				topics[a.Topic] = ts
			}
		}
		if en {
			c.EnAnswers++
		}
	}

	total := extra
	skills := map[string]Bar{}
	for _, s := range p.Skills {
		total += xp[s.ID]
		skills[s.ID] = bar(xp[s.ID], skillBase)
	}

	return Stats{
		Hero:         bar(total, heroBase),
		Skills:       skills,
		Bosses:       bosses,
		Topics:       topics,
		Counters:     c,
		Achievements: achievements(c),
	}
}

// Ключи пунктов босса: t0, t1… — темы CKA, l0, l1… — задачи лабы.
func TopicItem(i int) string { return "t" + strconv.Itoa(i) }
func LabItem(i int) string   { return "l" + strconv.Itoa(i) }

func achievements(c Counters) []Achievement {
	list := []struct {
		id, icon, name, desc string
		got                  bool
	}{
		{"first", "1", "Первый квест", "Отметить любой квест", c.AnyMark},
		{"min", "M", "Сила минимума", "10 квестов по минимуму — они тоже двигают", c.Mins >= 10},
		{"rest", "Z", "Честный отдых", "Взять день отдыха без чувства вины", c.Rests >= 1},
		{"tavern", "T", "Гость таверны", "Бонусный квест на выходных", c.BonusWE >= 1},
		{"five", "5", "Пять стихий", "Все пять навыков за один день", c.AllFive},
		{"eng", "E", "Полиглот", "Английский в 10 разных дней", c.EngDays >= 10},
		{"book3", "B", "Книжный червь", "Прочитать 3 новые главы", c.Chapters >= 3},
		{"book9", "K", "Том закрыт", "Дочитать Kubernetes in Action до конца", c.Chapters >= 9},
		{"boss", "X", "Победитель босса", "Повергнуть первого босса", c.BossesDown >= 1},
		{"chron", "C", "Летописец", "Написать 3 пятничные хроники", c.Reviews >= 3},
		{"ar1", "?", "Первый вопрос", "Разобрать вопрос арены — даже «не знаю»", c.ArenaDone >= 1},
		{"ar20", "Q", "Тёртый калач", "20 разобранных вопросов на арене", c.ArenaDone >= 20},
		{"mock", "I", "Пробный бой", "Пройти пробное собеседование", c.Mocks >= 1},
		{"brave", "!", "Смелость", "Первый ответ на арене по-английски", c.EnAnswers >= 1},
	}
	out := make([]Achievement, 0, len(list))
	for _, a := range list {
		out = append(out, Achievement{ID: a.id, Icon: a.icon, Name: a.name, Desc: a.desc, Got: a.got})
	}
	return out
}
