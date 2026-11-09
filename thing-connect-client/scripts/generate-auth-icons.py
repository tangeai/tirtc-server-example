"""Generate password visibility icons. Pillow is needed only to regenerate."""
from pathlib import Path
from PIL import Image, ImageDraw
import math

root = Path(__file__).resolve().parents[1] / 'src/assets/auth'
root.mkdir(parents=True, exist_ok=True)
for hidden in (False, True):
    image = Image.new('RGBA', (192, 192))
    draw = ImageDraw.Draw(image)
    color = '#8b9490'
    points = [(20 + 152 * i / 80, 96 - 46 * math.sin(math.pi * i / 80)) for i in range(81)]
    points += [(172 - 152 * i / 80, 96 + 46 * math.sin(math.pi * i / 80)) for i in range(81)]
    draw.line(points + [points[0]], fill=color, width=10, joint='curve')
    draw.ellipse((73, 73, 119, 119), outline=color, width=10)
    if hidden:
        draw.line((30, 30, 162, 162), fill='white', width=24)
        draw.line((30, 30, 162, 162), fill=color, width=10)
    image.resize((48, 48), getattr(Image, 'Resampling', Image).LANCZOS).save(root / ('eye-off.png' if hidden else 'eye.png'))
