package world

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"tns/engine/config"
	"tns/engine/core"
)

// Config — настройки модуля: где лежат схема мира, выбранные значения,
// каталог миров и файл выбора открытого мира.
type Config struct {
	// Schema — схема канона и формы героя.
	Schema string `json:"schema"`
	// Config — файл выбранного: включённые языки и умолчания героя.
	Config string `json:"config"`
	// Dir — каталог миров: <dir>/<адрес мира>/world.db.
	Dir string `json:"dir"`
	// Active — файл выбора: какой мир открыт в интерфейсе.
	Active string `json:"active"`
}

const (
	defaultSchema = "schemas/world.schema.json"
	defaultConfig = "config/world.json"
	defaultDir    = "worlds"
	defaultActive = "config/active_world.json"
)

func (c Config) withDefaults() Config {
	if c.Schema == "" {
		c.Schema = defaultSchema
	}
	if c.Config == "" {
		c.Config = defaultConfig
	}
	if c.Dir == "" {
		c.Dir = defaultDir
	}
	if c.Active == "" {
		c.Active = defaultActive
	}
	return c
}

// module — модуль мира: отдаёт перечни анкеты, рождает мир и помнит, какой
// мир открыт. Открытый мир хранится и в памяти, и в файле выбора: память
// отвечает за текущий запрос, файл — за возврат после перезапуска.
type module struct {
	cfg   Config
	lists Lists

	mu     sync.RWMutex
	active string
}

// New отдаёт модуль для точки сборки. settings — раздел
// modules.settings.world: схема, файл выбранного, каталог миров и файл
// выбора открытого мира.
func New(settings json.RawMessage) (core.Module, error) {
	cfg := Config{}
	if err := config.DecodeSection(settings, &cfg); err != nil {
		return nil, fmt.Errorf("world: настройки: %w", err)
	}
	cfg = cfg.withDefaults()

	lists, err := LoadWorld(cfg.Schema, cfg.Config)
	if err != nil {
		return nil, fmt.Errorf("world: %w", err)
	}

	m := &module{cfg: cfg, lists: lists}
	if err := m.restoreActive(); err != nil {
		return nil, fmt.Errorf("world: выбор мира: %w", err)
	}
	return m, nil
}

// restoreActive сверяет сохранённый выбор с мирами на диске. Удалённый мир
// даёт пустой выбор, а запись при этом убирается: указатель не должен вести
// в никуда. Если выбора нет, а мир ровно один — он открывается сам: свежий
// экземпляр и мир с одним миром начинают с него, а не с пустой формы.
func (m *module) restoreActive() error {
	slugs, err := worldSlugs(m.cfg.Dir)
	if err != nil {
		return err
	}

	active := readActive(m.cfg.Active)
	exists := false
	for _, slug := range slugs {
		if slug == active {
			exists = true
			break
		}
	}

	if !exists {
		active = ""
		if len(slugs) == 1 {
			active = slugs[0]
		}
		if err := saveActive(m.cfg.Active, active); err != nil {
			return err
		}
	}

	m.active = active
	return nil
}

// setActive запоминает открытый мир: в памяти сразу, в файле — как только
// он записан. Ошибка записи не отменяет выбор в памяти: мир уже рождён и
// открыт в этой сессии, а файл нужен только для следующего запуска.
func (m *module) setActive(slug string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.active = slug
	return saveActive(m.cfg.Active, slug)
}

// activeSlug отдаёт адрес открытого мира.
func (m *module) activeSlug() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active
}

func (*module) Name() string { return "world" }

func (m *module) Register(r core.Registry) error {
	r.HTTP("GET", "/api/world", m.anketa)
	r.HTTP("POST", "/api/world", m.create)
	r.HTTP("POST", "/api/world/{slug}/active", m.activate)
	r.HTTP("POST", "/api/world/{slug}/hero", m.createHero)
	return nil
}

// anketaResponse — перечни для формы, форма героя и миры на диске. Имена
// полей совпадают со стейтом экрана; порядок списков задаёт канон, поэтому
// он сохраняется. Миры идут по времени рождения: книги стоят в том порядке,
// в каком появились.
type anketaResponse struct {
	Genres       []string    `json:"genres"`
	SocialOrders []string    `json:"socialOrders"`
	TravelModes  []string    `json:"travelModes"`
	Ratings      []string    `json:"ratings"`
	Hero         Hero        `json:"hero"`
	Worlds       []WorldCard `json:"worlds"`
}

// listsResponse собирает перечни и мир-карточки: у каждого мира читаются его
// настройки и признак героя, открытый мир помечается. Каталог миров
// перечитывается на каждый запрос — миры рождаются редко, а список должен
// показывать то, что лежит на диске.
func (m *module) listsResponse() (anketaResponse, error) {
	resp := anketaResponse{
		Genres:       m.lists.Genres,
		SocialOrders: m.lists.SocialOrders,
		TravelModes:  m.lists.TravelModes,
		Ratings:      m.lists.Ratings,
		Hero:         m.lists.Hero,
	}

	slugs, err := worldSlugs(m.cfg.Dir)
	if err != nil {
		return resp, fmt.Errorf("каталог миров: %w", err)
	}

	active := m.activeSlug()
	for _, slug := range slugs {
		card, ok, err := readWorld(filepath.Join(m.cfg.Dir, slug, "world.db"))
		if err != nil {
			return resp, fmt.Errorf("мир %s: %w", slug, err)
		}
		if !ok {
			continue
		}
		card.Active = slug == active
		resp.Worlds = append(resp.Worlds, card)
	}

	sort.Slice(resp.Worlds, func(i, j int) bool {
		if resp.Worlds[i].CreatedAt != resp.Worlds[j].CreatedAt {
			return resp.Worlds[i].CreatedAt < resp.Worlds[j].CreatedAt
		}
		return resp.Worlds[i].Slug < resp.Worlds[j].Slug
	})
	return resp, nil
}

