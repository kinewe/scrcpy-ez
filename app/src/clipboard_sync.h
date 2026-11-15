#ifndef SC_CLIPBOARD_SYNC_H
#define SC_CLIPBOARD_SYNC_H

#include "common.h"
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#ifdef _WIN32
struct sc_control_msg;
bool sc_clipboard_sync_stamp(struct sc_control_msg *msg);
// Coordination is per physical device, not per transport or display. Shared
// memory holds transaction metadata only; it never contains clipboard data.
bool sc_clipboard_sync_init(const char *serial);
void sc_clipboard_sync_destroy(void);
void sc_clipboard_sync_disconnected(void);

// A short-lived claim for one automatic transfer. Completion requires a device
// acknowledgement. A surviving session may reclaim it after the sender exits.
bool sc_clipboard_sync_begin(uint32_t pc_sequence, uint64_t *transaction);
bool sc_clipboard_sync_needs_recovery(uint32_t pc_sequence);
void sc_clipboard_sync_failed(uint64_t transaction);
bool sc_clipboard_sync_ack(uint64_t sequence);

// Called on the SDL main thread. Complete, validated data is rendered eagerly
// into Windows-owned memory, so closing a casting window cannot erase it.
bool sc_clipboard_sync_receive_text(const char *text, uint64_t revision,
                                    uint32_t expected_sequence);
bool sc_clipboard_sync_receive_image(const char *mime, const uint8_t *data,
                                     size_t size, uint64_t revision,
                                     uint32_t expected_sequence);
// Preserve the original compressed image when forwarding a device image.
bool sc_clipboard_sync_get_image(uint8_t **data, size_t *size, char **mime,
                                 size_t limit, bool *oversized);
#endif
#endif
