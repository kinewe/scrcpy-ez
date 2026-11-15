#include "controller.h"

#include <assert.h>
#include <inttypes.h>
#include <stdlib.h>
#include <string.h>

#include "util/log.h"
#include "clipboard_sync.h"

// Drop droppable events above this limit
#define SC_CONTROL_MSG_QUEUE_LIMIT 60

// Only renew the connection lease; never inject activity into Android.
#define SC_KEEP_AWAKE_HEARTBEAT_INTERVAL SC_TICK_FROM_SEC(5)

static void
sc_controller_receiver_on_ended(struct sc_receiver *receiver, bool error,
                                void *userdata) {
    (void) receiver;

    struct sc_controller *controller = userdata;
    // Forward the event to the controller listener
    controller->cbs->on_ended(controller, error, controller->cbs_userdata);
}

bool
sc_controller_init(struct sc_controller *controller, sc_socket control_socket,
                   bool keep_awake,
                   const struct sc_controller_callbacks *cbs,
                   void *cbs_userdata) {
    sc_vecdeque_init(&controller->queue);

    // Add 4 to support 4 non-droppable events without re-allocation
    bool ok = sc_vecdeque_reserve(&controller->queue,
                                  SC_CONTROL_MSG_QUEUE_LIMIT + 4);
    if (!ok) {
        return false;
    }

    static const struct sc_receiver_callbacks receiver_cbs = {
        .on_ended = sc_controller_receiver_on_ended,
    };

    ok = sc_receiver_init(&controller->receiver, control_socket, &receiver_cbs,
                          controller);
    if (!ok) {
        sc_vecdeque_destroy(&controller->queue);
        return false;
    }

    ok = sc_mutex_init(&controller->mutex);
    if (!ok) {
        sc_receiver_destroy(&controller->receiver);
        sc_vecdeque_destroy(&controller->queue);
        return false;
    }

    ok = sc_cond_init(&controller->msg_cond);
    if (!ok) {
        sc_receiver_destroy(&controller->receiver);
        sc_mutex_destroy(&controller->mutex);
        sc_vecdeque_destroy(&controller->queue);
        return false;
    }

    controller->control_socket = control_socket;
    controller->stopped = false;
    controller->keep_awake = keep_awake;
    controller->abr_monitor = NULL;

    controller->resize_display.width = 0;
    controller->resize_display.height = 0;

    assert(cbs && cbs->on_ended);
    controller->cbs = cbs;
    controller->cbs_userdata = cbs_userdata;

    return true;
}

void
sc_controller_configure(struct sc_controller *controller,
                        struct sc_acksync *acksync,
                        struct sc_uhid_devices *uhid_devices) {
    controller->receiver.acksync = acksync;
    controller->receiver.uhid_devices = uhid_devices;
}

void
sc_controller_destroy(struct sc_controller *controller) {
#ifdef _WIN32
    sc_clipboard_sync_disconnected();
#endif
    sc_cond_destroy(&controller->msg_cond);
    sc_mutex_destroy(&controller->mutex);

    while (!sc_vecdeque_is_empty(&controller->queue)) {
        struct sc_control_msg *msg = sc_vecdeque_popref(&controller->queue);
        assert(msg);
        sc_control_msg_destroy(msg);
    }
    sc_vecdeque_destroy(&controller->queue);

    sc_receiver_destroy(&controller->receiver);
}

bool
sc_controller_push_msg(struct sc_controller *controller,
                       const struct sc_control_msg *msg) {
    // RESIZE_DISPLAY messages are handled separately
    assert(msg->type != SC_CONTROL_MSG_TYPE_RESIZE_DISPLAY);
#ifdef _WIN32
    struct sc_control_msg stamped = *msg;
    if (!sc_clipboard_sync_stamp(&stamped)) { return false; }
    msg = &stamped;
#endif

    bool pushed = false;

    sc_mutex_lock(&controller->mutex);
    size_t size = sc_vecdeque_size(&controller->queue);
    if (size < SC_CONTROL_MSG_QUEUE_LIMIT) {
        bool was_empty = sc_vecdeque_is_empty(&controller->queue);
        sc_vecdeque_push_noresize(&controller->queue, *msg);
        pushed = true;
        if (was_empty) {
            sc_cond_signal(&controller->msg_cond);
        }
    } else if (!sc_control_msg_is_droppable(msg)) {
        bool ok = sc_vecdeque_push(&controller->queue, *msg);
        if (ok) {
            pushed = true;
        } else {
            // A non-droppable event must be dropped anyway
            LOG_OOM();
        }
    }
    // Otherwise, the msg is discarded

    sc_mutex_unlock(&controller->mutex);

    return pushed;
}

void
sc_controller_resize_display(struct sc_controller *controller,
                             uint16_t width, uint16_t height) {
    assert(width && height);
    sc_mutex_lock(&controller->mutex);
    bool was_set = controller->resize_display.width;
    controller->resize_display.width = width;
    controller->resize_display.height = height;
    if (!was_set) {
        sc_cond_signal(&controller->msg_cond);
    }
    sc_mutex_unlock(&controller->mutex);
}

static bool
process_msg(struct sc_controller *controller,
            const struct sc_control_msg *msg, bool *eos) {
    // Serialize into a buffer allocated with the exact message size: control
    // messages may carry large image clipboard payloads, but most are tiny,
    // so a static SC_CONTROL_MSG_MAX_SIZE (256M) buffer must not be reserved
    // in BSS.
    size_t length;
    uint8_t *serialized_msg = sc_control_msg_serialize(msg, &length);
    if (!serialized_msg) {
#ifdef _WIN32
        if (msg->type == SC_CONTROL_MSG_TYPE_SET_CLIPBOARD_VERSIONED) {
            sc_clipboard_sync_failed(msg->set_clipboard.sequence);
        } else if (msg->type == SC_CONTROL_MSG_TYPE_SET_IMAGE_CLIPBOARD_VERSIONED) {
            sc_clipboard_sync_failed(msg->set_image_clipboard.sequence);
        }
#endif
        *eos = false;
        return false;
    }

    ssize_t w =
        net_send_all(controller->control_socket, serialized_msg, length);
    free(serialized_msg);
    if ((size_t) w != length) {
        *eos = true;
        return false;
    }

    return true;
}

