// Package models — модуль моделей: каталог ролей, состояние установки и
// экран подготовки к скачиванию. Каталог лежит данными в config/models.json:
// состав моделей меняется без пересборки, а код отвечает только за форму
// данных и за то, что видно на диске.
//
// Здесь пока только чтение: модуль рассказывает, что нужно скачать и сколько
// это займёт места и памяти; сама загрузка появится отдельной работой.
package models

import (
	"encoding/json"
	"fmt"
	"os"
)

// Role — место модели в работе мира. Ролей три, и каждая отвечает на свой
// вопрос хода: чем писать прозу, чем считать смысл реплики, чем разбирать
// первую реплику главы.
type Role string

const (
	// RoleWeaver — живая проза: сцена, когда Селектор выбрал пик.
	RoleWeaver Role = "weaver"
	// RoleEmbed — вектор смысла: считается на каждой реплике, поэтому живёт
	// в памяти постоянно и вариантов у него больше — под объём памяти машины.
	RoleEmbed Role = "embed"
	// RoleAnalysis — глубокий разбор: один вызов на главу.
	RoleAnalysis Role = "analysis"
)

// Variant — файл модели, которым можно заполнить роль. Bytes — размер файла
// в байтах; Approx отмечает оценку, которую загрузчик уточнит по ответу
// сервера. SHA256 — контрольная сумма файла: пустая строка значит «сверка
// появится вместе с загрузчиком».
type Variant struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	File   string `json:"file"`
	Bytes  int64  `json:"bytes"`
	Approx bool   `json:"approx"`
	RAMGB  int    `json:"ramGb"`
	Source string `json:"source"`
	SHA256 string `json:"sha256"`
}

// Slot — роль и её варианты в порядке предпочтения: первый вариант —
// рекомендуемый для этой роли.
type Slot struct {
	Role     Role      `json:"role"`
	Title    string    `json:"title"`
	Note     string    `json:"note"`
	Variants []Variant `json:"variants"`
}

// Engine — станок: llama-server из закреплённого релиза llama.cpp. Бинарник
// не входит в поставку: он скачивается один раз и живёт в каталоге bin.
type Engine struct {
	Title   string `json:"title"`
	File    string `json:"file"`
	Version string `json:"version"`
	Source  string `json:"source"`
	SHA256  string `json:"sha256"`
}

// Catalog — каталог поставки: станок и роли. Порядок ролей в файле задаёт
// порядок карточек на экране, поэтому он значим.
type Catalog struct {
	Engine Engine `json:"engine"`
	Roles  []Slot `json:"roles"`
}

// LoadCatalog читает каталог из файла. Неизвестные ключи запрещены, как и в
// настройках: опечатка в поле не должна тихо давать вариант без размера.
func LoadCatalog(path string) (Catalog, error) {
	f, err := os.Open(path)
	if err != nil {
		return Catalog{}, fmt.Errorf("каталог моделей: %w", err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()

	var cat Catalog
	if err := dec.Decode(&cat); err != nil {
		return Catalog{}, fmt.Errorf("каталог моделей %s: %w", path, err)
	}
	if err := cat.validate(); err != nil {
		return Catalog{}, fmt.Errorf("каталог моделей %s: %w", path, err)
	}
	return cat, nil
}

// validate проверяет каталог целиком: экран считает по нему итог, поэтому
// неполная запись должна останавливать запуск, а не показывать ноль.
func (c Catalog) validate() error {
	if c.Engine.File == "" {
		return fmt.Errorf("станок: не указан файл")
	}
	if len(c.Roles) == 0 {
		return fmt.Errorf("нет ни одной роли")
	}

	roles := make(map[Role]bool, len(c.Roles))
	for _, slot := range c.Roles {
		if slot.Role == "" {
			return fmt.Errorf("роль без имени")
		}
		if roles[slot.Role] {
			return fmt.Errorf("роль %s объявлена дважды", slot.Role)
		}
		roles[slot.Role] = true

		if slot.Title == "" {
			return fmt.Errorf("роль %s: нет названия", slot.Role)
		}
		if len(slot.Variants) == 0 {
			return fmt.Errorf("роль %s: нет вариантов", slot.Role)
		}

		ids := make(map[string]bool, len(slot.Variants))
		files := make(map[string]bool, len(slot.Variants))
		for _, v := range slot.Variants {
			if v.ID == "" || v.Title == "" || v.File == "" {
				return fmt.Errorf("роль %s: неполный вариант %q", slot.Role, v.ID)
			}
			if ids[v.ID] {
				return fmt.Errorf("роль %s: вариант %s объявлен дважды", slot.Role, v.ID)
			}
			ids[v.ID] = true
			if files[v.File] {
				return fmt.Errorf("роль %s: файл %s объявлен дважды", slot.Role, v.File)
			}
			files[v.File] = true

			if v.Bytes <= 0 {
				return fmt.Errorf("роль %s, вариант %s: размер не задан", slot.Role, v.ID)
			}
			if v.RAMGB <= 0 {
				return fmt.Errorf("роль %s, вариант %s: память не задана", slot.Role, v.ID)
			}
			if v.Source == "" {
				return fmt.Errorf("роль %s, вариант %s: нет источника", slot.Role, v.ID)
			}
		}
	}
	return nil
}
