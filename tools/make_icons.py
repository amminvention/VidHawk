"""Generate the VidHawk extension/app icons (a hawk silhouette diving onto a download arrow)."""
import sys
from pathlib import Path

from PIL import Image, ImageDraw

BG = (10, 15, 28)        # --bg-1 (Neon Glass theme)
ACCENT = (34, 211, 238)  # --cyan
LIGHT = (232, 240, 255)  # --fg

# Shapes on a 128x128 grid, drawn 8x oversampled.
WING = [(10, 46), (64, 72), (118, 46), (104, 58), (64, 90), (24, 58)]
HEAD = [(64, 72), (54, 50), (74, 50)]
SHAFT = (58, 86, 70, 102)
ARROW = [(44, 100), (84, 100), (64, 120)]


def draw(size: int) -> Image.Image:
    scale = 8
    big = 128 * scale
    img = Image.new("RGBA", (big, big), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    radius = 28 * scale
    d.rounded_rectangle((0, 0, big - 1, big - 1), radius=radius, fill=BG)
    s = lambda pts: [(x * scale, y * scale) for x, y in pts]
    d.polygon(s(WING), fill=ACCENT)
    d.polygon(s(HEAD), fill=ACCENT)
    x0, y0, x1, y1 = SHAFT
    d.rectangle((x0 * scale, y0 * scale, x1 * scale, y1 * scale), fill=LIGHT)
    d.polygon(s(ARROW), fill=LIGHT)
    return img.resize((size, size), Image.LANCZOS)


def main() -> None:
    """make_icons.py <out_dir> [sizes...] | --ico <file.ico> | --png <file.png> <size>"""
    if sys.argv[1] == "--ico":
        target = Path(sys.argv[2])
        target.parent.mkdir(parents=True, exist_ok=True)
        draw(256).save(target, sizes=[(256, 256), (128, 128), (64, 64), (48, 48), (32, 32), (16, 16)])
        print(f"wrote {target}")
        return
    if sys.argv[1] == "--png":
        target = Path(sys.argv[2])
        target.parent.mkdir(parents=True, exist_ok=True)
        draw(int(sys.argv[3])).save(target)
        print(f"wrote {target}")
        return
    out = Path(sys.argv[1])
    out.mkdir(parents=True, exist_ok=True)
    sizes = [int(a) for a in sys.argv[2:]] or [16, 32, 48, 128, 256]
    for size in sizes:
        draw(size).save(out / f"{size}.png")
        print(f"wrote {out / f'{size}.png'}")


if __name__ == "__main__":
    main()
