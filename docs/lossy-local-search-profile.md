# Exact и Lossy `Local.Search`: CPU-профиль

## Контекст

Измерение выполнено 2026-09-01 на Apple M1 Max, Go 1.26.0,
`GOMAXPROCS=1`, на текущем рабочем дереве поверх revision `e2d7b30`.
Рабочее дерево содержало незакоммиченные изменения streaming planner, поэтому
это сравнение описывает именно текущую candidate shape: production fixture из
38 098 constraints, Lossy budget 377 122 bytes (50% exact accounting), два
чередующихся прогретых запроса и 80 Lossy candidates/query.

Команды:

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeSearch/Local$' \
  -benchtime=15s -count=1 -cpuprofile=/tmp/ruleix-exact-local.cpu .
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeLossySearch/Local$' \
  -benchtime=15s -count=1 -cpuprofile=/tmp/ruleix-lossy-local.cpu .
go tool pprof -top -nodecount=40 /tmp/ruleix-exact-local.cpu
go tool pprof -top -nodecount=40 /tmp/ruleix-lossy-local.cpu
go tool pprof -list='localQueryKeyMatches' /tmp/ruleix-exact-local.cpu
go tool pprof -list='localQueryKeyMatches' /tmp/ruleix-lossy-local.cpu
```

## Результат

Exact измерен как 242.1 ns/op, Lossy как 260.5 ns/op: Lossy медленнее на
18.4 ns/op, или 7.6%. Оба пути сохранили 0 B/op и 0 allocs/op. Короткий
сопоставительный прогон `500ms x5` перед профилированием дал медианы 227.2 и
256.1 ns/op соответственно (+12.7% Lossy), поэтому направление дельты
воспроизводится, а её величина чувствительна к профилированию и длительности
прогона.

Профиль опровергает объяснение через размер bitmap или число Lossy candidates
для этого прогретого workload. Оба режима попадают в
`allRule.loadLocalQueryResult`, валидируют сохранённые query keys и возвращают
готовые IDs; bucket lookup, пересечение и candidate validation на этом hot path
не выполняются. Exact провёл 73.79% CPU samples cumulatively в
`loadLocalQueryResult`, Lossy — 64.68%; основная работа обоих профилей состоит
из последовательных `localQueryKeyMatches` всех детей production `All`.

Подтверждённая область возникновения дельты — разный concrete code path
проверки тех же логических ключей:

- Exact использует специализированные `unaryEqRule`/`binaryEqRule` для части
  equality-полей, тогда как сжатые поля используют общий
  `lossyEqualityRule`.
- `compareByRule.localQueryKeyMatches` читает сохранённый `r.compare`
  напрямую. `lossyCompareByRule.localQueryKeyMatches` на каждом cache hit
  вызывает `firstComparator()` и просматривает operator slots до первого
  присутствующего comparator. В Lossy profile этот дополнительный шаг виден
  непосредственно в строковом профиле.
- Exact `betweenRule` использует один `r.compare`; `lossyBetweenRule`
  разыменовывает comparator отдельно у `from` и `until`. Нормированная на
  число операций cumulative стоимость Between key check составила примерно
  42.6 ns/search Exact против 47.8 ns/search Lossy; CompareBy `[3]int` —
  примерно 14.1 против 17.3 ns/search. Значения являются оценками по CPU
  samples, а не отдельными microbenchmark measurements.

Следовательно, предположение «на поиске отличается только способ сжатия ключей
и размеры бакетов» верно для bitmap-представления, но не для текущего warm
query-result-cache path. Сам bitmap здесь уже обойдён, а polymorphic key
validation реализован разными exact/lossy типами. Для устранения дельты нужно
сначала измерить focused варианты: сохранить comparator непосредственно в
`lossyCompareByRule`, унифицировать Between key matcher и сравнить общий
prepared query-key matcher со специализированными exact equality matchers.
Профиль уверенно исключает bitmap execution и локализует overhead в key-check
loop; долю каждого concrete matcher следует считать ориентировочной из-за
10 ms sampling granularity, поэтому перечисленные функции ещё не являются
доказательством, что одна из них в одиночку объясняет всю дельту.
Это рекомендации для следующего optimization experiment; production code в
рамках данного диагностического измерения не менялся.

Корректность проверена командами:

```sh
go test -run '^(TestProductionShapeLossyNeverDropsExactMatches|TestLossyExactDifferentialEverySupportedRule)$' -count=1 .
```

Оба теста прошли.
