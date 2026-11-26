package models

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"tns/engine/config"
	"tns/engine/core"
	"tns/engine/jobs"
)

const (
	// kindModel — скачивание файла роли; kindServer — установка llama-server.
	kindModel  = jobs.Kind("models.download")
	kindServer = jobs.Kind("models.server")

	// serverKey — ключ живой загрузки сервера: повторное нажатие не ставит
	// вторую такую же работу.
	serverKey = "models/server"
)

// module — модуль моделей: показывает, что уже стоит, ставит загрузки
// работами очереди и отдаёт поток их прогресса. Прогресс живёт в памяти,
// потому что очередь хранит работу, а не её байты.
type module struct {
	cfg     Config
	catalog Catalog
	disp    *jobs.Dispatcher
	prog    *progressStore
	client  *http.Client
}

// New отдаёт модуль для точки сборки. settings — раздел
// modules.settings.models: файл каталога, каталог ролей и каталог сервера.
func New(disp *jobs.Dispatcher, settings json.RawMessage) (core.Module, error) {
	cfg := Config{}
	if err := config.DecodeSection(settings, &cfg); err != nil {
		return nil, fmt.Errorf("models: настройки: %w", err)
	}
	cfg = cfg.withDefaults()

	catalog, err := LoadCatalog(cfg.Catalog)
	if err != nil {
		return nil, fmt.Errorf("models: %w", err)
	}

	return module{
		cfg:     cfg,
		catalog: catalog,
		disp:    disp,
		prog:    newProgressStore(),
		// Общего срока у клиента нет: скачивание пяти гигабайт длится
		// дольше любого разумного таймаута, а предел ставит контекст работы.
		client: &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 30 * time.Second}},
	}, nil
}

func (module) Name() string { return "models" }

// Register объявляет маршруты и виды работ. Состояние читается, загрузки
// ставятся, прогресс уходит потоком: опрос не нужен.
func (m module) Register(r core.Registry) error {
	r.HTTP("GET", "/api/models", m.state)
	r.HTTP("POST", "/api/models/download", m.start)
	r.HTTPRaw("GET", "/api/models/events", http.HandlerFunc(m.events))

	// Загрузка идёт интерактивным классом: пока открыт поток прогресса,
	// процесс занят запросом, и фоновую работу разборщик не берёт. Модель
	// генерации при этом не занята, поэтому ходу загрузка не мешает.
	if err := r.Job(string(kindModel), jobs.Spec{
		Class:       jobs.ClassInteractive,
		Handler:     m.modelJob,
		MaxAttempts: 3,
		Backoff:     5 * time.Second,
	}); err != nil {
		return err
	}
	return r.Job(string(kindServer), jobs.Spec{
		Class:       jobs.ClassInteractive,
		Handler:     m.serverJob,
		MaxAttempts: 3,
		Backoff:     5 * time.Second,
	})
}

func (m module) state(*http.Request) (any, error) {
	snap := snapshotOf(m.cfg, m.catalog)
	items, active := m.prog.snapshot()
	snap.Downloads = downloadState{Items: items, Active: active}
	return snap, nil
}

// downloadRequest — выбор игрока: роль → вариант.
type downloadRequest struct {
	Selection map[string]string `json:"selection"`
}

// task — поставленная загрузка: ключ для прогресса, подпись для экрана и
// запрос в очередь.
type task struct {
	key     string
	title   string
	request jobs.Request
}

// modelTask — тело работы загрузки файла роли.
type modelTask struct {
	Role  string `json:"role"`
	ID    string `json:"id"`
	File  string `json:"file"`
	URL   string `json:"url"`
	SHA   string `json:"sha256"`
	Bytes int64  `json:"bytes"`
	Title string `json:"title"`
}

// serverTask — тело работы установки сервера.
type serverTask struct {
	URL   string `json:"url"`
	SHA   string `json:"sha256"`
	Title string `json:"title"`
}

// start проверяет выбор и ставит недостающее работами. Ни одна байта здесь
// не скачивается: постановка только ставит.
func (m module) start(r *http.Request) (any, error) {
	var req downloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, &core.Error{Status: http.StatusBadRequest, Message: "не разобрать выбор", Err: err}
	}

	tasks, err := m.tasks(req.Selection)
	if err != nil {
		return nil, err
	}

	if len(tasks) > 0 {
		m.prog.reset()
	}
	for _, t := range tasks {
		if _, err := m.disp.Enqueue(r.Context(), t.request); err != nil {
			return nil, fmt.Errorf("постановка %s: %w", t.title, err)
		}
		m.prog.ensure(t.key, t.title)
	}
	return map[string]int{"queued": len(tasks)}, nil
}

