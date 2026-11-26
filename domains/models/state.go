package models

import (
	"os"
	"path/filepath"
	"syscall"
)

// Config — настройки модуля: где лежат файлы ролей и бинарник машины.
// Пустое значение берёт путь по умолчанию, чтобы первая сборка работала
// без правки настроек.
type Config struct {
	// Catalog — файл каталога ролей и машины.
	Catalog string `json:"catalog"`
	// Dir — каталог ролей: <dir>/<роль>/<файл>.
	Dir string `json:"dir"`
	// Bin — каталог машины: <bin>/llama-server.
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
	// Machine — машина; Found значит, что бинарник на месте и исполняемый.
	Machine machineState `json:"machine"`
	// Slots — роли с вариантами; порядок каталога сохраняется.
	Slots []slotState `json:"slots"`
	// Disk — свободное место там, где будут лежать файлы.
	Disk diskState `json:"disk"`
	// Ready — ставить нечего: машина и все роли уже на месте.
	Ready bool `json:"ready"`
}

type machineState struct {
	Title   string `json:"title"`
	File    string `json:"file"`
	Found   bool   `json:"found"`
	Path    string `json:"path"`
	Version string `json:"version"`
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
		Machine: machineState{
			Title:   catalog.Machine.Title,
			File:    catalog.Machine.File,
			Version: catalog.Machine.Version,
			Path:    filepath.Join(cfg.Bin, catalog.Machine.File),
		},
	}

	// Машина считается найденным, только если файл исполняемый: лежащий
	// рядом архив или огрызок загрузки станцией не является.
	if info, err := os.Stat(out.Machine.Path); err == nil && info.Mode().Perm()&0o111 != 0 {
		out.Machine.Found = true
	}

	ready := out.Machine.Found
	for _, slot := range catalog.Roles {
		state := slotState{Role: string(slot.Role), Title: slot.Title, Note: slot.Note}
		installed := false
		for _, variant := range slot.Variants {
			v := variantState{
				ID:     variant.ID,
				Title:  variant.Title,
				File:   variant.File,
				Bytes:  variant.Bytes,
				Approx: variant.Approx,
				RAMGB:  variant.RAMGB,
				Path:   filepath.Join(cfg.Dir, string(slot.Role), variant.File),
			}
			// Установленным считается непустой файл нужного имени: манифест
			// и сверка суммы появятся вместе с загрузчиком, а до тех пор
			// единственный честный признак — файл на диске.
			if info, err := os.Stat(v.Path); err == nil && info.Size() > 0 {
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
