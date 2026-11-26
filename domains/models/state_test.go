package models

import (
	"os"
	"path/filepath"
	"testing"
)

// shippedCatalog читает каталог поставки: проверка держится за настоящий файл
// в config/, иначе экран мог бы показывать не то, что лежит в репозитории.
func shippedCatalog(t *testing.T) Catalog {
	t.Helper()

	cat, err := LoadCatalog(filepath.Join("..", "..", "config", "models.json"))
	if err != nil {
		t.Fatalf("каталог поставки: %v", err)
	}
	return cat
}

// TestShippedCatalog — каталог полон настолько, чтобы экран мог посчитать
// итог: у каждой роли есть вариант с размером, памятью и источником.
func TestShippedCatalog(t *testing.T) {
	cat := shippedCatalog(t)

	if cat.Engine.File == "" || cat.Engine.Source == "" {
		t.Fatal("станок: нет файла или источника")
	}
	if len(cat.Roles) != 3 {
		t.Fatalf("ролей: %d, ожидалось 3", len(cat.Roles))
	}

	roles := map[Role]bool{}
	for _, slot := range cat.Roles {
		roles[slot.Role] = true
		for _, v := range slot.Variants {
			if v.Bytes <= 0 || v.RAMGB <= 0 || v.Source == "" {
				t.Fatalf("роль %s, вариант %s: нет размера, памяти или источника", slot.Role, v.ID)
			}
		}
	}
	for _, role := range []Role{RoleWeaver, RoleEmbed, RoleAnalysis} {
		if !roles[role] {
			t.Fatalf("в каталоге нет роли %s", role)
		}
	}

	// У смысла вариантов несколько: выбор идёт по объёму памяти машины.
	for _, slot := range cat.Roles {
		if slot.Role == RoleEmbed && len(slot.Variants) < 3 {
			t.Fatalf("у смысла вариантов: %d, ожидалось не меньше трёх", len(slot.Variants))
		}
	}
}

// TestCatalogRejectsBroken — неполный каталог останавливает запуск с понятным
// текстом, а не превращается в ноль на экране.
func TestCatalogRejectsBroken(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"пустой", `{}`},
		{"роль без вариантов", `{"engine":{"file":"llama-server"},"roles":[{"role":"weaver","title":"Ткач","variants":[]}]}`},
		{"вариант без размера", `{"engine":{"file":"llama-server"},"roles":[{"role":"weaver","title":"Ткач","variants":[{"id":"a","title":"А","file":"a.gguf","ramGb":5,"source":"https://example"}]}]}`},
		{"вариант без памяти", `{"engine":{"file":"llama-server"},"roles":[{"role":"weaver","title":"Ткач","variants":[{"id":"a","title":"А","file":"a.gguf","bytes":10,"source":"https://example"}]}]}`},
		{"роль дважды", `{"engine":{"file":"llama-server"},"roles":[{"role":"weaver","title":"Ткач","variants":[{"id":"a","title":"А","file":"a.gguf","bytes":10,"ramGb":5,"source":"https://example"}]},{"role":"weaver","title":"Ткач","variants":[{"id":"b","title":"Б","file":"b.gguf","bytes":10,"ramGb":5,"source":"https://example"}]}]}`},
		{"неизвестный ключ", `{"engine":{"file":"llama-server"},"rolez":[]}`},
	}

	dir := t.TempDir()
	for i, c := range cases {
		path := filepath.Join(dir, "catalog-"+string(rune('a'+i))+".json")
		if err := os.WriteFile(path, []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadCatalog(path); err == nil {
			t.Fatalf("%s: каталог принят", c.name)
		}
	}
}

// TestSnapshotMissing — пустые каталоги: ставить нужно всё, станок не найден,
// свободное место всё равно сообщается (место общее с ближайшим предком).
func TestSnapshotMissing(t *testing.T) {
	dir := t.TempDir()
	got := snapshotOf(Config{
		Dir: filepath.Join(dir, "models"),
		Bin: filepath.Join(dir, "bin"),
	}, shippedCatalog(t))

	if got.Ready {
		t.Fatal("пустой каталог признан готовым")
	}
	if got.Engine.Found {
		t.Fatal("станок найден без файла")
	}
	for _, slot := range got.Slots {
		for _, v := range slot.Variants {
			if v.Installed {
				t.Fatalf("роль %s, вариант %s признан установленным", slot.Role, v.ID)
			}
		}
	}
	if got.Disk.FreeBytes <= 0 {
		t.Fatalf("свободное место: %d", got.Disk.FreeBytes)
	}
}

// TestSnapshotInstalled — файлы на месте: роль установлена, а станок
// найден только если он исполняемый.
func TestSnapshotInstalled(t *testing.T) {
	cat := shippedCatalog(t)
	dir := t.TempDir()
	cfg := Config{Dir: filepath.Join(dir, "models"), Bin: filepath.Join(dir, "bin")}

	// Неисполняемый файл станком не считается: так выглядит недокачанный
	// или распакованный без прав бинарник.
	if err := os.MkdirAll(cfg.Bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Bin, cat.Engine.File), []byte("не бинарник"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := snapshotOf(cfg, cat); got.Engine.Found {
		t.Fatal("неисполняемый файл признан станком")
	}

	if err := os.WriteFile(filepath.Join(cfg.Bin, cat.Engine.File), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// WriteFile не меняет права у уже созданного файла, поэтому режим
	// выставляется явно: без него проверка исполняемости не сработает.
	if err := os.Chmod(filepath.Join(cfg.Bin, cat.Engine.File), 0o755); err != nil {
		t.Fatal(err)
	}

	// По одному варианту каждой роли достаточно, чтобы роль считалась
	// установленной: остальные варианты — замена, а не дополнение.
	for _, slot := range cat.Roles {
		path := filepath.Join(cfg.Dir, string(slot.Role), slot.Variants[0].File)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("веса"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got := snapshotOf(cfg, cat)
	if !got.Engine.Found {
		t.Fatal("исполняемый станок не найден")
	}
	if !got.Ready {
		t.Fatal("всё установлено, а готовность не выставлена")
	}
	for _, slot := range got.Slots {
		if !slot.Variants[0].Installed {
			t.Fatalf("роль %s: файл не признан установленным", slot.Role)
		}
	}
}
