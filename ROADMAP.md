# Roadmap

This file is the active implementation plan. A fully completed step remains
marked here with its completion date and a concise result summary until every
step in its milestone is complete. Roadmap history is not stored in separate
files: Git preserves the committed pre-cleanup state. Release-facing behavior
belongs in [`CHANGELOG.md`](CHANGELOG.md). Partial work must not be marked as a
completed step.

## Формат шага

Каждый шаг оформляется по следующему шаблону:

```markdown
### N. Краткое название

Статус: `запланирован` | `в работе` | `завершён — YYYY-MM-DD`

Результат: краткая итоговая сводка выполненного изменения и его эффекта.

- Конкретный элемент scope.
- Конкретный элемент scope.

Gate: проверяемые критерии завершения и обязательные проверки.
```

Для статусов `запланирован` и `в работе` строка `Результат` не добавляется.
Статус `завершён` разрешён только после выполнения всего scope и Gate; дата
указывается в формате ISO `YYYY-MM-DD`, а `Результат` обязателен. Подробные
замеры, эксперименты и доказательства остаются в соответствующих канонических
документах.

## Завершение milestone и очистка roadmap

Когда все шаги milestone имеют статус `завершён`, сначала создаётся отдельный
коммит, в котором `ROADMAP.md` ещё содержит полный milestone: цель, все шаги,
даты, результаты и пройденные Gate. Этот коммит является исторической точкой
перед очисткой и должен успешно пройти применимые проверки.

Только после этого milestone удаляется из `ROADMAP.md`, а следующий roadmap
добавляется или переводится в активное состояние. Активация очищенного roadmap
фиксируется вторым отдельным коммитом. Файлы истории roadmap и архивные копии
milestone не создаются: для восстановления используется Git. Нельзя объединять
pre-cleanup snapshot и post-activation cleanup в один коммит.

## Потоковый Build для `Lossy`

Статус milestone: `активен`.

Цель — реализовать `Lossy` как однопроходное хранение единственного текущего
представления каждого правила. Пока хватает памяти, правило хранит
`exact key -> bitmap`. После pressure checkpoint всё сохранённое представление
правила целиком перекладывается через следующий вложенный уровень округления:

```text
current key -> quantize(next level) -> same physical index -> merge bitmaps
```

Текущий уровень сохраняется в правиле и одинаково применяется к последующим
insert и search values. Исходные exact keys после успешного перехода не
удерживаются. Уникальные округлённые ключи могут называться buckets/classes
только в диагностике: planner не вычисляет отдельное разбиение, не строит все
будущие representations и не сливает соседние postings по одной паре.

До последнего шага milestone запрещены performance-оптимизации, benchmark-
driven изменения layout и решения по промежуточным `ns/op`, allocations или
candidate count. Шаги 1–8 реализуют и проверяют только контракт, корректность,
детерминизм, hard memory limit и отсутствие удержания старых поколений. Все
performance benchmarks, profiles и оптимизации выполняются только в шаге 9.

### 1. Зафиксировать контракт потокового округления

Статус: `запланирован`

- Описать единый контракт current level, next level, преобразования нового
  значения и повторного преобразования уже сохранённого ключа.
- Потребовать вложенность уровней: переход `N -> N+1` без exact value должен
  совпадать с прямым округлением exact value на уровень `N+1`.
- Зафиксировать общий алгоритм checkpoint: при accounted usage выше 125%
  soft target выбрать правило с максимальным освобождением, перестроить всё
  его текущее представление и продолжить чтение input.
- Разделить retained accounting опубликованного поколения и transient память
  перестройки; hard `MemoryLimit` относится к финальному retained состоянию.
- Зафиксировать, что один universal bitmap допустим только как терминальный
  уровень, когда более точное доступное представление не помещается в hard
  limit, а не как следствие локальных последовательных merge.

Gate: контракт записан в канонической lossy-архитектуре; unit tests выражают
вложенность и монотонность уровней без performance assertions и benchmarks.

### 2. Ввести единый state и rebuild primitive

Статус: `запланирован`

- Представлять каждый lossy-лист одним mutable build index, current level и
  преобразователями insert/search key текущего уровня.
- Реализовать checked rebuild всего поколения: пройти текущие `(key, bitmap)`,
  применить следующий уровень, объединить совпавшие keys и заменить старое
  поколение только после успешной сборки нового.
- Немедленно освобождать ссылки на старое поколение и не сохранять исходный
  exact index, список exact postings или заранее построенные будущие уровни.
- Финализировать immutable aggregates, routing и search metadata только после
  последнего streaming downgrade.

Gate: повторные rebuild сохраняют каждый ID, не удерживают старые поколения,
корректно обновляют accounting и проходят targeted tests; benchmarks не
запускаются и layout не оптимизируется.

### 3. Перевести equality на вложенные уровни

Статус: `запланирован`

- Exact-фаза хранит точные значения; после первого downgrade новые значения
  сразу преобразуются текущим equality quantizer.
- Сделать hash-prefix/bucket levels вложенными, чтобы следующий storage key
  вычислялся только из текущего storage key.
- При повышении уровня полностью перекладывать текущий `equalityIndex`, сливая
  bitmap только у ключей с одинаковым новым значением.
- Удалить static representation ladder и параллельное хранение equality
  candidates из streaming path.

Gate: ordered/shuffled input, duplicates, wildcards, late values и повторные
downgrade дают `Lossy ⊇ Exact`, детерминированную форму и соблюдают hard
limit; выполняются только correctness и accounting checks.

### 4. Перевести numeric и time ordered rules

Статус: `запланирован`

- Реализовать вложенные уровни монотонного ключа с устойчивыми origin и width;
  расширение наблюдаемого диапазона не должно менять уже выбранный уровень.
