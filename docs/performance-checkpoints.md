# Дополнительные checkpoint-измерения

Этот документ продолжает каноническую историю из
[`performance-history.md`](performance-history.md) и хранит подробные серии,
не относящиеся к текущему release summary.

## 2026-09-03: общий equalityIndex lookup без знания режима

Baseline `4126439`; Apple M1 Max, macOS arm64, Go 1.26.0,
`GOMAXPROCS=1`. Универсальный для `equalityIndex[K]` one-based offset prototype
заменил `mapaccess2` на `mapaccess1`, не меняя размер структуры, map capacity,
physical keys или результаты. Команда и пять интерливированных пар:

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkLossyAllSearchRuntime$/^Budget50$' \
  -benchmem -benchtime=500ms -count=1 .
```

Медианы baseline/candidate: Index repeated 662,5/665,8 ns, Local repeated
394,2/396,9 ns, Index rotating 695,5/699,0 ns, Local rotating 875,4/875,1 ns.
10-секундный Local repeated control дал 396,8/395,1 ns; normalized CPU diff
показал ожидаемую замену runtime map entry points без устойчивого общего
выигрыша. Allocation classes не изменились. Вариант удалён.

Сохраняющий точные physical keys восьмибайтовый FNV chunk loop также измерен
пятью интерливированными парами. Repeated остался в шуме, rotating ухудшился
примерно на 1–1,5%; 10-секундный Local repeated control дал 393,3 ns/op, а
profile локализовал дополнительную работу в `stableStringEqualityHash`.
Вариант удалён. Архитектурное решение и границы следующего frozen-index
эксперимента записаны в
[`optimization-decisions.md`](optimization-decisions.md).

## 2026-09-03: Budget50 Local cache experiments

Среда: Apple M1 Max, macOS arm64, Go 1.26.0, `GOMAXPROCS=1`; baseline
`092db48`. Focused benchmark:

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkLossyAllSearchRuntime$/^Budget50$/^(LocalRepeated|LocalRotating)$' \
  -benchmem -benchtime=500ms -count=1 .
```

Baseline и каждый candidate запускались пятью интерливированными парами.
Per-node `(value, physicalKey)` cache получил repeated `394,5 -> 403,8 ns/op`
и rotating `891,6 -> 928,6 ns/op`. Search allocations не изменились; sampled
allocation profile показал 48 bytes на wrapper каждого hashed equality leaf.
CPU profiles использовали `-benchtime=10s -cpuprofile`; cache candidate дал
`403,5 ns/op` против `393,3 ns/op` baseline и перенёс CPU из hash path в
local-cache validation/dispatch.

Переиспользование уже существующего wide `localAllResult.bits` не меняло
структуры или retained accounting. Первая серия дала repeated
`390,0 -> 386,8 ns/op`, однако расширенная трёхпарная матрица Budget100/50/25
дала Budget50 `388,5 -> 389,1 ns/op`; остальные Local repeated/rotating случаи
также остались в шуме при прежних allocation classes. Длинный candidate
получил `388,7 ns/op`; differential profile не показал устойчивой общей CPU
экономии. Оба прототипа удалены. Подробный вывод и причина решения находятся в
[`optimization-decisions.md`](optimization-decisions.md).

## 2026-09-02: baseline шага 5 Between/CompareBy

Перед повторной унификацией `Between` и `CompareBy` снят baseline на `0598735`:
Apple M1 Max, macOS arm64, Go 1.26.0, `GOMAXPROCS=1`. Production Lossy50 с
бюджетом 377 122 bytes и 80 candidates/query получил медианы 27 456 ns/op,
13 592 B/op, 15 allocs/op для `Index.Search` и 248,2 ns/op, 0 B/op,
0 allocs/op для warm `Local.Search`. Focused candidate filtering `CompareBy`
получил медиану 52 812 ns/op, 21 056 B/op и 7 allocs/op; selective exact
`Between` — 35 198 ns/op, 19 497 B/op и 14 allocs/op.

Shared-key checkpoint подтвердил identity gate: Exact/identity-lossy дали
медианы 54 387/54 742 ns/op для Index и 59,92/60,03 ns/op для warm Local при
одинаковых 486 463 accounted bytes, 2,121 candidates/query и allocation
classes. Lossy50 дал 50 142 ns/op и 1 620 ns/op соответственно, 241 472
accounted bytes и 3,069 candidates/query. Команды:

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeLossySearch/(Index|Local)$' \
  -benchmem -benchtime=500ms -count=5 .
