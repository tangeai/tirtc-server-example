"""Generate the small native tab icons; requires Pillow only when regenerating."""
from pathlib import Path
from PIL import Image, ImageDraw
root = Path(__file__).resolve().parents[1] / 'src/assets/tab'
root.mkdir(parents=True, exist_ok=True)
for name in ('devices', 'account'):
    for suffix, color in (('', '#939b96'), ('-active', '#07b75b')):
        image = Image.new('RGBA', (192, 192))
        d = ImageDraw.Draw(image)
        if name == 'devices':
            d.rounded_rectangle((38, 18, 154, 174), radius=22, outline=color, width=10)
            d.rounded_rectangle((58, 40, 134, 119), radius=8, outline=color, width=8)
            d.ellipse((89, 140, 103, 154), fill=color)
        else:
            d.ellipse((62, 20, 130, 88), outline=color, width=10)
            d.arc((29, 100, 163, 224), 180, 360, fill=color, width=10)
            d.line((29, 162, 163, 162), fill=color, width=10)
        image.resize((48, 48), getattr(Image, 'Resampling', Image).LANCZOS).save(root / f'{name}{suffix}.png')
