# Roadmap

This file is the active implementation plan. Completed work and rejected
experiments belong in [`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md);
release-facing behavior belongs in [`CHANGELOG.md`](CHANGELOG.md).

## Общие exact/lossy-индексы

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

- Сделать уровни precision вложенными, чтобы каждый ключ текущего уровня
  однозначно переводился в следующий без исходного значения.
- Зафиксировать направленное округление для `Greater*`, `Less*`, `Between` и
  каждого оператора `CompareBy`; преобразование может только расширять
  множество совпадений.
- Добавить контрольный режим `identity quantizer`, который проходит общий
  lossy pipeline, но возвращает те же ключи и результаты, что exact.
- Зафиксировать текущие Exact, Lossy 50% и identity-lossy показатели для
  build time, accounted retained memory, `Index.Search`, warm `Local.Search`,
  allocations и candidate count.

Gate: differential-матрица всех поддерживаемых правил доказывает равенство
identity-lossy и exact, а обычный lossy сохраняет `result ⊇ exact result`.

### 2. Выделить общие key transformation и rebuild primitives

- Ввести build-скомпилированные encoder/quantizer без reflection и
  interface dispatch в search path.
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
