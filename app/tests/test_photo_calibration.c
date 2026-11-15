#include "photo_calibration.h"
#include <assert.h>
#include <stdio.h>
int main(void){
    struct sc_photo_calibration c={0};const float bg[3]={0,0,1};
    assert(!sc_photo_start(&c,100000,100000,bg,0));
    assert(sc_photo_start(&c,48,80,bg,0));uint8_t image[48*80*4];int64_t now=0;
    for(unsigned phase=1;phase<=4;++phase){
        assert(c.phase==phase);now+=400;
        for(unsigned sample=0;sample<SC_PHOTO_SAMPLES;++sample){
            float backing[3]={0,0,0};if(phase==1)backing[2]=1;if(phase==3)for(int k=0;k<3;++k)backing[k]=1;
            if(phase==4)for(int k=0;k<3;++k)backing[k]=.5f;
            for(unsigned y=0;y<80;++y)for(unsigned x=0;x<48;++x){
                unsigned i=y*48+x;float alpha=y>=60?.4f:0;
                float f[3]={0,0,0};
                if(x>=30&&y>=68){alpha=1;f[1]=.53f;f[2]=1;}
                // One moving foreground pixel must never become static UI.
                if(x==2&&y==2&&sample%2){alpha=1;f[1]=1;}
                for(unsigned k=0;k<3;++k)image[i*4+k]=(uint8_t)lroundf(255*(f[k]+(1-alpha)*backing[k]));image[i*4+3]=255;
            }
            int result=sc_photo_observe(&c,image,48*4,phase==1?1.f:0.f,now);now+=34;
            assert(result==(phase==4&&sample==SC_PHOTO_SAMPLES-1?1:0));
        }
    }
    unsigned count=0;uint8_t *model=sc_photo_build(&c,&count);assert(model&&count>48*79);
    unsigned blue=(70*48+36)*4,scrim=(65*48+20)*4;
    assert(model[blue+2]>=254&&model[blue+1]>=134&&model[blue+3]==255);
    assert(model[scrim+3]>=101&&model[scrim+3]<=103&&model[scrim]==0);
    assert(model[(2*48+2)*4]==255&&model[(2*48+2)*4+3]==0);
    free(model);sc_photo_reset(&c);
    /* RC.22 fast-boot calibration: with the 100 ms settle + SC_PHOTO_SAMPLES
     * samples the four backing phases must finish well inside 1.5 s even at a
     * realistic 16 ms video-frame spacing (previously ~350 ms settle/8 samples
     * took >2.5 s). */
    assert(sc_photo_start(&c,48,80,bg,0));now=0;
    int fast_frames=0,fast_done=0;
    while(fast_frames<200){
        unsigned ph=c.phase?c.phase:4;float backing[3]={0,0,0};
        if(ph==1)backing[2]=1;
        if(ph==3)for(int k=0;k<3;++k)backing[k]=1;
        if(ph==4)for(int k=0;k<3;++k)backing[k]=.5f;
        for(unsigned y=0;y<80;++y)for(unsigned x=0;x<48;++x){
            unsigned i=y*48+x;float alpha=y>=60?.4f:0;float f[3]={0,0,0};
            if(x>=30&&y>=68){alpha=1;f[1]=.53f;f[2]=1;}
            for(unsigned k=0;k<3;++k)image[i*4+k]=(uint8_t)lroundf(255*(f[k]+(1-alpha)*backing[k]));image[i*4+3]=255;
        }
        int result=sc_photo_observe(&c,image,48*4,ph==1?1.f:0.f,now);
        ++fast_frames;now+=16;
        if(result==1){fast_done=1;break;}
        assert(result>=0);
    }
    assert(fast_done&&fast_frames<=80);
    printf("fast boot calibration: four phases completed in %d frames (~%d ms at 16 ms spacing)\n",fast_frames,fast_frames*16);
    sc_photo_reset(&c);
    assert(sc_photo_start(&c,48,80,bg,0));
    assert(sc_photo_observe(&c,image,48*4,1,7000)==-1);sc_photo_reset(&c);
    puts("automatic photo calibration: known scrim/blue UI recovered, motion rejected, timeout and memory bounded");
    return 0;
}
