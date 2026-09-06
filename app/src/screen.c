#include "screen.h"

#include <assert.h>
#include <string.h>
#include <SDL3/SDL.h>
#include <SDL3/SDL_timer.h>
#ifdef _WIN32
#include <stdio.h>
#include <stdlib.h>
#include <windows.h>
#include <imm.h>
#define COBJMACROS
#include <msctf.h>
#include <objbase.h>
#include <winreg.h>
#endif

#include "events.h"
#include "fps_overlay.h"
#include "icon.h"
#include "options.h"
#include "util/log.h"
#include "util/sdl.h"

#define DISPLAY_MARGINS 96

#define DOWNCAST(SINK) container_of(SINK, struct sc_screen, frame_sink)

static void
set_aspect_ratio(struct sc_screen *screen, struct sc_size content_size) {
    assert(content_size.width && content_size.height);

    if (screen->window_aspect_ratio_lock) {
        float ar = (float) content_size.width / content_size.height;
        bool ok = SDL_SetWindowAspectRatio(screen->window, ar, ar);
        if (!ok) {
            LOGW("Could not set window aspect ratio: %s", SDL_GetError());
        }
    }
}

static inline struct sc_size
get_oriented_size(struct sc_size size, enum sc_orientation orientation) {
    struct sc_size oriented_size;
    if (sc_orientation_is_swap(orientation)) {
        oriented_size.width = size.height;
        oriented_size.height = size.width;
    } else {
        oriented_size.width = size.width;
        oriented_size.height = size.height;
    }
    return oriented_size;
}

static inline bool
is_windowed(struct sc_screen *screen) {
    return !(SDL_GetWindowFlags(screen->window) & (SDL_WINDOW_FULLSCREEN
                                                 | SDL_WINDOW_MINIMIZED
                                                 | SDL_WINDOW_MAXIMIZED));
}

// get the preferred display bounds (i.e. the screen bounds with some margins)
static bool
get_preferred_display_bounds(struct sc_size *bounds) {
    SDL_Rect rect;
    SDL_DisplayID display = SDL_GetPrimaryDisplay();
    if (!display) {
        LOGW("Could not get primary display: %s", SDL_GetError());
        return false;
    }

    bool ok = SDL_GetDisplayUsableBounds(display, &rect);
    if (!ok) {
        LOGW("Could not get display usable bounds: %s", SDL_GetError());
        return false;
    }

    bounds->width = MAX(0, rect.w - DISPLAY_MARGINS);
    bounds->height = MAX(0, rect.h - DISPLAY_MARGINS);
    return true;
}

static bool
is_optimal_size(struct sc_size current_size, struct sc_size content_size) {
    // The size is optimal if we can recompute one dimension of the current
    // size from the other
    return current_size.height == (uint32_t) current_size.width
                                * content_size.height / content_size.width
        || current_size.width == (uint32_t) current_size.height
                               * content_size.width / content_size.height;
}

// return the optimal size of the window, with the following constraints:
//  - it attempts to keep at least one dimension of the current_size (i.e. it
//    crops the black borders)
//  - it keeps the aspect ratio
//  - it scales down to make it fit in the display_size
static struct sc_size
get_optimal_size(struct sc_size current_size, struct sc_size content_size,
                 bool within_display_bounds) {
    if (content_size.width == 0 || content_size.height == 0) {
        // avoid division by 0
        return current_size;
    }

    struct sc_size window_size;

    struct sc_size display_size;
    if (!within_display_bounds ||
            !get_preferred_display_bounds(&display_size)) {
        // do not constraint the size
        window_size = current_size;
    } else {
        window_size.width = MIN(current_size.width, display_size.width);
        window_size.height = MIN(current_size.height, display_size.height);
    }

    if (is_optimal_size(window_size, content_size)) {
        return window_size;
    }

    bool keep_width = (uint32_t) content_size.width * window_size.height
                    > (uint32_t) content_size.height * window_size.width;
    if (keep_width) {
        // remove black borders on top and bottom
        window_size.height = (uint32_t) content_size.height * window_size.width
                           / content_size.width;
    } else {
        // remove black borders on left and right (or none at all if it already
        // fits)
        window_size.width = (uint32_t) content_size.width * window_size.height
                          / content_size.height;
    }

    return window_size;
}

// initially, there is no current size, so use the frame size as current size
// req_width and req_height, if not 0, are the sizes requested by the user
static inline struct sc_size
get_initial_optimal_size(struct sc_size content_size, uint16_t req_width,
                         uint16_t req_height) {
    struct sc_size window_size;
    if (!req_width && !req_height) {
        window_size = get_optimal_size(content_size, content_size, true);
    } else {
        if (req_width) {
            window_size.width = req_width;
        } else {
            // compute from the requested height
            window_size.width = (uint32_t) req_height * content_size.width
                              / content_size.height;
        }
        if (req_height) {
            window_size.height = req_height;
        } else {
            // compute from the requested width
            window_size.height = (uint32_t) req_width * content_size.height
                               / content_size.width;
        }
    }
    return window_size;
}

static inline void
sc_screen_track_resize(struct sc_screen *screen, struct sc_size size) {
    LOGV("Track resize: %" PRIu16 "x%" PRIu16, size.width, size.height);
    screen->resize_tracker.time = sc_tick_now();
    screen->resize_tracker.size = size;
}

static inline bool
sc_screen_is_relative_mode(struct sc_screen *screen) {
    // screen->im.mp may be NULL if --no-control
    return screen->im.mp && screen->im.mp->relative_mode;
}

static void
compute_content_rect(struct sc_size window_size, struct sc_size content_size,
                     bool is_icon, enum sc_render_fit render_fit,
                     SDL_FRect *rect) {
    if (is_icon) {
        if (content_size.width <= window_size.width
                && content_size.height <= window_size.height) {
            // Center without upscaling
            rect->x = (window_size.width - content_size.width) / 2.f;
            rect->y = (window_size.height - content_size.height) / 2.f;
            rect->w = content_size.width;
            rect->h = content_size.height;
            return;
        }
    } else if (render_fit == SC_RENDER_FIT_UNSCALED) {
        // Cast to float first because input sizes are unsigned
        float x = ((float) window_size.width - content_size.width) / 2.f;
        float y = ((float) window_size.height - content_size.height) / 2.f;
        rect->x = MAX(0, x);
        rect->y = MAX(0, y);
        rect->w = content_size.width;
        rect->h = content_size.height;
        return;
    } else if (render_fit == SC_RENDER_FIT_STRETCHED) {
        rect->x = 0;
        rect->y = 0;
        rect->w = window_size.width;
        rect->h = window_size.height;
        return;
    }

    assert(is_icon || render_fit == SC_RENDER_FIT_LETTERBOX);

    if (is_optimal_size(window_size, content_size)) {
        rect->x = 0;
        rect->y = 0;
        rect->w = window_size.width;
        rect->h = window_size.height;
        return;
    }

    bool keep_width = (uint32_t) content_size.width * window_size.height
                    > (uint32_t) content_size.height * window_size.width;
    if (keep_width) {
        rect->x = 0;
        rect->w = window_size.width;
        rect->h = (float) window_size.width * content_size.height
                                            / content_size.width;
        rect->y = (window_size.height - rect->h) / 2.f;
    } else {
        rect->y = 0;
        rect->h = window_size.height;
        rect->w = (float) window_size.height * content_size.width
                                             / content_size.height;
        rect->x = (window_size.width - rect->w) / 2.f;
    }
}

static void
sc_screen_update_content_rect(struct sc_screen *screen) {
    // Only upscale video frames, not icon
    bool is_icon = !screen->video || screen->disconnected;

    struct sc_size window_size = sc_sdl_get_window_size(screen->window);
    compute_content_rect(window_size, screen->content_size, is_icon,
                         screen->render_fit, &screen->rect);
}

// render the texture to the renderer
//
// Set the update_content_rect flag if the window or content size may have
// changed, so that the content rectangle is recomputed
static void
sc_screen_render(struct sc_screen *screen, bool update_content_rect) {
    assert(screen->window_shown);

    if (update_content_rect) {
        sc_screen_update_content_rect(screen);
    }

    SDL_Renderer *renderer = screen->renderer;
    struct sc_screen_bg_color bg = screen->bg;
    SDL_SetRenderDrawColor(renderer, bg.r, bg.g, bg.b, 0);
    sc_sdl_render_clear(renderer);

    SDL_Texture *texture = screen->tex.texture;
    if (!texture) {
        goto end;
    }

    float scale = SDL_GetWindowPixelDensity(screen->window);
    if (scale == 0) {
        // Just in case, but in practice the function can only fail when window
        // is invalid
        LOGE("Cannot get scale value: %s", SDL_GetError());
        scale = 1;
    }

    SDL_FRect geometry = {
        .x = screen->rect.x * scale,
        .y = screen->rect.y * scale,
        .w = screen->rect.w * scale,
        .h = screen->rect.h * scale,
    };
    enum sc_orientation orientation = screen->orientation;

    bool ok = false;
    if (orientation == SC_ORIENTATION_0) {
        // always align to a physical pixel
        geometry.x = (int32_t) geometry.x;
        geometry.y = (int32_t) geometry.y;
        ok = SDL_RenderTexture(renderer, texture, NULL, &geometry);
    } else {
        unsigned cw_rotation = sc_orientation_get_rotation(orientation);
        double angle = 90 * cw_rotation;

        SDL_FRect *dstrect = NULL;
        SDL_FRect rect;
        if (sc_orientation_is_swap(orientation)) {
            rect.x = geometry.x + (geometry.w - geometry.h) / 2.f;
            rect.y = geometry.y + (geometry.h - geometry.w) / 2.f;
            rect.w = geometry.h;
            rect.h = geometry.w;
            dstrect = &rect;
        } else {
            dstrect = &geometry;
        }

        SDL_FlipMode flip = sc_orientation_is_mirror(orientation)
                              ? SDL_FLIP_HORIZONTAL : 0;

        // always align to a physical pixel
        dstrect->x = (int32_t) dstrect->x;
        dstrect->y = (int32_t) dstrect->y;
        ok = SDL_RenderTextureRotated(renderer, texture, NULL, dstrect, angle,
                                      NULL, flip);
    }

    if (!ok) {
        LOGE("Could not render texture: %s", SDL_GetError());
    }

end:
    // a hidden overlay (Ctrl+F toggle) must not be rendered at all
    if (sc_fps_overlay_is_visible(&screen->fps_overlay)) {
        sc_fps_overlay_draw(&screen->fps_overlay, renderer);
    }
    sc_sdl_render_present(renderer);
}

// Interval of the one-shot SDL timer that keeps repainting while a lamp
// fade (hover or state cross-fade) is animating, so the animation does not
// depend on video frames arriving.
#define LAMP_ANIM_TIMER_MS 16
// Private event type for the lamp animation timer ticks. SDL_EVENT_USER is
// the base of the scrcpy event range (SC_EVENT_NEW_FRAME), so a value far
// above the registered events avoids intercepting real events.
#define SC_LAMP_ANIM_TICK_EVENT (SDL_EVENT_USER + 0x100)

