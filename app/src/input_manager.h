#ifndef SC_INPUTMANAGER_H
#define SC_INPUTMANAGER_H

#include "common.h"

#include <stdbool.h>
#include <stdint.h>
#include <SDL3/SDL_events.h>
#include <SDL3/SDL_keycode.h>

#include "controller.h"
#include "file_pusher.h"
#include "options.h"
#include "trait/gamepad_processor.h"
#include "trait/key_processor.h"
#include "trait/mouse_processor.h"

struct sc_input_manager {
    struct sc_controller *controller;
    struct sc_file_pusher *fp;
    struct sc_screen *screen;

    struct sc_key_processor *kp;
    struct sc_mouse_processor *mp;
    struct sc_gamepad_processor *gp;

    bool camera;

    // True when the device screen has been turned off by a client shortcut.
    // Tracked so that every exit path can request to turn it back on.
    bool screen_off;
    // Consume the full Ctrl+P sequence even if Ctrl is released before P.
    bool ctrl_power_pressed;

    struct sc_mouse_bindings mouse_bindings;
    bool legacy_paste;
    bool clipboard_autosync;
    bool clipboard_sync;
    bool suppress_start_push;

    uint16_t sdl_shortcut_mods;

    bool vfinger_down;
    bool vfinger_invert_x;
    bool vfinger_invert_y;

    uint8_t mouse_buttons_state; // OR of enum sc_mouse_button values

    // Tracks the number of identical consecutive shortcut key down events.
    // Not to be confused with event->repeat, which counts the number of
    // system-generated repeated key presses.
    unsigned key_repeat;
    SDL_Keycode last_keycode;
    uint16_t last_mod;

    uint64_t next_sequence; // used for request acknowledgements

    bool disconnected;
};

struct sc_input_manager_params {
    struct sc_controller *controller;
    struct sc_file_pusher *fp;
    struct sc_screen *screen;
    struct sc_key_processor *kp;
    struct sc_mouse_processor *mp;
    struct sc_gamepad_processor *gp;
    bool camera;

    struct sc_mouse_bindings mouse_bindings;
    bool legacy_paste;
    bool clipboard_autosync;
    bool clipboard_sync;
    bool clipboard_push_on_start;
    uint8_t shortcut_mods; // OR of enum sc_shortcut_mod values
};

void
sc_input_manager_init(struct sc_input_manager *im,
                      const struct sc_input_manager_params *params);

void
sc_input_manager_handle_event(struct sc_input_manager *im,
                              const SDL_Event *event);

// Return true when the device screen is currently turned off by a shortcut
bool
sc_input_manager_is_screen_off(const struct sc_input_manager *im);

// Request to turn the device screen back on (no-op when it is already on)
void
sc_input_manager_turn_screen_on(struct sc_input_manager *im);

// Send image clipboard from computer to device
bool
sc_input_manager_set_device_image_clipboard(struct sc_input_manager *im, bool paste,
                                           uint64_t sequence);

// Mark that the computer clipboard was set by a reverse synchronization
// (device -> computer). The next SDL_EVENT_CLIPBOARD_UPDATE must be ignored
// by the input manager, to avoid an infinite synchronization loop
// (computer -> device -> computer -> ...).
// Must be called on the main thread, right after setting the computer
// clipboard with SDL_SetClipboardText()/SDL_SetClipboardData().
void
sc_input_manager_mark_clipboard_reverse_sync(void);

#endif
