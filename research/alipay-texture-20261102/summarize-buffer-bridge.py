from pathlib import Path
import json
folder=Path(__file__).resolve().parent;out=folder/'out';results=[]
for label in ['k80-skpwire','pad-skpwire']:
 result=dict(label=label)
 for role in ['producer','consumer']:
  text=(out/('buffer-bridge-'+label+'-'+role+'.log')).read_text(errors='replace')
  prefix='BUFFER_BRIDGE_'+role.upper()+'_JSON '
  result[role]=json.loads(next(line[len(prefix):] for line in text.splitlines() if line.startswith(prefix)))
  assert 'BUFFER_BRIDGE_'+role.upper()+'_RELEASED' in text
 c=result['consumer'];p=result['producer']
 assert c['received']==p['acknowledged']==p['submitted']==24
 assert all(c[key]==0 for key in ['crcErrors','bufferIdErrors','sequenceErrors','alphaBandSampleErrors'])
 assert c['distinctRgbaCrc']==24 and c['skpReplayMatchesLastSourceCrc']
 assert c['producerPid']==p['pid'] and p['consumerPid']==c['pid'] and p['pid']!=c['pid']
 assert not c['targetAppAccessed'] and not p['targetAppAccessed']
 results.append(result)
(out/'buffer-bridge-summary.json').write_text(json.dumps(results,ensure_ascii=False,indent=2),encoding='utf8')
print('VALIDATED: two devices, 48 shared RGBA frames, two serialized command replays; owned peers only')