static Uint32 SDLCALL
sc_screen_lamp_anim_timer_cb(void *userdata, SDL_TimerID timer_id,
                             Uint32 interval) {
    (void) userdata;
    (void) timer_id;
    (void) interval;

    // Only nudge the UI thread: the event handler repaints and re-arms the
    // timer if a fade is still running.
    SDL_Event event = { .type = SC_LAMP_ANIM_TICK_EVENT };
    SDL_PushEvent(&event);
    return 0; // one-shot
}

static void
sc_screen_arm_lamp_anim_timer(struct sc_screen *screen) {
    if (screen->lamp_anim_timer) {
        return; // already armed
    }
    screen->lamp_anim_timer =
        SDL_AddTimer(LAMP_ANIM_TIMER_MS, sc_screen_lamp_anim_timer_cb, NULL);
}

// arm the repaint timer while any lamp fade is running
static void
sc_screen_arm_lamp_anim_if_animating(struct sc_screen *screen) {
    if (sc_fps_overlay_is_lamp_hover_animating(&screen->fps_overlay)
            || sc_fps_overlay_is_lamp_state_animating(
                &screen->fps_overlay)) {
        sc_screen_arm_lamp_anim_timer(screen);
    }
}

static void
sc_screen_request_resize_display(struct sc_screen *screen, uint16_t width,
                                 uint16_t height) {
    assert(screen->flex_display);
    assert(!screen->camera);
    if (sc_orientation_is_swap(screen->orientation)) {
        uint16_t tmp = width;
        width = height;
        height = tmp;
    }

    LOGV("resize_display(%" PRIu16 ", %" PRIu16 ")", width, height);
    sc_controller_resize_display(screen->controller, width, height);
}

static void
sc_screen_on_resize(struct sc_screen *screen, const SDL_WindowEvent *event) {
    // This event can be triggered before the window is shown
    if (!screen->window_shown) {
        return;
    }

    if (event->type == SDL_EVENT_WINDOW_PIXEL_SIZE_CHANGED) {
        sc_screen_render(screen, true);
    } else {
        assert(event->type == SDL_EVENT_WINDOW_RESIZED);
        if (screen->flex_display) {
            assert(!(event->data1 & ~0xFFFF));
            assert(!(event->data2 & ~0xFFFF));
            uint16_t width = event->data1;
            uint16_t height = event->data2;

            struct sc_resize_tracker *tracker = &screen->resize_tracker;
            if (tracker->time
                    && sc_tick_now() >= tracker->time + SC_TICK_FROM_MS(3000)) {
                // Remove obsolete request
                tracker->time = 0;
            }
            if (tracker->time && tracker->size.width == width
                              && tracker->size.height == height) {
                // This resize event is the result of a previous (recent) resize
                // request triggered by a change in the frame's dimensions.
                LOGV("Ignore local resize: %" PRIu16 "x%" PRIu16,
                     width, height);
                tracker->time = 0;
            } else {
                sc_screen_request_resize_display(screen, width, height);
            }
        }
    }
}

#if defined(__APPLE__) || defined(_WIN32)
# define CONTINUOUS_RESIZING_WORKAROUND
#endif

#ifdef CONTINUOUS_RESIZING_WORKAROUND
// On Windows and MacOS, resizing blocks the event loop, so resizing events are
// not triggered. As a workaround, handle them in an event handler.
//
// <https://bugzilla.libsdl.org/show_bug.cgi?id=2077>
// <https://stackoverflow.com/a/40693139/1987178>
static bool
event_watcher(void *data, SDL_Event *event) {
    struct sc_screen *screen = data;
    assert(screen->video);

    if (event->type == SDL_EVENT_WINDOW_PIXEL_SIZE_CHANGED
            || event->type == SDL_EVENT_WINDOW_RESIZED) {
        // In practice, it seems to always be called from the same thread in
        // that specific case. Anyway, it's just a workaround.
        sc_screen_on_resize(screen, &event->window);
    }

    return true;
}
#endif

static bool
sc_screen_frame_sink_open(struct sc_frame_sink *sink,
                          const AVCodecContext *ctx,
                          const struct sc_stream_session *session) {
    assert(ctx->pix_fmt == AV_PIX_FMT_YUV420P);

    struct sc_screen *screen = DOWNCAST(sink);

    if (ctx->width <= 0 || ctx->width > 0xFFFF
            || ctx->height <= 0 || ctx->height > 0xFFFF) {
        LOGE("Invalid video size: %dx%d", ctx->width, ctx->height);
        return false;
    }

    screen->current_session = *session;

    assert(session->video.width && session->video.height);
    if (session->video.width > 0xFFFF || session->video.height > 0xFFFF) {
        LOGE("Size too large: %" PRIu32 "x%" PRIu32, session->video.width,
                                                     session->video.height);
        return false;
    }

    struct sc_size *size = malloc(sizeof(*size));
    if (!size) {
        LOG_OOM();
        return false;
    }
    size->width = session->video.width;
    size->height = session->video.height;

    bool ok = sc_push_event_with_data(SC_EVENT_OPEN_WINDOW, size);
    if (!ok) {
        free(size);
        return false;
    }

#ifndef NDEBUG
    screen->open = true;
#endif

    // nothing to do, the screen is already open on the main thread
    return true;
}

static void
sc_screen_frame_sink_close(struct sc_frame_sink *sink) {
    struct sc_screen *screen = DOWNCAST(sink);
    (void) screen;
#ifndef NDEBUG
    screen->open = false;
#endif

    // nothing to do, the screen lifecycle is not managed by the frame producer
}

static bool
sc_screen_frame_sink_push(struct sc_frame_sink *sink, const AVFrame *frame) {
    struct sc_screen *screen = DOWNCAST(sink);
    assert(screen->video);

    sc_mutex_lock(&screen->mutex);
    bool previous_skipped = sc_frame_buffer_has_frame(&screen->fb);
    bool ok = sc_frame_buffer_push(&screen->fb, frame);
    screen->prevent_auto_resize = screen->current_session.video.client_resized;
    sc_mutex_unlock(&screen->mutex);
    if (!ok) {
        return false;
    }

    if (previous_skipped) {
        sc_fps_counter_add_skipped_frame(&screen->fps_counter);
        // The SC_EVENT_NEW_FRAME triggered for the previous frame will consume
        // this new frame instead
    } else {
        // Post the event on the UI thread
        bool ok = sc_push_event(SC_EVENT_NEW_FRAME);
        if (!ok) {
            return false;
        }
    }

    return true;
}

static bool
sc_screen_frame_sink_push_session(struct sc_frame_sink *sink,
                                  const struct sc_stream_session *session) {
    struct sc_screen *screen = DOWNCAST(sink);
    screen->current_session = *session;
    return true;
}

