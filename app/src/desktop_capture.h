#ifndef SC_DESKTOP_CAPTURE_H
#define SC_DESKTOP_CAPTURE_H
#include <stdbool.h>
#include <libavutil/buffer.h>
#include <libavutil/frame.h>
struct sc_desktop_capture;
struct sc_desktop_capture *sc_desktop_capture_create(unsigned width, unsigned height, unsigned fps);
void sc_desktop_capture_destroy(struct sc_desktop_capture *capture);
void sc_desktop_capture_suspend(struct sc_desktop_capture *capture);
AVBufferRef *sc_desktop_capture_ref_frames(struct sc_desktop_capture *capture);
#define SC_DESKTOP_CAPTURE_KEY_PROBE 8
#define SC_DESKTOP_CAPTURE_CENTER_LOCATORS 9
/* One-frame id grid: 4x3 two-tone markers on a green field. */
#define SC_DESKTOP_CAPTURE_ID_GRID 10
// mode: 0 desktop, 1 alignment markers, 2 black; physical desktop coordinates.
bool sc_desktop_capture_render(struct sc_desktop_capture *capture, AVFrame *frame,
        double x, double y, double width, double height, const double matrix[6],
        unsigned mode, unsigned key_channel);
#endif
