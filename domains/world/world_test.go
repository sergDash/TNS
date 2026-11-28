package world

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testLists() Lists {
	return Lists{
		Genres:       []string{"фэнтези", "детектив"},
		SocialOrders: []string{"нет", "феодализм"},
		TravelModes:  []string{"реальная", "вымышленная", "смешанная"},
		Ratings:      []string{"16+", "18+", "21+"},
		Langs:        []string{"ru"},
	}
}

func testHero() Hero {
	return Hero{
		Title: "Герой",
		Fields: []HeroField{
			{ID: "name", Title: "Имя", Type: "text", Required: true, Limit: 5},
			{ID: "estate", Title: "Сословие", Type: "choice", Required: true, Options: []string{"купец", "крестьянин"}},
			{ID: "books", Title: "Книги", Type: "text", Limit: 10},
		},
	}
}

func validAnketa() Anketa {
	return Anketa{
		Slug:        "dark-realm",
		Title:       "Тёмное королевство",
		Synopsis:    "Король умер, наследник пропал. Страна ждёт.",
		Lang:        "ru",
		TravelMode:  "вымышленная",
		Genres:      []string{"фэнтези"},
		SocialOrder: "феодализм",
		Censorship:  "16+",
	}
}

func validHeroAnswers() map[string]string {
	return map[string]string{"name": "Илья", "estate": "купец", "books": "Азимов"}
}

// TestValidateGood — полная анкета проходит проверку.
func TestValidateGood(t *testing.T) {
	if ve := testLists().Validate(validAnketa()); ve != nil {
		t.Fatalf("отказ на верной анкете: %v", ve)
	}
}

// TestValidateFields — каждый отказ называет своё поле: по списку полей
// форма подсвечивает ввод, а не показывает одну общую строку.
func TestValidateFields(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Anketa)
		field  string
	}{
		{"пустое название", func(a *Anketa) { a.Title = "  " }, "title"},
		{"пустой адрес", func(a *Anketa) { a.Slug = "" }, "slug"},
		{"адрес с кириллицей", func(a *Anketa) { a.Slug = "тёмный" }, "slug"},
		{"пустой синопсис", func(a *Anketa) { a.Synopsis = "" }, "synopsis"},
		{"четыре предложения", func(a *Anketa) { a.Synopsis = "Раз. Два. Три. Четыре." }, "synopsis"},
		{"язык вне списка", func(a *Anketa) { a.Lang = "tlh" }, "lang"},
		{"режим вне списка", func(a *Anketa) { a.TravelMode = "космос" }, "travel_mode"},
		{"строй не выбран", func(a *Anketa) { a.SocialOrder = "" }, "social_order"},
		{"строй вне списка", func(a *Anketa) { a.SocialOrder = "анархия" }, "social_order"},
		{"рейтинг не выбран", func(a *Anketa) { a.Censorship = "" }, "censorship"},
		{"жанр вне списка", func(a *Anketa) { a.Genres = []string{"киберпанк"} }, "genres"},
	}

	for _, c := range cases {
		a := validAnketa()
		c.change(&a)

		ve := testLists().Validate(a)
		if ve == nil {
			t.Fatalf("%s: анкета принята", c.name)
		}
		found := false
		for _, f := range ve.Fields {
			if f.Field == c.field {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: в отказе нет поля %s: %v", c.name, c.field, ve.Fields)
		}
	}
}

// TestCreateAndRead — мир рождается в своей базе, читается обратно, а
// повторное открытие не повторяет миграцию.
func TestCreateAndRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dark-realm", "world.db")

	if err := createWorld(path, validAnketa(), time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("рождение: %v", err)
	}

	got, ok, err := readWorld(path)
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	if !ok {
		t.Fatal("мир не найден после рождения")
	}
	if got.Title != "Тёмное королевство" || got.SocialOrder != "феодализм" || got.Censorship != "16+" {
		t.Fatalf("прочитано не то: %+v", got)
	}
	if len(got.Genres) != 1 || got.Genres[0] != "фэнтези" {
		t.Fatalf("жанры: %v", got.Genres)
	}

	// Повторное открытие: версия схемы уже первая, шагов не выполняется.
	db, err := openWorld(path)
	if err != nil {
		t.Fatalf("повторное открытие: %v", err)
	}
	defer db.Close()

	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("версия схемы: %d", version)
	}
}

// TestNewerSchemaRefused — база новее кода не открывается: старая сборка не
// должна писать в схему, которую не знает.
func TestNewerSchemaRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "future", "world.db")

	first, err := openWorld(path)
	if err != nil {
		t.Fatalf("первое открытие: %v", err)
	}
	first.Close()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	if _, err := openWorld(path); err == nil || !strings.Contains(err.Error(), "новее кода") {
		t.Fatalf("база новее кода открылась: %v", err)
	}
}

