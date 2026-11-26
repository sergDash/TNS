package world

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// activeWorld — выбор игрока: адрес открытого мира. Это состояние
// экземпляра, а не данные мира: переезд папки мира не должен тащить за собой
// чужой выбор, поэтому указатель лежит в config/, рядом с настройками
// модуля, а не внутри папки мира.
type activeWorld struct {
	Slug string `json:"slug"`
}

// readActive отдаёт адрес открытого мира. Нет файла, он пуст или испорчен —
// мир не выбран: ошибка чтения не должна мешать открыть страницу и выбрать
// мир заново.
func readActive(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	var a activeWorld
	if err := json.Unmarshal(data, &a); err != nil {
		return ""
	}
	return strings.TrimSpace(a.Slug)
}

// saveActive записывает адрес открытого мира. Пустой адрес — удаление файла:
// «мир не выбран» это отсутствие записи, а не пустое значение. Запись
// атомарная (временный файл рядом и переименование): прерванная запись не
// должна оставить обрезанный JSON.
func saveActive(path, slug string) error {
	if slug == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	data, err := json.Marshal(activeWorld{Slug: slug})
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".active_world-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// worldSlugs перечисляет миры на диске. Миром считается папка с базой
// внутри: каталог, оставшийся от прерванного рождения, в список не попадает.
// Порядок — по имени папки, чтобы выбор при одном мире не зависел от
// порядка чтения каталога.
func worldSlugs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "world.db")); err != nil {
			continue
		}
		out = append(out, e.Name())
	}
	return out, nil
}
