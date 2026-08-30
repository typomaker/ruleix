# Проверенные оптимизации и решения

Этот документ — краткий реестр завершённых performance-экспериментов Ruleix.
Он отвечает на три вопроса: что проверяли, что решили и почему. Подробные
замеры, команды воспроизведения и промежуточные варианты сохраняются в
[`ROADMAP_HISTORY.md`](../ROADMAP_HISTORY.md); здесь приведены только выводы,
подтверждённые бенчмарком или профилем.

Статусы:

- **принято** — изменение прошло focused-бенчмарк, production-shaped gate и
  проверки корректности;
- **принято с ограничением** — идея полезна только для измеренного режима и
  включается по порогу или вне чувствительного hot path;
- **отклонено** — реализация удалена после регрессии, отсутствия устойчивого
  выигрыша или недостаточного основания для дополнительной сложности.

## Критерии принятия

Основной end-to-end gate — синтетический production-shaped профиль: 38 098
constraints, 18 полей, диапазоны, UUID и категориальные признаки. Изменения
планировщика дополнительно проверяются на матрице 10K/100K/1M правил,
focused-сценариях разных кардинальностей, `go test ./...` и `go test -race
./...`.

Различия около 1–2% обычно считаются локальным шумом. Для стабильного warm
`Local.Search` регрессия более 3% является основанием отклонить изменение, если
она не компенсирована явно принятым выигрышем в целевом сценарии. Сравнение
должно сохранять allocation class и учитывать retained memory, когда
оптимизация добавляет кэш или build-time метаданные.

## Принятые решения

