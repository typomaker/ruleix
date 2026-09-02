# Проверенные оптимизации и решения

Этот документ — краткий реестр завершённых performance-экспериментов Ruleix.
Он отвечает на три вопроса: что проверяли, что решили и почему. Подробные
замеры, команды воспроизведения и промежуточные варианты сохраняются в Git и
соответствующих канонических документах; здесь приведены только выводы,
подтверждённые бенчмарком или профилем.

## 2026-09-02: общий ordered layout для Between/CompareBy отклонён

Прототип перевёл Lossy `Between` и `CompareBy` на общий `orderedIndex`, сохранив
outward-rounded boundaries, fused `Between` matcher и operator-specific
`CompareBy` indexes. Differential, boundary и streaming tests прошли, однако
production-shaped `Index.Search` регрессировал с медианы 26,1 до 76,4 мкс
при неизменном классе 50% budget. Warm `Local.Search` улучшился примерно с
252 до 232 нс и сохранил 0 B/op, но это не компенсирует uncached-регрессию.

CPU profiles локализовали дополнительную работу в candidate validation:
`betweenRule.matchesID` проходил через широкие common block aggregates, а
`runContainer16.searchRange` занял 18,4% candidate samples. Проверены
short-circuit после первого найденного ID, блоки 1/4/8, прямые posting probes,
принудительная bitmap-форма aggregates и bitmap materialization/filtering.
Лучший общий вариант оставался около 46,8 мкс (+79%), а bitmap-варианты либо
увеличивали allocations с 14 до 22, либо переставали укладываться в hard
retained limit. Причина является следствием несовместимых membership shapes:
legacy fused buckets дают ранний выход по компактному posting, тогда как общий
range layout выбирает между широким aggregate lookup и несколькими posting
lookups. Прототип удалён; шаг 5 roadmap остаётся запланированным до появления
общего layout, который не ухудшает ни один search path.

## 2026-09-01: удалены крупные build/memory benchmark-матрицы

`BenchmarkLossySelectionMatrix` удалён: один запуск разворачивал 120 дорогих
build-сценариев на 10 000 записей, дублируя уже зафиксированное решение о
селективном понижении листьев. Следом удалены широкие build/retained/peak/GC
матрицы `LossyAllPlanning`, `LossyScalePlanning`, `LossyStreamingBuild`,
`LossyStreamingTradeoff` и `ProductionScale*`: они доходили до 1 млн записей,
требовали явного `benchtime=1x` и существенно замедляли общий benchmark suite.

Основной suite теперь ориентирован на latency и allocations поиска, включая
сохранённые scale-search матрицы. Build проверяется точечными benchmark-ами под
конкретное изменение; исторические build/memory результаты и команды остаются
в этом документе, `performance-history.md` и Git. Корректность planner-а
продолжают проверять production-shaped и exact-superset/streaming fixtures.

`TestProductionShapeLossyNeverDropsExactMatches` удалён 2026-09-02: он
дублировал общий exact-superset differential gate, но добавлял сборки
production shape на 10 000 записей и 365 поисковых сравнений. Исторические
упоминания теста ниже относятся к ревизиям, в которых он ещё существовал.

## 2026-09-01: общий ordered layout принят для standalone operators

Принят build-only quantized wrapper над общим `orderedRule`/`orderedIndex`.
Соседние posting-классы сливаются с outward boundary, а опубликованный search
использует exact matcher, block aggregates, routing и Local cache. Legacy
numeric/comparator bucket search types удалены. Focused benchmark улучшил
selective path на 12–15% при прежних allocations; correctness, streaming и
race gates прошли. Between/CompareBy остаются отдельным шагом 5.

## 2026-09-01: общий equality posting layout

**Принято:** quantized equality хранит transformed `uint64` keys в общем
`equalityIndex`/`equalitySet`, объединяет коллизии тем же posting primitive и
выполняет streaming rebucket через `rebuildPostingGeneration`. Отдельные
lossy posting map и опубликованный `lossyEqualityRule` удалены; Exact и Lossy
делят bitmap preparation, physical-source metadata и Local cache primitive.

