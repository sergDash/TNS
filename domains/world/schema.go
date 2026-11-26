package world

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Canon — канонические перечни мира из схемы: то, из чего игрок выбирает
// и что игра признаёт допустимым.
type Canon struct {
	Genres       []string
	SocialOrders []string
	TravelModes  []string
	EconomyMods  []string
	Ratings      []string
	Langs        []string
}

// schemaFile — то, что загрузчику нужно из схемы мира: канонические перечни
// и форма ответов героя. Полная JSON Schema не проверяется: валидатора в
// проекте нет, а канон нужен как данные.
type schemaFile struct {
	Defs map[string]json.RawMessage `json:"$defs"`
}

// enumDef — перечень допустимых значений: в схеме это "enum".
type enumDef struct {
	Enum []string `json:"enum"`
}

// objectDef — определение вида объекта: свойства и обязательные имена.
type objectDef struct {
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Properties  json.RawMessage `json:"properties"`
	Required    []string        `json:"required"`
}

// propertyDef — одно свойство ответов героя. Вид поля выводится из схемы:
// есть перечень значений или ссылка на него — выбор, иначе текст.
type propertyDef struct {
	Ref         string   `json:"$ref"`
	Type        string   `json:"type"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	MinLength   int      `json:"minLength"`
	MaxLength   int      `json:"maxLength"`
	Enum        []string `json:"enum"`
}

// property — свойство вместе с его именем: порядок свойств в схеме задаёт
// порядок полей на экране, поэтому он сохраняется.
type property struct {
	ID  string
	Def propertyDef
}

// configFile — то, что берётся из config/world.json: включённые языки и
// значения, которыми заполнены поля героя.
type configFile struct {
	Langs []string `json:"langs"`
	Hero  struct {
		Defaults map[string]string `json:"defaults"`
	} `json:"hero"`
}

// LoadWorld собирает перечни и форму героя: канон берётся из схемы,
// выбранное — из конфига. Расхождение останавливает запуск с понятным
// текстом: значение конфига, которого нет в каноне, — ошибка, а не тишина.
func LoadWorld(schemaPath, configPath string) (Lists, error) {
	canon, hero, err := loadSchema(schemaPath)
	if err != nil {
		return Lists{}, fmt.Errorf("схема мира %s: %w", schemaPath, err)
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		return Lists{}, fmt.Errorf("настройки мира %s: %w", configPath, err)
	}

	for _, lang := range cfg.Langs {
		if !contains(canon.Langs, lang) {
			return Lists{}, fmt.Errorf("язык %q вне канона схемы", lang)
		}
	}

	for id, value := range cfg.Hero.Defaults {
		field := heroFieldByID(hero, id)
		if field == nil {
			return Lists{}, fmt.Errorf("умолчание для неизвестного поля героя %q", id)
		}
		if field.Type == "choice" && !contains(field.Options, value) {
			return Lists{}, fmt.Errorf("поле героя %s: умолчание %q вне списка", id, value)
		}
		if field.Limit > 0 && len([]rune(value)) > field.Limit {
			return Lists{}, fmt.Errorf("поле героя %s: умолчание длиннее предела", id)
		}
		field.Default = value
	}

	return Lists{
		Genres:       canon.Genres,
		SocialOrders: canon.SocialOrders,
		TravelModes:  canon.TravelModes,
		EconomyMods:  canon.EconomyMods,
		Ratings:      canon.Ratings,
		Langs:        cfg.Langs,
		Hero:         hero,
	}, nil
}

// loadSchema читает канон и форму героя из схемы.
func loadSchema(path string) (Canon, Hero, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Canon{}, Hero{}, err
	}

	var file schemaFile
	if err := json.Unmarshal(data, &file); err != nil {
		return Canon{}, Hero{}, err
	}

	var canon Canon
	lists := []struct {
		name string
		into *[]string
	}{
		{"genre", &canon.Genres},
		{"social_order", &canon.SocialOrders},
		{"travel_mode", &canon.TravelModes},
		{"economy_mod", &canon.EconomyMods},
		{"rating", &canon.Ratings},
		{"lang", &canon.Langs},
	}
	for _, item := range lists {
		values, err := file.enum(item.name)
		if err != nil {
			return Canon{}, Hero{}, err
		}
		*item.into = values
	}

	hero, err := file.heroForm()
	if err != nil {
		return Canon{}, Hero{}, err
	}
	return canon, hero, nil
}

// enum достаёт перечень допустимых значений из определений схемы.
func (f schemaFile) enum(name string) ([]string, error) {
	raw, ok := f.Defs[name]
	if !ok {
		return nil, fmt.Errorf("нет перечня %s", name)
	}

	var def enumDef
	if err := json.Unmarshal(raw, &def); err != nil {
		return nil, fmt.Errorf("перечень %s: %w", name, err)
	}
	if len(def.Enum) == 0 {
		return nil, fmt.Errorf("перечень %s пуст", name)
	}
	return def.Enum, nil
}

// heroForm собирает форму героя из определения heroAnswers: имя, подпись,
// подсказка, вид, обязательность и предел берутся из схемы.
func (f schemaFile) heroForm() (Hero, error) {
	raw, ok := f.Defs["heroAnswers"]
	if !ok {
		return Hero{}, fmt.Errorf("нет определения heroAnswers")
	}

	var def objectDef
	if err := json.Unmarshal(raw, &def); err != nil {
		return Hero{}, fmt.Errorf("heroAnswers: %w", err)
	}

	props, err := orderedProperties(def.Properties)
	if err != nil {
		return Hero{}, err
	}
	if len(props) == 0 {
		return Hero{}, fmt.Errorf("у героя нет полей")
	}

	hero := Hero{Title: def.Title, Note: def.Description}
	for _, prop := range props {
		field := HeroField{
			ID:       prop.ID,
			Title:    prop.Def.Title,
			Hint:     prop.Def.Description,
			Limit:    prop.Def.MaxLength,
			Required: contains(def.Required, prop.ID),
		}
		if field.Title == "" {
			return Hero{}, fmt.Errorf("поле героя %s: нет подписи", prop.ID)
		}

		options := prop.Def.Enum
		if prop.Def.Ref != "" {
			name := strings.TrimPrefix(prop.Def.Ref, "#/$defs/")
			options, err = f.enum(name)
			if err != nil {
				return Hero{}, fmt.Errorf("поле героя %s: %w", prop.ID, err)
			}
		}
		if len(options) > 0 {
			field.Type = "choice"
			field.Options = options
		} else {
			field.Type = "text"
		}
		hero.Fields = append(hero.Fields, field)
	}
	return hero, nil
}

// orderedProperties читает свойства в порядке документа: map сохранил бы
// порядок случайным, а он задаёт порядок полей на экране.
func orderedProperties(raw json.RawMessage) ([]property, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("свойства героя: ожидался объект")
	}

	var out []property
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("свойства героя: имя поля не строка")
		}

		var def propertyDef
		if err := dec.Decode(&def); err != nil {
			return nil, fmt.Errorf("поле %s: %w", key, err)
		}
		out = append(out, property{ID: key, Def: def})
	}
	return out, nil
}

// loadConfig читает выбранные значения из настроек мира.
func loadConfig(path string) (configFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return configFile{}, err
	}
	defer file.Close()

	dec := json.NewDecoder(file)
	dec.DisallowUnknownFields()

	var cfg configFile
	if err := dec.Decode(&cfg); err != nil {
		return configFile{}, err
	}
	if len(cfg.Langs) == 0 {
		return configFile{}, fmt.Errorf("не выбран ни один язык")
	}
	return cfg, nil
}

// heroFieldByID ищет поле формы героя по имени.
func heroFieldByID(hero Hero, id string) *HeroField {
	for i := range hero.Fields {
		if hero.Fields[i].ID == id {
			return &hero.Fields[i]
		}
	}
	return nil
}
