# История производительности

Команды удалённых широких build/memory benchmark-матриц ниже сохранены как
исторические протоколы измерений. Для их повторения следует использовать
указанную ревизию из Git; текущий benchmark suite оставляет scale-матрицы для
latency/allocations поиска, а Build измеряется точечными сценариями под задачу.

Документ хранит сопоставимые изменения основных performance-показателей между
релизами и предрелизными checkpoints. Он является сводкой, а не заменой сырых
benchmark-отчётов.

## 2026-09-01: common standalone ordered search path

На родительском `a791a30` и кандидате шага 4 сопоставлен selective standalone
ordered workload: один узкий ordered source и семь широких siblings, Apple M1
Max, Go 1.26.0, `GOMAXPROCS=1`, 300 ms, пять запусков. Команда:
`GOMAXPROCS=1 go test -run '^$' -bench
'^BenchmarkLossyAllSelectiveOrderedPlanning$' -benchmem -benchtime=300ms
-count=5 .`.

| Path | `a791a30` median ns/op | candidate median ns/op | B/op; allocs/op |
| --- | ---: | ---: | ---: |
| Adaptive estimate | 6 863 | 5 833 | 6 232; 70 (без изменения) |
| Unknown estimate | 8 760 | 7 692 | 7 368–7 369; 107 (без изменения) |

Общий `orderedIndex` ускорил пути на 15,0% и 12,2%; allocation class не
изменилась. Differential и race fixtures подтвердили общий matcher/Local
layout без false negatives. `go test ./...` и focused race прошли; changed-line
coverage по `gocovdiff` составил 96,7%. Between/CompareBy остаются scope шага
5.

## 2026-09-01: shared rebuild primitives revalidation

На `89fb085` и кандидате `rebuildPostingGeneration` прошёл merge, неизменность старого поколения и overflow fixtures; streaming-матрица подтвердила повторные
downgrade, hard limits, порядок входа и финализацию routing после downgrade.

Сопоставимый build gate выполнен на Apple M1 Max, Go 1.26.0,
`GOMAXPROCS=1`, 100 000 записей, `MemoryLimit(1<<20)`, 300 ms, пять baseline и
три candidate запуска:
```text
GOMAXPROCS=1 go test -run '^$' -bench '^BenchmarkLossyStreamingBuild$' -benchmem -benchtime=300ms -count=5 .
```

| Revision | median ns/op | B/op range | allocs/op range |
| --- | ---: | ---: | ---: |
| `89fb085` | 2 449 938 750 | 906 560 904–906 999 920 | 22 924 657–22 924 690 |
| step 2 candidate | 2 432 079 083 | 906 289 216–906 837 608 | 22 924 638–22 924 680 |
Build time находится в шуме; allocation class совпадает. Search-функции не
менялись. Проверки: `go test ./...`, streaming fixtures и race-вариант.

## 2026-09-01: compiled equality quantizer checkpoint

Первый implementation-срез общей key-архитектуры перенёс equality precision в
конкретный build-скомпилированный quantizer, напрямую используемый insertion,
search и streaming coarsening. Apple M1 Max, macOS arm64, Go 1.26.0,
`GOMAXPROCS=1`, 10 000 entries, `MemoryLimit(200000)`, `benchtime=300ms`, пять
запусков:

| Warm `Local.Search` workload | Предыдущий nested-ladder диапазон | Compiled quantizer | Allocations |
| --- | ---: | ---: | ---: |
| `[16]byte` | 50,91–53,46 ns/op | 49,84–50,69 ns/op | 0 B/op, 0 allocs/op |
| named UUID | 60,07–60,55 ns/op | 57,57–59,01 ns/op | 0 B/op, 0 allocs/op |

Диапазоны не показывают search-регрессии. Команда:
`GOMAXPROCS=1 go test -run '^$' -bench '^BenchmarkLossyCompiledCompositeCodec/(Bytes16|NamedUUID)$' -benchmem -benchtime=300ms -count=5 .`.

## Правила ведения

- Для каждой новой версии сначала измеряется последний релиз и кандидат в
  отдельных worktree на одной машине.
- Код benchmark-сценария должен быть одинаковым либо различия перечисляются в
  разделе «Сопоставимость».