bool
sc_screen_init(struct sc_screen *screen,
               const struct sc_screen_params *params) {
    screen->controller = params->controller;

    screen->resize_pending = false;
    screen->window_shown = false;
    screen->always_on_top = params->always_on_top;
    screen->lamp_anim_timer = 0;
    screen->paused = false;
    screen->resume_frame = NULL;
    screen->orientation = SC_ORIENTATION_0;
    screen->disconnected = false;
    screen->disconnect_started = false;
    screen->hid_keyboard = params->hid_keyboard;
#ifdef _WIN32
    screen->original_hkl = NULL;
    screen->layout_forced = false;
    screen->ime_open_saved = false;
    screen->ime_open_saved_valid = false;
    screen->ime_restore_allowed = true;
    screen->startup_hkl = params->startup_hkl;
    screen->tsf_thread_mgr = NULL;
    screen->tsf_empty_doc = NULL;
    screen->tsf_ime_blocked = false;
    screen->tsf_ime_unavailable = false;
#endif

    screen->video = params->video;
    screen->camera = params->camera;
    screen->window_aspect_ratio_lock = params->window_aspect_ratio_lock;
    screen->render_fit = params->render_fit;
    screen->flex_display = params->flex_display;

    screen->bg.r = (params->background_color >> 16) & 0xFF;
    screen->bg.g = (params->background_color >> 8) & 0xFF;
    screen->bg.b = params->background_color & 0xFF;

    screen->req.x = params->window_x;
    screen->req.y = params->window_y;
    screen->req.width = params->window_width;
    screen->req.height = params->window_height;
    screen->req.fullscreen = params->fullscreen;
    screen->req.start_fps_counter = params->start_fps_counter;

    screen->prevent_auto_resize = false;

    screen->resize_tracker.time = 0;
    screen->resize_tracker.size.width = 0;
    screen->resize_tracker.size.height = 0;

    bool ok = sc_mutex_init(&screen->mutex);
    if (!ok) {
        return false;
    }

    ok = sc_frame_buffer_init(&screen->fb);
    if (!ok) {
        goto error_destroy_mutex;
    }

    if (!sc_fps_counter_init(&screen->fps_counter)) {
        goto error_destroy_frame_buffer;
    }

    if (screen->video) {
        screen->orientation = params->orientation;
        if (screen->orientation != SC_ORIENTATION_0) {
            LOGI("Initial display orientation set to %s",
                 sc_orientation_get_name(screen->orientation));
        }
    }

    // Always create the window hidden to prevent blinking during initialization
    uint32_t window_flags = SDL_WINDOW_HIGH_PIXEL_DENSITY | SDL_WINDOW_HIDDEN;
    if (params->always_on_top) {
        window_flags |= SDL_WINDOW_ALWAYS_ON_TOP;
    }
    if (params->window_borderless) {
        window_flags |= SDL_WINDOW_BORDERLESS;
    }
    if (params->video) {
        // The window will be shown on first frame
        window_flags |= SDL_WINDOW_RESIZABLE;
    }

    const char *title = params->window_title;
    assert(title);

    int x = SDL_WINDOWPOS_UNDEFINED;
    int y = SDL_WINDOWPOS_UNDEFINED;
    int width = 256;
    int height = 256;
    if (params->window_x != SC_WINDOW_POSITION_UNDEFINED) {
        x = params->window_x;
    }
    if (params->window_y != SC_WINDOW_POSITION_UNDEFINED) {
        y = params->window_y;
    }
    if (params->window_width) {
        width = params->window_width;
    }
    if (params->window_height) {
        height = params->window_height;
    }

    // The window will be positioned and sized on first video frame
    screen->window =
        sc_sdl_create_window(title, x, y, width, height, window_flags);
    if (!screen->window) {
        LOGE("Could not create window: %s", SDL_GetError());
        goto error_destroy_fps_counter;
    }

    screen->renderer = SDL_CreateRenderer(screen->window, NULL);
    if (!screen->renderer) {
        LOGE("Could not create renderer: %s", SDL_GetError());
        goto error_destroy_window;
    }

#ifdef SC_DISPLAY_FORCE_OPENGL_CORE_PROFILE
    screen->gl_context = NULL;

    // starts with "opengl"
    const char *renderer_name = SDL_GetRendererName(screen->renderer);
    bool use_opengl = renderer_name && !strncmp(renderer_name, "opengl", 6);
    if (use_opengl) {
        // Persuade macOS to give us something better than OpenGL 2.1.
        // If we create a Core Profile context, we get the best OpenGL version.
        bool ok = SDL_GL_SetAttribute(SDL_GL_CONTEXT_PROFILE_MASK,
                                      SDL_GL_CONTEXT_PROFILE_CORE);
        if (!ok) {
            LOGW("Could not set a GL Core Profile Context");
        }

        LOGD("Creating OpenGL Core Profile context");
        screen->gl_context = SDL_GL_CreateContext(screen->window);
        if (!screen->gl_context) {
            LOGE("Could not create OpenGL context: %s", SDL_GetError());
            goto error_destroy_renderer;
        }
    }
#endif

    bool mipmaps = params->video;
    ok = sc_texture_init(&screen->tex, screen->renderer, mipmaps);
    if (!ok) {
        goto error_destroy_renderer;
    }

    if (!sc_fps_overlay_init(&screen->fps_overlay, screen->renderer)) {
        // not fatal, the overlay is a nice-to-have
        LOGW("Could not initialize FPS overlay");
    }

    ok = SDL_StartTextInput(screen->window);
    if (!ok) {
        LOGE("Could not enable text input: %s", SDL_GetError());
        goto error_destroy_texture;
    }

    SDL_Surface *icon = sc_icon_load(SC_ICON_FILENAME_SCRCPY);
    if (icon) {
        if (!SDL_SetWindowIcon(screen->window, icon)) {
            LOGW("Could not set window icon: %s", SDL_GetError());
        }

        if (!params->video) {
            screen->content_size.width = icon->w;
            screen->content_size.height = icon->h;
            ok = sc_texture_set_from_surface(&screen->tex, icon);
            if (!ok) {
                LOGE("Could not set icon: %s", SDL_GetError());
            }
        }

        sc_icon_destroy(icon);
    } else {
        // not fatal
        LOGE("Could not load icon");

        if (!params->video) {
            // Make sure the content size is initialized
            screen->content_size.width = 256;
            screen->content_size.height = 256;
        }
    }

    screen->frame = av_frame_alloc();
    if (!screen->frame) {
        LOG_OOM();
        goto error_destroy_texture;
    }

    struct sc_input_manager_params im_params = {
        .controller = params->controller,
        .fp = params->fp,
        .screen = screen,
        .kp = params->kp,
        .mp = params->mp,
        .gp = params->gp,
        .camera = params->camera,
        .mouse_bindings = params->mouse_bindings,
        .legacy_paste = params->legacy_paste,
        .clipboard_autosync = params->clipboard_autosync,
        .clipboard_sync = params->clipboard_sync,
        .clipboard_push_on_start = params->clipboard_push_on_start,
        .shortcut_mods = params->shortcut_mods,
    };

    sc_input_manager_init(&screen->im, &im_params);

    // Initialize even if not used for simplicity
    sc_mouse_capture_init(&screen->mc, screen->window, params->shortcut_mods);

#ifdef CONTINUOUS_RESIZING_WORKAROUND
    if (screen->video) {
        ok = SDL_AddEventWatch(event_watcher, screen);
        if (!ok) {
            LOGW("Could not add event watcher for continuous resizing: %s",
                 SDL_GetError());
        }
    }
#endif

    memset(&screen->current_session, 0, sizeof(screen->current_session));

    static const struct sc_frame_sink_ops ops = {
        .open = sc_screen_frame_sink_open,
        .close = sc_screen_frame_sink_close,
        .push = sc_screen_frame_sink_push,
        .push_session = sc_screen_frame_sink_push_session,
    };

    screen->frame_sink.ops = &ops;

#ifndef NDEBUG
    screen->open = false;
#endif

    if (!screen->video) {
        // Show the window immediately
        screen->window_shown = true;
        sc_sdl_show_window(screen->window);

        if (sc_screen_is_relative_mode(screen)) {
            // Capture mouse immediately if video mirroring is disabled
            sc_mouse_capture_set_active(&screen->mc, true);
        }
    }

    return true;

error_destroy_texture:
    sc_texture_destroy(&screen->tex);
error_destroy_renderer:
#ifdef SC_DISPLAY_FORCE_OPENGL_CORE_PROFILE
    if (screen->gl_context) {
        SDL_GL_DestroyContext(screen->gl_context);
    }
#endif
    SDL_DestroyRenderer(screen->renderer);
error_destroy_window:
    SDL_DestroyWindow(screen->window);
error_destroy_fps_counter:
    sc_fps_counter_destroy(&screen->fps_counter);
error_destroy_frame_buffer:
    sc_frame_buffer_destroy(&screen->fb);
error_destroy_mutex:
    sc_mutex_destroy(&screen->mutex);

    return false;
}

static void
sc_screen_show_initial_window(struct sc_screen *screen) {
    int x = screen->req.x != SC_WINDOW_POSITION_UNDEFINED
          ? screen->req.x : (int) SDL_WINDOWPOS_CENTERED;
    int y = screen->req.y != SC_WINDOW_POSITION_UNDEFINED
          ? screen->req.y : (int) SDL_WINDOWPOS_CENTERED;
    struct sc_point position = {
        .x = x,
        .y = y,
    };

    struct sc_size window_size =
        get_initial_optimal_size(screen->content_size, screen->req.width,
                                                       screen->req.height);

    if (screen->flex_display
            && window_size.width == screen->content_size.width
            && window_size.height == screen->content_size.height) {
        // Avoid sending an unnecessary initial "resize display" request to the
        // server if the size has not changed.
        sc_screen_track_resize(screen, window_size);
    }

    assert(is_windowed(screen));
    set_aspect_ratio(screen, screen->content_size);
    sc_sdl_set_window_size(screen->window, window_size);
    sc_sdl_set_window_position(screen->window, position);

    if (screen->req.fullscreen) {
        sc_screen_toggle_fullscreen(screen);
    }

    if (screen->req.start_fps_counter) {
        sc_fps_counter_start(&screen->fps_counter);
    }

    screen->window_shown = true;
    sc_sdl_show_window(screen->window);
    sc_screen_update_content_rect(screen);
}

void
sc_screen_hide_window(struct sc_screen *screen) {
    sc_sdl_hide_window(screen->window);
    screen->window_shown = false;
}

void
sc_screen_interrupt(struct sc_screen *screen) {
    sc_fps_counter_interrupt(&screen->fps_counter);
}

static void
sc_screen_interrupt_disconnect(struct sc_screen *screen) {
    if (screen->disconnect_started) {
        sc_disconnect_interrupt(&screen->disconnect);
    }
}

#ifdef _WIN32
static void
sc_screen_apply_keyboard_layout(struct sc_screen *screen, bool english);
static void
sc_screen_tsf_destroy(struct sc_screen *screen, HWND hwnd);
#endif

void
sc_screen_join(struct sc_screen *screen) {
    sc_fps_counter_join(&screen->fps_counter);
    if (screen->disconnect_started) {
        sc_disconnect_join(&screen->disconnect);
    }
}

void
sc_screen_destroy(struct sc_screen *screen) {
#ifndef NDEBUG
    assert(!screen->open);
#endif
    if (screen->disconnect_started) {
        sc_disconnect_destroy(&screen->disconnect);
    }
    if (screen->lamp_anim_timer) {
        SDL_RemoveTimer(screen->lamp_anim_timer);
        screen->lamp_anim_timer = 0;
    }
    sc_texture_destroy(&screen->tex);
    sc_fps_overlay_destroy(&screen->fps_overlay);
    av_frame_free(&screen->frame);
#ifdef SC_DISPLAY_FORCE_OPENGL_CORE_PROFILE
    SDL_GL_DestroyContext(screen->gl_context);
#endif
    SDL_DestroyRenderer(screen->renderer);
#ifdef _WIN32
    // The window may be closed directly (X button / Alt+F4) while focused:
    // SDL destroys the window without posting SDL_EVENT_WINDOW_FOCUS_LOST,
    // so the focus-lost handler below would never run and the computer
    // would be left on the forced English layout. Restore the original
    // layout on every exit path, while the window handle is still valid.
    // No-op when the layout was already restored (layout_forced == false)
    // or never forced (original_hkl == NULL).
    if (screen->hid_keyboard && screen->layout_forced) {
        sc_screen_apply_keyboard_layout(screen, false);
    }
    HWND hwnd = (HWND) SDL_GetPointerProperty(
            SDL_GetWindowProperties(screen->window),
            SDL_PROP_WINDOW_WIN32_HWND_POINTER, NULL);
    sc_screen_tsf_destroy(screen, hwnd);
#endif
    SDL_DestroyWindow(screen->window);
    sc_fps_counter_destroy(&screen->fps_counter);
    sc_frame_buffer_destroy(&screen->fb);
    sc_mutex_destroy(&screen->mutex);

    SDL_Event event;
    bool has_event =
        sc_dequeue_event(SC_EVENT_DISCONNECTED_ICON_LOADED, &event);
    if (has_event) {
        assert(event.type == SC_EVENT_DISCONNECTED_ICON_LOADED);
        // The event was posted, but not handled, the icon must be freed
        SDL_Surface *dangling_icon = event.user.data1;
        sc_icon_destroy(dangling_icon);
    }

    has_event = sc_dequeue_event(SC_EVENT_OPEN_WINDOW, &event);
    if (has_event) {
        assert(event.type == SC_EVENT_OPEN_WINDOW);
        // The event was posted, but not handled, the size must be freed
        struct sc_size * size = event.user.data1;
        free(size);
    }
}

static void
resize_for_content(struct sc_screen *screen, struct sc_size old_content_size,
                   struct sc_size new_content_size) {
    assert(screen->video);

    struct sc_size target_size = new_content_size;
    if (!screen->flex_display) {
        struct sc_size window_size = sc_sdl_get_window_size(screen->window);
        // Scale proportionally
        target_size.width = (uint32_t) window_size.width * target_size.width
                          / old_content_size.width;
        target_size.height = (uint32_t) window_size.height * target_size.height
                           / old_content_size.height;
    }
    target_size = get_optimal_size(target_size, new_content_size, true);
    assert(is_windowed(screen));
    set_aspect_ratio(screen, new_content_size);
    sc_sdl_set_window_size(screen->window, target_size);
}

