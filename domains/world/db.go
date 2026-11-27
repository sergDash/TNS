package world

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// schemaVersion — версия схемы мира. Миграции идут по порядку: номер в
// PRAGMA user_version говорит, какие уже применены.
const schemaVersion = 3

// openWorld открывает базу мира, создавая каталог и файл при первом
// рождении. Соединение одно: база мира пишется одним писателем.
func openWorld(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// migrate приводит схему к версии кода. Файл новее кода — отказ: старая
// сборка не должна писать в базу, которую не понимает.
func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("база мира новее кода: схема %d, код знает %d", version, schemaVersion)
	}

	for v := version; v < schemaVersion; v++ {
		if err := migrations[v](db); err != nil {
			return fmt.Errorf("миграция %d: %w", v+1, err)
		}
		// Версия пишется после шага: прерванная миграция повторится целиком.
		if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
			return err
		}
	}
	return nil
}

// migrations — шаги схемы по номеру. Новый шаг добавляется в конец, старые
// не правятся: базы уже прошли их.
var migrations = []func(*sql.DB) error{
	migrateWorlds,
	migrateHero,
	migrateHeroState,
}

// migrateWorlds заводит таблицу миров: одна строка — один мир.
func migrateWorlds(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS worlds (
		slug         TEXT PRIMARY KEY,
		title        TEXT NOT NULL,
		synopsis     TEXT NOT NULL,
		lang         TEXT NOT NULL,
		travel_mode  TEXT NOT NULL,
		social_order TEXT NOT NULL,
		censorship   TEXT NOT NULL,
		genres       TEXT NOT NULL,
		created_at   TEXT NOT NULL
	)`)
	return err
}

// migrateHero заводит таблицу героя: одна строка на мир.
func migrateHero(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS hero (
		answers    TEXT NOT NULL,
		created_at TEXT NOT NULL
	)`)
	return err
}

// migrateHeroState добавляет герою слой состояния. Уже рождённые герои
// получают состояние покоя: они появились до того, как статы стали данными,
// и нулевое здоровье читалось бы как мёртвый герой.
func migrateHeroState(db *sql.DB) error {
	_, err := db.Exec(`ALTER TABLE hero ADD COLUMN state TEXT NOT NULL DEFAULT '` + restStateJSON + `'`)
	return err
}

// HeroState — телесное состояние героя: восемь статов 0–100 и настроение.
// Набор и стартовые значения взяты из старой версии (B-07.1, T-19-14):
// здоровье, комфорт и дух начинаются со 100, остальное — с нуля. Это данные
// героя, а не настройка: по ним решается, что с ним происходит в сцене.
type HeroState struct {
	Health  int    `json:"health"`
	Hunger  int    `json:"hunger"`
	Thirst  int    `json:"thirst"`
	Fatigue int    `json:"fatigue"`
	Comfort int    `json:"comfort"`
	Spirit  int    `json:"spirit"`
	Bladder int    `json:"bladder"`
	Bowel   int    `json:"bowel"`
	Mood    string `json:"mood"`
}

// restStateJSON — состояние покоя в том виде, в каком его получают уже
// рождённые герои при миграции. Пишется в DDL строкой, потому что миграция —
// исторический шаг; значение совпадает с restState(), и проверка
// TestRestStateMatchesMigration следит, чтобы они не разошлись.
const restStateJSON = `{"health":100,"hunger":0,"thirst":0,"fatigue":0,"comfort":100,"spirit":100,"bladder":0,"bowel":0,"mood":"спокойное"}`

// restState отдаёт состояние покоя: с него начинается жизнь героя.
func restState() HeroState {
	return HeroState{Health: 100, Comfort: 100, Spirit: 100, Mood: "спокойное"}
}

