# Production shape общей exact/lossy-реализации

## Статус проверки 2026-09-02

Шаг 6 roadmap остаётся в работе. Общая реализация сохранена: удалять или
откатывать её из-за обнаруженной деградации запрещено условиями milestone.
Correctness, race, retained-memory и streaming gates проходят, но latency,
allocation, candidate-quality и deterministic-build gates пока не позволяют
завершить шаг.

Среда измерений: Apple M1 Max, macOS arm64, Go 1.26.0, `GOMAXPROCS=1`.
Исторический baseline — `0598735`, текущая unified-реализация — `6855a8a`
(`eb7a3dd` содержит production change; следующий commit меняет диагностику и
прямой ordered membership). Benchmark fixtures между сравниваемыми сериями не
менялись.

## Воспроизведение деградации

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeLossySearch/(Index|Local)$' \
  -benchmem -benchtime=500ms -count=5 .
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkSharedKeyBaseline/(Exact|IdentityLossy|Lossy50)/(IndexSearch|WarmLocalSearch)$' \
  -benchmem -benchtime=300ms -count=5 .
```

Production Lossy50 baseline `0598735` давал median 27 456 ns/op, 13 592 B/op,
15 allocs/op, 80 candidates/query для `Index.Search`; warm `Local.Search` —
248,2 ns/op, 0 B/op и 0 allocs/op. На `6855a8a` повторная серия дала median
46 344 ns/op (+68,8%), 14 264 B/op (+4,9%), 16 allocs/op и 150 candidates/query
(+87,5%); Local — 318,1 ns/op (+28,2%), 0 B/op и 0 allocs/op.

Прямой обход `orderedIndex.matches` вместо callback сохранил layout и
correctness. Первая серия дала median 45 540 ns/op, финальная при заметном
machine drift — 46 149 ns/op и 322,7 ns/op Local; candidate count не изменился.
Эксперимент с bucket-shaped блоками того же
`orderedIndex` и range aggregates по 128 блоков ухудшил median примерно до
47 594 ns/op и также не изменил 150 candidates/query; экспериментальные поля
удалены, unified implementation сохранена.

Shared-key серия подтвердила parity Exact/identity-lossy: median Index
54 560/55 138 ns/op, warm Local 60,60/60,49 ns/op, одинаковые 486 463
accounted bytes, 2,121 candidates/query и классы 23 498 B/op, 20 allocs/op и
0 B/op, 0 allocs/op соответственно. Один Lossy50 процесс дал median 57 518
ns/op, 1 687 ns/op Local, 237 184 accounted bytes и 3,328 candidates/query;
отдельные процессы выявили вариативность плана, описанную ниже.

## Локализованные причины

Production CPU profile:

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeLossySearch/Index$' \
  -benchtime=8s -count=1 -cpuprofile=/tmp/ruleix-step6-lossy-index.cpu .
go tool pprof -top -nodecount=30 ./ruleix.test /tmp/ruleix-step6-lossy-index.cpu
```

На `6855a8a` профиль отдал 33,5% cumulative в
`allRule.matchesChildID`, 10,8% cumulative в `betweenRule.matchesID` и 15,5%
flat в Roaring `union2by2`. Это подтверждает две независимые составляющие:
дорогой common range membership и больший объём bitmap union/candidate checks.

Временная child-level диагностика того же production build показала, что
activity `Between` под 50% cap получает всего три quantized key classes и
33 124 accounted bytes. Bytes-only aggregate selector отдаёт крупные доли
бюджета equality customer UUID (95 760 bytes, 529 classes) и platform subtree
(94 120 bytes, 35 classes), оставляя селективный interval слишком грубым.
Диагностический тест после измерения удалён.

