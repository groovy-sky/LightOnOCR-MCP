#!/usr/bin/env python3
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parents[1] / "testdata" / "parity"
FONT = ImageFont.truetype("/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", 30)
SMALL = ImageFont.truetype("/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", 24)


def page(name: str, draw_page) -> None:
    image = Image.new("RGB", (1240, 1754), "white")
    draw_page(ImageDraw.Draw(image))
    image.save(ROOT / name, optimize=True)


ROOT.mkdir(parents=True, exist_ok=True)
page("scan.png", lambda draw: draw.multiline_text((100, 120), "ARCHIVE COPY\nInvoice 1042\nDate: 2026-01-15\nTotal: EUR 129.50", fill="#222", font=FONT, spacing=24))


def table(draw: ImageDraw.ImageDraw) -> None:
    draw.text((100, 80), "Quarterly results", fill="black", font=FONT)
    rows = [("Region", "Q1", "Q2"), ("North", "120", "135"), ("South", "98", "111")]
    for row, values in enumerate(rows):
        for column, value in enumerate(values):
            box = (100 + column * 300, 160 + row * 70, 400 + column * 300, 230 + row * 70)
            draw.rectangle(box, outline="black", width=2)
            draw.text((box[0] + 12, box[1] + 15), value, fill="black", font=SMALL)


page("table.png", table)


def columns(draw: ImageDraw.ImageDraw) -> None:
    draw.text((100, 70), "Two-column bulletin", fill="black", font=FONT)
    draw.multiline_text((100, 160), "LEFT COLUMN\nAlpha begins here.\nBeta follows next.\nGamma closes the list.", fill="black", font=SMALL, spacing=18)
    draw.multiline_text((680, 160), "RIGHT COLUMN\nOne starts here.\nTwo follows next.\nThree closes the list.", fill="black", font=SMALL, spacing=18)


page("columns.png", columns)
page("formula.png", lambda draw: draw.multiline_text((100, 120), "Energy identity\nE = mc^2\nIntegral: int_0^1 x^2 dx = 1/3", fill="black", font=FONT, spacing=28))
page("languages.png", lambda draw: draw.multiline_text((100, 120), "English: Annual report\nFrancais: Rapport annuel\nDeutsch: Jahresbericht\nEspanol: Informe anual\nPortugues: Relatorio anual", fill="black", font=FONT, spacing=24))
