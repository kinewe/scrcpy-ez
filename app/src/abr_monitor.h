#ifndef SC_ABR_MONITOR_H
#define SC_ABR_MONITOR_H

#include "util/thread.h"
#include "util/tick.h"
#include <stdint.h>

#define SC_ABR_FEEDBACK_VALID 1
#define SC_ABR_FEEDBACK_HW 2
#define SC_ABR_FEEDBACK_BLOCKED 4
#define SC_ABR_FEEDBACK_IDLE 8

struct sc_abr_sample {
    uint32_t epoch, sequence, queue_ms, delivery_ms, flags, queue_count, work_us, receive_bps;
};

// Per connection. No wall-clock synchronization or GPU-name heuristic.
struct sc_abr_monitor {
    sc_mutex mutex;
    uint32_t epoch, decode_epoch, sequence;
    sc_tick last_arrival, last_send, offset_floor, delivery_delay, drift_remainder;
    sc_tick oldest_queued, active_recv, allowance;
    bool clock_valid, blocked, hardware;
    uint32_t queue_count, work_us;
    sc_tick rate_start;
    uint64_t rate_bytes;
    uint32_t receive_bps;
};

bool sc_abr_monitor_init(struct sc_abr_monitor *m, sc_tick allowance);
void sc_abr_monitor_destroy(struct sc_abr_monitor *m);
uint32_t sc_abr_monitor_epoch(struct sc_abr_monitor *m, bool reset);
void sc_abr_monitor_arrival(struct sc_abr_monitor *m, int64_t send_us, sc_tick now, uint32_t bytes);
void sc_abr_monitor_queue(struct sc_abr_monitor *m, sc_tick oldest, uint32_t count);
void sc_abr_monitor_work(struct sc_abr_monitor *m, sc_tick work);
void sc_abr_monitor_decode(struct sc_abr_monitor *m, uint32_t epoch,
                           sc_tick recv, bool blocked);
void sc_abr_monitor_output(struct sc_abr_monitor *m, bool hardware);
struct sc_abr_sample sc_abr_monitor_sample(struct sc_abr_monitor *m, sc_tick now);

#endif
