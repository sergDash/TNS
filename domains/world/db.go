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
const schemaVersion = 2

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

// createHero записывает героя мира. Ответы хранятся как JSON: набор полей
// задаётся конфигом, и схема за ним не должна меняться.
func createHero(path string, answers map[string]string, now time.Time) error {
	data, err := json.Marshal(answers)
	if err != nil {
		return err
	}

	db, err := openWorld(path)
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec(`INSERT INTO hero (answers, created_at) VALUES (?, ?)`,
		string(data), now.Format(time.RFC3339))
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

// WorldCard — мир для экрана: настройки мира, время рождения и признак
// рождённого героя. Настройки читаются из базы мира, а не из отдельного
// индекса: база — единственный источник истины о своём мире.
type WorldCard struct {
	Anketa
	CreatedAt string `json:"created_at"`
	Hero      bool   `json:"hero"`
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

	// Герой — отдельный шаг: экран по этому признаку решает, вести игрока
	// к рождению героя или уже в мир.
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM hero)`).Scan(&card.Hero); err != nil {
		return WorldCard{}, false, err
	}
	return card, true, nil
}