Повторные отдельные процессы одного shared-key Lossy50 benchmark при неизменном
486 463-byte exact baseline выбрали 236 424–243 064 accounted bytes и
2,466–3,586 candidates/query. Внутри одного процесса план стабилен. Значит, в
build/streaming pipeline остаётся зависимость от process-local map/hash order;
она меняет последовательность pressure downgrade и является самостоятельным
нарушением deterministic-build gate. Точный map-order участок ещё не
локализован; это измеренный эффект, а не предположение о конкретной функции.

## CPU и allocation profiles трёх режимов

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkSharedKeyBaseline/<Mode>/IndexSearch$' \
  -benchtime=5s -count=1 -cpuprofile=/tmp/ruleix-step6-<mode>.cpu .
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkSharedKeyBaseline/<Mode>/IndexSearch$' \
  -benchtime=1s -count=1 -memprofile=/tmp/ruleix-step6-<mode>.alloc \
  -memprofilerate=1 .
```

`<Mode>` заменяется на `Exact`, `IdentityLossy`, `Lossy50`. Exact и identity
сохранили один search allocation class: 23 497–23 498 B/op и 20 allocs/op.
Lossy50 в выбранном CPU процессе получил 19 105 B/op, 20 allocs/op, 241 415
accounted bytes и 2,466 candidates/query; отдельный allocation process —
16 577 B/op, 18 allocs/op, 243 064 bytes и 3,069 candidates/query.

В CPU profiles Exact/identity/Lossy50 Roaring `union2by2` занимал соответственно
23,2%, 40,2% и 67,6% flat samples. Allocation-space profiles во всех режимах
сосредоточены в Roaring array container, `Bitmap.AndAny`/clone и build-time
`equalityIndex.addSet`; профили включают setup benchmark-а, поэтому абсолютные
MB не используются как retained-memory результат.

## Пройденные gates

```sh
go test ./...
go test -race ./...
go test -run \
  '^(TestLossyExactDifferentialEverySupportedRule|TestIdentityLossyDifferentialEverySupportedRule|TestProductionShapeLossyNeverDropsExactMatches|TestProductionShapeStreamingLossyKeepsExactMatches|TestLossy.*Streaming.*|TestEveryStreamingRepresentationPreparesAndAppliesOneDowngrade)$' \
  -count=1 .
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShape(RetainedMemory|ShuffledRetainedMemory|LocalRetainedMemory)$' \
  -benchtime=1x -count=3 .
