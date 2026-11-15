#include "common.h"
#include <windows.h>
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "clipboard_sync.h"
#include "control_msg.h"
#include "device_msg.h"
#include "util/binary.h"

static wchar_t desktop_name[128];
static HWND seed_window;

static void
test_encoded_image(const char *name, const char *mime, uint64_t revision,
                   bool transparent) {
    char path[1024];
    const char *root = getenv("SCEZ_TEST_SOURCE");
    assert(root);
    snprintf(path, sizeof(path), "%s/app/tests/data/%s", root, name);
    FILE *f = fopen(path, "rb");
    assert(f && !fseek(f, 0, SEEK_END));
    long size = ftell(f);
    assert(size > 0 && !fseek(f, 0, SEEK_SET));
    uint8_t *bytes = malloc(size);
    assert(bytes && fread(bytes, 1, size, f) == (size_t) size);
    fclose(f);
    assert(sc_clipboard_sync_receive_image(mime, bytes, size, revision,
                                           GetClipboardSequenceNumber()));
    assert(OpenClipboard(seed_window));
    HANDLE h = GetClipboardData(CF_DIBV5);
    BITMAPV5HEADER *header = GlobalLock(h);
    assert(header && header->bV5Width == 2 && header->bV5Height == -1);
    const uint8_t *pixels = (const uint8_t *) header + header->bV5Size;
    assert(pixels[2] > 240 && pixels[4] > 240);
    assert(pixels[3] == (transparent ? 128 : 255) && pixels[7] == 255);
    GlobalUnlock(h);
    CloseClipboard();
    uint8_t *original;
    size_t original_size;
    char *original_mime;
    bool oversized = false;
    assert(!sc_clipboard_sync_get_image(&original, &original_size, &original_mime,
                                       (size_t) size - 1, &oversized));
    assert(oversized);
    oversized = false;
    assert(sc_clipboard_sync_get_image(&original, &original_size, &original_mime,
                                      (size_t) size, &oversized));
    assert(!oversized);
    assert(original_size == (size_t) size && !memcmp(original, bytes, size));
    assert(!strcmp(original_mime, mime));
    free(original);
    free(original_mime);
    free(bytes);
}

static void
seed_text(const wchar_t *text) {
    assert(OpenClipboard(seed_window));
    assert(EmptyClipboard());
    size_t size = (wcslen(text) + 1) * sizeof(*text);
    HGLOBAL h = GlobalAlloc(GMEM_MOVEABLE, size);
    assert(h);
    void *p = GlobalLock(h);
    assert(p);
    memcpy(p, text, size);
    GlobalUnlock(h);
    assert(SetClipboardData(CF_UNICODETEXT, h));
    assert(CloseClipboard());
}

static DWORD
child(const char *mode, bool wait) {
    char executable[MAX_PATH], command[MAX_PATH + 80];
    GetModuleFileNameA(NULL, executable, sizeof(executable));
    snprintf(command, sizeof(command), "\"%s\" %s", executable, mode);
    STARTUPINFOA si = {.cb = sizeof(si)};
    char desktop[256];
    WideCharToMultiByte(CP_UTF8, 0, desktop_name, -1, desktop, sizeof(desktop), NULL, NULL);
    si.lpDesktop = desktop;
    si.dwFlags = STARTF_USESHOWWINDOW;
    si.wShowWindow = SW_HIDE;
    PROCESS_INFORMATION pi;
    assert(CreateProcessA(NULL, command, NULL, NULL, FALSE, 0, NULL, NULL, &si, &pi));
    if (wait) {
        assert(WaitForSingleObject(pi.hProcess, 10000) == WAIT_OBJECT_0);
        DWORD code;
        assert(GetExitCodeProcess(pi.hProcess, &code));
        assert(code == 0);
    }
    DWORD id = pi.dwProcessId;
    CloseHandle(pi.hThread);
    CloseHandle(pi.hProcess);
    return id;
}

