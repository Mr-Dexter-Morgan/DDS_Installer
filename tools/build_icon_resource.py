from pathlib import Path
import struct
app=Path(__file__).resolve().parents[1] / 'cmd' / 'dds-installer'
ico=(app / 'gui_assets' / 'DDS.ico').read_bytes()
reserved,kind,count=struct.unpack_from('<HHH',ico,0)
assert reserved==0 and kind==1 and 1<=count<=30
icons=[]; group=bytearray(struct.pack('<HHH',0,1,count))
for j in range(count):
 off=6+j*16
 width,height,colors,res,planes,bits,size,location=struct.unpack_from('<BBBBHHII',ico,off)
 blob=ico[location:location+size]
 assert len(blob)==size
 icons.append(blob)
 group+=struct.pack('<BBBBHHIH',width,height,colors,res,planes,bits,size,j+1)
# Preserve DDS icons and declare asInvoker so Windows installer-name heuristics
# cannot elevate the per-user GUI. Only the bounded Apply helper uses runas.
manifest=(app / 'gui_assets' / 'DDSSetup.manifest').read_bytes()
entries=[(3,i+1,blob) for i,blob in enumerate(icons)]+[(14,1,bytes(group)),(24,1,manifest)]
resource_types=(3,14,24)
data=bytearray()
def reserve(n):
 off=len(data);data.extend(b'\x00'*n);return off
def write(off,b):data[off:off+len(b)]=b
def align():
 while len(data)%4:data.append(0)
root=reserve(16+8*len(resource_types))
parents={}
for t in resource_types:
 childs=[e for e in entries if e[0]==t]
 off=reserve(16+8*len(childs));parents[t]=off
# child language directories and data entries
languages={};records={}
for t,i,blob in entries:
 lang=reserve(16+8)
 de=reserve(16)
 languages[(t,i)]=lang;records[(t,i)]=(de,blob)
write(root,struct.pack('<IIHHHH',0,0,0,0,0,len(resource_types)))
for z,t in enumerate(resource_types):
 write(root+16+8*z,struct.pack('<II',t,0x80000000|parents[t]))
for t in resource_types:
 childs=[e for e in entries if e[0]==t];off=parents[t]
 write(off,struct.pack('<IIHHHH',0,0,0,0,0,len(childs)))
 for z,(_,i,_) in enumerate(childs):
  write(off+16+8*z,struct.pack('<II',i,0x80000000|languages[(t,i)]))
for t,i,blob in entries:
 off=languages[(t,i)]
 write(off,struct.pack('<IIHHHH',0,0,0,0,0,1))
 write(off+16,struct.pack('<II',0x409,records[(t,i)][0]))
relocations=[]
for t,i,blob in entries:
 align();blob_off=reserve(len(blob));write(blob_off,blob)
 de,_=records[(t,i)]
 write(de,struct.pack('<IIII',blob_off,len(blob),0,0))
 relocations.append(de)
# COFF obj: IMAGE_FILE_MACHINE_AMD64 (0x8664), section .rsrc; IMAGE_REL_AMD64_ADDR32NB=0x0003
section_size=len(data)
sec_raw=60
sec_reloc=sec_raw+section_size
symbols_ptr=sec_reloc+10*len(relocations)
coff=struct.pack('<HHIIIHH',0x8664,1,0,symbols_ptr,1,0,0)
section=(b'.rsrc\x00\x00\x00'+struct.pack('<IIIIIIHHI',0,0,section_size,sec_raw,sec_reloc,0,len(relocations),0,0x40000040))
reloc=b''.join(struct.pack('<IIH',o,0,0x0003) for o in relocations)
sym=b'.rsrc\x00\x00\x00'+struct.pack('<IhHBB',0,1,0,3,0)
output=coff+section+data+reloc+sym+struct.pack('<I',4)
assert len(output)==symbols_ptr+18+4
(app / 'rsrc_windows_amd64.syso').write_bytes(output)
print('Icon count:',count,'resource:',len(output))
