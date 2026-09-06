#!/usr/bin/env python3
"""生成 scrcpy-ez 托盘图标 assets/icon.ico（32x32，绿色圆角方块 + 白色上箭头）。
纯标准库实现（zlib+struct 手写 PNG），无 PIL 依赖。"""
import struct, zlib, os

SIZE = 32
GREEN = (0x43, 0xA8, 0x84, 255)   # --green #43A884
WHITE = (255, 255, 255, 255)
CLEAR = (0, 0, 0, 0)

def in_round(x, y, r=SIZE/2-1.5, rad=8):
    # 圆角正方形（半径 rad）
    cx = min(max(x, rad), SIZE-1-rad)
    cy = min(max(y, rad), SIZE-1-rad)
    return (x-cx)**2 + (y-cy)**2 <= rad**2 or (
        rad <= x <= SIZE-1-rad and rad <= y <= SIZE-1-rad
    )

def in_arrow(x, y):
    # 白色上箭头（三角形 + 竖杆）
    cx = SIZE/2
    top, base_y = 8, 24
    # 三角头
    if base_y-6 <= y <= base_y:
        half = (y - (base_y-6)) * 5 / 6 + 2
        if abs(x - cx) <= half:
            return True
    # 竖杆
    if base_y <= y <= base_y+3 and abs(x - cx) <= 2.5:
        return True
    return False

def png_bytes():
    rows = []
    for y in range(SIZE):
        row = bytearray(b'\x00')  # filter 0
        for x in range(SIZE):
            if in_round(x, y):
                px = WHITE if in_arrow(x, y) else GREEN
            else:
                px = CLEAR
            row += bytes(px)
        rows.append(bytes(row))
    raw = b''.join(rows)

    def chunk(typ, data):
        c = struct.pack('>I', len(data)) + typ + data
        c += struct.pack('>I', zlib.crc32(typ + data) & 0xffffffff)
        return c

    ihdr = struct.pack('>IIBBBBB', SIZE, SIZE, 8, 6, 0, 0, 0)  # 8bit RGBA
    return (b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', ihdr)
            + chunk(b'IDAT', zlib.compress(raw, 9)) + chunk(b'IEND', b''))

def ico_bytes():
    png = png_bytes()
    header = struct.pack('<HHH', 0, 1, 1)
    entry = struct.pack('<BBBBHHII', SIZE % 256, SIZE % 256, 0, 0, 1, 32, len(png), 22)
    return header + entry + png

out = os.path.join(os.path.dirname(__file__), '..', 'assets', 'icon.ico')
os.makedirs(os.path.dirname(out), exist_ok=True)
with open(out, 'wb') as f:
    f.write(ico_bytes())
print('written', out, len(ico_bytes()), 'bytes')
