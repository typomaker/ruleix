# Эксперимент с чанками внутренних ID

## Дизайн

Build передаёт положительному rule-дереву `internalID >> shift`, а финальная
выдача разворачивает chunk в непрерывный диапазон `Index.values`. Leaf rules и
bitmap операции используют обычный `uint32` и не знают о remap. Прототип
доступен только через внутренний test build control, удерживает lossy keys на
identity-level и отклоняет `Exclude`.

`All` имеет отдельные exact-ID candidate filtering и `matchesID` пути. На
production equality shape они дали разные результаты у холодного Index и
тёплого Local. Поэтому корректный прототип отключает эти операции при ненулевом
shift и материализует полные bitmap operands. Более узкая попытка отключить
только candidate filters не устранила расхождение; точная причина различия
`matchesID` и bitmap execution остаётся открытой и не считается установленной.

## Synthetic серия

Apple M1 Max, 100 000 уникальных ID, два equality-поля:

`go test -run '^$' -bench '^BenchmarkExperimentalIDChunking$' -benchmem
-benchtime=500ms -count=3 .`

| Shift | IDs/chunk | Posting bytes | IDs/result | Search |
| ---: | ---: | ---: | ---: | ---: |
| 0 | 1 | 401 632 | 190 | 9.0–9.8 us |
| 1 | 2 | 340 512 | 760 | 5.7 us |
| 2 | 4 | 340 512 | 3 036 | 7.8 us |
| 3 | 8 | 340 512 | 12 152 | 13.1 us |
| 4 | 16 | 340 512 | 48 592 | 27.4 us |

Первый уровень сохранил около 15% памяти, но дал 4x amplification. Более
крупные чанки память не уменьшили: стали доминировать keys и metadata.

## Production shape

Измерение 2026-09-03: Apple M1 Max, 38 098 уникальных UUID ID, две
чередующиеся production query, `GOMAXPROCS=1`, 500ms x3:

`GOMAXPROCS=1 go test -run '^$' -bench
'^BenchmarkProductionShapeIDChunking/' -benchmem -benchtime=500ms -count=3 .`

Медианы корректного bitmap-only chunk execution:

| Shape | Shift | Posting bytes | Candidates | Index ns/op | Local ns/op |
| --- | ---: | ---: | ---: | ---: | ---: |
| Full | 0 | 567 590 | 45 | 35 125 | 309.7 |
| Full | 1 | 547 648 | 90 | 119 882 | 326.6 |
| Full | 2 | 505 446 | 194 | 81 319 | 342.7 |
| Full | 3 | 477 808 | 446 | 66 977 | 406.2 |
| Equality | 0 | 257 733 | 150 | 19 291 | 485.8 |
| Equality | 1 | 251 097 | 300 | 28 646 | 495.0 |
| Equality | 2 | 246 133 | 610 | 27 973 | 557.8 |
| Equality | 3 | 243 649 | 1 274 | 38 713 | 699.5 |

На полном schema shift 1/2/3 сохранил 3.5%/10.9%/15.8% posting memory при
2.0x/4.3x/9.9x кандидатах. Index замедлился в 3.4x/2.3x/1.9x, Local — примерно
на 5%/11%/31%. Equality-only сохранил лишь 2.6%/4.5%/5.5% при 2.0x/4.1x/8.5x
кандидатах. Для этой production shape глобальный fixed-size chunk не даёт
приемлемого memory/search trade-off.

## Профили и статус

CPU и allocation profiles сняты для Full Index shift 0 и shift 1 командами с
`-benchtime=2s`, `-cpuprofile` и `-memprofile`. Shift 0 дал 34.9 us/op и 40.8
KiB/op, shift 1 — 130.1 us/op и 106.0 KiB/op. У shift 1 CPU сосредоточился в
Roaring `iorArray`, `union2by2` и `Bitmap.Or`; allocation space также перешёл в
`arrayContainer.iorArray` и bitmap clones. Это подтверждает стоимость полной
материализации после отключения exact-ID shortcuts.

Эксперимент не принят и не отклонён окончательно: regression локализован, но
расхождение direct-ID и bitmap semantics ещё не объяснено до первопричины.
Следующая разумная гипотеза — не глобальные соседние чанки, а build-time
группировка ID по одинаковой или близкой posting-signature, после которой
каждый chunk логически однороден для direct-ID операций.

Контроль `shift=0` ранее сравнивался с baseline `d62908c9bea7` через
`BenchmarkEq|BenchmarkAll` (300ms x3); измеренные диапазоны и allocations не
показали search regression.

Проверки production серии: `go test ./...`, `go test -race ./...`, `git diff
--check` и `go test ./... -coverprofile=/tmp/ruleix-idchunk-production.cover`.
Все прошли; package coverage 91.1%, aggregate с example packages 89.7%.