static void
set_content_size(struct sc_screen *screen, struct sc_size new_content_size,
                 bool resize) {
    assert(screen->video);

    if (resize) {
        if (is_windowed(screen)) {
            resize_for_content(screen, screen->content_size, new_content_size);
        } else if (screen->flex_display) {
            // Force a display resize, the client cannot resize in fullscreen
            struct sc_size size = sc_sdl_get_window_size(screen->window);
            sc_screen_request_resize_display(screen, size.width, size.height);
        } else if (!screen->resize_pending) {
            // Store the windowed size to be able to compute the optimal size
            // once fullscreen/maximized/minimized are disabled
            screen->windowed_content_size = screen->content_size;
            screen->resize_pending = true;
        }
    }

    screen->content_size = new_content_size;
}

static void
apply_pending_resize(struct sc_screen *screen) {
    assert(screen->video);

    assert(is_windowed(screen));
    if (screen->resize_pending) {
        resize_for_content(screen, screen->windowed_content_size,
                                   screen->content_size);
        screen->resize_pending = false;
    }
}

void
sc_screen_set_orientation(struct sc_screen *screen,
                          enum sc_orientation orientation) {
    assert(screen->video);

    if (orientation == screen->orientation) {
        return;
    }

    struct sc_size new_content_size =
        get_oriented_size(screen->frame_size, orientation);

    set_content_size(screen, new_content_size, true);

    screen->orientation = orientation;
    LOGI("Display orientation set to %s", sc_orientation_get_name(orientation));

    sc_screen_render(screen, true);
}

static bool
sc_screen_apply_frame(struct sc_screen *screen, bool can_resize) {
    assert(screen->video);
    assert(screen->window_shown);

    sc_fps_counter_add_rendered_frame(&screen->fps_counter);
    sc_fps_overlay_on_frame(&screen->fps_overlay);

    AVFrame *frame = screen->frame;
    struct sc_size new_frame_size = {frame->width, frame->height};

    if (!new_frame_size.width || !new_frame_size.height) {
        LOGE("Invalid frame size: %" PRIu32 "x%" PRIu32,
             new_frame_size.width, new_frame_size.height);
        return false;
    }

    if (screen->frame_size.width != new_frame_size.width
            || screen->frame_size.height != new_frame_size.height) {

        // frame dimension changed
        screen->frame_size = new_frame_size;

        struct sc_size new_content_size =
            get_oriented_size(new_frame_size, screen->orientation);

        if (screen->flex_display) {
            sc_screen_track_resize(screen, new_content_size);
        }

        set_content_size(screen, new_content_size, can_resize);
        sc_screen_update_content_rect(screen);
    }

    bool ok = sc_texture_set_from_frame(&screen->tex, frame);
    if (!ok) {
        return false;
    }

    sc_screen_render(screen, false);
    return true;
}

#ifdef _WIN32
// Absolute-latency measurement: log (wall clock ms, frame pts us) for
// every rendered frame to %%TEMP%%\\scrcpy_frame_log.txt (overwritten on
// each scrcpy start, unbuffered so data survives hard kills). The
// analysis script pairs screen pulse detections with the nearest frame
// and converts pts to device epoch ms via adb clock calibration.
static FILE *g_frame_log;
static void
frame_log_open(void) {
    if (g_frame_log) {
        return;
    }
    const char *tmp = getenv("TEMP");
    char path[MAX_PATH];
    if (tmp) {
        snprintf(path, sizeof(path), "%s\\scrcpy_frame_log.txt", tmp);
    } else {
        snprintf(path, sizeof(path), "scrcpy_frame_log.txt");
    }
    g_frame_log = fopen(path, "w");
    if (g_frame_log) {
        setvbuf(g_frame_log, NULL, _IONBF, 0);
    }
}

static int64_t
unix_ms_now(void) {
    FILETIME ft;
    GetSystemTimeAsFileTime(&ft);
    ULARGE_INTEGER ul;
    ul.LowPart = ft.dwLowDateTime;
    ul.HighPart = ft.dwHighDateTime;
    return (int64_t) ((ul.QuadPart - 116444736000000000ULL) / 10000);
}

static void
frame_log_write(int64_t pts_us) {
    if (pts_us < 0) {
        return; // no pts (AV_NOPTS_VALUE)
    }
    frame_log_open();
    if (g_frame_log) {
        // 2 columns: wall_ms(render), pts_us(device)
        fprintf(g_frame_log, "%lld,%lld\n", (long long) unix_ms_now(),
                (long long) pts_us);
    }
}
#endif

static bool
sc_screen_update_frame(struct sc_screen *screen) {
    assert(screen->video);

    if (screen->paused) {
        if (!screen->resume_frame) {
            screen->resume_frame = av_frame_alloc();
            if (!screen->resume_frame) {
                LOG_OOM();
                return false;
            }
        } else {
            av_frame_unref(screen->resume_frame);
        }
        sc_mutex_lock(&screen->mutex);
        sc_frame_buffer_consume(&screen->fb, screen->resume_frame);
        sc_mutex_unlock(&screen->mutex);
        return true;
    }

    av_frame_unref(screen->frame);
    sc_mutex_lock(&screen->mutex);
    sc_frame_buffer_consume(&screen->fb, screen->frame);
    // read with lock held
    bool can_resize = !screen->prevent_auto_resize;
    sc_mutex_unlock(&screen->mutex);
#ifdef _WIN32
    frame_log_write(screen->frame->pts);
#endif
    return sc_screen_apply_frame(screen, can_resize);
}

void
sc_screen_set_paused(struct sc_screen *screen, bool paused) {
    assert(screen->video);

    if (!paused && !screen->paused) {
        // nothing to do
        return;
    }

    if (screen->paused && screen->resume_frame) {
        // If display screen was paused, refresh the frame immediately, even if
        // the new state is also paused.
        av_frame_free(&screen->frame);
        screen->frame = screen->resume_frame;
        screen->resume_frame = NULL;
        bool ok = sc_screen_apply_frame(screen, true);
        if (!ok) {
            LOGE("Resume frame update failed");
        }
    }

    if (!paused) {
        LOGI("Display screen unpaused");
    } else if (!screen->paused) {
        LOGI("Display screen paused");
    } else {
        LOGI("Display screen re-paused");
    }

    screen->paused = paused;
}

void
sc_screen_toggle_fullscreen(struct sc_screen *screen) {
    assert(screen->video);

    bool req_fullscreen =
        !(SDL_GetWindowFlags(screen->window) & SDL_WINDOW_FULLSCREEN);

    bool ok = SDL_SetWindowFullscreen(screen->window, req_fullscreen);
    if (!ok) {
        LOGW("Could not switch fullscreen mode: %s", SDL_GetError());
        return;
    }

    LOGD("Requested %s mode", req_fullscreen ? "fullscreen" : "windowed");
}

// apply the always-on-top state without arming any lamp animation; used by
// both the Ctrl+T shortcut (which arms the cross-fade) and the lamp click
// (which has its own preview fade-out)
static void
sc_screen_set_always_on_top(struct sc_screen *screen, bool on_top) {
    bool ok = SDL_SetWindowAlwaysOnTop(screen->window, on_top);
    if (!ok) {
        LOGW("Could not toggle always-on-top mode: %s", SDL_GetError());
        return;
    }

    screen->always_on_top = on_top;
    // keep the fps overlay status lamp in sync
    sc_fps_overlay_set_always_on_top(&screen->fps_overlay, on_top);
    // repaint immediately so the lamp updates at once, without waiting for
    // the next frame
    sc_screen_render(screen, false);
    LOGI("Window always-on-top %s", on_top ? "enabled" : "disabled");
}

void
sc_screen_toggle_always_on_top(struct sc_screen *screen) {
    // Ctrl+T: cross-fade the lamp between the two terminal states
    sc_fps_overlay_start_state_transition(&screen->fps_overlay);
    sc_screen_set_always_on_top(screen, !screen->always_on_top);
    // keep repainting while the fade runs, even without video frames
    sc_screen_arm_lamp_anim_if_animating(screen);
}

void
sc_screen_toggle_fps_overlay(struct sc_screen *screen) {
    struct sc_fps_overlay *overlay = &screen->fps_overlay;
    bool visible = !sc_fps_overlay_is_visible(overlay);
    sc_fps_overlay_set_visible(overlay, visible);
    LOGI("fps overlay %s", visible ? "shown" : "hidden");
    // repaint immediately so the toggle takes effect at once
    sc_screen_render(screen, false);
}

void
sc_screen_resize_to_fit(struct sc_screen *screen) {
    assert(screen->video);

    if (!is_windowed(screen)) {
        return;
    }

    if (screen->render_fit == SC_RENDER_FIT_STRETCHED) {
        // nothing to do
        return;
    }

    struct sc_size window_size = sc_sdl_get_window_size(screen->window);

    if (screen->render_fit == SC_RENDER_FIT_UNSCALED) {
        struct sc_size content_size = screen->content_size;
        set_aspect_ratio(screen, content_size);
        sc_sdl_set_window_size(screen->window, content_size);

        int32_t x_offset = 0;
        if (content_size.width < window_size.width) {
            x_offset = (window_size.width - content_size.width) / 2;
        }
        int32_t y_offset = 0;
        if (content_size.height < window_size.height) {
            y_offset = (window_size.height - content_size.height) / 2;
        }
        assert(x_offset >= 0 && y_offset >= 0);
        if (x_offset || y_offset) {
            struct sc_point pos = sc_sdl_get_window_position(screen->window);
            pos.x += x_offset;
            pos.y += y_offset;
            sc_sdl_set_window_position(screen->window, pos);
        }

        LOGD("Resized to content size: %ux%u", content_size.width,
                                               content_size.height);
        return;
    }

    assert(screen->render_fit == SC_RENDER_FIT_LETTERBOX);

    struct sc_point point = sc_sdl_get_window_position(screen->window);

    struct sc_size optimal_size =
        get_optimal_size(window_size, screen->content_size, false);

    // Center the window related to the device screen
    assert(optimal_size.width <= window_size.width);
    assert(optimal_size.height <= window_size.height);

    struct sc_point new_position = {
        .x = point.x + (window_size.width - optimal_size.width) / 2,
        .y = point.y + (window_size.height - optimal_size.height) / 2,
    };

    set_aspect_ratio(screen, screen->content_size);
    sc_sdl_set_window_size(screen->window, optimal_size);
    sc_sdl_set_window_position(screen->window, new_position);
    LOGD("Resized to optimal size: %ux%u", optimal_size.width,
                                           optimal_size.height);
}

void
sc_screen_resize_to_pixel_perfect(struct sc_screen *screen) {
    assert(screen->video);

    if (!is_windowed(screen)) {
        return;
    }

    struct sc_size content_size = screen->content_size;
    set_aspect_ratio(screen, content_size);
    sc_sdl_set_window_size(screen->window, content_size);
    LOGD("Resized to pixel-perfect: %ux%u", content_size.width,
                                            content_size.height);
}

static void
sc_disconnect_on_icon_loaded(struct sc_disconnect *d, SDL_Surface *icon,
                             void *userdata) {
    (void) d;
    (void) userdata;

    bool ok = sc_push_event_with_data(SC_EVENT_DISCONNECTED_ICON_LOADED, icon);
    if (!ok) {
        sc_icon_destroy(icon);
    }
}