- Основные числа — медианы нескольких прогонов; время, B/op и allocs/op
  записываются вместе.
- Production-shaped поиск, warm/parallel `Local`, `Build`, retained index и
  retained Local составляют минимальную матрицу.
- Различия около 1–2% помечаются как шум; пограничные результаты подтверждаются
  более длинным чередующимся прогоном.
- Строка «release → checkpoint» не считается фактическим результатом нового
  релиза, пока checkpoint не помечен соответствующим тегом.

## Сводка измеренных переходов

### `v0.4.2` → `v0.5.0`

Production-shaped профиль: 38 098 constraints, Apple M1 Max; медиана пяти
прогонов по 50 итераций.

| Сценарий | `v0.4.2` | `v0.5.0` | Изменение |
| --- | ---: | ---: | ---: |
| `Index.Search`, время | 115,8 µs | 93,1 µs | **−19,6%** |
| `Index.Search`, память | 208,2 KB/op | 108,3 KB/op | **−48,0%** |
| `Index.Search`, аллокации | 74 | 33 | **−55,4%** |
| `Local.Search`, время | 36,2 µs | 6,8 µs | **−81,2%** |
| `Local.Search`, память | 152,4 KB/op | 7,7 KB/op | **−95,0%** |
| `Local.Search`, аллокации | 50 | 7 | **−86,0%** |
| `Build`, время | 44,6 ms | 33,3 ms | **−25,4%** |
| `Build`, память | 14,83 MB/op | 4,51 MB/op | **−69,6%** |
| `Build`, аллокации | 311 254 | 24 587 | **−92,1%** |

Заключение: крупное улучшение всех основных путей благодаря сокращению
временных bitmap, компактным postings и устранению дублирующихся структур.
Первичный отчёт: [`BENCHMARK_OPTIMIZATIONS.md`](../BENCHMARK_OPTIMIZATIONS.md).

### `v0.6.0` → pre-`v0.7` checkpoint `18a0bb2`

Замер 24 августа 2026 года, Apple M1 Max, Go 1.26.0. Это checkpoint, а не
строгое сравнение двух выпущенных тегов.

| Сценарий | `v0.6.0` | `18a0bb2` | Изменение |
| --- | ---: | ---: | ---: |
| `Index.Search`, время | 99,17 µs | 97,68 µs | −1,5% (шум) |
| `Index.Search`, память | 108,25 KB/op | 91,92 KB/op | **−15,1%** |
| `Local.Search`, время | 4,147 µs | 6,097 µs | **+47,0%** |
| parallel `Local`, время/search | 2,105 µs | 2,645 µs | **+25,7%** |
| `Build`, время | 33,86 ms | 33,78 ms | −0,2% (шум) |
| retained index | 1,289 MB | 1,289 MB | ≈0% |

Заключение на checkpoint: uncached Index нейтрально-положителен, но выпуск для
Local-heavy нагрузки заблокирован крупной регрессией. Первичный отчёт:
[`BENCHMARK_V0.6.0_VS_MAIN.md`](../BENCHMARK_V0.6.0_VS_MAIN.md).

### `v0.7.1` → pre-`v0.8` checkpoint `6499b0b`

Замер 24 августа 2026 года, Apple M1 Max, Go 1.26.0.

| Сценарий | `v0.7.1` | `6499b0b` | Изменение |
| --- | ---: | ---: | ---: |
| `Index.Search`, время | 41,45 µs | 40,81 µs | −1,6% (шум) |
| `Local.Search`, время | 2,457 µs | 2,642 µs | **+7,5%** |
| parallel `Local`, время/search | 1,252 µs | 1,295 µs | +3,4% |
| `Build`, время | 33,86 ms | 33,99 ms | +0,4% (шум) |
| `Build`, память | 4 921 889 B/op | 5 056 750 B/op | **+2,7%** |
| `Build`, аллокации | 24 825 | 30 155 | **+21,5%** |
| cold Local retained | 1 752 B | 2 968 B | **+69,4%** (+1 216 B) |

Регрессия Local была локализована в безрезультатном lossy planning lookup для
exact-схемы. После исправления Local составил 2,472 µs против 2,642 µs до него
и 2,457 µs у `v0.7.1`. Первичный отчёт:
[`BENCHMARK_V0.7.1_VS_MAIN.md`](../BENCHMARK_V0.7.1_VS_MAIN.md).

