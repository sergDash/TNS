package models

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// installServer скачивает архив релиза llama.cpp, сверяет сумму и
// раскладывает llama-server и общие библиотеки плоско в каталог bin:
// RUNPATH $ORIGIN находит lib*.so рядом с бинарником.
func installServer(ctx context.Context, client *http.Client, source, wantSHA, binDir string, onProgress func(done, total int64)) error {
	tmp, err := os.CreateTemp("", "tns-llama-*.tar.gz")
	if err != nil {
		return err
	}
	archive := tmp.Name()
	tmp.Close()
	defer os.Remove(archive)

	if err := fetch(ctx, client, source, archive, wantSHA, onProgress); err != nil {
		return err
	}
	return extractServer(archive, binDir)
}

// extractServer берёт из архива только llama-server и общие библиотеки.
// Остальное — серверы для других задач и README — приложению не нужно, и
// раскладывать их по каталогу сборки значит прятать мусор среди рабочих
// файлов.
func extractServer(archive, dir string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()

	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	found := false
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		base := filepath.Base(hdr.Name)
		if base != "llama-server" && !strings.HasPrefix(base, "lib") {
			continue
		}
		if err := writeRegular(filepath.Join(dir, base), tr); err != nil {
			return err
		}
		if base == "llama-server" {
			found = true
		}
	}

	if !found {
		return fmt.Errorf("в архиве нет llama-server")
	}
	return nil
}

// writeRegular кладёт файл из архива на место с правами запуска: библиотеки
// тоже ставятся исполняемыми, как их отдаёт релиз.
func writeRegular(path string, src io.Reader) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
