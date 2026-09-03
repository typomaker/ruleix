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

## Потоковый Build для `Lossy`

Статус milestone: `активен`.

Цель — реализовать `Lossy` как однопроходное хранение единственного текущего
представления каждого правила. Пока хватает памяти, правило хранит
`exact key -> bitmap`. После pressure checkpoint всё сохранённое представление
правила целиком перекладывается через следующий вложенный уровень округления:

```text
current key -> quantize(next level) -> same physical index -> merge bitmaps
```

Это переписывание, а не последовательная доработка текущей lossy-реализации.
Существующие lossy types, planners, ladders, capacity/classes и coarsening
алгоритмы не являются источником требований или ограничений нового дизайна и
не должны переноситься в него по умолчанию. Их разрешено читать только для
удаления, переноса публичных контрактов и сохранения доказанных correctness
cases; архитектурные решения принимаются от exact path и контракта этого
milestone.

Новый lossy не имеет собственных posting, index, matcher, range-search или
cache структур. Он всегда использует те же структуры и search-код, что Exact.
Единственное различие Exact и Lossy — функция преобразования ключа и
сохранённый уровень её точности:

```text
physical key = quantize(semantic key, current level, role)
level 0: quantize(key, 0, role) = exact physical key for key
level N: quantize(key, N, role) = conservative coarser physical key
```

Exact всегда работает на identity level 0. Lossy также начинается с level 0 и
переходит на следующий уровень только при memory pressure. Quantizer имеет две
однозначные операции одного контракта:

```text
key(value, level, role) -> physical key for insert/search
next(current physical key, level, role) -> physical key at level + 1
```

Для любого значения и role обязателен закон вложенности:

```text
next(key(value, N, role), N, role) == key(value, N+1, role)
```

Insert и search используют `key`; полный rebuild использует `next`. За
пределами этих преобразований режим не должен быть виден общим exact-
структурам.

`role` не обозначает Exact/Lossy. Для equality он один. Для ordered он явно
задаёт сторону консервативного преобразования: stored lower округляется вниз,
его query boundary — вверх; stored upper округляется вверх, его query boundary
— вниз. Strict/inclusive остаётся параметром общего matcher-а и не выбирает
другой physical layout. Таким образом insert и search вызывают один transformer
с разным направлением одной и той же boundary-семантики, а не разные lossy
алгоритмы.

Общий equality physical key всегда является полным или округлённым `uint64`:

```text
physicalKey(V, level) = round(hash(V), level)
level 0 -> full 64-bit hash(V)
level 1 -> retain the high 16 bits
level N -> clear one more retained low bit
```

Исходное `V` не входит в physical index ни на одном уровне. Exact и Lossy
используют один `equalityIndex[uint64]` и один `eqRule`; level меняет только
округление integer key. Rebuild следующего уровня работает с текущим `uint64`
без повторного hash и без сохранения semantic value.

Операция смены уровня всегда транзакционна и имеет один порядок действий:

1. Построить пустое поколение того же exact index type.
2. Для каждого текущего `(physical key, bitmap)` вычислить ключ уровня `N+1`.
3. Вставить bitmap под новым ключом либо объединить его с существующим.
4. Проверить accounting и сохранение всех IDs.
5. Одним присваиванием опубликовать новое поколение и `current level = N+1`.
6. Удалить все ссылки на старое поколение и продолжить чтение input.

Частичное изменение текущего поколения, параллельная публикация двух уровней и
fallback к заранее подготовленному representation запрещены.

Режим задаётся только внешней Build-политикой: отсутствие `Lossy` означает, что
контроллер никогда не инициирует переход с level 0; `Lossy(MemoryLimit)`
разрешает контроллеру повышать уровень. Сам `eqRule`, `orderedRule` и их indexes
не определяют режим по level, наличию quantizer-а или другому флагу. Ветвление
по номеру уровня разрешено только внутри обязательного key transformer.
`Inspect.Mode` получает Exact/Lossy из policy metadata, а не из physical rule.

Текущий уровень сохраняется в правиле и одинаково применяется к последующим
insert и search values. Исходные exact keys после успешного перехода не
удерживаются. Диагностика сообщает число уникальных physical keys: planner не
вычисляет отдельное разбиение, не строит все
будущие representations и не сливает соседние postings по одной паре.

До последнего шага milestone запрещены performance-оптимизации, benchmark-
driven изменения layout и решения по промежуточным `ns/op`, allocations или
candidate count. Шаги 1–11 реализуют и проверяют только контракт, корректность,
детерминизм, hard memory limit и отсутствие удержания старых поколений. Все
performance benchmarks, profiles и оптимизации выполняются только в шаге 12.