// TestHeroValidate — форма героя проверяется по конфигу: обязательное
// заполнено, выбор из списка, текст в пределе, чужих полей нет.
func TestHeroValidate(t *testing.T) {
	cases := []struct {
		name   string
		change func(map[string]string)
		field  string
	}{
		{"пустое имя", func(a map[string]string) { a["name"] = "  " }, "name"},
		{"имя длиннее предела", func(a map[string]string) { a["name"] = "Илья Муромец" }, "name"},
		{"сословие не выбрано", func(a map[string]string) { delete(a, "estate") }, "estate"},
		{"сословие вне списка", func(a map[string]string) { a["estate"] = "аристократ" }, "estate"},
		{"книги длиннее предела", func(a map[string]string) { a["books"] = "очень длинный список книг" }, "books"},
		{"чужое поле", func(a map[string]string) { a["nickname"] = "x" }, "nickname"},
	}

	if ve := testHero().ValidateHero(validHeroAnswers()); ve != nil {
		t.Fatalf("верные ответы не приняты: %v", ve)
	}

	for _, c := range cases {
		answers := validHeroAnswers()
		c.change(answers)

		ve := testHero().ValidateHero(answers)
		if ve == nil {
			t.Fatalf("%s: ответы приняты", c.name)
		}
		found := false
		for _, f := range ve.Fields {
			if f.Field == c.field {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: в отказе нет поля %s: %v", c.name, c.field, ve.Fields)
		}
	}
}

// TestCreateHero — герой записывается в базу мира и читается обратно ровно
// теми ответами, которые пришли.
func TestCreateHero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dark-realm", "world.db")

	if err := createWorld(path, validAnketa(), time.Now().UTC()); err != nil {
		t.Fatalf("мир: %v", err)
	}
	if err := createHero(path, validHeroAnswers(), time.Now().UTC()); err != nil {
		t.Fatalf("герой: %v", err)
	}

	answers, ok, err := readHero(path)
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	if !ok {
		t.Fatal("герой не найден после рождения")
	}
	if answers["name"] != "Илья" || answers["estate"] != "купец" || answers["books"] != "Азимов" {
		t.Fatalf("прочитано не то: %v", answers)
	}
}

