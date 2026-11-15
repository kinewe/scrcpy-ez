#include "fps_overlay.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "util/log.h"
#include "util/tick.h"
#ifdef _WIN32
#include "native_source.h"
#endif

// 5x7 dot-matrix font: one uint8_t per row, 5 bits per row, least
// significant bit on the right. Index: 0-9 are the digits, then
// 10='f', 11='p', 12='s' (also 'S'), 13='M', 14='k', 15='U', 16='B',
// 17='W', 18='I', 19='F', 20=' ' (space), 21='/' (rendered as '·'),
// 22='L', 23='A', 24='N'.
static const uint8_t FONT[25][7] = {
    {0x0E, 0x11, 0x13, 0x15, 0x19, 0x11, 0x0E}, // 0
    {0x04, 0x0C, 0x04, 0x04, 0x04, 0x04, 0x0E}, // 1
    {0x0E, 0x11, 0x01, 0x02, 0x04, 0x08, 0x1F}, // 2
    {0x1F, 0x02, 0x04, 0x02, 0x01, 0x11, 0x0E}, // 3
    {0x02, 0x06, 0x0A, 0x12, 0x1F, 0x02, 0x02}, // 4
    {0x1F, 0x10, 0x1E, 0x01, 0x01, 0x11, 0x0E}, // 5
    {0x06, 0x08, 0x10, 0x1E, 0x11, 0x11, 0x0E}, // 6
    {0x1F, 0x01, 0x02, 0x04, 0x08, 0x08, 0x08}, // 7
    {0x0E, 0x11, 0x11, 0x0E, 0x11, 0x11, 0x0E}, // 8
    {0x0E, 0x11, 0x11, 0x0F, 0x01, 0x02, 0x0C}, // 9
    {0x06, 0x09, 0x08, 0x1E, 0x08, 0x08, 0x08}, // f
    {0x1E, 0x11, 0x11, 0x1E, 0x10, 0x10, 0x10}, // P
    {0x0F, 0x10, 0x10, 0x0E, 0x01, 0x01, 0x1E}, // S
    {0x11, 0x1B, 0x15, 0x15, 0x11, 0x11, 0x11}, // M
    {0x08, 0x08, 0x08, 0x08, 0x08, 0x0A, 0x04}, // k
    {0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0E}, // U
    {0x1E, 0x11, 0x11, 0x1E, 0x11, 0x11, 0x1E}, // B
    {0x11, 0x11, 0x11, 0x15, 0x15, 0x1B, 0x11}, // W
    {0x1F, 0x04, 0x04, 0x04, 0x04, 0x04, 0x1F}, // I
    {0x1F, 0x10, 0x10, 0x1E, 0x10, 0x10, 0x10}, // F
    {0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, // ' '
    {0x00, 0x00, 0x0E, 0x0E, 0x0E, 0x00, 0x00}, // '·' middle dot (nicer divider)
    {0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x1F}, // L
    {0x0E, 0x11, 0x11, 0x1F, 0x11, 0x11, 0x11}, // A
    {0x11, 0x19, 0x15, 0x13, 0x11, 0x11, 0x11}, // N
};

#define FONT_SCALE 3
#define FONT_CHAR_W 5
#define FONT_CHAR_H 7
#define FONT_SPACING 2
#define OVERLAY_MARGIN_RIGHT 180
#define OVERLAY_MARGIN_TOP 10
#define OVERLAY_PADDING 6
// extra width reserved on the left side of the background for the
// always-on-top status lamp, so the text (whose width changes with the fps /
// bitrate digits) never overlaps it
#define OVERLAY_LAMP_SPACE 32
// "clickable" hint: while Alt is held over the lamp, its colors fade to
// white within LAMP_HOVER_FADE_MS (smoothstep-eased so the ramp is visible)
#define LAMP_HOVER_R 255
#define LAMP_HOVER_G 255
#define LAMP_HOVER_B 255
#define LAMP_HOVER_FADE_MS 300
// cross-fade between the two terminal lamp states after a toggle (Ctrl+T)
#define LAMP_STATE_FADE_MS 300

// map a character to its index in FONT, or -1 if unsupported
static int
glyph_of(char c) {
    if (c >= '0' && c <= '9') {
        return c - '0';
    }
    switch (c) {
        case 'f':
            return 10;
        case 'p':
        case 'P':
            return 11;
        case 's':
        case 'S':
            return 12;
        case 'M':
            return 13;
        case 'k':
            return 14;
        case 'U':
            return 15;
        case 'B':
            return 16;
        case 'W':
            return 17;
        case 'I':
            return 18;
        case 'F':
            return 19;
        case ' ':
            return 20;
        case '/':
            return 21; // rendered as '·'
        case 'L':
            return 22;
        case 'A':
            return 23;
        case 'N':
            return 24;
        default:
            return -1;
    }
}

// render "text" (e.g. "60 FPS") into a texture, replacing the previous one
static bool
render_text(struct sc_fps_overlay *overlay, const char *text) {
    size_t len = strlen(text);

    size_t gaps = len ? len - 1 : 0;
    int w = (int) (len * FONT_CHAR_W * FONT_SCALE + gaps * FONT_SPACING);
    int h = FONT_CHAR_H * FONT_SCALE;

    SDL_Surface *surface = SDL_CreateSurface(w, h, SDL_PIXELFORMAT_RGBA8888);
    if (!surface) {
        LOGW("Could not create fps overlay surface: %s", SDL_GetError());
        return false;
    }

    // fully transparent background
    bool ok = SDL_FillSurfaceRect(surface, NULL, 0);
    if (!ok) {
        LOGW("Could not clear fps overlay surface: %s", SDL_GetError());
        SDL_DestroySurface(surface);
        return false;
    }

    for (size_t i = 0; i < len; ++i) {
        int glyph = glyph_of(text[i]);
        if (glyph < 0) {
            continue;
        }

        int x0 = (int) i * (FONT_CHAR_W * FONT_SCALE + FONT_SPACING);
        for (int row = 0; row < FONT_CHAR_H; ++row) {
            uint8_t bits = FONT[glyph][row];
            for (int col = 0; col < FONT_CHAR_W; ++col) {
                if (!(bits & (1u << (FONT_CHAR_W - 1 - col)))) {
                    continue;
                }
                // draw a scale x scale white block for this font pixel
                for (int dy = 0; dy < FONT_SCALE; ++dy) {
                    for (int dx = 0; dx < FONT_SCALE; ++dx) {
                        ok = SDL_WriteSurfacePixel(surface,
                                                   x0 + col * FONT_SCALE + dx,
                                                   row * FONT_SCALE + dy,
                                                   255, 255, 255, 255);
                        if (!ok) {
                            LOGW("Could not write fps overlay pixel: %s",
                                 SDL_GetError());
                            SDL_DestroySurface(surface);
                            return false;
                        }
                    }
                }
            }
        }
    }

    SDL_Texture *texture =
        SDL_CreateTextureFromSurface(overlay->renderer, surface);
    SDL_DestroySurface(surface);
    if (!texture) {
        LOGW("Could not create fps overlay texture: %s", SDL_GetError());
        return false;
    }

    if (overlay->texture) {
        SDL_DestroyTexture(overlay->texture);
    }
    overlay->texture = texture;
    overlay->tex_w = w;
    overlay->tex_h = h;
    return true;
}

// scrcpy-ez: initial visibility of the parameter overlay (fps/bitrate/status)
// is chosen by the GUI launcher through the SCEZ_PARAM_OVERLAY environment
// variable, inherited by this process from the launcher (the GUI sets it on
// the .bat process tree, which starts scrcpy.exe):
//   - "0"     → start hidden (GUI setting "show parameter overlay" = off)
//   - unset or any other value → start visible (historical default)
// Ctrl+F still toggles the overlay at runtime; this only sets the startup
// state. The variable is read once, during overlay initialization.
static bool
initial_visible_from_env(void) {
    const char *value = getenv("SCEZ_PARAM_OVERLAY");
    if (!value) {
        return true;
    }
    return strcmp(value, "0") != 0;
}

bool
sc_fps_overlay_init(struct sc_fps_overlay *overlay, SDL_Renderer *renderer) {
    overlay->renderer = renderer;
    overlay->texture = NULL;
    overlay->tex_w = 0;
    overlay->tex_h = 0;
    sc_fps_sample_reset(&overlay->sample);
    overlay->fps = 0;
    overlay->bitrate = 0;
    overlay->abr_fps = 0;
    overlay->abr_dirty = false;
    overlay->visible = initial_visible_from_env();
    overlay->always_on_top = false;
    overlay->lamp_hovered = false;
    overlay->lamp_hover_suppressed = false;
    overlay->lamp_hover_change_tick = sc_tick_now();
    overlay->lamp_state_change_tick = sc_tick_now();
    overlay->mode = SC_OVERLAY_MODE_USB;
    overlay->pos_x = -1;
    overlay->pos_y = -1;

    return render_text(overlay, "0 FPS");
}

void
sc_fps_overlay_destroy(struct sc_fps_overlay *overlay) {
    if (overlay->texture) {
        SDL_DestroyTexture(overlay->texture);
        overlay->texture = NULL;
    }
}

void
sc_fps_overlay_get_pos(struct sc_fps_overlay *overlay, int out_w, int *x,
                       int *y) {
    if (overlay->pos_x < 0) {
        *x = out_w - overlay->tex_w - OVERLAY_MARGIN_RIGHT;
        *y = OVERLAY_MARGIN_TOP;
    } else {
        *x = overlay->pos_x;
        *y = overlay->pos_y;
    }
}

void
sc_fps_overlay_set_pos(struct sc_fps_overlay *overlay, int x, int y) {
    overlay->pos_x = x;
    overlay->pos_y = y;
}

void
sc_fps_overlay_clamp_pos(struct sc_fps_overlay *overlay, int out_w, int out_h,
                         int *x, int *y) {
    // The widget outline extends OVERLAY_PADDING on the top/right/bottom of
    // the text and OVERLAY_PADDING + OVERLAY_LAMP_SPACE on its left, so the
    // lamp strip stays clickable when the overlay touches the left edge.
    int min_x = OVERLAY_PADDING + OVERLAY_LAMP_SPACE;
    int max_x = out_w - overlay->tex_w - OVERLAY_PADDING;
    int min_y = OVERLAY_PADDING;
    int max_y = out_h - overlay->tex_h - OVERLAY_PADDING;
    // A window smaller than the widget would make the clamp range invalid;
    // fall back to 0 so the text stays visible (the lamp may then stick out
    // of the window, but that cannot be avoided).
    if (max_x < min_x) {
        min_x = 0;
        max_x = 0;
    }
    if (max_y < min_y) {
        min_y = 0;
        max_y = 0;
    }

    if (*x < min_x) {
        *x = min_x;
    } else if (*x > max_x) {
        *x = max_x;
    }
    if (*y < min_y) {
        *y = min_y;
    } else if (*y > max_y) {
        *y = max_y;
    }
}

bool
sc_fps_overlay_is_visible(const struct sc_fps_overlay *overlay) {
    return overlay->visible;
}

void
sc_fps_overlay_set_visible(struct sc_fps_overlay *overlay, bool visible) {
    overlay->visible = visible;
}

void
sc_fps_overlay_set_always_on_top(struct sc_fps_overlay *overlay, bool on_top) {
    overlay->always_on_top = on_top;
}

bool
sc_fps_overlay_update_lamp_hover(struct sc_fps_overlay *overlay, bool inside,
                                 bool alt) {
    if (!inside) {
        // the cursor left the lamp strip: re-arm the preview for the next
        // time it enters
        overlay->lamp_hover_suppressed = false;
    }

    bool hovered = inside && alt && !overlay->lamp_hover_suppressed;
    if (overlay->lamp_hovered == hovered) {
        return false;
    }
    overlay->lamp_hovered = hovered;
    overlay->lamp_hover_change_tick = sc_tick_now();
    return true;
}

void
sc_fps_overlay_lamp_clicked(struct sc_fps_overlay *overlay) {
    // after a click the preview fades out to the new lamp state; it stays
    // off until the cursor leaves the strip and comes back
    overlay->lamp_hover_suppressed = true;
    if (overlay->lamp_hovered) {
        overlay->lamp_hovered = false;
        overlay->lamp_hover_change_tick = sc_tick_now();
    }
}

bool
sc_fps_overlay_is_lamp_hover_animating(
        const struct sc_fps_overlay *overlay) {
    sc_tick elapsed = sc_tick_now() - overlay->lamp_hover_change_tick;
    return elapsed < (sc_tick) SC_TICK_FROM_MS(LAMP_HOVER_FADE_MS);
}

void
sc_fps_overlay_start_state_transition(struct sc_fps_overlay *overlay) {
    // Continue the cross-fade from the currently visible blend instead of
    // restarting from a full terminal state: rapid consecutive Ctrl+T
    // presses reverse/continue the ramp smoothly (the new ramp starts at
    // st = 1 - st_old, the complement that keeps the composite identical).
    sc_tick now = sc_tick_now();
    sc_tick fade = (sc_tick) SC_TICK_FROM_MS(LAMP_STATE_FADE_MS);
    sc_tick elapsed = now - overlay->lamp_state_change_tick;
    float st = (float) elapsed / (float) fade;
    if (st > 1.0f) {
        st = 1.0f;
    } else if (st < 0.0f) {
        st = 0.0f;
    }
    overlay->lamp_state_change_tick = now - (sc_tick) ((1.0f - st) * fade);
}

bool
sc_fps_overlay_is_lamp_state_animating(
        const struct sc_fps_overlay *overlay) {
    sc_tick elapsed = sc_tick_now() - overlay->lamp_state_change_tick;
    return elapsed < (sc_tick) SC_TICK_FROM_MS(LAMP_STATE_FADE_MS);
}

bool
sc_fps_overlay_hit_lamp(struct sc_fps_overlay *overlay, int out_w, float x,
                        float y) {
    if (!overlay->visible || !overlay->texture) {
        return false;
    }

    int tex_x;
    int tex_y;
    sc_fps_overlay_get_pos(overlay, out_w, &tex_x, &tex_y);

    // the whole strip reserved on the left of the background is clickable,
    // so the lamp is easy to hit without aiming at the dot itself
    float x0 = tex_x - OVERLAY_PADDING - OVERLAY_LAMP_SPACE;
    float y0 = tex_y - OVERLAY_PADDING;
    float w = OVERLAY_LAMP_SPACE;
    float h = overlay->tex_h + 2 * OVERLAY_PADDING;
    return x >= x0 && x < x0 + w && y >= y0 && y < y0 + h;
}

void
sc_fps_overlay_set_mode(struct sc_fps_overlay *overlay,
                        enum sc_overlay_mode mode) {
    if (overlay->mode == mode) {
        return;
    }
    overlay->mode = mode;
    overlay->abr_dirty = true;
}

void
sc_fps_overlay_update_abr(struct sc_fps_overlay *overlay, int bitrate,
                          int fps) {
    if (overlay->bitrate == bitrate && overlay->abr_fps == fps) {
        return;
    }
    overlay->bitrate = bitrate;
    overlay->abr_fps = fps;
    overlay->abr_dirty = true;
}

void
sc_fps_overlay_on_frame(struct sc_fps_overlay *overlay) {
    sc_fps_sample_add(&overlay->sample, sc_tick_now());
    sc_fps_overlay_refresh(overlay);
}

void
sc_fps_overlay_reset(struct sc_fps_overlay *overlay) {
    sc_fps_sample_reset(&overlay->sample);
    overlay->abr_dirty = true;
    sc_fps_overlay_refresh(overlay);
}

bool
sc_fps_overlay_refresh(struct sc_fps_overlay *overlay) {
    if (!overlay->texture) {
        return false;
    }
    sc_fps_sample_update(&overlay->sample, sc_tick_now());
    int fps = overlay->sample.fps;
    int native_fps=-1;
#ifdef _WIN32
    native_fps=sc_native_source_fps();
#endif
    static int last_native_fps=-2;
    if (fps == overlay->fps && !overlay->abr_dirty && native_fps==last_native_fps) {
        return false;
    }
    overlay->fps = fps;
    last_native_fps=native_fps;
    overlay->abr_dirty = false;

    // "<fps>fps[/<abr_fps>] <bitrate><unit> <mode>"
    // e.g. "60/60 FPS 15M USB", "60 FPS 15M USB" (before ABR state)
    const char *mode = overlay->mode == SC_OVERLAY_MODE_WIFI ? "LAN" : "USB";
    char text[48];
    if(native_fps>=0){
        if(overlay->bitrate>=1000000)snprintf(text,sizeof(text),"UI %d FPS %dM %s",native_fps,overlay->bitrate/1000000,mode);
        else snprintf(text,sizeof(text),"UI %d FPS %dk %s",native_fps,overlay->bitrate/1000,mode);
        return render_text(overlay,text);
    }
    if (overlay->bitrate >= 1000000) {
        if (overlay->abr_fps > 0) {
            snprintf(text, sizeof(text), "%d/%d FPS %dM %s", fps,
                     overlay->abr_fps, overlay->bitrate / 1000000, mode);
        } else {
            // ABR state not received yet: show client submission rate only
            snprintf(text, sizeof(text), "%d FPS %dM %s", fps,
                     overlay->bitrate / 1000000, mode);
        }
    } else {
        if (overlay->abr_fps > 0) {
            snprintf(text, sizeof(text), "%d/%d FPS %dk %s", fps,
                     overlay->abr_fps, overlay->bitrate / 1000, mode);
        } else {
            // ABR state not received yet: show client submission rate only
            snprintf(text, sizeof(text), "%d FPS %dk %s", fps,
                     overlay->bitrate / 1000, mode);
        }
    }
    // on failure the previous texture is kept
    return render_text(overlay, text);
}

// fill a disc centered at (cx, cy) with the current draw color, one point at
// a time; coordinates are floats for subpixel placement. The largest radius
// used (13px) yields ~530 points, drawn once per frame: negligible even at
// high refresh rates.
static bool
fill_circle(SDL_Renderer *renderer, float cx, float cy, float radius) {
    int r = (int) radius;
    int r2 = r * r;
    for (int y = -r; y <= r; ++y) {
        for (int x = -r; x <= r; ++x) {
            if (x * x + y * y <= r2) {
                if (!SDL_RenderPoint(renderer, cx + x, cy + y)) {
                    return false;
                }
            }
        }
    }
    return true;
}

// fill a ring between inner_radius (excluded) and outer_radius (included)
// around (cx, cy) with the current draw color; used for the off-state lamp
// outline so the socket stays visible on dark backgrounds
static bool
fill_ring(SDL_Renderer *renderer, float cx, float cy, float inner_radius,
          float outer_radius) {
    int r = (int) outer_radius;
    int inner2 = (int) (inner_radius * inner_radius);
    int outer2 = r * r;
    for (int y = -r; y <= r; ++y) {
        for (int x = -r; x <= r; ++x) {
            int d2 = x * x + y * y;
            if (d2 <= outer2 && d2 > inner2) {
                if (!SDL_RenderPoint(renderer, cx + x, cy + y)) {
                    return false;
                }
            }
        }
    }
    return true;
}

// mix a lamp layer color toward white and its alpha toward "target_a" by
// "mix" (0 = normal, 1 = full hover); the lit lamp retracts its glow this
// way, so both states converge to the same small white preview
static void
lamp_hover_color(uint8_t r, uint8_t g, uint8_t b, uint8_t a, uint8_t target_a,
                 float mix, uint8_t *hr, uint8_t *hg, uint8_t *hb,
                 uint8_t *ha) {
    *hr = (uint8_t) (r + (LAMP_HOVER_R - r) * mix);
    *hg = (uint8_t) (g + (LAMP_HOVER_G - g) * mix);
    *hb = (uint8_t) (b + (LAMP_HOVER_B - b) * mix);
    *ha = (uint8_t) (a + (target_a - a) * mix);
}

void
sc_fps_overlay_draw(struct sc_fps_overlay *overlay, SDL_Renderer *renderer) {
    if (!overlay->texture) {
        return;
    }

    int out_w;
    int out_h;
    bool ok = SDL_GetRenderOutputSize(renderer, &out_w, &out_h);
    if (!ok) {
        LOGW("Could not get render output size: %s", SDL_GetError());
        return;
    }

    // top-right corner by default, or the user-set position (Alt+drag);
    // semi-transparent black background extended by 6px on each side, plus
    // the lamp space reserved on the left
    int tex_x;
    int tex_y;
    sc_fps_overlay_get_pos(overlay, out_w, &tex_x, &tex_y);

    SDL_FRect bg_rect = {
        .x = tex_x - OVERLAY_PADDING - OVERLAY_LAMP_SPACE,
        .y = tex_y - OVERLAY_PADDING,
        .w = overlay->tex_w + 2 * OVERLAY_PADDING + OVERLAY_LAMP_SPACE,
        .h = overlay->tex_h + 2 * OVERLAY_PADDING,
    };
    // the renderer default draw blend mode is NONE: enable blending for the
    // semi-transparent background
    ok = SDL_SetRenderDrawBlendMode(renderer, SDL_BLENDMODE_BLEND);
    if (ok) {
        ok = SDL_SetRenderDrawColor(renderer, 0, 0, 0, 140);
    }
    if (ok) {
        ok = SDL_RenderFillRect(renderer, &bg_rect);
    }
    if (!ok) {
        LOGW("Could not draw fps overlay background: %s", SDL_GetError());
        return;
    }

    // Always-on-top status lamp, drawn after the background and before the
    // text: an orange glowing dot when the window is pinned (Ctrl+T), a gray
    // dot otherwise. It is centered in the reserved strip on the left of the
    // text, so it never overlaps the digits when the text width changes.
    float lamp_cx = bg_rect.x + OVERLAY_LAMP_SPACE / 2.0f;
    float lamp_cy = bg_rect.y + bg_rect.h / 2.0f;

    // Hover fade toward the white "clickable" hint. The mix is derived from
    // the elapsed time since the hover state last changed, so even with
    // sparse redraws the color always reflects the true progress and never
    // jumps to the end state; smoothstep easing makes the ramp visible.
    sc_tick elapsed = sc_tick_now() - overlay->lamp_hover_change_tick;
    float t = (float) elapsed / (float) SC_TICK_FROM_MS(LAMP_HOVER_FADE_MS);
    if (t > 1.0f) {
        t = 1.0f;
    }
    float mix = overlay->lamp_hovered ? t : 1.0f - t;
    mix = mix * mix * (3.0f - 2.0f * mix);

    // Cross-fade between the two terminal lamp states after a toggle
    // (Ctrl+T): the lit and unlit appearances are alpha-blended into each
    // other, with no intermediate geometry. Time-based and smoothstep-eased
    // like the hover fade.
    sc_tick state_elapsed = sc_tick_now() - overlay->lamp_state_change_tick;
    float st = (float) state_elapsed
             / (float) SC_TICK_FROM_MS(LAMP_STATE_FADE_MS);
    if (st > 1.0f) {
        st = 1.0f;
    }
    st = st * st * (3.0f - 2.0f * st);
    float on_factor = overlay->always_on_top ? st : 1.0f - st;
    float off_factor = overlay->always_on_top ? 1.0f - st : st;

    uint8_t r;
    uint8_t g;
    uint8_t b;
    uint8_t a;

    if (off_factor > 0.0f) {
        // unlit appearance: small gray dot (gray-500) with a lighter
        // outline ring, so the lamp socket stays visible on dark
        // backgrounds. On hover both layers fade to solid white, keeping
        // the same small geometry.
        lamp_hover_color(156, 163, 175, 230, 255, mix, &r, &g, &b, &a);
        ok = SDL_SetRenderDrawColor(renderer, r, g, b,
                                    (uint8_t) (a * off_factor));
        if (ok) {
            ok = fill_ring(renderer, lamp_cx, lamp_cy, 5.0f, 7.0f);
        }
        lamp_hover_color(107, 114, 128, 160, 255, mix, &r, &g, &b, &a);
        if (ok) {
            ok = SDL_SetRenderDrawColor(renderer, r, g, b,
                                        (uint8_t) (a * off_factor));
        }
        if (ok) {
            ok = fill_circle(renderer, lamp_cx, lamp_cy, 5.0f);
        }
    }

    if (on_factor > 0.0f) {
        // lit appearance: orange glow. On hover it retracts (radius shrinks,
        // alpha fades out) while the inner layers whiten, converging to the
        // same small white disc as the unlit preview.
        lamp_hover_color(245, 158, 11, 55, 0, mix, &r, &g, &b, &a);
        ok = SDL_SetRenderDrawColor(renderer, r, g, b,
                                    (uint8_t) (a * on_factor));
        if (ok) {
            ok = fill_circle(renderer, lamp_cx, lamp_cy,
                             13.0f + (7.5f - 13.0f) * mix);
        }
        lamp_hover_color(245, 158, 11, 130, 255, mix, &r, &g, &b, &a);
        if (ok) {
            ok = SDL_SetRenderDrawColor(renderer, r, g, b,
                                        (uint8_t) (a * on_factor));
        }
        if (ok) {
            ok = fill_circle(renderer, lamp_cx, lamp_cy,
                             9.0f + (7.0f - 9.0f) * mix);
        }
        lamp_hover_color(251, 191, 36, 255, 255, mix, &r, &g, &b, &a);
        if (ok) {
            ok = SDL_SetRenderDrawColor(renderer, r, g, b,
                                        (uint8_t) (a * on_factor));
        }
        if (ok) {
            ok = fill_circle(renderer, lamp_cx, lamp_cy,
                             6.0f + (5.0f - 6.0f) * mix);
        }
    }
    if (!ok) {
        LOGW("Could not draw fps overlay status lamp: %s", SDL_GetError());
        return;
    }
    // The draw color is left as-is: the text below is drawn with
    // SDL_RenderTexture, which is not affected by it.

    SDL_FRect dst_rect = {
        .x = tex_x,
        .y = tex_y,
        .w = overlay->tex_w,
        .h = overlay->tex_h,
    };
    ok = SDL_RenderTexture(renderer, overlay->texture, NULL, &dst_rect);
    if (!ok) {
        LOGW("Could not draw fps overlay: %s", SDL_GetError());
    }
}