// createHero записывает героя мира. Ответы хранятся как JSON: набор полей
// задаётся конфигом, и схема за ним не должна меняться. Состояние пишется
// сразу состоянием покоя: герой рождается здоровым.
func createHero(path string, answers map[string]string, now time.Time) error {
	data, err := json.Marshal(answers)
	if err != nil {
		return err
	}
	state, err := json.Marshal(restState())
	if err != nil {
		return err
	}

	db, err := openWorld(path)
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec(`INSERT INTO hero (answers, state, created_at) VALUES (?, ?, ?)`,
		string(data), string(state), now.Format(time.RFC3339))
	return err
}

// readHero читает героя мира. Нет строки — герой ещё не рождён.
func readHero(path string) (map[string]string, bool, error) {
	db, err := openWorld(path)
	if err != nil {
		return nil, false, err
	}
	defer db.Close()

	var data string
	err = db.QueryRow(`SELECT answers FROM hero LIMIT 1`).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	var answers map[string]string
	if err := json.Unmarshal([]byte(data), &answers); err != nil {
		return nil, false, err
	}
	return answers, true, nil
}

// createWorld записывает мир в его базу. Каталог мира — это его дом: база,
// а позже книга и литературная подготовка живут рядом.
func createWorld(path string, a Anketa, now time.Time) error {
	genres, err := json.Marshal(a.Genres)
	if err != nil {
		return err
	}

	db, err := openWorld(path)
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec(`INSERT INTO worlds
		(slug, title, synopsis, lang, travel_mode, social_order, censorship, genres, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Slug, a.Title, a.Synopsis, a.Lang, a.TravelMode, a.SocialOrder, a.Censorship,
		string(genres), now.Format(time.RFC3339))
	return err
}

// HeroCard — герой мира для панели персонажа: имя и происхождение из
// ответов, здоровье и настроение из слоя состояния. Пустой указатель —
// героя ещё нет.
type HeroCard struct {
	Name    string `json:"name"`
	Estate  string `json:"estate"`
	Arrival string `json:"arrival"`
	Health  int    `json:"health"`
	Mood    string `json:"mood"`
}

// WorldCard — мир для экрана: настройки мира, время рождения, герой и
// признак открытого мира. Настройки читаются из базы мира, а не из
// отдельного индекса: база — единственный источник истины о своём мире.
type WorldCard struct {
	Anketa
	CreatedAt string    `json:"created_at"`
	Hero      *HeroCard `json:"hero"`
	Active    bool      `json:"active"`
}

// readWorld читает мир из его базы: у мира свой файл, поэтому строка одна.
func readWorld(path string) (WorldCard, bool, error) {
	db, err := openWorld(path)
	if err != nil {
		return WorldCard{}, false, err
	}
	defer db.Close()

	var card WorldCard
	var genres string
	err = db.QueryRow(`SELECT slug, title, synopsis, lang, travel_mode, social_order, censorship, genres, created_at
		FROM worlds LIMIT 1`).
		Scan(&card.Slug, &card.Title, &card.Synopsis, &card.Lang, &card.TravelMode,
			&card.SocialOrder, &card.Censorship, &genres, &card.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return WorldCard{}, false, nil
	}
	if err != nil {
		return WorldCard{}, false, err
	}

	if err := json.Unmarshal([]byte(genres), &card.Genres); err != nil {
		return WorldCard{}, false, err
	}

	// Герой — отдельный шаг: экран по этому признаку решает, открывать
	// форму создания или показывать панель персонажа. Ответы и состояние
	// читаются здесь же, чтобы экран не делал второй запрос.
	var answers, state string
	err = db.QueryRow(`SELECT answers, state FROM hero LIMIT 1`).Scan(&answers, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return card, true, nil
	}
	if err != nil {
		return WorldCard{}, false, err
	}

	var fields map[string]string
	if err := json.Unmarshal([]byte(answers), &fields); err != nil {
		return WorldCard{}, false, err
	}
	var st HeroState
	if err := json.Unmarshal([]byte(state), &st); err != nil {
		return WorldCard{}, false, err
	}

	card.Hero = &HeroCard{
		Name:    fields["name"],
		Estate:  fields["estate"],
		Arrival: fields["arrival"],
		Health:  st.Health,
		Mood:    st.Mood,
	}
	return card, true, nil
}
