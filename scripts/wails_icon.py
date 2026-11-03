"""Add the Wails edition badge to the shared EasyRead icon."""
from pathlib import Path
from PIL import Image, ImageDraw

root = Path(__file__).resolve().parents[1]
image = Image.open(root / 'electron/assets/icon.png').convert('RGBA')
size = image.width
badge = Image.new('RGBA', image.size)
draw = ImageDraw.Draw(badge)
x, y, edge = int(size * .66), int(size * .66), int(size * .29)
draw.rounded_rectangle((x, y, x + edge, y + edge), radius=int(size * .07), fill='#2563EB', outline='#FBFAF6', width=max(2, int(size * .012)))
# Vector strokes keep the mark consistent on build hosts without system fonts.
points = [(x + edge * .18, y + edge * .27), (x + edge * .32, y + edge * .74), (x + edge * .50, y + edge * .40), (x + edge * .68, y + edge * .74), (x + edge * .82, y + edge * .27)]
draw.line(points, fill='white', width=max(2, int(edge * .09)), joint='curve')
image = Image.alpha_composite(image, badge)
out = root / 'desktop-wails/build/appicon.png'
out.parent.mkdir(parents=True, exist_ok=True)
image.save(out)
