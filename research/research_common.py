"""Local configuration for perspective-camera research; never selects a device implicitly."""
from pathlib import Path
import json
import os
import re
import shutil
import sys

REPOSITORY = Path(__file__).resolve().parent.parent


def env_path(name, default=None):
    value = os.environ.get(name) or default
    if value is None:
        raise SystemExit(f'Set {name}; see research/perspective-camera/README.md')
    return Path(os.path.expandvars(str(value))).expanduser().resolve()


def executable(env_name, command):
    value = os.environ.get(env_name) or shutil.which(command)
    if not value:
        raise SystemExit(f'Set {env_name} or add {command} to PATH')
    return str(value)


def adb():
    return executable('ADB', 'adb')


def adb_port():
    value = os.environ.get('RC25_RESEARCH_ADB_PORT', '5037')
    if not value.isdecimal() or not 1 <= int(value) <= 65535:
        raise SystemExit('RC25_RESEARCH_ADB_PORT must be between 1 and 65535')
    return value


def validate_serial(value):
    if not isinstance(value, str) or not re.fullmatch(r'[A-Za-z0-9_.:\[\]%-]+', value):
        raise SystemExit('An explicit valid ADB serial is required')
    return value


def serial():
    # A few existing runners parse this option after their initial configuration.
    if '--serial' in sys.argv:
        index = sys.argv.index('--serial') + 1
        if index >= len(sys.argv):
            raise SystemExit('--serial needs a value')
        return validate_serial(sys.argv[index])
    return validate_serial(os.environ.get('RC25_RESEARCH_SERIAL'))


def devices():
    value = os.environ.get('RC25_RESEARCH_DEVICES')
    if not value:
        return [('device', serial())]
    try:
        data = json.loads(value)
    except json.JSONDecodeError as error:
        raise SystemExit('RC25_RESEARCH_DEVICES must be a JSON label-to-serial object') from error
    if not isinstance(data, dict) or not data:
        raise SystemExit('RC25_RESEARCH_DEVICES must contain at least one device')
    result = []
    for label, device in data.items():
        if not re.fullmatch(r'[A-Za-z0-9_-]+', label):
            raise SystemExit('Device labels may contain letters, digits, underscore and hyphen')
        result.append((label, validate_serial(device)))
    if len({device for _, device in result}) != len(result):
        raise SystemExit('Each device serial must appear only once')
    return result


def serials():
    return [device for _, device in devices()]


def product():
    return env_path('SCRCPY_EZ_PRODUCT_ROOT', REPOSITORY)


def build_dir():
    return env_path('SCRCPY_EZ_BUILD_DIR', product() / 'build-rc25/out/rc25')


def runtime():
    return env_path('SCRCPY_EZ_RUNTIME')


def sdk():
    return env_path('ANDROID_SDK_ROOT', os.environ.get('ANDROID_HOME'))


def java_bin(name):
    home = os.environ.get('JAVA_HOME')
    return str(Path(home) / 'bin' / (name + ('.exe' if os.name == 'nt' else ''))) if home else executable(name.upper(), name)


def mingw():
    return env_path('MINGW_ROOT')


def gcc():
    root = os.environ.get('MINGW_ROOT')
    return str(Path(root) / 'bin/gcc.exe') if root else executable('CC', 'gcc')


def reference_build():
    return env_path('SCRCPY_EZ_REFERENCE_BUILD')


def codec_prefix():
    return env_path('SCRCPY_EZ_CODEC_PREFIX')


def archive_dir():
    return env_path('RC25_RESEARCH_ARCHIVE_DIR', REPOSITORY / 'research/perspective-camera/out/packages')
