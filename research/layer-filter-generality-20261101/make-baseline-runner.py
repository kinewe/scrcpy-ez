raise SystemExit("HISTORICAL SESSION SCRIPT: direct execution disabled; see research/perspective-camera/README.md")
from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config

"""Adapt only host paths/ADB selection; retain the original probe and original server."""
from pathlib import Path
folder=Path(__file__).resolve().parent
source=(folder/'baseline-host-source-original.py').read_text(encoding='utf8')
changes={
 'from device_tools import ADB, select_serial':'import os\nADB="<adb-path>"\ndef select_serial():return os.environ["RC25_RESEARCH_SERIAL"]',
 '[adb]+list(args)':'[adb,"-P","5038"]+list(args)',
 "[adb,'-s',serial,'shell'":"[adb,'-P','5038','-s',serial,'shell'",
 "str(folder/'out/probes.zip')":"str(folder/'out/baseline-original-probes.zip')",
 "str(research_config.build_dir() / 'scrcpy-server')":"str(folder/'before-product/scrcpy-server.jar')",
 'rc25-general-probes.zip':'rc25-scope-baseline-probes.zip',
 'rc25-general-base.jar':'rc25-scope-baseline-base.jar',
}
for before,after in changes.items():
    assert before in source,before
    source=source.replace(before,after)
compile(source,'baseline-wire.py','exec')
(folder/'baseline-wire.py').write_text(source,encoding='utf8')
print('Baseline runner created; device code and original server are unchanged.')
