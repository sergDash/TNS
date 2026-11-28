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
//
// Ссылки в архиве сохраняются как ссылки. Опыт 2026-11-28: без них bin
// оставался с libllama-common.so.0.8.0, но без libllama-common.so.0, и
// llama-server не запускался — загрузчик ищет soname, а не файл с версией.
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

	// Жёсткие ссылки создаются после разбора: цель может лежать в архиве
	// позже ссылки на неё.
	type hardLink struct{ path, target string }
	var links []hardLink

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		base := filepath.Base(hdr.Name)
		if base != "llama-server" && !strings.HasPrefix(base, "lib") {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeReg:
			if err := writeRegular(filepath.Join(dir, base), tr); err != nil {
				return err
			}
			if base == "llama-server" {
				found = true
			}
		case tar.TypeSymlink:
			if err := writeSymlink(filepath.Join(dir, base), filepath.Base(hdr.Linkname)); err != nil {
				return err
			}
		case tar.TypeLink:
			links = append(links, hardLink{
				path:   filepath.Join(dir, base),
				target: filepath.Join(dir, filepath.Base(hdr.Linkname)),
			})
		}
	}

	for _, l := range links {
		_ = os.Remove(l.path)
		if err := os.Link(l.target, l.path); err != nil {
			return err
		}
	}

	if !found {
		return fmt.Errorf("в архиве нет llama-server")
	}
	return nil
}

// writeSymlink кладёт ссылку из архива. Прежняя ссылка с тем же именем
// убирается: повторная распаковка не должна падать на уже созданном.
func writeSymlink(path, target string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(target, path)
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
