"""Collect paired CPU/allocation evidence for a focused regression or win."""
import argparse
import os
from pathlib import Path
import subprocess

p=argparse.ArgumentParser()
p.add_argument("scratch",type=Path)
p.add_argument("case")
p.add_argument("variants",nargs="+")
p.add_argument("--count",type=int,default=50000)
p.add_argument("--label",default="profile")
a=p.parse_args()
env={**os.environ,"GOMAXPROCS":"1","RULEIX_TRIAL_MODES":a.case,"RULEIX_TRIAL_PROFILE":"1"}
for variant in a.variants:
 stem=f"{a.label}-{a.case}-{variant}"
 args=[str(a.scratch/(variant+".test")),"-test.run","^$","-test.bench","^BenchmarkPlannerProduction$",
       f"-test.benchtime={a.count}x","-test.benchmem","-test.cpuprofile="+str(a.scratch/(stem+".cpu")),
       "-test.memprofile="+str(a.scratch/(stem+".mem"))]
 print(stem,flush=True)
 with (a.scratch/(stem+".txt")).open("w") as f:subprocess.run(args,env=env,stdout=f,stderr=subprocess.STDOUT,check=True)
 for kind,flags in [("cpu",[]),("mem",["-alloc_space"])]:
  with (a.scratch/(stem+"-"+kind+"-top.txt")).open("w") as f:
   subprocess.run(["go","tool","pprof","-top","-nodefraction=0.02",*flags,str(a.scratch/(variant+".test")),str(a.scratch/(stem+"."+kind))],stdout=f,stderr=subprocess.STDOUT,check=True)
