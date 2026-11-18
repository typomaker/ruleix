"""Isolated repeated-value lookup experiments; transforms pinned source."""

def apply(source, mode):
    old = '''\tvar postings [batchWidth]*equalitySet
\tvar results [batchWidth]uint64'''
    if mode == "all":
        source = source.replace(old, '''\tvar keys [batchWidth]optionalValue[V]
\tvar results [batchWidth]uint64''', 1)
        start = source.index('\t\tvar posting *equalitySet')
        end = source.index('\t\tmasks[i] &= results[class]', start)
        source = source[:start] + '''\t\tkey := getOptional(r.get, values[row])
        class := count - 1
        for ; class >= 0; class-- {
            if keys[class] == key { break }
        }
        if class < 0 {
            class = count; count++
            keys[class] = key
            var posting *equalitySet
            if key.ok { posting = r.values.get(r.equalityKey(key.value)) }
            results[class] = batchEqualityMask(posting, ids, wildcard)
            if work != nil { work.maskEvaluations++ }
        }
''' + source[end:]
        return source.replace("unsafe.Sizeof(postings)", "unsafe.Sizeof(keys)", 1)
    source = source.replace(old, old + '''
    var previous optionalValue[V]
    var previousResult uint64
    havePrevious := false''', 1)
    needle = '\t\tvar posting *equalitySet\n\t\tif value, present := r.get(values[row]); present {'
    source = source.replace(needle, '''        key := getOptional(r.get, values[row])
        if havePrevious && key == previous {
            masks[i] &= previousResult
            continue
        }
        var posting *equalitySet
        if value, present := key.value, key.ok; present {''', 1)
    source = source.replace('\t\tmasks[i] &= results[class]', '''        previous, previousResult, havePrevious = key, results[class], true
        masks[i] &= previousResult''', 1)
    return source
