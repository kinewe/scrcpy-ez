#ifndef SC_LOCAL_PREVIEW_H
#define SC_LOCAL_PREVIEW_H
#include <SDL3/SDL.h>
#include <libavutil/frame.h>
#include "perspective.h"
#include "coords.h"
#ifdef _WIN32
SDL_Texture *local_preview_texture(SDL_Renderer *,struct sc_perspective *,const AVFrame *,SDL_Texture *);
void local_preview_destroy(void);
bool local_preview_is_composited(SDL_Texture *);
void local_preview_abort_preparation(struct sc_perspective *);
void local_preview_on_presented(SDL_Texture *);
void local_preview_capture_geometry(double,double,double,double);
struct sc_point local_preview_remap_point(struct sc_point);
#else
static inline SDL_Texture *local_preview_texture(SDL_Renderer *r,struct sc_perspective *p,const AVFrame *f,SDL_Texture *fallback) {
    (void)r; (void)p; (void)f; return fallback;
}
static inline void local_preview_destroy(void) {}
static inline bool local_preview_is_composited(SDL_Texture *t) { (void)t; return false; }
static inline struct sc_point local_preview_remap_point(struct sc_point p) { return p; }
#endif
#endif
