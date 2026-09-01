# Roadmap

This file is the active implementation plan. A fully completed step remains
marked here with its completion date and a concise result summary until every
step in its milestone is complete. Roadmap history is not stored in separate
files: Git preserves the committed pre-cleanup state. Release-facing behavior
belongs in [`CHANGELOG.md`](CHANGELOG.md). Partial work must not be marked as a
completed step.

## Формат шага

Каждый шаг оформляется по следующему шаблону:

```markdown
### N. Краткое название

Статус: `запланирован` | `в работе` | `завершён — YYYY-MM-DD`

Результат: краткая итоговая сводка выполненного изменения и его эффекта.

- Конкретный элемент scope.
- Конкретный элемент scope.

Gate: проверяемые критерии завершения и обязательные проверки.
```

Для статусов `запланирован` и `в работе` строка `Результат` не добавляется.
Статус `завершён` разрешён только после выполнения всего scope и Gate; дата
указывается в формате ISO `YYYY-MM-DD`, а `Результат` обязателен. Подробные
замеры, эксперименты и доказательства остаются в соответствующих канонических
документах.

## Завершение milestone и очистка roadmap

Когда все шаги milestone имеют статус `завершён`, сначала создаётся отдельный
коммит, в котором `ROADMAP.md` ещё содержит полный milestone: цель, все шаги,
даты, результаты и пройденные Gate. Этот коммит является исторической точкой
перед очисткой и должен успешно пройти применимые проверки.

Только после этого milestone удаляется из `ROADMAP.md`, а следующий roadmap
добавляется или переводится в активное состояние. Активация очищенного roadmap
фиксируется вторым отдельным коммитом. Файлы истории roadmap и архивные копии
milestone не создаются: для восстановления используется Git. Нельзя объединять
pre-cleanup snapshot и post-activation cleanup в один коммит.

## Общие exact/lossy-индексы

Статус milestone: `активирован заново — 2026-09-01`.

Milestone выполняется повторно с шага 1. Результаты и код предыдущего прохода
не засчитываются автоматически: на каждом шаге нужно заново проверить весь
scope и пройти Gate на текущем `HEAD`. Уже реализованные части разрешено
переиспользовать только после такой проверки; незавершённые изменения следующих
шагов не переводят их в статус `в работе`, пока шаг 1 не завершён.

Цель — оставить `Lossy` политикой преобразования ключей, а не отдельным
search engine. Exact и lossy должны использовать одни физические posting
структуры, matcher-ы, range search и `Local`-кэши. Lossy отличается только
скомпилированным преобразованием ключа и выбранной точностью:

```text
stored value -> exact key -> quantizer(identity | lossy precision)
             -> common equalityIndex / orderedIndex -> common matcher
```

Streaming downgrade остаётся обязательным. При memory pressure текущие
`(key, posting)` перекладываются в индекс того же типа через более грубый
quantizer; postings одинаковых новых ключей объединяются. После завершения
`Build` build-only состояние удаляется, а search получает ту же immutable
структуру независимо от режима.

### 1. Зафиксировать семантику ключей и baseline

Статус: `завершён — 2026-09-01`

Результат: на чистом `97c9e00` повторно подтверждены key/precision/rounding
контракты; differential-матрица доказала identity-lossy = Exact и
Lossy ⊇ Exact, а сопоставимый lifecycle baseline зафиксирован в канонической
документации с командами воспроизведения.

- Заново описать и проверить внутренние контракты exact key, quantized key и
  precision ladder на фактической реализации текущего `HEAD`.
- Подтвердить вложенность уровней precision: каждый ключ текущего уровня должен
  однозначно переводиться в следующий без исходного значения.
- Зафиксировать направленное округление для `Greater*`, `Less*`, `Between` и
  каждого оператора `CompareBy`; преобразование может только расширять
  множество совпадений.
- Повторно проверить контрольный `identity quantizer`, проходящий общий lossy
  pipeline и сохраняющий те же ключи и результаты, что exact.
- Снять новые Exact, Lossy 50% и identity-lossy baseline для build time,
  accounted retained memory, `Index.Search`, warm `Local.Search`, allocations
  и candidate count. Старые замеры остаются историческим контекстом.

Gate: на текущем `HEAD` differential-матрица всех поддерживаемых правил
доказывает равенство identity-lossy и exact, обычный lossy сохраняет
`result ⊇ exact result`, а новые baseline и команды воспроизведения записаны в
канонической документации.

### 2. Выделить общие key transformation и rebuild primitives

Статус: `завершён — 2026-09-01`

Результат: повторно подтверждены build-скомпилированные equality/ordered key
transformations, общий checked rebuild независимых posting generations и
финализация immutable routing/aggregates только после streaming downgrade;
hard-limit, order, race, full-test и сопоставимый build gate пройдены без
изменения allocation class.

