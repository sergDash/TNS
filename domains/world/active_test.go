package world

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// activeModule собирает модуль с временным каталогом миров и файлом выбора:
// списки для проверки выбора не нужны.
func activeModule(t *testing.T, dir string) *module {
	t.Helper()
	return &module{cfg: Config{Dir: dir, Active: filepath.Join(dir, "active_world.json")}}
}

// bornWorld рождает мир в каталоге миров: миром считается папка с базой
// внутри, поэтому проверка выбора работает с настоящими файлами.
func bornWorld(t *testing.T, dir, slug string) {
	t.Helper()
	a := validAnketa()
	a.Slug = slug
	path := filepath.Join(dir, slug, "world.db")
	if err := createWorld(path, a, time.Now().UTC()); err != nil {
		t.Fatalf("мир %s: %v", slug, err)
	}
}

// TestActiveChoice — выбор пишется и читается; пустой выбор — это отсутствие
// файла, а испорченный файл читается как «мир не выбран».
func TestActiveChoice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active_world.json")

	if got := readActive(path); got != "" {
		t.Fatalf("без файла выбор: %q", got)
	}

	if err := saveActive(path, "dark-realm"); err != nil {
		t.Fatal(err)
	}
	if got := readActive(path); got != "dark-realm" {
		t.Fatalf("прочитан выбор: %q", got)
	}

	if err := os.WriteFile(path, []byte("{не json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readActive(path); got != "" {
		t.Fatalf("испорченный файл дал выбор: %q", got)
	}

	if err := saveActive(path, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("пустой выбор оставил файл: %v", err)
	}
}

// TestRestoreActive — при запуске выбор сверяется с папками миров: удалённый
// мир даёт пустой выбор, единственный мир открывается сам, а при двух мирах
// без выбора угадывать нечего.
func TestRestoreActive(t *testing.T) {
	cases := []struct {
		name   string
		worlds []string
		saved  string
		want   string
	}{
		{"мира нет, выбора нет", nil, "", ""},
		{"один мир открывается сам", []string{"dark-realm"}, "", "dark-realm"},
		{"два мира без выбора", []string{"dark-realm", "second"}, "", ""},
		{"сохранённый выбор главнее", []string{"dark-realm", "second"}, "second", "second"},
		{"удалённый мир не выбран", []string{"second"}, "dark-realm", "second"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, slug := range c.worlds {
				bornWorld(t, dir, slug)
			}

			m := activeModule(t, dir)
			if c.saved != "" {
				if err := saveActive(m.cfg.Active, c.saved); err != nil {
					t.Fatal(err)
				}
			}

			if err := m.restoreActive(); err != nil {
				t.Fatalf("восстановление: %v", err)
			}
			if got := m.activeSlug(); got != c.want {
				t.Fatalf("выбор: %q, ожидался %q", got, c.want)
			}
			if got := readActive(m.cfg.Active); got != c.want {
				t.Fatalf("выбор в файле: %q, ожидался %q", got, c.want)
			}
		})
	}
}