### `v0.8.1` → checkpoint `72d496c`

Повторный интегральный замер 29 августа 2026 года, Apple M1 Max, Go 1.26.0.

| Сценарий | `v0.8.1` | `72d496c` | Изменение |
| --- | ---: | ---: | ---: |
| `Index.Search`, время | 43,14 µs | 33,66 µs | **−22,0%** |
| `Index.Search`, память | 73 394 B/op | 40 851 B/op | **−44,3%** |
| `Index.Search`, аллокации | 28 | 28 | 0% |
| warm `Local.Search`, время | 563,9 ns | 564,0 ns | 0,0% |
| warm `Local.Search`, память | 0 B/op | 0 B/op | 0% |
| `Build`, время | 34,78 ms | 33,89 ms | −2,6% |
| `Build`, память | 5 002 202 B/op | 5 244 901 B/op | **+4,9%** |
| `Build`, аллокации | 30 203 | 33 116 | **+9,6%** |

Заключение: physical-source/candidate executor дал крупный uncached search
выигрыш без warm Local регрессии. Build allocation traffic был признан
отдельным узким местом; последующий двухпроходный equality-class compiler
вернул 2 914 allocs/op и 228 255 B/op без отказа от нового search layout.
Первичный отчёт:
[`BENCHMARK_V0.8.1_VS_MAIN.md`](../BENCHMARK_V0.8.1_VS_MAIN.md).

### `v0.8.1` → `v0.8.2`

Полная сравнительная матрица на 31 августа 2026 года для релизного кода
`v0.8.2` (измеренный commit `d81c9ca`), `v0.8.1` и `v0.7.1` записана в
[`benchmark-current-v0.8.1-v0.7.1.md`](benchmark-current-v0.8.1-v0.7.1.md).
После измеренного commit до релиза менялись только benchmark, документация и
lint-оформление без изменения исполняемого поведения библиотеки.
При `GOMAXPROCS=1` текущий код относительно `v0.8.1` ускорил `Index.Search` на
18,4%, warm `Local` на 59,5% и parallel `Local` на 59,0%; относительно
`v0.7.1` выигрыши составили 19,7%, 91,4% и 90,8%. Время `Build` совпало с
`v0.8.1`; build allocation traffic выше `v0.7.1` на 4,9% B/op и 22,0%
allocs/op, но отличается от `v0.8.1` лишь на 0,6% и 0,2% соответственно.

Отдельный generation benchmark на том же production-shaped наборе не выявил
замедления warm `Local.Search` после 64 последовательных `Build` и публикаций:
медианы пяти прогонов оставались в диапазоне 227,0–228,5 ns/op напрямую и
234,2–235,3 ns/op через application-like `RWMutex`. Все поколения сохранили
0 B/op и 0 allocs/op. Методика и полный временной срез находятся в
[`local-search-sequential-builds.md`](local-search-sequential-builds.md).

Дополнительные локальные замеры на 30 августа 2026 года относятся к отдельным
оптимизациям внутри перехода `v0.8.1` → `v0.8.2`:

| Показатель | Последний зафиксированный результат | Контекст |
| --- | ---: | --- |
| `Index.Search` | 32,383 µs/op, 40 851–40 852 B/op, 28 allocs/op | Финальный combined executor gate, 7×3 s. |
| warm `Local.Search` | 228,5 ns/op, 0 B/op, 0 allocs/op | L5 candidate, 7 interleaved runs; L4 parent median 227,8 ns/op, `GOMAXPROCS=1`. |
| three-key Local churn | 318,3 ns/op, 0 B/op, 0 allocs/op | L5 four-slot result working set; parent 1 408 ns/op, 962 B/op, 7 allocs/op. |
| parallel `Local` | 240,4 ns/search | L4 fixed-session gate with `GOMAXPROCS=1`. |
| warm Local retained | 92 432 B | Четырёхслотовый L5 working set под прежним общим бюджетом 64 KiB. |

Итог: `v0.8.2` существенно ускоряет основные search-сценарии без регрессии
времени сборки относительно `v0.8.1`; небольшое изменение build allocation
traffic находится в пределах 0,6%, а удерживаемая индексом память выросла на
0,4%. Полная матрица выше является каноническим release-to-release замером.

