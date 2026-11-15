#ifndef SC_PHOTO_CALIBRATION_H
#define SC_PHOTO_CALIBRATION_H
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <math.h>

#define SC_PHOTO_SAMPLES 3
struct sc_photo_calibration {
    unsigned width,height,phase,count;
    uint16_t *sums;
    uint8_t *first,*motion;
    int64_t started_ms,phase_ms,sample_ms;
    int black_luma,white_luma;
    float blue[3];
};
static inline void sc_photo_reset(struct sc_photo_calibration *c){
    free(c->sums);free(c->first);free(c->motion);memset(c,0,sizeof(*c));
}
static inline bool sc_photo_start(struct sc_photo_calibration *c,unsigned w,unsigned h,const float blue[3],int64_t now){
    sc_photo_reset(c);
    size_t n=(size_t)w*h;
    // Bound temporary startup memory; unsupported dimensions retain live UI.
    if(!w||!h||(uint64_t)w*h>4194304)return false;
    c->sums=calloc(n*12,sizeof(uint16_t));c->first=malloc(n*3);c->motion=calloc(n,1);
    if(!c->sums||!c->first||!c->motion){sc_photo_reset(c);return false;}
    c->width=w;c->height=h;c->phase=1;c->started_ms=c->phase_ms=now;
    memcpy(c->blue,blue,sizeof(c->blue));return true;
}
static inline int sc_photo_luma(const struct sc_photo_calibration *c,const uint8_t *rgba,unsigned pitch){
    unsigned histogram[256]={0},count=0;
    for(unsigned y=c->height/5;y<c->height*13/20;y+=12)
        for(unsigned x=c->width/5;x<c->width*4/5;x+=12){
            const uint8_t *p=rgba+(size_t)y*pitch+x*4;
            ++histogram[(p[0]+p[1]+p[2])/3];++count;
        }
    unsigned sum=0;for(int i=0;i<256;++i){sum+=histogram[i];if(sum>count/2)return i;}return 0;
}
/* 1=blue, 2=black, 3=white, 4=gray. Wait for actual returned content, not
 * merely the outgoing timer. Changed animation pixels cannot train a static UI. */
static inline int sc_photo_observe(struct sc_photo_calibration *c,const uint8_t *rgba,unsigned pitch,float blue_fraction,int64_t now){
    if(!c->phase)return 1;
    if(now-c->started_ms>6500)return -1;
    if(now-c->phase_ms<100||now-c->sample_ms<16)return 0;
    int luma=sc_photo_luma(c,rgba,pitch);
    bool arrived=c->phase==1?blue_fraction>.72f:
                 c->phase==2?luma<64:
                 c->phase==3?luma>c->black_luma+32:
                            luma>c->black_luma+8&&luma<c->white_luma-12;
    if(!arrived)return 0;
    size_t n=(size_t)c->width*c->height;
    uint16_t *sum=c->sums+(c->phase-1)*n*3;
    for(unsigned y=0;y<c->height;++y)for(unsigned x=0;x<c->width;++x){
        size_t i=(size_t)y*c->width+x;const uint8_t *p=rgba+(size_t)y*pitch+x*4;
        for(unsigned k=0;k<3;++k){
            if(!c->count)c->first[i*3+k]=p[k];
            else if(abs((int)p[k]-c->first[i*3+k])>12)c->motion[i]=1;
            sum[i*3+k]+=p[k];
        }
    }
    c->sample_ms=now;
    if(++c->count<SC_PHOTO_SAMPLES)return 0;
    if(c->phase==2)c->black_luma=luma;
    if(c->phase==3)c->white_luma=luma;
    c->count=0;c->phase_ms=now;
    if(++c->phase>4){c->phase=0;return 1;}
    return 0;
}
static inline float sc_photo_clamp(float x,float low,float high){return x<low?low:x>high?high:x;}
static inline float sc_photo_median3(float a,float b,float c){return a+b+c-fminf(a,fminf(b,c))-fmaxf(a,fmaxf(b,c));}
static inline uint8_t *sc_photo_build(const struct sc_photo_calibration *c,unsigned *valid_count){
    size_t n=(size_t)c->width*c->height;uint8_t *out=malloc(n*4);if(!out)return NULL;
    unsigned valid_pixels=0;
    for(size_t i=0;i<n;++i){
        float blue[3],black[3],white[3],gray[3],d[3];
        for(unsigned k=0;k<3;++k){
            blue[k]=c->sums[i*3+k]/(255.f*SC_PHOTO_SAMPLES);
            black[k]=c->sums[n*3+i*3+k]/(255.f*SC_PHOTO_SAMPLES);
            white[k]=c->sums[n*6+i*3+k]/(255.f*SC_PHOTO_SAMPLES);
            gray[k]=c->sums[n*9+i*3+k]/(255.f*SC_PHOTO_SAMPLES);
            d[k]=white[k]-black[k];
        }
        float t=sc_photo_clamp(sc_photo_median3(d[0],d[1],d[2]),0,1),error=0;
        bool colored=fmaxf(black[0],fmaxf(black[1],black[2]))>.025f;
        for(unsigned k=0;k<3;++k)error=fmaxf(error,fabsf(black[k]+.5f*t-gray[k]));
        bool valid=!c->motion[i]&&(error<.025f||(colored&&error<.12f));
        if(!valid){out[i*4]=255;out[i*4+1]=0;out[i*4+2]=255;out[i*4+3]=0;continue;}
        if(colored){
            for(unsigned k=0;k<3;++k){
                if(c->blue[k]>.02f)t=fminf(t,(blue[k]+.015f)/c->blue[k]);
                if(c->blue[k]<.98f)t=fminf(t,(1-blue[k]+.015f)/(1-c->blue[k]));
            }
            t=sc_photo_clamp(t,0,1);
        }
        float alpha=1-t;
        for(unsigned k=0;k<3;++k){
            float f=colored?blue[k]-t*c->blue[k]:black[k];
            out[i*4+k]=(uint8_t)lroundf(sc_photo_clamp(f,0,alpha)*255);
        }
        out[i*4+3]=(uint8_t)lroundf(alpha*255);++valid_pixels;
    }
    *valid_count=valid_pixels;return out;
}
#endif
