# История производительности

Документ хранит сопоставимые изменения основных performance-показателей между
релизами и предрелизными checkpoints. Он является сводкой, а не заменой сырых
benchmark-отчётов.

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

## Текущий предрелизный baseline

Полная сравнительная матрица на 31 августа 2026 года для текущего `d81c9ca`,
`v0.8.1` и `v0.7.1` записана в
[`benchmark-current-v0.8.1-v0.7.1.md`](benchmark-current-v0.8.1-v0.7.1.md).
При `GOMAXPROCS=1` текущий код относительно `v0.8.1` ускорил `Index.Search` на
18,4%, warm `Local` на 59,5% и parallel `Local` на 59,0%; относительно
`v0.7.1` выигрыши составили 19,7%, 91,4% и 90,8%. Время `Build` совпало с
`v0.8.1`; build allocation traffic выше `v0.7.1` на 4,9% B/op и 22,0%
allocs/op, но отличается от `v0.8.1` лишь на 0,6% и 0,2% соответственно.

Последние принятые локальные замеры на 30 августа 2026 года относятся к
отдельным оптимизациям после `v0.8.1`, а не к одному заново измеренному
release-comparison прогону:

| Показатель | Последний зафиксированный результат | Контекст |
| --- | ---: | --- |
| `Index.Search` | 32,383 µs/op, 40 851–40 852 B/op, 28 allocs/op | Финальный combined executor gate, 7×3 s. |
| warm `Local.Search` | 228,5 ns/op, 0 B/op, 0 allocs/op | L5 candidate, 7 interleaved runs; L4 parent median 227,8 ns/op, `GOMAXPROCS=1`. |
| three-key Local churn | 318,3 ns/op, 0 B/op, 0 allocs/op | L5 four-slot result working set; parent 1 408 ns/op, 962 B/op, 7 allocs/op. |
| parallel `Local` | 240,4 ns/search | L4 fixed-session gate with `GOMAXPROCS=1`. |
| warm Local retained | 92 432 B | Четырёхслотовый L5 working set под прежним общим бюджетом 64 KiB. |

Эти числа нельзя напрямую объявлять результатом следующего релиза: перед тегом
нужно повторить полную матрицу против `v0.8.1` в одной сессии и заменить этот
раздел новым tagged transition.

## Непокрытые переходы

Для `v0.1.0`–`v0.4.1`, `v0.5.0`→`v0.6.0`, `v0.7.0`→`v0.7.1` и
`v0.8.0`→`v0.8.1` в репозитории нет полного сопоставимого release-to-release
набора по нынешней production-shaped методике. Changelog описывает изменения,
но не заменяет измерение; поэтому численные строки для этих переходов не
восстанавливаются задним числом из несопоставимых focused-бенчмарков.

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
