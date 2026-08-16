# Benchmark baseline

Исходные показатели до оптимизаций из `OPTIMIZATION_PLAN.md`.

## Окружение

- Дата: 2026-08-16
- ОС: macOS (darwin/arm64)
- CPU: Apple M1 Max
- Go: 1.26.0
- Набор: 10 000 правил; equality high-cardinality — 100 правил на значение

## Результаты

В таблице приведена медиана пяти запусков. `B/op` и `allocs/op` были
стабильны между повторами.

| Сценарий | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| Eq / low cardinality / hit | 136.9 | 152 | 4 |
| Eq / low cardinality / miss | 70.0 | 112 | 1 |
| Eq / high cardinality / hit | 679.4 | 1 240 | 4 |
| Eq / high cardinality / miss | 59.7 | 112 | 1 |
| Eq / wildcard | 64 061 | 90 293 | 4 |
| Legacy EqValue / low cardinality / hit | 135.3 | 152 | 4 |
| Legacy EqValue / low cardinality / miss | 68.3 | 112 | 1 |
| Legacy EqValue / high cardinality / hit | 677.9 | 1 240 | 4 |
| Legacy EqValue / high cardinality / miss | 57.2 | 112 | 1 |
| GTE / start | 182.8 | 152 | 4 |
| GTE / middle | 1 211 406 | 73 930 | 17 |
| GTE / end | 1 330 768 | 114 910 | 17 |
| LTE / start | 4 711 533 | 114 915 | 17 |
| LTE / middle | 4 602 268 | 73 937 | 17 |
| LTE / end | 249.2 | 152 | 4 |
| CompareBy / start | 4 619 788 | 73 958 | 18 |
| CompareBy / middle | 2 490 194 | 73 956 | 18 |
| CompareBy / end | 1 216 868 | 73 954 | 18 |
| Between / many unique bounds | 2 510 153 | 106 795 | 33 |
| All / flat | 5 070 | 15 761 | 11 |
| All / nested | 10 592 | 27 039 | 31 |
| Parallel Search | 9 199 | 27 053 | 31 |
| Mixed Search/Insert (15:1) | 2 011 | 3 634 | 3 |
| Build 10 000-rule index | 4 173 642 | 2 939 113 | 79 860 |

## Команды воспроизведения

```sh
go test ./...
go test -race ./...
go test -run '^$' -bench . -benchmem -count 5
```

Репрезентативные CPU- и heap-профили сняты на широком `Between`:

```sh
go test -run '^$' -bench '^BenchmarkBetweenManyUniqueBounds$' -benchtime=3s \
  -cpuprofile profiles/baseline_cpu.pprof \
  -memprofile profiles/baseline_mem.pprof
```

Результат профильного запуска: `2 613 753 ns/op`, `106 794 B/op`,
`33 allocs/op`. Профили предназначены для сравнения с тем же сценарием после
изменений алгоритма.
