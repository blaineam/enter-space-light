#!/usr/bin/env python3
"""Render Enter Space Light's icons from the Enter Space glyph.

Usage: python3 scripts/make-icons.py <path to the 1024px glyph PNG>

Writes the app icon PNGs, a multi-size Windows .ico, tray icons and the UI
favicon. The outputs are committed, so this only runs when the art changes.
"""
import os
import sys

from PIL import Image, ImageChops, ImageDraw, ImageFilter

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
TOP = (98, 141, 255)    # display-p3 0.383,0.552,1.089 clipped to sRGB
BOTTOM = (55, 103, 255)  # srgb 0.216,0.404,1.000


def glyph_mask(src: Image.Image) -> Image.Image:
    """Alpha mask of the glyph, whatever the source's background."""
    src = src.convert("RGBA")
    alpha = src.getchannel("A")
    if alpha.getextrema() == (255, 255):
        # Opaque artwork on white: the glyph is whatever isn't white.
        gray = src.convert("L")
        alpha = ImageChops.invert(gray).point(lambda v: min(255, v * 4))
    return alpha


def gradient(size: int) -> Image.Image:
    g = Image.new("RGB", (1, size))
    for y in range(size):
        t = y / max(1, size - 1)
        g.putpixel((0, y), tuple(round(TOP[i] + (BOTTOM[i] - TOP[i]) * t) for i in range(3)))
    return g.resize((size, size))


def app_icon(mask: Image.Image, size: int, rounded: bool = True) -> Image.Image:
    s = size * 4  # supersample
    bg = gradient(s).convert("RGBA")
    shape = Image.new("L", (s, s), 0)
    radius = int(s * 0.225) if rounded else int(s * 0.5)
    ImageDraw.Draw(shape).rounded_rectangle((0, 0, s - 1, s - 1), radius=radius, fill=255)
    bg.putalpha(shape)
    inset = int(s * 0.14)
    m = mask.resize((s - 2 * inset, s - 2 * inset), Image.LANCZOS)
    white = Image.new("RGBA", m.size, (255, 255, 255, 255))
    white.putalpha(m)
    shadow = Image.new("RGBA", m.size, (10, 30, 90, 0))
    shadow.putalpha(m.point(lambda v: v * 0.35))
    shadow = shadow.filter(ImageFilter.GaussianBlur(s * 0.01))
    bg.alpha_composite(shadow, (inset, inset + int(s * 0.012)))
    bg.alpha_composite(white, (inset, inset))
    return bg.resize((size, size), Image.LANCZOS)


def main() -> None:
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    mask = glyph_mask(Image.open(sys.argv[1]))
    # Trim to the glyph's bounds so it sits centred.
    bbox = mask.getbbox()
    mask = mask.crop(bbox)
    side = max(mask.size)
    sq = Image.new("L", (side, side), 0)
    sq.paste(mask, ((side - mask.size[0]) // 2, (side - mask.size[1]) // 2))
    mask = sq

    assets = os.path.join(ROOT, "assets")
    os.makedirs(assets, exist_ok=True)
    for size in (16, 24, 32, 48, 64, 128, 256, 512):
        app_icon(mask, size).save(os.path.join(assets, f"icon-{size}.png"))
    big = app_icon(mask, 256)
    big.save(os.path.join(assets, "app.ico"), sizes=[(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)])

    tray = os.path.join(ROOT, "internal", "tray")
    os.makedirs(tray, exist_ok=True)
    app_icon(mask, 64).save(os.path.join(tray, "icon.png"))
    app_icon(mask, 64).save(os.path.join(tray, "icon.ico"), sizes=[(16, 16), (24, 24), (32, 32), (48, 48), (64, 64)])

    # Attention variant: same icon with an amber dot.
    def dotted(size: int) -> Image.Image:
        im = app_icon(mask, size)
        d = ImageDraw.Draw(im)
        r = size * 0.2
        cx, cy = size - r - size * 0.02, size - r - size * 0.02
        d.ellipse((cx - r - size * 0.04, cy - r - size * 0.04, cx + r + size * 0.04, cy + r + size * 0.04), fill=(255, 255, 255, 255))
        d.ellipse((cx - r, cy - r, cx + r, cy + r), fill=(245, 158, 11, 255))
        return im
    dotted(64).save(os.path.join(tray, "icon-alert.png"))
    dotted(64).save(os.path.join(tray, "icon-alert.ico"), sizes=[(16, 16), (24, 24), (32, 32), (48, 48), (64, 64)])

    web = os.path.join(ROOT, "internal", "ui", "web")
    os.makedirs(web, exist_ok=True)
    app_icon(mask, 128).save(os.path.join(web, "icon.png"))


if __name__ == "__main__":
    main()
