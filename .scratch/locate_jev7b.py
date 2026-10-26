from PIL import Image

img = Image.open(r"C:\Users\yzq\Downloads\jev\7.png").convert("RGB")
w, h = img.size
print("size", w, h)
# Find the header row "资产" by looking for dark text clusters per row.
for y in range(100, 260, 4):
    runs = []
    start = None
    for x in range(200, 1100):
        r, g, b = img.getpixel((x, y))
        dark = max(r, g, b) < 120
        if dark and start is None:
            start = x
        elif not dark and start is not None:
            if x - start > 6:
                runs.append((start, x))
            start = None
    if runs:
        print(y, runs[:8])
