package models

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestFetch — файл скачивается, сумма сверяется, прогресс доходит до конца.
func TestFetch(t *testing.T) {
	body := []byte(strings.Repeat("модель", 1000))
	sum := sha256.Sum256(body)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer server.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "role", "file.gguf")

	var lastDone, lastTotal int64
	err := fetch(context.Background(), server.Client(), server.URL, dest, hex.EncodeToString(sum[:]), func(done, total int64) {
		lastDone, lastTotal = done, total
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("файл: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatal("содержимое не совпало")
	}
	if lastDone != int64(len(body)) || lastTotal != int64(len(body)) {
		t.Fatalf("прогресс: %d из %d", lastDone, lastTotal)
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatal(".part остался после успешной загрузки")
	}
}

// TestFetchResume — докачка начинается с размера .part, а не с нуля.
func TestFetchResume(t *testing.T) {
	body := []byte(strings.Repeat("0123456789", 100))
	half := len(body) / 2

	var ranged bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Range")
		if header == "" {
			w.Write(body)
			return
		}
		ranged = true

		start, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(header, "bytes="), "-"))
		if err != nil {
			t.Errorf("Range: %q", header)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(body)-1, len(body)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(body[start:])
	}))
	defer server.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "file.gguf")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest+".part", body[:half], 0o644); err != nil {
		t.Fatal(err)
	}

	if err := fetch(context.Background(), server.Client(), server.URL, dest, "", func(int64, int64) {}); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !ranged {
		t.Fatal("докачка не запросила Range")
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatal("содержимое после докачки не совпало")
	}
}

// TestFetchBadSHA — несовпадение суммы убирает файл: недокачанный .part
// не должен выглядеть установленной моделью.
func TestFetchBadSHA(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("не то"))
	}))
	defer server.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "file.gguf")

	err := fetch(context.Background(), server.Client(), server.URL, dest, strings.Repeat("0", 64), func(int64, int64) {})
	if err == nil {
		t.Fatal("несовпадение суммы принято")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("файл остался после неверной суммы")
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatal(".part остался после неверной суммы")
	}
}

// TestExtractServer — из архива берутся только сервер и библиотеки.
func TestExtractServer(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	files := map[string]string{
		"llama-b1/llama-server": "#!/bin/sh\n",
		"llama-b1/libggml.so":   "lib",
		"llama-b1/README.md":    "не нужно",
	}
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()

	dir := t.TempDir()
	archive := filepath.Join(dir, "release.tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(dir, "bin")
	if err := extractServer(archive, bin); err != nil {
		t.Fatalf("extractServer: %v", err)
	}

	for _, name := range []string{"llama-server", "libggml.so"} {
		if _, err := os.Stat(filepath.Join(bin, name)); err != nil {
			t.Fatalf("%s не разложен: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(bin, "README.md")); !os.IsNotExist(err) {
		t.Fatal("в каталог попал посторонний файл")
	}

	info, err := os.Stat(filepath.Join(bin, "llama-server"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatal("сервер разложен без прав запуска")
	}
}
