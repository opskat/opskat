"""Print rows/columns where a run of mid-gray pixels (already blurred, or text) sits."""
from PIL import Image

img = Image.open(r"C:\Users\yzq\Downloads\jev\4.png").convert("RGB")
w, h = img.size
# Path text is dark gray on white. Find rows with many dark pixels in the card.
for y in range(120, 420, 2):
    dark = []
    for x in range(120, 900):
        r, g, b = img.getpixel((x, y))
        if r < 150 and abs(r - g) < 12 and abs(g - b) < 12:
            dark.append(x)
    if len(dark) > 40:
        print(f"y={y} x={dark[0]}..{dark[-1]} n={len(dark)}")