### Compiled composite equality codecs 2026-09-01

Apple M1 Max, Go 1.26.0, 10 000 entries, `MemoryLimit(200000)`, 500ms x5.
Warm `Local.Search`: `[16]byte` 51,88–53,33 ns/op, named UUID 59,75–61,80,
string 46,71–51,59, `[3]int` 53,84–54,37, struct 42,51–55,52; все варианты
0 B/op и 0 allocs/op. Команда: `go test -run '^$' -bench
'^BenchmarkLossyCompiledCompositeCodec$' -benchmem -benchtime=500ms -count=5 .`.
Это focused checkpoint нового codec path, а не release-to-release сравнение.

## Непокрытые переходы

### Shared-key migration baseline 2026-09-01

Перед началом объединения физических индексов один смешанный
equality/ordered/range benchmark зафиксировал Exact, internal identity-lossy и
Lossy с 50% бюджета в одном бинарнике. Повторный gate снят на чистом `97c9e00`
(Apple M1 Max, macOS arm64, Go 1.26.0, `GOMAXPROCS=1`), 5 632 entries, 58 запросов,
`benchtime=500ms`, `count=5`; ниже приведены медианы.

| Показатель | Exact | identity-lossy | Lossy50 |
| --- | ---: | ---: | ---: |
| Build | 6,813 ms | 132,382 ms | 108,150 ms |
| Build B/op | 4 899 230 | 71 776 218 | 60 300 443 |
| Build allocs/op | 84 335 | 1 966 737 | 1 579 352 |
| accounted retained | 486 463 B | 486 463 B | 241 016 B |
| `Index.Search` | 50 946 ns/op | 51 029 ns/op | 47 990 ns/op |
| `Index.Search` B/op | 23 497 | 23 497 | 24 721 |
| `Index.Search` allocs/op | 20 | 20 | 17 |
| warm `Local.Search` | 59,53 ns/op | 59,83 ns/op | 2 191 ns/op |
| warm `Local.Search` B/op | 0 | 0 | 152 |
| warm `Local.Search` allocs/op | 0 | 0 | 6 |
| candidates/query | 2,121 | 2,121 | 3,414 |

Identity и Exact совпадают по памяти, allocations и candidate quality;
разница latency `Index.Search` +0,2%, а Local +0,5% находится в шуме серии.
Большая цена Build у identity — измеренная цена текущего полного Lossy planner,
который используется только тестовым control и должен исчезнуть после общей
build-time key transformation. Lossy50 удерживает 49,55% exact accounting и
остаётся корректным conservative superset, но отдельный matcher/cache объясняет
его текущий тёплый Local overhead; это baseline для следующих этапов, а не
разрешение сохранить регрессию.

```sh
GOMAXPROCS=1 go test -run \
  'Test(LossyExactDifferentialEverySupportedRule|IdentityLossyDifferentialEverySupportedRule)$' \
  -count=1 .
GOMAXPROCS=1 go test -run '^$' -bench '^BenchmarkSharedKeyBaseline/' \
  -benchmem -benchtime=500ms -count=5 .
```

### Equality shared-layout gate 2026-09-01

После перевода quantized equality на общий `equalityIndex` повторена
сопоставимая часть baseline на Apple M1 Max, macOS arm64, Go 1.26.0,
`GOMAXPROCS=1`, 5 632 entries, 58 queries, `benchtime=500ms`, `count=5`.
Медиана Exact / identity-lossy: `Index.Search` 52 924 / 51 071 ns/op,
warm `Local.Search` 60,80 / 59,95 ns/op. Оба режима сохранили 486 463 B
accounted retained memory, 2,121 candidates/query, 23 498 B и 20 allocations
для Index, 0 B и 0 allocations для warm Local. Таким образом identity-lossy
не хуже Exact вне шума серии; correctness подтверждён десятикратной
differential-матрицей и repeated-rebuild fixture.

Lossy50 control после общей posting-структуры: медианы 46 734 ns/op для Index
и 2 214 ns/op для warm Local, 24 721 B/17 allocs и 152 B/6 allocs
соответственно; candidate quality остался 3,414, accounted retained уменьшился
с baseline 241 016 до 240 576 B. Search latency и allocation class не
регрессировали относительно baseline выше.

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkSharedKeyBaseline/(Exact|IdentityLossy)/(IndexSearch|WarmLocalSearch)$' \
  -benchmem -benchtime=500ms -count=5 .
