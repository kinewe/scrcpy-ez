"""Read DEX metadata and selected code references from the device's public framework JAR.

This is a bounded reference inventory, not a decompiler. Referenced literals/calls
are decoded on Dalvik instruction boundaries. No target application is modified.
"""
from pathlib import Path
import hashlib
import json
import struct
import zipfile

folder = Path(__file__).resolve().parent
archive = folder / 'out/device-framework.jar'


class Dex:
    def __init__(self, data):
        self.data = data
        assert data[:4] == b'dex\n'
        self.strings = []
        count, offset = self.u32(56), self.u32(60)
        for i in range(count):
            pos = self.u32(offset + 4*i)
            _, pos = self.uleb(pos)
            self.strings.append(data[pos:data.index(b'\0', pos)].decode('utf8', errors='replace'))
        self.types = [self.strings[self.u32(self.u32(68)+4*i)] for i in range(self.u32(64))]
        self.methods = []
        for i in range(self.u32(88)):
            owner, proto, name = struct.unpack_from('<HHI', data, self.u32(92)+8*i)
            self.methods.append(self.types[owner] + '->' + self.strings[name])

    def u32(self, pos):
        return struct.unpack_from('<I', self.data, pos)[0]

    def uleb(self, pos):
        result = 0
        for shift in range(0, 35, 7):
            b = self.data[pos]
            pos += 1
            result |= (b & 127) << shift
            if b < 128:
                return result, pos
        raise ValueError('invalid uleb128')

    def refs(self, offset):
        count = self.u32(offset+12)
        units = struct.unpack_from('<'+'H'*count, self.data, offset+16)
        i, strings, calls = 0, [], []
        width2 = {2,5,8,0x13,0x15,0x16,0x19,0x1a,0x1c,0x1f,0x20,0x22,0x23,0x29,0xfe,0xff}
        width2.update(range(0x2d,0x3e))
        width2.update(range(0x44,0x6e))
        width2.update(range(0x90,0xb0))
        width2.update(range(0xd0,0xe3))
        width3 = {3,6,9,0x14,0x17,0x1b,0x24,0x25,0x26,0x2a,0x2b,0x2c,0xfc,0xfd}
        width3.update(range(0x6e,0x73))
        width3.update(range(0x74,0x79))
        while i < count:
            op = units[i] & 255
            if op == 0 and units[i] >> 8:
                kind = units[i] >> 8
                if kind == 1: width = 4 + 2*units[i+1]
                elif kind == 2: width = 2 + 4*units[i+1]
                elif kind == 3: width = 4 + (units[i+1]*(units[i+2] | units[i+3] << 16)+1)//2
                else: raise ValueError('unknown payload')
            else:
                width = 5 if op == 0x18 else 4 if op in (0xfa,0xfb) else 3 if op in width3 else 2 if op in width2 else 1
                if op in (0x1a,0x1b):
                    index = units[i+1] if op == 0x1a else units[i+1] | units[i+2] << 16
                    strings.append(self.strings[index])
                if op in {*range(0x6e,0x73),*range(0x74,0x79),0xfa,0xfb}:
                    calls.append(self.methods[units[i+1]])
            i += width
        assert i == count, ('bad instruction alignment', i, count)
        return {'strings': sorted(set(strings)), 'calls': sorted(set(calls))}

    def selected(self):
        results = {}
        for i in range(self.u32(96)):
            off = self.u32(100) + 32*i
            owner = self.types[self.u32(off)]
            if owner not in {'Landroid/view/ViewDebug;', 'Landroid/view/ViewRootImpl$W;', 'Landroid/view/View;', 'Lcom/android/internal/view/ScrollCaptureInternal;','Lcom/android/internal/policy/DecorView;','Landroid/view/ThreadedRenderer;','Landroid/view/ViewRootImpl;'}:
                continue
            pos = self.u32(off+24)
            if not pos: continue
            counts = []
            for _ in range(4):
                value, pos = self.uleb(pos)
                counts.append(value)
            for _ in range(counts[0]+counts[1]):
                _, pos = self.uleb(pos)
                _, pos = self.uleb(pos)
            for n in counts[2:]:
                method = 0
                for _ in range(n):
                    delta, pos = self.uleb(pos)
                    _, pos = self.uleb(pos)
                    code, pos = self.uleb(pos)
                    method += delta
                    name = self.methods[method]
                    wanted = ('dispatchCommand','executeCommand','serializeReturnValue','deserializeMethodParameters','capture','performViewCapture','createSnapshot','requestCallback','detectScrollingType','dispatchScrollCaptureSearch','onScrollCaptureSearch','updateElevation','setElevation','drawableChanged','setWindowBackground','setBackgroundDrawable','updateOpacity','changeCanvasOpacity','setup','setOpaque')
                    if code and any(name.endswith('->'+x) for x in wanted):
                        results[name+'#'+str(method)] = self.refs(code)
        return results


report = {'sha256': hashlib.sha256(archive.read_bytes()).hexdigest(), 'dex': {}}
with zipfile.ZipFile(archive) as z:
    for name in z.namelist():
        if name.endswith('.dex'):
            selected = Dex(z.read(name)).selected()
            if selected: report['dex'][name] = selected
target = folder / 'out/framework-reference-inventory.json'
target.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf8')
for entries in report['dex'].values():
    for method, refs in entries.items():
        if 'dispatchCommand' in method or 'executeCommand' in method:
            print(method, json.dumps(refs, ensure_ascii=False))
print(target)
