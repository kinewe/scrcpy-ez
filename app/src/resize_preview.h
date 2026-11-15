#ifndef SC_RESIZE_PREVIEW_H
#define SC_RESIZE_PREVIEW_H

#include <stdbool.h>
#include <SDL3/SDL_events.h>
#include <SDL3/SDL_rect.h>

#include "coords.h"

// A watch can also run on a producer thread. Only native window callbacks
// on the main thread may upload video textures or present the renderer.
static inline bool
sc_resize_preview_redraw_event(const SDL_Event *event, SDL_WindowID window_id,
                               bool main_thread, bool shown, bool busy) {
    if (!main_thread || !shown || busy) {
        return false;
    }
    switch (event->type) {
        case SDL_EVENT_WINDOW_EXPOSED:
        case SDL_EVENT_WINDOW_RESIZED:
        case SDL_EVENT_WINDOW_PIXEL_SIZE_CHANGED:
        case SDL_EVENT_WINDOW_MOVED:
            return event->window.windowID == window_id;
        default:
            return false;
    }
}

// Fit the last decoded frame while the Android layout/encoder catches up.
// The caller also uses this rectangle for input, so preview and clicks agree.
static inline bool
sc_resize_preview_rect(struct sc_size window, struct sc_size content,
                        SDL_FRect *rect) {
    if (!window.width || !window.height || !content.width || !content.height
            || (window.width == content.width
                && window.height == content.height)) {
        return false;
    }
    float scale_x = (float) window.width / content.width;
    float scale_y = (float) window.height / content.height;
    float scale = scale_x < scale_y ? scale_x : scale_y;
    rect->w = content.width * scale;
    rect->h = content.height * scale;
    rect->x = (window.width - rect->w) / 2.f;
    rect->y = (window.height - rect->h) / 2.f;
    return true;
}

#endif