### 1. Зафиксировать контракт потокового округления

Статус: `завершён — 2026-09-02`

Результат: в канонической lossy-архитектуре закреплены identity level 0,
вложенные полные переходы поколений, общий Exact physical/search path,
pressure selector и разделение retained/transient accounting; contract unit
tests подтвердили identity, вложенность, монотонность и общий physical index.

- Описать единый контракт current level, next level, преобразования нового
  значения и повторного преобразования уже сохранённого ключа.
- Зафиксировать level 0 как identity-функцию для любого ключа; Exact всегда
  использует level 0, а Lossy начинает с него без отдельного physical layout.
- Зафиксировать, что quantizer является единственной mode-specific частью:
  posting/index/matcher/range/cache структуры и весь search execution берутся
  непосредственно из Exact и не знают, выбран Exact или Lossy.
- Рассматривать текущий lossy production code только как удаляемую реализацию и
  источник публичных compatibility/correctness cases, но не как основу нового
  алгоритма или ограничение его внутреннего устройства.
- Потребовать вложенность уровней: переход `N -> N+1` без exact value должен
  совпадать с прямым округлением exact value на уровень `N+1`.
- Зафиксировать общий алгоритм checkpoint: при accounted usage выше 125%
  soft target выбрать правило с максимальным освобождением, перестроить всё
  его текущее представление и продолжить чтение input.
- Разделить retained accounting опубликованного поколения и transient память
  перестройки; hard `MemoryLimit` относится к финальному retained состоянию.
- Зафиксировать, что один universal bitmap допустим только как терминальный
  уровень, когда более точное доступное представление не помещается в hard
  limit, а не как следствие локальных последовательных merge.

Gate: контракт записан в канонической lossy-архитектуре; unit tests выражают
identity level 0, полную общность physical/search структур, вложенность и
монотонность уровней без performance assertions и benchmarks.

### 2. Ввести единый state и rebuild primitive

Статус: `завершён — 2026-09-02`

Результат: добавлен единый mutable build-state с current level и общим
преобразованием insert/search; атомарный полный rebuild сохраняет ID, раздельно
учитывает retained/transient bytes, откатывается при ошибке и освобождает ссылки
старого поколения. Targeted и full tests, diff coverage и `git diff --check`
прошли без запуска benchmarks.

- Представлять каждый lossy-лист одним mutable build index, current level и
  преобразователями insert/search key текущего уровня.
- Реализовать checked rebuild всего поколения: пройти текущие `(key, bitmap)`,
  применить следующий уровень, объединить совпавшие keys и заменить старое
  поколение только после успешной сборки нового.
- Немедленно освобождать ссылки на старое поколение и не сохранять исходный
  exact index, список exact postings или заранее построенные будущие уровни.
- Финализировать immutable aggregates, routing и search metadata только после
  последнего streaming downgrade.

Gate: повторные rebuild сохраняют каждый ID, не удерживают старые поколения,
корректно обновляют accounting и проходят targeted tests; benchmarks не
запускаются и layout не оптимизируется.

### 3. Перевести equality на вложенные уровни

Статус: `завершён — 2026-09-02`

Результат: equality переведён на ленивый identity-to-prefix rebuild одного
поколения без будущих streaming candidates; текущий quantizer применяется к
поздним insert/search, а ordered/shuffled, duplicates, wildcards и повторные
downgrade прошли correctness, accounting, hard-limit и diff-coverage gates.

- Exact-фаза хранит полный integer hash; после первого downgrade новые значения
  сразу хешируются и округляются текущим equality quantizer.
- Сделать hash-prefix levels вложенными, чтобы следующий storage key
  вычислялся только из текущего storage key.
- Использовать одну фиксированную equality ladder: level 0 хранит полный hash;
  level 1 хранит старшие 16 бит стабильного полного hash; каждый следующий
  уровень удаляет ровно один младший retained bit; level 17 хранит 0 bits и
  является единственным universal equality key. Нестепенные разбиения,
  build-selected salts и дополнительные representations не входят.
- При повышении уровня полностью перекладывать текущий `equalityIndex`, сливая
  bitmap только у ключей с одинаковым новым значением.
- Удалить static representation ladder и параллельное хранение equality
  candidates из streaming path.

Gate: ordered/shuffled input, duplicates, wildcards, late values и повторные
downgrade дают `Lossy ⊇ Exact`, детерминированную форму и соблюдают hard
limit; выполняются только correctness и accounting checks.

