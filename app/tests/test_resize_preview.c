#include "common.h"

#include <assert.h>
#include <math.h>
#include <stdio.h>
#include <SDL3/SDL.h>

#include "resize_preview.h"

static void
test_geometry(void) {
    struct sc_size content[] = {{1952, 1296}, {800, 1200}, {3200, 2136}};
    struct sc_size windows[] = {{800, 1200}, {1952, 1296}, {3840, 2160}};
    for (unsigned i = 0; i < 3; ++i) {
        SDL_FRect rect;
        assert(sc_resize_preview_rect(windows[i], content[i], &rect));
        assert(rect.x >= -0.001f && rect.y >= -0.001f);
        assert(rect.x + rect.w <= windows[i].width + 0.001f);
        assert(rect.y + rect.h <= windows[i].height + 0.001f);
        assert(fabsf(rect.w / rect.h
                     - (float) content[i].width / content[i].height) < 0.001f);
        // The same rectangle is used to map input: its center and quarter
        // points must still address the same content, including black bars.
        float x = rect.x + rect.w * 0.25f;
        float y = rect.y + rect.h * 0.5f;
        assert(fabsf((x - rect.x) * content[i].width / rect.w
                     - content[i].width * 0.25f) < 0.01f);
        assert(fabsf((y - rect.y) * content[i].height / rect.h
                     - content[i].height * 0.5f) < 0.01f);
    }
    SDL_FRect unchanged = {1, 2, 3, 4};
    assert(!sc_resize_preview_rect((struct sc_size) {800, 600},
                                   (struct sc_size) {800, 600}, &unchanged));
    assert(!sc_resize_preview_rect((struct sc_size) {0, 600},
                                   (struct sc_size) {800, 600}, &unchanged));
    assert(!sc_resize_preview_rect((struct sc_size) {800, 600},
                                   (struct sc_size) {0, 600}, &unchanged));
    assert(unchanged.x == 1 && unchanged.w == 3);
}

struct fixture {
    unsigned paints;
    bool shown;
    bool busy;
    bool nested;
};

static void
push_window(Uint32 type, SDL_WindowID window) {
    SDL_Event event = {.window = {.type = type, .windowID = window, .data1 = 1}};
    assert(SDL_PushEvent(&event));
}

static bool
watch(void *data, SDL_Event *event) {
    struct fixture *f = data;
    if (sc_resize_preview_redraw_event(event, 42, SDL_IsMainThread(),
                                       f->shown, f->busy)) {
        f->busy = true;
        SDL_FlushEvent(SDL_EVENT_USER); // discard obsolete frame wakeups only
        ++f->paints;
        if (f->nested) {
            f->nested = false;
            push_window(SDL_EVENT_WINDOW_EXPOSED, 42);
        }
        f->busy = false;
    }
    return true;
}

static int
worker(void *unused) {
    (void) unused;
    push_window(SDL_EVENT_WINDOW_EXPOSED, 42);
    return 0;
}

static void
test_live_event_dispatch(void) {
    struct fixture f = {.shown = true};
    assert(SDL_AddEventWatch(watch, &f));
    SDL_Event old_frame = {.user = {.type = SDL_EVENT_USER}};
    SDL_Event control = {.user = {.type = SDL_EVENT_USER + 1}};
    assert(SDL_PushEvent(&old_frame) && SDL_PushEvent(&control));
    // Do not poll: SDL's watch must paint immediately while a modal native
    // window loop prevents the application's regular event loop from running.
    push_window(SDL_EVENT_WINDOW_RESIZED, 42);
    push_window(SDL_EVENT_WINDOW_PIXEL_SIZE_CHANGED, 42);
    push_window(SDL_EVENT_WINDOW_EXPOSED, 42);
    push_window(SDL_EVENT_WINDOW_MOVED, 42);
    assert(f.paints == 4);
    assert(!SDL_HasEvent(SDL_EVENT_USER));
    assert(SDL_HasEvent(SDL_EVENT_USER + 1)); // input/control events survive
    push_window(SDL_EVENT_WINDOW_RESIZED, 99);
    assert(f.paints == 4);
    f.shown = false;
    push_window(SDL_EVENT_WINDOW_EXPOSED, 42);
    assert(f.paints == 4);
    f.shown = true;
    f.nested = true;
    push_window(SDL_EVENT_WINDOW_EXPOSED, 42);
    assert(f.paints == 5); // nested expose is not allowed to reenter rendering

    SDL_Thread *thread = SDL_CreateThread(worker, "preview-worker", NULL);
    assert(thread);
    int status = -1;
    SDL_WaitThread(thread, &status);
    assert(status == 0 && f.paints == 5); // producer thread must never render

    SDL_Event user = {.user = {.type = SDL_EVENT_USER}};
    assert(SDL_PushEvent(&user));
    assert(f.paints == 5);
    assert(SDL_HasEvent(SDL_EVENT_USER));
    SDL_RemoveEventWatch(watch, &f);
    push_window(SDL_EVENT_WINDOW_EXPOSED, 42);
    assert(f.paints == 5);
}

int main(void) {
    assert(SDL_Init(SDL_INIT_EVENTS)); // no window, renderer or Android device
    test_geometry();
    test_live_event_dispatch();
    SDL_Quit();
    puts("resize preview geometry and live SDL dispatch: PASS");
    return 0;
}