go test -run \
  'Test(LossyExactDifferentialEverySupportedRule|IdentityLossyDifferentialEverySupportedRule|LossyEqualityRepeatedRebuildKeepsEveryPosting)$' \
  -count=10 .
go test -race ./...
```

### Exact versus Lossy search checkpoint 2026-09-01

Latest recheck on Apple M1 Max, Go 1.26.0, `GOMAXPROCS=1`, revision
`316596b`, `benchtime=500ms`, `count=7` used the same 38,098-constraint
production fixture and 377,122-byte Lossy budget:

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShape(Search|LossySearch)/(Index|Local)$' \
  -benchmem -benchtime=500ms -count=7 .
```

| Path | Exact median | Lossy median | Lossy delta | Exact / Lossy allocations |
| --- | ---: | ---: | ---: | ---: |
| `Index.Search` | 32,409 ns/op | 27,905 ns/op | **−13.9%** | 40,805 / 13,594 B/op; 28 / 15 allocs/op |
| warm `Local.Search` | 223.5 ns/op | 251.2 ns/op | **+12.4%** | 0 / 0 B/op; 0 / 0 allocs/op |

Both Lossy queries returned 80 candidates. The current implementation therefore
does not show an uncached search degradation: `Index.Search` is faster while
allocating 66.7% fewer bytes and 46.4% fewer objects. A reproducible latency
degradation remains on the warm Local cache-hit path, although both modes stay
allocation-free. Exact-superset correctness passed with
`TestProductionShapeLossyNeverDropsExactMatches` and
`TestLossyExactDifferentialEverySupportedRule`; Lossy may add false positives
but did not drop exact matches.

The older checkpoint below is retained because it captures the search shape
before the subsequent streaming and comparator corrections.

Apple M1 Max, Go 1.26.0, `GOMAXPROCS=1`, current revision `8b81d55`,
`benchtime=500ms`, `count=5`. The production-shaped matrix used 38,098
constraints and a 377,122-byte Lossy budget (50% of exact accounting):

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeSearch/(Index|Local)$' \
  -benchmem -benchtime=500ms -count=5 .
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeLossySearch/(Index|Local)$' \
  -benchmem -benchtime=500ms -count=5 .
