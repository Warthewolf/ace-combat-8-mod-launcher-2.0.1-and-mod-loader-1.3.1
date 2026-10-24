/* Load the REAL built main.dll into an isolated test host. Its normal PE scan,
 * thread freezing, code protection changes, hook installation and removal all
 * run against real Windows APIs. Machine-code fixtures below have the actual
 * verified game prologues, followed by ABI-recording bodies (no game needed).
 * UE4SS.dll beside this host is a test-only constructor/destructor stand-in. */
#include <windows.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <wchar.h>

typedef struct {
    void* inner;
    const wchar_t* path;
    uint8_t signedMode, parserFlag;
} Args;
extern void* fake_pak_ctor(Args*, void*, const wchar_t*, uint8_t, uint8_t);
extern void* fake_mount(void*, void*, const wchar_t*, int32_t, const void*, const void*);
extern uint8_t fake_decoy[], fake_sig[];
extern uint8_t fake_legacy_mount[];
static wchar_t legacyToc[1024];

__asm__(
    ".text\n.p2align 4\n.globl fake_pak_ctor\nfake_pak_ctor:\n"
    ".byte 0x48,0x89,0x5C,0x24,0x10,0x48,0x89,0x6C,0x24,0x18\n"
    ".byte 0x48,0x89,0x74,0x24,0x20,0x57,0x41,0x54,0x41,0x55\n"
    ".byte 0x41,0x56,0x41,0x57,0x48,0x83,0xEC,0x30,0x48,0x8D,0x05\n"
    ".long 0\n"
    ".byte 0x4C,0x8B,0xEA,0x48,0x89,0x01,0x4C,0x8B,0xF1,0x48,0x8D,0x05\n.long 0\n"
    ".byte 0x33,0xFF,0x48,0x89,0x41,0x10,0x49,0x8B,0xD0,0x89,0x79,0x08,0x41,0x0F,0xB6,0xF1\n"
    "movq %r13,0(%rcx)\nmovq %r8,8(%rcx)\nmovb %r9b,16(%rcx)\n"
    "movb 0x80(%rsp),%al\nmovb %al,17(%rcx)\nmovq %rcx,%rax\n"
    "addq $0x30,%rsp\npopq %r15\npopq %r14\npopq %r13\npopq %r12\npopq %rdi\n"
    "movq 0x10(%rsp),%rbx\nmovq 0x18(%rsp),%rbp\nmovq 0x20(%rsp),%rsi\nret\n"
    ".p2align 4\n.globl fake_mount\nfake_mount:\n"
    ".byte 0x40,0x55,0x53,0x56,0x57,0x41,0x54,0x41,0x55,0x41,0x57\n"
    ".byte 0x48,0x8D,0xAC,0x24,0xF0,0xFC,0xFF,0xFF\n"
    ".byte 0x48,0x81,0xEC,0x10,0x04,0x00,0x00,0x48,0x8B,0x05\n.long 0\n"
    "movq 0x470(%rsp),%rax\nmovq %rax,0x20(%rsp)\n"
    "movq 0x478(%rsp),%rax\nmovq %rax,0x28(%rsp)\ncall fake_mount_body\naddq $0x410,%rsp\n"
    "popq %r15\npopq %r13\npopq %r12\npopq %rdi\npopq %rsi\npopq %rbx\npopq %rbp\nret\n"
    ".p2align 4\n.globl fake_legacy_mount\nfake_legacy_mount:\n"
    ".byte 0x40,0x55,0x53,0x56,0x57,0x41,0x54,0x41,0x55,0x41,0x56,0x41,0x57\n"
    ".byte 0x48,0x8D,0xAC,0x24,0xC8,0xFC,0xFF,0xFF,0x48,0x81,0xEC,0x38,0x04,0,0\n"
    ".byte 0x48,0x8B,0x05\n.long 0\n"
    ".byte 0x48,0x33,0xC4,0x48,0x89,0x85,0x28,0x03,0,0\n"
    ".byte 0x48,0x8B,0x85,0xA8,0x03,0,0,0x48,0x8B,0xD9,0x48,0x89,0x4D,0xB8\n"
    ".byte 0x4D,0x8B,0xF1,0x48,0x8B,0x49,0x08,0x45,0x8B,0xF8\n"
    "movq %rdx,%r12\nmovq %r14,%r9\nmovq %rbx,%r14\nmovq %rbx,%rcx\nmovq %r12,%rdx\nmovl %r15d,%r8d\n"
    "movq 0x4A0(%rsp),%rax\nmovq %rax,0x20(%rsp)\n"
    "movq 0x4A8(%rsp),%rax\nmovq %rax,0x28(%rsp)\n"
    "leaq 0x100(%rsp),%rax\nmovq %rax,0x30(%rsp)\ncall fake_legacy_prepare\n"
    "testb %al,%al\njz fake_legacy_done\n"
    "movq 0xD0(%r14),%rcx\nleaq 0x100(%rsp),%rdx\nleaq legacyToc(%rip),%r8\nmovl %r15d,%r9d\n"
    "movq $0,0x20(%rsp)\nmovq $0,0x28(%rsp)\njmp fake_legacy_call\n"
    ".org fake_legacy_mount+0x857,0x90\nfake_legacy_call:\ncall fake_mount\n"
    "testq %rax,%rax\njz fake_legacy_done\ncmpl $0,(%rax)\nsete %al\n"
    "fake_legacy_done:\naddq $0x438,%rsp\n"
    "popq %r15\npopq %r14\npopq %r13\npopq %r12\npopq %rdi\npopq %rsi\npopq %rbx\npopq %rbp\nret\n"
    ".p2align 4\n.globl fake_sig\nfake_sig:\n"
    ".byte 0xE8,0x11,0x22,0x33,0x44,0x44,0x39,0x70,0x08,0x74,0x56\n"
    ".byte 0x48,0x8B,0x08,0x48,0x85,0xC9,0x74,0x4E,0x48,0x8B,0x01\n"
    ".byte 0xFF,0x50,0x30,0x84,0xC0,0x74,0x44,0x40,0xF6,0xC7,0x04,0x75,0x48\n"
    ".p2align 4\n.globl fake_decoy\nfake_decoy:\n.space 96,0xCC\n"
);