На shared-key gate identity-lossy против Exact получил медианы 51 071 против
52 924 ns/op для Index и 59,95 против 60,80 ns/op для warm Local, с одинаковыми
486 463 accounted bytes, candidate count и allocation classes. Поэтому общий
layout принят без search-регрессии. Среда и команда воспроизведения записаны в
[`performance-history.md`](performance-history.md); race, full suite,
десятикратная differential-матрица и repeated streaming rebuild прошли.

## 2026-09-01: compiled ordered encoder только для текущего scalar layout

**Принято с ограничением:** встроенные numeric ordered Lossy rules компилируют
монотонный `V -> uint64` encoder при построении representation. Reflection
выбирает typed load один раз; insertion и search вызывают сохранённый encoder
без reflection, `any` conversion и runtime type switch. Физический grid и
наблюдаемое поведение остаются прежними.

**Отклонено для этого среза:** перевод named numeric типов из
comparator-backed layout в numeric grid только на основании underlying kind.
На shared-key workload это изменило retained accounting с 243 016 до 240 696
байт и candidates/query с 3.586 до 3.414, то есть перестало быть изолированной
key-transformation заменой. Candidate warm Local дал 2 286–2 412 ns/op против
2 185–2 250 ns/op parent; непересекающаяся регрессия привела к удалению этой
части прототипа.

Ограниченный candidate, сохраняющий прежний выбор representation, дал
1 780–1 797 ns/op, 152 B/op и 6 allocs/op. Последовательные абсолютные времена
заметно менялись, поэтому результат трактуется только как отсутствие
регрессии, не как ускорение. Среда: Apple M1 Max, macOS arm64, Go 1.26.0,
`GOMAXPROCS=1`, 5 632 entries, 58 queries, 500ms, пять запусков. Команда:

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkSharedKeyBaseline/Lossy50/LocalSearch$' \
  -benchmem -benchtime=500ms -count=5 .
