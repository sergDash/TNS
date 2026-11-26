// Поток данных страницы.
//
// 1. Стейт создаётся синхронно: связь не проверена, каталог ещё не прочитан.
// 2. mount() вставляет шаблон #app в DOM и подписывает его на стейт.
// 3. call() — единственное место разбора конверта: сначала error, затем
//    result, как в Go сначала проверяют err, а потом берут значение.
// 4. Ответ ложится в поля стейта, и wex перерисовывает изменившиеся узлы.
//    Обновление идёт по кнопке, без опроса: канал обновлений — шаг
//    «Поток в интерфейсе», он ещё не сделан.
//
// --- Аудит wex, 2026-11-26 ---
//
// Помеченные ниже места («Аудит wex») опираются на поведение ядра wex,
// а не на договорённость из книги. Это НЕ задание на переделку: сейчас всё
// работает, код остаётся как есть. Это список для перепроверки, если wex
// обновят: сначала прочитать эти пункты, потом проверять экран руками.
//
// Проверено на сборке wex 3.1.1 из interface/vendor/wex.min.js
// (sha256 06a13649…30f4; файл побайтно совпадает с dist форка sergDash/wex,
// коммит a584ca0; в апстриме wemmbley/wex ветка 3.x, коммит c3926b2 —
// поведение в перечисленных местах то же).
//
// Что перепроверять после обновления:
// 1. w-on подставляет элемент, на котором нашёлся обработчик, в
//    event.currentTarget. На этом стоит выбор роли и варианта: обработчики
//    читают data-role и data-variant из currentTarget.
// 2. Хуки $init/$mount/$unmount принимают одноуровневую функцию
//    .on('$mount', function (state) {...}), в this — вью экземпляра.
//    Двухуровневая форма (return function () {}) у хуков молчит.
// 3. Значения из data-* приходят строками, даже если в данных были числа.
//
// Быстрая проверка после обновления: открыть страницу, дождаться карточек,
// переключить вариант смысла и выключить роль — итог под карточками должен
// меняться, в консоли — ни одной ошибки.
$(function () {
    var view = $('#app').state({
        checking: true,
        ok: false,
        status: 'проверяем связь с сервером…',
        version: '',
        models: { server: { found: false, title: '', path: '' }, slots: [], disk: { freeBytes: 0 } },
        plan: { bytes: 0, text: '—', ramGb: 0, ramText: '—', enough: true },
        diskText: '—',
        failure: ''
    });

    // humanBytes показывает размер так, как его читают в каталоге: гигабайты
    // и терабайты с одним знаком, остальное целыми мегабайтами. Запятая —
    // как в тексте.
    function humanBytes(n) {
        if (!n || n <= 0) {
            return '—';
        }
        if (n >= 1000000000000) {
            return (n / 1000000000000).toFixed(1).replace('.', ',') + ' ТБ';
        }
        if (n >= 1000000000) {
            return (n / 1000000000).toFixed(1).replace('.', ',') + ' ГБ';
        }
        return Math.round(n / 1000000) + ' МБ';
    }

    // humanRAM — память при работе, целыми гигабайтами.
    function humanRAM(gb) {
        if (!gb) {
            return '—';
        }
        return gb + ' ГБ';
    }

    // prepareModels переводит ответ модуля в стейт: у роли появляется
    // включённость и выбранный вариант, у варианта — готовые строки размера
    // и памяти, чтобы шаблон ничего не считал. По умолчанию выбрана
    // установленная модель, а если её нет — первый вариант каталога: он
    // самый экономный по памяти.
    function prepareModels(data) {
        var slots = [];

        for (var i = 0; i < data.slots.length; i++) {
            var slot = data.slots[i];
            var variants = [];
            var installed = '';

            for (var j = 0; j < slot.variants.length; j++) {
                var v = slot.variants[j];

                variants.push({
                    id: v.id,
                    title: v.title,
                    bytes: v.bytes,
                    ramGb: v.ramGb,
                    installed: v.installed,
                    sizeText: (v.approx ? '≈ ' : '') + humanBytes(v.bytes),
                    ramText: humanRAM(v.ramGb)
                });
                if (v.installed && !installed) {
                    installed = v.id;
                }
            }

            slots.push({
                role: slot.role,
                title: slot.title,
                note: slot.note,
                enabled: true,
                chosen: installed || (variants.length ? variants[0].id : ''),
                variants: variants
            });
        }
        return { server: data.server, slots: slots, disk: data.disk };
    }

    // recomputePlan считает итог по выбору: сколько предстоит скачать и
    // сколько памяти займёт работа. Смысл живёт в памяти постоянно,
    // генераторы — по одному за раз, поэтому память считается как смысл
    // плюс старший из включённых генераторов.
    function recomputePlan(state) {
        var bytes = 0;
        var resident = 0;
        var generator = 0;

        for (var i = 0; i < state.models.slots.length; i++) {
            var slot = state.models.slots[i];
            if (!slot.enabled) {
                continue;
            }

            var chosen = null;
            for (var j = 0; j < slot.variants.length; j++) {
                if (slot.variants[j].id === slot.chosen) {
                    chosen = slot.variants[j];
                    break;
                }
            }
            if (!chosen) {
                continue;
            }

            if (!chosen.installed) {
                bytes += chosen.bytes;
            }
            if (slot.role === 'embed') {
                resident = chosen.ramGb;
            } else if (chosen.ramGb > generator) {
                generator = chosen.ramGb;
            }
        }

        var parts = [];
        if (bytes > 0) {
            parts.push(humanBytes(bytes));
        }
        // Размер архива сервера ещё неизвестен: он уточняется при
        // скачивании, поэтому в итоге он назван словом, а не числом.
        if (!state.models.server.found) {
            parts.push('сервер');
        }

        state.plan.bytes = bytes;
        state.plan.text = parts.length ? parts.join(' + ') : 'всё уже на месте';
        state.plan.ramGb = resident + generator;
        state.plan.ramText = humanRAM(resident + generator);
        state.plan.enough = state.models.disk.freeBytes >= bytes;
    }

    // call разбирает конверт и отдаёт только result; ошибка становится
    // исключением, чтобы её ловил один общий catch на каждый запрос.
    function call(url, options) {
        return fetch(url, options)
            .then(function (response) {
                return response.json();
            })
            .then(function (body) {
                const { result, error, data } = body;

                if (error) {
                    // Данные ошибки кладутся в объект исключения: по ним форма
                    // показывает, какие поля исправить, а не только текст.
                    const failure = new Error(error);
                    failure.data = data;
                    throw failure;
                }
                return result;
            });
    }

    // snapshot читает состояние процесса: по нему видно, что сервер отвечает
    // и какая сборка запущена.
    function snapshot(state) {
        state.checking = true;
        state.status = 'проверяем связь с сервером…';

        call('/api/health')
            .then(function (health) {
                state.checking = false;
                state.ok = health.ok;
                state.status = health.status;
                state.version = health.version;
                state.failure = '';
            })
            .catch(function (error) {
                state.checking = false;
                state.ok = false;
                state.status = 'сервер не ответил: ' + error.message;
                state.failure = error.message;
            });
    }

    // loadModels читает каталог и состояние установки: что уже на месте,
    // что предстоит скачать и сколько для этого нужно места.
    function loadModels(state) {
        return call('/api/models')
            .then(function (data) {
                state.models = prepareModels(data);
                state.diskText = humanBytes(data.disk.freeBytes);
                recomputePlan(state);
            })
            .catch(function (error) {
                state.failure = error.message;
            });
    }

    // check повторяет тот запрос, которым страница проверяет связь. Кнопка
    // нужна не для данных, а чтобы запрос было видно в инструментах
    // веб-разработчика: нажатие оставляет одну строку в сетевой панели.
    view.method('check', function (state) {
        return function () {
            snapshot(state);
        };
    });

    // refresh перечитывает состояние по кнопке: опроса нет.
    view.method('refresh', function (state) {
        return function () {
            snapshot(state);
            loadModels(state);
        };
    });

    // toggleRole включает и выключает роль кликом по её заголовку: выбор
    // того, что ставить, — это и есть отметка в карточке.
    view.method('toggleRole', function (state) {
        return function (event) {
            var role = event.currentTarget.dataset.role;

            for (var i = 0; i < state.models.slots.length; i++) {
                if (state.models.slots[i].role === role) {
                    state.models.slots[i].enabled = !state.models.slots[i].enabled;
                    break;
                }
            }
            recomputePlan(state);
        };
    });

    // chooseVariant выбирает вариант роли и включает роль: выбрать вариант
    // значит согласиться ставить. w-on аргументов не передаёт, поэтому роль
    // и вариант берутся из data-атрибутов строки.
    view.method('chooseVariant', function (state) {
        return function (event) {
            var role = event.currentTarget.dataset.role;
            var variant = event.currentTarget.dataset.variant;

            for (var i = 0; i < state.models.slots.length; i++) {
                if (state.models.slots[i].role === role) {
                    state.models.slots[i].chosen = variant;
                    state.models.slots[i].enabled = true;
                    break;
                }
            }
            recomputePlan(state);
        };
    });

    // Первый снимок читается сразу после появления страницы: $mount —
    // документированное место для начальной загрузки данных.
    //
    // Аудит wex 2026-11-26: здесь форма хука правильная и требует проверки
    // после обновления: обработчик — одна функция, state первым аргументом,
    // this — вью.
    view.on('$mount', function (state) {
        snapshot(state);
        loadModels(state);
    });

    view.mount();
});
