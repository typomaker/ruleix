# Exact и Lossy `Local.Search`: CPU-профиль

## Forced finest-lossy control

Эксперимент 2026-09-01 на Apple M1 Max, Go 1.26.0, `GOMAXPROCS=1`, parent
revision `c017736` отделил overhead lossy implementations от false-positive
materialization. Временный build-only hook исключал exact candidate и выбирал
первый, самый точный lossy ladder level для каждого содержательного leaf
production schema. Листья без отдельного lossy candidate оставались exact.
Hook и временный benchmark удалены после измерения; production code не изменён.

Оба production-запроса вернули 45 IDs в Exact и forced finest-lossy, тогда как
обычный 50%-budget Lossy вернул 80. На этой fixture forced representation
потому имело exact observable precision без false positives, при этом
`Inspector.Mode()` подтверждал `lossy`. Accounted retained memory составила
539 596 bytes против 754 244 bytes у Exact (−28.5%).

Сопоставимая baseline/50%-Lossy серия использовала `benchtime=500ms`,
`count=7`; forced вариант измерялся одним process run, чтобы дорогое построение
finest ladders не повторялось, сначала `500ms x5` для выбранного leaf, затем
`3s x1` для всех доступных lossy leaves. Медианы и устойчивые точки:

| Path | Exact | Forced finest-lossy, 45 IDs | Lossy 50%, 80 IDs |
| --- | ---: | ---: | ---: |
| `Index.Search` | 32 734 ns/op | 186 700 ns/op | 27 874 ns/op |
| warm `Local.Search` | 222.1 ns/op | 227.0 ns/op | 247.9 ns/op |
| `Index.Search` allocations | 40 805 B/op, 28 allocs | 13 274 B/op, 14 allocs | 13 594 B/op, 15 allocs |
| warm `Local.Search` allocations | 0 B/op, 0 allocs | 0 B/op, 0 allocs | 0 B/op, 0 allocs |

Таким образом, при одинаковых 45 выходных IDs чистая Local delta составляет
около +2.2%; основная прежняя +11.6% delta 50%-Lossy связана с 1.78x candidate
amplification. Сопоставимые 20-second Exact и 15-second forced CPU profiles
дали 231.1 и 236.8 ns/op. Нормированная cumulative стоимость
`loadLocalQueryResult` составила 159.2 и 163.3 ns/op; материализация 45 IDs —
22.8 и 25.6 ns/op. Остаточная разница мала и распределена между concrete lossy
query-key matchers и sampling noise, а не вызвана дополнительными кандидатами.

Forced `Index.Search` является плохой практической конфигурацией, несмотря на
точность и меньшую память. Его профиль отнёс 64.7% CPU к `Bitmap.Or`; 62.1%
cumulative пришлось на прежний lossy ordered `addRange`, далее доминировали
`union2by2` (32.6% flat), `bitmapContainer.iorArray` (14.7%) и
`bitmapContainer.loadData` (6.6%). Finest ordered grids материализуют запрос как
union большого числа мелких physical keys, тогда как exact ordered index использует
свою range/block структуру. Обычный 50%-Lossy быстрее Exact на uncached path,
поскольку более грубая сетка резко уменьшает число объединяемых physical keys.

Контроль отвечает именно на вопрос об overhead: при одинаковой точности
`Local.Search` lossy path почти совпадает с Exact, поэтому его заметная
деградация при 50% budget вызвана преимущественно дополнительными результатами.
Однако «больше гранулярности» нельзя использовать как общую оптимизацию:
uncached ordered search регрессирует примерно в 5.7 раза.

## Current output-amplification diagnosis

Повторное измерение выполнено 2026-09-01 на Apple M1 Max, Go 1.26.0,
`GOMAXPROCS=1`, revision `c86ab8a`. Сопоставимые 20-second CPU profiles
production fixture из 38 098 constraints дали 231.1 ns/op для Exact и
255.7 ns/op для Lossy с бюджетом 377 122 bytes:

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeSearch/Local$' \
  -benchtime=20s -count=1 -cpuprofile=/tmp/ruleix-exact-current.cpu .
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeLossySearch/Local$' \
  -benchtime=20s -count=1 -cpuprofile=/tmp/ruleix-lossy-current.cpu .
