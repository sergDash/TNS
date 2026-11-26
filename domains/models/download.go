package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// progressInterval — не чаще четырёх сообщений в секунду: экрану этого
// хватает, а перерисовка не дёргается на каждом блоке.
const progressInterval = 250 * time.Millisecond

// fetch скачивает url в dest с докачкой. Байты пишутся в dest+".part", а
// готовый файл появляется только после сверки суммы: недокачанный файл
// не должен выглядеть установленным.
//
// wantSHA пуст — сверка не выполняется (у части вариантов суммы ещё нет).
// onProgress вызывается не чаще progressInterval и в конце.
func fetch(ctx context.Context, client *http.Client, url, dest, wantSHA string, onProgress func(done, total int64)) error {
	part := dest + ".part"

	// Уже готовый файл не качается повторно: так повтор работы после отказа
	// не тратит трафик.
	if wantSHA != "" {
		if got, err := fileSHA(dest); err == nil && got == wantSHA {
			if info, statErr := os.Stat(dest); statErr == nil {
				onProgress(info.Size(), info.Size())
			}
			return nil
		}
	}

	// Каталог создаётся до записи .part: иначе файл некуда положить.
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}

	var start int64
	if info, err := os.Stat(part); err == nil {
		start = info.Size()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if start > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(start, 10)+"-")
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var flags int
	switch resp.StatusCode {
	case http.StatusOK:
		// Сервер не умеет докачку: начинаем файл заново.
		start = 0
		flags = os.O_CREATE | os.O_TRUNC | os.O_WRONLY
	case http.StatusPartialContent:
		flags = os.O_CREATE | os.O_APPEND | os.O_WRONLY
	default:
		return fmt.Errorf("скачивание: сервер ответил %s", resp.Status)
	}

	total := int64(-1)
	if resp.ContentLength >= 0 {
		total = start + resp.ContentLength
	}

	file, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}

	done := start
	last := time.Now()
	onProgress(done, total)

	buf := make([]byte, 64<<10)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := file.Write(buf[:n]); writeErr != nil {
				file.Close()
				return writeErr
			}
			done += int64(n)
			if time.Since(last) >= progressInterval {
				onProgress(done, total)
				last = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			file.Close()
			return readErr
		}
	}
	if err := file.Close(); err != nil {
		return err
	}
	if total > 0 && done != total {
		return fmt.Errorf("скачивание: получено %d из %d байт", done, total)
	}

	if wantSHA != "" {
		got, err := fileSHA(part)
		if err != nil {
			return err
		}
		if got != wantSHA {
			// Испорченный файл удаляется: докачивать его бессмысленно,
			// а оставленный .part соврал бы следующей попытке.
			os.Remove(part)
			return fmt.Errorf("скачивание: sha256 не совпал: получено %s", got)
		}
	}

	onProgress(done, done)
	return os.Rename(part, dest)
}

// fileSHA считает сумму файла целиком: докачка складывает файл из кусков,
// поэтому сумма берётся по готовому результату, а не по потоку.
func fileSHA(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
