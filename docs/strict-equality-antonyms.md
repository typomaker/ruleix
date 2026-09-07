# Строгие equality-антонимы

## Принятый алгоритм

После оптимизации дерева и интернирования bitmap `Build` рассматривает пары
equality-потомков одного `All`. Пара компилируется только если wildcard bitmap
являются строгими дополнениями во внутренней ID-вселенной:

```text
cardinality(WA XOR WB) == cardinality(universe)
```

Это условие означает, что каждый ID конкретен ровно в одном из двух правил.
Так как concrete posting всегда лежит вне wildcard собственного правила,
для любого запроса выполняется:

```text
(WA union PA) intersect (WB union PB) == PA union PB
```

Поэтому два runtime-операнда заменяются одним immutable operand, который
объединяет только concrete postings. Это не эвристический short-circuit:
wildcard части алгебраически сокращены на Build, а candidate count и результат
не меняются. Алгоритм работает через общие physical equality capabilities и
не читает `RuleMode`; identity и сжатый Lossy являются теми же состояниями
общего алгоритма.

Если у правила несколько дополнений, candidates сортируются по сумме
serialized wildcard bytes и жадно выбирается не более одного партнёра.
Одинаковый вес сохраняет порядок потомков. Пустые, пересекающиеся и имеющие
gap wildcard обрабатываются тем же строгим доказательством; при отсутствии
доказательства дерево остаётся прежним.

Result-cache родительского `All` продолжает хранить collision-safe ключи
исходных equality-компонентов. Список двух provider-ссылок создаётся один раз
на Build только у реально скомпилированной пары; Search не строит metadata,
не вычисляет XOR и не делает связанных с антонимами allocations. Редкие данные legacy duplicate-equality объединены в
ленивый sidecar, поэтому размер обычного `All` и production retained layout
не растут. Файлы `all.go`, `all_planning.go` и `all_execution.go` разделяют
ядро, планирование/cache и выполнение; каждый остаётся меньше 500 строк.

## Проверка и измерения

Baseline: `be3df36`; candidate: рабочее дерево перед итоговым commit. Среда:
Apple M1 Max, macOS arm64, Go 1.26.0, `GOMAXPROCS=1`.

Representative fixture содержит 4 096 правил, разделённых между двумя
строгими дополнениями. Команда поиска:

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkStrictEqualityAntonymSearch/' \
  -benchmem -benchtime=1s -count=5
```

Медианы Exact: Index `4 113 → 423 ns/op`, rotating Local
`4 395 → 565.5 ns/op`, stable Local `68.63 → 68.41 ns/op`.
Identity-compressed: Index `4 181 → 436.5 ns/op`, rotating Local
`4 237 → 570.5 ns/op`; отдельный interleaved stable gate после layout fix дал
`70.64 → 69.83 ns/op` для Exact и `70.67 → 69.88 ns/op` для identity.
Index allocations изменились `14 385 B/5 → 80 B/4`, rotating Local
`14 385 B/5 → 120 B/6`. Прогретый retained Local (`20x x5`) уменьшился
`13 326 → 3 022 B/local`.

Focused Build (`20x x5`) дал медиану `531 238 → 522 800 ns/op`; цена
доказательства — `553 002 → 553 138 B/op` и `1 241 → 1 246 allocs/op`.
Production Build, где пар нет, после lazy-layout fix сохранил allocation class:
около `38.5 ms`, `5 754 889 B/op`, `30 330 allocs/op`.
Production retained gates также совпали: `96 363 B/Local`, а Index — медиана
`1 323 163 B` в обеих ревизиях.

Production fixture на 38 098 entries содержит ноль строгих пар в Exact,
identity-compressed и Lossy50. Семь интерливированных A/B запусков по 1s дали
Exact Index/Local `27.11 us/308.2 ns → 26.85 us/304.0 ns`; Lossy Local
`1 088 → 1 085 ns`, 358 candidates и 0 B/0 allocs. Lossy Index сохранил
38 098 candidates и 25 allocs; медиана парных изменений +0.15% с выбросами
обоих знаков, то есть устойчивой регрессии нет.

CPU profiles (`-benchtime=10s`) локализовали baseline в Roaring clone,
intersection и add; compiled operand устраняет intersection двух wildcard
результатов. Allocation profiles с `-memprofilerate=1` уменьшили alloc-space
примерно с 5.35 GiB до 64 MiB; baseline 57.2% приходилось на container clone и
42.7% на add, в candidate этих массовых clone нет.

Корректность покрыта deterministic differential Exact/identity-Lossy,
unknown и missing query keys, overlap/gap, пустым wildcard и MatchAll,
несколькими партнёрами и interned bitmap. Финальные gates: `go test ./...`,
`go test -race ./...`, diff coverage не ниже 90% и `git diff --check`.
