package world

import (
	"fmt"
	"strings"
)

// HeroField — одно поле анкеты героя: подпись, вид и правило проверки.
// Поля приходят из базы перечней (schemas/enums.db), поэтому форма героя
// меняется без пересборки.
type HeroField struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Hint     string   `json:"hint"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Limit    int      `json:"limit"`
	Options  []string `json:"options"`
	// Default — значение, которым поле заполнено при показе формы. У выбора
	// это один из вариантов; у текста — готовый ответ, если он уместен.
	Default string `json:"default"`
}

// Hero — форма героя из базы перечней. Состав полей описан таблицей
// hero_fields в schemas/enums.db, значения по умолчанию — в настройках
// модуля.
type Hero struct {
	Title  string      `json:"title"`
	Note   string      `json:"note"`
	Fields []HeroField `json:"fields"`
}

// ValidateHero проверяет ответы на форму героя: обязательные заполнены,
// выбор — из списка, текст — в пределе. Неизвестное поле — отказ: опечатка
// в имени поля не должна теряться молча.
func (h Hero) ValidateHero(answers map[string]string) *ValidationError {
	var fields []FieldError

	known := make(map[string]bool, len(h.Fields))
	for _, f := range h.Fields {
		known[f.ID] = true
		value := strings.TrimSpace(answers[f.ID])

		if value == "" {
			if f.Required {
				fields = append(fields, FieldError{Field: f.ID, Reason: "поле не заполнено"})
			}
			continue
		}

		switch f.Type {
		case "choice":
			if !contains(f.Options, value) {
				fields = append(fields, FieldError{Field: f.ID, Reason: fmt.Sprintf("значение вне списка: %q", value)})
			}
		default:
			if f.Limit > 0 && len([]rune(value)) > f.Limit {
				fields = append(fields, FieldError{
					Field:  f.ID,
					Reason: fmt.Sprintf("не больше %d символов, введено %d", f.Limit, len([]rune(value))),
				})
			}
		}
	}

	for id := range answers {
		if !known[id] {
			fields = append(fields, FieldError{Field: id, Reason: "неизвестное поле"})
		}
	}

	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}
