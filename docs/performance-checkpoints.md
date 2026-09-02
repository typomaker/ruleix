# Дополнительные checkpoint-измерения

Этот документ продолжает каноническую историю из
[`performance-history.md`](performance-history.md) и хранит подробные серии,
не относящиеся к текущему release summary.

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