### 4. Перевести numeric и time ordered rules

Статус: `завершён — 2026-09-02`

Результат: numeric и time ordered rules переведены на фиксированные вложенные
outward levels с полным rebuild поколения; strict/inclusive границы, поздние
значения, extremes и повторные переходы прошли differential, full-test,
hard-limit и diff-coverage gates без benchmarks.

- Реализовать вложенные уровни монотонного ключа с устойчивыми origin и width;
  расширение наблюдаемого диапазона не должно менять уже выбранный уровень.
- Для lower bounds округлять наружу вниз, для upper bounds — наружу вверх;
  strict/inclusive используют один уровень с корректной boundary-семантикой.
- На downgrade перекладывать все текущие keys через следующий уровень вместо
  последовательного слияния соседних postings.
- Удалить `orderedIndex.coarsenOne`, `orderedMergeExpansion`, управление через
  `lossyCapacity` и тесты, фиксирующие выбор конкретной соседней пары.

Gate: boundary/adversarial differential matrix, поздние значения за исходным
диапазоном и последовательные переходы не дают false negatives; никаких
performance conclusions на этом шаге не делается.

### 5. Поддержать arbitrary comparator через boundary levels

Статус: `завершён — 2026-09-02`

Результат: arbitrary stable total-order comparators переведены на вложенные
outward boundary levels непосредственно в общем orderedIndex; structs,
case-insensitive strings, composite и descending orders, late extremes и все
strict/inclusive направления прошли differential, hard-accounting, full-test
и diff-coverage gates без benchmarks.

- Для стабильного total-order `Compare[V]` строить вложенные уровни outward
  boundaries, где каждый следующий уровень является подмножеством предыдущего.
- Округлять insert и search values бинарным поиском к boundary текущего уровня;
  storage key должен позволять перейти к родительской boundary без exact value.
- Корректно включать новые значения внутри и за пределами наблюдаемого диапазона
  без перестройки ранее выбранной семантики уровня.
- Не пытаться выводить порядок из reflection/getter codec. Автоматический
  ordered codec разрешён только для типов и comparator-ов с доказанной общей
  семантикой; иначе используется boundary quantizer.
- Задокументировать требование стабильного транзитивного total order как часть
  существующего контракта пользовательского comparator-а.

Gate: пользовательские structs, strings/custom collation, composite keys,
descending order и range extremes проходят differential tests на всех уровнях;
benchmark и layout tuning отложены.

### 6. Перевести `Between` и `CompareBy`

Статус: `завершён — 2026-09-03`

Результат: `Between` получил независимые уровни нижней и верхней стороны, а
`CompareBy` — отдельные nested outward levels для всех пяти операторов;
operator-specific pairwise coarsening удалён из streaming path, а missing,
duplicates, wildcards, strict/inclusive и differential hard-limit gates прошли.

- `Between` хранит независимые current levels нижней и верхней стороны и
  перестраивает за один pressure step только одну полную сторону.
- `CompareBy` хранит уровень отдельно для `EQ`, `LT`, `LTE`, `GT`, `GTE` и
  применяет соответствующее направление outward rounding.
- Сохранить общий fused matcher/search path; lossy отличается только текущим
  преобразованием ключа и физическими postings после build-time rebuild.
- Удалить оставшиеся operator-specific pairwise coarsening и static ladder
  helpers после переноса correctness coverage.

Gate: missing values, duplicate IDs, wildcards, strict/inclusive boundaries и
все операторы сохраняют `Lossy ⊇ Exact`; identity level полностью равен
Exact. Performance не измеряется и не оптимизируется.

### 7. Переписать aggregate pressure selector

Статус: `завершён — 2026-09-03`

Результат: aggregate selector переведён на фактический next-generation release
с deterministic release/current-usage/schema-order выбором; pressure останавливается
на soft target, nested caps применяются от потомка к предку, а отдельный финальный
gate обеспечивает hard limit и допускает terminal fallback только при публикации;
full, race, deterministic и 100% diff-coverage проверки пройдены.

- На каждом checkpoint для каждого доступного листа материализовать ровно одно
  временное полное поколение уровня `N+1` тем же rebuild primitive и измерить
  его фактический accounted `nextLevelUsage`; формула-оценка, выборка keys и
  заранее сохранённый candidate запрещены.
- До выбора сохранить не более одного временного поколения на лист. После
  выбора опубликовать поколение выбранного листа, а временные поколения всех
  остальных листьев немедленно освободить.
- Выбирать один лист по максимальному `currentUsage - nextLevelUsage`, затем
  применять ровно один глобальный переход уровня и повторять до soft target.
