#ifndef SC_PERSPECTIVE_H
#define SC_PERSPECTIVE_H
#include "common.h"
#include <stdbool.h>
#include <stdatomic.h>
#include <libavutil/frame.h>
#include "util/net.h"
#include "util/thread.h"

#define SC_KEY_PROBE_TIME SC_TICK_FROM_MS(1200)

enum sc_key_probe_state {
    SC_KEY_PROBE_IDLE,
    SC_KEY_PROBE_REQUESTED,
    SC_KEY_PROBE_ACTIVE,
    SC_KEY_PROBE_VERIFY,
    SC_KEY_PROBE_DONE,
    SC_KEY_PROBE_DISABLED,
};

struct sc_perspective {
    sc_socket socket;
    sc_thread thread;
    sc_thread receiver;
    sc_mutex mutex;
    atomic_bool stopped;
    atomic_bool active;
    atomic_uint camera_cycle;
    atomic_bool evicted;
    atomic_bool compatibility_key;
    atomic_bool keyframe;
    atomic_bool calibration_requested;
    atomic_uint photo_phase;
    void *hwnd;
    unsigned fps;
    unsigned input_period;
    uint32_t previous_affinity;
    bool affinity_changed;
    double x, y, width, height;
    double map[6]; // camera (u,v) -> normalized phone-frame (x,y)
    unsigned orientation;
    // Hot-start calibration cache (geometry key -> affine map), injected by
    // the GUI from profiles.json through SCEZ_PERSPECTIVE_CALIB.
    char cache_env[1200];
    char cache_key[64];
    double cache_map[6];
    bool cache_hit, cache_verified, cache_reported;
    // GUI-written "WeChat BaseScanUI is foreground" signal file. When set, a
    // markerless calibration waits for the page instead of a fixed retry delay.
    char ready_file[280];
    bool scanner_waiting_ready, scanner_ready_seen;
    sc_tick scanner_ready_deadline;
    bool calibrating;
    unsigned calibration_pass;
    bool calibration_region_valid;
    bool scanner_alignment;
    bool alignment_valid;
    uint32_t handoff_epoch;
    sc_tick handoff_deadline;
    bool handoff_confirmed;
    bool handoff_restarted;
    double calibration_region[4]; // normalized visible camera rectangle
    enum sc_key_probe_state key_probe;
    sc_tick key_probe_deadline;
    unsigned key_channel;
    sc_tick calibration_deadline;
    sc_tick calibration_started;
    // Scanner startup may show a stale WeChat page (e.g. the previous QR
    // result) before the camera preview exists; track the calibration field
    // and retry once the page settles instead of walking the legacy ladder.
    bool calibration_field_seen;
    sc_tick calibration_retry_at;
    unsigned calibration_retry_count;
    sc_tick last_resize;
    sc_tick calibration_not_before;
    double candidate_map[6];
    unsigned candidate_count;
    sc_tick candidate_since;
    int observed_width, observed_height;
    bool started;
    bool threads_joined;
};
bool sc_perspective_start(struct sc_perspective *p, sc_socket socket, void *hwnd,
                          unsigned input_period, bool scanner_alignment);
void sc_perspective_begin_handoff(struct sc_perspective *p, uint32_t epoch);
void sc_perspective_confirm_handoff(struct sc_perspective *p, uint32_t epoch);
void sc_perspective_update(struct sc_perspective *p, double x, double y,
                           double width, double height, unsigned orientation);
void sc_perspective_stop(struct sc_perspective *p);
void sc_perspective_destroy(struct sc_perspective *p);
void sc_perspective_observe(struct sc_perspective *p, const AVFrame *frame);
#endif
