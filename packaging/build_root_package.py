"""Build the isolated v2.2.2-root.1 ZIP from a digest-verified ez baseline.

No official install directory is read or overwritten. Supply an already built GUI.
"""
import argparse
import hashlib
import json
from pathlib import Path
import zipfile

VERSION = "v2.2.2-root.1"
BASE_COMMIT = "b680f55158a38ff8c042a57559f8930372e608e6"
BASE_DIGEST = "5b55695f2edb249db33e6b7cc5e5ecb34b16619718678e35c0af429dda5920a5"


def digest(data):
    return hashlib.sha256(data).hexdigest()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--baseline", type=Path, required=True)
    parser.add_argument("--gui", type=Path, required=True)
    parser.add_argument("--source-commit", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    source = Path(__file__).resolve().parents[1]
    if digest(args.baseline.read_bytes()) != BASE_DIGEST:
        raise SystemExit("Baseline digest differs from GitHub v2.2.2 release")
    if args.output.resolve().parent != (source / "dist").resolve():
        raise SystemExit("Package output must be this branch's own dist directory")
    entries = {}
    with zipfile.ZipFile(args.baseline) as baseline:
        for entry in baseline.infolist():
            name = entry.filename
            if "/" in name or "\\" in name or name in (".", ".."):
                raise SystemExit("Unexpected non-flat baseline path")
            # Runtime dependencies are unchanged; profiles, documentation and GUI
            # are replaced with experiment-specific content.
            if name.endswith(".dll") or name in {
                "adb.exe", "scrcpy.exe", "scrcpy-server", "scrcpy-server-legacy",
                "scrcpy.png", "disconnected.png", "LICENSE",
            }:
                entries[name] = baseline.read(entry)
    for required in ("adb.exe", "scrcpy.exe", "scrcpy-server", "WebView2Loader.dll"):
        if required not in entries:
            raise SystemExit("Missing baseline component: " + required)
    unchanged = {name: digest(data) for name, data in entries.items()}
    entries["scrcpy-ez.exe"] = args.gui.read_bytes()
    entries["投屏支持.bat"] = (source / "packaging/投屏启动.bat").read_bytes()
    entries["profiles.json"] = b"{}\n"
    entries["README-ROOT.txt"] = (source / "doc/root-user-guide-v2.2.2-root.1.md").read_bytes()
    entries["ROOT-RESEARCH.md"] = (source / "doc/root-research-v2.2.2-root.1.md").read_bytes()
    entries["ROOT-VALIDATION.md"] = (source / "doc/root-validation-v2.2.2-root.1.md").read_bytes()
    entries["ROOT-EXPERIMENT.json"] = json.dumps({
        "version": VERSION,
        "repository": "https://github.com/kinewe/PC-kinewe-yinmo",
        "branch": "codex/root-tmp-repair-v2.2.2",
        "sourceCommit": args.source_commit,
        "baselineCommit": BASE_COMMIT,
        "baselineRelease": "https://github.com/kinewe/scrcpy-ez/releases/tag/v2.2.2",
        "baselineArchiveSHA256": BASE_DIGEST,
        "deviceValidated": False,
        "stableAutoUpdate": False,
        "filesSHA256": {name: digest(data) for name, data in sorted(entries.items())},
        "unchangedBaselineFilesSHA256": unchanged,
    }, ensure_ascii=False, indent=2).encode("utf-8")
    args.output.parent.mkdir(parents=True, exist_ok=True)
    # A single distinctive directory prevents accidental extraction over stable.
    with zipfile.ZipFile(args.output, "w", zipfile.ZIP_DEFLATED, compresslevel=6) as package:
        for name, data in sorted(entries.items()):
            info = zipfile.ZipInfo("yinmo-2.2.2-root.1/" + name, (2026, 10, 3, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = 0o100644 << 16
            package.writestr(info, data)
    with zipfile.ZipFile(args.output) as package:
        if package.testzip() is not None:
            raise SystemExit("Package CRC verification failed")
        for name, data in entries.items():
            if package.read("yinmo-2.2.2-root.1/" + name) != data:
                raise SystemExit("Package content differs: " + name)
    checksum = digest(args.output.read_bytes())
    args.output.with_suffix(".sha256.txt").write_text(checksum + "  " + args.output.name + "\n", encoding="ascii")
    print(json.dumps({"version": VERSION, "path": str(args.output.resolve()),
                      "sha256": checksum, "files": len(entries)}, ensure_ascii=False))


if __name__ == "__main__":
    main()