// shippedWorldSettings читает раздел модуля мира из настроек экземпляра:
// проверка идёт по тем значениям, с которыми приложение запускается.
func shippedWorldSettings(t *testing.T) Config {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "..", "config", "httpapi.json"))
	if err != nil {
		t.Fatalf("настройки: %v", err)
	}

	var file struct {
		Modules struct {
			Settings map[string]json.RawMessage `json:"settings"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("настройки: %v", err)
	}

	var cfg Config
	if err := json.Unmarshal(file.Modules.Settings["world"], &cfg); err != nil {
		t.Fatalf("настройки мира: %v", err)
	}
	cfg.Enums = filepath.Join("..", "..", cfg.Enums)
	return cfg
}

// TestShippedEnums — перечни берутся из базы, выбранное из настроек: размеры
// перечней, порядок полей героя и умолчания должны совпасть с поставкой.
func TestShippedEnums(t *testing.T) {
	cfg := shippedWorldSettings(t)
	lists, err := LoadLists(cfg.Enums, cfg.Langs, cfg.HeroDefaults)
	if err != nil {
		t.Fatalf("мир: %v", err)
	}

	if len(lists.Genres) != 30 || len(lists.SocialOrders) != 18 || len(lists.Ratings) != 3 {
		t.Fatalf("канон: жанров %d, строёв %d, рейтингов %d",
			len(lists.Genres), len(lists.SocialOrders), len(lists.Ratings))
	}
	if len(lists.Langs) != 1 || lists.Langs[0] != "ru" {
		t.Fatalf("языки: %v", lists.Langs)
	}

	var order []string
	for _, f := range lists.Hero.Fields {
		order = append(order, f.ID)
	}
	want := []string{"name", "estate", "arrival", "origin", "books"}
	if len(order) != len(want) {
		t.Fatalf("поля героя: %v", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("порядок полей героя: %v", order)
		}
	}

	estate := heroFieldByID(lists.Hero, "estate")
	if estate == nil || estate.Type != "choice" || estate.Default != "крестьянин" {
		t.Fatalf("поле estate: %+v", estate)
	}
	if len(estate.Options) != 6 {
		t.Fatalf("сословий: %d", len(estate.Options))
	}
}

// TestSettingsOutsideEnums — значение настроек, которого нет в перечнях,
// останавливает запуск: это опечатка, а не выбор.
func TestSettingsOutsideEnums(t *testing.T) {
	enumsPath := filepath.Join("..", "..", "schemas", "enums.db")

	cases := []struct {
		name     string
		langs    []string
		defaults map[string]string
	}{
		{"язык вне перечня", []string{"tlh"}, nil},
		{"умолчание вне списка", []string{"ru"}, map[string]string{"estate": "барон"}},
		{"умолчание неизвестного поля", []string{"ru"}, map[string]string{"nickname": "x"}},
		{"без языков", nil, nil},
	}

	for _, c := range cases {
		_, err := LoadLists(enumsPath, c.langs, c.defaults)
		if err == nil {
			t.Fatalf("%s: настройки приняты", c.name)
		}
	}
}

// TestListWorlds — карточки читаются из папок миров, идут по времени
// рождения, а открытый мир помечен: по этим полям экран строит сетку.
func TestListWorlds(t *testing.T) {
	dir := t.TempDir()

	first := validAnketa()
	first.Slug = "first-book"
	first.Title = "Первая книга"
	second := validAnketa()
	second.Slug = "second-book"
	second.Title = "Вторая книга"

	if err := createWorld(filepath.Join(dir, "second-book", "world.db"), second, time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("второй мир: %v", err)
	}
	if err := createWorld(filepath.Join(dir, "first-book", "world.db"), first, time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("первый мир: %v", err)
	}
	if err := createHero(filepath.Join(dir, "first-book", "world.db"), map[string]string{"name": "Анна"}, time.Now().UTC()); err != nil {
		t.Fatalf("герой: %v", err)
	}

	m := &module{cfg: Config{Dir: dir}, active: "second-book"}
	resp, err := m.listsResponse()
	if err != nil {
		t.Fatalf("карточки: %v", err)
	}

	if len(resp.Worlds) != 2 {
		t.Fatalf("миров: %d", len(resp.Worlds))
	}
	if resp.Worlds[0].Slug != "first-book" || resp.Worlds[1].Slug != "second-book" {
		t.Fatalf("порядок миров: %s, %s", resp.Worlds[0].Slug, resp.Worlds[1].Slug)
	}
	hero := resp.Worlds[0].Hero
	if hero == nil || hero.Name != "Анна" {
		t.Fatalf("герой первого мира: %+v", resp.Worlds[0].Hero)
	}
	if hero.Health != 100 || hero.Mood != "спокойное" {
		t.Fatalf("состояние героя: %+v", hero)
	}
	if resp.Worlds[0].Active || !resp.Worlds[1].Active {
		t.Fatalf("пометка открытого мира: %v, %v", resp.Worlds[0].Active, resp.Worlds[1].Active)
	}
}

// TestListWorldsEmpty — пустой каталог отдаёт пустой список, а не null:
// экран обходит список и не должен проверять его на nil.
func TestListWorldsEmpty(t *testing.T) {
	m := &module{cfg: Config{Dir: t.TempDir()}}
	resp, err := m.listsResponse()
	if err != nil {
		t.Fatalf("карточки: %v", err)
	}
	if resp.Worlds == nil {
		t.Fatal("список миров nil, нужен пустой массив")
	}
	if len(resp.Worlds) != 0 {
		t.Fatalf("миров: %d", len(resp.Worlds))
	}
}

// TestRestStateMatchesMigration — состояние покоя в DDL миграции и в коде
// совпадают: иначе новорождённый герой и герой, прошедший миграцию,
// начинают с разных чисел.
func TestRestStateMatchesMigration(t *testing.T) {
	var fromMigration HeroState
	if err := json.Unmarshal([]byte(restStateJSON), &fromMigration); err != nil {
		t.Fatalf("состояние из миграции: %v", err)
	}
	if fromMigration != restState() {
		t.Fatalf("состояния разошлись: миграция %+v, код %+v", fromMigration, restState())
	}
}

// TestHeroStateMigration — база версии 2 получает слой состояния, а уже
// рождённый герой — состояние покоя: нулевое здоровье читалось бы как
// мёртвый герой.
func TestHeroStateMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old", "world.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE hero (answers TEXT NOT NULL, created_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO hero (answers, created_at) VALUES (?, ?)`,
		`{"name":"Илья"}`, "2026-11-27T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 2"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	migrated, err := openWorld(path)
	if err != nil {
		t.Fatalf("миграция: %v", err)
	}
	defer migrated.Close()

	var raw string
	if err := migrated.QueryRow(`SELECT state FROM hero LIMIT 1`).Scan(&raw); err != nil {
		t.Fatalf("состояние после миграции: %v", err)
	}
	var st HeroState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatalf("разбор состояния: %v", err)
	}
	if st != restState() {
		t.Fatalf("состояние после миграции: %+v", st)
	}
}
