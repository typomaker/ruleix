"""Final paired checks against 053c15c using the frozen accepted sources.

Run after build --variants BaseFinalDeep,AcceptedShortDeep and after all CPU-heavy
verification tools finish. This script runs all measurements sequentially.
"""
import os
from pathlib import Path
import subprocess
import sys

out=Path(sys.argv[1])
here=Path(__file__).resolve().parent
env={**os.environ,"GOMAXPROCS":"1"}
variants=["BaseFinalDeep",sys.argv[2] if len(sys.argv)>2 else "AcceptedShortDeep"]


def run(command,name,extra=None):
    print(name,flush=True)
    with (out/name).open("w") as stream:
        subprocess.run(command,env={**env,**(extra or {})},stdout=stream,stderr=subprocess.STDOUT,check=True)


for variant in variants:
    run([str(out/(variant+".test")),"-test.run","^TestPlannerLayout$","-test.v"],"layout-"+variant+".txt",
        {"RULEIX_TRIAL_LAYOUT":str(out/("layout-"+variant+".json"))})
for phase,label,count,extra in [
    ("confirm","shipped-core",40000,{}),
    ("confirm","shipped-short",40000,{"RULEIX_TRIAL_MODES":"mixed,zero,storezero,slotzero,intersectzero,predicatezero,unrelated,identical",
                                      "RULEIX_TRIAL_LENGTHS":"1,4,5,6,8,10,12,15,16"}),
    ("shapes","shipped-shapes",10000,{}),
]:
    run(["python3",str(here/"run.py"),"--phase",phase,"--label",label,"--scratch",str(out),
         "--variants",",".join(variants),"--iterations",str(count)],label+"-driver.txt",extra)
for turn,order in enumerate([variants,list(reversed(variants)),variants]):
    for variant in order:
        run([str(out/(variant+".test")),"-test.run","^$","-test.bench",
             "^BenchmarkPlannerShapes$/^8$/^observed$/^16$","-test.benchtime=80000x","-test.benchmem"],
            f"shipped-observed-check-{turn}-{variant}.txt")
for turn,order in enumerate([variants,list(reversed(variants))]):
    for variant in order:
        run([str(out/(variant+".test")),"-test.run","^$","-test.bench",
             "^BenchmarkPlanner(Build|IndexRetained)$","-test.benchtime=10x","-test.benchmem"],
            f"shipped-storage-{turn}-{variant}.txt")
        run([str(out/(variant+".test")),"-test.run","^$","-test.bench",
             "^BenchmarkPlannerProof$","-test.benchtime=10000x","-test.benchmem"],
            f"shipped-proof-{turn}-{variant}.txt")
for case in ["mixed","intersectzero"]:
    run(["python3",str(here/"profile.py"),str(out),case,*variants,"--label","profile-shipped"],f"profile-shipped-{case}-driver.txt")
for variant in variants:
    stem="profile-shipped-build-"+variant
    run([str(out/(variant+".test")),"-test.run","^$","-test.bench","^BenchmarkPlannerBuild$",
         "-test.benchtime=20x","-test.cpuprofile="+str(out/(stem+".cpu")),
         "-test.memprofile="+str(out/(stem+".mem"))],stem+".txt")
    for kind,flags in [("cpu",[]),("mem",["-alloc_space"])]:
        run(["go","tool","pprof","-top",*flags,str(out/(variant+".test")),str(out/(stem+"."+kind))],stem+"-"+kind+"-top.txt")
