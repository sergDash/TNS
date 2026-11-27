// Поток данных страницы.
//
// 1. Стейт создаётся синхронно: каталог ещё не прочитан.
// 2. mount() вставляет шаблон #app в DOM и подписывает его на стейт.
// 3. call() — единственное место разбора конверта: сначала error, затем
//    result, как в Go сначала проверяют err, а потом берут значение.
// 4. Ответ ложится в поля стейта, и wex перерисовывает изменившиеся узлы.
// 5. Прогресс загрузок приходит потоком: опроса нет.
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
// 4. Выражение внутри атрибута разметки подставляется как текст: ширина
//    полосы прогресса задаётся style="width: {{ percent }}%".
//
// Быстрая проверка после обновления: открыть страницу, дождаться карточек,
// переключить вариант смысла и выключить роль — итог должен меняться;
// затем начать скачивание и убедиться, что полосы двигаются без опроса.
$(function () {
    // events — открытый поток прогресса. Объект живёт вне стейта: это
    // транспорт, а не данные страницы.
    var events = null;

    var view = $('#app').state({
        // storage — значения, которые wex держит в localStorage: вкладка и
        // открытая форма мира переживают обновление страницы.
        storage: { step: 'models', form: false },
        // nav — куда сейчас можно попасть. Верхние пункты и нижние кнопки
        // читают одни и те же поля, поэтому не могут разойтись: доступный
        // шаг и нажимается, и ведёт.
        nav: { models: true, world: false, hero: false },
        models: { server: { found: false, title: '', path: '' }, slots: [], disk: { freeBytes: 0 } },
        plan: { bytes: 0, text: '—', status: '—', ramGb: 0, ramText: '—', enough: true, paths: [] },
        world: {
            genres: [], orders: [], ratings: [], modes: [],
            title: '', slug: '', synopsis: '',
            socialOrder: '', censorship: '', travelMode: '',
            errors: {},
            // active — настройки открытого мира (null, когда мира нет).
            // Показывать ли форму нового мира — в storage.form: это режим
            // экрана, и он переживает обновление страницы.
            active: null
        },
        hero: { title: '', note: '', fields: [] },
        progress: [],
        downloads: { active: false, items: [] },
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
        if (n >= 1000000) {
            return Math.round(n / 1000000) + ' МБ';
        }
        if (n >= 1000) {
            return Math.round(n / 1000) + ' КБ';
        }
        return n + ' Б';
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
                    key: v.key,
                    path: v.path,
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
        return { ready: data.ready, server: data.server, slots: slots, disk: data.disk };
    }

    // progressRow — одна строка полосы. Живой прогресс главнее: если по
    // ключу пришли байты, показывается он. Иначе установленное — «готово»
    // с полной полосой, а недостающее — прочерк: блок не прыгает и с
    // первого экрана видно, где что будет.
    function progressRow(title, key, installed, bytes, live) {
        var item = live[key];

        if (item) {
            // «Готово» рисуется полной полосой даже когда размер неизвестен
            // (у сервера архив уже удалён, и его байтов не осталось).
            var percent = 0;
            var text = '—';

            if (item.state === 'готово') {
                percent = 100;
            } else if (item.total > 0) {
                percent = Math.round(item.done * 100 / item.total);
            }
            if (item.total > 0) {
                text = humanBytes(item.done) + ' из ' + humanBytes(item.total);
            }
            return {
                key: key,
                title: title,
                state: item.state,
                error: item.error || '',
                percent: percent,
                progressText: text
            };
        }

        if (installed) {
            return {
                key: key,
                title: title,
                state: 'готово',
                error: '',
                percent: 100,
                progressText: bytes > 0 ? humanBytes(bytes) : '—'
            };
        }
        return { key: key, title: title, state: '—', error: '', percent: 0, progressText: '—' };
    }

    // renderProgress собирает строки под текущий выбор: сервер первой
    // строкой, затем выбранные роли в порядке каталога. Список постоянный,
    // поэтому полосы не появляются и не исчезают при выборе и загрузке.
    function renderProgress(state) {
        var live = {};
        var rows = [];
        var i;

        for (i = 0; i < state.progress.length; i++) {
            live[state.progress[i].key] = state.progress[i];
        }

        rows.push(progressRow(state.models.server.title, state.models.server.key,
            state.models.server.found, state.models.server.bytes, live));

        for (i = 0; i < state.models.slots.length; i++) {
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
            rows.push(progressRow(chosen.title, chosen.key, chosen.installed, chosen.bytes, live));
        }

        state.downloads = { active: state.downloads.active, items: rows };
    }

    // pickDefault берёт желаемое значение из списка, а если его там нет —
    // первое: пустой выбор на экране выглядел бы как «ничего не выбрано».
    function pickDefault(list, want) {
        for (var i = 0; i < list.length; i++) {
            if (list[i] === want) {
                return want;
            }
        }
        return list.length ? list[0] : '';
    }

    // chips переводит список в строки выбора: подпись и признак «выбран».
    function chips(list, chosen) {
        var out = [];

        for (var i = 0; i < list.length; i++) {
            out.push({ title: list[i], on: list[i] === chosen });
        }
        return out;
    }

    // prepareWorld строит состояние анкеты из перечней модуля: списки
    // приходят данными, а умолчания выставлены здесь, чтобы форма сразу
    // была заполнена и её можно было отправить. Миры приходят карточками;
    // открытый мир ищется среди них по пометке.
    function prepareWorld(data) {
        var genre = pickDefault(data.genres, 'фэнтези');
        var order = pickDefault(data.socialOrders, 'феодализм');
        var mode = pickDefault(data.travelModes, 'вымышленная');
        var rating = pickDefault(data.ratings, '16+');

        var genres = [];
        for (var i = 0; i < data.genres.length; i++) {
            genres.push({ title: data.genres[i], on: data.genres[i] === genre });
        }

        var list = [];
        var active = null;
        var source = data.worlds || [];
        for (var j = 0; j < source.length; j++) {
            var card = prepareCard(source[j]);
            list.push(card);
            if (card.active) {
                active = card;
            }
        }

        return {
            genres: genres,
            orders: chips(data.socialOrders, order),
            modes: chips(data.travelModes, mode),
            ratings: chips(data.ratings, rating),
            title: '', slug: '', synopsis: '',
            socialOrder: order, censorship: rating, travelMode: mode,
            errors: emptyErrors(),
            list: list,
            active: active
        };
    }

    // prepareCard переводит настройки мира в вид для карточки: строки
    // сведений собираются здесь, шаблон только выводит их. Так же мир
    // выглядел в старой версии: имя, сведения, состояние героя и действия.
    function prepareCard(card) {
        var meta = [];
        if (card.created_at) {
            meta.push('рождён ' + formatDate(card.created_at));
        }
        meta.push(card.social_order);
        meta.push('карта: ' + card.travel_mode);
        meta.push(card.censorship);

        var heroText = 'Герой не рождён';
        var bornText = '';
        if (card.hero) {
            heroText = card.hero_name ? 'Герой «' + card.hero_name + '»' : 'Герой рождён';
            bornText = card.hero_name ? 'Герой «' + card.hero_name + '» рождён' : 'Герой рождён';
        }

        return {
            slug: card.slug,
            title: card.title,
            synopsis: card.synopsis,
            genres: card.genres || [],
            meta: meta.join(' · '),
            hero: !!card.hero,
            heroText: heroText,
            bornText: bornText,
            active: !!card.active
        };
    }

    // formatDate показывает время рождения мира по-человечески: день месяц
    // год. Время суток в карточке не нужно.
    function formatDate(iso) {
        var d = new Date(iso);
        if (isNaN(d.getTime())) {
            return '';
        }
        return d.getDate() + '.' + (d.getMonth() + 1) + '.' + d.getFullYear();
    }

    // emptyErrors — полный набор полей анкеты с пустыми причинами. Строка
    // причины на экране есть всегда, поэтому ключи должны существовать:
    // иначе шаблон выведет «undefined» вместо пустоты.
    function emptyErrors() {
        return {
            title: '', slug: '', synopsis: '',
            genres: '', social_order: '', travel_mode: '', censorship: ''
        };
    }

    // prepareHero переводит форму героя из конфига в стейт: у поля есть
    // значение и причина отказа, шаблон только выводит их. Состав полей
    // задаётся конфигом, поэтому код про них ничего не знает.
    function prepareHero(hero) {
        var fields = [];
        var source = hero.fields || [];

        for (var i = 0; i < source.length; i++) {
            fields.push({
                id: source[i].id,
                title: source[i].title,
                hint: source[i].hint,
                type: source[i].type,
                options: source[i].options || [],
                value: source[i].default || '',
                default: source[i].default || '',
                error: ''
            });
        }
        return { title: hero.title || '', note: hero.note || '', fields: fields };
    }

    // recomputeNav отмечает, куда сейчас можно попасть: «Мир» — когда всё
    // скачано (то же условие, что у кнопки «Далее»), «Герой» — когда есть
    // открытый мир. Верхние пункты и нижние кнопки читают эти поля, поэтому
    // разойтись между собой не могут.
    function recomputeNav(state) {
        state.nav.world = !!state.models.ready;
        state.nav.hero = !!state.world.active;
    }

    // clampStep возвращает вкладку туда, куда можно попасть. Сохранённый шаг
    // мог остаться от прошлой сессии: модели с тех пор не скачаны или мир
    // удалён. Без сверки страница открыла бы шаг, которого ещё нет.
    function clampStep(state) {
        var step = state.storage.step;

        if (step !== 'models' && step !== 'world' && step !== 'hero') {
            step = 'models';
        }
        if (step === 'hero' && !state.nav.hero) {
            step = state.nav.world ? 'world' : 'models';
        }
        if (step === 'world' && !state.nav.world) {
            step = 'models';
        }
        state.storage.step = step;
    }

    // resetHero возвращает форму героя к умолчаниям схемы: герой относится
    // к миру, поэтому у нового мира он не наследуется от прошлого.
    function resetHero(state) {
        for (var i = 0; i < state.hero.fields.length; i++) {
            state.hero.fields[i].value = state.hero.fields[i].default;
            state.hero.fields[i].error = '';
        }
    }

    // heroField находит поле формы героя по имени.
    function heroField(state, id) {
        for (var i = 0; i < state.hero.fields.length; i++) {
            if (state.hero.fields[i].id === id) {
                return state.hero.fields[i];
            }
        }
        return null;
    }

    // applyHeroErrors раскладывает отказ модуля по полям формы: причина
    // встаёт под своим вводом, а не одной строкой сверху.
    function applyHeroErrors(state, error) {
        if (!error.data || !error.data.fields) {
            return;
        }
        for (var i = 0; i < error.data.fields.length; i++) {
            var field = heroField(state, error.data.fields[i].field);
            if (field) {
                field.error = error.data.fields[i].reason;
            }
        }
    }

    // fieldErrors переводит отказ модуля в подсветку полей: сервер называет
    // поле и причину, форма показывает причину под нужным вводом.
    function fieldErrors(error) {
        var out = emptyErrors();

        if (!error.data || !error.data.fields) {
            return out;
        }
        for (var i = 0; i < error.data.fields.length; i++) {
            out[error.data.fields[i].field] = error.data.fields[i].reason;
        }
        return out;
    }

    // pickedGenres собирает отмеченные жанры в том порядке, в каком они
    // стоят в списке: порядок не зависит от того, что нажимали первым.
    function pickedGenres(state) {
        var out = [];

        for (var i = 0; i < state.world.genres.length; i++) {
            if (state.world.genres[i].on) {
                out.push(state.world.genres[i].title);
            }
        }
        return out;
    }

    // recomputePlan считает итог по выбору: сколько предстоит скачать и
    // сколько памяти займёт работа. Понимание живёт в памяти постоянно,
    // а писать может только одна модель за раз, поэтому память считается
    // как понимание плюс старший из включённых вариантов.
    function recomputePlan(state) {
        var bytes = 0;
        var resident = 0;
        var generator = 0;
        var paths = [];

        // Сервер идёт первым: он ложится отдельно от моделей, и по списку
        // путей видно, куда именно. Показывается и установленный: адрес
        // от этого не меняется, а блок должен быть на месте всегда.
        paths.push({ title: 'Сервер', path: state.models.server.path });

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
            // Путь показывается всегда: блок «Куда» — это адрес файла,
            // а не список того, что осталось скачать.
            paths.push({ title: slot.title, path: chosen.path });
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
        state.plan.status = parts.length ? 'скачать ' + parts.join(' + ') : 'всё на месте';
        state.plan.ramGb = resident + generator;
        state.plan.ramText = humanRAM(resident + generator);
        state.plan.enough = state.models.disk.freeBytes >= bytes;
        state.plan.paths = paths;

        renderProgress(state);
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

    // loadModels читает каталог и состояние установки: что уже на месте,
    // что предстоит скачать и сколько для этого нужно места.
    function loadModels(state) {
        return call('/api/models')
            .then(function (data) {
                state.models = prepareModels(data);
                state.diskText = humanBytes(data.disk.freeBytes);
                state.progress = data.downloads.items;
                state.downloads = { active: data.downloads.active, items: [] };
                recomputePlan(state);
                recomputeNav(state);

                // Загрузка, начатая до перезагрузки страницы, продолжается:
                // поток подключается к ней сразу, а не ждёт новой кнопки.
                if (state.downloads.active) {
                    openEvents(state);
                }
            })
            .catch(function (error) {
                state.failure = error.message;
            });
    }

    // loadWorld читает перечни анкеты: жанры, строи, карты, рейтинги.
    function loadWorld(state) {
        return call('/api/world')
            .then(function (data) {
                state.world = prepareWorld(data);
                state.hero = prepareHero(data.hero || {});
                recomputeNav(state);
            })
            .catch(function (error) {
                state.failure = error.message;
            });
    }

    // openEvents подписывается на поток прогресса. Поток закрывается сам,
    // когда загрузок не осталось: тогда каталог перечитывается, и на экране
    // появляются отметки «уже на месте».
    function openEvents(state) {
        if (events) {
            return;
        }

        events = new EventSource('/api/models/events');
        events.onmessage = function (event) {
            var data = JSON.parse(event.data);

            state.progress = data.items;
            state.downloads.active = data.active;
            renderProgress(state);

            if (!data.active) {
                events.close();
                events = null;
                loadModels(state);
            }
        };
        events.onerror = function () {
            // Ошибка приходит и при собственном закрытии потока, поэтому
            // проверяется, что он всё ещё тот же.
            if (!events) {
                return;
            }
            events.close();
            events = null;
            state.downloads.active = false;
            state.failure = 'поток прогресса прерван';
        };
    }

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

    // goModels, goWorld, goHero переключают вкладку. Верхний пункт и нижняя
    // кнопка зовут один и тот же обработчик, поэтому нажатие ведёт туда,
    // куда обещает подпись. Доступность берётся из nav: недоступный шаг
    // не открывается, даже если клик всё-таки пришёл.
    view.method('goModels', function (state) {
        return function () {
            state.storage.step = 'models';
        };
    });

    view.method('goWorld', function (state) {
        return function () {
            if (!state.nav.world) {
                return;
            }
            state.storage.step = 'world';
        };
    });

    view.method('goHero', function (state) {
        return function () {
            if (!state.nav.hero) {
                return;
            }
            state.storage.step = 'hero';
        };
    });

    // switchWorld открывает другой мир: адрес уходит на сервер, а экран
    // получает свежие карточки. Герой прошлого мира к новому не относится,
    // поэтому его форма сбрасывается, а открытый шаг героя возвращается
    // к «Миру».
    view.method('switchWorld', function (state) {
        return function (event) {
            var slug = event.currentTarget.dataset.slug;
            if (!slug) {
                return;
            }
            if (state.world.active && state.world.active.slug === slug) {
                return;
            }
            state.failure = '';

            call('/api/world/' + encodeURIComponent(slug) + '/active', { method: 'POST' })
                .then(function (data) {
                    state.world = prepareWorld(data);
                    state.hero = prepareHero(data.hero || {});
                    recomputeNav(state);
                    if (state.storage.step === 'hero') {
                        state.storage.step = 'world';
                    }
                })
                .catch(function (error) {
                    state.failure = error.message;
                });
        };
    });

    // newWorld показывает форму нового мира вместо карточки. Открытый мир
    // при этом не теряется: он остаётся на диске, и после перезагрузки
    // страницы карточка вернётся. Форма героя сбрасывается: герой прошлого
    // мира к новому не относится.
    view.method('newWorld', function (state) {
        return function () {
            state.storage.form = true;
            state.world.title = '';
            state.world.slug = '';
            state.world.synopsis = '';
            state.world.errors = emptyErrors();
            resetHero(state);
        };
    });

    // cancelNew закрывает форму нового мира и возвращает карточку открытого:
    // введённое не сохраняется, мира ещё нет.
    view.method('cancelNew', function (state) {
        return function () {
            state.storage.form = false;
            state.world.title = '';
            state.world.slug = '';
            state.world.synopsis = '';
            state.world.errors = emptyErrors();
        };
    });

    // setAnswer запоминает ввод в текстовом поле героя. w-on не передаёт
    // значений, поэтому поле берётся из data-field, а текст — из элемента.
    view.method('setAnswer', function (state) {
        return function (event) {
            var field = heroField(state, event.currentTarget.dataset.field);
            if (!field) {
                return;
            }
            field.value = event.currentTarget.value;
            field.error = '';
        };
    });

    // pickAnswer выбирает вариант в поле-списке героя.
    view.method('pickAnswer', function (state) {
        return function (event) {
            var field = heroField(state, event.currentTarget.dataset.field);
            if (!field) {
                return;
            }
            field.value = event.currentTarget.dataset.value;
            field.error = '';
        };
    });

    // createHero отправляет ответы и рождает героя в базе мира. Мир берётся
    // из карточки открытого мира: он уже рождён, поэтому адрес известен.
    view.method('createHero', function (state) {
        return function () {
            if (!state.world.active) {
                return;
            }

            var answers = {};
            for (var i = 0; i < state.hero.fields.length; i++) {
                var field = state.hero.fields[i];
                answers[field.id] = field.value;
                field.error = '';
            }
            state.failure = '';

            call('/api/world/' + encodeURIComponent(state.world.active.slug) + '/hero', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ answers: answers })
            })
                .then(function () {
                    var name = answers.name || '';
                    if (state.world.active) {
                        state.world.active.hero = true;
                        state.world.active.heroText = name ? 'Герой «' + name + '»' : 'Герой рождён';
                        state.world.active.bornText = name ? 'Герой «' + name + '» рождён' : 'Герой рождён';
                    }
                })
                .catch(function (error) {
                    applyHeroErrors(state, error);
                    if (!error.data || !error.data.fields) {
                        state.failure = error.message;
                    }
                });
        };
    });

    // pickOne выбирает один пункт из списка: строй, карту или рейтинг.
    // w-on аргументов не передаёт, поэтому список и значение берутся из
    // data-атрибутов, а признак выбранности переставляется в стейте.
    view.method('pickOne', function (state) {
        return function (event) {
            var list = event.currentTarget.dataset.list;
            var value = event.currentTarget.dataset.value;
            var items = state.world[list];

            for (var i = 0; i < items.length; i++) {
                items[i].on = items[i].title === value;
            }
            if (list === 'orders') {
                state.world.socialOrder = value;
            } else if (list === 'ratings') {
                state.world.censorship = value;
            } else if (list === 'modes') {
                state.world.travelMode = value;
            }
        };
    });

    // toggleGenre отмечает и снимает жанр: их может быть несколько.
    view.method('toggleGenre', function (state) {
        return function (event) {
            var value = event.currentTarget.dataset.value;

            for (var i = 0; i < state.world.genres.length; i++) {
                if (state.world.genres[i].title === value) {
                    state.world.genres[i].on = !state.world.genres[i].on;
                    break;
                }
            }
        };
    });

    // createWorld отправляет анкету. Отказ приходит списком полей: форма
    // показывает причину под каждым, а не одну общую строку.
    view.method('createWorld', function (state) {
        return function () {
            state.world.errors = emptyErrors();
            state.failure = '';

            call('/api/world', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    title: state.world.title,
                    slug: state.world.slug,
                    synopsis: state.world.synopsis,
                    travel_mode: state.world.travelMode,
                    social_order: state.world.socialOrder,
                    censorship: state.world.censorship,
                    genres: pickedGenres(state)
                })
            })
                .then(function () {
                    // Мир рождён и уже открыт на сервере: экран перечитывает
                    // перечни и получает карточку мира вместо формы. Отдельного
                    // «создан» в стейте нет — открытый мир и есть результат.
                    state.storage.form = false;
                    return loadWorld(state);
                })
                .catch(function (error) {
                    var fields = fieldErrors(error);
                    state.world.errors = fields;

                    // Отказ по полям объясняется под вводами: общая плашка
                    // сверху только сдвинула бы форму. Плашка остаётся для
                    // отказов, у которых полей нет.
                    if (!error.data || !error.data.fields) {
                        state.failure = error.message;
                    }
                });
        };
    });

    // startDownload отправляет выбор и подписывается на прогресс. Ответ
    // приходит сразу: сервер только ставит работы, байты идут следом.
    view.method('startDownload', function (state) {
        return function () {
            var selection = {};

            for (var i = 0; i < state.models.slots.length; i++) {
                var slot = state.models.slots[i];
                if (slot.enabled) {
                    selection[slot.role] = slot.chosen;
                }
            }

            // Живой прогресс от прошлой загрузки не должен показываться
            // поверх нового выбора: до ответа сервера строки — прочерки.
            state.progress = [];
            state.downloads.active = true;
            state.failure = '';
            renderProgress(state);

            call('/api/models/download', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ selection: selection })
            })
                .then(function (data) {
                    if (!data.queued) {
                        // Ставить нечего: всё выбранное уже на диске.
                        state.downloads.active = false;
                        loadModels(state);
                        return;
                    }
                    openEvents(state);
                })
                .catch(function (error) {
                    state.downloads.active = false;
                    renderProgress(state);
                    state.failure = error.message;
                });
        };
    });

    // Первый снимок читается сразу после появления страницы: $mount —
    // документированное место для начальной загрузки данных.
    //
    // Аудит wex 2026-11-26: здесь форма хука правильная и требует проверки
    // после обновления: обработчик — одна функция, state первым аргументом,
    // this — вью.
    view.on('$mount', function (state) {
        // Оба снимка читаются параллельно, а вкладка из прошлой сессии
        // проверяется, когда уже известно, куда можно попасть.
        Promise.all([loadModels(state), loadWorld(state)]).then(function () {
            clampStep(state);
        });
    });

    view.mount();
});
