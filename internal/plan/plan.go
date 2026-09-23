// Package plan описывает содержимое похода: недели, квесты, главы книги.
// План — это данные, а не код: его можно поменять в JSON и выкатить через git.
package plan

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"
)

//go:embed october.json
var defaultPlan []byte

const DateLayout = "2006-01-02"

type Skill struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Sub   string `json:"sub"`
	Glyph string `json:"glyph"`
}

type Task struct {
	Text string `json:"text"`
	Min  string `json:"min"`
	Tag  string `json:"tag,omitempty"`
}

type Week struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Boss     string   `json:"boss"`
	BossDesc string   `json:"bossDesc"`
	Eng      string   `json:"eng"`
	Body     string   `json:"body"`
	Mind     string   `json:"mind"`
	CKA      []*Task  `json:"cka"`
	Topics   []string `json:"topics"`
	Lab      []string `json:"lab"`
}

type Chapter struct {
	N     int    `json:"n"`
	Title string `json:"title"`
	Lab   string `json:"lab,omitempty"`
}

type Plan struct {
	Title        string         `json:"title"`
	GridStart    string         `json:"gridStart"`
	Start        string         `json:"start"`
	Days         int            `json:"days"`
	ReadChapters int            `json:"readChapters"`
	Skills       []Skill        `json:"skills"`
	Weeks        []Week         `json:"weeks"`
	Chapters     []Chapter      `json:"chapters"`
	ChapterDays  map[string]int `json:"chapterDays"`
	Eng          []Task         `json:"eng"`
	BodyTrain    Task           `json:"bodyTrain"`
	BodyWalk     Task           `json:"bodyWalk"`
	Mind         []Task         `json:"mind"`
	ArenaTopics  []string       `json:"arenaTopics"`

	gridStart time.Time
	start     time.Time
}

// Load читает план из файла, а если путь пустой — берёт встроенный october.json.
func Load(path string) (*Plan, error) {
	raw := defaultPlan
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read plan: %w", err)
		}
		raw = b
	}
	var p Plan
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parse plan: %w", err)
	}
	return &p, p.validate()
}

func (p *Plan) validate() error {
	var err error
	if p.gridStart, err = time.Parse(DateLayout, p.GridStart); err != nil {
		return fmt.Errorf("gridStart: %w", err)
	}
	if p.start, err = time.Parse(DateLayout, p.Start); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	if p.gridStart.Weekday() != time.Monday {
		return fmt.Errorf("gridStart %s must be a Monday", p.GridStart)
	}
	if p.Days <= 0 || p.Days%7 != 0 || len(p.Weeks) != p.Days/7 {
		return fmt.Errorf("days=%d must be a multiple of 7 and match %d weeks", p.Days, len(p.Weeks))
	}
	if len(p.Eng) != 5 || len(p.Mind) != 2 {
		return fmt.Errorf("eng needs 5 weekday tasks and mind needs 2")
	}
	for _, w := range p.Weeks {
		if len(w.CKA) != 5 {
			return fmt.Errorf("week %s: cka needs 5 weekday entries (null for days off)", w.ID)
		}
	}
	return nil
}

// Quest — задание одного навыка на конкретный день.
type Quest struct {
	Skill string `json:"skill"`
	Task
	Chapter int `json:"chapter,omitempty"`
}

// Day — клетка карты похода.
type Day struct {
	Date    string  `json:"date"`
	Row     int     `json:"row"`
	Dow     int     `json:"dow"` // 0 = понедельник
	Pre     bool    `json:"pre"` // до старта похода
	Weekend bool    `json:"weekend"`
	Chapter int     `json:"chapter,omitempty"`
	Quests  []Quest `json:"quests"`
}

// Calendar раскладывает план по дням.
func (p *Plan) Calendar() []Day {
	days := make([]Day, 0, p.Days)
	for i := 0; i < p.Days; i++ {
		d := p.gridStart.AddDate(0, 0, i)
		day := Day{
			Date:    d.Format(DateLayout),
			Row:     i / 7,
			Dow:     i % 7,
			Pre:     d.Before(p.start),
			Weekend: i%7 > 4,
		}
		day.Chapter = p.ChapterDays[key(day.Row, day.Dow)]
		if !day.Pre && !day.Weekend {
			day.Quests = p.questsFor(day)
		}
		days = append(days, day)
	}
	return days
}

// Day возвращает день по дате или false, если дата вне похода.
func (p *Plan) Day(date string) (Day, bool) {
	for _, d := range p.Calendar() {
		if d.Date == date {
			return d, true
		}
	}
	return Day{}, false
}

func (p *Plan) questsFor(d Day) []Quest {
	w := p.Weeks[d.Row]
	var qs []Quest
	if t := w.CKA[d.Dow]; t != nil {
		qs = append(qs, Quest{Skill: "cka", Task: *t})
	}
	qs = append(qs, p.bookQuest(d))
	qs = append(qs, Quest{Skill: "eng", Task: p.Eng[d.Dow]})
	body := p.BodyTrain
	if d.Dow%2 == 1 {
		body = p.BodyWalk
	}
	qs = append(qs, Quest{Skill: "body", Task: body})
	mind := p.Mind[0]
	if d.Dow == 4 {
		mind = p.Mind[1]
	}
	qs = append(qs, Quest{Skill: "mind", Task: mind})
	return qs
}

func (p *Plan) bookQuest(d Day) Quest {
	if n := d.Chapter; n > 0 {
		c := p.chapter(n)
		text := fmt.Sprintf("Глава %d: %s. Одна глава за подход — в этот день кластер можно сделать по минимуму.", n, c.Title)
		if c.Lab != "" {
			text += " " + c.Lab
		}
		return Quest{Skill: "book", Chapter: n, Task: Task{Text: text, Min: "Половина главы, вторая — завтра", Tag: "глава"}}
	}
	last := p.lastChapterBefore(d)
	c := p.chapter(last)
	return Quest{Skill: "book", Task: Task{
		Text: fmt.Sprintf("Повторение главы %d «%s»: 5 карточек в Anki или пересказ вслух за 5 минут.", last, c.Title),
		Min:  "Перечитать свои заметки",
		Tag:  "повтор",
	}}
}

func (p *Plan) lastChapterBefore(d Day) int {
	last, best := p.ReadChapters, -1
	idx := d.Row*7 + d.Dow
	for k, n := range p.ChapterDays {
		row, dow := parseKey(k)
		if at := row*7 + dow; at <= idx && at > best {
			best, last = at, n
		}
	}
	return last
}

func (p *Plan) chapter(n int) Chapter {
	for _, c := range p.Chapters {
		if c.N == n {
			return c
		}
	}
	return Chapter{N: n, Title: "?"}
}

// HasSkill и HasWeek нужны API для проверки входных данных.
func (p *Plan) HasSkill(id string) bool {
	for _, s := range p.Skills {
		if s.ID == id {
			return true
		}
	}
	return false
}

func (p *Plan) Week(id string) (Week, bool) {
	for _, w := range p.Weeks {
		if w.ID == id {
			return w, true
		}
	}
	return Week{}, false
}

func (p *Plan) HasArenaTopic(t string) bool {
	for _, x := range p.ArenaTopics {
		if x == t {
			return true
		}
	}
	return false
}

func key(row, dow int) string { return strconv.Itoa(row) + "-" + strconv.Itoa(dow) }

func parseKey(k string) (int, int) {
	var row, dow int
	fmt.Sscanf(k, "%d-%d", &row, &dow)
	return row, dow
}