```

| Path | Exact median | Lossy median | Lossy delta | Exact / Lossy allocations |
| --- | ---: | ---: | ---: | ---: |
| `Index.Search` | 33,380 ns/op | 43,773 ns/op | +31.1% | 40,805 / 38,909 B/op; 28 / 22 allocs/op |
| warm `Local.Search` | 228.5 ns/op | 586.4 ns/op | +156.6% | 0 / 0 B/op; 0 / 0 allocs/op |

Both production queries returned 139 Lossy candidates. Thus the 50% retained-
memory policy reduces uncached allocation traffic by 4.6% and allocations by
21.4%, but degrades production-shaped search latency, particularly the warm
Local path. This is a measured mode tradeoff, not a revision-to-revision
regression.

The focused four-equality-child matrix did not reproduce the production
latency degradation consistently. At a 50% budget, repeated Index and Local
medians improved from 850.3 to 658.0 ns/op and from 450.8 to 383.6 ns/op;
rotating Index improved from 848.6 to 773.9 ns/op, while rotating Local slowed
from 894.5 to 916.8 ns/op (+2.5%). The companion quality workload returned
exactly 1.000 candidate/query and zero observed false positives at both Exact
and Budget50. `TestLossyExactDifferentialEverySupportedRule` and
`TestProductionShapeLossyNeverDropsExactMatches` passed, so no false negatives
were observed. At that checkpoint the legacy Budget25 benchmark did not build
because the streaming state could not fit that limit, so it produced no search
result and was excluded from the comparison. The 2026-09-01 repeated-checkpoint
correction later made the case build successfully; the historical search
comparison above remains unchanged.

Focused correction measurement on Apple M1 Max, Go 1.26.0, one iteration:

```text
go test -run '^$' -bench '^BenchmarkLossyAllPlanning/(Children4|Children8)/Budget25$' -benchmem -benchtime=1x -count=1 .
Children4/Budget25  402.8 ms/op  271,040,824 B/op  6,839,939 allocs/op  limit 303,144 B
Children8/Budget25  1.736 s/op  1,120,496,104 B/op  28,200,263 allocs/op  limit 606,288 B
```

These are measured build-cost results for the formerly failing points, not a
revision-to-revision speedup claim. Both completed within the configured hard
retained limit; full tests, including production search compactness and exact
superset gates, passed.

Focused CPU profiles confirmed separate causes for the two production paths.
Profiles used the same benchmarks with `-cpuprofile`, `GOMAXPROCS=1`, and a
longer timed region (`benchtime=12s` for Index and `15s` for Local). Index
measured 33,341 ns/op Exact versus 41,488 ns/op Lossy. In the filtered profile,
`validateCandidateBitmap` grew from 10.18% to 18.72% of samples and
`matchesChildID` from 9.04% to 16.23%. The Lossy bucket superset therefore
spends the additional CPU validating candidates against exact child
predicates; this is the primary confirmed Index cause.

Local measured 226.3 ns/op Exact versus 618.1 ns/op Lossy. Exact spent 76.59%
of samples cumulatively in `loadLocalQueryResult`, which validates the two
rotating query keys and returns stored IDs before planning. Lossy spent 47.35%
in `populatePlanningLocalPlan` and 36.24% in `cachedLocalPlanChild` instead.
The confirmed capability gap is `lossyEqualityRule`: it provides bitmap-cache
lookup but not `localQueryKeyProvider`. Consequently `captureLocalQueryKeys`
cannot store a complete key tuple, `loadLocalQueryResult` cannot hit, and every
warm Lossy search must reconstruct the child plan and inspect cached bitmaps.
Adding a collision-safe lossy equality query key is the focused correction to
evaluate; candidate validation remains a separate uncached-Index cost.

The correction stores the original `optionalValue` for lossy equality, matching
the exact implementation. On the unchanged production fixture before the
concurrent streaming-planner worktree changed its candidate shape, 1s x5 gave
a 310.5 ns/op Local median, 0 B/op and 0 allocs/op, versus the preceding
586.4 ns/op checkpoint: a 47.0% latency reduction. Index remained within its
previous range (44,283 ns/op median, 38,909 B/op, 22 allocs/op). A direct A/B
against a compact `{present, bucketID}` key gave 344.0 ns/op; recomputing the
codec hash on every lookup made that variant 10.8% slower than retaining and
directly comparing the original value, so it was rejected. The query-key
correction does not address the independently profiled uncached candidate-
validation cost.

Verification note: the focused query-key and equality-cache tests passed five
consecutive runs. A clean-HEAD full suite and the synthetic Budget50 benchmark
were blocked before search by the existing streaming planner: depending on the
build, it either reported `Lossy streaming state cannot fit the memory limit`
or produced 10,629 candidates and failed the pre-existing compact-result gate.
The query-key path is inactive for that broad result because ready-ID caching
is capped at 256 candidates. These failures reproduce without the query-key
patch and are not counted as correction measurements.

### Production Lossy checkpoint 2026-08-31

Apple M1 Max, Go 1.26.0, `GOMAXPROCS=1`, 38 098 constraints. Полная
production-схема, включая `[16]byte`, `[2]string`, `Between[time.Time]` и
`CompareBy[[3]int]`, собрана под единым бюджетом 377 122 bytes (50% exact
accounting). Команда:

```sh
GOMAXPROCS=1 go test -run '^$' -bench '^BenchmarkProductionShapeLossySearch/' \
  -benchmem -benchtime=1s -count=5 .