- Сохранить nested `MemoryLimit`, deterministic tie-break и ошибку Build, если
  сумма терминальных представлений превышает hard limit.
- Не учитывать candidate quality, search latency или результаты benchmarks в
  selector до завершения функциональной реализации.

Gate: multi-leaf и nested-policy tests подтверждают правильный выбор по
освобождённым байтам, детерминизм, hard limit и отсутствие преждевременного
universal fallback; performance assertions отсутствуют.

### 8. Удалить устаревший streaming planner и завершить correctness gates

Статус: `завершён — 2026-09-03`

Результат: старые planners/ladders, capacity/pairwise coarsening и universal-tail
удалены; Build публикует только одно текущее streaming-поколение, Inspect
показывает его фактические keys и accounting, а full, race, differential,
deterministic и 92.7% diff-coverage gates пройдены без benchmarks.

- Удалить static leaf representation ladders, pairwise merge planner,
  universal-tail fallback и build-only состояние, противоречащее модели одного
  текущего поколения.
- Обновить `Inspect.Strategy`, `Granularity` и memory details так, чтобы они
  описывали реально опубликованный level и число текущих rounded keys.
- Добавить общий регрессионный test: если многоключевое представление реально
  помещается в hard limit, streaming Build не должен публиковать один bitmap.
- Выполнить full tests, race, differential matrix, streaming scale,
  deterministic-build, retained accounting и diff coverage.
- Обновить архитектуру и пользовательский lossy contract; промежуточные
  performance observations не использовать для изменения реализации.

Gate: все функциональные и memory gates проходят, production fixture не
переогрубляется относительно доступного hard limit, старый planner недостижим
из production code, а performance benchmarks ещё не использовались как gate.

### 9. Унифицировать equality rule и search execution

Статус: `завершён — 2026-09-03`

Результат: Exact и Lossy equality объединены в одном `eqRule` и одном
`equalityIndex[uint64]`; physical index не удерживает исходный `V`, а общие
search, planning, cache,
inspection и bitmap paths прошли full, race и 96.8% diff-coverage gates без
benchmarks.

- Удалить `quantizedEqualityRule` как самостоятельный production rule и
  перенести преобразование ключа в общий `eqRule`.
- Exact и Lossy должны использовать один тип правила, одни реализации insert,
  search, cardinality, `matchesID`, planning lookup, Local cache и bitmap
  interning; mode-specific методы поиска запрещены.
- Использовать один `equalityIndex[uint64]`: level 0 записывает полный hash,
  первый rebuild очищает младшие биты до level 1, последующие rebuild очищают
  ещё один retained bit только из текущего integer key.
- Ни один опубликованный key или build-state не удерживает исходное `V`;
  generic specialization и physical layout между уровнями не меняются.
- Сохранить build-only полный rebuild поколения, не вводя отдельный runtime
  wrapper или search branch для Lossy.

Gate: production tree не содержит отдельного lossy equality search type;
Exact, identity-Lossy и lossy equality проходят одну реализацию всех search-
методов, а differential, cache, inspection и retained-memory tests проходят
без benchmarks и performance tuning.

### 10. Сделать level 0 явной identity-функцией

Статус: `завершён — 2026-09-03`

Результат: standalone ordered Exact и Lossy объединены в одном `orderedRule`
с обязательным identity-level transformer; отдельный `quantizedOrderedRule` и
lossy state в `orderedIndex` удалены, mode перенесён в policy metadata, а
identity/type, full, race и diff-coverage gates пройдены без benchmarks.

- Заменить семантику `nil quantizer означает Exact` единым обязательным key
  transformer для equality и ordered families.
- Удалить `quantizedOrderedRule` как отдельный production/build-lifecycle тип;
  Exact и Lossy должны публиковать один `orderedRule` и выполнять одни методы
  insert, search, cardinality, `matchesID`, range walk и Local cache.
- Удалить из `orderedRule` и `orderedIndex` lossy-специфичные понятия и
  ветвления (`nil quantizer`, `boundaries != nil`, проверку режима по level).
  Общий `orderedIndex` хранит и сравнивает только уже преобразованные physical
  keys и не знает ни о Lossy, ни об уровнях, ни о способе округления.
- Хранить в общем rule обязательный mode-neutral key transformer и его current
  level. Для Exact transformer находится на identity level 0; различие режима
  не должно менять тип rule, index или вызываемый search-код.
- Определить level 0 формально и в коде как `quantize(key, 0) = key`; Exact
  всегда использует этот уровень, Lossy начинает с него и меняет только номер
  уровня при pressure.
