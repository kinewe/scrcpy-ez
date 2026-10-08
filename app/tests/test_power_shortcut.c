// Exercise the actual input handler and controller queue without a GUI/device.
#include "../src/input_manager.c"

// Keep the configured ABI (sc_mutex differs with NDEBUG), but always check
// the test assertions even when linking to release client objects.
#undef assert
#define assert(condition) do { \
    if (!(condition)) { \
        fprintf(stderr, "check failed at line %d: %s\n", __LINE__, #condition); \
        exit(1); \
    } \
} while (0)

static unsigned forwarded;

static void
on_key(struct sc_key_processor *kp, const struct sc_key_event *event,
       uint64_t sequence) {
    (void) kp;
    (void) event;
    (void) sequence;
    ++forwarded;
}

static void
on_ended(struct sc_controller *controller, bool error, void *userdata) {
    (void) controller;
    (void) error;
    (void) userdata;
    assert(false);
}

static void
key(struct sc_input_manager *im, SDL_Keymod mods, bool down, bool repeat) {
    SDL_KeyboardEvent event = {
        .type = down ? SDL_EVENT_KEY_DOWN : SDL_EVENT_KEY_UP,
        .key = SDLK_P,
        .scancode = SDL_SCANCODE_P,
        .mod = mods,
        .repeat = repeat,
    };
    sc_input_manager_process_key(im, &event);
}

static void
expect_press(struct sc_controller *controller) {
    assert(sc_vecdeque_size(&controller->queue) == 2);
    struct sc_control_msg down = sc_vecdeque_pop(&controller->queue);
    struct sc_control_msg up = sc_vecdeque_pop(&controller->queue);
    assert(down.type == SC_CONTROL_MSG_TYPE_INJECT_KEYCODE);
    assert(up.type == SC_CONTROL_MSG_TYPE_INJECT_KEYCODE);
    assert(down.inject_keycode.keycode == AKEYCODE_POWER);
    assert(up.inject_keycode.keycode == AKEYCODE_POWER);
    assert(down.inject_keycode.action == AKEY_EVENT_ACTION_DOWN);
    assert(up.inject_keycode.action == AKEY_EVENT_ACTION_UP);
    assert(down.inject_keycode.repeat == 0 && up.inject_keycode.repeat == 0);
    assert(down.inject_keycode.metastate == 0 && up.inject_keycode.metastate == 0);
}

int
main(void) {
    _set_error_mode(_OUT_TO_STDERR);
    SetErrorMode(SEM_FAILCRITICALERRORS | SEM_NOGPFAULTERRORBOX);
    static const struct sc_controller_callbacks cbs = {.on_ended = on_ended};
    static const struct sc_key_processor_ops key_ops = {.process_key = on_key};
    struct sc_controller controller;
    assert(sc_controller_init(&controller, SC_SOCKET_NONE, &cbs, NULL));
    struct sc_key_processor kp = {.ops = &key_ops};
    struct sc_screen screen = {.video = true};
    struct sc_input_manager im = {
        .controller = &controller, .kp = &kp, .screen = &screen,
        .sdl_shortcut_mods = SDL_KMOD_LALT,
    };

    // Both Ctrl keys work; repeats and early release of Ctrl never leak P.
    for (unsigned i = 0; i < 2; ++i) {
        SDL_Keymod ctrl = i ? SDL_KMOD_RCTRL : SDL_KMOD_LCTRL;
        key(&im, ctrl, true, false);
        expect_press(&controller);
        key(&im, ctrl, true, true);
        key(&im, 0, false, false);
        assert(sc_vecdeque_is_empty(&controller.queue));
        assert(!im.ctrl_power_pressed && forwarded == 0);
    }

    // Unrelated printing/typing shortcuts still reach the Android keyboard.
    key(&im, 0, true, false);
    key(&im, SDL_KMOD_LCTRL | SDL_KMOD_LSHIFT, true, false);
    assert(forwarded == 2 && sc_vecdeque_is_empty(&controller.queue));

    // No injection during pause, disconnect, camera, disabled keyboard/control.
    for (unsigned i = 0; i < 5; ++i) {
        screen.paused = i == 0;
        im.disconnected = i == 1;
        im.camera = i == 2;
        im.kp = i == 3 ? NULL : &kp;
        im.controller = i == 4 ? NULL : &controller;
        key(&im, SDL_KMOD_LCTRL, true, false);
        key(&im, 0, false, false);
        assert(sc_vecdeque_is_empty(&controller.queue));
        assert(forwarded == 2);
    }

    screen.paused = im.disconnected = im.camera = false;
    im.kp = &kp;
    im.controller = &controller;
    // Existing MOD+P keeps its usual down/up behavior.
    key(&im, SDL_KMOD_LALT, true, false);
    key(&im, SDL_KMOD_LALT, false, false);
    expect_press(&controller);
    sc_controller_destroy(&controller);
    puts("Ctrl+P: power pair, repeats, early release, isolation and MOD+P passed");
    return 0;
}
