"""Generate quiet, monochrome robot outlines for small device-list icons."""
from pathlib import Path
from PIL import Image, ImageDraw

root = Path(__file__).resolve().parents[1] / 'src/assets/devices'
root.mkdir(parents=True, exist_ok=True)
for state, ink in [('online', '#07a956'), ('offline', '#929c96')]:
    icon = Image.new('RGBA', (384, 384))
    d = ImageDraw.Draw(icon)
    # Rounded head, short antenna and two eyes remain legible at small sizes.
    d.line((192, 64, 192, 108), fill=ink, width=16)
    d.ellipse((180, 48, 204, 72), fill=ink)
    d.rounded_rectangle((64, 104, 320, 312), radius=56, outline=ink, width=16)
    d.rounded_rectangle((32, 176, 64, 240), radius=12, fill=ink)
    d.rounded_rectangle((320, 176, 352, 240), radius=12, fill=ink)
    d.rounded_rectangle((128, 174, 148, 218), radius=10, fill=ink)
    d.rounded_rectangle((236, 174, 256, 218), radius=10, fill=ink)
    d.arc((164, 226, 220, 258), 0, 180, fill=ink, width=12)
    icon.resize((96, 96), getattr(Image, 'Resampling', Image).LANCZOS).save(root / ('robot-outline-' + state + '.png'))
