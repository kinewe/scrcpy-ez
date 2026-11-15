#ifndef SC_NATIVE_ALPHA_CODEC_H
#define SC_NATIVE_ALPHA_CODEC_H
#include <stdint.h>
#include <stdbool.h>
#include <stdlib.h>
#include <string.h>
#include <zlib.h>
struct na_decoder {uint8_t *rgba;size_t size;uint32_t width,height,epoch;bool valid,premultiplied;};
static uint32_t na_be32(const uint8_t *p){return (uint32_t)p[0]<<24|(uint32_t)p[1]<<16|(uint32_t)p[2]<<8|p[3];}
static bool na_decode(struct na_decoder *d,uint32_t flags,uint32_t epoch,uint32_t w,uint32_t h,const uint8_t *raw,size_t length,uint32_t expected_crc){
    size_t size=(size_t)w*h*4;
    if(flags==1||flags==2){
        if(length!=size)goto bad;
        if(d->size!=size){uint8_t *next=realloc(d->rgba,size);if(!next)goto bad;d->rgba=next;d->size=size;}
        memcpy(d->rgba,raw,size);d->width=w;d->height=h;d->epoch=epoch;d->premultiplied=flags==2;
    }else if(flags==3||flags==4){
        if(!d->valid||!d->premultiplied||d->epoch!=epoch||d->width!=w||d->height!=h||length<4)goto bad;
        uint32_t count=na_be32(raw);size_t pos=4,end=0,row=(size_t)w*4;
        if(count>h)goto bad;
        for(uint32_t i=0;i<count;i++){
            if(length-pos<8)goto bad;
            size_t offset=na_be32(raw+pos),bytes=na_be32(raw+pos+4);pos+=8;
            if(!bytes||offset<end||offset>size||bytes>size-offset||bytes>length-pos||offset%row||bytes%row)goto bad;
            if(flags==3){
                memcpy(d->rgba+offset,raw+pos,bytes);
            }else{
                for(size_t j=0;j<bytes;++j)d->rgba[offset+j]^=raw[pos+j];
            }
            pos+=bytes;end=offset+bytes;
        }
        if(pos!=length)goto bad;
    }else goto bad;
    if(crc32(0,d->rgba,(uInt)size)!=expected_crc)goto bad;
    d->valid=true;return true;
bad:d->valid=false;return false;
}
#endif