- Убрать проверки режима из общих posting/index/matcher/range/cache структур:
  они получают уже преобразованный physical key и не знают источник уровня.
- Оставить решение о разрешении pressure переходов во внешнем Lossy Build-
  controller; общий rule не выводит Exact/Lossy из current level, а inspection
  получает mode из metadata соответствующего policy decorator-а.
- Свести storage и query transformation к одному `quantize(key, level, role)`;
  допустимые ordered roles и их направления полностью заданы в общей цели
  milestone, а отдельные mode-specific функции или wrappers запрещены.

Gate: unit tests напрямую подтверждают identity level 0 для всех семейств;
Exact и identity-Lossy имеют одинаковые rule/index/search types и поведение,
`quantizedOrderedRule` отсутствует, `orderedIndex` не содержит lossy state, а в
production search code нет ветвления по Exact/Lossy. Benchmarks не запускаются.

### 11. Закрыть семантику arbitrary comparator и финальный functional gate

Статус: `завершён — 2026-09-03`

Результат: late comparator extremes получили явную open-edge семантику и
входят в следующий полный rebuild; `Between` и `CompareBy` лишились отдельных
lossy runtime/search types, а full, race, differential, deterministic,
hard-accounting и >90% changed-code coverage gates прошли без benchmarks.

- Реализовать однозначную функцию текущего boundary level для arbitrary
  comparator. Внутри наблюдаемого диапазона она выбирает ближайшую retained
  boundary в заданном outward-направлении. За пределами диапазона, когда
  существующей outward boundary нет, `key` возвращает само значение; во время
  Build insert сохраняет его как новую крайнюю boundary текущего уровня, а
  immutable search использует тот же возвращённый ключ только как transient
  lookup/range boundary и не изменяет index. Это часть quantizer-а, а не обход
  округления.
- Новая крайняя boundary участвует в следующем полном `N -> N+1` rebuild на
  общих основаниях. Нельзя немедленно перестраивать остальные boundaries,
  менять номер уровня или сохранять отдельный exact tail только из-за позднего
  extreme value.
- Обеспечить вложенность boundary levels и эквивалентность последовательного
  `N -> N+1` прямому округлению exact value на `N+1` без сохранения exact keys.
- Исправить оставшиеся inspection, numeric-kind и compound-downgrade failures;
  не ослаблять assertions, выражающие публичный или новый streaming-контракт.
- Повторить full tests, race, differential matrix, streaming scale,
  deterministic-build, hard/retained accounting и diff coverage после шагов
  9–10.
- Обновить canonical architecture и lossy contract в соответствии с реально
  общей Exact/Lossy реализацией.

Gate: `go test ./...` и race проходят; late comparator values следуют явно
заданному edge-правилу и после следующего pressure rebuild входят в общий более
грубый уровень, `Lossy ⊇ Exact` сохраняется на всех уровнях, отдельные lossy
search types отсутствуют и diff coverage изменённого production-кода не ниже
90%. Performance benchmarks и оптимизация всё ещё запрещены.

### 12. Выполнить benchmarks, profiles и только затем оптимизацию

Статус: `в работе`

- После завершения шагов 1–11 снять сопоставимые Exact, identity-lossy и Lossy
  серии для equality, standalone ordered, `Between`, `CompareBy`, production
  `All`, mixed shared-key, range-heavy и adversarial workloads.
- Измерить `Index.Search`, warm `Local.Search`, Build time, allocations,
  accounted retained memory, candidates/query и observed false-positive rate.
- Воспроизвести baseline/candidate interleaved runs и снять CPU/allocation
  profiles для каждого обнаруженного search regression.
- Только на этом шаге выполнять performance-оптимизации; каждая оптимизация
  должна сохранять streaming-контракт и заново проходить correctness/memory
  gates шага 11.
- Зафиксировать результаты в performance history и optimization decisions,
  обновить changelog и финальную архитектуру.

Gate: ни один публичный search path не регрессирует по корректности, latency,
allocations или retained memory; измерения воспроизводимы и документированы,
а все принятые оптимизации повторно прошли полный gate шага 11.

## Порядок поставки

Шаги выполняются небольшими коммитами строго в указанном порядке. Каждый шаг
содержит документацию, tests и измерение diff coverage для изменённого
production-кода. До шага 12 benchmarks и profiles не являются gate, а
performance-оптимизации запрещены; шаг 12 выполняет все сопоставимые измерения и
последующую оптимизацию завершённой функциональной реализации.