| Решение | Проверенный эффект | Почему принято |
| --- | --- | --- |
| Прямые getters `(value, ok)`, компактные equality postings, bitmap interning, вынесенные `Exclude`, copy-on-write scratch (`v0.5.0`) | Относительно `v0.4.2`: `Index.Search` −19,6% времени и −48,0% B/op; warm `Local` −81,2% времени и −95,0% B/op; `Build` −25,4% времени и −69,6% B/op. | Крупный выигрыш одновременно в build, cold search и Local без смены семантики. |
| Unary/binary equality specialization (`v0.5.1`) | Lookup leaf быстрее на 5,1–6,2%; retained rule memory меньше на 26,3–35,7%. Полный unary search изменился лишь на +0,6% из-за стоимости перечисления 38 098 результатов. | Уменьшает постоянную память и стоимость самого lookup; нейтральный широкий контроль объяснён output cost. |
| Direct-ID validation для малых кандидатов `All` | Порог четыре ID выбран по dense/sparse матрице; materialized fallback и поздняя проверка сохраняют уже полученные bitmap. | Малые множества дешевле проверить напрямую, но порог не даёт per-ID lookup заменить быстрые bitmap на широких результатах. |
| Candidate filtering для ordered, `Between` и `CompareBy` | Полные диапазонные результаты не материализуются, ограничения применяются к существующему candidate bitmap. В общей physical-identity серии `Index.Search` получил около −22% времени и −44,3% B/op против `v0.8.1`. | Профиль показал materialization главным источником Roaring allocation traffic; оптимизация сокращает именно его. |
| Блок ordered-индекса 64 и bounded second-level aggregates для `CompareBy` | Production-shaped поиск выиграл от блока 64 против 128; второй уровень ускоряет широкие operator unions без изменения search allocations. | Размеры выбраны измерением, а дополнительная память ограничена и включается только для подходящего представления. |
| Компиляция exact nested `All` в плоское дерево | Убирает промежуточные пересечения, сохраняя границы inspected/lossy узлов. | Это build-time преобразование не добавляет ветвей в запрос и прошло differential correctness gate. |
| Ограниченные байтами Local-кэши | Exact `All` results имеют общий бюджет 64 KiB; child caches, планы и compact IDs учитываются отдельно. | Ускорение повторных запросов не должно создавать неограниченную retained memory на широких или adversarial запросах. |
| Compact internal IDs для warm exact `All` results | Warm Local: 546,8 → 407,4 ns/op (−25,5%), 0 B/op и 0 allocs/op; +448 B на production Local. | Убирает bitmap copy и перечисление для результатов до 64 ID, оставаясь внутри существующего бюджета. |
| Валидация точных query keys перед child lookup | Warm Local: 404,5 → 228,1 ns/op (−43,6%); parallel Local: 354,2 → 209,8 ns/search; 0 B/op и 0 allocs/op. | Collision-safe сравнение getter outputs позволяет вернуть точный compact result до ranking и cache lookup. |
| Двухключевая валидация через viable mask (отклонено) | L1 parent 227,7 → candidate 267,8 ns/op (+17,6%); 0/7 paired wins; 0 B/op и 0 allocs/op. | Дополнительные interface dispatch, mask bookkeeping и две type assertion на child оказались дороже устранённых повторных getter-вызовов. Прототип удалён; L3 продолжает от L1. |
| Скомпилированные cached capabilities | Warm Local: 565,4 → 536,2 ns/op (−5,2%); parallel Local −5,4%; uncached Index без регрессии. | Убирает повторные interface assertions из стабильного hot path без изменения кэш-политики. |
| Cardinality-gated equality ID filtering | На 8 и 128 кандидатах adaptive path существенно быстрее bitmap; неограниченный вариант регрессировал 4096-кандидатные случаи до 105,5/149,8 µs, поэтому введён guard 512. | Принята измеренная область применимости, а не глобальная замена алгоритма. |
| Physical-source identity executor | Дубликаты выполняются один раз; first cold search 5,00 → 1,51 µs, second-use 4,55 → 1,17 µs; stable warm +0,7%, ниже 3% gate. | Интегрированный map-free вариант дал крупный cold выигрыш и не ухудшил warm path значимо. |
| Двухпроходная компиляция equality classes | `Build`: −2 914 allocs/op (−8,8%) и −228 255 B/op (−4,35%) без latency/search регрессии. | Убраны setter closures, но сохранён принятый search layout physical-source executor. |
| Total-cost source selection только для uncached Index | Focused mixed case: 3,919 → 2,439 µs, 20 612 → 12 393 B; production Index слегка улучшился, Local остался на прежнем пути. | Cost model полезен для uncached materialization, но исключён из чувствительного warm Local loop. |
| Прямое ограничение concrete postings в shared-wildcard группе | Focused A/B: 2,977 → 2,465 µs (−17,2%), 5 272 → 2 600 B/op, 16 → 8 allocs. | Убирает реальный intermediate только в uncached path; Local и immutable postings не затронуты. |
| Lossy exact-or-superset representations | Бюджет проверяется детерминированным accounting; fixture-матрица проверяет exact/minimum уровни и отсутствие false negatives. | Даёт ограничение retained memory с формальным контрактом `lossy result ⊇ exact result`. |
| Селективное понижение lossy-листьев по максимальному освобождению памяти | В 16-child single-heavy equality при 50% сохраняет 15 exact-листьев; 5,859 candidates/query и 0,000486 observed false-positive rate. Полная 120-case матрица не нарушила лимит и не дала false negatives. | Реализует целевую exact-leaf retention; абсолютное качество принято, несмотря на более низкую candidate amplification у пропорционального baseline. |

Исходные измерения первых двух строк находятся также в
[`BENCHMARK_OPTIMIZATIONS.md`](../BENCHMARK_OPTIMIZATIONS.md). Интегральное
сравнение с `v0.8.1` — в
[`BENCHMARK_V0.8.1_VS_MAIN.md`](../BENCHMARK_V0.8.1_VS_MAIN.md).

## Отклонённые решения

