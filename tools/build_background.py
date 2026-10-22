from PIL import Image, ImageFilter, ImageEnhance, ImageDraw
from pathlib import Path
root=Path(__file__).resolve().parents[1]/'cmd'/'dds-installer'/'gui_assets'
art=Image.open(root/'DDS_mascot.png').convert('RGB')
w,h=900,390
bg=Image.new('RGB',(w,h),(5,18,40))
art=art.resize((400,720),Image.Resampling.LANCZOS).crop((0,150,400,540))
art=ImageEnhance.Brightness(art).enhance(.48)
bg.paste(art,(500,0))
trans=Image.new('RGBA',(w,h),(0,0,0,0))
d=ImageDraw.Draw(trans)
d.rectangle((0,0,w,h),fill=(4,18,39,100))
d.rectangle((0,0,470,h),fill=(5,18,39,150))
bg=Image.alpha_composite(bg.convert('RGBA'),trans).convert('RGB').filter(ImageFilter.GaussianBlur(radius=3))
r,g,b=bg.split()
rgba=Image.merge('RGBA',(r,g,b,Image.new('L',bg.size,255)))
(root/'background.bgra').write_bytes(rgba.tobytes('raw','BGRA'))
print('GUI background regenerated')