static int failures;
#define CHECK(cond) do { if (!(cond)) { printf("FAIL line %d: %s\n", __LINE__, #cond); ++failures; } } while (0)

static int checkArgs, batchCalls, nativeDupeCalls, failedRetryCalls, realMountCalls;
static void* expectedSelf;
static const void* expectedGuid;
static const void* expectedKey;
static const wchar_t* expectedPath;
static int32_t expectedOrder;
static HANDLE parallelEntered, parallelRelease, officialStarted;
typedef struct { void* data; int32_t num, max; } Array;
/* Live AC8 layout: the second vtable occupies +0x10; Filename starts at +0x18. */
typedef struct { uint8_t pad[24]; Array filename; } FakePak;
typedef struct { uint32_t order, pad; FakePak* pak; } PakEntry;
static _Alignas(16) uint8_t platformFile[0xE8];
static PakEntry pakEntries[32];
static FakePak pakFiles[32];
static int legacyCalls, pairCalls, failPairCalls, looseTocCalls;
static int32_t firstLegacyOrder, looseTocOrder;

/* This body models the observed distinction: a pure Pak can be in the table
 * while AL=0, and a failing paired UTOC does not undo that table insertion. */
uint8_t fake_legacy_prepare(void* self, const wchar_t* path, int32_t order,
    const wchar_t* mountPoint, uint8_t parserFlag, void* outRef, void* result)
{
    CHECK(self == platformFile);
    CHECK(mountPoint == NULL && parserFlag == 1 && outRef == NULL);
    memset(result, 0, 0x300);
    if (order >= 1000) {
        ++legacyCalls;
        if (!firstLegacyOrder) firstLegacyOrder = order;
    }
    if (wcsstr(path, L"Broken_P.pak")) return 0;
    Args args = {0};
    CHECK(fake_pak_ctor(&args, self, path, 1, parserFlag) == &args);
    CHECK(args.signedMode == (wcsstr(path, L"_P.pak") ? 0 : 1));
    Array* list = (Array*)(platformFile + 0x10);
    CHECK(list->num < 32);
    int i = list->num++;
    pakFiles[i].filename.data = _wcsdup(path);
    pakFiles[i].filename.num = pakFiles[i].filename.max = (int32_t)wcslen(path) + 1;
    pakEntries[i].order = order;
    pakEntries[i].pak = &pakFiles[i];
    wcscpy(legacyToc, path);
    wcscpy(wcsrchr(legacyToc, L'.'), L".utoc");
    DWORD attr = GetFileAttributesW(legacyToc);
    return wcsstr(path, L"pakchunk0-Windows.pak") != NULL || attr != INVALID_FILE_ATTRIBUTES;
}