- Для lower bounds округлять наружу вниз, для upper bounds — наружу вверх;
  strict/inclusive используют один уровень с корректной boundary-семантикой.
- На downgrade перекладывать все текущие keys через следующий уровень вместо
  последовательного слияния соседних postings.
- Удалить `orderedIndex.coarsenOne`, `orderedMergeExpansion`, управление через
  `lossyCapacity` и тесты, фиксирующие выбор конкретной соседней пары.

Gate: boundary/adversarial differential matrix, поздние значения за исходным
диапазоном и последовательные переходы не дают false negatives; никаких
performance conclusions на этом шаге не делается.

### 5. Поддержать arbitrary comparator через boundary levels

Статус: `запланирован`

- Для стабильного total-order `Compare[V]` строить вложенные уровни outward
  boundaries, где каждый следующий уровень является подмножеством предыдущего.
- Округлять insert и search values бинарным поиском к boundary текущего уровня;
  storage key должен позволять перейти к родительской boundary без exact value.
- Корректно включать новые значения внутри и за пределами наблюдаемого диапазона
  без перестройки ранее выбранной семантики уровня.
- Не пытаться выводить порядок из reflection/getter codec. Автоматический
  ordered codec разрешён только для типов и comparator-ов с доказанной общей
  семантикой; иначе используется boundary quantizer.
- Задокументировать требование стабильного транзитивного total order как часть
  существующего контракта пользовательского comparator-а.

Gate: пользовательские structs, strings/custom collation, composite keys,
descending order и range extremes проходят differential tests на всех уровнях;
benchmark и layout tuning отложены.

### 6. Перевести `Between` и `CompareBy`

Статус: `запланирован`

- `Between` хранит независимые current levels нижней и верхней стороны и
  перестраивает за один pressure step только одну полную сторону.
- `CompareBy` хранит уровень отдельно для `EQ`, `LT`, `LTE`, `GT`, `GTE` и
  применяет соответствующее направление outward rounding.
- Сохранить общий fused matcher/search path; lossy отличается только текущим
  преобразованием ключа и физическими postings после build-time rebuild.
- Удалить оставшиеся operator-specific pairwise coarsening и static ladder
  helpers после переноса correctness coverage.

Gate: missing values, duplicate IDs, wildcards, strict/inclusive boundaries и
все операторы сохраняют `Lossy ⊇ Exact`; identity level полностью равен
Exact. Performance не измеряется и не оптимизируется.

### 7. Переписать aggregate pressure selector

Статус: `запланирован`

- На каждом checkpoint получать для каждого доступного листа реальный либо
  точно рассчитанный `nextLevelUsage` полного следующего поколения.
- Выбирать один лист по максимальному `currentUsage - nextLevelUsage`, затем
  применять ровно один глобальный переход уровня и повторять до soft target.
- Сохранить nested `MemoryLimit`, deterministic tie-break и ошибку Build, если
  сумма терминальных представлений превышает hard limit.
- Не учитывать candidate quality, search latency или результаты benchmarks в
  selector до завершения функциональной реализации.

Gate: multi-leaf и nested-policy tests подтверждают правильный выбор по
освобождённым байтам, детерминизм, hard limit и отсутствие преждевременного
universal fallback; performance assertions отсутствуют.

### 8. Удалить устаревший streaming planner и завершить correctness gates

Статус: `запланирован`

- Удалить static leaf representation ladders, pairwise merge planner,
  universal-tail fallback и build-only состояние, противоречащее модели одного
  текущего поколения.
- Обновить `Inspect.Strategy`, `Granularity` и memory details так, чтобы они
  описывали реально опубликованный level и число текущих rounded keys.
- Добавить общий регрессионный test: если многоключевое представление реально
  помещается в hard limit, streaming Build не должен публиковать один bitmap.
- Выполнить full tests, race, differential matrix, streaming scale,
  deterministic-build, retained accounting и diff coverage.
- Обновить архитектуру и пользовательский lossy contract; промежуточные
  performance observations не использовать для изменения реализации.

Gate: все функциональные и memory gates проходят, production fixture не
переогрубляется относительно доступного hard limit, старый planner недостижим
из production code, а performance benchmarks ещё не использовались как gate.

### 9. Выполнить benchmarks, profiles и только затем оптимизацию

Статус: `запланирован`

- После завершения шагов 1–8 снять сопоставимые Exact, identity-lossy и Lossy
  серии для equality, standalone ordered, `Between`, `CompareBy`, production
  `All`, mixed shared-key, range-heavy и adversarial workloads.
- Измерить `Index.Search`, warm `Local.Search`, Build time, allocations,
  accounted retained memory, candidates/query и observed false-positive rate.
- Воспроизвести baseline/candidate interleaved runs и снять CPU/allocation
  profiles для каждого обнаруженного search regression.
- Только на этом шаге выполнять performance-оптимизации; каждая оптимизация
  должна сохранять streaming-контракт и заново проходить correctness/memory
  gates шага 8.
- Зафиксировать результаты в performance history и optimization decisions,
  обновить changelog и финальную архитектуру.

Gate: ни один публичный search path не регрессирует по корректности, latency,
allocations или retained memory; измерения воспроизводимы и документированы,
а все принятые оптимизации повторно прошли полный gate шага 8.

## Порядок поставки

Шаги выполняются небольшими коммитами строго в указанном порядке. Каждый шаг
содержит документацию, tests и измерение diff coverage для изменённого
production-кода. До шага 9 benchmarks и profiles не являются gate, а
performance-оптимизации запрещены; шаг 9 выполняет все сопоставимые измерения и
последующую оптимизацию завершённой функциональной реализации.