GOMAXPROCS=1 go test -run '^$' \
  -bench '^(BenchmarkAllCompareByCandidateFiltering|BenchmarkBetweenSelectiveSide)$' \
  -benchmem -benchtime=300ms -count=5 .
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkSharedKeyBaseline/(Exact|IdentityLossy|Lossy50)/(IndexSearch|WarmLocalSearch)$' \
  -benchmem -benchtime=300ms -count=3 .
```

Следующий кандидат обязан сохранить эти allocation classes, candidate quality
и retained budget без регрессии любого search path. Предыдущий общий
`orderedIndex` prototype не является кандидатом: его доказанно медленный
aggregate membership path сначала заменяется leaf/aggregate range traversal.

### Повторная проверка общего ordered layout

Восстановленный прототип поверх `8ce6ca6` после исправления shared-bitmap
accounting прошёл exact-superset, identity, boundary и minimum-memory tests.
На той же production fixture он, однако, дал медиану около 94 966 ns/op,
13 304 B/op и 14 allocs/op при 58 candidates/query; warm Local улучшился до
233,4 ns/op. Quantized leaf-only membership дал 93 040–96 192 ns/op, то есть
не устранил uncached-регрессию. Принудительный leaf-only path для exact и
lossy дал 101 490–102 208 ns/op и был хуже.

Профиль снят командой:

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeLossySearch/Index$' \
  -benchtime=3s -cpuprofile=/tmp/ruleix-step5.cpu .
go tool pprof -top ./ruleix.test /tmp/ruleix-step5.cpu
```

В candidate profile `runContainer16.searchRange` занял 24,7% samples;
`orderedIndex.walk`, `Bitmap.Contains` и `allRule.matchesChildID` подтвердили
тот же широкий aggregate membership path, что и в первой отклонённой серии.
Экспериментальный код удалён, поскольку ни одна проверенная leaf-стратегия не
вернула baseline latency. Следующее направление — общий common physical-key layout.

### Bitmap-only candidate filtering

На baseline `8ce6ca6` проверен отказ от `matchesID` для compound range rules.
Canonical lossy leaf/aggregate postings передавались непосредственно в
`Bitmap.AndAny`; среда и production fixture совпадают с baseline шага 5.

| Отключённый direct-ID path | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| baseline | 27 456 | 13 592 | 15 |
| только Lossy `Between` | 25 385 | 30 216 | 22 |
| только Lossy `CompareBy` | 26 910 | 71 433 | 34 |
| оба Lossy rules | 29 901 | 71 433 | 34 |

Отдельный Exact `BenchmarkAllCompareByCandidateFiltering` ухудшился с baseline
52 812 ns/op, 21 056 B/op и 7 allocations/op до 71 081 ns/op, 41 929 B/op и
13 allocations/op. Таким образом, bitmap-only вариант иногда сокращает CPU,
но нарушает allocation gate во всех проверенных конфигурациях. Код удалён.

### Fused `AndAny` scratch prototype

Во временной копии Roaring v2.4.4 `AndAny` получил reusable scratch для cursor
slices и временных array/bitmap union containers. `sync.Pool`-вариант дал
production median 25 288 ns/op, 38 265 B/op и 21 allocations/op. Затем scratch
был закреплён непосредственно за Ruleix `bitmapPool`; median улучшилась до
23 398 ns/op, но allocation shape остался 38 264 B/op и 21 allocations/op.
Baseline был 27 456 ns/op, 13 592 B/op и 15 allocations/op.

`-memprofile`/`pprof -alloc_space` локализовал дополнительный payload в
`bitmapContainer.clone`, `arrayContainer.clone` и writable-container paths.
Следовательно, оставшиеся шесть allocations создаёт COW mutation candidate
bitmap, а не временное представление union. Для нулевой добавочной аллокации
нужен Roaring API, принимающий reusable destination container storage; пул
только верхнего bitmap или scratch union не закрывает gate. Прототип удалён.

## 2026-09-01: exact-first против one-pass streaming

Исправление streaming rebuilding устранило аварийный one-physical key collapse.
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

## 2026-09-01: finer equality physical key counts

На Apple M1 Max, macOS arm64, Go 1.26.0, 10 000 entries и
`MemoryLimit(200000)` fixed-byte поиск занял 55,65–56,94 ns/op, named UUID —
55,82–56,40 ns/op; оба сохранили 0 B/op и 0 allocs/op. Escape analysis
подтвердил inline для multiply-high reduction и отсутствие reflection в
search. Команда:

```sh
go test -run '^$' -bench '^BenchmarkLossyCompiledCompositeCodec$' \
  -benchmem -benchtime=300ms -count=3 .
```