/* Unlike a success-only stub, this models unsigned signature rejection and
 * unrelated parser errors. It also observes the actual game-code patch byte. */
void* fake_mount_body(void* self, void* out, const wchar_t* path, int32_t order,
                      const void* guid, const void* key)
{
    ++realMountCalls;
    int custom = path && wcslen(path) >= 7 && !_wcsicmp(path + wcslen(path) - 7, L"_P.utoc");
    int bypass = custom || order >= 1000;
    CHECK(fake_sig[0x1B] == (bypass ? 0xEB : 0x74));
    if (checkArgs) {
        CHECK(self == expectedSelf && path == expectedPath && order == expectedOrder);
        CHECK(guid == expectedGuid && key == expectedKey);
    }
    if (path && wcsstr(path, L"native_seen_P.utoc")) ++nativeDupeCalls;
    if (path && wcsstr(path, L"fail_retry_P.utoc")) ++failedRetryCalls;
    if (order >= 1000) ++batchCalls;
    if (path && wcsstr(path, L"Pair_P.utoc")) ++pairCalls;
    if (path && wcsstr(path, L"fail_pair_P.utoc")) ++failPairCalls;
    if (path && wcsstr(path, L"LooseToc.utoc")) { ++looseTocCalls; looseTocOrder = order; }
    if (path && (wcsstr(path, L"nested_P.utoc") || wcsstr(path, L"nested-stock.utoc"))) {
        uint8_t inner[0x300] = {0};
        const wchar_t* child = custom ? L"~mods/global.utoc" : L"~mods/child_P.utoc";
        CHECK(fake_mount(self, inner, child, 3, guid, key) == inner);
        CHECK(*(int32_t*)inner == 0);
        CHECK(fake_sig[0x1B] == (bypass ? 0xEB : 0x74));
    }
    if (path && wcsstr(path, L"parallel_P.utoc")) {
        SetEvent(parallelEntered);
        CHECK(WaitForSingleObject(parallelRelease, 5000) == WAIT_OBJECT_0);
        CHECK(fake_sig[0x1B] == 0xEB);
    }
    if (path && wcsstr(path, L"null_P.utoc")) return NULL;
    int32_t code = path && wcsstr(path, L"fail") ? 23 : 0;
    /* A loose unsigned container is rejected on an unmodified native path. */
    if (path && wcsstr(path, L"unsigned") && fake_sig[0x1B] != 0xEB) code = 12;
    memcpy(out, &code, 4);
    return out;
}

static void check_mount(const wchar_t* path, int32_t expected)
{
    uint8_t result[0x300] = {0};
    int self = 0, guid[4] = {1,2,3,4}, key[8] = {5,6,7,8};
    expectedSelf = &self; expectedGuid = guid; expectedKey = key;
    expectedPath = path; expectedOrder = 37; checkArgs = 1;
    void* r = fake_mount(&self, result, path, 37, guid, key);
    checkArgs = 0;
    CHECK(r == (expected < 0 ? NULL : result));
    if (r) CHECK(*(int32_t*)r == expected);
    CHECK(fake_sig[0x1B] == 0x74);
}

static DWORD WINAPI parallel_mount(void* arg)
{
    uint8_t result[0x300] = {0};
    const wchar_t* path = arg ? L"~mods/official.utoc" : L"~mods/parallel_P.utoc";
    if (arg) SetEvent(officialStarted);
    CHECK(fake_mount(NULL, result, path, 3, NULL, NULL) == result);
    CHECK(*(int32_t*)result == 0);
    return 0;
}

