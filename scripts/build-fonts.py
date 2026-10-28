#!/usr/bin/env python3
"""Builds Tilde's bundled UI font (D56): the Ubuntu font that Linux Mint
uses, for browsers on machines that do not have it.

  make fonts                     # from the installed fonts-ubuntu package
  UBUNTU_FONT_DIR=... make fonts # from a folder with Ubuntu[wdth,wght].ttf

Writes web/src/assets/fonts/tilde-ui.woff2, its licence next to it, and
web/src/styles/fonts.css (the @font-face rules). Needs fontTools and
brotli (apt install python3-fonttools python3-brotli).

The font is the variable Ubuntu font pinned to normal width, its weights
from 300 to 700 kept, and subset to Latin, Greek and Cyrillic. That makes
it a Modified Version under the Ubuntu Font Licence, which must keep the
original name with "derivative X" added: it is "Ubuntu derivative Tilde".

The fallback faces take Ubuntu's space with fonts that are already there
(Arial on Windows and macOS, Liberation Sans or Arimo with Arial's metrics
on Linux, DejaVu Sans), so the layout does not shift while the bundled
font loads: size-adjust from average letter widths weighted by English
letter frequencies, as Next.js and fontaine do, and Ubuntu's ascent and
descent.
"""
import io
import os
import re
import sys

from fontTools import subset
from fontTools.ttLib import TTFont
from fontTools.varLib import instancer

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
FONT_OUT = os.path.join(ROOT, "web", "src", "assets", "fonts", "tilde-ui.woff2")
LICENCE_OUT = os.path.join(ROOT, "web", "src", "assets", "fonts", "UBUNTU-FONT-LICENCE.txt")
CSS_OUT = os.path.join(ROOT, "web", "src", "styles", "fonts.css")

SRC_DIR = os.environ.get("UBUNTU_FONT_DIR", "/usr/share/fonts/truetype/ubuntu")
SRC_FONT = os.path.join(SRC_DIR, "Ubuntu[wdth,wght].ttf")
COPYRIGHT = os.environ.get("UBUNTU_FONT_COPYRIGHT", "/usr/share/doc/fonts-ubuntu/copyright")

NAME = "Ubuntu derivative Tilde"
PS_NAME = "UbuntuderivativeTilde"

# Latin (with Extended-A and B, IPA bits and Vietnamese), Greek, Cyrillic,
# punctuation, currency, letterlike symbols, arrows, maths operators, and
# the shapes and ligatures Ubuntu has.
RANGES = [
    (0x0000, 0x024F),
    (0x0259, 0x0259),
    (0x02B0, 0x036F),
    (0x0370, 0x03FF),
    (0x0400, 0x052F),
    (0x1E00, 0x1EFF),
    (0x2000, 0x206F),
    (0x20A0, 0x20CF),
    (0x2100, 0x214F),
    (0x2190, 0x21FF),
    (0x2200, 0x22FF),
    (0x25A0, 0x25FF),
    (0xFB00, 0xFB06),
]

# Letter frequencies in English text, and the space between words.
FREQ = {
    "a": 8.2, "b": 1.5, "c": 2.8, "d": 4.3, "e": 12.7, "f": 2.2, "g": 2.0,
    "h": 6.1, "i": 7.0, "j": 0.15, "k": 0.77, "l": 4.0, "m": 2.4, "n": 6.7,
    "o": 7.5, "p": 1.9, "q": 0.095, "r": 6.0, "s": 6.3, "t": 9.1, "u": 2.8,
    "v": 0.98, "w": 2.4, "x": 0.15, "y": 2.0, "z": 0.074, " ": 18.0,
}

# The fallbacks, measured from fonts with the same metrics as what the
# browser will find: Liberation Sans for Arial.
FALLBACKS = [
    {
        "family": "Tilde UI Fallback",
        "measure": ["/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf"],
        "local": ["Arial", "ArialMT", "Liberation Sans", "LiberationSans-Regular", "Arimo", "Arimo-Regular"],
    },
    {
        "family": "Tilde UI Fallback DejaVu",
        "measure": ["/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"],
        "local": ["DejaVu Sans", "DejaVuSans"],
    },
]

# The weights Tilde uses: text, title bars (Mint: Ubuntu Medium), bold.
WEIGHTS = [(400, "Regular"), (500, "Medium"), (700, "Bold")]


def die(msg):
    print(f"build-fonts: {msg}", file=sys.stderr)
    sys.exit(1)


def avg_width(font):
    """Average advance per character, in ems."""
    cmap = font.getBestCmap()
    hmtx = font["hmtx"]
    upem = font["head"].unitsPerEm
    total = weight = 0.0
    for ch, f in FREQ.items():
        glyph = cmap.get(ord(ch))
        if glyph is None:
            continue
        total += hmtx[glyph][0] * f
        weight += f
    return total / weight / upem


def vertical(font):
    """Ascent and descent in ems, as browsers take them."""
    os2 = font["OS/2"]
    upem = font["head"].unitsPerEm
    if os2.fsSelection & (1 << 7):  # USE_TYPO_METRICS
        return os2.sTypoAscender / upem, -os2.sTypoDescender / upem, os2.sTypoLineGap / upem
    hhea = font["hhea"]
    return hhea.ascent / upem, -hhea.descent / upem, hhea.lineGap / upem