| Эксперимент | Результат | Почему отклонён |
| --- | --- | --- |
| Скомпилированная цепочка валидатора exact query key | L1 parent 228,0 → candidate 261,1 ns/op (+14,5%); 0/7 paired wins; профиль: 71,2% cumulative CPU в цепочке closures. | Go не встроил разнородные typed closures: косвенный вызов на каждом leaf заменил interface dispatch, но не создал fused machine code и оказался дороже. Кандидат удалён. |
| Compiled warm-Local plan routing | End-to-end warm Local улучшился лишь на 0,34%; кандидат выиграл 4 из 7 пар, drift был больше эффекта. | Внутренний профиль улучшился, но пользовательский сценарий — нет; дополнительный routing удалён. |
| Small-result `ManyIterator` decoding | 8 результатов: 58,47 → 101,8 ns/op, появились 192 B/op и 2 allocs. Empty/singleton вариант дал только нестабильные 1,3% на production. | Нарушен zero-allocation класс либо выигрыш не прошёл production gate. |
| Eager total-cost ranking для каждого запроса | Index 43,575 → 45,377 µs; warm Local 566,0 → 591,5 ns (+4,5%). | Дополнительный scoring pass дороже пользы; cost model оставлен только для uncached Index. |
| Shared cross-Local planner learning | Ни один benchmark не показал улучшения fresh Local; собранная статистика почти не использовалась при выборе плана. | Shared state, mutex и retained telemetry не оправданы доказанным эффектом; оставлено локальное детерминированное обучение. |
| Per-query scan одинаковых cached bitmap | Cold path ускорился, но warm 2/4/8-child cases регрессировали на +5,8%/+9,8%/+20,8%. | Cold выигрыш не может оплачиваться систематической warm-регрессией. |
| Map-backed dense equality source classes | Warm latency хуже примерно на 5–6%, cold path получил ещё четыре allocation. | Runtime map lookup и retained pointer-ID table оказались дороже линейного baseline. |
| Source ID внутри каждого equality representation | Те же +4 cold allocations и около +5–6% warm latency. | Перенос ID не устранил per-`All` class-map cost; production-код удалён. |
| Boundary specialization результата 0/1/2 ID | Empty/singleton ускорились на несколько ns, но 4095-result control перешёл 3% regression gate. | Нишевый выигрыш несущественен для production и ухудшает широкий контроль. |
| Reuse destination в `Between.searchBitmaps` | 57 325 → 57 317 B/op, повторяемого latency выигрыша нет. | Изменение не устраняет внутренние Roaring container clones, то есть не воздействует на измеренное узкое место. |
| Unconditional ordered-source streaming | Не прошло матрицу кардинальностей: стоимость обхода широкого ordered source превышала материализацию/фильтрацию. | Представление выбирается по стоимости и кардинальности, а не принудительно. |
| Bounded equality-intersection prototype | Production-варианты оказались существенно медленнее baseline. | Cheap cardinality lookup уже давал нужный порядок; дополнительная intersection стадия создавала лишнюю работу. |
| Post-intersection candidate scan с отдельным широким порогом | Вариант с лимитом 256 ID был decisively worse. | Direct validation остаётся выгодной только для малого, измеренного диапазона. |
| Marginal lossy score по collision rate / bucket resolution | В 16-child single-heavy equality при 50% понизил все 16 листьев и дал 5,609 candidates/query вместо 5,859 у released-bytes; при 25% осталось 625 candidates/query. Mixed 50% ухудшился с 1,516 до 10,92 candidates/query. | Эвристика не устранила проблемную amplification и потеряла главное свойство — сохранение малых exact-листьев; прототип удалён. |

## Как добавлять новое решение

Новая запись должна содержать:

1. дату, коммит и точное описание parent/candidate;
2. CPU, ОС, Go version, benchmark pattern, `benchtime` и `count`;
3. медианы времени, B/op и allocs/op, а для кэшей — retained memory;
4. focused benchmark и end-to-end production/scale gate;
5. проверку корректности и race detector для изменений executor;
6. однозначный итог: принято, принято с порогом или удалено;
7. причину, связывающую измеренный эффект с механизмом, а не только с
   корреляцией.

Неуспешный эксперимент не следует удалять из истории: он предотвращает
повторение уже проверенной идеи и фиксирует условия, при которых вывод может
быть пересмотрен.
