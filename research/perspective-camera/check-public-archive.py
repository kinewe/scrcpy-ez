"""Validate the explicitly listed public archive without opening ADB or running probes."""
from pathlib import Path
import ast
import hashlib
import ipaddress
import json
import re
import sys
from urllib.parse import unquote

ROOT = Path(__file__).resolve().parents[2]
MANIFEST = Path(__file__).with_name('PUBLIC_MANIFEST.json')
PRIVATE_DIRS = {'out', 'private-work', 'logs', 'test-results', '__pycache__', '.git'}
BINARY_SUFFIXES = {'.apk', '.jar', '.dex', '.so', '.dll', '.exe', '.zip', '.png', '.jpg', '.jpeg', '.bin', '.skp', '.whl', '.gz'}


def validate():
    errors = []
    manifest = json.loads(MANIFEST.read_text(encoding='utf8'))
    entries = manifest['files']
    seen = set()
    markdown_count = 0
    for entry in entries:
        relative = Path(entry['file'])
        path = (ROOT / relative).resolve()
        if relative.is_absolute() or not path.is_relative_to(ROOT) or relative.as_posix() in seen:
            errors.append(f'Invalid/duplicate archive path: {relative}')
            continue
        seen.add(relative.as_posix())
        if any(part in PRIVATE_DIRS for part in relative.parts) or path.suffix.lower() in BINARY_SUFFIXES or path.name.lower() in {'profiles.json', 'settings.json', 'local-config.json'} or path.name.startswith('.env'):
            errors.append(f'Private/generated artifact in manifest: {relative}')
            continue
        if not path.is_file():
            errors.append(f'Missing archive file: {relative}')
            continue
        data = path.read_bytes()
        if hashlib.sha256(data.replace(b'\r\n', b'\n')).hexdigest() != entry['sha256']:
            errors.append(f'Checksum mismatch: {relative}')
        try:
            text = data.decode('utf8')
        except UnicodeError:
            errors.append(f'Not UTF-8 text: {relative}')
            continue
        if '\ufffd' in text:
            errors.append(f'Replacement character in text: {relative}')
        if re.search(r'(?<![\w])[A-Za-z]:[\\/]', text):
            errors.append(f'Machine-specific absolute path: {relative}')
        if re.search(r'\badb-[A-Za-z0-9_-]+\._adb-tls-connect\._tcp\b', text):
            errors.append(f'Literal wireless ADB identity: {relative}')
        for candidate in re.findall(r'(?<![\w.])\d{1,3}(?:\.\d{1,3}){3}(?![\w.])', text):
            try: address = ipaddress.ip_address(candidate)
            except ValueError: continue  # Version strings are not addresses.
            if address.is_private and not address.is_loopback and not address.is_unspecified:
                errors.append(f'Literal private network address: {relative}')
                break
        if path.suffix == '.py':
            try: ast.parse(text, filename=str(relative))
            except SyntaxError as error: errors.append(f'Python syntax: {relative}: {error}')
        if path.suffix == '.md':
            markdown_count += 1
            content = re.sub(r'```[^\n]*\n.*?```', '', text, flags=re.S)
            for target in re.findall(r'\[[^\]\n]*\]\(([^\n]*?)\)', content):
                if target.startswith(('https://', 'http://', 'mailto:', '#')): continue
                name = unquote(target.strip('<>').split('#', 1)[0])
                if not name: continue
                linked = (path.parent / name).resolve()
                if not linked.is_relative_to(ROOT) or not linked.exists():
                    errors.append(f'Broken/nonportable link: {relative}: {target}')
                elif any(part in PRIVATE_DIRS for part in linked.relative_to(ROOT).parts):
                    errors.append(f'Link to unpublished raw output: {relative}: {target}')
    history = json.loads((ROOT / 'docs/perspective-camera/HISTORY_MANIFEST.json').read_text(encoding='utf8'))
    for entry in history['files']:
        path = ROOT / entry['file']
        if not path.is_file() or hashlib.sha256(path.read_bytes().replace(b'\r\n', b'\n')).hexdigest() != entry['preparedSHA256']:
            errors.append(f'Historical prepared checksum mismatch: {entry["file"]}')
    retired = manifest['historicalSessionScripts']
    for name in retired:
        if name not in seen:
            errors.append(f'Historical script missing from archive: {name}')
            continue
        first = ast.parse((ROOT / name).read_text(encoding='utf8')).body[0]
        if not (isinstance(first, ast.Raise) and isinstance(first.exc, ast.Call)
                and isinstance(first.exc.func, ast.Name) and first.exc.func.id == 'SystemExit'):
            errors.append(f'Historical script execution guard missing: {name}')
    if errors:
        for error in errors: print(error)
        return 1
    print(f'PASS: {len(entries)} text/source files, {markdown_count} Markdown documents, {len(history["files"])} historical inputs, {len(retired)} guarded session scripts; hashes, paths, links, identifiers and Python syntax checked.')
    print('No device commands, app launches, injection or deployment performed.')
    return 0


if __name__ == '__main__':
    raise SystemExit(validate())
