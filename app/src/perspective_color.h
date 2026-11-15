#ifndef SC_PERSPECTIVE_COLOR_H
#define SC_PERSPECTIVE_COLOR_H

#include <libavutil/frame.h>
#include <libswscale/swscale.h>
#include <stdbool.h>
#include <math.h>

/* Match the ordinary SDL video path, including its BT.709 fallback when the
 * Android encoder omits matrix metadata. Do not let libswscale's BT.601 default
 * silently give the keyed and ordinary video paths different colors. */
static inline int
sc_perspective_color_matrix(enum AVColorSpace space) {
    switch (space) {
        case AVCOL_SPC_BT470BG:
        case AVCOL_SPC_SMPTE170M: return SWS_CS_ITU601;
        case AVCOL_SPC_BT2020_NCL:
        case AVCOL_SPC_BT2020_CL: return SWS_CS_BT2020;
        default: return SWS_CS_ITU709;
    }
}

static inline int
sc_perspective_configure_rgb(struct SwsContext *sws, const AVFrame *frame) {
    const int *matrix=sws_getCoefficients(sc_perspective_color_matrix(frame->colorspace));
    return sws_setColorspaceDetails(sws,matrix,frame->color_range==AVCOL_RANGE_JPEG,
                                   matrix,1,0,1<<16,1<<16);
}

struct sc_perspective_yuv_coefficients {
    float y_scale, y_offset, r_v, g_u, g_v, b_u;
};

static inline struct sc_perspective_yuv_coefficients
sc_perspective_yuv_coefficients(const AVFrame *frame) {
    float kr=.2126f,kb=.0722f;
    int matrix=sc_perspective_color_matrix(frame->colorspace);
    if(matrix==SWS_CS_ITU601){kr=.299f;kb=.114f;}
    if(matrix==SWS_CS_BT2020){kr=.2627f;kb=.0593f;}
    bool full=frame->color_range==AVCOL_RANGE_JPEG;
    float chroma=full?1.f:255.f/224.f,kg=1-kr-kb;
    return (struct sc_perspective_yuv_coefficients){
        full?1.f:255.f/219.f,full?0.f:16.f,
        2*(1-kr)*chroma,-2*kb*(1-kb)/kg*chroma,
        -2*kr*(1-kr)/kg*chroma,2*(1-kb)*chroma,
    };
}

static inline int sc_perspective_color_byte(float x) {
    return x<0?0:x>255?255:(int)lroundf(x);
}

static inline void
sc_perspective_yuv_rgb(const struct sc_perspective_yuv_coefficients *c,
                       int y,int u,int v,int *r,int *g,int *b) {
    float yy=(y-c->y_offset)*c->y_scale,uu=u-128,vv=v-128;
    *r=sc_perspective_color_byte(yy+c->r_v*vv);
    *g=sc_perspective_color_byte(yy+c->g_u*uu+c->g_v*vv);
    *b=sc_perspective_color_byte(yy+c->b_u*uu);
}
#endif
