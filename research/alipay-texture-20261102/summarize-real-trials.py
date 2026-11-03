"""Summarize actual PC casting trials without printing raw device logs."""
from pathlib import Path
import json
import statistics

out = Path(__file__).resolve().parent / 'out'
summary = []
for path in sorted(out.glob('*cast*.log')):
    text = path.read_text(encoding='utf-8-sig', errors='replace')
    lines = [s.partition('WINDOW_ADAPTATION_JSON ')[2] for s in text.splitlines() if s.startswith('WINDOW_ADAPTATION_JSON ')]
    if not lines:
        continue
    data = json.loads(lines[-1])
    result = {'log': path.name, 'display': data['scene']['display'], 'size': [data['scene']['width'], data['scene']['height']], 'stages': []}
    for trial in data['trials']:
        if 'frames' not in trial:
            continue
        frames = trial['frames']
        timings = [f.get('captureReadMs', f.get('elapsedMs')) for f in frames]
        positives=[f for f in frames if f['min']<255]
        audits=[f for f in positives if 'auditAlphaErrors' in f]
        result['stages'].append({'name': trial.get('name', trial.get('label')), 'frames': len(frames), 'nonOpaqueFrames': len(positives), 'minAlpha': min(f['min'] for f in frames), 'maxAlpha': max(f['max'] for f in frames), 'distinctRgbaCrc': trial.get('distinctRgbaCrc'), 'medianCaptureReadMs': round(statistics.median(timings), 3), 'positiveSamplesFirstEight': [f.get('sample', i) for i, f in enumerate(frames) if f['min'] < 255][:8], 'readbackValidatedOnPositive': all(f.get('alphaExact', False) for f in positives) if positives else None, 'individuallyAuditedPositiveFrames':len(audits), 'maxPerFrameCpuAlphaErrors':max((f['auditAlphaErrors'] for f in audits), default=None), 'maxPerFrameCpuRgbError':max((f['auditMaxRgbError'] for f in audits), default=None)})
    result['restored'] = 'ADAPTATION_RESTORE' in text and 'RESOURCES_RELEASED' in text
    result['freeformVerified'] = next((s for s in text.splitlines() if s.startswith('FREEFORM_TRIAL ')), None)
    summary.append(result)
(out / 'real-cast-120-summary.json').write_text(json.dumps(summary, ensure_ascii=False, indent=2), encoding='utf8')
print(json.dumps(summary, ensure_ascii=False, indent=2))
