"""Report added/modified production statement blocks from a Go cover profile."""
import re,subprocess,json,sys
from pathlib import Path
lines={};file=None
for line in subprocess.check_output(['git','diff','053c15c','--unified=0','--','*.go'],text=True).splitlines():
 if line.startswith('+++ b/'):
  file=line[6:]
 elif line.startswith('@@') and file and not file.endswith('_test.go'):
  m=re.search(r'\+(\d+)(?:,(\d+))?',line);start=int(m[1]);n=int(m[2] or 1);lines.setdefault(file,set()).update(range(start,start+n))
covered=total=0;details=[]
for line in Path(sys.argv[1]).read_text().splitlines()[1:]:
 loc,n,count=line.split();name,span=loc.split(':');file=name.removeprefix('github.com/typomaker/ruleix/');start,end=span.split(',');a,b=int(start.split('.')[0]),int(end.split('.')[0])
 if any(i in lines.get(file,set()) for i in range(a,b+1)):
  n=int(n);count=int(count);total+=n;covered+=n if count else 0;details.append({'file':file,'span':span,'statements':n,'covered':bool(count)})
result={'covered':covered,'statements':total,'percent':round(100*covered/total,2),'blocks':details};print(json.dumps(result,indent=2));assert covered/total>=.9
