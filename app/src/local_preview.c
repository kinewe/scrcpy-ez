#include "local_preview.h"
#include "key_preview.h"
#include "native_source.h"
#include "perspective_key.h"
#include "util/log.h"
#include "util/tick.h"
#include <math.h>
#include <string.h>

#define NATIVE_ALPHA_WAIT SC_TICK_FROM_SEC(5)

static struct {
    SDL_Texture *texture;
    int width, height;
    uint64_t generation, presented_generation;
    sc_tick deadline;
    unsigned fallback_cycle;
    bool valid, logged, use_key, presented;
} lp;

void local_preview_capture_geometry(double x,double y,double w,double h){(void)x;(void)y;(void)w;(void)h;}
struct sc_point local_preview_remap_point(struct sc_point p){return p;}

void
local_preview_abort_preparation(struct sc_perspective *p) {
    if (sc_perspective_color_key_enabled()) {
        return;
    }
    lp.fallback_cycle = atomic_load(&p->camera_cycle);
    if (lp.use_key) {
        return;
    }
    lp.use_key = true;
    lp.valid = false;
    lp.deadline = 0;
    atomic_store(&p->compatibility_key, true);
    sc_native_source_release();
}

void
local_preview_on_presented(SDL_Texture *texture) {
    if (!lp.valid || texture != lp.texture) {
        return;
    }
    if (lp.presented_generation != lp.generation) {
        lp.presented_generation = lp.generation;
        sc_native_source_presented();
    }
    if (!lp.presented) {
        lp.presented = true;
        LOGI("SCRCPY_EZ_NATIVE_ALPHA_PRESENTED");
    }
}

bool
local_preview_is_composited(SDL_Texture *texture) {
    return texture && ((lp.valid && texture == lp.texture)
                    || sc_key_preview_is_composited(texture));
}

void
local_preview_destroy(void) {
    sc_key_preview_destroy();
    SDL_DestroyTexture(lp.texture);
    memset(&lp, 0, sizeof(lp));
}

static void
update_native_texture(SDL_Renderer *renderer, SDL_Surface *surface,
                      const AVFrame *frame) {
    if (frame && fabs((double) surface->w * frame->height
                    / (surface->h * (double) frame->width) - 1.0) > .025) {
        lp.valid = false;
        return;
    }
    if (!lp.texture || lp.width != surface->w || lp.height != surface->h) {
        SDL_DestroyTexture(lp.texture);
        lp.width = surface->w;
        lp.height = surface->h;
        lp.texture = SDL_CreateTexture(renderer, SDL_PIXELFORMAT_RGBA32,
                                       SDL_TEXTUREACCESS_STREAMING, lp.width, lp.height);
        if (lp.texture) {
            SDL_SetTextureBlendMode(lp.texture, SDL_BLENDMODE_NONE);
            SDL_SetTextureScaleMode(lp.texture, SDL_SCALEMODE_LINEAR);
        }
    }
    // DWM consumes premultiplied RGB; keep the original A8 values.
    if (!SDL_GetBooleanProperty(SDL_GetSurfaceProperties(surface),
                                "ez.native.premultiplied", false)) {
        for (int y = 0; y < surface->h; ++y) {
            uint8_t *pixel = (uint8_t *) surface->pixels + y * surface->pitch;
            for (int x = 0; x < surface->w; ++x, pixel += 4) {
                for (unsigned c = 0; c < 3; ++c) {
                    pixel[c] = (pixel[c] * pixel[3] + 127) / 255;
                }
            }
        }
    }
    lp.valid = lp.texture && SDL_UpdateTexture(lp.texture, NULL,
                                               surface->pixels, surface->pitch);
    if (lp.valid) {
        if (!lp.logged) {
            LOGI("NATIVE_ALPHA overlay active: %dx%d, original A8", lp.width, lp.height);
            lp.logged = true;
        }
    }
}

SDL_Texture *
local_preview_texture(SDL_Renderer *renderer, struct sc_perspective *p,
                      const AVFrame *frame, SDL_Texture *fallback) {
    if (!p->started) {
        return fallback;
    }
    if (sc_perspective_color_key_enabled()) {
        return sc_key_preview_texture(renderer, p, frame, fallback);
    }
    if (!lp.use_key && sc_native_source_scene_rejected()) {
        LOGI("Perspective color key fallback: native alpha scene rejected");
        local_preview_abort_preparation(p);
    }
    // Restore the app's layers before allowing either compositor to take over.
    if (lp.use_key) {
        if (sc_native_source_release_pending() && !sc_native_source_release()) {
            return fallback;
        }
        unsigned cycle = atomic_load(&p->camera_cycle);
        if (lp.fallback_cycle == cycle || !atomic_load(&p->active)) {
            return sc_key_preview_texture(renderer, p, frame, fallback);
        }
        enum sc_native_retry retry = sc_native_source_retry();
        if (retry == SC_NATIVE_RETRY_PENDING) {
            return fallback;
        }
        if (retry == SC_NATIVE_RETRY_FAILED) {
            lp.fallback_cycle = cycle;
            return sc_key_preview_texture(renderer, p, frame, fallback);
        }
        lp.use_key = false;
        atomic_store(&p->compatibility_key, false);
        LOGI("NATIVE_ALPHA retry on new camera session");
    }
    if (!lp.use_key) {
        // The phone confirms the actual scanner activity and unchanged geometry,
        // including when the user opens it inside a regular WeChat app window.
        sc_perspective_confirm_handoff(p, sc_native_source_confirmed_scene_epoch());
        bool available = !atomic_load(&p->stopped) && !atomic_load(&p->evicted);
        bool active = available && atomic_load(&p->active);
        sc_mutex_lock(&p->mutex);
        bool aligned = !p->calibrating && !atomic_load(&p->calibration_requested);
        sc_mutex_unlock(&p->mutex);
        bool scene = available && sc_native_source_scene_epoch();
        bool enabled = (active && aligned) || scene;
        sc_native_source_set_warming(active || scene);
        sc_native_source_set_enabled(enabled);
        SDL_Surface *surface = sc_native_source_take(&lp.generation);
        if (surface) {
            update_native_texture(renderer, surface, frame);
            SDL_DestroySurface(surface);
        }
        lp.valid = lp.valid && active && aligned && !scene && sc_native_source_valid();
        if (!enabled || lp.valid) {
            lp.deadline = 0;
            return lp.valid ? lp.texture : fallback;
        }
        sc_tick now = sc_tick_now();
        if (scene) {
            lp.deadline = 0;
        } else if (!lp.deadline) {
            // Scanner sessions run through a system-scene transition
            // (WeChat BaseScanUI). When the phone and tablet are opened
            // concurrently that transition may spend >5s selecting/rebinding
            // the scanner window; the old 5s deadline then expired and
            // permanently dropped the session into color-key compatibility
            // mode. Give scanner windows a wider grace period.
            lp.deadline = now + (p->scanner_alignment ? SC_TICK_FROM_SEC(12)
                                                      : NATIVE_ALPHA_WAIT);
        }
        bool failed = sc_native_source_failed();
        if (!failed && (scene || now < lp.deadline)) {
            return fallback;
        }
        LOGI("Perspective color key fallback: %s", failed ? "native alpha unavailable"
                                                          : "native alpha timed out");
        local_preview_abort_preparation(p);
    }
    return sc_native_source_release() ? sc_key_preview_texture(renderer, p, frame, fallback)
                                      : fallback;
}
