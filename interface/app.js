// Поток данных страницы.
//
// 1. Стейт создаётся синхронно: связь не проверена, снимок пуст.
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
// обновят: сначала прочитать эти пункты, потом проверять списки руками.
//
// Проверено на сборке wex 3.1.1 из interface/vendor/wex.min.js
// (sha256 06a13649…30f4; файл побайтно совпадает с dist форка sergDash/wex,
// коммит a584ca0; в апстриме wemmbley/wex ветка 3.x, коммит c3926b2 —
// поведение в перечисленных местах то же).
//
// Что перепроверять после обновления:
// 1. w-on подставляет элемент, на котором нашёлся обработчик, в
//    event.currentTarget. На этом стоят все pick в компонентах.
// 2. Хуки $init/$mount/$unmount принимают одноуровневую функцию
//    .on('$mount', function (state) {...}), в this — вью экземпляра.
//    Двухуровневая форма (return function () {}) у хуков молчит; поэтому
//    watchOutside и висит на первом toggle, а не на $mount.
// 3. Пропс кладётся в состояние по ссылке, поэтому массив из :items общий
//    с родителем; ключ, которого нет в состоянии компонента, отбрасывается
//    молча — опечатка в имени пропса ничего не сообщает.
// 4. Значения пунктов проходят через data-value и возвращаются строкой,
//    хотя в items они могут быть числами.
//
// Быстрая проверка после обновления: открыть страницу, по очереди открыть
// все списки, выбрать пункт в каждом, кликнуть мимо. Подпись выбранного
// должна меняться, в консоли — ни одной ошибки; в инструментах разработчика
// видно, что на документе столько же click-слушателей, сколько было.
$(function () {
    var colors = ['красный', 'зелёный', 'синий', 'жёлтый'];
    var cities = ['Москва', 'Санкт-Петербург', 'Казань', 'Новосибирск', 'Екатеринбург'];
    var tags = ['гость', 'подписчик', 'автор', 'редактор'];

    // options переводит строки в пункты списка: компоненту нужны значение
    // и подпись, даже когда они совпадают.
    function options(list) {
        var out = [];

        for (var i = 0; i < list.length; i++) {
            out.push({ value: list[i], label: list[i] });
        }
        return out;
    }

    // filterItems отбирает пункты по строке без учёта регистра.
    function filterItems(items, query) {
        var needle = query.trim().toLowerCase();
        var out = [];

        for (var i = 0; i < items.length; i++) {
            if (items[i].label.toLowerCase().indexOf(needle) >= 0) {
                out.push(items[i]);
            }
        }
        return out;
    }

    // watchOutside закрывает список компонента кликом мимо своего окна:
    // клик по другому списку — старый или компонент — тоже считается мимо,
    // поэтому одновременно открыт только один.
    //
    // Аудит wex 2026-11-26: слушатель вешается один раз за жизнь страницы и
    // не снимается — removeEventListener в этом файле не встречается. Для
    // статичной страницы это нормально. Если компонент окажется внутри w-if
    // или w-for и начнёт пересоздаваться, слушатели накопятся вместе
    // с замыканием на view и state: на документе сейчас по одному click
    // на каждый открытый компонент плюс один от страницы. Не переделка,
    // а напоминание: после обновления wex подписку уместно перенести
    // в $mount, а снятие — в $unmount (форма хука одноуровневая, в this —
    // вью экземпляра). WeakSet остаётся страховкой от повторной подписки
    // на каждом toggle.
    var watched = new WeakSet();

    function watchOutside(view, state) {
        if (watched.has(state)) {
            return;
        }
        watched.add(state);
        document.addEventListener('click', function (event) {
            var root = view.find('.select')[0];

            if (!root || !root.contains(event.target)) {
                state.open = false;
            }
        });
    }

    var view = $('#app').state({
        checking: true,
        ok: false,
        status: 'проверяем связь с сервером…',
        version: '',
        models: { engine: { found: false, title: '', path: '' }, slots: [], disk: { freeBytes: 0 } },
        plan: { bytes: 0, text: '—', ramGb: 0, ramText: '—', enough: true },
        diskText: '—',
        selects: {
            color: { open: false, value: 'зелёный', label: 'зелёный' },
            city: { open: false, value: '', label: '— город —', query: '' },
            tags: { open: false, value: [] }
        },
        colors: colors,
        colorItems: options(colors),
        componentColor: '',
        cityItems: options(cities),
        componentCity: '',
        tagItems: options(tags),
        componentTags: [],
        cities: cities,
        cityMatches: cities.slice(),
        tags: tags,
        failure: ''
    });

    // x-select — выпадающий список как компонент: разметка одна на все
    // экземпляры, состояние у каждого своё, выбранное значение уходит
    // родителю событием change. Внешняя функция метода вызывается с вью
    // в this, поэтому emit отправляется через сохранённую ссылку.
    //
    // Аудит wex 2026-11-26: pick читает event.currentTarget.dataset.value.
    // Это работает потому, что w-on сам подставляет найденный элемент в
    // currentTarget — поведение ядра при делегировании, а не правило из книги.
    // Если после обновления wex семантика события изменится (например, в this
    // окажется вью, а элемент придётся брать отдельно), этот pick и два
    // однотипных ниже отдадут undefined молча, без ошибки в консоли.
    // Проверка после обновления: открыть список, выбрать пункт, убедиться, что
    // подпись в списке сменилась и наружу ушло значение.
    // Запись, не зависящая от этой подстановки: event.target.closest('[data-value]').
    // Значения проходят через data-value, то есть через DOM-атрибут, и
    // возвращаются строкой. Пока в items строки (цвета, города, теги) —
    // сходится; с числовыми значениями подсветка value === item.value в
    // шаблоне перестанет совпадать.
    var xSelect = $('#x-select')
        .state(function () {
            return { items: [], value: '', open: false };
        })
        .method('toggle', function (state) {
            watchOutside(this, state);

            return function () {
                state.open = !state.open;
            };
        })
        .method('pick', function (state) {
            var component = this;

            return function (event) {
                state.value = event.currentTarget.dataset.value;
                state.open = false;
                component.emit('change', state.value);
            };
        })

    view.component('x-select', xSelect);

    // x-select-search — список с поиском: пункты фильтруются по вводу,
    // поле получает фокус при открытии.
    //
    // Аудит wex 2026-11-26: matches — производное состояние, оно считается
    // только при открытии и при вводе. Если родитель подменит :items, пока
    // меню открыто, список продолжит показывать старые пункты до закрытия и
    // повторного открытия (проверено). Сейчас items статические, поэтому не
    // проявляется. Когда список станет обновляемым из сети, поле matches
    // придётся либо пересчитывать по $change (следить за items и query), либо
    // убрать, а фильтровать прямо в шаблоне:
    // w-for="item in items | byQuery:query traceby item.value" — фильтры в
    // выражении w-for работают.
    // Имя метода filter совпадает с именем шаблонного view.filter(); здесь это
    // действие для w-on, а не фильтр шаблона — при чтении не путать.
    // Аудит wex 2026-11-26: w-focus объявлена ещё и на вью страницы (ниже,
    // перед $mount). Директивы не наследуются между вью, поэтому копия нужна;
    // но два одинаковых объявления разъедутся при правке. Если появится третий
    // список, регистрировать глобально: Wex.directive('w-focus', ...).
    var xSelectSearch = $('#x-select-search')
        .state(function () {
            return { items: [], matches: [], value: '', open: false, query: '' };
        })
        .directive('w-focus', null, function ($el) {
            $el[0].focus();
        })
        .method('toggle', function (state) {
            watchOutside(this, state);

            return function () {
                state.open = !state.open;
                // Ввод не стирается: при повторном открытии список
                // пересобирается по тому, что уже набрано.
                if (state.open) {
                    state.matches = filterItems(state.items, state.query);
                }
            };
        })
        .method('filter', function (state) {
            return function (event) {
                state.matches = filterItems(state.items, event.currentTarget.value);
            };
        })
        .method('pick', function (state) {
            var component = this;

            return function (event) {
                state.value = event.currentTarget.dataset.value;
                state.open = false;
                component.emit('change', state.value);
            };
        })

    view.component('x-select-search', xSelectSearch);

    // x-select-multi — множественный выбор: пункт переключается, список
    // остаётся открытым, наружу уходит массив значений.
    //
    // Аудит wex 2026-11-26: шаблон обращается с value как с массивом
    // (value.join, value.includes). Если в :value придёт не массив, выражения
    // шаблона будут падать на каждом рендере. Переключение пункта — тот же
    // алгоритм, что в pickSelect по ветке name === 'tags' ниже: править нужно
    // оба места, иначе разъедутся. Значение пункта, как и в двух списках выше,
    // приходит строкой из data-value.
    var xSelectMulti = $('#x-select-multi')
        .state(function () {
            return { items: [], value: [], open: false };
        })
        .method('toggle', function (state) {
            watchOutside(this, state);

            return function () {
                state.open = !state.open;
            };
        })
        .method('pick', function (state) {
            var component = this;

            return function (event) {
                var chosen = event.currentTarget.dataset.value;
                var next = [];
                var found = false;

                for (var i = 0; i < state.value.length; i++) {
                    if (state.value[i] === chosen) {
                        found = true;
                    } else {
                        next.push(state.value[i]);
                    }
                }
                if (!found) {
                    next.push(chosen);
                }

                state.value = next;
                component.emit('change', next);
            };
        })

    view.component('x-select-multi', xSelectMulti);

    // onComponentColor принимает выбранное от компонента: значение приходит
    // аргументом события, а не через DOM.
    view.method('onComponentColor', function (state) {
        return function (value) {
            state.componentColor = value;
        };
    });

    view.method('onComponentCity', function (state) {
        return function (value) {
            state.componentCity = value;
        };
    });

    view.method('onComponentTags', function (state) {
        return function (value) {
            state.componentTags = value;
        };
    });

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

    // humanBytes показывает размер так, как его читают в каталоге: гигабайты
    // с одним знаком, остальное целыми мегабайтами. Запятая — как в тексте.
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
        return { engine: data.engine, slots: slots, disk: data.disk };
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
        // Архив станка сервер ещё не мерил: его размер уточняется при
        // скачивании, поэтому в итоге он назван словом, а не числом.
        if (!state.models.engine.found) {
            parts.push('станок');
        }

        state.plan.bytes = bytes;
        state.plan.text = parts.length ? parts.join(' + ') : 'всё уже на месте';
        state.plan.ramGb = resident + generator;
        state.plan.ramText = humanRAM(resident + generator);
        state.plan.enough = state.models.disk.freeBytes >= bytes;
    }

    // snapshot читает состояние процесса: по нему видно, что сервер отвечает
    // и какая сборка запущена. Один запрос — одна перерисовка.
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

    // filterCities отбирает города по введённой строке. Значение читается
    // из самого поля: вместе с w-bind это дало бы гонку, а так список
    // пересобирается в том же обработчике, где получен ввод.
    view.method('filterCities', function (state) {
        return function (event) {
            var query = event.currentTarget.value.trim().toLowerCase();
            var matches = [];

            for (var i = 0; i < state.cities.length; i++) {
                if (state.cities[i].toLowerCase().indexOf(query) >= 0) {
                    matches.push(state.cities[i]);
                }
            }
            state.cityMatches = matches;
        };
    });

    // toggleSelect открывает выпадающий список и закрывает остальные:
    // одновременно открытым может быть только один.
    view.method('toggleSelect', function (state) {
        return function (event) {
            var name = event.currentTarget.dataset.select;
            var open = state.selects[name].open;

            for (var key in state.selects) {
                state.selects[key].open = false;
            }
            state.selects[name].open = !open;
        };
    });

    // pickSelect записывает выбор. Значение и подпись берутся из data-атрибутов
    // пункта: w-on не передаёт аргументов, обработчик получает только событие.
    // Множественный выбор не закрывает список и переключает пункт.
    view.method('pickSelect', function (state) {
        return function (event) {
            var item = event.currentTarget;
            var name = item.dataset.select;
            var value = item.dataset.value;
            var select = state.selects[name];

            if (name === 'tags') {
                var picked = select.value;
                var next = [];
                var found = false;

                for (var i = 0; i < picked.length; i++) {
                    if (picked[i] === value) {
                        found = true;
                    } else {
                        next.push(picked[i]);
                    }
                }
                if (!found) {
                    next.push(value);
                }

                // Новый массив вместо splice: прокси видит замену значения,
                // и снятые пункты теряют подсветку.
                select.value = next;
                return;
            }

            select.value = value;
            select.label = item.textContent.trim();
            select.open = false;
        };
    });

    // refresh перечитывает состояние по кнопке: опроса нет.
    view.method('refresh', function (state) {
        return function () {
            snapshot(state);
            loadModels(state);
        };
    });

    // w-focus ставит курсор в поле, когда оно появляется: поиск готов
    // к вводу сразу после открытия списка.
    // Аудит wex 2026-11-26: второе объявление той же директивы — на вью
    // компонента x-select-search выше. Директивы между вью не наследуются,
    // поэтому копия нужна, но две реализации одного поведения при правке
    // разъедутся; при появлении третьего списка вынести в Wex.directive().
    view.directive('w-focus', null, function ($el) {
        $el[0].focus();
    });

    // Первый снимок читается сразу после появления страницы: $mount —
    // документированное место для начальной загрузки данных.
    //
    // Аудит wex 2026-11-26: здесь форма хука правильная и требует проверки
    // после обновления: обработчик — одна функция, state первым аргументом,
    // this — вью. Внешняя функция, возвращающая function () {}, у хуков не
    // вызывается (см. список в начале файла, пункт 2): у компонентов из-за
    // этого watchOutside живёт на toggle, а не на $mount.
    view.on('$mount', function (state) {
        snapshot(state);
        loadModels(state);
    });

    // Клик вне списка закрывает открытые. Слушатель на документе, потому что
    // списки разбросаны по странице; клик внутри списка пропускается,
    // иначе он закрыл бы тот список, в котором выбирают.
    // Аудит wex 2026-11-26: этот слушатель не снимается: страница живёт до
    // перезагрузки, поэтому вреда нет. Вместе с тремя компонентными
    // (watchOutside) на документе висит по одному click на каждый открытый
    // компонент — после обновления wex это первое, что стоит пересчитать.
    document.addEventListener('click', function (event) {
        var inSelect = event.target.closest('.select');
        var inComponent = event.target.closest('x-select, x-select-search, x-select-multi');

        if (inSelect && !inComponent) {
            return;
        }
        view.update(function (state) {
            for (var key in state.selects) {
                state.selects[key].open = false;
            }
        });
    });

    view.mount();
});