static void fixture_file(const wchar_t* name)
{
    HANDLE h = CreateFileW(name, GENERIC_WRITE, 0, NULL, CREATE_NEW, FILE_ATTRIBUTE_NORMAL, NULL);
    CHECK(h != INVALID_HANDLE_VALUE);
    if (h != INVALID_HANDLE_VALUE) CloseHandle(h);
}

static void check_utoc_policy(void)
{
    check_mount(L"~mods/unsigned_P.utoc", 0);
    check_mount(L"~mods/UNSIGNED_p.UTOC", 0);
    check_mount(L"~mods/unsigned.utoc", 12);
    check_mount(L"~mods/unsigned_P.utoc.sig", 12);
    check_mount(L"~mods/fail_P.utoc", 23);
    check_mount(L"~mods/null_P.utoc", -1);
    check_mount(NULL, 0);
    check_mount(L"", 0);
    uint8_t result[0x300] = {0};
    CHECK(fake_mount(NULL, result, L"~mods/nested_P.utoc", 3, NULL, NULL) == result);
    CHECK(fake_sig[0x1B] == 0x74);
    CHECK(fake_mount(NULL, result, L"~mods/nested-stock.utoc", 3, NULL, NULL) == result);
    CHECK(fake_sig[0x1B] == 0x74);

    parallelEntered = CreateEventW(NULL, TRUE, FALSE, NULL);
    parallelRelease = CreateEventW(NULL, TRUE, FALSE, NULL);
    officialStarted = CreateEventW(NULL, TRUE, FALSE, NULL);
    HANDLE threads[2];
    threads[0] = CreateThread(NULL, 0, parallel_mount, NULL, 0, NULL);
    CHECK(WaitForSingleObject(parallelEntered, 5000) == WAIT_OBJECT_0);
    threads[1] = CreateThread(NULL, 0, parallel_mount, (void*)1, 0, NULL);
    CHECK(WaitForSingleObject(officialStarted, 5000) == WAIT_OBJECT_0);
    /* The stock call is blocked while the unsigned call owns the policy. */
    CHECK(WaitForSingleObject(threads[1], 40) == WAIT_TIMEOUT);
    SetEvent(parallelRelease);
    CHECK(WaitForMultipleObjects(2, threads, TRUE, 5000) == WAIT_OBJECT_0);
    CloseHandle(threads[0]); CloseHandle(threads[1]);
    CloseHandle(parallelEntered); CloseHandle(parallelRelease); CloseHandle(officialStarted);
    CHECK(fake_sig[0x1B] == 0x74);

    /* The actual DLL scanner finds these under its mod root (..). A native
     * success is skipped in the plan; arbitrary mod names still get bypass. */
    wchar_t fixtureDir[128], paths[6][256];
    const wchar_t* names[] = {L"native_seen_P.utoc", L"native_seen_P.ucas",
        L"unsigned_anyname.utoc", L"unsigned_anyname.ucas", L"fail_retry_P.utoc", L"fail_retry_P.ucas"};
    swprintf(fixtureDir, 128, L"../UtocPolicyFixture_%lu", GetCurrentProcessId());
    int created = CreateDirectoryW(fixtureDir, NULL);
    CHECK(created);
    if (!created) return;
    for (int i = 0; i < 6; ++i) {
        swprintf(paths[i], 256, L"%ls/%ls", fixtureDir, names[i]);
        fixture_file(paths[i]);
    }
    CHECK(fake_mount(NULL, result, L"~mods/native_seen_P.utoc", 3, NULL, NULL) == result);
    CHECK(fake_mount(NULL, result, L"~mods/fail_retry_P.utoc", 3, NULL, NULL) == result);
    CHECK(*(int32_t*)result == 23);
    CHECK(fake_mount(NULL, result, L"../../../Game/Content/Paks/pakchunk0-Windows.utoc", 3, NULL, NULL) == result);
    CHECK(batchCalls == 2);
    CHECK(nativeDupeCalls == 1);
    CHECK(failedRetryCalls == 2);
    CHECK(fake_sig[0x1B] == 0x74);
    for (int i = 0; i < 6; ++i) CHECK(DeleteFileW(paths[i]));
    CHECK(RemoveDirectoryW(fixtureDir));
}