int
main(int argc, char **argv) {
    bool use_user_clipboard = argc > 1 && !strcmp(argv[1], "--user-clipboard");
    if (argc > 1 && !use_user_clipboard) {
        assert(sc_clipboard_sync_init("CLIPBOARD_TEST_DEVICE"));
        uint64_t tx;
        bool claimed = sc_clipboard_sync_begin(GetClipboardSequenceNumber(), &tx);
        if (!strcmp(argv[1], "busy")) { assert(!claimed); }
        else {
            assert(claimed);
            if (!strcmp(argv[1], "complete")) { assert(sc_clipboard_sync_ack(tx)); }
            else if (!strcmp(argv[1], "abandon")) { ExitProcess(0); }
        }
        sc_clipboard_sync_destroy();
        return 0;
    }
    // A private window station has a separate system clipboard. These tests
    // never read, overwrite or restore the user's clipboard.
    wchar_t station[96];
    swprintf(station, 96, L"SCEZ_CLIPBOARD_TEST_%lu", GetCurrentProcessId());
    HWINSTA original = GetProcessWindowStation();
    HWINSTA isolated = use_user_clipboard ? original : CreateWindowStationW(station, 0, WINSTA_ALL_ACCESS, NULL);
    if (!isolated) { fprintf(stderr, "CreateWindowStation error=%lu\n", GetLastError()); }
    bool switched = use_user_clipboard || (isolated && SetProcessWindowStation(isolated));
    if (!switched) { fprintf(stderr, "SetProcessWindowStation error=%lu\n", GetLastError()); }
    assert(switched);
    HDESK desktop = NULL;
    if (!use_user_clipboard) {
        desktop = CreateDesktopW(L"Default", NULL, NULL, 0, GENERIC_ALL, NULL);
        assert(desktop && SetThreadDesktop(desktop));
    } else {
        DWORD needed;
        assert(GetUserObjectInformationW(original, UOI_NAME, station, sizeof(station), &needed));
    }
    swprintf(desktop_name, 128, L"%ls\\Default", station);
    seed_window = CreateWindowExW(0, L"STATIC", L"fixture", 0, 0, 0, 0, 0, HWND_MESSAGE, NULL, NULL, NULL);
    assert(seed_window && sc_clipboard_sync_init("CLIPBOARD_TEST_DEVICE"));

    seed_text(L"synthetic computer copy");
    DWORD seq = GetClipboardSequenceNumber();
    uint64_t tx;
    assert(sc_clipboard_sync_begin(seq, &tx));
    child("busy", true); // one transfer, despite multiple casting processes
    sc_clipboard_sync_disconnected();
    assert(sc_clipboard_sync_needs_recovery(seq));
    child("complete", true);
    assert(!sc_clipboard_sync_begin(seq, &tx));

    seed_text(L"next synthetic copy");
    child("abandon", true); // abrupt death: no cleanup notification
    assert(sc_clipboard_sync_begin(GetClipboardSequenceNumber(), &tx));
    assert(sc_clipboard_sync_ack(tx));

    struct sc_control_msg text = {.type = SC_CONTROL_MSG_TYPE_SET_CLIPBOARD};
    text.set_clipboard.text = "versioned fixture";
    text.clipboard_pc_sequence = GetClipboardSequenceNumber();
    assert(sc_clipboard_sync_stamp(&text));
    assert(text.type == SC_CONTROL_MSG_TYPE_SET_CLIPBOARD_VERSIONED);
    size_t wire_size;
    uint8_t *wire = sc_control_msg_serialize(&text, &wire_size);
    assert(wire && wire_size == 30 + strlen(text.set_clipboard.text));
    assert(wire[0] == SC_CONTROL_MSG_TYPE_SET_CLIPBOARD_VERSIONED);
    assert(sc_read64be(wire + 1) == text.clipboard_epoch);
    assert(sc_read64be(wire + 9) == text.clipboard_version);
    free(wire);
    struct sc_control_msg old = {.type = SC_CONTROL_MSG_TYPE_SET_CLIPBOARD};
    old.set_clipboard.text = "obsolete fixture";
    old.clipboard_pc_sequence = GetClipboardSequenceNumber();
    seed_text(L"new local copy during a snapshot read");
    assert(!sc_clipboard_sync_stamp(&old));

    // Two pixels, red and blue, in an uncompressed 24-bit BMP.
    uint8_t bmp[62] = { 'B', 'M', 62 };
    bmp[10] = 54;
    bmp[14] = 40;
    bmp[18] = 2;
    bmp[22] = 1;
    bmp[26] = 1;
    bmp[28] = 24;
    bmp[34] = 8;
    bmp[56] = 255;
    bmp[57] = 255;
    seq = GetClipboardSequenceNumber();
    assert(sc_clipboard_sync_receive_image("image/bmp", bmp, sizeof(bmp), 100, seq));
    assert(IsClipboardFormatAvailable(CF_DIBV5));
    assert(IsClipboardFormatAvailable(CF_DIB)); // Windows synthesizes the legacy format
    assert(OpenClipboard(seed_window));
    HGLOBAL dib = GetClipboardData(CF_DIBV5);
    BITMAPV5HEADER *header = GlobalLock(dib);
    assert(header && header->bV5Width == 2 && header->bV5Height == -1);
    const uint8_t *pixels = (const uint8_t *) header + header->bV5Size;
    assert(pixels[0] == 0 && pixels[1] == 0 && pixels[2] == 255 && pixels[3] == 255);
    assert(pixels[4] == 255 && pixels[5] == 0 && pixels[6] == 0 && pixels[7] == 255);
    GlobalUnlock(dib);
    CloseClipboard();
    uint8_t *image;
    size_t size;
    char *mime;
    bool oversized = false;
    assert(sc_clipboard_sync_get_image(&image, &size, &mime, 0, &oversized));
    assert(size == sizeof(bmp) && !memcmp(image, bmp, size) && !strcmp(mime, "image/bmp"));
    free(image);
    free(mime);
    seq = GetClipboardSequenceNumber();
    assert(sc_clipboard_sync_receive_image("image/bmp", bmp, sizeof(bmp), 100, seq));
    assert(GetClipboardSequenceNumber() == seq); // duplicate receiver never rewrites it
    assert(!sc_clipboard_sync_begin(seq, &tx)); // provenance works across sessions

    seed_text(L"a newer computer copy wins");
    assert(!sc_clipboard_sync_receive_image("image/bmp", bmp, sizeof(bmp), 101, seq));
    seq = GetClipboardSequenceNumber();
    assert(sc_clipboard_sync_receive_image("image/bmp", bmp, sizeof(bmp), 102, seq));
    bmp[56] = 120;
    assert(!sc_clipboard_sync_receive_image("image/bmp", bmp, sizeof(bmp), 101, GetClipboardSequenceNumber()));
    seed_text(L"same text\r\nwith rich formats");
    assert(OpenClipboard(seed_window));
    HGLOBAL html = GlobalAlloc(GMEM_MOVEABLE, 1);
    assert(html && SetClipboardData(RegisterClipboardFormatW(L"HTML Format"), html));
    CloseClipboard();
    seq = GetClipboardSequenceNumber();
    assert(sc_clipboard_sync_receive_text("same text\nwith rich formats", 103, seq));
    assert(GetClipboardSequenceNumber() == seq);
    assert(IsClipboardFormatAvailable(RegisterClipboardFormatW(L"HTML Format")));
    const uint8_t invalid[] = {0, 1, 2, 3};
    assert(!sc_clipboard_sync_receive_image("image/png", invalid, sizeof(invalid), 104, seq));
    assert(GetClipboardSequenceNumber() == seq); // decode failure preserves data
    test_encoded_image("clipboard_2x1.png", "image/png", 110, true);
    assert(IsClipboardFormatAvailable(RegisterClipboardFormatW(L"PNG")));
    test_encoded_image("clipboard_2x1.jpg", "image/jpeg", 111, false);
    sc_clipboard_sync_destroy();
    assert(IsClipboardFormatAvailable(CF_DIBV5)); // survives receiver/window exit
    assert(sc_clipboard_sync_init("CLIPBOARD_TEST_DEVICE"));
    assert(sc_clipboard_sync_get_image(&image, &size, &mime, 0, &oversized));
    free(image);
    free(mime);
    assert(!sc_clipboard_sync_begin(GetClipboardSequenceNumber(), &tx));
    sc_clipboard_sync_destroy();
    DestroyWindow(seed_window);
    SetProcessWindowStation(original);
    if (desktop) { CloseDesktop(desktop); }
    if (!use_user_clipboard) { CloseWindowStation(isolated); }
    puts("PASS: Windows clipboard, process coordination, failover, versions, lossless pixels, paste formats and exit persistence");
    return 0;
}