go tool pprof -top -nodecount=35 /tmp/ruleix-exact-current.cpu
go tool pprof -top -nodecount=35 /tmp/ruleix-lossy-current.cpu
go tool pprof -list='searchAllMatches' /tmp/ruleix-exact-current.cpu
go tool pprof -list='searchAllMatches' /tmp/ruleix-lossy-current.cpu
```

Построчный профиль локализует текущую дельту в materialization результата,
а не в поиске или проверке ключей. Строка
`result = append(result, values[id])` получила 2.28 seconds Exact samples на
100,000,000 операций, то есть 22.8 ns/op, и 4.83 seconds Lossy samples на
94,787,914 операций, то есть 51.0 ns/op. Разница 28.2 ns/op полностью покрывает
наблюдаемые 24.6 ns/op с точностью 10 ms CPU sampling. Собственная cumulative
стоимость `loadLocalQueryResult`, нормированная на число операций, составила
159.2 ns/op Exact и 156.2 ns/op Lossy; следовательно, matcher path не является
причиной текущей деградации.

Focused diagnostic на тех же двух запросах показал 45 Exact matches и 80 Lossy
candidates для каждого запроса, то есть 1.78x candidate amplification. Lossy
копирует в destination на 35 дополнительных `[16]byte` ID. Это ожидаемая цена
разрешённых false positives при текущем memory budget, а не дополнительная
ветка или allocation regression: оба пути остаются на 0 B/op и 0 allocs/op.
Устранить эту дельту одной оптимизацией cache-hit кода нельзя без уменьшения
candidate amplification, изменения публичного результата либо переноса
валидации false positives в библиотеку. Последние два варианта меняют контракт
или состав работы и не являются эквивалентной search-оптимизацией.

Диагностический тест candidate counts был временным и удалён после измерения;
production и benchmark code не менялись. Результаты корректности были
проверены существовавшим тогда `TestProductionShapeLossyNeverDropsExactMatches` и
`TestLossyExactDifferentialEverySupportedRule`.

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
готовые IDs; physical key lookup, пересечение и candidate validation на этом hot path
не выполняются. Exact провёл 73.79% CPU samples cumulatively в
`loadLocalQueryResult`, Lossy — 64.68%; основная работа обоих профилей состоит
из последовательных `localQueryKeyMatches` всех детей production `All`.

Подтверждённая область возникновения дельты — разный concrete code path
проверки тех же логических ключей:

- На момент профиля Exact использовал отдельные fixed-arity equality rules, а
  Lossy — общий rule. Эти специализации впоследствии удалены; текущий layout
  обоих режимов — общий `eqRule`.
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
и размеры физических ключей» верно для bitmap-представления, но не для текущего warm
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
go test -run '^TestLossyExactDifferentialEverySupportedRule$' -count=1 .
```

Актуальный differential-тест проходит; удалённый production-shape тест прошёл
в зафиксированной исторической ревизии.

## Comparator experiments

Повторная проверка выполнена 2026-09-01 на Apple M1 Max, Go 1.26.0,
`GOMAXPROCS=1`, от parent revision `ea57e7f`. Для каждой точки использовался
production benchmark с 38 098 constraints, Lossy budget 377 122 bytes, 80
candidates/query, `-benchtime=1s`; baseline и финальный кандидат измерялись по
10 запусков:

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeLossySearch/Local$' \
  -benchmem -benchtime=1s -count=10 .
```

| Вариант | Медиана | B/op | allocs/op | Retained accounting | Итог |
| --- | ---: | ---: | ---: | ---: | --- |
| Parent | 261.2 ns/op | 0 | 0 | 377 122 bytes | Baseline |
| `lossyCompareByRule`: прямой `r.compare` | 253.0 ns/op | 0 | 0 | 377 122 bytes | Принят, −3.1% |
| Плюс единый comparator в `lossyBetweenRule` | 252.35 ns/op | 0 | 0 | 377 122 bytes budget; +8 bytes на representation | Отклонён |

Прямое чтение уже подготовленного `lossyCompareByRule.compare` устраняет
линейный просмотр пяти operator slots на каждом попадании в Local result cache.
Финальная серия воспроизвела улучшение без смены allocation class или Lossy
budget.

Prepared comparator для `lossyBetweenRule` не дал измеримого end-to-end
выигрыша относительно первого кандидата: 252.35 против 252.4 ns/op в
последовательных промежуточных сериях, то есть разница меньше шума. Поле при
этом увеличивало каждое представление на 8 bytes; accounting был временно
изменён с 48 на 56 bytes. Эксперимент удалён, поэтому `Between` по-прежнему
использует comparator уже подготовленных `from` и `until` physical keys.