static void
sc_disconnect_on_timeout(struct sc_disconnect *d, void *userdata) {
    (void) d;
    (void) userdata;

    bool ok = sc_push_event(SC_EVENT_DISCONNECTED_TIMEOUT);
    (void) ok; // ignore failure
}

#ifdef _WIN32
// TSF empty-document block, enabled by default: focus an empty document
// manager on scrcpy's UI thread so text services (Sogou, Microsoft Pinyin)
// have no composition target and pass keys through. This avoids
// ActivateKeyboardLayout(00000409), which on Windows 8+ is system-wide while
// this process owns focus and pollutes other windows' IME mode memory.
// Set SCRCPY_IME_NO_TSF_BLOCK=1 to disable and use the legacy HKL path.
static bool
sc_screen_tsf_block_enabled(void) {
    return getenv("SCRCPY_IME_NO_TSF_BLOCK") == NULL;
}

static bool
sc_screen_tsf_block(struct sc_screen *screen, HWND hwnd) {
    if (screen->tsf_ime_blocked) {
        return true;
    }
    if (screen->tsf_ime_unavailable) {
        return false;
    }
    if (!sc_screen_tsf_block_enabled()) {
        LOGI("TSF block disabled via SCRCPY_IME_NO_TSF_BLOCK, using legacy "
             "HKL forcing");
        return false;
    }

    HRESULT hr = CoInitializeEx(NULL, COINIT_APARTMENTTHREADED);
    if (hr != S_OK && hr != S_FALSE && hr != RPC_E_CHANGED_MODE) {
        LOGW("CoInitializeEx failed for TSF block: 0x%08lx",
             (unsigned long) hr);
        screen->tsf_ime_unavailable = true;
        return false;
    }

    HMODULE mod = LoadLibraryW(L"msctf.dll");
    if (!mod) {
        LOGW("Could not load msctf.dll for TSF block");
        screen->tsf_ime_unavailable = true;
        return false;
    }

    HRESULT (WINAPI *tf_get_thread_mgr)(ITfThreadMgr **) =
        (HRESULT (WINAPI *)(ITfThreadMgr **)) (void *)
            GetProcAddress(mod, "TF_GetThreadMgr");
    HRESULT (WINAPI *tf_create_thread_mgr)(ITfThreadMgr **) =
        (HRESULT (WINAPI *)(ITfThreadMgr **)) (void *)
            GetProcAddress(mod, "TF_CreateThreadMgr");

    ITfThreadMgr *tm = NULL;
    if ((!tf_get_thread_mgr || FAILED(tf_get_thread_mgr(&tm)))
            && (!tf_create_thread_mgr
                || FAILED(tf_create_thread_mgr(&tm)))) {
        LOGW("Could not create TSF thread manager");
        screen->tsf_ime_unavailable = true;
        return false;
    }

    TfClientId client_id = 0;
    hr = ITfThreadMgr_Activate(tm, &client_id);
    if (FAILED(hr)) {
        LOGW("Could not activate TSF thread manager: 0x%08lx",
             (unsigned long) hr);
        ITfThreadMgr_Release(tm);
        screen->tsf_ime_unavailable = true;
        return false;
    }

    ITfDocumentMgr *empty = NULL;
    hr = ITfThreadMgr_CreateDocumentMgr(tm, &empty);
    if (FAILED(hr) || !empty) {
        LOGW("Could not create empty TSF document manager: 0x%08lx",
             (unsigned long) hr);
        ITfThreadMgr_Deactivate(tm);
        ITfThreadMgr_Release(tm);
        screen->tsf_ime_unavailable = true;
        return false;
    }

    // Associate the empty document with the window so TSF keeps it focused
    // on WM_ACTIVATE, then set it as the current focus immediately.
    ITfDocumentMgr *previous = NULL;
    hr = ITfThreadMgr_AssociateFocus(tm, hwnd, empty, &previous);
    if (FAILED(hr)) {
        LOGW("Could not associate empty TSF document: 0x%08lx",
             (unsigned long) hr);
    }
    if (previous) {
        ITfDocumentMgr_Release(previous);
    }

    hr = ITfThreadMgr_SetFocus(tm, empty);
    if (FAILED(hr)) {
        LOGW("Could not focus empty TSF document: 0x%08lx",
             (unsigned long) hr);
        ITfDocumentMgr *prev = NULL;
        ITfThreadMgr_AssociateFocus(tm, hwnd, NULL, &prev);
        if (prev) {
            ITfDocumentMgr_Release(prev);
        }
        ITfDocumentMgr_Release(empty);
        ITfThreadMgr_Deactivate(tm);
        ITfThreadMgr_Release(tm);
        screen->tsf_ime_unavailable = true;
        return false;
    }

    screen->tsf_thread_mgr = tm;
    screen->tsf_empty_doc = empty;
    screen->tsf_ime_blocked = true;
    LOGI("TSF empty document focused: IME bypassed in-process, global "
         "keyboard layout untouched");
    return true;
}

static void
sc_screen_tsf_destroy(struct sc_screen *screen, HWND hwnd) {
    if (screen->tsf_thread_mgr && hwnd) {
        ITfThreadMgr *tm = (ITfThreadMgr *) screen->tsf_thread_mgr;
        ITfDocumentMgr *previous = NULL;
        // Disassociate before releasing: TSF does not AddRef the attached
        // document manager, so it must not outlive our reference.
        ITfThreadMgr_AssociateFocus(tm, hwnd, NULL, &previous);
        if (previous) {
            ITfDocumentMgr_Release(previous);
        }
        // Chromium does the same despite the docs saying pdimFocus cannot
        // be NULL; it works on Windows 8+ and releases TSF focus.
        ITfThreadMgr_SetFocus(tm, NULL);
    }
    if (screen->tsf_empty_doc) {
        ITfDocumentMgr_Release((ITfDocumentMgr *) screen->tsf_empty_doc);
        screen->tsf_empty_doc = NULL;
    }
    if (screen->tsf_thread_mgr) {
        ITfThreadMgr *tm = (ITfThreadMgr *) screen->tsf_thread_mgr;
        ITfThreadMgr_Deactivate(tm);
        ITfThreadMgr_Release(tm);
        screen->tsf_thread_mgr = NULL;
    }
    screen->tsf_ime_blocked = false;
}

// MinGW's imm.h omits the WM_IME_CONTROL subcommands; values are the
// documented IMC_* codes used by the default IME window.
#define IMC_GETCONVERSIONMODE 0x0001
#define IMC_SETCONVERSIONMODE 0x0002

// TSF-only edit controls (Word document, UWP) have no HIMC. Classic
// Win32 threads usually still expose a hidden default IME window owned
// by ctfmon; WM_IME_CONTROL to that window is the documented
// cross-process way to query/set the conversion mode without hooks.
static HWND
sc_screen_find_default_ime_wnd(HWND hwnd) {
    while (hwnd) {
        HWND ime = ImmGetDefaultIMEWnd(hwnd);
        if (ime) {
            return ime;
        }
        hwnd = GetAncestor(hwnd, GA_PARENT);
    }
    return NULL;
}

static bool
sc_screen_ime_is_native(HWND target) {
    HWND ime = sc_screen_find_default_ime_wnd(target);
    if (!ime) {
        return false;
    }
    DWORD_PTR result = 0;
    LRESULT ok = SendMessageTimeoutW(ime, WM_IME_CONTROL,
                                     IMC_GETCONVERSIONMODE, 0,
                                     SMTO_ABORTIFHUNG, 500, &result);
    return ok && (result & IME_CMODE_NATIVE) != 0;
}

static bool
sc_screen_ime_set_native(HWND target) {
    HWND ime = sc_screen_find_default_ime_wnd(target);
    if (!ime) {
        return false;
    }
    DWORD_PTR result = 0;
    LRESULT ok = SendMessageTimeoutW(ime, WM_IME_CONTROL,
                                     IMC_SETCONVERSIONMODE,
                                     IME_CMODE_NATIVE,
                                     SMTO_ABORTIFHUNG, 500, &result);
    if (!ok) {
        LOGW("Set IME conversion mode failed: %lu",
             (unsigned long) GetLastError());
    }
    return ok != 0;
}

// GetForegroundWindow() is wrong for UWP: the top-level
// ApplicationFrameWindow lives in ApplicationFrameHost.exe, whose frame
// thread keeps a stale HKL and does not receive layout changes.
// GetGUIThreadInfo(0) returns the real foreground input thread (CoreWindow)
// and its focused window.
static DWORD
sc_screen_get_input_thread(HWND *hwnd_out) {
    GUITHREADINFO gti = { .cbSize = sizeof(gti) };
    if (GetGUIThreadInfo(0, &gti)) {
        HWND hwnd = gti.hwndFocus ? gti.hwndFocus
                  : gti.hwndActive ? gti.hwndActive
                  : NULL;
        if (hwnd) {
            if (hwnd_out) {
                *hwnd_out = hwnd;
            }
            return GetWindowThreadProcessId(hwnd, NULL);
        }
    }

    // Fallback: classic top-level foreground window.
    HWND fg = GetForegroundWindow();
    if (hwnd_out) {
        *hwnd_out = fg;
    }
    return fg ? GetWindowThreadProcessId(fg, NULL) : 0;
}

// Read the "Use a different input method for each app window" setting.
// The registry value is undocumented: when it is missing or unreadable,
// assume disabled (the Windows default), which keeps the historical
// behavior. This function never writes to the registry.
static bool
sc_screen_per_app_ime_enabled(void) {
    DWORD enabled = 0;
    DWORD size = sizeof(enabled);
    LSTATUS status = RegGetValueW(HKEY_CURRENT_USER,
                                  L"Software\\Microsoft\\Input\\Settings",
                                  L"EnablePerAppLanguageProfile",
                                  RRF_RT_REG_DWORD, NULL, &enabled, &size);
    if (status == ERROR_SUCCESS) {
        LOGI("Per-app IME registry: EnablePerAppLanguageProfile=%lu",
             enabled);
        return enabled != 0;
    }
    LOGI("Per-app IME registry unavailable (status=0x%lx), runtime probe",
         (unsigned long) status);
    // Value missing (common on Windows 11 where the UI default is "on" but
    // the value is only written once the user touches the setting): fall
    // back to a runtime observation. With per-app IME off (shared layout),
    // scrcpy's forced English leaks to the foreground thread; with per-app
    // on, the new foreground thread keeps its own layout. If the foreground
    // thread is already on the original layout, the forced layout never
    // leaked -- treat it as per-app enabled and skip the global restore.
    HWND fg = NULL;
    DWORD fg_tid = sc_screen_get_input_thread(&fg);
    if (!fg || !fg_tid || fg_tid == GetCurrentThreadId()) {
        return false;
    }
    HKL fg_hkl = GetKeyboardLayout(fg_tid);
    if (!fg_hkl) {
        return false;
    }
    unsigned long lang = (unsigned long) (uintptr_t) fg_hkl & 0xFFFF;
    if (lang != 0x0409) {
        LOGI("Per-app IME runtime probe: foreground thread layout not "
             "English (0x%04lx), treat as per-app enabled",
             lang);
        return true;
    }
    LOGI("Per-app IME runtime probe: foreground thread layout English "
         "(0x%04lx), treat as shared layout",
         lang);
    return false;
}

