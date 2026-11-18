"""Build isolated variants from 053c15c; normal API never gains mode flags."""
from pathlib import Path
import lookup

HERE = Path(__file__).resolve().parent
VARIANTS = ["Base", "LookupAll", "Lookup", "Order", "OrderEmpty", "Anchor", "AnchorGuard",
            "Common", "Adaptive5", "Adaptive8", "LookupOrder", "LookupCommon", "LookupGuard", "LookupHybrid", "OrderLarge", "CommonReject", "AdaptiveBuild", "LookupSelect", "LookupWide", "BuildSelect", "BaseDeep", "LookupSelectDeep", "LookupWideDeep", "AdaptiveBuildDeep", "BuildSelectDeep", "AdaptivePrepared", "PreparedSelect", "AdaptivePreparedDeep", "PreparedSelectDeep", "LookupSelect64", "LookupSelect64Deep", "PreparedSelect64", "PreparedSelect64Deep", "AcceptedDeep", "BaseFinalDeep", "Accepted2Deep", "Accepted3Deep", "Accepted4Deep", "AcceptedShortDeep"]


def prepare(source, name):
    if name.endswith("Deep"): name = name.removesuffix("Deep")
    if name == "BaseFinal": name = "Base"
    if name == "PreparedSelect64":
        prepare(source, "LookupSelect64")
        prepare(source, "AdaptivePrepared")
        return
    if name == "PreparedSelect":
        prepare(source, "LookupSelect")
        prepare(source, "AdaptivePrepared")
        return
    if name == "BuildSelect":
        prepare(source, "LookupSelect")
        prepare(source, "AdaptiveBuild")
        return
    batch = (source / "search_batch.go").read_text()
    typed = (source / "batch_typed.go").read_text()
    if name.startswith("Lookup") and name not in ("LookupGuard", "LookupHybrid", "LookupSelect", "LookupWide", "LookupSelect64"):
        typed = lookup.apply(typed, "all" if name == "LookupAll" else "previous")
    if name in ("LookupGuard", "LookupHybrid", "LookupSelect", "LookupWide", "LookupSelect64"):
        original = typed
        optimized = lookup.apply(typed, "all" if name in ("LookupHybrid", "LookupWide") else "previous")
        begin = optimized.index("func (r *eqRule[T, V, K]) applyBatchMasks")
        end = optimized.index("// A compiled Between", begin)
        helper = optimized[begin:end].replace(") applyBatchMasks(", ") trialRepeatedMasks(")
        if name == "LookupHybrid":
            # Keep a bounded eight-value admission cache. After an overflow,
            # use the original pointer classes instead of scanning N raw keys.
            helper = helper.replace("var keys [batchWidth]optionalValue[V]", "var keys [8]optionalValue[V]\n var postings [batchWidth]*equalitySet\n unique:=true")
            a = helper.index("        class := count - 1")
            b = helper.index("\t\tmasks[i] &= results[class]", a)
            helper = helper[:a] + '''
        class := count-1
        if unique {
            for ; class>=0;class-- {if keys[class]==key {break}}
            if class>=0 {masks[i]&=results[class];continue}
            if count==8 {unique=false}
        }
        var posting *equalitySet
        if key.ok {posting=r.values.get(r.equalityKey(key.value))}
        if !unique {class=count-1;for ;class>=0;class-- {if postings[class]==posting {break}}}
        if class<0 {
            class=count;count++
            if unique {keys[class]=key}
            postings[class]=posting
            results[class]=batchEqualityMask(posting,ids,wildcard)
            if work!=nil {work.maskEvaluations++}
        }
''' + helper[b:]
        (source / "trial_lookup.go").write_text("package ruleix\nimport \"unsafe\"\n" + helper)
        needle = "\tvar postings [batchWidth]*equalitySet"
        condition = "len(rows)>=16 && unsafe.Sizeof(*new(V))<=32"
        if name in ("LookupSelect", "LookupSelect64"):
            condition += " && getOptional(r.get,values[rows[0]])==getOptional(r.get,values[rows[1]])"
        if name == "LookupSelect64":
            condition = condition.replace("len(rows)>=16", "len(rows)>=64")
        if name == "LookupWide":
            condition = "len(rows)>=64 && unsafe.Sizeof(*new(V))<=32"
        typed = original.replace(needle, '''    if CONDITION {
        r.trialRepeatedMasks(values,rows,ids,masks,work)
        return
    }
'''.replace("CONDITION", condition) + needle, 1)
    if name in ("Order", "OrderEmpty", "OrderLarge", "LookupOrder"):

        helper = (HERE / "order.go.txt").read_text()
        if name == "OrderEmpty":
            helper = '''package ruleix
func trialOrderedMasks[T any](root *allRule[T],values []T,rows []int,ids []uint32,masks []uint64,anchor int,work *batchWork) {
 for child:=range root.children {
  if child==anchor {continue}
  if batchMask(root,child,values[rows[0]],ids,batchAllBits(len(ids)),work)!=0 {continue}
  if batchMask(root,child,values[rows[len(rows)-1]],ids,batchAllBits(len(ids)),work)!=0 {continue}
  root.children[child].(batchMaskOperand[T]).applyBatchMasks(values,rows,ids,masks,work)
  alive:=false;for _,mask:=range masks {alive=alive||mask!=0};if !alive {return}
  break
 }
 batchTypedMasks(root,values,rows,ids,masks,anchor,work)
}
'''
        if name == "OrderLarge":
            helper = helper.replace(" var order [8]int", " if len(rows)<16 {batchTypedMasks(root,values,rows,ids,masks,anchor,work);return}\n var order [8]int")
        (source / "trial_order.go").write_text(helper)
        batch = batch.replace("batchTypedMasks(root, values, rows, ids, masks, anchor, work)",
                              "trialOrderedMasks(root, values, rows, ids, masks, anchor, work)")
    if name in ("Anchor", "AnchorGuard"):
        (source / "trial_anchor.go").write_bytes((HERE / "anchor.go.txt").read_bytes())
        if name == "Anchor":
            batch = batch.replace("batchAnchor(root, values[0], work)", "trialAnchor(root, values, work)")
        else:
            needle = "\tif anchor < 0 || card > batchCandidateLimit {"
            batch = batch.replace(needle, '''    if card > batchCandidateLimit { anchor,source,card = trialAnchor(root,values,work) }
''' + needle, 1)
            needle = "\tsource.batchPostings(values, postings, work)"
            batch = batch.replace(needle, needle + '''
    if postings[0] != postings[len(postings)-1] {
        next,operand,size:=trialAnchor(root,values,work)
        if next>=0 && next!=anchor && size<=batchCandidateLimit {
            anchor,source=next,operand
            source.batchPostings(values,postings,work)
        }
    }''', 1)
    if name in ("Common", "CommonReject", "LookupCommon"):
        common = (HERE / "common.go.txt").read_text()
        if name == "CommonReject":
            common = common.replace(" key:=getOptional(r.get,values[rows[0]])", " if r.applyCandidateMask(values[rows[0]],ids,batchAllBits(len(ids)))!=0 {return 0,false}\n key:=getOptional(r.get,values[rows[0]])")
            common = common.replace(" first:=values[rows[0]]", " first:=values[rows[0]]\n if r.applyCandidateMask(first,ids,batchAllBits(len(ids)))!=0 {return 0,false}")
            common = common.replace("if len(rows)<8", "if len(rows)<32")
        (source / "trial_common.go").write_text(common)
        begin = typed.index("func batchTypedMasks")
        end = typed.index("// Posting identity", begin)
        shared = typed[begin:end].replace("func batchTypedMasks", "func trialSharedMasks")
        shared = shared.replace("anchor int, work *batchWork", "anchor int, skip uint8, work *batchWork")
        shared = shared.replace("child == anchor ||", "child == anchor || skip&(uint8(1)<<child)!=0 ||")
        (source / "trial_shared.go").write_text("package ruleix\n\n" + shared)
        needle = "\tids := batchPostingIDs(posting, scratch.ids[:0])"
        batch = batch.replace(needle, needle + '''
    ids,skip := trialCommonCandidates(root,values,rows,ids,anchor)
    if len(ids)==0 {return}''', 1)
        batch = batch.replace("batchTypedMasks(root, values, rows, ids, masks, anchor, work)",
                              "trialSharedMasks(root, values, rows, ids, masks, anchor, skip, work)")
    if name == "AdaptivePrepared":
        (source / "trial_short.go").write_bytes((HERE / "short-prepared.go.txt").read_bytes())
        all_file = source / "all.go"
        all_file.write_text(all_file.read_text().replace("compiledEqualityClasses bool", "compiledEqualityClasses bool\n shortBatchAnchor uint8"))
        masks = source / "local_candidate_mask.go"
        masks.write_text(masks.read_text().replace("r.narrowMaskPrepared = equality != 0", '''r.narrowMaskPrepared = equality != 0
    best:=uint64(batchCandidateLimit+1)
    for child,rule:=range r.children {
        if source,ok:=rule.(trialShortSource);ok {
            if bound:=source.trialShortBound();bound<best {best=bound;r.shortBatchAnchor=uint8(child+1)}
        }
    }
'''))
        start = batch.index("func (ix *Index[C, ID]) searchBatchChunk(")
        end = batch.index("// Only exact equality", start)
        helper = batch[start:end].replace("searchBatchChunk(", "searchPreparedBatch(", 1)
        first = helper.index("\tanchor, source, card :=")
        last = helper.index("\tscratch :=", first)
        helper = helper[:first]+'''    anchor:=int(root.shortBatchAnchor)-1
    source:=root.children[anchor].(batchPostingSource[C])
'''+helper[last:]
        (source / "trial_prepared.go").write_text('package ruleix\nimport "unsafe"\n'+helper)
        batch = batch.replace("if len(conditions) < 16 {", '''if len(conditions) < 16 {
        if len(conditions)>=5 && strategy==batchTyped {
            if root,ok:=ix.root.(*allRule[C]);ok && root.shortBatchAnchor!=0 && !local.observed {
                ix.searchPreparedBatch(root,local,conditions,found,strategy,work)
                return
            }
        }
''')
    if name == "AdaptiveBuild":
        (source / "trial_short.go").write_bytes((HERE / "short-build.go.txt").read_bytes())
        all_file = source / "all.go"
        all_file.write_text(all_file.read_text().replace("compiledEqualityClasses bool", "compiledEqualityClasses bool\n shortBatchSafe bool"))
        masks = source / "local_candidate_mask.go"
        masks.write_text(masks.read_text().replace("r.narrowMaskPrepared = equality != 0", '''r.narrowMaskPrepared = equality != 0
    for _,child:=range r.children {if source,ok:=child.(trialShortSource);ok && source.trialShortBounded() {r.shortBatchSafe=true;break}}
'''))
        batch = batch.replace("if len(conditions) < 16 {", '''if len(conditions) < 16 {
        root,ok:=ix.root.(*allRule[C])
        if len(conditions)>=5 && ok && root.shortBatchSafe && !local.observed {
            ix.searchBatchChunk(root,local,conditions,found,strategy,work)
            return
        }
''')
    if name in ("Adaptive5", "Adaptive8"):

        threshold = 5 if name == "Adaptive5" else 8
        batch = batch.replace("len(conditions) < 16", "len(conditions) < " + str(threshold))
        needle = "\tscratch := batchScratchPool.Get().(*batchScratch)"
        batch = batch.replace(needle, '''    if len(values)<16 && card>8 {batchLocalSearch(local,values,found,nil,work);return}
''' + needle, 1)
    (source / "search_batch.go").write_text(batch)
    (source / "batch_typed.go").write_text(typed)
