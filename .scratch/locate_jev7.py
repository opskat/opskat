from PIL import Image

img = Image.open(r"C:\Users\yzq\Downloads\jev\7.png").convert("RGB")
# Asset column sits between 工具 and 命令. Sample one data row.
for y in (175, 200, 230):
    runs = []
    start = None
    for x in range(300, 900):
        r, g, b = img.getpixel((x, y))
        dark = r < 170 and abs(r - g) < 20
        if dark and start is None:
            start = x
        elif not dark and start is not None:
            if x - start > 8:
                runs.append((start, x))
            start = None
    print(y, runs[:12])