static void patch(void* at, const void* bytes, size_t n)
{
    DWORD old, ignored;
    if (!VirtualProtect(at, n, PAGE_EXECUTE_READWRITE, &old)) ExitProcess(2);
    memcpy(at, bytes, n);
    FlushInstructionCache(GetCurrentProcess(), at, n);
    if (!VirtualProtect(at, n, old, &ignored)) ExitProcess(2);
}

static void check_pak_plan(int withObject, int recoverObject)
{
    wchar_t dir[128], nested[160], backup[160], paths[24][256];
    wchar_t loose[128], off[128], text[512];
    const wchar_t* names[] = {
        L"Only_P.pak", L"Pair_P.pak", L"Pair_P.utoc", L"Pair_P.ucas",
        L"Missing_P.pak", L"Missing_P.utoc", L"Broken_P.pak",
        L"fail_pair_P.pak", L"fail_pair_P.utoc", L"fail_pair_P.ucas",
        L"Seen_P.pak", L"LooseToc.utoc", L"LooseToc.ucas", L"ordinary.pak",
        L"Nested/Deep_P.pak", L"backup/Backup_P.pak"
    };
    swprintf(dir, 128, L"../PakPlanFixture_%lu", GetCurrentProcessId());
    swprintf(nested, 160, L"%ls/Nested", dir);
    swprintf(backup, 160, L"%ls/backup", dir);
    CHECK(CreateDirectoryW(dir, NULL));
    CHECK(CreateDirectoryW(nested, NULL));
    CHECK(CreateDirectoryW(backup, NULL));
    size_t count = sizeof(names) / sizeof(names[0]);
    for (size_t i = 0; i < count; ++i) {
        swprintf(paths[i], 256, L"%ls/%ls", dir, names[i]);
        fixture_file(paths[i]);
    }
    swprintf(loose, 128, L"Loose_%lu_P", GetCurrentProcessId());
    swprintf(off, 128, L"Off_%lu_P", GetCurrentProcessId());
    const wchar_t* exts[] = {L"pak", L"utoc", L"ucas"};
    for (int i = 0; i < 6; ++i) {
        swprintf(paths[count + i], 256, L"../%ls.%ls", i < 3 ? loose : off, exts[i % 3]);
        fixture_file(paths[count + i]);
    }
    /* Loose Pak owns its UTOC as one mod. Its disable switch must cover both. */
    HANDLE orderFile = CreateFileW(L"../loadorder.txt", GENERIC_WRITE, 0, NULL,
        CREATE_NEW, FILE_ATTRIBUTE_NORMAL, NULL);
    CHECK(orderFile != INVALID_HANDLE_VALUE);
    if (orderFile == INVALID_HANDLE_VALUE) return;
    swprintf(text, 512, L"-%ls.pak\r\n+%ls.pak\r\n+PakPlanFixture_%lu\r\n", off, loose, GetCurrentProcessId());
    const uint16_t bom = 0xFEFF;
    DWORD written;
    CHECK(WriteFile(orderFile, &bom, 2, &written, NULL));
    CHECK(WriteFile(orderFile, text, (DWORD)wcslen(text) * 2, &written, NULL));
    CloseHandle(orderFile);

    Array* list = (Array*)(platformFile + 0x10);
    list->data = pakEntries; list->max = 32;
    *(void**)(platformFile + 8) = platformFile;
    *(void**)(platformFile + 0xD0) = platformFile;
    InitializeCriticalSection((CRITICAL_SECTION*)(platformFile + 0x38));
    typedef uint8_t (*Legacy)(void*, const wchar_t*, int32_t, const wchar_t*, uint8_t, void*);
    Legacy mountPak = (Legacy)(uintptr_t)fake_legacy_mount;
    if (withObject || recoverObject) {
        CHECK(mountPak(platformFile, paths[10], 3, NULL, 1, NULL) == 0);
        CHECK(list->num == 1); /* pure native Pak: AL=0 but it was inserted */
    }
    if (!withObject) {
        uint8_t result[0x300] = {0};
        CHECK(fake_mount(NULL, result, L"../../../Game/Content/Paks/pakchunk0-Windows.utoc", 3, NULL, NULL) == result);
        CHECK(legacyCalls == 0 && pairCalls == 0 && failPairCalls == 0);
    }
    if (withObject || recoverObject) {
        CHECK(mountPak(platformFile, L"../../../Game/Content/Paks/pakchunk0-Windows.pak", 3, NULL, 1, NULL) == 1);
        CHECK(legacyCalls == 6); /* loose, broken, failing pair, only, pair, deep */
        CHECK(pairCalls == 1 && failPairCalls == 1);
        CHECK(firstLegacyOrder == 1000);
        CHECK(looseTocOrder > firstLegacyOrder);
        CHECK(list->num == 7); /* seen + base + 5 inserted active Paks */
    }
    CHECK(looseTocCalls == 1);
    CHECK(fake_sig[0x1B] == 0x74);
    CHECK(DeleteFileW(L"../loadorder.txt"));
    for (size_t i = 0; i < count + 6; ++i) CHECK(DeleteFileW(paths[i]));
    CHECK(RemoveDirectoryW(nested)); CHECK(RemoveDirectoryW(backup)); CHECK(RemoveDirectoryW(dir));
    for (int i = 0; i < list->num; ++i) free(pakFiles[i].filename.data);
    DeleteCriticalSection((CRITICAL_SECTION*)(platformFile + 0x38));
}

