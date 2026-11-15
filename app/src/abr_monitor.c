#include "abr_monitor.h"
#include <string.h>

bool sc_abr_monitor_init(struct sc_abr_monitor *m, sc_tick allowance) {
    memset(m, 0, sizeof(*m));
    m->allowance = allowance;
    return sc_mutex_init(&m->mutex);
}

void sc_abr_monitor_destroy(struct sc_abr_monitor *m) {
    sc_mutex_destroy(&m->mutex);
}

uint32_t sc_abr_monitor_epoch(struct sc_abr_monitor *m, bool reset) {
    sc_mutex_lock(&m->mutex);
    if (reset) {
        ++m->epoch;
        m->clock_valid = false;
        m->last_arrival = 0;
        m->delivery_delay = 0;
        m->rate_start = 0;
        m->rate_bytes = 0;
        m->receive_bps = 0;
    }
    uint32_t epoch = m->epoch;
    sc_mutex_unlock(&m->mutex);
    return epoch;
}

void sc_abr_monitor_arrival(struct sc_abr_monitor *m, int64_t send_us, sc_tick now, uint32_t bytes) {
    if (send_us <= 0) return;
    sc_mutex_lock(&m->mutex);
    sc_tick offset = now - send_us;
    sc_tick elapsed = now - m->last_arrival;
    if (!m->rate_start || elapsed > SC_TICK_FROM_SEC(3)) {
        m->rate_start = now;
        m->rate_bytes = 0;
        m->receive_bps = 0;
    }
    m->rate_bytes += bytes;
    if (now - m->rate_start >= SC_TICK_FROM_MS(500)) {
        uint64_t bps = m->rate_bytes * 8 * SC_TICK_FREQ / (now - m->rate_start);
        m->receive_bps = bps > 1000000000 ? 1000000000 : bps;
        m->rate_bytes = 0;
        m->rate_start = now;
    }
    if (!m->clock_valid || send_us <= m->last_send
            || elapsed > SC_TICK_FROM_SEC(3)) {
        m->offset_floor = offset;
        m->clock_valid = true;
    } else {
        // At most 100ppm upward drift; never follow short congestion bursts.
        m->drift_remainder += elapsed;
        m->offset_floor += m->drift_remainder / 10000;
        m->drift_remainder %= 10000;
        if (offset < m->offset_floor) m->offset_floor = offset;
    }
    m->delivery_delay = offset - m->offset_floor;
    m->last_arrival = now;
    m->last_send = send_us;
    sc_mutex_unlock(&m->mutex);
}

void sc_abr_monitor_queue(struct sc_abr_monitor *m, sc_tick oldest, uint32_t count) {
    sc_mutex_lock(&m->mutex);
    m->oldest_queued = oldest;
    m->queue_count = count;
    sc_mutex_unlock(&m->mutex);
}

void sc_abr_monitor_work(struct sc_abr_monitor *m, sc_tick work) {
    if (work < 0) return;
    if (work > SC_TICK_FROM_SEC(10)) work = SC_TICK_FROM_SEC(10);
    sc_mutex_lock(&m->mutex);
    m->work_us = m->work_us ? (m->work_us * 7 + work) / 8 : work;
    sc_mutex_unlock(&m->mutex);
}

void sc_abr_monitor_decode(struct sc_abr_monitor *m, uint32_t epoch,
                           sc_tick recv, bool blocked) {
    sc_mutex_lock(&m->mutex);
    m->decode_epoch = epoch;
    m->active_recv = recv;
    m->blocked = blocked;
    sc_mutex_unlock(&m->mutex);
}

void sc_abr_monitor_output(struct sc_abr_monitor *m, bool hardware) {
    sc_mutex_lock(&m->mutex);
    m->hardware = hardware;
    sc_mutex_unlock(&m->mutex);
}

static uint32_t age_ms(sc_tick age) {
    if (age <= 0) return 0;
    return age > SC_TICK_FROM_SEC(10) ? 10000 : age / 1000;
}

struct sc_abr_sample sc_abr_monitor_sample(struct sc_abr_monitor *m, sc_tick now) {
    sc_mutex_lock(&m->mutex);
    struct sc_abr_sample sample = {
        .epoch = m->epoch,
        .sequence = ++m->sequence,
        .delivery_ms = age_ms(m->delivery_delay),
        .queue_count = m->queue_count,
        .work_us = m->work_us,
        .receive_bps = m->receive_bps,
        .flags = (m->hardware ? SC_ABR_FEEDBACK_HW : 0)
               | (m->blocked ? SC_ABR_FEEDBACK_BLOCKED : 0),
    };
    if (m->clock_valid && m->epoch == m->decode_epoch) {
        sc_tick oldest = m->oldest_queued;
        if (m->active_recv && (!oldest || m->active_recv < oldest)) {
            oldest = m->active_recv;
        }
        if (now - m->last_arrival < SC_TICK_FROM_SEC(1)) {
            sample.flags |= SC_ABR_FEEDBACK_VALID;
            if (oldest && !m->blocked) {
                sample.queue_ms = age_ms(now - oldest - m->allowance);
            }
        } else if (!oldest && !m->queue_count && !m->blocked) {
            // This epoch received video, then went quiet and fully drained.
            sample.flags |= SC_ABR_FEEDBACK_IDLE;
        }
    }
    sc_mutex_unlock(&m->mutex);
    return sample;
}
