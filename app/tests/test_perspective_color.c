#include <assert.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "perspective_color.h"

int main(void) {
    AVFrame f={0};
    f.color_range=AVCOL_RANGE_MPEG;f.colorspace=AVCOL_SPC_BT709;
    struct sc_perspective_yuv_coefficients c=sc_perspective_yuv_coefficients(&f);
    int r,g,b;
    sc_perspective_yuv_rgb(&c,16,128,128,&r,&g,&b);assert(r==0&&g==0&&b==0);
    sc_perspective_yuv_rgb(&c,235,128,128,&r,&g,&b);assert(r==255&&g==255&&b==255);
    // BT.709 limited-range blue, including unavoidable 8-bit rounding.
    sc_perspective_yuv_rgb(&c,32,240,118,&r,&g,&b);assert(r<=2&&g<=2&&b>=253);
    assert(sc_perspective_color_matrix(AVCOL_SPC_UNSPECIFIED)==SWS_CS_ITU709);
    assert(sc_perspective_color_matrix(AVCOL_SPC_BT470BG)==SWS_CS_ITU601);
    for(int range=0;range<2;++range)for(int matrix=0;matrix<3;++matrix){
        enum AVColorSpace spaces[]={AVCOL_SPC_BT709,AVCOL_SPC_SMPTE170M,AVCOL_SPC_BT2020_NCL};
        f.colorspace=spaces[matrix];f.color_range=range?AVCOL_RANGE_JPEG:AVCOL_RANGE_MPEG;
        c=sc_perspective_yuv_coefficients(&f);
        struct SwsContext *s=sws_getContext(16,16,AV_PIX_FMT_YUV420P,16,16,AV_PIX_FMT_RGBA,SWS_POINT,NULL,NULL,NULL);
        assert(s&&sc_perspective_configure_rgb(s,&f)>=0);
        uint8_t y[256],u[64],v[64],rgba[1024];const uint8_t *src[]={y,u,v,NULL};
        int strides[]={16,8,8,0},dst_stride[]={64,0,0,0};uint8_t *dst[]={rgba,NULL,NULL,NULL};
        // Non-neutral samples distinguish the matrix and full/limited range.
        for(int k=0;k<8;++k){
            int yy=40+k*22,uu=95+k*4,vv=160-k*3;
            memset(y,yy,sizeof(y));memset(u,uu,sizeof(u));memset(v,vv,sizeof(v));
            assert(sws_scale(s,src,strides,0,16,dst,dst_stride)==16);
            sc_perspective_yuv_rgb(&c,yy,uu,vv,&r,&g,&b);
            assert(abs(r-rgba[0])<=3&&abs(g-rgba[1])<=3&&abs(b-rgba[2])<=3);
        }
        sws_freeContext(s);
    }
    puts("perspective colors: matrix/range aware; calibration and swscale agree");
    return 0;
}
