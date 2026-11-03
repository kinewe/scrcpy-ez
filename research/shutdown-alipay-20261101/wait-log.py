from pathlib import Path
import sys,time
folder=Path(__file__).resolve().parent;label,marker=sys.argv[1:3];timeout=float(sys.argv[3]) if len(sys.argv)>3 else 20
assert label.replace('-','').isalnum();path=folder/'out'/(label+'.log');deadline=time.monotonic()+timeout
while time.monotonic()<deadline:
    text=path.read_text(encoding='utf8',errors='replace') if path.exists() else ''
    if marker in text:print('LOG_MARKER_FOUND',label,marker);break
    time.sleep(.2)
else:
    print('\n'.join(s for s in text.splitlines() if 'NATIVE_ALPHA' in s)[-5000:]);raise SystemExit(1)