static int
run_controller(void *data) {
    struct sc_controller *controller = data;

    bool error = false;
    sc_tick next_heartbeat = sc_tick_now();
    sc_tick next_feedback = sc_tick_now();
    const bool diagnostics = strstr(SCRCPY_VERSION, "-resize8") != NULL;
    uint64_t inputs_sent = 0;
    uint64_t heartbeats_sent = 0;
    sc_tick next_diagnostic = 0;

    for (;;) {
        sc_mutex_lock(&controller->mutex);
        while (!controller->stopped
                && !controller->resize_display.width
                && sc_vecdeque_is_empty(&controller->queue)) {
            if (controller->abr_monitor && sc_tick_now() >= next_feedback) break;
            if (controller->abr_monitor || controller->keep_awake) {
                if (controller->keep_awake && sc_tick_now() >= next_heartbeat) {
                    break;
                }
                sc_cond_timedwait(&controller->msg_cond, &controller->mutex,
                                 controller->abr_monitor
                                     ? (controller->keep_awake && next_heartbeat < next_feedback ? next_heartbeat : next_feedback)
                                     : next_heartbeat);
            } else {
                sc_cond_wait(&controller->msg_cond, &controller->mutex);
            }
        }
        if (controller->stopped) {
            // stop immediately, do not process further msgs
            sc_mutex_unlock(&controller->mutex);
            LOGD("Controller stopped");
            break;
        }

        bool has_resize_display = controller->resize_display.width;
        bool heartbeat_due = controller->keep_awake
                          && sc_tick_now() >= next_heartbeat;
        bool feedback_due = controller->abr_monitor && sc_tick_now() >= next_feedback;
        assert(feedback_due || heartbeat_due || has_resize_display
               || !sc_vecdeque_is_empty(&controller->queue));

        struct sc_control_msg msg;

        if (heartbeat_due) {
            // Do not enqueue behind large clipboard/input bursts.
            msg = (struct sc_control_msg) {
                .type = SC_CONTROL_MSG_TYPE_KEEP_AWAKE_HEARTBEAT,
            };
            next_heartbeat = sc_tick_now() + SC_KEEP_AWAKE_HEARTBEAT_INTERVAL;
        } else if (feedback_due) {
            msg = (struct sc_control_msg) {.type = SC_CONTROL_MSG_TYPE_ABR_FEEDBACK};
            msg.abr_feedback = sc_abr_monitor_sample(controller->abr_monitor, sc_tick_now());
            next_feedback = sc_tick_now() + SC_TICK_FROM_MS(200);
        } else if (has_resize_display) {
            msg.type = SC_CONTROL_MSG_TYPE_RESIZE_DISPLAY;
            msg.resize_display.width = controller->resize_display.width;
            msg.resize_display.height = controller->resize_display.height;
            controller->resize_display.width = 0;
            controller->resize_display.height = 0;
        } else {
            msg = sc_vecdeque_pop(&controller->queue);
        }
        sc_mutex_unlock(&controller->mutex);

        if (sc_get_log_level() <= SC_LOG_LEVEL_VERBOSE) {
            sc_control_msg_log(&msg);
        }

        bool eos;
        bool ok = process_msg(controller, &msg, &eos);
        if (diagnostics && ok) {
            if (msg.type == SC_CONTROL_MSG_TYPE_KEEP_AWAKE_HEARTBEAT) {
                ++heartbeats_sent;
            } else if (msg.type == SC_CONTROL_MSG_TYPE_INJECT_TOUCH_EVENT
                    || msg.type == SC_CONTROL_MSG_TYPE_INJECT_SCROLL_EVENT) {
                ++inputs_sent;
            }
            sc_tick now = sc_tick_now();
            if (now >= next_diagnostic) {
                LOGI("SCRCPY_EZ_CONTROL_TX heartbeats=%" PRIu64 " input_sent=%" PRIu64,
                     heartbeats_sent, inputs_sent);
                next_diagnostic = now + SC_TICK_FROM_SEC(5);
            }
        }
        sc_control_msg_destroy(&msg);
        if (!ok) {
            if (eos) {
                LOGD("Controller stopped (socket closed)");
            } // else error already logged
            error = !eos;
            break;
        }
    }

    controller->cbs->on_ended(controller, error, controller->cbs_userdata);

    return 0;
}

bool
sc_controller_start(struct sc_controller *controller) {
    LOGD("Starting controller thread");

    bool ok = sc_thread_create(&controller->thread, run_controller,
                               "scrcpy-ctl", controller);
    if (!ok) {
        LOGE("Could not start controller thread");
        return false;
    }

    if (!sc_receiver_start(&controller->receiver)) {
        sc_controller_stop(controller);
        sc_thread_join(&controller->thread, NULL);
        return false;
    }

    return true;
}

void
sc_controller_stop(struct sc_controller *controller) {
    sc_mutex_lock(&controller->mutex);
    controller->stopped = true;
    sc_cond_signal(&controller->msg_cond);
    sc_mutex_unlock(&controller->mutex);
}

void
sc_controller_join(struct sc_controller *controller) {
    sc_thread_join(&controller->thread, NULL);
    sc_receiver_join(&controller->receiver);
}
