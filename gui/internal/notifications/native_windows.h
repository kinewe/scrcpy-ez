#ifndef SCEZ_NOTIFICATIONS_H
#define SCEZ_NOTIFICATIONS_H
#include <stdint.h>
#ifdef __cplusplus
extern "C" {
#endif
int32_t scez_toast_open(const char *app_id, const char *exe, const char *shortcut, const char *clsid, uint64_t handle, int *copy_ready, void **result, int *phase);
int32_t scez_toast_show(void *context, const char *xml, const char *group, const char *tag, int silent, int *phase);
int32_t scez_toast_remove(void *context, const char *group, const char *tag);
int32_t scez_toast_clear(void *context, const char *group);
int32_t scez_toast_count(void *context, int *count);
int scez_toast_icon_size(void);
int32_t scez_toast_sender_label(void *context, char *label, int capacity);
int32_t scez_toast_failures(void *context, int *count, int32_t *code);
void scez_toast_close(void *context);
int32_t scez_clipboard_copy(void *context, const char *code, uint32_t sequence);
int32_t scez_copy_feedback_show(void *context);
int scez_copy_feedback_tick(void *context);
void scez_copy_feedback_hide(void *context);
int scez_copy_feedback_state(void *context);
int32_t scez_toast_test_activate(void *context, const char *token);
int32_t scez_toast_test_external_activate(const char *app, const char *clsid, const char *token);
int32_t scez_toast_contains(void *context, const char *value, int *found);
int32_t scez_clipboard_test_roundtrip(void *context, int *tested);
void scez_notification_opened(uint64_t handle, char *token);
void scez_notification_activation_event(uint64_t handle, int source);
void scez_notification_dismissed(uint64_t handle, int reason);
int32_t scez_toast_test_open(void *context, const char *token);
int32_t scez_toast_test_event(void *context, const char *group, const char *tag, const char *args);
void scez_notification_clicked(uint64_t handle, char *token, uint32_t sequence);
#ifdef __cplusplus
}
#endif
#endif
