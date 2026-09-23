// Package model — прогресс похода в том виде, в каком его хранит база и отдаёт API.
package model

import "time"

// Level квеста: 0 — не отмечен, 1 — минимум, 2 — полностью.
const (
	LevelNone = 0
	LevelMin  = 1
	LevelFull = 2
)

type DayMeta struct {
	Rest bool   `json:"rest"`
	Note string `json:"note"`
}

type Review struct {
	Good   string `json:"good"`
	Hard   string `json:"hard"`
	Change string `json:"change"`
	Story  string `json:"story"`
	Book   string `json:"book"`
	Weight string `json:"weight"`
}

func (r Review) Filled() bool { return r.Good != "" || r.Hard != "" || r.Change != "" }

type Guide struct {
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ArenaEntry — вопрос с собеседования (kind=q) или итог пробного собеседования (kind=mock).
type ArenaEntry struct {
	ID         int64      `json:"id"`
	Kind       string     `json:"kind"`
	Lang       string     `json:"lang"`
	Topic      string     `json:"topic"`
	Question   string     `json:"question"`
	Answer     *string    `json:"answer"`
	Feedback   *string    `json:"feedback"`
	Score      *int       `json:"score"`
	CreatedAt  time.Time  `json:"createdAt"`
	AnsweredAt *time.Time `json:"answeredAt"`
	ReviewedAt *time.Time `json:"reviewedAt"`
}

// Status: new → answered (ждёт разбора) → reviewed.
func (a ArenaEntry) Status() string {
	switch {
	case a.Feedback != nil:
		return "reviewed"
	case a.Answer != nil:
		return "answered"
	default:
		return "new"
	}
}

type Progress struct {
	Marks     map[string]map[string]int   `json:"marks"`     // день → навык → уровень
	Days      map[string]DayMeta          `json:"days"`      // день → привал и заметка
	WeekItems map[string]map[string]bool  `json:"weekItems"` // неделя → пункт (t0, l0) → сделан
	Reviews   map[string]Review           `json:"reviews"`   // неделя → хроника
	Chapters  map[int]bool                `json:"chapters"`  // прочитанные главы
	Guides    map[string]map[string]Guide `json:"guides"`    // день → навык → разбор наставника
	Arena     []ArenaEntry                `json:"arena"`
	Hero      string                      `json:"hero"`
}

func NewProgress() *Progress {
	return &Progress{
		Marks:     map[string]map[string]int{},
		Days:      map[string]DayMeta{},
		WeekItems: map[string]map[string]bool{},
		Reviews:   map[string]Review{},
		Chapters:  map[int]bool{},
		Guides:    map[string]map[string]Guide{},
		Arena:     []ArenaEntry{},
	}
}
