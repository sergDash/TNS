// Package world — модуль мира: перечни анкеты, проверка ответов и рождение
// мира в его собственной базе. Анкета задаёт мир, а не книгу: имя, адрес,
// о чём история, жанры, строй и рейтинг.
//
// Перечни лежат данными в config/world.json: канонические списки жанров и
// строёв правятся без пересборки, а код отвечает за форму и проверку.
package world

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Lists — перечни анкеты: из чего игрок выбирает.
type Lists struct {
	Genres       []string `json:"genres"`
	SocialOrders []string `json:"social_orders"`
	TravelModes  []string `json:"travel_modes"`
	EconomyMods  []string `json:"economy_mods"`
	Ratings      []string `json:"ratings"`
	Langs        []string `json:"langs"`
	// Hero — форма героя. Пустой список полей значит, что отдельного шага
	// героя нет: вопрос о нём ещё открыт.
	Hero Hero `json:"hero"`
}

// LoadLists читает перечни из файла. Неизвестные ключи запрещены: опечатка
// в имени списка не должна тихо давать пустой выбор на экране.
func LoadLists(path string) (Lists, error) {
	f, err := os.Open(path)
	if err != nil {
		return Lists{}, fmt.Errorf("перечни мира: %w", err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()

	var lists Lists
	if err := dec.Decode(&lists); err != nil {
		return Lists{}, fmt.Errorf("перечни мира %s: %w", path, err)
	}
	if len(lists.Genres) == 0 || len(lists.SocialOrders) == 0 || len(lists.Ratings) == 0 {
		return Lists{}, fmt.Errorf("перечни мира %s: пусты жанры, строи или рейтинги", path)
	}
	if err := lists.Hero.validateForm(); err != nil {
		return Lists{}, fmt.Errorf("перечни мира %s: %w", path, err)
	}
	return lists, nil
}

// Anketa — ответы игрока о мире. Полей ровно столько, сколько спрашивает
// экран; остальные поля полной анкеты появятся вместе со своими вопросами.
type Anketa struct {
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Synopsis    string   `json:"synopsis"`
	Lang        string   `json:"lang"`
	TravelMode  string   `json:"travel_mode"`
	Genres      []string `json:"genres"`
	SocialOrder string   `json:"social_order"`
	Censorship  string   `json:"censorship"`
}

// FieldError — поле и причина отказа: по ним экран подсвечивает ввод.
type FieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// ValidationError — отказ анкеты со списком полей. Данные ошибки уходят
// клиенту в поле data рядом с текстом, чтобы форма знала, что править.
type ValidationError struct {
	Fields []FieldError `json:"fields"`
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		parts = append(parts, f.Field+": "+f.Reason)
	}
	return "анкета: " + strings.Join(parts, "; ")
}

var slugRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// Validate проверяет анкету и ничего не пишет. Причины отказов собираются
// все сразу: игрок должен увидеть весь список, а не исправлять по одной.
func (l Lists) Validate(a Anketa) *ValidationError {
	var fields []FieldError

	if strings.TrimSpace(a.Title) == "" {
		fields = append(fields, FieldError{Field: "title", Reason: "название мира пустое"})
	}

	if a.Slug == "" {
		fields = append(fields, FieldError{Field: "slug", Reason: "адрес мира пустой"})
	} else if !slugRe.MatchString(a.Slug) {
		fields = append(fields, FieldError{Field: "slug", Reason: "допустимы только латиница, цифры и дефис"})
	}

	if n := countSentences(a.Synopsis); n == 0 {
		fields = append(fields, FieldError{Field: "synopsis", Reason: "о чём история — не заполнено"})
	} else if n > 3 {
		fields = append(fields, FieldError{Field: "synopsis", Reason: fmt.Sprintf("нужно 1–3 предложения, получено %d", n)})
	}

	if !contains(l.Langs, a.Lang) {
		fields = append(fields, FieldError{Field: "lang", Reason: fmt.Sprintf("язык вне списка: %q", a.Lang)})
	}
	if !contains(l.TravelModes, a.TravelMode) {
		fields = append(fields, FieldError{Field: "travel_mode", Reason: fmt.Sprintf("режим походов вне списка: %q", a.TravelMode)})
	}
	if a.SocialOrder == "" {
		fields = append(fields, FieldError{Field: "social_order", Reason: "строй не выбран"})
	} else if !contains(l.SocialOrders, a.SocialOrder) {
		fields = append(fields, FieldError{Field: "social_order", Reason: fmt.Sprintf("строй вне списка: %q", a.SocialOrder)})
	}
	if a.Censorship == "" {
		fields = append(fields, FieldError{Field: "censorship", Reason: "рейтинг не выбран"})
	} else if !contains(l.Ratings, a.Censorship) {
		fields = append(fields, FieldError{Field: "censorship", Reason: fmt.Sprintf("рейтинг вне списка: %q", a.Censorship)})
	}

	for _, g := range a.Genres {
		if !contains(l.Genres, g) {
			fields = append(fields, FieldError{Field: "genres", Reason: fmt.Sprintf("жанр вне списка: %q", g)})
		}
	}

	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// countSentences считает предложения по завершающим знакам «.!?…», за
// которыми идёт конец строки, пробел или закрывающая кавычка. Текст без
// завершающего знака считается одним предложением — так же, как в анкете
// старой версии, чтобы правило не разъехалось между сборками.
func countSentences(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}

	runes := []rune(s)
	count := 0
	i := 0
	for i < len(runes) {
		c := runes[i]
		if c == '.' || c == '!' || c == '?' || c == '…' {
			j := i
			for j < len(runes) && (runes[j] == '.' || runes[j] == '…') {
				j++
			}
			for j < len(runes) && (runes[j] == '"' || runes[j] == '\'' || runes[j] == '»' || runes[j] == ')' || runes[j] == ']') {
				j++
			}
			if j == len(runes) || runes[j] == ' ' || runes[j] == '\t' || runes[j] == '\n' || runes[j] == '\r' {
				count++
			}
			i = j
			continue
		}
		i++
	}
	if count == 0 {
		return 1
	}
	return count
}
