/* Exercise the real adb argv builder without starting adb or a device. */
#define sc_adb_execute test_adb_execute
#define sc_adb_get_executable test_adb_get_executable
#include "../src/server.c"

#include <assert.h>
#include <stdio.h>

static bool saw_flex, saw_auto, saw_preserve, saw_camera;
static unsigned calls;

const char *
test_adb_get_executable(void) {
    return "adb-test-no-execution";
}

sc_pid
test_adb_execute(const char *const argv[], unsigned flags) {
    (void) flags;
    ++calls;
    saw_flex = saw_auto = saw_preserve = saw_camera = false;
    for (unsigned i = 0; argv[i]; ++i) {
        saw_flex |= !strcmp(argv[i], "flex_display=true");
        saw_auto |= !strcmp(argv[i], "flex_display_auto_dpi=true");
        saw_preserve |= !strcmp(argv[i], "flex_display_preserve_ui=true");
        saw_camera |= !strcmp(argv[i], "perspective_camera=true");
    }
    return SC_PROCESS_NONE;
}

int main(void) {
    struct sc_server server = {.serial = "TEST-NO-DEVICE"};
    struct sc_server_params params = {
        .log_level = SC_LOG_LEVEL_INFO,
        .video_codec = SC_CODEC_H264,
        .audio_codec = SC_CODEC_OPUS,
        .video_source = SC_VIDEO_SOURCE_DISPLAY,
        .screen_off_timeout = -1,
        .new_display = "3200x2136/440",
        .flex_display = true,
        .perspective_package = "com.android.browser",
        .video = true, .control = true, .cleanup = true,
    };
    _putenv_s("SCEZ_VD_AUTO_DPI", "1");
    _putenv_s("SCEZ_VD_PRESERVE_UI", "");
    for (unsigned camera = 0; camera < 2; ++camera) {
        params.perspective_camera = camera;
        execute_server(&server, &params);
        assert(saw_flex && saw_auto && saw_preserve);
        assert(saw_camera == (bool) camera);
    }
    // Explicit compatibility override and manual DPI still use the old path.
    _putenv_s("SCEZ_VD_PRESERVE_UI", "0");
    execute_server(&server, &params);
    assert(saw_flex && saw_auto && !saw_preserve && saw_camera);
    _putenv_s("SCEZ_VD_PRESERVE_UI", "");
    _putenv_s("SCEZ_VD_AUTO_DPI", "0");
    execute_server(&server, &params);
    assert(saw_flex && !saw_auto && !saw_preserve && saw_camera);
    params.flex_display = false;
    _putenv_s("SCEZ_VD_AUTO_DPI", "1");
    execute_server(&server, &params);
    assert(!saw_flex && !saw_auto && !saw_preserve && saw_camera);
    assert(calls == 5);
    puts("PASS: real server argv preserves flex UI with camera on/off; manual DPI, opt-out and flex-off remain independent");
    return 0;
}
