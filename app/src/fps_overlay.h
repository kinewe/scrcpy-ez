#ifndef SC_FPS_OVERLAY_H
#define SC_FPS_OVERLAY_H

#include <SDL3/SDL.h>
#include <stdbool.h>
#include <stdint.h>
#include "util/fps_sample.h"

enum sc_overlay_mode {
    SC_OVERLAY_MODE_USB,
    SC_OVERLAY_MODE_WIFI,
};

struct sc_fps_overlay {
    SDL_Renderer *renderer;
    SDL_Texture *texture;      // current text texture (e.g. "60 FPS 15M USB")
    int tex_w;                 // texture logical width
    int tex_h;                 // texture logical height
    struct sc_fps_sample sample; // successful new-frame texture submissions
    int fps;                   // currently displayed fps
    int bitrate;               // current ABR bitrate (server-reported)
    int abr_fps;               // ABR target fps level (server-reported)
    bool abr_dirty;            // bitrate or mode changed, re-render needed
    bool visible;              // show/hide toggle (Ctrl+F), session-only memory
    bool always_on_top;        // always-on-top state lamp (Ctrl+T), session-only memory
    bool lamp_hovered;         // cursor Alt-hovers the lamp (clickable hint)
    bool lamp_hover_suppressed; // click consumed: preview off until cursor leaves
    uint64_t lamp_hover_change_tick; // hover state last changed (sc_tick)
    uint64_t lamp_state_change_tick; // state toggle last changed (sc_tick)
    enum sc_overlay_mode mode; // connection mode: USB or WIFI
    int pos_x;                 // overlay top-left x (logical coords), -1 = default top-right
    int pos_y;                 // overlay top-left y (logical coords), -1 = default top-right
};

bool sc_fps_overlay_init(struct sc_fps_overlay *overlay, SDL_Renderer *renderer);
void sc_fps_overlay_destroy(struct sc_fps_overlay *overlay);
// call once after each successful new-frame texture update (not redraws)
void sc_fps_overlay_on_frame(struct sc_fps_overlay *overlay);
// UI-thread periodic refresh also expires stale rates without new frames.
bool sc_fps_overlay_refresh(struct sc_fps_overlay *overlay);
void sc_fps_overlay_reset(struct sc_fps_overlay *overlay);
// call before present (at the end of sc_screen_render)
void sc_fps_overlay_draw(struct sc_fps_overlay *overlay, SDL_Renderer *renderer);
// set the connection mode shown in the overlay (USB / WIFI); marks dirty
void sc_fps_overlay_set_mode(struct sc_fps_overlay *overlay,
                             enum sc_overlay_mode mode);
// update the ABR bitrate and target fps from a server ABR state message;
// marks dirty
void sc_fps_overlay_update_abr(struct sc_fps_overlay *overlay, int bitrate,
                               int fps);
// get the current overlay position (resolves -1 to the default top-right)
void sc_fps_overlay_get_pos(struct sc_fps_overlay *overlay, int out_w,
                            int *x, int *y);
// set the overlay position (Alt+drag to reposition)
void sc_fps_overlay_set_pos(struct sc_fps_overlay *overlay, int x, int y);
// clamp a proposed overlay position (text top-left corner, the coordinate
// used by set_pos) so that the whole widget outline — including the lamp
// strip on the left — stays inside the window
void sc_fps_overlay_clamp_pos(struct sc_fps_overlay *overlay, int out_w,
                              int out_h, int *x, int *y);
// get the current visibility (toggled by Ctrl+F)
bool sc_fps_overlay_is_visible(const struct sc_fps_overlay *overlay);
// set the visibility (toggled by Ctrl+F); a hidden overlay is not rendered
void sc_fps_overlay_set_visible(struct sc_fps_overlay *overlay, bool visible);
// set the always-on-top state (lit lamp when set); drawn on the next frame
void sc_fps_overlay_set_always_on_top(struct sc_fps_overlay *overlay,
                                      bool on_top);
// Alt+click hit test for the status lamp: true when (x, y) falls inside the
// lamp strip on the left of the overlay (render output coordinates, matching
// the mouse event position); always false when the overlay is hidden or has
// no text texture yet
bool sc_fps_overlay_hit_lamp(struct sc_fps_overlay *overlay, int out_w,
                             float x, float y);
// update the lamp hover preview: "inside" = cursor over the lamp strip,
// "alt" = Alt held. The white preview is active only while both are true and
// no click suppression is pending; the suppression is cleared as soon as the
// cursor leaves the strip. Returns true when the hover state changed.
bool sc_fps_overlay_update_lamp_hover(struct sc_fps_overlay *overlay,
                                      bool inside, bool alt);
// call right after an Alt+click on the lamp toggled the window state: the
// white preview fades out to the new lamp color and stays off until the
// cursor leaves the lamp strip
void sc_fps_overlay_lamp_clicked(struct sc_fps_overlay *overlay);
// true while the hover fade is still in progress (used to keep repainting
// until the fade settles even without new video frames)
bool sc_fps_overlay_is_lamp_hover_animating(
    const struct sc_fps_overlay *overlay);
// arm the cross-fade between the two terminal lamp states (Ctrl+T): the
// lit and unlit appearances are alpha-blended (no intermediate geometry);
// rapid consecutive toggles continue the ramp from the currently visible
// blend instead of restarting from a full terminal state
void sc_fps_overlay_start_state_transition(struct sc_fps_overlay *overlay);
// true while the state cross-fade is still in progress (used to keep
// repainting until the fade settles even without new video frames)
bool sc_fps_overlay_is_lamp_state_animating(
    const struct sc_fps_overlay *overlay);

#endif