```

| Path | Median | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `Index.Search` | 70 030 ns/op | 40 222 | 23 |
| warm `Local.Search` | 1 386 ns/op | 0 | 0 |

Это самостоятельный lossy checkpoint, а не прямое performance-сравнение с
exact: бюджет меняет candidate amplification и состав представлений. Попытка
собрать `Between` из двух comparator-bucket детей дала 11,004 мкс,
18 778 B/op и 6 allocs/op на warm Local против 1,908 мкс и нулевых аллокаций у
universal minimum. Alloc-space профиль отнёс 85,1% выделений к Roaring
`bitmapContainer.clone` на bucket union и ещё 13,0% к clone при intersection;
прототип отклонён. Принятое fused-представление сохраняет обе стороны внутри
одного узла и кеширует готовый результат запроса. До добавления cache
alloc-space профиль кандидата относил 92,44% из 3,304 GiB к
`bitmapContainer.clone`; warm Local занимал 26,482 ns/op, 35,231 B/op и 10
allocs/op. После исправления Local стал быстрее прежнего universal minimum и
вернулся к нулевым аллокациям. Некешированный `Index.Search` намеренно принят
более медленным: он выполняет bucket unions ради сохранения селективности,
одновременно снижая allocation traffic с 62,410 до 40,222 B/op.

Для `v0.1.0`–`v0.4.1`, `v0.5.0`→`v0.6.0`, `v0.7.0`→`v0.7.1` и
`v0.8.0`→`v0.8.1` в репозитории нет полного сопоставимого release-to-release
набора по нынешней production-shaped методике. Changelog описывает изменения,
но не заменяет измерение; поэтому численные строки для этих переходов не
восстанавливаются задним числом из несопоставимых focused-бенчмарков.

## 2026-09-01: exact-first против one-pass streaming

Исправление streaming rebucketing устранило аварийный one-bucket collapse.
Apple M1 Max, Go 1.26.0, `GOMAXPROCS=1`, 38,098 entries, 377,122-byte budget,
`go test -run '^$' -bench '^BenchmarkProductionShapeLossySearch/(Index|Local)$' -benchmem -benchtime=500ms -count=5 .`:
`Index.Search` 42,927–43,423 ns/op (median 43,075), 38,909 B/op и 22 allocs/op;
warm `Local.Search` 586.4–589.7 ns/op (median 586.9), 0 B/op и 0 allocs/op.
Оба запроса возвращали 139 candidates. Непосредственно предшествующий
operator-specific streaming checkpoint давал медианы 50,386 и 3,893 ns/op;
записанный exact-first checkpoint — 70,030 и 1,386 ns/op соответственно.
Измерение является accepted correction текущего streaming path, а не
ретроспективной заменой старых чисел ниже.

Следующий operator-specific streaming вариант заменил universal tail и принят
как production default. Apple M1 Max, Go 1.26.0, 10K entries, четыре equality-
листа, 65% budget, `benchtime=1x`: ordered и shuffled streaming сохранили 9,797
candidates/query и нулевой observed false-positive rate; accounted working peak
составил 261,928 B против 290,856 B exact-first. Build занял 190,9–207,3 ms
против 168,1–181,8 ms и выделил 102,2–104,3 MB/op против 81,7–81,9 MB/op.

Production-shaped Search, 38,098 entries, 377,122-byte budget,
`GOMAXPROCS=1`, 500ms x3: `Index.Search` измерен как
50,129/50,386/56,161 ns/op, 78,858–78,860 B/op, 25 allocs/op против прежней
медианы 70,030 ns/op, 40,222 B/op, 23 allocs/op. Warm `Local.Search`:
3,887/3,893/3,912 ns/op, 0 allocs/op против 1,386 ns/op. Требование задачи
приоритизирует отсутствие полного exact materialization и отсутствие
регрессии `Index.Search`; рост build cost, transient allocations и Local
latency принят и не маскируется.

Исторический universal-tail эксперимент ниже оставлен как отклонённый baseline.
Apple M1 Max, macOS arm64, Go 1.26.0. Четыре equality-листа, 1 024 distinct
значения на лист, общий `MemoryLimit` 65% от exact accounting, 64 равномерно
распределённых запроса. `benchtime=1x`, `count=1`; числа ниже — отдельные
checkpoint-измерения, не статистическая медиана. Accounted peak не включает
Go allocator/RSS. Полный 1M streaming прогон остановлен после 4 минут; это
зафиксированный незавершённый measurement, а не численный результат.

| Entries / порядок | Policy | Build | B/op | allocs/op | accounted peak | retained | candidates/query | FP rate |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 10K / ordered | exact-first | 170.3 ms | 82.0 MB | 1.93M | 290,856 | 187,872 | 9.797 | 0 |
| 10K / ordered | streaming | 197.1 ms | 101.3 MB | 2.43M | 261,928 | 32,928 | 10,000 | 1.0 |
| 10K / shuffled | exact-first | 185.6 ms | 82.1 MB | 1.93M | 290,856 | 188,072 | 9.797 | 0 |
| 10K / shuffled | streaming | 206.6 ms | 103.2 MB | 2.48M | 261,928 | 32,928 | 10,000 | 1.0 |
| 100K / ordered | exact-first | 432.8 ms | 252.3 MB | 2.28M | 1,029,160 | 656,359 | 97.67 | 0 |
| 100K / ordered | streaming | 2.321 s | 1.229 GB | 16.80M | 818,984 | 32,940 | 100,000 | 1.0 |
| 100K / shuffled | exact-first | 507.8 ms | 257.0 MB | 2.28M | 1,029,160 | 655,055 | 97.67 | 0 |
| 100K / shuffled | streaming | 2.452 s | 1.256 GB | 16.40M | 818,984 | 32,940 | 100,000 | 1.0 |
| 1M / ordered | exact-first | 3.359 s | 2.047 GB | 11.14M | 8,687,912 | 5,595,352 | 976.6 | 0 |

GC checkpoints на 100K: exact-first 3 cycles и 0.259 ms pause; streaming 18
cycles и 1.85 ms pause. Peak-live probe с отключённым GC дал соответственно
примерно 252.6 MB и 1.229 GB роста heap. Несмотря на меньшие deterministic
accounted peak/retained числа streaming, текущий universal accumulator теряет
всю селективность и создаёт больше фактической работы. Порядок входа итог не
меняет. Ordered-проба при 50–65% не смогла вместить streaming tails при
успешном exact-first плане.

CPU profile 100K streaming: 50.96% cumulative в `representationLadder`, 40.71%
в callback pressure-path, 31.30% flat в runtime `madvise`. Подтверждённая
причина регрессии — повторная материализация лестниц/bitmap unions на pressure
checks и allocator pressure. На этом историческом checkpoint exact-first был
оставлен default; последующая operator-specific реализация выше заменила этот
вывод, не возвращая universal tail.

```sh
go test -run '^$' -bench '^BenchmarkLossyStreamingTradeoff$' \
  -benchmem -benchtime=1x -count=1 .