```

Full, race, differential, identity, production superset и streaming fixtures
прошли. Retained measurements стабильны во всех трёх повторениях: 1 322 832
B/index ordered, 1 310 752 B/index shuffled; Local — 2 968 B cold, 92 432 B
warm, 111 056 B adaptive и 74 256 B adversarial.

## Детерминизация string equality 2026-09-02

Расследование показало, что межпроцессная вариативность вызвана не порядком
обхода Go map, а `hash/maphash.MakeSeed()` в string codec. Один string получал
разные lossy buckets и физический план в каждом процессе. Codec переведён на
стабильный tagged FNV; обычные, именованные и вложенные строки используют один
контракт. Build-time map inputs дополнительно упорядочены: equality candidates
по `(hash, insertion offset)`, equality classes по physical source pair,
posting rebuild по ключу.

Три отдельных Lossy50-запуска дали одинаковые 220 790 accounted bytes и 3,155
candidates/query. Серия `300ms x5`: median 70 788 ns/op, 25 720 B/op, 20
allocs/op; warm Local 64,04 ns/op, 0 B/op, 0 allocs/op. Exact/identity остались
в одном классе: 51 328/51 846 ns/op и 61,15/60,81 ns/op Local. Production
Lossy: 47 770 ns/op и 5 482 ns/op Local. Deterministic-build gate закрыт, но
общий performance gate шага 6 остаётся открыт.

## Возможные пути решения

Решение владельца от 2026-09-02 задаёт search-first порядок: latency и
allocations `Local.Search`/`Index.Search` приоритетнее стоимости Build. Более
дорогие build-time анализ, выбор quantizer-а и allocations допустимы, если
улучшают оба публичных search path; hard retained-memory limit, correctness и
streaming downgrade остаются обязательными.

Exact и Lossy не могут расходиться внутри дерева или executor-а. Lossy остаётся
только build-скомпилированным преобразованием ключа. Любые новые layout,
membership metadata, matcher, routing и cache допустимы лишь как универсальные
механизмы общего индекса, используемые тем же кодом также для Exact. Отдельные
lossy branches, search types и проверки режима запрещены.

1. Добавить quality-aware score aggregate planner-а:
   учитывать ожидаемую candidate amplification вместе с released bytes, не
   меняя hard retained cap. Проверять на production, mixed shared-key и
   adversarial distributions, чтобы не оптимизироваться под один запрос.
2. Сделать common `orderedIndex` компактнее: хранить immutable ordered items
   плотным массивом вместо per-item pointers/objects. Освобождённый retained
   budget позволит сохранить больше interval classes без возврата отдельного
   lossy search type.
3. Добавить auxiliary compact membership metadata к common ordered blocks,
   если CPU profiles после улучшения planner-а всё ещё показывают
   `matchesChildID`; metadata должна входить в accounting и использоваться
   одинаковым matcher-ом, а не создавать отдельный lossy engine.

Equality hash проверяется отдельно от ordered quantization. Для equality gate
нужно сравнивать weighted bucket collisions, максимальный posting, estimated
false-positive rate и candidates/query на реально выбранных precision levels.
Смена стабильного hash или build-selected salt не считается исправлением без
end-to-end выигрыша. Для ordered правил hash отсутствует: следующий quantizer
должен минимизировать расширение postings у outward-rounded lower/upper
границ, сохраняя key-only повторное огрубление.

До реализации и сопоставимого повторного профилирования принятого решения шаг
6 нельзя завершить.

## Ordered precision shape 2026-09-02

Добавлен диагностический `BenchmarkProductionOrderedPrecisionShape`. Он
изолирует фактические lower/upper поля production `Between`, отдельно проверяет
strict/inclusive направления и все пять операторов `CompareBy`. В отличие от
двух соседних запросов search-attribution, отчёт проходит 365 наблюдаемых
временных границ и пять version keys. `ns/op` включает сборку и серию запросов
и не является performance gate.

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionOrderedPrecisionShape/' \
  -benchtime=1x -count=1 .
```

Среда: Apple M1 Max, macOS arm64, Go 1.26.0, `GOMAXPROCS=1`, 38 098 entries.
Результаты первого сопоставимого запуска:

| Граница | Budget | Classes | Exact candidates/query | Lossy candidates/query | Amplification |
|---|---:|---:|---:|---:|---:|
| lower GT strict | 75% / 50% | 2 / 1 | 19 048 | 34 311 / 37 994 | 1,801x / 1,995x |
| lower GTE inclusive | 75% / 50% | 2 / 1 | 19 152 | 34 415 / 38 098 | 1,797x / 1,989x |
| upper LT strict | 75% / 50% | 2 / 1 | 18 970 | 28 447 / 37 994 | 1,500x / 2,003x |
| upper LTE inclusive | 75% / 50% | 2 / 1 | 19 074 | 28 551 / 38 098 | 1,497x / 1,997x |
| CompareBy EQ | 75% / 57% | 1 / 1 | 33 168 | 35 633 / 35 633 | 1,074x / 1,074x |
| CompareBy LT/GT strict | 75% / 57% | 1 / 1 | 34 400 | 35 633 / 35 633 | 1,036x / 1,036x |
| CompareBy LTE/GTE inclusive | 75% / 57% | 1 / 1 | 35 633 | 36 865 / 36 865 | 1,035x / 1,035x |

Lower и upper уже расходятся на двух классах: equal-key-count разбиение даёт
примерно 1,80x против 1,52x, поэтому направление outward rounding должно
участвовать в выборе соседних классов. Strict/inclusive внутри одного
направления отличаются только ожидаемой boundary cardinality и требуют одного
общего алгоритма с явной семантикой включения.