- Проверить заново и при необходимости переработать build-скомпилированный
  equality encoder/quantizer, затем распространить общий контракт на ordered
  key transformations без reflection и interface dispatch в search path.
- Реализовать общую операцию `old key -> coarser key -> merge postings` с
  checked accounting и освобождением старого поколения после успешной сборки.
- Отделить mutable build layout от финализации immutable search layout:
  агрегаты блоков, routing и range blocks строятся один раз после последнего
  streaming downgrade.
- Сохранить текущий aggregate selector, nested `MemoryLimit`, `Inspect` и
  детерминированный выбор следующей ступени.

Gate: повторные streaming downgrade укладываются в hard retained limit,
не зависят от порядка входа сверх уже задокументированного контракта и не
удерживают полное exact-представление после перехода.

### 3. Унифицировать equality

Статус: `завершён — 2026-09-01`

Результат: exact и quantized equality переведены на общие
`equalityIndex`/`equalitySet`, Local cache и checked streaming rebuild;
отдельный `lossyEqualityRule` удалён, а correctness, race, retained и
сопоставимый identity-lossy performance gate прошли без регрессии.

- Научить `equalityIndex` принимать уже преобразованный ключ и объединять
  `equalitySet` при коллизии quantized keys.
- Exact использует identity key; lossy использует полный compiled hash и
  выбранное сокращение класса, но lookup, postings, matcher и Local cache
  остаются общими.
- Перенести streaming `rebucket` на общий rebuild primitive.
- После прохождения gates удалить опубликованный search type
  `lossyEqualityRule` и дублирующую логику его matcher/cache.

Gate: correctness, retained accounting и streaming fixtures проходят;
identity-lossy equality не хуже exact по latency, allocations и retained
memory за пределами шума сравнимой серии.

### 4. Унифицировать ordered rules

Статус: `запланирован`

- Хранить exact и округлённые monotonic keys в общем `orderedIndex`.
- При lossy downgrade округлять хранимую границу наружу в зависимости от
  направления и inclusive/exclusive семантики.
- При совпадении новых ключей объединять postings, затем использовать
  существующие block aggregates, routing, `walk` и range blocks.
- Перенести numeric regrid и comparator coarsening на общий rebuild primitive.
- Удалить отдельные search paths `lossyOrderedRule` и
  `lossyComparedOrderedRule` после прохождения gates.

Gate: finest/identity-lossy `Index.Search` больше не выполняет линейный union
мелких lossy buckets и не регрессирует относительно exact; обычные lossy
ступени сохраняют текущую или лучшую latency/allocations/candidate quality.

### 5. Унифицировать `Between` и `CompareBy`

Статус: `запланирован`

- `Between` хранит нижний и верхний преобразованные ключи в общих ordered
  структурах: lower округляется вниз, upper вверх; используется общий fused
  matcher exact-пути.
- `CompareBy` компилирует безопасное направление quantization отдельно для
  `EQ`, `LT`, `LTE`, `GT`, `GTE`, но использует общий тип ordered index и
  общий operator matcher.
- Streaming downgrade перекладывает текущие интервалы/границы без хранения
  исходных exact values. Граница должна сохранять представляемый диапазон,
  чтобы повторное огрубление оставалось безопасным.
- Удалить `lossyBetweenRule`, `lossyCompareByRule` и их отдельные query-key
  matcher-ы после функционального и performance parity.

Gate: boundary/adversarial differential tests не дают false negatives;
identity-lossy полностью равен exact, включая strict boundaries, missing
values, duplicate IDs и все операторы.

### 6. Завершить миграцию и подтвердить production shape

Статус: `запланирован`

- Удалить неиспользуемые lossy search types, отдельные caches и bucket-union
  helpers; оставить lossy planner, quantizers, accounting и build-time rebuild.
- Обновить `Inspect.Strategy`/`Granularity`, не меняя публичный API без
  отдельного решения.
- Выполнить race, full test, differential, retained-memory, streaming-scale и
  production-shaped benchmark gates.
- Снять сопоставимые CPU/allocation profiles Exact, identity-lossy и Lossy 50%.
- Обновить архитектуру, performance history, optimization decisions и
  changelog только после принятия реализации.

Финальный gate: ни один публичный search path не регрессирует по корректности,
latency, allocations или retained memory. Если общий layout ухудшает exact или
lossy workload, изменение профилируется и исправляется либо отклоняется согласно
[`docs/project-governance.md`](docs/project-governance.md).

## Порядок поставки

Шаги выполняются небольшими коммитами в указанном порядке. Старый lossy search
path остаётся рабочим до прохождения gate соответствующего семейства правил;
одновременная замена всех операторов не требуется. Каждый шаг должен содержать
документацию, тесты, сравнимый benchmark при изменении performance path и
измерение diff coverage для production-кода.