#define SC_KEYBOARD_LAYOUT_RESTORE_DELAY_MS 120
#define SC_KEYBOARD_LAYOUT_RESTORE_SECOND_DELAY_MS 400
#define SC_KEYBOARD_LAYOUT_RESTORE_THIRD_DELAY_MS 1000
#define SC_KEYBOARD_LAYOUT_RESTORE_FOURTH_DELAY_MS 2000
#define SC_KEYBOARD_LAYOUT_RESTORE_FIFTH_DELAY_MS 5000

// Restore the system-wide keyboard layout after scrcpy lost focus.
// Windows keyboard layouts are per-thread, but with the "Use a different
// input method for each app window" setting disabled (the default), the
// layout is system-wide: while scrcpy has focus, ActivateKeyboardLayout()
// (in sc_screen_apply_keyboard_layout) switches the shared layout to
// English and the system forwards the change to the other threads. On
// focus loss the caller has already restored the scrcpy thread layout;
// this function restores the other threads:
//  - broadcast WM_INPUTLANGCHANGEREQUEST to all top-level windows (the
//    same mechanism the language bar uses);
//  - synchronously request the layout on the new foreground thread,
//    because TSF-integrated apps (Office/Word, some edit controls) may
//    ignore or lag behind the asynchronous broadcast.
// With the per-app IME setting enabled, each window keeps its own layout,
// and the broadcast above would wrongly change them all: skip it.
//
// Return true when a delayed retry could still help (the per-app setting
// is disabled and there is a foreground thread to target).
static bool
sc_screen_restore_global_keyboard_layout_apply(struct sc_screen *screen);

static bool
sc_screen_restore_global_keyboard_layout_once(struct sc_screen *screen) {
    HKL original = (HKL) screen->original_hkl;
    if (!original) {
        return false;
    }

    unsigned long orig_lang = (unsigned long) (uintptr_t) original & 0xFFFF;
    if (orig_lang == 0x0409) {
        // original is the very layout we force (00000409): with the shared
        // layout there is nothing that leaked, and with per-app enabled we
        // must not broadcast English onto windows that kept another layout.
        LOGI("Original layout already English (0x%04lx), skip global restore",
             orig_lang);
        return false;
    }

    if (sc_screen_per_app_ime_enabled()) {
        // The forced English layout never leaked to the other windows
        // (each window keeps its own layout): nothing to restore globally.
        LOGI("Per-app IME setting enabled, skip global keyboard layout "
             "restore");
        return false;
    }

    return sc_screen_restore_global_keyboard_layout_apply(screen);
}

// Execution part of the restore: the per-app decision has already been made
// (disabled). The retry timer calls this directly so the runtime probe is
// not re-sampled on every retry (a successful first restore leaves the
// foreground thread on the original layout, which the probe would then
// misread as per-app enabled).
static bool
sc_screen_restore_global_keyboard_layout_apply(struct sc_screen *screen) {
    HKL original = (HKL) screen->original_hkl;
    if (!original) {
        return false;
    }

    if (!PostMessage(HWND_BROADCAST, WM_INPUTLANGCHANGEREQUEST,
                     INPUTLANGCHANGE_FORWARD, (LPARAM) original)) {
        LOGW("Keyboard layout restore broadcast failed: %lu",
             (unsigned long) GetLastError());
    } else {
        LOGI("Keyboard layout restore broadcast posted (0x%08lx)",
             (unsigned long) (uintptr_t) original);
    }

    // The broadcast is asynchronous and some TSF-integrated windows may
    // ignore or lag behind it. Reinforce synchronously on the real input
    // thread (GetGUIThreadInfo) -- not the top-level window, which for UWP
    // is the ApplicationFrameHost frame thread with a stale HKL:
    // WM_INPUTLANGCHANGEREQUEST is processed by the target thread itself
    // (DefWindowProc activates the layout there), so SendMessageTimeout()
    // waits until the target has handled it, without any polling.
    HWND fg = NULL;
    DWORD fg_tid = sc_screen_get_input_thread(&fg);
    if (!fg || !fg_tid || fg_tid == GetCurrentThreadId()) {
        return false;
    }

    // Diagnostic: detect hosted/UWP windows (top-level and input threads
    // differ), so logs can confirm which target was used.
    HWND top = GetForegroundWindow();
    DWORD top_tid = top ? GetWindowThreadProcessId(top, NULL) : 0;
    if (top_tid && top_tid != fg_tid) {
        LOGI("Foreground top-level thread %lu != input thread %lu "
             "(hosted/UWP window), targeting input thread",
             (unsigned long) top_tid, (unsigned long) fg_tid);
    }

    DWORD_PTR result = 0;
    LRESULT ok = SendMessageTimeoutW(fg, WM_INPUTLANGCHANGEREQUEST,
                                     INPUTLANGCHANGE_FORWARD,
                                     (LPARAM) original,
                                     SMTO_ABORTIFHUNG, 500, &result);
    if (!ok) {
        // Best effort: the asynchronous broadcast above is still in flight.
        LOGW("Targeted keyboard layout restore to foreground window failed "
             "(broadcast remains): %lu",
             (unsigned long) GetLastError());
        return true;
    }

    LOGI("Foreground thread keyboard layout restore request processed");

    // The layout switch back to Chinese can leave the TSF IME in English
    // mode (Chinese layout, IME closed). Reopen the IME on the new
    // foreground window only -- never touch any other window's IME state.
    // Opening the IME is only safe under the original Chinese layout
    // (ime_restore_allowed) and only once the foreground thread has
    // actually left the English layout (otherwise the TSF IME would
    // reactivate and switch the layout away).
    if (!screen->ime_restore_allowed) {
        return true;
    }
    // Verify on the real input thread (not the top-level frame thread),
    // re-querying because the user may have switched again.
    fg = NULL;
    fg_tid = sc_screen_get_input_thread(&fg);
    if (!fg || !fg_tid || fg_tid == GetCurrentThreadId()) {
        return true;
    }
    HKL after_hkl = GetKeyboardLayout(fg_tid);
    LOGI("After targeted restore: input thread %lu layout 0x%08lx",
         (unsigned long) fg_tid, (unsigned long) (uintptr_t) after_hkl);

    // The input thread may still be on English (UWP InputHost keeps its
    // own state, or Word re-asserted it). Attach our input state to the
    // real input thread and activate the layout there synchronously.
    if ((!after_hkl || after_hkl != original)
            && (!after_hkl
                || ((unsigned long) (uintptr_t) after_hkl & 0xFFFF)
                       == 0x0409)) {
        if (AttachThreadInput(GetCurrentThreadId(), fg_tid, TRUE)) {
            ActivateKeyboardLayout(original, 0);
            AttachThreadInput(GetCurrentThreadId(), fg_tid, FALSE);
            after_hkl = GetKeyboardLayout(fg_tid);
            LOGI("AttachThreadInput fallback: input thread layout now "
                 "0x%08lx", (unsigned long) (uintptr_t) after_hkl);
        } else {
            LOGW("AttachThreadInput to input thread %lu failed: %lu",
                 (unsigned long) fg_tid, (unsigned long) GetLastError());
        }
    }

    // Require the layout to have actually returned to the original (Chinese)
    // layout -- not merely left English. If the user has switched to a third
    // language, opening the IME would activate the Chinese TIP and hijack
    // the layout away.
    if (!after_hkl || after_hkl != original) {
        LOGW("Input thread layout not confirmed original "
             "(after=0x%08lx, original=0x%08lx), skip IME reopen",
             (unsigned long) (uintptr_t) after_hkl,
             (unsigned long) (uintptr_t) original);
        return true;
    }
    HIMC imc = ImmGetContext(fg);
    HWND imc_owner = fg;
    if (!imc) {
        HWND top = GetForegroundWindow();
        if (top && top != fg) {
            imc = ImmGetContext(top);
            imc_owner = top;
        }
    }
    if (!imc) {
        HWND def = ImmGetDefaultIMEWnd(fg);
        if (def && def != fg) {
            imc = ImmGetContext(def);
            imc_owner = def;
        }
    }

    if (imc) {
        // Legacy IMM32 window: restore open status exactly as before.
        if (screen->ime_open_saved_valid) {
            ImmSetOpenStatus(imc, screen->ime_open_saved);
        }
        ImmReleaseContext(imc_owner, imc);
        LOGI("Foreground window IME restored (saved open state)");
    } else {
        // TSF-only control (Word document, UWP, Chrome): no HIMC. For
        // classic Win32 windows the hidden default IME window still works;
        // force native conversion mode unconditionally -- the saved open
        // state above is scrcpy's OWN window state (always closed) and
        // says nothing about the user's 中/英 mode in the target app, so
        // it must not gate this restore. UWP has no such window and
        // WM_IME_CONTROL is ignored there.
        if (sc_screen_ime_set_native(fg)) {
            LOGI("Foreground IME conversion mode restored to native");
        } else {
            LOGW("No IMM32/IME window for foreground control, IME mode "
                 "not restored (TSF-only/UWP)");
        }
    }
    return true;
}

// SDL timer callback (runs on the SDL timer thread): ask the UI thread to
// retry the restore after a fixed delay. When the user clicks directly
// into a TSF-integrated edit control, the target application may finish
// its focus initialization after the immediate restore above and re-assert
// the English layout; the delayed retries re-apply the restore once the
// target has settled. No polling and no hotkey injection.
static Uint32 SDLCALL
sc_screen_keyboard_layout_restore_timer(void *userdata, SDL_TimerID timer_id,
                                        Uint32 interval) {
    (void) userdata;
    (void) timer_id;
    (void) interval;
    sc_push_event(SC_EVENT_KEYBOARD_LAYOUT_RESTORE);
    return 0; // one-shot
}