```

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
| Ternary equality specialization | На 38 098 ID lookup третьего значения: 18,94 → 14,31 ns/op (−24,4%); retained shape: 264 → 200 B/rule (−24,2%), без учёта дополнительно устранённой map storage. Build latency осталась в пределах шума, специализация добавляет одну аллокацию 208 B. Apple M1 Max, Go 1.26, `-benchtime=300ms -count=5`. | Третье значение больше не создаёт hash map: build использует три inline-ключа, затем общий leaf полностью заменяется immutable ternary-структурой без остаточных map/slice/hint полей. |
| Quaternary equality specialization; fixed N≥5 отклонены | На 38 098 ID lookup четвёртого значения: 19,33 → 14,49 ns/op (−25,0%); retained shape: 296 → 256 B/rule (−13,5%), без учёта устранённой map. Focused build добавляет одну временную аллокацию 256 B. В cutover-матрице строковый miss N=4: 8,9 → 7,17 ns; N=5 — разница в пределах 1,5% около 8,7 ns; N=6: 8,62 → 10,84 ns (+25,8%). Apple M1 Max, Go 1.26, `-benchtime=300–400ms -count=5`. | Принята последняя ступень с устойчивым lookup-выигрышем. Allocation profile с `-memprofilerate=1` подтвердил, что дополнительная focused-аллокация — сам immutable runtime leaf; убрать её без сохранения общей map-backed структуры или unsafe-переиспользования невозможно. Для N≥5 экономия map не оправдывает отсутствие выигрыша или регрессию miss-path; они остаются general map-backed. |
| Direct-ID validation для малых кандидатов `All` | Порог четыре ID выбран по dense/sparse матрице; materialized fallback и поздняя проверка сохраняют уже полученные bitmap. | Малые множества дешевле проверить напрямую, но порог не даёт per-ID lookup заменить быстрые bitmap на широких результатах. |
| Candidate filtering для ordered, `Between` и `CompareBy` | Полные диапазонные результаты не материализуются, ограничения применяются к существующему candidate bitmap. В общей physical-identity серии `Index.Search` получил около −22% времени и −44,3% B/op против `v0.8.1`. | Профиль показал materialization главным источником Roaring allocation traffic; оптимизация сокращает именно его. |
| Блок ordered-индекса 64 и bounded second-level aggregates для `CompareBy` | Production-shaped поиск выиграл от блока 64 против 128; второй уровень ускоряет широкие operator unions без изменения search allocations. | Размеры выбраны измерением, а дополнительная память ограничена и включается только для подходящего представления. |
| Компиляция exact nested `All` в плоское дерево | Убирает промежуточные пересечения, сохраняя границы inspected/lossy узлов. | Это build-time преобразование не добавляет ветвей в запрос и прошло differential correctness gate. |
| Ограниченные байтами Local-кэши | Exact `All` results имеют общий бюджет 64 KiB; child caches, планы и compact IDs учитываются отдельно. | Ускорение повторных запросов не должно создавать неограниченную retained memory на широких или adversarial запросах. |
| Четырёхслотовый exact-result working set | Трёхключевой churn: 1 408 → 318,3 ns/op (−77,4%), 962 B/7 allocs → 0 B/0 allocs; warm production +0,3%. | Устраняет повторную материализацию для небольшого high-churn working set, не меняя общий бюджет 64 KiB; пять ключей сохраняют bounded miss path. |
| Compact internal IDs для warm exact `All` results | Первичное введение лимита 64: warm Local 546,8 → 407,4 ns/op. L4 расширил измеренный лимит до 256: 65/96/128/256 результатов ускорились на 77,8%/77,8%/76,3%/77,1%, 0 B/op и 0 allocs/op. | Убирает bitmap copy и перечисление в измеренном диапазоне; результаты от 257 ID, exclusion path и общий бюджет 64 KiB сохраняют bitmap fallback. |
| Валидация точных query keys перед child lookup | Warm Local: 404,5 → 228,1 ns/op (−43,6%); parallel Local: 354,2 → 209,8 ns/search; 0 B/op и 0 allocs/op. | Collision-safe сравнение getter outputs позволяет вернуть точный compact result до ranking и cache lookup. |
| Двухключевая валидация через viable mask (отклонено) | L1 parent 227,7 → candidate 267,8 ns/op (+17,6%); 0/7 paired wins; 0 B/op и 0 allocs/op. | Дополнительные interface dispatch, mask bookkeeping и две type assertion на child оказались дороже устранённых повторных getter-вызовов. Прототип удалён; L3 продолжает от L1. |
| Скомпилированные cached capabilities | Warm Local: 565,4 → 536,2 ns/op (−5,2%); parallel Local −5,4%; uncached Index без регрессии. | Убирает повторные interface assertions из стабильного hot path без изменения кэш-политики. |
| Cardinality-gated equality ID filtering | На 8 и 128 кандидатах adaptive path существенно быстрее bitmap; неограниченный вариант регрессировал 4096-кандидатные случаи до 105,5/149,8 µs, поэтому введён guard 512. | Принята измеренная область применимости, а не глобальная замена алгоритма. |
| Physical-source identity executor | Дубликаты выполняются один раз; first cold search 5,00 → 1,51 µs, second-use 4,55 → 1,17 µs; stable warm +0,7%, ниже 3% gate. | Интегрированный map-free вариант дал крупный cold выигрыш и не ухудшил warm path значимо. |
| Двухпроходная компиляция equality classes | `Build`: −2 914 allocs/op (−8,8%) и −228 255 B/op (−4,35%) без latency/search регрессии. | Убраны setter closures, но сохранён принятый search layout physical-source executor. |
| Total-cost source selection только для uncached Index | Focused mixed case: 3,919 → 2,439 µs, 20 612 → 12 393 B; production Index слегка улучшился, Local остался на прежнем пути. | Cost model полезен для uncached materialization, но исключён из чувствительного warm Local loop. |
| Прямое ограничение concrete postings в shared-wildcard группе | Focused A/B: 2,977 → 2,465 µs (−17,2%), 5 272 → 2 600 B/op, 16 → 8 allocs. | Убирает реальный intermediate только в uncached path; Local и immutable postings не затронуты. |
| Lossy exact-or-superset representations | Бюджет проверяется детерминированным accounting; fixture-матрица проверяет exact/minimum уровни и отсутствие false negatives. | Даёт ограничение retained memory с формальным контрактом `lossy result ⊇ exact result`. |
| Build-compiled recursive equality codecs | `[16]byte` 51,88–53,33 ns/op, named UUID 59,75–61,80, recursive array 53,84–54,37; все 0 B/op и 0 allocs/op. Последовательные UUID занимают >8 000 из 10 000 high-16 buckets после avalanche. | Build-only reflection компилирует безопасные field/element loads; avalanche устраняет плохое распределение старших FNV-битов, не добавляя search allocations. |
| Avalanche для scalar equality hash | Apple M1 Max, Go 1.26.0, `BenchmarkLossyCompiledScalarCodec`, 500ms x5: `Int64` 188,8–190,1 → 45,47–46,64 ns/op; named `int64` 189,7–190,9 → 45,37–46,47 ns/op; 0 B/op, 0 allocs/op. До исправления `int64(0..99999)` занимали только 8 из 256 и 86 из 4096 bucket-ов. | Raw FNV плохо распределяет последовательные scalar-значения по старшим битам, используемым multiply-high reduction. Финальный SplitMix-style avalanche принят для integer/float/complex/pointer-like codec paths; string уже использует `maphash`, byte/composite paths уже смешивались. |
| Четыре equality bucket-count уровня на степенной интервал | Apple M1 Max, Go 1.26.0, 10K entries, `MemoryLimit(200000)`: `[16]byte` 55,65–56,94 ns/op, named UUID 55,82–56,40 ns/op; 0 B/op, 0 allocs/op. | Принято: multiply-high поддерживает произвольный immutable `bucketCount`; фиксированная лестница 8/8, 7/8, 6/8, 5/8 сохраняет простой детерминированный planner. Adaptive binary search не вводился: он не уменьшает число публикуемых кандидатов без изменения текущего aggregate selector. |
| Fused comparator-bucket minima для `Between` и `CompareBy` | Полная 38 098-entry production-схема с 50% budget: Index median 70,030 ns/op, 40,222 B/op, 23 allocs/op; warm Local 1,386 ns/op, 0 B/op, 0 allocs/op. Boundary/operator differential tests не дали false negatives. | `Between` расширяет обе хранимые границы наружу; `CompareBy` хранит operator-specific ranges. Local cache устраняет повторные bucket unions и сохраняет allocation-free hot path. |
| Селективное понижение lossy-листьев по максимальному освобождению памяти | В 16-child single-heavy equality при 50% сохраняет 15 exact-листьев; 5,859 candidates/query и 0,000486 observed false-positive rate. Полная 120-case матрица не нарушила лимит и не дала false negatives. | Реализует целевую exact-leaf retention; абсолютное качество принято, несмотря на более низкую candidate amplification у пропорционального baseline. |
| Build-time streaming rebucketing при 125% soft target | Исправляющий production gate, Apple M1 Max, Go 1.26, `GOMAXPROCS=1`, 38,098 entries, 377,122 B, 500ms x5: `Index.Search` median 43,075 ns/op, 38,909 B/op, 22 allocs; warm Local median 586.9 ns/op, 0 B/op/allocs, 139 candidates. До исправления streaming давал 50,386 ns и 3,893 ns; exact-first checkpoint — 70,030 ns и 1,386 ns. | Поздний extreme пересчитывает numeric grid либо расширяет comparator boundaries; memory pressure сливает ступени по одной. Полное exact input не удерживается, search type не получает wrapper или runtime rebucketing. |
| Повторный streaming pressure selector | Apple M1 Max, Go 1.26.0, 10k equality entries, `benchtime=1x`: ранее не собиравшиеся Budget25 точки завершились за 402.8 ms / 271.0 MB / 6.84M allocs (4 leaves) и 1.736 s / 1.120 GB / 28.20M allocs (8 leaves). Production compact-search и exact-superset gates прошли. | Проверка остаётся активной каждые 4096 записей; exact и lossy сравниваются по освобождению следующего шага. Build-only adaptive holder удаляется до публикации, поэтому search path не меняется. |
| Прямой prepared comparator в Lossy `CompareBy` | Apple M1 Max, Go 1.26.0, `GOMAXPROCS=1`, production Local, `1s x10`: 261.2 → 253.0 ns/op (−3.1%), 0 B/op, 0 allocs/op, budget 377 122 bytes. | Убирает просмотр пяти operator slots из каждого warm query-key check; comparator уже сохранён при Build, поэтому новая память не требуется. |

Исходные измерения первых двух строк находятся также в
[`BENCHMARK_OPTIMIZATIONS.md`](../BENCHMARK_OPTIMIZATIONS.md). Интегральное
сравнение с `v0.8.1` — в
[`BENCHMARK_V0.8.1_VS_MAIN.md`](../BENCHMARK_V0.8.1_VS_MAIN.md).

## Отклонённые решения

| Эксперимент | Результат | Почему отклонён |
| --- | --- | --- |
| Скомпилированная цепочка валидатора exact query key | L1 parent 228,0 → candidate 261,1 ns/op (+14,5%); 0/7 paired wins; профиль: 71,2% cumulative CPU в цепочке closures. | Go не встроил разнородные typed closures: косвенный вызов на каждом leaf заменил interface dispatch, но не создал fused machine code и оказался дороже. Кандидат удалён. |
| Direct-ID cutover 16 вместо 8 | Cold Local 459,6 → 453,1 µs (−1,4%), churn 1 416 → 1 409 ns (−0,5%): ниже 10% gate. | Более широкий cutover почти не затронул production range-materialization bottleneck; порог возвращён к 8. |
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
| `Between` как вложенный `All` двух comparator-bucket правил | На production 50% budget warm Local ухудшился с 1,908 мкс и 0 allocs у universal fallback до 11,004 мкс, 18 778 B/op и 6 allocs/op. Alloc-space профиль отнёс 85,1% к Roaring `bitmapContainer.clone` через `lossyComparedOrderedRule.search`/`Bitmap.Or`, ещё 13,0% — к clone на intersection. | Композиция потеряла fused `Between` execution/cache path и материализует широкие стороны; для более точного lossy `Between` нужно отдельное fused представление, прототип удалён. |
| Universal-tail streaming downgrade при 120% soft target | На 10K/100K equality с четырьмя листьями и 65% budget дал 100% false positives; на 100K занял 2,32 с против 0,43 с exact-first. Ordered-проба при 50–65% не смогла вместить universal tails. | Отклонена именно universal-tail реализация. Её заменила operator-specific вставка в публикуемые buckets; результаты старого прототипа не относятся к принятому streaming path. |
| Отдельный prepared comparator в `lossyBetweenRule` | После принятого `CompareBy` production Local остался в шуме: 252.4 → 252.35 ns/op, 0 B/op и 0 allocs/op. Поле добавляло 8 bytes на representation; временный accounting 48 → 56 bytes. | Оба bucket comparator уже подготовлены на Build. Дублирование указателя не ускорило hot path и ухудшало retained shape, поэтому прототип удалён. |

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
