package models

import (
	"encoding/json"
	"fmt"
	"net/http"

	"tns/engine/config"
	"tns/engine/core"
)

// module — модуль моделей: отвечает, что уже стоит и что нужно скачать.
// Каталог читается при сборке модуля: файл правится без пересборки, но
// ошибка в нём видна сразу при запуске, а не при первом запросе.
type module struct {
	cfg     Config
	catalog Catalog
}

// New отдаёт модуль для точки сборки. settings — раздел
// modules.settings.models: файл каталога, каталог ролей и каталог сервера.
func New(settings json.RawMessage) (core.Module, error) {
	cfg := Config{}
	if err := config.DecodeSection(settings, &cfg); err != nil {
		return nil, fmt.Errorf("models: настройки: %w", err)
	}
	cfg = cfg.withDefaults()

	catalog, err := LoadCatalog(cfg.Catalog)
	if err != nil {
		return nil, fmt.Errorf("models: %w", err)
	}
	return module{cfg: cfg, catalog: catalog}, nil
}

func (module) Name() string { return "models" }

// Register объявляет маршрут состояния. Запрос ничего не меняет: экран
// подготовки к скачиванию можно перечитывать сколько угодно раз.
func (m module) Register(r core.Registry) error {
	r.HTTP("GET", "/api/models", m.state)
	return nil
}

func (m module) state(*http.Request) (any, error) {
	return snapshotOf(m.cfg, m.catalog), nil
}