static void check_call(const wchar_t* path, uint8_t signedMode, uint8_t expected, uint8_t flag)
{
    Args out = {0};
    int inner;
    CHECK(fake_pak_ctor(&out, &inner, path, signedMode, flag) == &out);
    CHECK(out.inner == &inner);
    CHECK(out.path == path);
    CHECK(out.signedMode == expected);
    CHECK(out.parserFlag == flag);
}

int main(int argc, char** argv)
{
    const char* mode = argc > 1 ? argv[1] : "normal";
    uint8_t* ctor = (uint8_t*)(uintptr_t)fake_pak_ctor;
    uint8_t* mount = (uint8_t*)(uintptr_t)fake_mount;
    uint8_t savedCtor[128], savedMount[32], beforeCtor[128];
    memcpy(savedCtor, ctor, sizeof(savedCtor));
    memcpy(savedMount, mount, sizeof(savedMount));
    int pakPlan = !strncmp(mode, "pak-", 4);
    int refuse = !pakPlan && strcmp(mode, "normal") != 0 && strcmp(mode, "prefix-decoy") != 0;
    if (!strcmp(mode, "missing")) {
        uint8_t bad = 0x11;
        patch(ctor + 4, &bad, 1);
    } else if (!strcmp(mode, "ambiguous")) {
        patch(fake_decoy, ctor, 67);
    } else if (!strcmp(mode, "prefix-decoy")) {
        /* Three real game functions share these first 31 bytes. */
        patch(fake_decoy, ctor, 31);
    } else if (!strcmp(mode, "other-hook")) {
        uint8_t jump[14] = {0xFF,0x25,0,0,0,0};
        uintptr_t target = (uintptr_t)fake_decoy;
        memcpy(jump + 6, &target, 8);
        patch(ctor, jump, 14);
    } else if (!strcmp(mode, "other-short-hook")) {
        uint8_t jump[5] = {0xE9};
        int32_t rel = (int32_t)(fake_decoy - (ctor + 5));
        memcpy(jump + 1, &rel, 4);
        patch(ctor, jump, 5);
    } else if (!strcmp(mode, "pak-no-entry")) {
        uint8_t bad = 0xCC;
        patch(fake_legacy_mount, &bad, 1);
    } else if (!strcmp(mode, "pak-ambiguous-entry")) {
        patch(fake_decoy, fake_legacy_mount, 69);
    } else if (!strcmp(mode, "pak-bad-call")) {
        uint8_t bad = 0x90;
        patch(fake_legacy_mount + 0x857, &bad, 1);
    } else if (refuse) {
        return 2;
    }
    memcpy(beforeCtor, ctor, sizeof(beforeCtor));
    HMODULE loader = LoadLibraryW(L"main.dll");
    if (!loader) { printf("LoadLibrary failed: %lu\n", GetLastError()); return 2; }
    typedef void* (*Start)(void);
    typedef void (*Stop)(void*);
    Start start = (Start)(uintptr_t)GetProcAddress(loader, "start_mod");
    Stop stop = (Stop)(uintptr_t)GetProcAddress(loader, "uninstall_mod");
    if (!start || !stop) { puts("Loader exports missing"); return 2; }
    void* mod = start();
    CHECK(mod != NULL);
    if (refuse) {
        CHECK(memcmp(ctor, beforeCtor, sizeof(beforeCtor)) == 0);
        CHECK(memcmp(mount, savedMount, sizeof(savedMount)) == 0);
        stop(mod);
        CHECK(memcmp(ctor, beforeCtor, sizeof(beforeCtor)) == 0);
    } else {
        CHECK(memcmp(ctor, savedCtor, 14) != 0);
        CHECK(memcmp(ctor + 14, savedCtor + 14, sizeof(savedCtor) - 14) == 0);
        CHECK(memcmp(mount, savedMount, 14) != 0);
        CHECK(memcmp(mount + 14, savedMount + 14, sizeof(savedMount) - 14) == 0);
        check_call(L"../../../Game/Content/Paks/pakchunk20optional-Windows.pak", 1, 1, 0xA5);
        check_call(L"../../../Game/Content/Paks/~mods/ui_P.pak", 1, 0, 0x5A);
        check_call(L"C:\\mods\\UI_p.PAK", 0x7F, 0, 0xFE);
        check_call(L"ui_P.pak", 0, 0, 0x91);
        check_call(L"~mods/custom.pak", 0x7F, 0x7F, 0x11);
        check_call(L"ui_P.pak.sig", 1, 1, 0x22);
        check_call(NULL, 1, 1, 0x33);
        check_call(L"", 1, 1, 0x44);
        /* Call the hooked IoStore trampoline too, without triggering MountAll. */
        uint8_t result[0x300] = {0};
        CHECK(fake_mount(NULL, result, L"../../../Game/Content/Paks/global.utoc", 3, NULL, NULL) == result);
        CHECK(*(int32_t*)result == 0);
        if (pakPlan) check_pak_plan(!strcmp(mode, "pak-plan"), !strcmp(mode, "pak-deferred"));
        else check_utoc_policy();
        /* An unexpected third-party byte must never be overwritten or called
         * through. Restore our fixture explicitly before continuing. */
        int beforeCalls = realMountCalls;
        uint8_t badBranch = 0x90, goodBranch = 0x74;
        patch(fake_sig + 0x1B, &badBranch, 1);
        CHECK(fake_mount(NULL, result, L"~mods/unsigned_P.utoc", 3, NULL, NULL) == NULL);
        CHECK(realMountCalls == beforeCalls);
        CHECK(fake_sig[0x1B] == 0x90);
        patch(fake_sig + 0x1B, &goodBranch, 1);
        /* Duplicate UE4SS start must not overwrite either trampoline. */
        void* second = start();
        CHECK(second != NULL);
        stop(mod);
        check_call(L"second_P.pak", 1, 0, 0x55);
        stop(second);
        CHECK(memcmp(ctor, savedCtor, sizeof(savedCtor)) == 0);
        CHECK(memcmp(mount, savedMount, sizeof(savedMount)) == 0);
        check_call(L"unloaded_P.pak", 1, 1, 0x66);
        /* The pinned DLL can install both hooks again after UE4SS restart. */
        mod = start();
        check_call(L"restart_P.pak", 1, 0, 0x77);
        stop(mod);
        CHECK(memcmp(ctor, savedCtor, sizeof(savedCtor)) == 0);
        CHECK(memcmp(mount, savedMount, sizeof(savedMount)) == 0);
    }
    CHECK(fake_sig[0x1B] == 0x74);
    printf("%s: %s\n", mode, failures ? "FAIL" : "PASS");
    return failures ? 1 : 0;
}
