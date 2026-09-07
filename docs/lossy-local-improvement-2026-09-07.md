# Lossy Local: candidate quality и общий cache

## Диагноз

На `ab24d5a`, Apple M1 Max, Go 1.26.0, `GOMAXPROCS=1`, production Exact
возвращает 45 IDs за примерно 310 нс, а Lossy при половинном retained budget —
358 кандидатов за примерно 1,64 мкс. CPU profile локализовал два эффекта:
candidate amplification 7,96x и потерю compact result path на прежнем пороге
256 IDs. Lossy заново восстанавливал план и перечислял Roaring bitmap.

Family attribution показала, что основной рост создают equality leaves. В
equality-only форме 75% budget также дают 358 кандидатов; production `All`
только ограничивает эту форму другими предикатами. Поэтому cache является
второй оптимизацией, а не заменой улучшению representation.

## Отклонённые candidate-planner варианты

Все короткие серии выполнены с `200ms x1`; их задача — отсеять варианты перед
дорогим interleaved gate.

- Equality ladder с первым уровнем 32 hash-бита вместо 16 дала shared-key
  2,121 candidates/query, 40,16 мкс `Index` и 68,48 нс Local, но production
  ухудшился до 1 960 candidates/query и 6,98 мкс Local. Дополнительные уровни
  полезны, но greedy selector выбрал плохую комбинацию.
- Bounded knapsack по released bytes после каждого streaming-перехода ухудшил
  production до 4 322 candidates/query и 16,29 мкс Local, shared-key — до
  3,241x и 76,64 мкс Index. Минимальный overshoot не является quality metric.
- Выбор leaf с максимальным средним posting ухудшил production до 1 982
  candidates/query и 6,36 мкс Local; shared-key Local получил 152 B/op и шесть
  allocations. Локальная ширина leaf не описывает качество пересечения `All`.

Код трёх экспериментов удалён. Следующий representation planner должен
оптимизировать глобальную стоимость комбинации, учитывать future-growth
reserve streaming build и проходить production, shared-key и adversarial gates.

## Принятое общее увеличение compact cache

Mode-agnostic `allRule` сохранил общий executor, а порог готового результата
увеличен с 256 до 512 IDs. Общий 64 KiB result-cache budget не изменён; более
широкие результаты остаются на bitmap path. Никакой `RuleMode` или Lossy-only
проверки не добавлено.

Пять интерливированных baseline/candidate пар готовых test binaries по 1s:

```sh
<baseline-or-candidate>.test -test.run '^$' \
  -test.bench '^BenchmarkProductionShapeLossySearch/Local$' \
  -test.benchmem -test.benchtime=1s -test.count=1
```

Медиана улучшилась `1 636 → 1 125 ns/op` (−31,2%) при неизменных 358
candidates/query, 0 B/op и 0 allocs/op. Exact control сохранил 309,8 ns/op.
Десятисекундные profiles дали 1 616/1 102 ns/op: baseline тратил 65,1%
cumulative CPU на Roaring iteration и 20,0% на planning, кандидат — 65,2% на
неизбежные `appendChunkValues`/`memmove` и 14,5% на query-key validation.

`BenchmarkProductionShapeLossyLocalRetainedMemory`, `20x x5`, дал медианы
93 403/96 477 retained-B/Local (+3 074 bytes, +3,3%). `memprofilerate=1`
сохранил общий allocation profile; разница полного alloc space была около
0,04 MiB на 20 Local. Synthetic Budget100/50/25 repeated/rotating в трёх
интерливированных 300ms парах сохранили latency и allocation classes.

## Build-selected hash и bitmap-антонимы

Hash-функция и key transformer должны оставаться общими: identity/exact —
полное состояние того же алгоритма, compressed — его более грубое состояние.
Build-selected hash нельзя оценивать только по числу physical keys: при `b`
оставленных битах больше `2^b` классов всё равно невозможно. Целевая функция
должна учитывать распределение postings и их совместную селективность в `All`.

Для equality конкретные key bitmaps взаимоисключающи. На Build антонимы
вычисляются **только** по bitmap конкретных ключей: wildcard не является ни
узлом, ни входом score/pruning. Поэтому bitmap несовместимой ветви является
безопасным антонимом: попадание доказывает несовпадение, а отсутствие ID в
конкретном ключе означает, что для этой колонки остаются query key или
wildcard. Wildcard добавляется к выбранной concrete-корзине уже общей
операцией поиска; отдельная lossy-проверка для него не нужна.
Общее перспективное представление — bitmap-trie физического ключа. Exact
хранит полное дерево, compressed state обрезает нижние ветви; sibling bitmap
на каждом уровне даёт антоним без false negatives. Это не отдельный Lossy path,
а разные глубины одной структуры. До реализации нужны accounting модели для
узлов/bitmap, алгоритм budget pruning и сравнение с flat physical-key index.

Первый test-only feasibility prototype построил адаптивный prefix forest для
семи непустых production equality leaves. Split score уменьшал сумму квадратов
posting cardinality на каждый дополнительный accounted byte; wildcard bitmap
оставался вне дерева. При том же equality-only 75% checkpoint `234 076 B`
модель с 8/16-byte node overhead использовала 230 554/232 276 bytes и вернула
150 candidates/query — observable parity с Exact против 358 у flat ladder.
При 20/24-byte overhead она успела купить меньше splits и вернула 747/1 485
кандидатов при 233 524/233 402 bytes. Реалистичный runtime-node тем самым уже
хуже плоского индекса. Дерево остаётся полезным Build-time способом подобрать
разбиение, но публиковаться должен прежний плоский concrete-key index с общим
transformer-ом: identity является exact-состоянием, hash partition — одним из
compressed-состояний. Это сохраняет единый executor и не хранит wildcard в
дереве.