func (m *module) anketa(*http.Request) (any, error) {
	return m.listsResponse()
}

// activate открывает выбранный мир: адрес становится открытым, а экран
// получает свежие перечни и карточки. Мир, которого нет на диске, — 404:
// открывать нечего.
func (m *module) activate(r *http.Request) (any, error) {
	slug := r.PathValue("slug")
	if !slugRe.MatchString(slug) {
		return nil, &core.Error{Status: http.StatusBadRequest, Message: "неверный адрес мира"}
	}

	dbPath := filepath.Join(m.cfg.Dir, slug, "world.db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, &core.Error{Status: http.StatusNotFound, Message: "мир не найден"}
	}

	if err := m.setActive(slug); err != nil {
		return nil, fmt.Errorf("открыть мир: %w", err)
	}
	return m.listsResponse()
}

// create проверяет анкету и рождает мир. Проверка отделена от записи: при
// отказе в базу не попадает ничего.
func (m *module) create(r *http.Request) (any, error) {
	var a Anketa
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		return nil, &core.Error{Status: http.StatusBadRequest, Message: "не разобрать анкету", Err: err}
	}

	a.Slug = strings.TrimSpace(a.Slug)
	a.Title = strings.TrimSpace(a.Title)
	a.Synopsis = strings.TrimSpace(a.Synopsis)
	if a.Lang == "" {
		// Язык — параметр языкового пакета, а не вопрос анкеты: первый
		// пакет русский, поэтому пустое поле означает ru.
		a.Lang = "ru"
	}

	if ve := m.lists.Validate(a); ve != nil {
		return nil, &core.Error{
			Status:  http.StatusBadRequest,
			Message: "анкета не принята: проверьте поля",
			Data:    map[string]any{"code": "bad_request", "fields": ve.Fields},
			Err:     ve,
		}
	}

	dbPath := filepath.Join(m.cfg.Dir, a.Slug, "world.db")
	if _, err := os.Stat(filepath.Dir(dbPath)); err == nil {
		return nil, &core.Error{
			Status:  http.StatusConflict,
			Message: "мир с таким адресом уже есть",
			Data: map[string]any{"code": "conflict", "fields": []FieldError{
				{Field: "slug", Reason: "адрес занят другим миром"},
			}},
			Err: errWorldExists,
		}
	}

	if err := createWorld(dbPath, a, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("рождение мира: %w", err)
	}

	// Новорождённый мир сразу открыт: второй раз игрок его не выбирает.
	// Ошибка записи выбора не отменяет рождение — мир уже на диске, и выбор
	// повторится при следующем создании или выборе.
	_ = m.setActive(a.Slug)

	return map[string]any{"world": map[string]string{"slug": a.Slug, "title": a.Title}}, nil
}

// heroRequest — ответы на форму героя: поле → значение.
type heroRequest struct {
	Answers map[string]string `json:"answers"`
}

// createHero проверяет ответы и записывает героя в базу мира. Мир должен
// существовать, а герой — ещё нет: второй герой в одном мире не рождается.
func (m *module) createHero(r *http.Request) (any, error) {
	slug := r.PathValue("slug")
	if !slugRe.MatchString(slug) {
		return nil, &core.Error{Status: http.StatusBadRequest, Message: "неверный адрес мира"}
	}

	var req heroRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, &core.Error{Status: http.StatusBadRequest, Message: "не разобрать ответы", Err: err}
	}

	dbPath := filepath.Join(m.cfg.Dir, slug, "world.db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, &core.Error{Status: http.StatusNotFound, Message: "мир не найден"}
	}

	if _, ok, err := readHero(dbPath); err != nil {
		return nil, fmt.Errorf("герой: %w", err)
	} else if ok {
		return nil, &core.Error{Status: http.StatusConflict, Message: "у этого мира уже есть герой"}
	}

	if ve := m.lists.Hero.ValidateHero(req.Answers); ve != nil {
		return nil, &core.Error{
			Status:  http.StatusBadRequest,
			Message: "ответы не приняты: проверьте поля",
			Data:    map[string]any{"code": "bad_request", "fields": ve.Fields},
			Err:     ve,
		}
	}

	if err := createHero(dbPath, req.Answers, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("рождение героя: %w", err)
	}
	return map[string]any{"hero": req.Answers}, nil
}

// errWorldExists нужен, чтобы отказ «адрес занят» отличался от прочих ошибок
// записи при разборе в проверках.
var errWorldExists = errors.New("мир с таким адресом уже есть")