static void
sc_screen_restore_global_keyboard_layout(struct sc_screen *screen) {
#ifdef _WIN32
    // Diagnostic: capture the activation race direction on user machines,
    // for both the top-level window and the real input thread (they differ
    // for UWP/hosted windows).
    HWND top = GetForegroundWindow();
    DWORD top_tid = top ? GetWindowThreadProcessId(top, NULL) : 0;
    HWND input_hwnd = NULL;
    DWORD input_tid = sc_screen_get_input_thread(&input_hwnd);
    HKL input_hkl = input_tid ? GetKeyboardLayout(input_tid) : NULL;
    LOGI("Focus lost: top-level thread %lu, input thread %lu, layout "
         "0x%08lx",
         (unsigned long) top_tid, (unsigned long) input_tid,
         (unsigned long) (uintptr_t) input_hkl);
#endif
    bool retry = sc_screen_restore_global_keyboard_layout_once(screen);
    if (retry) {
        SDL_AddTimer(SC_KEYBOARD_LAYOUT_RESTORE_DELAY_MS,
                     sc_screen_keyboard_layout_restore_timer, NULL);
        SDL_AddTimer(SC_KEYBOARD_LAYOUT_RESTORE_SECOND_DELAY_MS,
                     sc_screen_keyboard_layout_restore_timer, NULL);
        SDL_AddTimer(SC_KEYBOARD_LAYOUT_RESTORE_THIRD_DELAY_MS,
                     sc_screen_keyboard_layout_restore_timer, NULL);
        SDL_AddTimer(SC_KEYBOARD_LAYOUT_RESTORE_FOURTH_DELAY_MS,
                     sc_screen_keyboard_layout_restore_timer, NULL);
        SDL_AddTimer(SC_KEYBOARD_LAYOUT_RESTORE_FIFTH_DELAY_MS,
                     sc_screen_keyboard_layout_restore_timer, NULL);
    }
}

// Retry guards:
//  - scrcpy regained focus (layout_forced) -> never restore;
//  - TSF block is active -> HKL was never changed, nothing to restore;
//  - the real input thread is already on original -> done, no churn;
//  - the real input thread is on a third language -> never override.
static void
sc_screen_keyboard_layout_retry(struct sc_screen *screen) {
    if (!screen->hid_keyboard || screen->layout_forced
            || screen->tsf_ime_blocked) {
        return;
    }

    HWND input_hwnd = NULL;
    DWORD input_tid = sc_screen_get_input_thread(&input_hwnd);
    if (!input_hwnd || !input_tid || input_tid == GetCurrentThreadId()) {
        return;
    }

    HKL current = GetKeyboardLayout(input_tid);
    HKL original = (HKL) screen->original_hkl;
    if (current && original && current == original) {
        // Layout already restored. The remaining failure mode is the TSF
        // conversion mode (Chinese layout but English mode): retry it via
        // the classic default-IME-window path (unconditionally -- the
        // saved open state reflects scrcpy's own window, not the target
        // app). UWP has no such window, so this is a no-op there.
        if (screen->ime_restore_allowed
                && !sc_screen_ime_is_native(input_hwnd)) {
            sc_screen_ime_set_native(input_hwnd);
        }
        return;
    }
    if (current && original && current != original) {
        unsigned long lang = (unsigned long) (uintptr_t) current & 0xFFFF;
        if (lang != 0x0409) {
            LOGI("Keyboard layout retry skipped: input thread is on "
                 "0x%04lx", lang);
            return;
        }
    }

    sc_screen_restore_global_keyboard_layout_apply(screen);
}

static void
sc_screen_apply_keyboard_layout(struct sc_screen *screen, bool english) {
    HWND hwnd = (HWND) SDL_GetPointerProperty(
            SDL_GetWindowProperties(screen->window),
            SDL_PROP_WINDOW_WIN32_HWND_POINTER, NULL);
    if (!hwnd) {
        return;
    }
    if (english) {
        if (sc_screen_tsf_block(screen, hwnd)) {
            // The IME is bypassed in-process and HKL was not changed:
            // nothing leaks to other windows and no restore is needed.
            return;
        }
        if (screen->layout_forced) {
            // Already forced: keep the current state, do not re-activate.
            return;
        }
        if (!screen->original_hkl) {
            // Prefer the layout captured before SDL initialization: after
            // SDL_Init(VIDEO), GetKeyboardLayout(0) returns the Preload
            // default of the scrcpy thread, which may differ from the
            // user's actual system layout (problem B: restore switched to
            // the wrong layout).
            screen->original_hkl = screen->startup_hkl
                                       ? screen->startup_hkl
                                       : (void *) GetKeyboardLayout(0);
        }
        // Activate the English (US) keyboard layout. TSF input methods
        // (Sogou, Microsoft Pinyin...) are bound to their own keyboard
        // layout, so activating 00000409 disables them for this thread.
        HKL en = LoadKeyboardLayoutW(L"00000409", KLF_ACTIVATE);
        if (en) {
            ActivateKeyboardLayout(en, 0);
            screen->layout_forced = true;
        }
        // Also close the IME open status via IMM32 (best effort: TSF IMEs
        // may ignore it, the layout switch above is the main mechanism).
        // Save the original open status first so that focus loss restores
        // it exactly: unconditionally reopening the IME under the ENG
        // layout makes TSF activate the Chinese IME, which switches the
        // layout away (problem B).
        //
        // Restoring the open status is only safe for Chinese layouts: on
        // any other layout (e.g. the system current language is English),
        // ImmSetOpenStatus(TRUE) activates the TSF Chinese IME which
        // switches the layout away (problem B residual).
        unsigned long ime_restore_lang = (unsigned long) (uintptr_t)
            screen->original_hkl & 0x3FF;
        screen->ime_restore_allowed =
            ime_restore_lang == 0x0004 || ime_restore_lang == 0x0008;
        HIMC imc = ImmGetContext(hwnd);
        if (imc) {
            if (screen->layout_forced || !screen->ime_open_saved_valid) {
                // layout_forced==true means this period actually forced
                // English: re-sample every period so the previous period's
                // stale state is never restored; the !valid branch keeps an
                // initial value even when LoadKeyboardLayout failed.
                screen->ime_open_saved = ImmGetOpenStatus(imc) != FALSE;
                screen->ime_open_saved_valid = true;
            }
            // NOTE: ImmSetOpenStatus(imc, FALSE) deliberately removed:
            // the 00000409 layout switch already deactivates TSF text
            // services, and this call writes open/close (not the 中/英
            // conversion mode), so it was redundant. The saved read above
            // is kept for the legacy IMM32 fallback.
            ImmReleaseContext(hwnd, imc);
        }
        LOGI("IME layout forced to English (00000409), original 0x%08lx%s, "
             "IME open saved=%d, restore_allowed=%d",
             (unsigned long) (uintptr_t) screen->original_hkl,
             screen->startup_hkl ? " (startup)" : " (thread)",
             screen->ime_open_saved_valid ? (screen->ime_open_saved ? 1 : 0)
                                          : -1,
             screen->ime_restore_allowed ? 1 : 0);
    } else if (screen->original_hkl) {
        // Restore the user's original keyboard layout. The IME open status
        // must be restored symmetrically: focus gained closed it via
        // ImmSetOpenStatus(FALSE), otherwise TSF IMEs (Sogou, Microsoft
        // Pinyin...) stay closed and the user cannot type Chinese after
        // switching away from the scrcpy window ("layout not restored").
        ActivateKeyboardLayout((HKL) screen->original_hkl, 0);
        screen->layout_forced = false;
        HIMC imc = ImmGetContext(hwnd);
        bool ime_open = false;
        if (imc) {
            if (screen->ime_open_saved_valid) {
                // Symmetric restore of the state recorded at focus gain
                // (unconditionally reopening the IME under the ENG layout
                // would trigger TSF to activate the Chinese IME and switch
                // the layout away).
                if (screen->ime_restore_allowed) {
                    ImmSetOpenStatus(imc, screen->ime_open_saved);
                } else {
                    LOGI("IME open restore skipped (non-Chinese layout "
                         "0x%08lx, reopening would switch the layout away)",
                         (unsigned long) (uintptr_t) screen->original_hkl);
                }
                ime_open = ImmGetOpenStatus(imc) != FALSE;
            } else {
                // No saved state (no IME context when focus was gained):
                // fall back to reopening the IME, best effort.
                ImmSetOpenStatus(imc, TRUE);
                ime_open = ImmGetOpenStatus(imc) != FALSE;
            }
            ImmReleaseContext(hwnd, imc);
        }
        LOGI("IME layout restored (0x%08lx), IME open=%d%s%s",
             (unsigned long) (uintptr_t) screen->original_hkl, ime_open ? 1 : 0,
             imc ? "" : " (no IME context: no IME installed on this system)",
             screen->ime_open_saved_valid ? "" : " (no saved IME state)");
        LOGI("IME: %s",
             ime_open ? "open (Chinese IME usable)" : "closed or unavailable");
        // Restore the system-wide layout (browser and other windows), not
        // only the scrcpy thread's layout.
        sc_screen_restore_global_keyboard_layout(screen);
    }
}
#endif