// tasks переводит выбор в список работ: сервер, если его нет, и файлы ролей,
// которых нет на диске. Уже установленное не ставится повторно.
func (m module) tasks(selection map[string]string) ([]task, error) {
	var out []task

	// Сервер ставится первым: без него моделям нечем работать.
	if !serverFound(m.cfg, m.catalog) {
		out = append(out, task{
			key:   serverKey,
			title: m.catalog.Server.Title,
			request: jobs.Request{
				Kind:    kindServer,
				Class:   jobs.ClassInteractive,
				Key:     serverKey,
				Payload: serverTask{URL: m.catalog.Server.Source, SHA: m.catalog.Server.SHA256, Title: m.catalog.Server.Title},
			},
		})
	}

	for _, slot := range m.catalog.Roles {
		id, ok := selection[string(slot.Role)]
		if !ok {
			continue
		}

		variant, found := findVariant(slot, id)
		if !found {
			return nil, &core.Error{
				Status:  http.StatusBadRequest,
				Message: fmt.Sprintf("роль %s: неизвестный вариант %q", slot.Role, id),
			}
		}
		if variantInstalled(m.cfg, slot.Role, variant.File) {
			continue
		}

		key := modelKey(slot.Role, variant.File)
		out = append(out, task{
			key:   key,
			title: variant.Title,
			request: jobs.Request{
				Kind:  kindModel,
				Class: jobs.ClassInteractive,
				Key:   key,
				Payload: modelTask{
					Role:  string(slot.Role),
					ID:    variant.ID,
					File:  variant.File,
					URL:   variant.Source,
					SHA:   variant.SHA256,
					Bytes: variant.Bytes,
					Title: variant.Title,
				},
			},
		})
	}
	return out, nil
}

func findVariant(slot Slot, id string) (Variant, bool) {
	for _, v := range slot.Variants {
		if v.ID == id {
			return v, true
		}
	}
	return Variant{}, false
}

// modelKey — ключ загрузки роли: по нему экран сопоставляет строку прогресса
// с файлом, а очередь не ставит второй раз тот же файл.
func modelKey(role Role, file string) string {
	return "models/" + string(role) + "/" + file
}

// modelJob скачивает файл роли и пишет указатель версии.
func (m module) modelJob(ctx context.Context, job jobs.Job) error {
	var task modelTask
	if err := json.Unmarshal(job.Payload, &task); err != nil {
		return jobs.Permanent(fmt.Errorf("models: тело работы: %w", err))
	}

	key := modelKey(Role(task.Role), task.File)
	dir := filepath.Join(m.cfg.Dir, task.Role)
	dest := filepath.Join(dir, task.File)

	err := fetch(ctx, m.client, task.URL, dest, task.SHA, func(done, total int64) {
		m.prog.update(key, done, total, runningState(done, total))
	})
	if err != nil {
		m.prog.fail(key, err)
		return err
	}

	// Прежний файл остаётся на месте копией отката: меняется только
	// указатель, а не имена файлов.
	previous := ""
	if old, ok := readManifest(dir); ok && old.File != task.File {
		previous = old.File
	}
	man := manifest{
		Role:        task.Role,
		ModelRef:    task.ID,
		Source:      task.URL,
		File:        task.File,
		SHA256:      task.SHA,
		Size:        task.Bytes,
		InstalledAt: time.Now().UTC(),
		Previous:    previous,
	}
	if err := writeManifest(dir, man); err != nil {
		m.prog.fail(key, err)
		return err
	}

	m.prog.update(key, task.Bytes, task.Bytes, stateDone)
	return nil
}

// serverJob ставит llama-server: архив релиза и его библиотеки.
func (m module) serverJob(ctx context.Context, job jobs.Job) error {
	var task serverTask
	if err := json.Unmarshal(job.Payload, &task); err != nil {
		return jobs.Permanent(fmt.Errorf("models: тело работы: %w", err))
	}

	// Последние байты запоминаются: архив удаляется сразу после распаковки,
	// а экран должен показать его размер, а не пустоту.
	var lastDone, lastTotal int64
	err := installServer(ctx, m.client, task.URL, task.SHA, m.cfg.Bin, func(done, total int64) {
		lastDone, lastTotal = done, total
		m.prog.update(serverKey, done, total, runningState(done, total))
	})
	if err != nil {
		m.prog.fail(serverKey, err)
		return err
	}

	m.prog.update(serverKey, lastDone, lastTotal, stateDone)
	return nil
}

// runningState отличает «качается» от «готово»: последнее сообщение
// прогресса приходит, когда байты уже все, и экран должен увидеть конец
// загрузки, даже если указатель версии пишется следом.
func runningState(done, total int64) string {
	if total > 0 && done == total {
		return stateDone
	}
	return stateRunning
}

// events отдаёт прогресс потоком: сервер присылает снимок по мере изменений
// и закрывает соединение, когда загрузок не осталось. Опрос не нужен.
func (m module) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "поток не поддержан", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	// send отдаёт снимок и отвечает, есть ли ещё что ждать.
	send := func() bool {
		items, active := m.prog.snapshot()
		body, err := json.Marshal(downloadState{Items: items, Active: active})
		if err != nil {
			return false
		}
		fmt.Fprintf(w, "data: %s\n\n", body)
		flusher.Flush()
		return active
	}

	// Пустой поток сразу закрывается: подписываться не на что.
	if !send() {
		return
	}

	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !send() {
				return
			}
		}
	}
}
