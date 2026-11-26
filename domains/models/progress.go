package models

import "sync"

// progress — состояние одной загрузки для экрана. Done и Total в байтах:
// экран переводит их в гигабайты сам.
type progress struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

const (
	stateWaiting = "ждёт"
	stateRunning = "качается"
	stateDone    = "готово"
	stateFailed  = "ошибка"
)

// progressStore — прогресс загрузок. Живёт в памяти модуля: очередь хранит
// работу, а не её байты, поэтому после перезапуска работы повторятся, и
// прогресс начнётся заново с того, что уже лежит в .part.
type progressStore struct {
	mu    sync.Mutex
	items map[string]progress
	order []string
}

func newProgressStore() *progressStore {
	return &progressStore{items: map[string]progress{}}
}

// ensure заводит строку прогресса в состоянии «ждёт», сохраняя порядок
// постановки: экран показывает загрузки в том порядке, в каком их поставили.
func (s *progressStore) ensure(key, title string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.items[key]; !ok {
		s.order = append(s.order, key)
	}
	s.items[key] = progress{Key: key, Title: title, State: stateWaiting}
}

// update записывает ход загрузки.
func (s *progressStore) update(key string, done, total int64, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	item, ok := s.items[key]
	if !ok {
		return
	}
	item.Done = done
	item.Total = total
	item.State = state
	item.Error = ""
	s.items[key] = item
}

// fail помечает загрузку отказом: текст ошибки виден на экране, а повтор
// остаётся за очередь — она попробует ещё раз по правилу вида работы.
func (s *progressStore) fail(key string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	item, ok := s.items[key]
	if !ok {
		return
	}
	item.State = stateFailed
	item.Error = err.Error()
	s.items[key] = item
}

// reset очищает прошлые строки: новый запуск показывает только свои
// загрузки, а не «готово» от предыдущего.
func (s *progressStore) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.items = map[string]progress{}
	s.order = nil
}

// snapshot отдаёт строки в порядке постановки и признак «что-то ещё идёт».
// Признак нужен потоку обновлений: пока он ложен, соединение закрывается.
func (s *progressStore) snapshot() ([]progress, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	items := make([]progress, 0, len(s.order))
	active := false
	for _, key := range s.order {
		item, ok := s.items[key]
		if !ok {
			continue
		}
		items = append(items, item)
		if item.State == stateWaiting || item.State == stateRunning {
			active = true
		}
	}
	return items, active
}
