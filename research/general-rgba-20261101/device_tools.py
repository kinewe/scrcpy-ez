"""Use an explicitly selected online research device; never choose implicitly."""
from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
import os, subprocess

ADB=os.environ.get('ADB',research_config.adb())
def adb_output(*args):
    return subprocess.check_output([ADB,'-P',research_config.adb_port()]+list(args),timeout=30).decode('utf8',errors='replace').strip()
def select_serial():
    explicit=research_config.serial()
    online=[s.split()[0] for s in adb_output('devices').splitlines()[1:] if len(s.split())>=2 and s.split()[1]=='device']
    if explicit not in online:raise RuntimeError('Requested research device is not online')
    return explicit
