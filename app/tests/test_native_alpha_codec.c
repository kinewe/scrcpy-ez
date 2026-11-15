#include <assert.h>
#include <stdio.h>
#include "native_alpha_codec.h"
static uint32_t read32(FILE *f){uint8_t b[4];assert(fread(b,1,4,f)==4);return na_be32(b);}
int main(int argc,char **argv){
 assert(argc==2);FILE *f=fopen(argv[1],"rb");assert(f);unsigned frames=read32(f),patches=0,xor_patches=0,full=0;size_t checked=0;struct na_decoder d={0};
 for(unsigned i=0;i<frames;i++){
  uint32_t flags=read32(f),epoch=read32(f),w=read32(f),h=read32(f),bytes=read32(f),crc=read32(f);size_t size=(size_t)w*h*4,cap=size+4+h*8;
  uint8_t *packed=malloc(bytes),*raw=malloc(cap),*expected=malloc(size);assert(packed&&raw&&expected);assert(fread(packed,1,bytes,f)==bytes&&fread(expected,1,size,f)==size);
  uLongf length=cap;assert(uncompress(raw,&length,packed,bytes)==Z_OK);assert(na_decode(&d,flags,epoch,w,h,raw,length,crc));assert(d.valid&&d.premultiplied&&!memcmp(d.rgba,expected,size));checked+=size;if(flags==3)patches++;else if(flags==4)xor_patches++;else full++;
  if(flags==3||flags==4){assert(!na_decode(&d,flags,epoch+1,w,h,raw,length,crc));assert(!d.valid);assert(na_decode(&d,2,epoch,w,h,expected,size,crc));}
  assert(!na_decode(&d,2,epoch,w,h,expected,size,crc^1));assert(!d.valid);assert(na_decode(&d,2,epoch,w,h,expected,size,crc));
  uint8_t malformed[]={0,0,0,1,255,255,255,255,0,0,0,4,0,0,0,0};assert(!na_decode(&d,3,epoch,w,h,malformed,sizeof(malformed),crc));assert(na_decode(&d,2,epoch,w,h,expected,size,crc));
  free(packed);free(raw);free(expected);
 }
 fclose(f);free(d.rgba);assert(patches&&xor_patches&&full);printf("PASS native-alpha cross-language codec: %u frames (%u full, %u row patches, %u XOR patches), %llu exact RGBA bytes; epoch, CRC, malformed patch and keyframe recovery\n",frames,full,patches,xor_patches,(unsigned long long)checked);return 0;
}
