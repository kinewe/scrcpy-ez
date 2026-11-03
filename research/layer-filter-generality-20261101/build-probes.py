from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import os, subprocess
folder=Path(__file__).resolve().parent;root=folder.parents[1];out=folder/'out';classes=out/'classes';classes.mkdir(parents=True,exist_ok=True)
sdk=Path(str(research_config.sdk()/"platforms/android-36/android.jar"));base=research_config.build_dir()
sources=[p for p in folder.glob('*.java') if p.name!='CodecProfilingParity.java']+[root/'research/general-rgba-20261101/NameFilterLab.java']
subprocess.run([research_config.java_bin("javac"),'-encoding','UTF-8','-source','11','-target','11','-cp',str(sdk)+os.pathsep+str(base/'classes'),'-d',str(classes)]+[str(p)for p in sources],check=True)
subprocess.run([research_config.java_bin("java"),'-cp',str(research_config.sdk()/"build-tools/36.0.0/lib/d8.jar"),'com.android.tools.r8.D8','--min-api','21','--lib',str(sdk),'--classpath',str(base/'classes.jar'),'--output',str(out/'probes.zip')]+[str(p)for p in classes.rglob('*.class')],check=True)
print(out/'probes.zip')
