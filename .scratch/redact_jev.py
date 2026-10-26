"""Blur sensitive regions in the Jev PR screenshots. Boxes are (x0, y0, x1, y1)."""
from PIL import Image, ImageFilter
from pathlib import Path

src = Path(r"C:\Users\yzq\Downloads\jev")
out = Path(r"D:\src_test_env\opskat\.scratch\jev-redacted")
out.mkdir(parents=True, exist_ok=True)

# Regions measured against the displayed screenshots.
boxes = {
    "4.png": [
        (150, 150, 560, 178),   # shared plugin install path
        (150, 348, 690, 376),   # claude plugin install path
    ],
    "6.png": [
        (78, 196, 250, 222),    # host address
        (150, 392, 330, 418),   # proxy hop address
    ],
    "7.png": [
        (575, 175, 760, 990),   # asset column
    ],
    "8.png": [
        (330, 120, 470, 148),   # asset name in detail
        (150, 268, 470, 296),   # session id
    ],
    "9.png": [
        (360, 118, 470, 146),   # asset name
        (150, 318, 500, 346),   # session id
    ],
}


def blur(img, box):
    x0, y0, x1, y1 = box
    crop = img.crop((x0, y0, x1, y1))
    img.paste(crop.filter(ImageFilter.GaussianBlur(14)), (x0, y0))


for name in [f"{i}.png" for i in range(1, 10)]:
    img = Image.open(src / name).convert("RGB")
    for box in boxes.get(name, []):
        blur(img, box)
    img.save(out / name, optimize=True)
    print(f"{name}: {img.size}")