go test -run '^$' \
  -bench '^BenchmarkLossyStreamingTradeoff$/Entries100000$/Equality$/Ordered$/Streaming$' \
  -benchtime=1x -count=1 -cpuprofile=/tmp/ruleix-streaming-step8.cpu .
go tool pprof -top -cum /tmp/ruleix-streaming-step8.cpu
```

## Шаблон следующего релиза

```markdown
### `vX.Y.Z` → `vA.B.C`

- Дата, CPU/ОС, Go version:
- Baseline commit/tag:
- Candidate commit/tag:
- Совместимость benchmark-кода:
- Команды и порядок прогонов:

| Сценарий | baseline | candidate | Изменение |
| --- | ---: | ---: | ---: |
| `Index.Search`, ns/op | | | |
| `Index.Search`, B/op | | | |
| `Index.Search`, allocs/op | | | |
| warm `Local.Search`, ns/op | | | |
| parallel `Local`, ns/search | | | |
| `Build`, ns/op | | | |
| `Build`, B/op | | | |
| `Build`, allocs/op | | | |
| retained index | | | |
| cold/warm/adaptive Local retained | | | |

Заключение:
Ссылки на raw output/profile:
```

Решения о конкретных техниках следует переносить также в
[`optimization-decisions.md`](optimization-decisions.md), чтобы история чисел
и история архитектурных выводов оставались связанными.

## 2026-09-01: finer equality bucket counts

На Apple M1 Max, macOS arm64, Go 1.26.0, 10 000 entries и
`MemoryLimit(200000)` fixed-byte поиск занял 55,65–56,94 ns/op, named UUID —
55,82–56,40 ns/op; оба сохранили 0 B/op и 0 allocs/op. Escape analysis
подтвердил inline для multiply-high reduction и отсутствие reflection в
search. Команда:

```sh
go test -run '^$' -bench '^BenchmarkLossyCompiledCompositeCodec$' \
  -benchmem -benchtime=300ms -count=3 .
```