Одновременно измерение ограничивает следующий эксперимент: на production
50%-точке standalone ordered и на обеих проверенных точках `CompareBy` выбран
ровно один класс. При одном классе перестановка внутренних границ невозможна;
она не исправит около 2x standalone amplification. Значит, улучшение leaf
quantizer нужно проверять на многоклассовой 75%-точке, а для 50% после этого
необходим quality-aware aggregate planner, сохраняющий больше одного класса у
селективного ordered leaf. Это измеренный structural limit, а не гипотеза о
конкретной scoring formula.

Проверки изменения: `go test ./...`, `go test -race ./...` и
`git diff --check` прошли. `make lint` остаётся красным на 26 ранее
существовавших замечаниях в не изменённых этим экспериментом файлах (13 `lll`,
4 `gocognit`, 4 `nestif`, 2 `staticcheck`, 2 `unconvert`, 1 `revive`); новый
benchmark дополнительных замечаний не добавил.

## Equality precision shape 2026-09-02

Добавлен test-only физический отчёт `BenchmarkProductionEqualityPrecisionShape`.
Он не расширяет публичный API и для реально выбираемых production budget points
показывает число buckets, распределение posting cardinality (median/p95/max по
листьям), weighted collision cost, observed query false-positive rate и
candidates/query. Воспроизведение:

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionEqualityPrecisionShape/' \
  -benchtime=1x -count=1 .
```

Среда: Apple M1 Max, macOS arm64, Go 1.26.0, `GOMAXPROCS=1`, 38 098 entries.
Результаты физической формы детерминированы сборкой; `ns/op` этого diagnostic
benchmark не является search-метрикой и не используется как performance gate.

| Budget | Accounted | Buckets | Candidates/query | Max leaf median/p95/max | Weighted collision | Observed query FP |
|---:|---:|---:|---:|---:|---:|---:|
| 100% | 419 451 B | exact | 150 | exact | exact | 0 |
| 75% | 284 665 B | 534 | 1 931 | 29 137 / 29 137 / 29 137 | 13 067 | 0,04693 |
| 50% | 140 347 B | 6 | 38 098 | 38 097 / 38 097 / 38 097 | 34 764 | 1,000 |

Три posting-квантили в таблице — отдельные maxima соответствующей метрики среди
листьев; они показывают worst-leaf форму, а не общую CDF всех buckets.

Измерение исключает stable-hash/salt как самостоятельное исправление 50%-точки:
planner оставляет всего шесть классов на весь equality-only `All`, а один
выбранный posting содержит 38 097 из 38 098 IDs. Перестановка hash не может
разделить posting при одном классе. Следующий обоснованный эксперимент —
quality-aware aggregate planning, который должен сохранить больше классов у
селективных equality-листьев; его принятие по-прежнему требует end-to-end
улучшения обоих search paths и прохождения mixed/adversarial gates.

## Search-first атрибуция по семействам 2026-09-02

Добавлен отдельный `BenchmarkProductionShapeAttribution`, который на одинаковых
38 098 production constraints изолирует equality, standalone ordered,
`Between`, `CompareBy` и полный production `All`. Для каждого семейства
сравниваются Exact, finest/identity Lossy и половинный retained budget;
`CompareBy` не имеет представления, помещающегося в 50%, поэтому для него
используется измеренный минимально достижимый budget 57% (16 508 из 29 090
accounted bytes). Timed search использует один запрос, а candidates/query и
amplification вычисляются по двум соседним production-запросам.

Среда: Apple M1 Max, macOS arm64, Go 1.26.0, `GOMAXPROCS=1`, текущий рабочий
tree после `6855a8a`. Команда серии:

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeAttribution/' \
  -benchmem -benchtime=200ms -count=3 .
```

Медианы серии (время в ns/op):

