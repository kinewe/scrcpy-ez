#include "abr_monitor.h"
#include <assert.h>

int main(void) {
    struct sc_abr_monitor m;
    assert(sc_abr_monitor_init(&m, SC_TICK_FROM_MS(32)));
    uint32_t epoch = sc_abr_monitor_epoch(&m, true);
    sc_abr_monitor_decode(&m, epoch, 0, false);
    sc_abr_monitor_arrival(&m, 1000000, 5000000, 1000);
    struct sc_abr_sample s = sc_abr_monitor_sample(&m, 5000000);
    assert((s.flags & SC_ABR_FEEDBACK_VALID) && !s.delivery_ms && !s.queue_ms);
    sc_abr_monitor_arrival(&m, 1100000, 5180000, 1000);
    sc_abr_monitor_queue(&m, 5100000, 3);
    sc_abr_monitor_work(&m, 25000);
    s = sc_abr_monitor_sample(&m, 5200000);
    assert(s.delivery_ms >= 79 && s.queue_ms == 68 && s.queue_count == 3 && s.work_us == 25000);
    sc_abr_monitor_decode(&m, epoch, 5100000, true);
    s = sc_abr_monitor_sample(&m, 5200000);
    assert(!s.queue_ms && (s.flags & SC_ABR_FEEDBACK_BLOCKED));
    sc_abr_monitor_output(&m, true);
    assert(sc_abr_monitor_sample(&m, 5200000).flags & SC_ABR_FEEDBACK_HW);
    sc_abr_monitor_output(&m, false);
    assert(!(sc_abr_monitor_sample(&m, 5200000).flags & SC_ABR_FEEDBACK_HW));
    assert(!(sc_abr_monitor_sample(&m, 7000000).flags & (SC_ABR_FEEDBACK_VALID | SC_ABR_FEEDBACK_IDLE)));
    sc_abr_monitor_queue(&m, 0, 0);
    sc_abr_monitor_decode(&m, epoch, 5100000, false);
    assert(!(sc_abr_monitor_sample(&m, 7000000).flags & SC_ABR_FEEDBACK_IDLE));
    sc_abr_monitor_decode(&m, epoch, 0, false);
    assert(sc_abr_monitor_sample(&m, 7000000).flags & SC_ABR_FEEDBACK_IDLE);
    uint32_t next = sc_abr_monitor_epoch(&m, true);
    assert(!(sc_abr_monitor_sample(&m, 8000000).flags & SC_ABR_FEEDBACK_IDLE));
    sc_abr_monitor_arrival(&m, 2000000, 9000000, 1000);
    assert(!(sc_abr_monitor_sample(&m, 9000000).flags & SC_ABR_FEEDBACK_VALID));
    sc_abr_monitor_decode(&m, next, 0, false);
    sc_abr_monitor_queue(&m, 0, 0);
    s = sc_abr_monitor_sample(&m, 9000000);
    assert((s.flags & SC_ABR_FEEDBACK_VALID) && !s.delivery_ms && !s.queue_ms);
    sc_abr_monitor_arrival(&m, 2500000, 9500000, 1000);
    assert(sc_abr_monitor_sample(&m, 9500000).receive_bps == 32000);
    sc_abr_monitor_destroy(&m);
    return 0;
}
