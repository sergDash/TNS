package models

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// manifest — указатель версии в каталоге роли: какой файл сейчас активен.
// Previous — файл прежней версии: откат меняет указатель, файлы остаются.
type manifest struct {
	Role        string    `json:"role"`
	ModelRef    string    `json:"model_ref"`
	Source      string    `json:"source"`
	File        string    `json:"file"`
	SHA256      string    `json:"sha256"`
	Size        int64     `json:"size"`
	InstalledAt time.Time `json:"installed_at"`
	Previous    string    `json:"previous"`
}

const manifestName = "manifest.json"

// readManifest читает указатель роли. Отсутствие файла — не ошибка: роль
// ещё не устанавливали.
func readManifest(dir string) (manifest, bool) {
	data, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		return manifest{}, false
	}

	var man manifest
	if err := json.Unmarshal(data, &man); err != nil {
		return manifest{}, false
	}
	return man, true
}

// writeManifest пишет указатель через временный файл: оборванная запись не
// должна оставить половину манифеста, по которому роль считается целой.
func writeManifest(dir string, man manifest) error {
	data, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp := filepath.Join(dir, manifestName+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, manifestName))
}