def rename(font):
    """The licence's name for a Modified Version (condition 2c)."""
    names = font["name"]
    for rec in list(names.names):
        if rec.nameID in (1, 3, 4, 6, 16, 21, 25):
            names.removeNames(nameID=rec.nameID)
    names.setName(NAME, 1, 3, 1, 0x409)
    names.setName(NAME, 16, 3, 1, 0x409)
    names.setName("Regular", 2, 3, 1, 0x409)
    names.setName(NAME + " Regular", 4, 3, 1, 0x409)
    names.setName(PS_NAME + "-Regular", 6, 3, 1, 0x409)
    names.setName(PS_NAME, 25, 3, 1, 0x409)
    names.setName(f"{PS_NAME}-Regular;{font['head'].fontRevision:.3f}", 3, 3, 1, 0x409)


def licence_text():
    """The UFL's text from the package's copyright file."""
    try:
        text = open(COPYRIGHT, encoding="utf-8").read()
    except OSError:
        die(f"no {COPYRIGHT}: set UBUNTU_FONT_COPYRIGHT to a file with the Ubuntu Font Licence")
    m = re.search(r"^License: Ubuntu-Font-Licence-1\.0\n((?: .*\n?)+)", text[text.find("\nLicense: Ubuntu-Font-Licence-1.0\n", 1) :], re.M)
    if not m:
        die(f"no Ubuntu Font Licence text in {COPYRIGHT}")
    lines = [ln[1:] if ln.startswith(" ") else ln for ln in m.group(1).splitlines()]
    return "\n".join("" if ln.strip() == "." else ln for ln in lines).strip() + "\n"


def pct(x):
    return f"{x * 100:.2f}%"


def main():
    if not os.path.exists(SRC_FONT):
        die(f"no {SRC_FONT}: install fonts-ubuntu, or set UBUNTU_FONT_DIR")
    # Keep the source's timestamps, so a rebuild gives the same bytes.
    src = TTFont(SRC_FONT, recalcTimestamp=False)
    copyright = src["name"].getDebugName(0)
    version = src["name"].getDebugName(5)

    font = instancer.instantiateVariableFont(src, {"wdth": 100, "wght": (300, 700)})
    # A round trip through bytes: the subsetter trips over the instancer's
    # lazily loaded glyph variations otherwise (fontTools 4.46).
    buf = io.BytesIO()
    font.save(buf)
    buf.seek(0)
    font = TTFont(buf, recalcTimestamp=False)
    rename(font)
    opts = subset.Options()
    opts.flavor = "woff2"
    opts.layout_features = ["*"]
    opts.name_IDs = ["*"]
    opts.name_languages = ["*"]
    opts.notdef_outline = True
    sub = subset.Subsetter(opts)
    sub.populate(unicodes=[c for lo, hi in RANGES for c in range(lo, hi + 1)])
    sub.subset(font)
    os.makedirs(os.path.dirname(FONT_OUT), exist_ok=True)
    font.flavor = "woff2"
    font.save(FONT_OUT)

    with open(LICENCE_OUT, "w", encoding="utf-8") as f:
        f.write(f"tilde-ui.woff2 is \"{NAME}\", a Modified Version of the Ubuntu font\n")
        f.write(f"({version}): normal width only, weights 300-700, Latin, Greek and\n")
        f.write("Cyrillic. Built by scripts/build-fonts.py.\n\n")
        f.write(f"{copyright}\n\n")
        f.write(licence_text())

    main_w = avg_width(src)
    asc, desc, gap = vertical(src)
    css = [
        "/* Generated by scripts/build-fonts.py (make fonts); do not edit. */",
        f"/* \"{NAME}\": the Ubuntu font, under the Ubuntu Font Licence 1.0 */",
        "/* (assets/fonts/UBUNTU-FONT-LICENCE.txt). An installed Ubuntu wins. */",
        "",
    ]
    for weight, style in WEIGHTS:
        locals_ = ", ".join(f"local('{n}')" for n in (f"Ubuntu {style}", f"Ubuntu-{style}"))
        if weight == 400:
            locals_ = "local('Ubuntu'), " + locals_
        css += [
            "@font-face {",
            "  font-family: 'Tilde UI';",
            "  font-style: normal;",
            f"  font-weight: {weight};",
            "  font-display: swap;",
            f"  src: {locals_}, url('../assets/fonts/tilde-ui.woff2') format('woff2');",
            "}",
            "",
        ]
    for fb in FALLBACKS:
        path = next((p for p in fb["measure"] if os.path.exists(p)), None)
        if path is None:
            die(f"cannot measure {fb['family']}: none of {fb['measure']} (apt install fonts-liberation fonts-dejavu-core)")
        fallback = TTFont(path)
        size = main_w / avg_width(fallback)
        css += [
            f"/* Ubuntu's space in {os.path.basename(path)}'s metrics. */",
            "@font-face {",
            f"  font-family: '{fb['family']}';",
            "  src: " + ", ".join(f"local('{n}')" for n in fb["local"]) + ";",
            f"  size-adjust: {pct(size)};",
            f"  ascent-override: {pct(asc / size)};",
            f"  descent-override: {pct(desc / size)};",
            f"  line-gap-override: {pct(gap / size)};",
            "}",
            "",
        ]
    with open(CSS_OUT, "w", encoding="utf-8") as f:
        f.write("\n".join(css))
    print(f"wrote {os.path.relpath(FONT_OUT, ROOT)} ({os.path.getsize(FONT_OUT) // 1024} KiB), {os.path.relpath(CSS_OUT, ROOT)}")


if __name__ == "__main__":
    main()
