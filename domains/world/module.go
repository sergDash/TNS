package world

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tns/engine/config"
	"tns/engine/core"
)

// Config — настройки модуля: где лежат перечни анкеты и каталог миров.
type Config struct {
	// Lists — файл перечней: жанры, строи, режимы походов, рейтинги, языки.
	Lists string `json:"lists"`
	// Dir — каталог миров: <dir>/<адрес мира>/world.db.
	Dir string `json:"dir"`
}

const (
	defaultLists = "config/world.json"
	defaultDir   = "world_db/worlds"
)

func (c Config) withDefaults() Config {
	if c.Lists == "" {
		c.Lists = defaultLists
	}
	if c.Dir == "" {
		c.Dir = defaultDir
	}
	return c
}

// module — модуль мира: отдаёт перечни анкеты и рождает мир.
type module struct {
	cfg   Config
	lists Lists
}

// New отдаёт модуль для точки сборки. settings — раздел
// modules.settings.world: файл перечней и каталог миров.
func New(settings json.RawMessage) (core.Module, error) {
	cfg := Config{}
	if err := config.DecodeSection(settings, &cfg); err != nil {
		return nil, fmt.Errorf("world: настройки: %w", err)
	}
	cfg = cfg.withDefaults()

	lists, err := LoadLists(cfg.Lists)
	if err != nil {
		return nil, fmt.Errorf("world: %w", err)
	}
	return module{cfg: cfg, lists: lists}, nil
}

func (module) Name() string { return "world" }

func (m module) Register(r core.Registry) error {
	r.HTTP("GET", "/api/world", m.anketa)
	r.HTTP("POST", "/api/world", m.create)
	r.HTTP("POST", "/api/world/{slug}/hero", m.createHero)
	return nil
}

// anketaResponse — перечни для формы. Имена полей совпадают со стейтом
// экрана; порядок списков задаёт канон, поэтому он сохраняется.
type anketaResponse struct {
	Genres       []string `json:"genres"`
	SocialOrders []string `json:"socialOrders"`
	TravelModes  []string `json:"travelModes"`
	Ratings      []string `json:"ratings"`
	Hero         Hero     `json:"hero"`
}

func (m module) anketa(*http.Request) (any, error) {
	return anketaResponse{
		Genres:       m.lists.Genres,
		SocialOrders: m.lists.SocialOrders,
		TravelModes:  m.lists.TravelModes,
		Ratings:      m.lists.Ratings,
		Hero:         m.lists.Hero,
	}, nil
}

// create проверяет анкету и рождает мир. Проверка отделена от записи: при
// отказе в базу не попадает ничего.
func (m module) create(r *http.Request) (any, error) {
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

	return map[string]any{"world": map[string]string{"slug": a.Slug, "title": a.Title}}, nil
}

// heroRequest — ответы на форму героя: поле → значение.
type heroRequest struct {
	Answers map[string]string `json:"answers"`
}

// createHero проверяет ответы и записывает героя в базу мира. Мир должен
// существовать, а герой — ещё нет: второй герой в одном мире не рождается.
func (m module) createHero(r *http.Request) (any, error) {
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
