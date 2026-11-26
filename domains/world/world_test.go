package world

import (
	"database/sql"
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

// TestShippedLists — перечни поставки читаются и не пусты: экран берёт
// варианты из них, а не из кода.
func TestShippedLists(t *testing.T) {
	lists, err := LoadLists(filepath.Join("..", "..", "config", "world.json"))
	if err != nil {
		t.Fatalf("перечни: %v", err)
	}
	if len(lists.Genres) < 10 || len(lists.SocialOrders) < 5 || len(lists.Ratings) != 3 {
		t.Fatalf("перечни неполны: жанров %d, строёв %d, рейтингов %d",
			len(lists.Genres), len(lists.SocialOrders), len(lists.Ratings))
	}
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
		name  string
		change func(*Anketa)
		field string
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

// TestShippedHeroForm — форма героя есть в конфиге поставки: пока вопрос о
// её составе открыт, она живёт данными и должна быть непустой.
func TestShippedHeroForm(t *testing.T) {
	lists, err := LoadLists(filepath.Join("..", "..", "config", "world.json"))
	if err != nil {
		t.Fatalf("перечни: %v", err)
	}
	if len(lists.Hero.Fields) == 0 {
		t.Fatal("в конфиге нет полей героя")
	}
	for _, f := range lists.Hero.Fields {
		if f.ID == "" || f.Title == "" {
			t.Fatalf("поле героя без имени: %+v", f)
		}
		if f.Type == "choice" && len(f.Options) == 0 {
			t.Fatalf("поле %s: выбор без вариантов", f.ID)
		}
	}
}

// TestHeroFormRejected — форма героя проверяется по тем же правилам, что
// описаны в schemas/hero.schema.json: неизвестный вид, выбор без вариантов,
// умолчание вне списка и повтор имени останавливают запуск.
func TestHeroFormRejected(t *testing.T) {
	cases := []struct {
		name string
		hero Hero
	}{
		{"выбор без вариантов", Hero{Fields: []HeroField{{ID: "estate", Title: "Сословие", Type: "choice"}}}},
		{"текст с вариантами", Hero{Fields: []HeroField{{ID: "name", Title: "Имя", Type: "text", Options: []string{"а"}}}}},
		{"умолчание вне списка", Hero{Fields: []HeroField{{ID: "estate", Title: "Сословие", Type: "choice", Options: []string{"купец"}, Default: "аристократ"}}}},
		{"повтор имени", Hero{Fields: []HeroField{{ID: "name", Title: "Имя", Type: "text"}, {ID: "name", Title: "Ещё имя", Type: "text"}}}},
		{"неизвестный вид", Hero{Fields: []HeroField{{ID: "name", Title: "Имя", Type: "select"}}}},
	}

	for _, c := range cases {
		if err := c.hero.validateForm(); err == nil {
			t.Fatalf("%s: форма принята", c.name)
		}
	}
}

// TestShippedHeroForm проверяет и умолчания: у выбора умолчание лежит внутри
// списка, иначе форма покажет значение, которого нет среди вариантов.
func TestShippedHeroFormDefaults(t *testing.T) {
	lists, err := LoadLists(filepath.Join("..", "..", "config", "world.json"))
	if err != nil {
		t.Fatalf("перечни: %v", err)
	}
	for _, f := range lists.Hero.Fields {
		if f.Type != "choice" || f.Default == "" {
			continue
		}
		found := false
		for _, opt := range f.Options {
			if opt == f.Default {
				found = true
			}
		}
		if !found {
			t.Fatalf("поле %s: умолчание %q не среди вариантов", f.ID, f.Default)
		}
	}
}
