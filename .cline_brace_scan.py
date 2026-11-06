import re

lines = open(r"C:\WebSearch_GO\internal\agent\news.go", encoding="utf-8").read().splitlines()
depth = 0
for i, line in enumerate(lines, 1):
    code = re.sub(r'"[^"]*"', '', line).split('//')[0]
    depth += code.count('{') - code.count('}')
    stripped = line.strip()
    if i in (125, 126, 127, 128, 129, 130, 139, 140, 153, 154, 155, 156, 157, 158):
        print(f"{i}: depth={depth} | {line[:110]}")
    if depth == 0 and stripped and not stripped.startswith("//") and i > 3:
        print(f"TOP-LEVEL at {i}: depth={depth} | {line[:110]}")