| Семейство | Режим | Index | B/op; allocs | warm Local | Local B/op; allocs | Accounted | Candidates | Amplification |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Equality | Exact | 18 639 | 22 640; 20 | 215,6 | 0; 0 | 419 451 | 150 | 1,000x |
| Equality | identity | 25 908 | 22 640; 20 | 495,5 | 0; 0 | 419 451 | 150 | 1,000x |
| Equality | Lossy 50% | 171 993 | 66 946; 18 | 131 773 | 1 152; 2 | 140 347 | 38 098 | 254,0x |
| Standalone ordered | Exact | 94 835 | 28 120; 10 | 38 884 | 1 152; 2 | 41 168 | 10 682 | 1,000x |
| Standalone ordered | identity | 95 215 | 28 120; 10 | 38 513 | 1 152; 2 | 41 168 | 10 682 | 1,000x |
| Standalone ordered | Lossy 50% | 134 894 | 9 376; 4 | 130 888 | 1 152; 2 | 8 332 | 38 098 | 3,567x |
| Between | Exact | 103 257 | 36 584; 14 | 38 484 | 1 152; 2 | 272 592 | 10 682 | 1,000x |
| Between | identity | 104 011 | 36 584; 14 | 38 408 | 1 152; 2 | 272 592 | 10 682 | 1,000x |
| Between | Lossy 50% | 110 435 | 17 648; 7 | 98 767 | 1 152; 2 | 135 976 | 28 195 | 2,640x |
| CompareBy | Exact | 137 695 | 9 376; 4 | 125 254 | 1 152; 2 | 29 090 | 36 044 | 1,000x |
| CompareBy | identity | 138 010 | 9 376; 4 | 125 408 | 1 152; 2 | 29 090 | 36 044 | 1,000x |
| CompareBy | Lossy minimum 57% | 135 413 | 9 376; 4 | 131 642 | 1 152; 2 | 16 508 | 38 098 | 1,057x |
| Production All | Exact | 34 924 | 40 769; 28 | 196,4 | 0; 0 | 754 244 | 45 | 1,000x |
| Production All | identity | 39 937 | 40 769; 28 | 549,2 | 0; 0 | 754 244 | 45 | 1,000x |
| Production All | Lossy 50% | 69 575 | 103 881; 31 | 5 450 | 0; 0 | 376 925 | 1 722 | 38,27x |

Измерения локализуют основной candidate-quality дефект в equality: отдельный
50%-индекс выбирает единственный класс и возвращает все 38 098 правил. В полном
`All` пересечение ограничивает результат до 1 722, но это всё ещё 38,27x exact
cardinality и объясняет рост bitmap union и candidate checks. `Between` и
standalone ordered также требуют улучшения quantizer-а, однако дают существенно
меньшую amplification. Изолированный `CompareBy` на своей минимальной ступени
расширяет результат только на 5,7%; универсальную ordered membership
оптимизацию преждевременно ставить перед equality и leaf quantizers.

Finest/identity Lossy полностью совпал с Exact по accounted bytes, candidates
и allocation classes во всех семействах. Ordered, `Between` и `CompareBy`
совпали также по latency в пределах шума. Однако equality показал 25 908 против
18 639 ns/op для `Index.Search` и 495,5 против 215,6 ns/op для warm
`Local.Search`; production `All` — 39 937 против 34 924 ns/op и 549,2 против
196,4 ns/op. Это измеренное parity-нарушение требует отдельной чередующейся
серии и CPU profiles; до такой проверки причина не приписывается layout или
codec.

Следующий эксперимент по roadmap — equality-only анализ реально выбранных
ступеней: posting cardinality distribution, weighted collision cost,
максимальный bucket, estimated false-positive rate и candidates/query. После
этого можно оценивать stable hash/salt и quality-aware score только по
end-to-end `Index.Search` и `Local.Search`.

## Профилирование equality identity parity 2026-09-02

