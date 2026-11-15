#ifndef SC_CONTROLMSG_H
#define SC_CONTROLMSG_H

#include "common.h"

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "android/input.h"
#include "android/keycodes.h"
#include "coords.h"
#include "abr_monitor.h"
#include "hid/hid_event.h"

// 256M: protocol limit for lossless large image clipboard support. Buffers
// are no longer statically reserved at this size: the client serialization
// now allocates exactly the message size on demand (sc_control_msg_serialize).
#define SC_CONTROL_MSG_MAX_SIZE (1 << 28)

#define SC_CONTROL_MSG_INJECT_TEXT_MAX_LENGTH 300
// type + sequence + paste + length + the optional epoch/version (16 bytes)
#define SC_CONTROL_MSG_CLIPBOARD_TEXT_MAX_LENGTH (SC_CONTROL_MSG_MAX_SIZE - 30)

#define SC_CONTROL_MSG_SCAN_FILE_PATH_MAX_LENGTH 256

#define SC_POINTER_ID_MOUSE UINT64_C(-1)
#define SC_POINTER_ID_GENERIC_FINGER UINT64_C(-2)

// Used for injecting an additional virtual pointer for pinch-to-zoom
#define SC_POINTER_ID_VIRTUAL_FINGER UINT64_C(-3)

enum sc_control_msg_type {
    SC_CONTROL_MSG_TYPE_INJECT_KEYCODE,
    SC_CONTROL_MSG_TYPE_INJECT_TEXT,
    SC_CONTROL_MSG_TYPE_INJECT_TOUCH_EVENT,
    SC_CONTROL_MSG_TYPE_INJECT_SCROLL_EVENT,
    SC_CONTROL_MSG_TYPE_BACK_OR_SCREEN_ON,
    SC_CONTROL_MSG_TYPE_EXPAND_NOTIFICATION_PANEL,
    SC_CONTROL_MSG_TYPE_EXPAND_SETTINGS_PANEL,
    SC_CONTROL_MSG_TYPE_COLLAPSE_PANELS,
    SC_CONTROL_MSG_TYPE_GET_CLIPBOARD,
    SC_CONTROL_MSG_TYPE_SET_CLIPBOARD,
    SC_CONTROL_MSG_TYPE_SET_DISPLAY_POWER,
    SC_CONTROL_MSG_TYPE_ROTATE_DEVICE,
    SC_CONTROL_MSG_TYPE_UHID_CREATE,
    SC_CONTROL_MSG_TYPE_UHID_INPUT,
    SC_CONTROL_MSG_TYPE_UHID_DESTROY,
    SC_CONTROL_MSG_TYPE_OPEN_HARD_KEYBOARD_SETTINGS,
    SC_CONTROL_MSG_TYPE_START_APP,
    SC_CONTROL_MSG_TYPE_RESET_VIDEO,
    SC_CONTROL_MSG_TYPE_CAMERA_SET_TORCH,
    SC_CONTROL_MSG_TYPE_CAMERA_ZOOM_IN,
    SC_CONTROL_MSG_TYPE_CAMERA_ZOOM_OUT,
    SC_CONTROL_MSG_TYPE_RESIZE_DISPLAY,
    SC_CONTROL_MSG_TYPE_SCAN_FILE,
    SC_CONTROL_MSG_TYPE_SET_IMAGE_CLIPBOARD,
    // No argument: the server copies the most recent clipboard image file
    // (already stored in the device clipboard cache) to the gallery and
    // triggers a media scan, so it appears in the gallery app.
    SC_CONTROL_MSG_TYPE_SAVE_CLIPBOARD_IMAGE_TO_GALLERY,
    SC_CONTROL_MSG_TYPE_SET_CLIPBOARD_VERSIONED,
    SC_CONTROL_MSG_TYPE_SET_IMAGE_CLIPBOARD_VERSIONED,
    SC_CONTROL_MSG_TYPE_KEEP_AWAKE_HEARTBEAT,
    SC_CONTROL_MSG_TYPE_ABR_FEEDBACK,
    SC_CONTROL_MSG_TYPE_TOGGLE_DISPLAY_BACKLIGHT,
    SC_CONTROL_MSG_TYPE_AUTO_BLACKOUT,
};

enum sc_copy_key {
    SC_COPY_KEY_NONE,
    SC_COPY_KEY_COPY,
    SC_COPY_KEY_CUT,
};

struct sc_control_msg {
    enum sc_control_msg_type type;
    uint64_t clipboard_epoch;
    uint64_t clipboard_version;
    uint32_t clipboard_pc_sequence;
    union {
        struct sc_abr_sample abr_feedback;
        struct {
            enum android_keyevent_action action;
            enum android_keycode keycode;
            uint32_t repeat;
            enum android_metastate metastate;
        } inject_keycode;
        struct {
            char *text; // owned, to be freed by free()
        } inject_text;
        struct {
            enum android_motionevent_action action;
            enum android_motionevent_buttons action_button;
            enum android_motionevent_buttons buttons;
            uint64_t pointer_id;
            struct sc_position position;
            float pressure;
        } inject_touch_event;
        struct {
            struct sc_position position;
            float hscroll;
            float vscroll;
            enum android_motionevent_buttons buttons;
        } inject_scroll_event;
        struct {
            enum android_keyevent_action action; // action for the BACK key
            // screen may only be turned on on ACTION_DOWN
        } back_or_screen_on;
        struct {
            enum sc_copy_key copy_key;
        } get_clipboard;
        struct {
            uint64_t sequence;
            char *text; // owned, to be freed by free()
            bool paste;
        } set_clipboard;
        struct {
            bool on;
        } set_display_power;
        struct {
            uint16_t id;
            uint16_t vendor_id;
            uint16_t product_id;
            const char *name; // pointer to static data
            uint16_t report_desc_size;
            const uint8_t *report_desc; // pointer to static data
        } uhid_create;
        struct {
            uint16_t id;
            uint16_t size;
            uint8_t data[SC_HID_MAX_SIZE];
        } uhid_input;
        struct {
            uint16_t id;
        } uhid_destroy;
        struct {
            char *name;
        } start_app;
        struct {
            bool on;
        } camera_set_torch;
        struct {
            uint64_t sequence;
            uint8_t *data; // owned, to be freed by free()
            uint32_t size;
            char *mimetype; // owned, to be freed by free()
            bool paste;
        } set_image_clipboard;
        struct {
            uint16_t width;
            uint16_t height;
        } resize_display;
        struct {
            char *path; // owned, to be freed by free()
        } scan_file;
    };
};

// Return the exact serialized size in bytes for this message (including the
// type header), or 0 if the message type is unknown.
size_t
sc_control_msg_serialized_size(const struct sc_control_msg *msg);

// Allocate a buffer of the exact serialized size, write the serialized
// message into it, and return the buffer (the caller must free() it).
// *len is set to the serialized length. NULL (with *len = 0) is returned on
// allocation failure or unknown message type. The wire format is unchanged.
uint8_t *
sc_control_msg_serialize(const struct sc_control_msg *msg, size_t *len);

void
sc_control_msg_log(const struct sc_control_msg *msg);

// Even when the buffer is "full", some messages must absolutely not be dropped
// to avoid inconsistencies.
bool
sc_control_msg_is_droppable(const struct sc_control_msg *msg);

void
sc_control_msg_destroy(struct sc_control_msg *msg);

#endif
