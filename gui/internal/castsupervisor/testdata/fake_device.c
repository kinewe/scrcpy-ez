#include <windows.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static void record(const char *env, const char *text) {
    const char *path = getenv(env);
    if (!path) return;
    HANDLE h = CreateFileA(path, FILE_APPEND_DATA, FILE_SHARE_READ | FILE_SHARE_WRITE,
                          NULL, OPEN_ALWAYS, FILE_ATTRIBUTE_NORMAL, NULL);
    if (h != INVALID_HANDLE_VALUE) {
        DWORD n; WriteFile(h, text, (DWORD) strlen(text), &n, NULL); CloseHandle(h);
    }
}

int main(int argc, char **argv) {
    if (strstr(argv[0], "adb.exe")) {
        char line[2048] = "";
        for (int i = 1; i < argc; ++i) { strcat(line, argv[i]); strcat(line, " "); }
        strcat(line, "\n"); record("SCEZ_TEST_ADB_LOG", line);
        const char *id = "PHONE_A";
        if (argc > 2 && (!strcmp(argv[2], "USB_B") || strstr(argv[2], ".99:"))) id = "PHONE_B";
        if (strstr(line, "shell -T sh") || strstr(line, "shell -T su -c sh")) {
            char script[16384] = "";
            fread(script, 1, sizeof(script)-1, stdin);
            const char *fixed = getenv("SCEZ_TEST_ROOT_FIXED");
            if (strstr(line, "shell -T su -c sh")) {
                if (strstr(script, "YINMO_ROOT_AUTH=ok")) {
                    record("SCEZ_TEST_ADB_LOG", "ROOT_AUTH\n");
                    if (getenv("SCEZ_TEST_ROOT_DENY")) { puts("Permission denied"); return 1; }
                    if (getenv("SCEZ_TEST_ROOT_DELAY")) Sleep(atoi(getenv("SCEZ_TEST_ROOT_DELAY")));
                    puts("YINMO_ROOT_AUTH=ok");
                    puts("YINMO_ROOT_DIR_META=2000:2000:771");
                    puts("YINMO_ROOT_DIR_LABEL=u:object_r:system_data_file:s0 /data/local/tmp");
                } else if (strstr(script, "restorecon -F")) {
                    record("SCEZ_TEST_ADB_LOG", "ROOT_RESTORE\n");
                    if (fixed) { FILE *file = fopen(fixed, "w"); if (file) { fputs("fixed", file); fclose(file); } }
                }
            } else if (strstr(script, "YINMO_ROOT_WRITABLE")) {
                puts("YINMO_ROOT_FINGERPRINT=123:456");
                puts("YINMO_ROOT_DIR_META=2000:2000:771");
                puts("YINMO_ROOT_UID=2000");
                puts(fixed && GetFileAttributesA(fixed) == INVALID_FILE_ATTRIBUTES ? "YINMO_ROOT_WRITABLE=no" : "YINMO_ROOT_WRITABLE=yes");
            } else if (strstr(script, "YINMO_ROOT_PROBE=ok")) puts("YINMO_ROOT_PROBE=ok");
        } else if (strstr(line, "ro.serialno")) {
            if (argc > 2 && !strcmp(argv[2], "USB_A") && getenv("SCEZ_TEST_ID_DELAY")) Sleep(400);
            puts(id);
        } else if (strstr(line, "ro.product.marketname")) puts("SAME_MODEL");
        else if (strstr(line, "ro.product.model")) puts("SAME_MODEL");
        else if (strstr(line, "ro.build.version.sdk")) puts("34");
        else if (strstr(line, "service.adb.tcp.port")) puts("5555");
        else if (strstr(line, "wm size")) puts("Physical size: 1080x2400");
        else if (strstr(line, "peak_refresh_rate")) puts("120");
        else if (strstr(line, "ip -o")) puts("4: wlan0 inet 192.0.2.1/24 scope global wlan0");
        return 0;
    }
    const char *serial = argc > 2 ? argv[2] : "?";
    char line[512];
    snprintf(line, sizeof(line), "START %s %lu\n", serial, GetCurrentProcessId());
    record("SCEZ_TEST_CAST_LOG", line);
    const char *forced = getenv("SCEZ_TEST_CLIENT_RC");
    if (forced) { record("SCEZ_TEST_CAST_LOG", "EXIT failure\n"); return atoi(forced); }
    const char *stop_name = getenv("SCEZ_EVENT_STOP_NAME");
    const char *route_name = getenv("SCEZ_EVENT_SWITCH_NAME");
    if (!stop_name || !route_name) return 1;
    HANDLE stop = OpenEventA(SYNCHRONIZE, FALSE, stop_name);
    HANDLE route = OpenEventA(SYNCHRONIZE, FALSE, route_name);
    char user_name[256]; snprintf(user_name, sizeof(user_name), "Local\\SCEZ_TEST_USER_%s", getenv("SCEZ_WATCH_TAG"));
    HANDLE user = CreateEventA(NULL, TRUE, FALSE, user_name);
    if (!stop || !route || !user) return 1;
    puts("SCRCPY_EZ_READY"); fflush(stdout);
    HANDLE handles[] = {stop, user, route};
    DWORD r = WaitForMultipleObjects(3, handles, FALSE, INFINITE);
    int code = r == WAIT_OBJECT_0 + 2 ? 3 : 0;
    if (code == 3) { puts("SCRCPY_EZ_ROUTE_SWITCH"); record("SCEZ_TEST_CAST_LOG", "EXIT switch\n"); }
    else { puts("SCRCPY_EZ_USER_CLOSE"); record("SCEZ_TEST_CAST_LOG", "EXIT user\n"); }
    fflush(stdout); CloseHandle(user); CloseHandle(route); CloseHandle(stop);
    return code;
}