Parity-нарушение из attribution-серии воспроизведено отдельными процессами:
Exact `Index.Search` — 18 320 ns/op, identity-lossy — 25 317 ns/op при
одинаковых 419 451 accounted bytes, 150 candidates/query, 22 640 B/op и
20 allocs/op. CPU profiles сняты командами:

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeAttribution/Equality/<Mode>/IndexSearch$' \
  -benchtime=3s -count=1 -cpuprofile=/tmp/ruleix-eq-<mode>.cpu .
go tool pprof -top -nodecount=18 ./ruleix.test /tmp/ruleix-eq-<mode>.cpu
```

В identity profile `allRule.validateCandidateBitmap` занимает 28,95%
cumulative против 13,00% у Exact, `Bitmap.Contains` — 13,85% cumulative, а
`bitmapContainer.contains` — 5,51% flat против 2,08%. Одновременно оба профиля
содержат заметную работу GC и ОС (`scanObject`, `madvise`, `kevent`), поэтому
проценты не используются как самостоятельное доказательство причины.

Добавлен production gate
`TestProductionEqualityIdentityUsesExactPhysicalRuleShape`: он подтверждает
одинаковые concrete rule types, число children/execution/planning providers,
shared-wildcard groups и duplicate-bitmap metadata у Exact и identity-lossy.
Следовательно, расхождение не объясняется отдельным search type, потерянным
executor capability или отличающимся числом alias-групп.

Попытка атрибутировать профиль через root и child `Inspector.CandidateCheck`
дала ноль в обоих режимах: этот production equality workload проходит через
bitmap materialization/validation, а счётчик отражает только прямой
child-ID membership path. Диагностический benchmark не сохранён, поскольку
метрика не наблюдает исследуемую работу. Текущая локализация — различающаяся
стоимость физически одинакового bitmap validation path; до сравнения container
shape и порядка выбранных operands это остаётся измеренным симптомом, а не
установленной причиной.

## Отклонённый greedy quality score 2026-09-02

Проверен build-only selector, минимизирующий оценённый рост candidates на
освобождённый байт. Equality использовал weighted collision estimate, ordered
семейства — отношение исходных boundaries к сохранённым классам. Дополнительно
проверено ограничение знаменателя только байтами, нужными до hard limit. Код
эксперимента удалён после измерений.

На production `All` кандидат улучшил physical shape с 1 722 до 1 210
candidates/query. Сопоставимая серия против `d010cf2`, Apple M1 Max, macOS
arm64, Go 1.26.0, `GOMAXPROCS=1`, `500ms x5`, дала median `Index.Search`
48 421 → 47 081 ns/op, 95 658 → 94 042 B/op, 29 → 30 allocs/op; warm
`Local.Search` 5 482 → 4 067 ns/op, без allocations. Однако обязательный mixed
shared-key gate регрессировал: сопоставимые `3s x1` процессы дали 68 474 →
106 702 ns/op (+55,8%), 25 720 → 28 825 B/op и 20 → 23 allocs/op; accounted
shape уменьшился с 220 790 до 161 534 bytes при доступном 50%-лимите, а
candidates/query выросли 3,155 → 3,414.

CPU profiles сняты `BenchmarkSharedKeyBaseline/Lossy50/IndexSearch` с
`-benchtime=3s -cpuprofile`. В baseline/candidate Roaring `union2by2` занимал
53,27%/56,05% flat, `Bitmap.AndAny` — 58,41%/62,09% cumulative. Это связывает
регрессию с более грубой дискретной комбинацией posting representations и
дополнительной bitmap union/intersection работой, а не с runtime scoring:
selector выполнялся только во время Build.

Локальный greedy ratio отклонён: он не учитывает глобальный дискретный frontier
комбинаций и может заметно недоиспользовать retained budget. Следующий planner
эксперимент должен выбирать комбинацию уровней глобально (Pareto frontier или
bounded knapsack), сохранять детерминизм и проверяться одновременно на
production, mixed shared-key и adversarial сериях.
