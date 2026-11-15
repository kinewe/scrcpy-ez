// Link to the release native objects: sc_mutex has a debug-only ABI field.
#define NDEBUG
#include "perspective.h"
#undef NDEBUG
#include <assert.h>
#include <math.h>
#include <stdio.h>
#include <string.h>
#include <libswscale/swscale.h>

static void rect(AVFrame *f, int x, int y, int w, int h,
                  unsigned r, unsigned g, unsigned b) {
    for (int j = y; j < y + h; ++j) for (int i = x; i < x + w; ++i) {
        uint8_t *p = f->data[0] + j * f->linesize[0] + i * 3;
        p[0] = r; p[1] = g; p[2] = b;
    }
}

int main(void) {
    AVFrame *rgb = av_frame_alloc(), *yuv = av_frame_alloc();
    assert(rgb && yuv);
    rgb->format = AV_PIX_FMT_RGB24; yuv->format = AV_PIX_FMT_YUV420P;
    yuv->colorspace=AVCOL_SPC_SMPTE170M;yuv->color_range=AVCOL_RANGE_MPEG;
    rgb->width = yuv->width = 320; rgb->height = yuv->height = 640;
    assert(av_frame_get_buffer(rgb, 32) == 0 && av_frame_get_buffer(yuv, 32) == 0);
    rect(rgb, 0, 0, 320, 640, 0, 0, 0);
    rect(rgb, 0, 26, 320, 486, 0, 255, 0);
    rect(rgb, 0, 0, 320, 2, 0, 255, 0); // disconnected top fringe
    rect(rgb, 60, 105, 40, 40, 255, 0, 0);
    rect(rgb, 220, 105, 40, 40, 0, 0, 255);
    rect(rgb, 60, 355, 40, 40, 255, 255, 0);
    rect(rgb, 220, 355, 40, 40, 255, 0, 255);
    struct SwsContext *convert = sws_getContext(320, 640, AV_PIX_FMT_RGB24,
                          320, 640, AV_PIX_FMT_YUV420P, SWS_POINT, NULL, NULL, NULL);
    assert(convert);
    sws_scale(convert, (const uint8_t *const *) rgb->data, rgb->linesize,
                0, 640, yuv->data, yuv->linesize);
    struct sc_perspective p = {0};
    assert(sc_mutex_init(&p.mutex));
    atomic_init(&p.stopped, false);
    atomic_init(&p.calibration_requested, false);
    p.started = true; p.calibrating = true;
    p.observed_width = 320; p.observed_height = 640;
    sc_perspective_observe(&p, yuv);
    assert(p.calibration_region_valid);
    assert(p.calibration_region[0] == 0 && p.calibration_region[1] == 0);
    assert(p.calibration_region[2] == 1);
    assert(p.calibration_region[3] >= .8 && p.calibration_region[3] < .82);
    assert(p.candidate_count == 1);
    assert(fabs(p.candidate_map[0] - 1) < .02);
    assert(fabs(p.candidate_map[3] - .78125) < .02);
    // A scanner's native dark scrim must not make its locators disappear.
    for(int y=0;y<rgb->height;++y)for(int x=0;x<rgb->width*3;++x)
        rgb->data[0][y*rgb->linesize[0]+x]=rgb->data[0][y*rgb->linesize[0]+x]*3/5;
    sws_scale(convert,(const uint8_t *const*)rgb->data,rgb->linesize,0,640,yuv->data,yuv->linesize);
    p.candidate_count=0;p.calibration_region_valid=false;
    sc_perspective_observe(&p,yuv);
    assert(p.calibration_region_valid && p.candidate_count==1);
    assert(fabs(p.candidate_map[0]-1)<.02 && fabs(p.candidate_map[3]-.78125)<.02);
    // Compact locators remain visible when an app shows only the left portion
    // of the virtual camera, as measured on the tablet scanner.
    rect(rgb,0,0,320,640,0,255,0);
    rect(rgb,75,140,20,40,255,0,0);rect(rgb,203,140,20,40,0,0,255);
    rect(rgb,75,460,20,40,255,255,0);rect(rgb,203,460,20,40,255,0,255);
    sws_scale(convert,(const uint8_t *const*)rgb->data,rgb->linesize,0,640,yuv->data,yuv->linesize);
    p.calibration_pass=1;p.candidate_count=0;
    sc_perspective_observe(&p,yuv);
    assert(p.candidate_count==1);
    assert(fabs(p.candidate_map[0]-2.6667)<.03&&fabs(p.candidate_map[3]-1)<.02);
    sc_mutex_destroy(&p.mutex);
    av_frame_free(&rgb); av_frame_free(&yuv); sws_freeContext(convert);
    puts("camera region: top fringe included, bottom app UI excluded, locator mapping unchanged");
    return 0;
}
