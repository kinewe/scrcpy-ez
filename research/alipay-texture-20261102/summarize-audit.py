from pathlib import Path
import json

out=Path(__file__).resolve().parent/'out'
text=(out/'external-cast-freeform-audit.log').read_text(encoding='utf-8-sig')
data=json.loads(next(s.partition('WINDOW_ADAPTATION_JSON ')[2] for s in text.splitlines() if s.startswith('WINDOW_ADAPTATION_JSON ')))
audit=next(s for s in data['trials'] if s.get('label')=='same-buffer-per-frame-audit')['samples']
summary={'samples':len(audit),'gpuNonOpaque':sum(s['gpu']['min']<255 for s in audit),'cpuNonOpaque':sum(s['cpu']['min']<255 for s in audit),'forcedNonOpaque':sum(s['forcedHasAlpha']['min']<255 for s in audit),'maxAlphaErrors':max(s['alphaErrors'] for s in audit),'maxRgbError':max(s['maxRgbError'] for s in audit),'hasAlphaValues':sorted(set(s['hasAlpha'] for s in audit)),'saved':[s for s in audit if 'diagnosticPng'in s]}
(out/'real-cast-audit-summary.json').write_text(json.dumps(summary,ensure_ascii=False,indent=2),encoding='utf8')
print(json.dumps(summary,ensure_ascii=False,indent=2))
