package world

import (
	"database/sql"
	"fmt"
	"os"
)

// heroFormTitle и heroFormNote — подпись и подсказка формы героя. Это текст
// экрана, а не перечень: он не меняется вместе с составом полей.
const (
	heroFormTitle = "Герой"
	heroFormNote  = "Кем герой был в этом мире и откуда пришёл. Остальное игра узнает по ходу: характер складывается из книг и реплик."
)

// LoadLists собирает перечни и форму героя из базы перечней: допустимые
// значения лежат таблицами (world_genres, hero_estates и другие), выбранное
// экземпляром — в настройках модуля. Расхождение останавливает запуск
// с понятным текстом: язык или умолчание вне перечня — ошибка, а не тишина.
func LoadLists(path string, langs []string, defaults map[string]string) (Lists, error) {
	if _, err := os.Stat(path); err != nil {
		return Lists{}, fmt.Errorf("перечни %s: %w", path, err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return Lists{}, fmt.Errorf("перечни %s: %w", path, err)
	}
	defer db.Close()

	genres, err := readEnum(db, "world_genres")
	if err != nil {
		return Lists{}, err
	}
	socialOrders, err := readEnum(db, "world_social_orders")
	if err != nil {
		return Lists{}, err
	}
	travelModes, err := readEnum(db, "world_travel_modes")
	if err != nil {
		return Lists{}, err
	}
	ratings, err := readEnum(db, "world_ratings")
	if err != nil {
		return Lists{}, err
	}
	allLangs, err := readEnum(db, "world_langs")
	if err != nil {
		return Lists{}, err
	}
	estates, err := readEnum(db, "hero_estates")
	if err != nil {
		return Lists{}, err
	}
	arrivals, err := readEnum(db, "hero_arrivals")
	if err != nil {
		return Lists{}, err
	}

	hero, err := readHeroFields(db, map[string][]string{
		"hero_estates":  estates,
		"hero_arrivals": arrivals,
	})
	if err != nil {
		return Lists{}, err
	}

	if len(langs) == 0 {
		return Lists{}, fmt.Errorf("не выбран ни один язык")
	}
	for _, lang := range langs {
		if !contains(allLangs, lang) {
			return Lists{}, fmt.Errorf("язык %q вне перечня", lang)
		}
	}

	for id, value := range defaults {
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
		Genres:       genres,
		SocialOrders: socialOrders,
		TravelModes:  travelModes,
		Ratings:      ratings,
		Langs:        langs,
		Hero:         hero,
	}, nil
}

// readEnum читает перечень по возрастанию ord. Имя таблицы задано кодом,
// а не данными. Пустой перечень — ошибка: экран не должен показывать пустой
// выбор.
func readEnum(db *sql.DB, table string) ([]string, error) {
	rows, err := db.Query(`SELECT value FROM ` + table + ` ORDER BY ord`)
	if err != nil {
		return nil, fmt.Errorf("перечень %s: %w", table, err)
	}
	defer rows.Close()

	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("перечень %s: %w", table, err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("перечень %s: %w", table, err)
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("перечень %s пуст", table)
	}
	return values, nil
}

// readHeroFields читает поля формы героя в порядке ord. Поле выбора ссылается
// на таблицу перечня в options_table; неизвестная таблица — ошибка, чтобы
// опечатка не оставила поле без вариантов.
func readHeroFields(db *sql.DB, options map[string][]string) (Hero, error) {
	rows, err := db.Query(`SELECT id, title, hint, required, max_length, COALESCE(options_table, '')
		FROM hero_fields ORDER BY ord`)
	if err != nil {
		return Hero{}, fmt.Errorf("поля героя: %w", err)
	}
	defer rows.Close()

	hero := Hero{Title: heroFormTitle, Note: heroFormNote}
	for rows.Next() {
		var field HeroField
		var required int
		var optionsTable string
		if err := rows.Scan(&field.ID, &field.Title, &field.Hint, &required, &field.Limit, &optionsTable); err != nil {
			return Hero{}, fmt.Errorf("поля героя: %w", err)
		}
		if field.Title == "" {
			return Hero{}, fmt.Errorf("поле героя %s: нет подписи", field.ID)
		}

		field.Required = required != 0
		if optionsTable == "" {
			field.Type = "text"
		} else {
			values, ok := options[optionsTable]
			if !ok {
				return Hero{}, fmt.Errorf("поле героя %s: неизвестный перечень %q", field.ID, optionsTable)
			}
			field.Type = "choice"
			field.Options = values
		}
		hero.Fields = append(hero.Fields, field)
	}
	if err := rows.Err(); err != nil {
		return Hero{}, fmt.Errorf("поля героя: %w", err)
	}
	if len(hero.Fields) == 0 {
		return Hero{}, fmt.Errorf("у героя нет полей")
	}
	return hero, nil
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