void
sc_screen_handle_event(struct sc_screen *screen, const SDL_Event *event) {
    if (event->type == SC_LAMP_ANIM_TICK_EVENT) {
        // Lamp animation tick: the one-shot timer fired. Repaint while a
        // fade is still running and re-arm it, otherwise it stops.
        screen->lamp_anim_timer = 0;
        if (sc_fps_overlay_is_lamp_hover_animating(&screen->fps_overlay)
                || sc_fps_overlay_is_lamp_state_animating(
                    &screen->fps_overlay)) {
            sc_screen_render(screen, false);
            sc_screen_arm_lamp_anim_timer(screen);
        }
        return;
    }

    switch (event->type) {
        case SC_EVENT_OPEN_WINDOW: {
            struct sc_size *size = event->user.data1;
            assert(size);

            screen->frame_size = *size;
            free(size);
            screen->content_size = get_oriented_size(screen->frame_size,
                                                     screen->orientation);
            sc_screen_show_initial_window(screen);

            if (sc_screen_is_relative_mode(screen)) {
                // Capture mouse on start
                sc_mouse_capture_set_active(&screen->mc, true);
            }

            sc_screen_render(screen, false);
            return;
        }
        case SC_EVENT_NEW_FRAME: {
            bool ok = sc_screen_update_frame(screen);
            if (!ok) {
                LOGE("Frame update failed\n");
            }
            return;
        }
        case SDL_EVENT_WINDOW_FOCUS_GAINED:
#ifdef _WIN32
            // With HID keyboards (UHID/AOA), the computer IME must not
            // interfere with the keys: force the English layout while the
            // window has focus (TSF IMEs like Sogou react to plain Shift
            // presses even when text input is not started).
            if (screen->hid_keyboard) {
                sc_screen_apply_keyboard_layout(screen, true);
            }
#endif
            return;
        case SDL_EVENT_WINDOW_FOCUS_LOST:
#ifdef _WIN32
            if (screen->hid_keyboard) {
                if (screen->tsf_ime_blocked) {
                    // HKL was never changed: leave every other window
                    // exactly as it was.
                    return;
                }
                sc_screen_apply_keyboard_layout(screen, false);
            }
#endif
            return;
        case SC_EVENT_KEYBOARD_LAYOUT_RESTORE:
#ifdef _WIN32
            // Delayed retry with guards (see
            // sc_screen_keyboard_layout_retry): only if scrcpy did not
            // regain focus and the input thread is still on English.
            sc_screen_keyboard_layout_retry(screen);
#endif
            return;
        case SDL_EVENT_WINDOW_EXPOSED:
            sc_screen_render(screen, true);
            return;
// If defined, then the actions are already performed by the event watcher
#ifndef CONTINUOUS_RESIZING_WORKAROUND
        case SDL_EVENT_WINDOW_RESIZED:
        case SDL_EVENT_WINDOW_PIXEL_SIZE_CHANGED:
            sc_screen_on_resize(screen, &event->window);
            return;
#endif
        case SDL_EVENT_WINDOW_RESTORED:
            if (screen->video && is_windowed(screen)) {
                apply_pending_resize(screen);
                sc_screen_render(screen, true);
            }
            return;
        case SDL_EVENT_WINDOW_ENTER_FULLSCREEN:
            LOGD("Switched to fullscreen mode");
            assert(screen->video);
            return;
        case SDL_EVENT_WINDOW_LEAVE_FULLSCREEN:
            LOGD("Switched to windowed mode");
            assert(screen->video);
            if (is_windowed(screen)) {
                apply_pending_resize(screen);
                sc_screen_render(screen, true);
            }
            return;
        case SC_EVENT_DEVICE_DISCONNECTED:
            assert(!screen->disconnected);
            screen->disconnected = true;
            if (!screen->window_shown) {
                // No window open
                return;
            }

            sc_input_manager_handle_event(&screen->im, event);

            sc_texture_reset(&screen->tex);
            sc_screen_render(screen, true);

            sc_tick deadline = sc_tick_now() + SC_TICK_FROM_SEC(2);
            static const struct sc_disconnect_callbacks cbs = {
                .on_icon_loaded = sc_disconnect_on_icon_loaded,
                .on_timeout = sc_disconnect_on_timeout,
            };
            bool ok =
                sc_disconnect_start(&screen->disconnect, deadline, &cbs, NULL);
            if (ok) {
                screen->disconnect_started = true;
            }

            return;
    }

    // Alt+hover over the status lamp drives the "clickable" highlight: the
    // fade itself is time-based inside the overlay draw, here we only track
    // the hover state and keep repainting while the fade is still animating
    // (so it completes even without new video frames).
    if (event->type == SDL_EVENT_MOUSE_MOTION) {
        int out_w;
        int out_h;
        if (SDL_GetRenderOutputSize(screen->renderer, &out_w, &out_h)) {
            bool inside =
                sc_fps_overlay_hit_lamp(&screen->fps_overlay, out_w,
                                        event->motion.x, event->motion.y);
            bool alt = SDL_GetModState() & SDL_KMOD_ALT;
            bool changed =
                sc_fps_overlay_update_lamp_hover(&screen->fps_overlay,
                                                 inside, alt);
            if (changed
                    || sc_fps_overlay_is_lamp_hover_animating(
                        &screen->fps_overlay)
                    || sc_fps_overlay_is_lamp_state_animating(
                        &screen->fps_overlay)) {
                sc_screen_render(screen, false);
            }
            sc_screen_arm_lamp_anim_if_animating(screen);
        }
    }
    // Pressing/releasing Alt while the cursor rests on the lamp, or the
    // cursor leaving the window, must refresh (or clear) the hover state
    // even without a mouse motion event.
    bool alt_up = event->type == SDL_EVENT_KEY_UP
                  && (event->key.key == SDLK_LALT
                      || event->key.key == SDLK_RALT);
    bool alt_down = event->type == SDL_EVENT_KEY_DOWN
                    && (event->key.key == SDLK_LALT
                        || event->key.key == SDLK_RALT);
    if (alt_up || alt_down || event->type == SDL_EVENT_WINDOW_MOUSE_LEAVE) {
        int out_w;
        int out_h;
        if (SDL_GetRenderOutputSize(screen->renderer, &out_w, &out_h)) {
            float mx;
            float my;
            SDL_GetMouseState(&mx, &my);
            bool inside = sc_fps_overlay_hit_lamp(&screen->fps_overlay, out_w,
                                                  mx, my);
            bool alt = alt_down;
            bool changed =
                sc_fps_overlay_update_lamp_hover(&screen->fps_overlay,
                                                 inside, alt);
            if (changed
                    || sc_fps_overlay_is_lamp_hover_animating(
                        &screen->fps_overlay)
                    || sc_fps_overlay_is_lamp_state_animating(
                        &screen->fps_overlay)) {
                sc_screen_render(screen, false);
            }
            sc_screen_arm_lamp_anim_if_animating(screen);
        }
    }

    // Alt+click on the fps overlay status lamp toggles always-on-top
    // (Ctrl+T equivalent); Alt+drag anywhere else repositions the overlay.
    // Both events are consumed, not injected into the device.
    if (event->type == SDL_EVENT_MOUSE_BUTTON_DOWN
            && event->button.button == SDL_BUTTON_LEFT
            && (SDL_GetModState() & SDL_KMOD_ALT)) {
        int out_w;
        int out_h;
        if (SDL_GetRenderOutputSize(screen->renderer, &out_w, &out_h)) {
            if (sc_fps_overlay_hit_lamp(&screen->fps_overlay, out_w,
                                        event->button.x, event->button.y)) {
                // After the click the white preview fades out to the new
                // lamp state and stays off until the cursor leaves the lamp
                // strip and comes back.
                sc_fps_overlay_lamp_clicked(&screen->fps_overlay);
                sc_screen_set_always_on_top(screen,
                                            !screen->always_on_top);
                // keep repainting while the preview fades out
                sc_screen_arm_lamp_anim_if_animating(screen);
                return;
            }
            int ox;
            int oy;
            sc_fps_overlay_get_pos(&screen->fps_overlay, out_w, &ox, &oy);
            screen->overlay_dragging = true;
            screen->overlay_drag_dx = (int) event->button.x - ox;
            screen->overlay_drag_dy = (int) event->button.y - oy;
            return;
        }
    }
    if (event->type == SDL_EVENT_MOUSE_MOTION && screen->overlay_dragging) {
        int out_w;
        int out_h;
        if (!SDL_GetRenderOutputSize(screen->renderer, &out_w, &out_h)) {
            return;
        }
        int nx = (int) event->motion.x - screen->overlay_drag_dx;
        int ny = (int) event->motion.y - screen->overlay_drag_dy;
        // Keep the whole widget outline (including the lamp strip on the
        // left) inside the window, so the lamp stays clickable even when
        // the overlay is dragged against an edge.
        sc_fps_overlay_clamp_pos(&screen->fps_overlay, out_w, out_h,
                                 &nx, &ny);
        sc_fps_overlay_set_pos(&screen->fps_overlay, nx, ny);
        sc_screen_render(screen, false);
        return;
    }
    if (event->type == SDL_EVENT_MOUSE_BUTTON_UP
            && event->button.button == SDL_BUTTON_LEFT
            && screen->overlay_dragging) {
        screen->overlay_dragging = false;
        return;
    }

    if (sc_screen_is_relative_mode(screen)
            && sc_mouse_capture_handle_event(&screen->mc, event)) {
        // The mouse capture handler consumed the event
        return;
    }

    sc_input_manager_handle_event(&screen->im, event);
}

void
sc_screen_handle_disconnection(struct sc_screen *screen) {
    if (!screen->window_shown) {
        // No window open, quit immediately
        return;
    }

    if (!screen->disconnect_started) {
        // If sc_disconnect_start() failed, quit immediately
        return;
    }

    SDL_Event event;
    while (SDL_WaitEvent(&event)) {
        switch (event.type) {
            case SDL_EVENT_WINDOW_EXPOSED:
                sc_screen_render(screen, true);
                break;
            case SDL_EVENT_WINDOW_FOCUS_LOST:
#ifdef _WIN32
                // Device is already disconnected, no need to force English
                // again; if it was forced, restore the global layout so the
                // disconnection screen does not leave the system stuck on
                // the English layout until the window is closed. With the
                // TSF block active, HKL was never changed and there is
                // nothing to restore.
                if (screen->hid_keyboard && !screen->tsf_ime_blocked) {
                    sc_screen_apply_keyboard_layout(screen, false);
                }
#endif
                break;
            case SC_EVENT_DISCONNECTED_ICON_LOADED: {
                SDL_Surface *icon_disconnected = event.user.data1;
                assert(icon_disconnected);

                bool ok = sc_texture_set_from_surface(&screen->tex,
                                                      icon_disconnected);
                if (ok) {
                    screen->content_size.width = icon_disconnected->w;
                    screen->content_size.height = icon_disconnected->h;
                    sc_screen_render(screen, true);
                } else {
                    // not fatal
                    LOGE("Could not set disconnected icon");
                }

                sc_icon_destroy(icon_disconnected);
                break;
            }
            case SC_EVENT_DISCONNECTED_TIMEOUT:
                LOGD("Closing after device disconnection");
                return;
            case SC_EVENT_KEYBOARD_LAYOUT_RESTORE:
#ifdef _WIN32
                // Delayed retry arriving while the disconnection screen is
                // shown (guards in sc_screen_keyboard_layout_retry).
                sc_screen_keyboard_layout_retry(screen);
#endif
                break;
            case SDL_EVENT_QUIT:
                LOGD("User requested to quit");
                fprintf(stdout, "SCRCPY_EZ_USER_CLOSE\n");
                fflush(stdout);
                sc_screen_interrupt_disconnect(screen);
                return;
            default:
                sc_input_manager_handle_event(&screen->im, &event);
        }
    }
}

struct sc_point
sc_screen_convert_window_to_frame_coords(struct sc_screen *screen,
                                         int32_t x, int32_t y) {
    assert(screen->video);

    enum sc_orientation orientation = screen->orientation;

    int32_t w = screen->content_size.width;
    int32_t h = screen->content_size.height;

    // screen->rect must be initialized to avoid a division by zero
    assert(screen->rect.w && screen->rect.h);

    x = (int64_t) (x - screen->rect.x) * w / screen->rect.w;
    y = (int64_t) (y - screen->rect.y) * h / screen->rect.h;

    struct sc_point result;
    switch (orientation) {
        case SC_ORIENTATION_0:
            result.x = x;
            result.y = y;
            break;
        case SC_ORIENTATION_90:
            result.x = y;
            result.y = w - x;
            break;
        case SC_ORIENTATION_180:
            result.x = w - x;
            result.y = h - y;
            break;
        case SC_ORIENTATION_270:
            result.x = h - y;
            result.y = x;
            break;
        case SC_ORIENTATION_FLIP_0:
            result.x = w - x;
            result.y = y;
            break;
        case SC_ORIENTATION_FLIP_90:
            result.x = h - y;
            result.y = w - x;
            break;
        case SC_ORIENTATION_FLIP_180:
            result.x = x;
            result.y = h - y;
            break;
        default:
            assert(orientation == SC_ORIENTATION_FLIP_270);
            result.x = y;
            result.y = x;
            break;
    }

    return result;
}
