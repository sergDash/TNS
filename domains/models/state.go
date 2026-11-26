package models

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Config — настройки модуля: где лежат файлы ролей и бинарник сервера.
// Пустое значение берёт путь по умолчанию, чтобы первая сборка работала
// без правки настроек.
type Config struct {
	// Catalog — файл каталога ролей и сервера.
	Catalog string `json:"catalog"`
	// Dir — каталог ролей: <dir>/<роль>/<файл>.
	Dir string `json:"dir"`
	// Bin — каталог сервера: <bin>/llama-server.
	Bin string `json:"bin"`
}

const (
	defaultCatalog = "config/models.json"
	defaultDir     = "world_db/models"
	defaultBin     = "bin"
)

// withDefaults достраивает пустые настройки до рабочих путей.
func (c Config) withDefaults() Config {
	if c.Catalog == "" {
		c.Catalog = defaultCatalog
	}
	if c.Dir == "" {
		c.Dir = defaultDir
	}
	if c.Bin == "" {
		c.Bin = defaultBin
	}
	return c
}

// snapshot — состояние установки для экрана. Данные только для чтения:
// ни один путь отсюда не пишется, поэтому снимок безопасно запрашивать
// сколько угодно раз.
type snapshot struct {
	// Server — сервер; Found значит, что бинарник на месте и исполняемый.
	Server serverState `json:"server"`
	// Slots — роли с вариантами; порядок каталога сохраняется.
	Slots []slotState `json:"slots"`
	// Disk — свободное место там, где будут лежать файлы.
	Disk diskState `json:"disk"`
	// Ready — ставить нечего: сервер и все роли уже на месте.
	Ready bool `json:"ready"`
	// Downloads — загрузки на момент снимка; поток обновлений отдаёт то же.
	Downloads downloadState `json:"downloads"`
}

// downloadState — строки загрузок и признак «что-то ещё идёт». Снимок и
// поток отдают одинаковую форму, чтобы клиент разбирал её одним кодом.
type downloadState struct {
	Items  []progress `json:"items"`
	Active bool       `json:"active"`
}

type serverState struct {
	Title   string `json:"title"`
	File    string `json:"file"`
	Key     string `json:"key"`
	Found   bool   `json:"found"`
	Path    string `json:"path"`
	Version string `json:"version"`
	// Bytes — сколько сервер занимает на диске: сам бинарник и его
	// библиотеки. Размер архива после установки уже не узнать, а место
	// на диске игроку нужно то же самое.
	Bytes int64 `json:"bytes"`
}

type slotState struct {
	Role     string         `json:"role"`
	Title    string         `json:"title"`
	Note     string         `json:"note"`
	Variants []variantState `json:"variants"`
}

type variantState struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Key       string `json:"key"`
	File      string `json:"file"`
	Bytes     int64  `json:"bytes"`
	Approx    bool   `json:"approx"`
	RAMGB     int    `json:"ramGb"`
	Installed bool   `json:"installed"`
	Path      string `json:"path"`
}

type diskState struct {
	FreeBytes int64  `json:"freeBytes"`
	Path      string `json:"path"`
}

// snapshot собирает состояние по каталогам из настроек. catalog — каталог
// поставки: снимок ничего не решает сам, он только сверяет его с диском.
func snapshotOf(cfg Config, catalog Catalog) snapshot {
	out := snapshot{
		Server: serverState{
			Title:   catalog.Server.Title,
			File:    catalog.Server.File,
			Key:     serverKey,
			Version: catalog.Server.Version,
			Path:    serverPath(cfg, catalog),
			Found:   serverFound(cfg, catalog),
			Bytes:   serverSize(cfg, catalog),
		},
	}

	ready := out.Server.Found
	for _, slot := range catalog.Roles {
		state := slotState{Role: string(slot.Role), Title: slot.Title, Note: slot.Note}
		installed := false
		for _, variant := range slot.Variants {
			v := variantState{
				ID:     variant.ID,
				Title:  variant.Title,
				Key:    modelKey(slot.Role, variant.File),
				File:   variant.File,
				Bytes:  variant.Bytes,
				Approx: variant.Approx,
				RAMGB:  variant.RAMGB,
				Path:   variantPath(cfg, slot.Role, variant.File),
			}
			if variantInstalled(cfg, slot.Role, variant.File) {
				v.Installed = true
				installed = true
			}
			state.Variants = append(state.Variants, v)
		}
		if !installed {
			ready = false
		}
		out.Slots = append(out.Slots, state)
	}
	out.Ready = ready

	free, dir := freeSpace(cfg.Dir)
	out.Disk = diskState{FreeBytes: free, Path: dir}
	return out
}

// serverPath — где лежит бинарник сервера.
func serverPath(cfg Config, catalog Catalog) string {
	return filepath.Join(cfg.Bin, catalog.Server.File)
}

// serverFound сообщает, есть ли исполняемый сервер. Лежащий рядом архив
// или огрызок загрузки сервером не считается.
func serverFound(cfg Config, catalog Catalog) bool {
	info, err := os.Stat(serverPath(cfg, catalog))
	return err == nil && info.Mode().Perm()&0o111 != 0
}

// serverSize считает, сколько занимают сервер и его библиотеки в каталоге
// bin. Посторонние файлы (собранное приложение) не учитываются.
func serverSize(cfg Config, catalog Catalog) int64 {
	entries, err := os.ReadDir(cfg.Bin)
	if err != nil {
		return 0
	}

	var total int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name != catalog.Server.File && !strings.HasPrefix(name, "lib") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		total += info.Size()
	}
	return total
}

// variantPath — где лежит файл варианта роли.
func variantPath(cfg Config, role Role, file string) string {
	return filepath.Join(cfg.Dir, string(role), file)
}

// variantInstalled сообщает, лежит ли непустой файл варианта. Манифест и
// сверка суммы проверяются загрузкой; для экрана достаточно файла на диске.
func variantInstalled(cfg Config, role Role, file string) bool {
	info, err := os.Stat(variantPath(cfg, role, file))
	return err == nil && info.Size() > 0
}

// freeSpace отвечает, сколько свободно там, где будут лежать модели.
// Каталога может ещё не быть: тогда смотрится ближайший существующий
// предок, потому что место общее для всей файловой системы.
func freeSpace(path string) (int64, string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	for {
		var st syscall.Statfs_t
		if err := syscall.Statfs(abs, &st); err == nil {
			return int64(st.Bavail) * int64(st.Bsize), abs
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return 0, ""
		}
		abs = parent
	}
}
