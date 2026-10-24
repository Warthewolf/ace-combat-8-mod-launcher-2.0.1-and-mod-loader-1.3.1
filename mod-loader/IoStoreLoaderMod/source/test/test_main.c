/* Self-test: runs the real loader.c against a simulated game.
 *
 * The "game" is a block of memory laid out like a loaded Windows .exe, with a
 * mount function made of real x64 machine code that starts with exactly the
 * bytes the loader looks for, plus the signature-check byte pattern. The loader
 * finds both, hooks the function through its trampoline, and we then call the
 * function the way the game would and record what it ends up being asked to
 * mount. Each scenario runs in its own process so loader state starts clean.  */
#include "../src/win32_min.h"
#include "shim.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <time.h>
#include <fcntl.h>
#include <unistd.h>

void* start_mod(void);
void  uninstall_mod(void* mod);
BOOL WINAPI DllMain(HANDLE module, DWORD reason, void* reserved);

typedef void* (WINAPI *MountFn)(void* self, void* out, const wchar_t* path, int32_t order,
                                const void* guid, const void* key);

#define CHECK(cond, ...) do { if (!(cond)) { \
    printf("    FAIL line %d: %s  -- ", __LINE__, #cond); printf(__VA_ARGS__); printf("\n"); \
    failures++; } } while (0)

static int failures;

/* ---- recorded calls into the fake game mount ------------------------------- */
typedef struct {
    char    path[1024];
    int     order;
    uint8_t branch;               /* signature-check byte at the time of the call */
    int     guidZero, keyZero;
    void*   self;
    void*   out;
    int     code;
} Call;

static Call     calls[64];
static int      ncalls;
static uint8_t* img;
static uint8_t* fn;
static uint8_t* sigAt;
static int      failStockOnce;
static int      tildeIsStock;     /* the game mounts ~mods containers itself */
static char     root[512];        /* <tmp>/Mods/AC8ModLoader */
static uint8_t  origFn[32];

static int all_zero(const uint8_t* p, size_t n) { while (n--) if (*p++) return 0; return 1; }

/* Container headers the fake mount hands back (FIoContainerHeader start, 40 bytes) */
typedef struct { uint64_t cid; void* idsData; int32_t idsNum, idsMax; void* entData; int32_t entNum, entMax; } THdr;
_Static_assert(sizeof(THdr) == 40, "header layout");
static struct { char suffix[256]; THdr hdr; } hdrs[16];
static int nhdrs;
static void register_header(const char* suffix, const THdr* h)
{
    snprintf(hdrs[nhdrs].suffix, sizeof(hdrs[nhdrs].suffix), "%s", suffix);
    hdrs[nhdrs].hdr = *h;
    nhdrs++;
}
static int ends_with(const char* s, const char* suffix);

static void* WINAPI fake_mount_impl(void* self, void* out, const wchar_t* path, int32_t order,
                                    const void* guid, const void* key)
{
    Call* c = &calls[ncalls++];
    memset(c, 0, sizeof(*c));
    win_path_to_posix(path, c->path, sizeof(c->path));
    c->order  = order;
    c->branch = sigAt[0x1B];
    c->self   = self;
    c->out    = out;

    int         code = 0;
    const char* msg  = "";
    if (strstr(c->path, "/Paks/") && (!strstr(c->path, "/~mods/") || (tildeIsStock && order < 1000))) {   /* the game's own */
        if (failStockOnce) { failStockOnce = 0; code = 3; msg = "stock failure"; }
    } else {
        c->guidZero = all_zero(guid, 16);
        c->keyZero  = all_zero(key, 32);
        if (c->branch == 0x74)               { code = 12; msg = "Cannot load a non-signed container"; }
        else if (strstr(c->path, "bad.utoc")) { code = 5;  msg = "Corrupt toc"; }
        else if (access(c->path, R_OK) != 0)  { code = 4;  msg = "File not found"; }
    }
    c->code = code;

    memset(out, 0xAA, 0x300);                                /* a result bigger than 0x104 */
    memcpy(out, &code, 4);
    wchar_t wmsg[128];
    size_t n = u8_to_u16(msg, strlen(msg), wmsg, 127);
    wmsg[n] = 0;
    memcpy((uint8_t*)out + 4, wmsg, (n + 1) * sizeof(wchar_t));

    memset((uint8_t*)out + 0x108, 0, 0x78);                  /* an empty container header */
    if (code == 0)
        for (int i = 0; i < nhdrs; ++i)
            if (ends_with(c->path, hdrs[i].suffix)) memcpy((uint8_t*)out + 0x108, &hdrs[i].hdr, sizeof(THdr));
    return out;
}

/* ---- the fake game image ------------------------------------------------------ */
static const uint8_t kPrologue[26] = {
    0x40,0x55,0x53,0x56,0x57,0x41,0x54,0x41,0x55,0x41,0x57,     /* pushes            */
    0x48,0x8D,0xAC,0x24,0xF0,0xFC,0xFF,0xFF,                    /* lea rbp,[rsp-310] */
    0x48,0x81,0xEC,0x10,0x04,0x00,0x00                          /* sub rsp,0x410     */
};
static const uint8_t kSigCheck[35] = {
    0xE8,0x11,0x22,0x33,0x44, 0x44,0x39,0x70,0x08, 0x74,0x56, 0x48,0x8B,0x08, 0x48,0x85,0xC9,
    0x74,0x4E, 0x48,0x8B,0x01, 0xFF,0x50,0x30, 0x84,0xC0, 0x74,0x44, 0x40,0xF6,0xC7,0x04, 0x75,0x48
};
static const uint8_t kPakCtor[67] = {
    0x48,0x89,0x5C,0x24,0x10,0x48,0x89,0x6C,0x24,0x18,
    0x48,0x89,0x74,0x24,0x20,0x57,0x41,0x54,0x41,0x55,
    0x41,0x56,0x41,0x57,0x48,0x83,0xEC,0x30,0x48,0x8D,0x05,
    0x11,0x22,0x33,0x44,0x4C,0x8B,0xEA,0x48,0x89,0x01,0x4C,0x8B,0xF1,0x48,0x8D,0x05,
    0x55,0x66,0x77,0x88,0x33,0xFF,0x48,0x89,0x41,0x10,0x49,0x8B,0xD0,0x89,0x79,0x08,
    0x41,0x0F,0xB6,0xF1
};

static size_t emit_mount_fn(uint8_t* at)
{
    uint8_t* p = at;
    memcpy(p, kPrologue, 26); p += 26;
    /* mov rax,[rip+disp] -> a qword at img+0x1F00 (stands in for the stack cookie) */
    *p++ = 0x48; *p++ = 0x8B; *p++ = 0x05;
    int32_t disp = (int32_t)((img + 0x1F00) - (p + 4));
    memcpy(p, &disp, 4); p += 4;
    /* forward the two stack arguments, call the C helper, undo the prologue */
    static const uint8_t a5[] = { 0x48,0x8B,0x84,0x24,0x70,0x04,0x00,0x00, 0x48,0x89,0x44,0x24,0x20 };
    static const uint8_t a6[] = { 0x48,0x8B,0x84,0x24,0x78,0x04,0x00,0x00, 0x48,0x89,0x44,0x24,0x28 };
    memcpy(p, a5, sizeof(a5)); p += sizeof(a5);
    memcpy(p, a6, sizeof(a6)); p += sizeof(a6);
    *p++ = 0x48; *p++ = 0xB8;                                   /* mov rax, imm64 */
    uint64_t helper = (uint64_t)(uintptr_t)fake_mount_impl;
    memcpy(p, &helper, 8); p += 8;
    *p++ = 0xFF; *p++ = 0xD0;                                   /* call rax */
    static const uint8_t epi[] = { 0x48,0x81,0xC4,0x10,0x04,0x00,0x00,
                                   0x41,0x5F, 0x41,0x5D, 0x41,0x5C, 0x5F, 0x5E, 0x5B, 0x5D, 0xC3 };
    memcpy(p, epi, sizeof(epi)); p += sizeof(epi);
    return (size_t)(p - at);
}

/* ---- the game's own caller of the mount function, and its package store ---------- */
static int selfObj;               /* the mount function's object (the game's "self") */
static const uint8_t kStoreRegT[156] = {
    0x4D,0x8B,0xB6,0xE0,0x00,0x00,0x00,0x49,0x8B,0xBD,0x60,0x02,0x00,0x00,0x49,0x8D,
    0x4E,0x08,0xFF,0x15,0x24,0x79,0x31,0x08,0xB8,0x01,0x00,0x00,0x00,0x48,0x89,0x7D,
    0x50,0x44,0x8B,0xFE,0x89,0x75,0x58,0xF0,0x41,0x0F,0xC1,0x46,0x48,0x49,0x63,0x7E,
    0x40,0x89,0x45,0x5C,0x33,0xC0,0x48,0x89,0x45,0x60,0x89,0x45,0x68,0x8D,0x47,0x01,
    0x41,0x89,0x46,0x40,0x41,0x3B,0x46,0x44,0x76,0x0B,0x8B,0xD7,0x49,0x8D,0x4E,0x38,
    0xE8,0x0F,0xE4,0xED,0xFE,0x49,0x8B,0x46,0x38,0x48,0x8B,0xD7,0x0F,0x10,0x45,0x50,
    0x44,0x0F,0xB6,0x4C,0x24,0x31,0x0F,0x10,0x4D,0x60,0x44,0x0F,0xB6,0x44,0x24,0x31,
    0x48,0xC1,0xE2,0x05,0x0F,0x11,0x04,0x02,0x0F,0x11,0x4C,0x02,0x10,0x41,0x8B,0x56,
    0x40,0x49,0x8B,0x4E,0x38,0xE8,0x7A,0xB9,0x00,0x00,0x49,0x8D,0x4E,0x08,0x41,0xC6,
    0x86,0x18,0x01,0x00,0x00,0x01,0xFF,0x15,0xA8,0x78,0x31,0x08,
};
static int noStoreReg;            /* build the image without the package store code */
static uint8_t* pakMountAt;       /* img+0x1A00: mov r14,rcx ... mov rcx,[r14+0xD0]; call fn */

typedef struct { uint8_t* data; int32_t num, max; } TA;
static uint8_t backendObj[0x200] __attribute__((aligned(16)));
static uint8_t pakObj[0x100] __attribute__((aligned(16)));
static int lockCount, unlockCount, lockedNow, growCalls, sortCalls, sortUnlocked;

static void WINAPI fake_lock(void* l)
{
    if (l != backendObj + 8 || lockedNow) { printf("    bad lock\n"); abort(); }
    lockedNow = 1; lockCount++;
}
static void WINAPI fake_unlock(void* l)
{
    if (l != backendObj + 8 || !lockedNow) { printf("    bad unlock\n"); abort(); }
    lockedNow = 0; unlockCount++;
}
static void WINAPI fake_grow(void* a, int32_t oldNum)
{
    TA* t = (TA*)a;
    if (a != backendObj + 0x38 || t->num != oldNum + 1) { printf("    bad grow\n"); abort(); }
    t->max = t->num + 3;
    t->data = realloc(t->data, (size_t)t->max * 32);
    growCalls++;
}
static void WINAPI fake_sort(void* data, int32_t num, uint8_t p1, uint8_t p2)
{
    (void)p1; (void)p2;
    if (!lockedNow) sortUnlocked++;
    uint8_t* d = (uint8_t*)data;
    for (int i = 1; i < num; i++)            /* highest order first, then highest sequence */
        for (int j = i; j > 0; j--) {
            uint32_t oa, ob, sa, sb;
            memcpy(&oa, d + (size_t)j * 32 + 8, 4); memcpy(&ob, d + (size_t)(j - 1) * 32 + 8, 4);
            memcpy(&sa, d + (size_t)j * 32 + 12, 4); memcpy(&sb, d + (size_t)(j - 1) * 32 + 12, 4);
            if (oa > ob || (oa == ob && sa > sb)) {
                uint8_t tmp[32];
                memcpy(tmp, d + (size_t)j * 32, 32); memcpy(d + (size_t)j * 32, d + (size_t)(j - 1) * 32, 32);
                memcpy(d + (size_t)(j - 1) * 32, tmp, 32);
            } else break;
        }
    sortCalls++;
}

/* the backend as the game has it before pakchunk0: two containers, array full */
static void init_backend(void)
{
    memset(backendObj, 0, sizeof(backendObj));
    memset(pakObj, 0, sizeof(pakObj));
    TA* t = (TA*)(backendObj + 0x38);
    t->data = calloc(2, 32); t->num = 2; t->max = 2;
    uint64_t h0 = 0x111100, h1 = 0x222200; uint32_t o0 = 0, o1 = 3, s0 = 0, s1 = 1;
    memcpy(t->data, &h0, 8);      memcpy(t->data + 8, &o1, 4);  memcpy(t->data + 12, &s1, 4);
    memcpy(t->data + 32, &h1, 8); memcpy(t->data + 40, &o0, 4); memcpy(t->data + 44, &s0, 4);
    int32_t next = 2; memcpy(backendObj + 0x48, &next, 4);
    void* self = &selfObj; void* be = backendObj;
    memcpy(pakObj + 0xD0, &self, 8);
    memcpy(pakObj + 0xE0, &be, 8);
    lockCount = unlockCount = lockedNow = growCalls = sortCalls = sortUnlocked = 0;
}

static void emit_jump_stub(uint8_t* at, void* target)    /* mov rax,imm64 ; jmp rax */
{
    uint64_t t = (uint64_t)(uintptr_t)target;
    at[0] = 0x48; at[1] = 0xB8; memcpy(at + 2, &t, 8); at[10] = 0xFF; at[11] = 0xE0;
}

static void emit_store_code(void)
{
    uint8_t* p = img + 0x1A00;
    pakMountAt = p;
    static const uint8_t head[] = {
        0x41,0x56,                                  /* push r14              */
        0x48,0x83,0xEC,0x40,                        /* sub rsp,0x40          */
        0x49,0x89,0xCE,                             /* mov r14,rcx           */
        0x48,0x8B,0x84,0x24,0x70,0x00,0x00,0x00,    /* mov rax,[rsp+0x70]    */
        0x48,0x89,0x44,0x24,0x20,                   /* mov [rsp+0x20],rax    */
        0x48,0x8B,0x84,0x24,0x78,0x00,0x00,0x00,    /* mov rax,[rsp+0x78]    */
        0x48,0x89,0x44,0x24,0x28,                   /* mov [rsp+0x28],rax    */
        0x49,0x8B,0x8E,0xD0,0x00,0x00,0x00 };       /* mov rcx,[r14+0xD0]    */
    memcpy(p, head, sizeof(head)); p += sizeof(head);
    *p = 0xE8; int32_t rel = (int32_t)(fn - (p + 5)); memcpy(p + 1, &rel, 4); p += 5;   /* call fn */
    static const uint8_t tail[] = { 0x48,0x83,0xC4,0x40, 0x41,0x5E, 0xC3 };  /* add rsp; pop r14; ret */
    memcpy(p, tail, sizeof(tail));

    /* the package store code (never run, only read by the loader) */
    uint8_t* sr = img + 0x1A80;
    memcpy(sr, kStoreRegT, sizeof(kStoreRegT));
    uint8_t* growStub = img + 0x1C00; emit_jump_stub(growStub, (void*)fake_grow);
    uint8_t* sortStub = img + 0x1C20; emit_jump_stub(sortStub, (void*)fake_sort);
    void* lk = (void*)fake_lock, *ul = (void*)fake_unlock;
    memcpy(img + 0x2300, &lk, 8); memcpy(img + 0x2308, &ul, 8);
    rel = (int32_t)(growStub - (sr + 0x55));    memcpy(sr + 0x51, &rel, 4);
    rel = (int32_t)(sortStub - (sr + 0x8A));    memcpy(sr + 0x86, &rel, 4);
    rel = (int32_t)((img + 0x2300) - (sr + 0x18)); memcpy(sr + 0x14, &rel, 4);
    rel = (int32_t)((img + 0x2308) - (sr + 0x9C)); memcpy(sr + 0x98, &rel, 4);
}

typedef void* (WINAPI *PakMountFn)(void* pak, void* out, const wchar_t* path, int32_t order,
                                   const void* guid, const void* key);

static void build_image(int duplicateMountSig)
{
    img = mmap(NULL, 0x4000, PROT_READ | PROT_WRITE | PROT_EXEC, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    memset(img, 0xCC, 0x4000);
    memset(img, 0, 0x1000);

    /* PE headers: DOS e_lfanew -> NT headers -> two sections */
    int32_t lfanew = 0x80;            memcpy(img + 0x3C, &lfanew, 4);
    uint8_t* nt = img + 0x80;         memcpy(nt, "PE\0\0", 4);
    uint16_t nsec = 2, optSize = 0xF0;
    memcpy(nt + 0x06, &nsec, 2);      memcpy(nt + 0x14, &optSize, 2);
    uint8_t* sec = nt + 0x18 + optSize;
    uint32_t v;
    memcpy(sec, ".rdata\0\0", 8);                                    /* not executable */
    v = 0x1000;     memcpy(sec + 8, &v, 4);  v = 0x2000; memcpy(sec + 12, &v, 4);
    v = 0x40000040; memcpy(sec + 36, &v, 4);
    sec += 40;
    memcpy(sec, ".text\0\0\0", 8);                                   /* executable */
    v = 0x1000;     memcpy(sec + 8, &v, 4);  v = 0x1000; memcpy(sec + 12, &v, 4);
    v = 0x60000020; memcpy(sec + 36, &v, 4);

    fn = img + 0x1000;
    emit_mount_fn(fn);
    memcpy(origFn, fn, sizeof(origFn));

    sigAt = img + 0x1800;
    memcpy(sigAt, kSigCheck, sizeof(kSigCheck));
    memcpy(img + 0x1500, kPakCtor, sizeof(kPakCtor));

    /* near misses inside .text that must NOT match */
    memcpy(img + 0x1400, kPrologue, 26);  img[0x1400 + 20] ^= 1;    /* one byte different   */
    memcpy(img + 0x1900, kSigCheck, 35);  img[0x1900 + 0x1B] = 0xEB; /* branch already a jmp */
    memcpy(img + 0x1FF0, kPrologue, 16);                             /* cut off by section end */

    /* exact copies in the non-executable section that must be ignored */
    memcpy(img + 0x2100, fn, 40);
    memcpy(img + 0x2200, kSigCheck, 35);

    if (duplicateMountSig)
        memcpy(img + 0x1600, fn, 29);

    if (!noStoreReg)
        emit_store_code();

    shim_exe_base = img;
    shim_set_page_prot(img + 0x1000, 0x1000, PAGE_EXECUTE_READ);      /* code is read-only */
}

/* ---- files -------------------------------------------------------------------- */
static void mkdirs(const char* rel)
{
    char cmd[2048];
    snprintf(cmd, sizeof(cmd), "mkdir -p '%s/%s'", root, rel);
    if (system(cmd) != 0) abort();
}

static void mkfile(const char* rel, const void* data, size_t n)
{
    char p[2048];
    snprintf(p, sizeof(p), "%s/%s", root, rel);
    FILE* f = fopen(p, "wb");
    if (!f) { perror(p); abort(); }
    if (n) fwrite(data, 1, n, f);
    fclose(f);
}

/* a .utoc and the .ucas next to it */
static void mkutoc(const char* rel)
{
    mkfile(rel, "x", 1);
    char cas[1024];
    snprintf(cas, sizeof(cas), "%s", rel);
    memcpy(cas + strlen(cas) - 4, "ucas", 4);
    mkfile(cas, "x", 1);
}

static char logText[1 << 17];
static void read_log(void)
{
    char p[2048];
    snprintf(p, sizeof(p), "%s/AC8IoStoreLoader.log", root);
    FILE* f = fopen(p, "rb");
    logText[0] = 0;
    if (!f) return;
    size_t n = fread(logText, 1, sizeof(logText) - 1, f);
    logText[n] = 0;
    fclose(f);
}

static void setup(int duplicateMountSig)
{
    char tmpl[] = "/tmp/ac8test.XXXXXX";
    char* t = mkdtemp(tmpl);
    snprintf(root, sizeof(root), "%s/Mods/AC8ModLoader", t);
    mkdirs("dlls");
    mkfile("dlls/main.dll", "MZ", 2);

    char modPath[1024];
    snprintf(modPath, sizeof(modPath), "%s/dlls/main.dll", root);
    for (char* p = modPath; *p; ++p) if (*p == '/') *p = '\\';     /* a Windows-style path */
    size_t n = u8_to_u16(modPath, strlen(modPath), shim_module_path, 1023);
    shim_module_path[n] = 0;

    build_image(duplicateMountSig);
}

/* The standard set of folders used by most scenarios */
static void standard_mods(void)
{
    mkdirs("AC8PGMDirect");  mkutoc("AC8PGMDirect/AC8PGMDirect_P.utoc");
    mkdirs("zeta");          mkutoc("zeta/z.utoc");
    mkdirs("Alpha");         mkutoc("Alpha/b2.utoc");
                             mkutoc("Alpha/B1.UTOC");
                             mkfile("Alpha/old.utoc.bak", "x", 1);      /* not a container */
                             mkfile("Alpha/readme.txt", "x", 1);
    mkdirs("empty");         mkfile("empty/notes.txt", "x", 1);         /* no .utoc: not a mod */
    mkdirs("Scripts");       mkutoc("Scripts/s.utoc");                  /* UE4SS folder: skipped */
    mkutoc("dlls/stray.utoc");                                          /* dlls: skipped */
    mkutoc("loose.utoc");                                               /* a loose file is a mod too */
}

static uint8_t stockOut[0x400];

static void* game_mounts(const char* posixPath, int order)
{
    static const uint8_t guid[16] = {1,2,3}, key[32] = {4,5,6};
    wchar_t w[1024];
    char    p[1024];
    snprintf(p, sizeof(p), "%s", posixPath);
    for (char* q = p; *q; ++q) if (*q == '/') *q = '\\';
    size_t n = u8_to_u16(p, strlen(p), w, 1023);
    w[n] = 0;
    return ((MountFn)(void*)fn)(&selfObj, stockOut, w, order, guid, key);
}

static int ends_with(const char* s, const char* suffix)
{
    size_t n = strlen(s), m = strlen(suffix);
    return n >= m && strcmp(s + n - m, suffix) == 0;
}

static void expect_call(int i, const char* suffix, int order)
{
    CHECK(i < ncalls, "expected call #%d (%s) but only %d calls were made", i, suffix, ncalls);
    if (i >= ncalls) return;
    CHECK(ends_with(calls[i].path, suffix), "call #%d was %s, expected ...%s", i, calls[i].path, suffix);
    CHECK(calls[i].order == order, "call #%d order %d, expected %d", i, calls[i].order, order);
    CHECK(calls[i].self == &selfObj, "call #%d wrong self pointer", i);
    if (order >= 1000) {
        CHECK(calls[i].branch == 0xEB, "call #%d made with signature check still on", i);
        CHECK(calls[i].guidZero && calls[i].keyZero, "call #%d key/guid not zero", i);
        CHECK(calls[i].out != stockOut, "call #%d reused the game's result buffer", i);
    }
}

static void common_end_checks(void)
{
    CHECK(sigAt[0x1B] == 0x74, "signature check byte left as 0x%02X", sigAt[0x1B]);
    CHECK(shim_suspends == shim_resumes, "suspend %d != resume %d", shim_suspends, shim_resumes);
    CHECK(shim_thread_opens == shim_thread_closes, "thread handles leaked");
    CHECK(shim_alloc_while_frozen == 0, "loader allocated or logged while threads were frozen");
    CHECK(img[0x1400 + 20] == (kPrologue[20] ^ 1), "near-miss prologue was modified");
    CHECK(memcmp(img + 0x2100, origFn, 32) == 0, "non-executable decoy was modified");
}

/* ============================================================================== */

/* A: no loadorder.txt -> every folder, A to Z; full life cycle incl. unload */
static void scenario_default_order(void)
{
    setup(0);
    standard_mods();
    shim_fake_threads = 3;
    shim_busy_rip     = (uint64_t)(uintptr_t)(fn + 5);   /* a thread inside the bytes to patch */
    shim_busy_times   = 2;                               /* ...for the first two attempts      */

    DllMain((HANDLE)0x1234, DLL_PROCESS_ATTACH, NULL);
    void* mod = start_mod();
    CHECK(mod != NULL, "start_mod returned NULL");
    CHECK(shim_ctor_calls == 1, "base constructor calls=%d", shim_ctor_calls);
    CHECK(*(void**)mod != (void*)0xBA5EBA5E, "vtable pointer not replaced");
    CHECK(shim_busy_times == 0, "busy-thread retry path not exercised");
    CHECK(memcmp(fn, origFn, 14) != 0, "function was not hooked");
    CHECK(memcmp(fn + 14, origFn + 14, 18) == 0, "bytes after the 14-byte patch were touched");

    char s[256];
    const wchar_t* w = shim_read_wstring(mod, 0);
    s[u16_to_u8(w, wlen16(w), s, 255)] = 0;
    CHECK(strcmp(s, "AC8ModLoader") == 0, "ModName=%s", s);
    w = shim_read_wstring(mod, 1);
    s[u16_to_u8(w, wlen16(w), s, 255)] = 0;
    CHECK(strcmp(s, "1.3.0-ac8-1.1.2.0-ue5.4") == 0, "ModVersion=%s", s);
    w = shim_read_wstring(mod, 3);
    s[u16_to_u8(w, wlen16(w), s, 255)] = 0;
    CHECK(strcmp(s, "voidcallsreaper") == 0, "ModAuthors=%s", s);

    /* every UE4SS callback slot must be callable and do nothing */
    typedef void* (WINAPI *Slot)(void*);
    void** vt = *(void***)mod;
    for (int i = 1; i < 30; ++i) CHECK(((Slot)vt[i])(mod) == NULL, "vtable slot %d", i);

    char p[1024];
    void* r;

    snprintf(p, sizeof(p), "%s/../../Content/Paks/global.utoc", root);
    r = game_mounts(p, 3);
    CHECK(r == stockOut, "hook did not return the game's own result");
    CHECK(ncalls == 1, "a non-trigger container caused %d calls", ncalls);

    failStockOnce = 1;                                    /* pakchunk0 fails: no trigger */
    snprintf(p, sizeof(p), "%s/../../Content/Paks/pakchunk0-Windows.utoc", root);
    r = game_mounts(p, 4);
    CHECK(ncalls == 2, "failed pakchunk0 mount triggered mods (%d calls)", ncalls);
    CHECK(*(int*)r == 3, "game did not see its own failure code");

    snprintf(p, sizeof(p), "%s/../../Content/Paks/PAKCHUNK0-windows.UTOC", root);   /* case-insensitive */
    r = game_mounts(p, 4);
    CHECK(r == stockOut && *(int*)r == 0, "game did not see its own success result");

    expect_call(0, "/Paks/global.utoc", 3);
    expect_call(1, "/Paks/pakchunk0-Windows.utoc", 4);
    expect_call(2, "/Paks/PAKCHUNK0-windows.UTOC", 4);
    expect_call(3, "/AC8PGMDirect/AC8PGMDirect_P.utoc", 1000);
    expect_call(4, "/Alpha/B1.UTOC", 1001);
    expect_call(5, "/Alpha/b2.utoc", 1002);
    expect_call(6, "/AC8ModLoader/loose.utoc", 1003);
    expect_call(7, "/zeta/z.utoc", 1004);
    CHECK(ncalls == 8, "expected 8 calls, got %d", ncalls);
    for (int i = 3; i < ncalls; ++i) CHECK(calls[i].code == 0, "custom mount %d code=%d", i, calls[i].code);

    r = game_mounts(p, 4);                                /* second pakchunk0: no repeat */
    CHECK(ncalls == 9, "mods were mounted twice (%d calls)", ncalls);

    uninstall_mod(mod);
    CHECK(shim_dtor_calls == 1, "base destructor calls=%d", shim_dtor_calls);
    CHECK(memcmp(fn, origFn, 32) == 0, "original bytes not restored on unload");
    game_mounts(p, 4);                                    /* unhooked function still works */
    CHECK(ncalls == 10, "unhooked call count %d", ncalls);

    common_end_checks();
    read_log();
    CHECK(strstr(logText, "done: 5 of 5 containers mounted from 4 mod(s)") != NULL, "summary line missing");
    CHECK(strstr(logText, "mods found=4 (loader folder 4, ~mods 0)") != NULL, "mods-found line missing");
    CHECK(strstr(logText, "not recognised") == NULL, "empty headers should not be reported");
    CHECK(strstr(logText, "mount signature matches=1") != NULL, "scan line missing");
    CHECK(strstr(logText, "unloaded, hook removed=yes") != NULL, "unload line missing");
}

/* B: UTF-8 loadorder.txt with BOM, CRLF, comments, duplicates, a missing folder */
static void scenario_loadorder_utf8(void)
{
    setup(0);
    standard_mods();
    mkdirs("Newcomer"); mkutoc("Newcomer/n.utoc");
    const char* txt = "\xEF\xBB\xBF# load order\r\n\r\n+zeta\r\n-Alpha\r\n+Ghost\r\n"
                      "  +  AC8PGMDirect  \r\n+zeta\r\n; another comment\r\n";
    mkfile("loadorder.txt", txt, strlen(txt));

    DllMain((HANDLE)0x1234, DLL_PROCESS_ATTACH, NULL);
    void* mod = start_mod();
    char p[1024];
    snprintf(p, sizeof(p), "%s/../../Content/Paks/pakchunk0-Windows.utoc", root);
    game_mounts(p, 4);

    expect_call(1, "/zeta/z.utoc", 1000);
    expect_call(2, "/AC8PGMDirect/AC8PGMDirect_P.utoc", 1001);
    expect_call(3, "/loose.utoc", 1002);
    expect_call(4, "/Newcomer/n.utoc", 1003);
    CHECK(ncalls == 5, "expected 5 calls, got %d", ncalls);

    uninstall_mod(mod);
    common_end_checks();
    read_log();
    CHECK(strstr(logText, "listed but not found (ignored): Ghost") != NULL, "missing-folder note absent");
    CHECK(strstr(logText, "  off  Alpha") != NULL, "disabled mod not logged");
}

/* C: UTF-16 LE loadorder.txt, LF only, non-ASCII folder name, a line with no +/- */
static void scenario_loadorder_utf16(void)
{
    setup(0);
    standard_mods();
    mkdirs("M\xC3\xB3" "dulo \xE6\x97\xA5\xE6\x9C\xAC"); mkutoc("M\xC3\xB3" "dulo \xE6\x97\xA5\xE6\x9C\xAC/m.utoc");

    const char* txt8 = "-zeta\n+M\xC3\xB3" "dulo \xE6\x97\xA5\xE6\x9C\xAC\nalpha\n";   /* "alpha": other case, no flag */
    wchar_t w[256];
    w[0] = 0xFEFF;
    size_t n = u8_to_u16(txt8, strlen(txt8), w + 1, 250);
    mkfile("loadorder.txt", w, (n + 1) * sizeof(wchar_t));

    DllMain((HANDLE)0x1234, DLL_PROCESS_ATTACH, NULL);
    void* mod = start_mod();
    char p[1024];
    snprintf(p, sizeof(p), "%s/../../Content/Paks/pakchunk0-Windows.utoc", root);
    game_mounts(p, 4);

    expect_call(1, "/M\xC3\xB3" "dulo \xE6\x97\xA5\xE6\x9C\xAC/m.utoc", 1000);
    expect_call(2, "/Alpha/B1.UTOC", 1001);
    expect_call(3, "/Alpha/b2.utoc", 1002);
    expect_call(4, "/AC8PGMDirect/AC8PGMDirect_P.utoc", 1003);
    expect_call(5, "/loose.utoc", 1004);
    CHECK(ncalls == 6, "expected 6 calls, got %d", ncalls);

    uninstall_mod(mod);
    common_end_checks();
    read_log();
    CHECK(strstr(logText, "M\xC3\xB3" "dulo \xE6\x97\xA5\xE6\x9C\xAC") != NULL, "log is not valid UTF-8 for non-ASCII names");
}

/* D: everything switched off -> the game is left alone */
static void scenario_all_disabled(void)
{
    setup(0);
    standard_mods();
    const char* txt = "-zeta\n-Alpha\n-AC8PGMDirect\n-loose.utoc\n";
    mkfile("loadorder.txt", txt, strlen(txt));

    DllMain((HANDLE)0x1234, DLL_PROCESS_ATTACH, NULL);
    void* mod = start_mod();
    char p[1024];
    snprintf(p, sizeof(p), "%s/../../Content/Paks/pakchunk0-Windows.utoc", root);
    game_mounts(p, 4);
    CHECK(ncalls == 1, "expected only the stock call, got %d", ncalls);
    CHECK(calls[0].branch == 0x74, "signature check touched with nothing to mount");

    uninstall_mod(mod);
    common_end_checks();
    read_log();
    CHECK(strstr(logText, "nothing to mount") != NULL, "no 'nothing to mount' line");
}

/* E: the mount pattern appears twice (wrong game version) -> do not hook at all */
static void scenario_ambiguous_pattern(void)
{
    setup(1);
    standard_mods();

    DllMain((HANDLE)0x1234, DLL_PROCESS_ATTACH, NULL);
    void* mod = start_mod();
    CHECK(mod != NULL, "start_mod must still return the mod object");
    CHECK(memcmp(fn, origFn, 32) == 0, "function hooked despite ambiguous pattern");

    char p[1024];
    snprintf(p, sizeof(p), "%s/../../Content/Paks/pakchunk0-Windows.utoc", root);
    game_mounts(p, 4);
    CHECK(ncalls == 1, "expected only the stock call, got %d", ncalls);

    uninstall_mod(mod);
    common_end_checks();
    read_log();
    CHECK(strstr(logText, "mount signature matches=2") != NULL, "scan count not logged");
    CHECK(strstr(logText, "nothing changed") != NULL, "no refusal line");
}

/* F: one container fails -> the rest still mount, and the check is restored */
static void scenario_one_bad_container(void)
{
    setup(0);
    mkdirs("A_Good");   mkutoc("A_Good/a.utoc");
    mkdirs("B_Broken"); mkutoc("B_Broken/bad.utoc");
    mkdirs("C_Good");   mkutoc("C_Good/c.utoc");
    mkdirs("_under");   mkutoc("_under/u.utoc");             /* '_' sorts after letters */
    mkdirs("10_num");   mkutoc("10_num/n.utoc");             /* digits sort before letters */

    DllMain((HANDLE)0x1234, DLL_PROCESS_ATTACH, NULL);
    void* mod = start_mod();
    char p[1024];
    snprintf(p, sizeof(p), "%s/../../Content/Paks/pakchunk0-Windows.utoc", root);
    game_mounts(p, 4);

    expect_call(1, "/10_num/n.utoc", 1000);
    expect_call(2, "/A_Good/a.utoc", 1001);
    expect_call(3, "/B_Broken/bad.utoc", 1002);
    expect_call(4, "/C_Good/c.utoc", 1003);
    expect_call(5, "/_under/u.utoc", 1004);
    CHECK(ncalls == 6, "expected 6 calls, got %d", ncalls);
    CHECK(calls[3].code == 5 && calls[4].code == 0, "codes %d %d", calls[3].code, calls[4].code);

    uninstall_mod(mod);
    common_end_checks();
    read_log();
    CHECK(strstr(logText, "result code=5 message=Corrupt toc") != NULL, "failure not logged");
    CHECK(strstr(logText, "done: 4 of 5 containers mounted") != NULL, "summary wrong");
}

/* G: no mod folders at all */
static void scenario_no_mods(void)
{
    setup(0);
    DllMain((HANDLE)0x1234, DLL_PROCESS_ATTACH, NULL);
    void* mod = start_mod();
    char p[1024];
    snprintf(p, sizeof(p), "%s/../../Content/Paks/pakchunk0-Windows.utoc", root);
    game_mounts(p, 4);
    CHECK(ncalls == 1, "expected only the stock call, got %d", ncalls);
    uninstall_mod(mod);
    common_end_checks();
}


/* ---- helpers for the newer scenarios ------------------------------------------ */
static void start_and_mount_pakchunk0(void** modOut)
{
    DllMain((HANDLE)0x1234, DLL_PROCESS_ATTACH, NULL);
    *modOut = start_mod();
    char p[1024];
    snprintf(p, sizeof(p), "%s/../../Content/Paks/global.utoc", root);
    game_mounts(p, 3);
    snprintf(p, sizeof(p), "%s/../../Content/Paks/pakchunk0-Windows.utoc", root);
    game_mounts(p, 4);
}

static void set_mtime(const char* rel, long secondsAgo)
{
    char p[2048];
    snprintf(p, sizeof(p), "%s/%s", root, rel);
    struct timespec ts[2];
    clock_gettime(CLOCK_REALTIME, &ts[0]);
    ts[0].tv_sec -= secondsAgo;
    ts[1] = ts[0];
    if (utimensat(AT_FDCWD, p, ts, 0) != 0) { perror(p); abort(); }
}

static int count_mod_calls(void) { int n = 0; for (int i = 0; i < ncalls; ++i) if (calls[i].order >= 1000) n++; return n; }

/* H: mods wrapped in extra folders, tool folders skipped, depth limit */
static void scenario_nested(void)
{
    setup(0);
    mkdirs("Wrapped/Wrapped v1.2/Inner");   mkutoc("Wrapped/Wrapped v1.2/Inner/w.utoc");
    mkdirs("Wrapped/old");                  mkutoc("Wrapped/old/w_old.utoc");          /* old: skipped */
    mkdirs("Wrapped/Backup");               mkutoc("Wrapped/Backup/w_bak.utoc");       /* any case */
    mkdirs("SkyEditor/packed");             mkutoc("SkyEditor/packed/SkyEdit.utoc");   /* tool output */
    mkdirs("SkyEditor/build/Live");         mkutoc("SkyEditor/build/Live/x.utoc");
    mkdirs("SkyEditor/export");             mkutoc("SkyEditor/export/e.utoc");
    mkdirs("SkyEdit");                      mkutoc("SkyEdit/SkyEdit.utoc");
    mkdirs("Deep/1/2/3/4/5");               mkutoc("Deep/1/2/3/4/5/ok.utoc");          /* 5 levels: found */
    mkdirs("Deeper/1/2/3/4/5/6");           mkutoc("Deeper/1/2/3/4/5/6/no.utoc");      /* 6 levels: not */
    mkdirs("old");                          mkutoc("old/top_old.utoc");                /* top-level old */

    void* mod;
    start_and_mount_pakchunk0(&mod);
    expect_call(2, "/Deep/1/2/3/4/5/ok.utoc", 1000);
    expect_call(3, "/SkyEdit/SkyEdit.utoc", 1001);
    expect_call(4, "/Wrapped/Wrapped v1.2/Inner/w.utoc", 1002);
    CHECK(ncalls == 5, "expected 5 calls, got %d", ncalls);

    uninstall_mod(mod);
    common_end_checks();
    read_log();
    CHECK(strstr(logText, "mods found=3 (loader folder 3, ~mods 0)") != NULL, "mod count wrong");
    CHECK(strstr(logText, "SkyEditor") == NULL, "SkyEditor treated as a mod");
}

/* I: a .utoc without its .ucas is skipped and named in the log */
static void scenario_missing_ucas(void)
{
    setup(0);
    mkdirs("NoCas"); mkfile("NoCas/n.utoc", "x", 1);
    mkdirs("Good");  mkutoc("Good/g.utoc");
    mkdirs("Half");  mkutoc("Half/h1.utoc"); mkfile("Half/h2.utoc", "x", 1);

    void* mod;
    start_and_mount_pakchunk0(&mod);
    expect_call(2, "/Good/g.utoc", 1000);
    expect_call(3, "/Half/h1.utoc", 1001);
    CHECK(ncalls == 4, "expected 4 calls, got %d", ncalls);

    uninstall_mod(mod);
    common_end_checks();
    read_log();
    CHECK(strstr(logText, "n.utoc: its .ucas file is missing (n.ucas)") != NULL, "missing .ucas not logged");
    CHECK(strstr(logText, "h2.utoc: its .ucas file is missing") != NULL, "missing .ucas not logged (2)");
    CHECK(strstr(logText, "done: 2 of 2 containers mounted from 2 mod(s)") != NULL, "summary wrong");
}

/* J: the same container in two places loads once, the newer copy */
static void scenario_duplicates(void)
{
    setup(0);
    mkdirs("ModA");      mkutoc("ModA/Shared_P.utoc"); mkutoc("ModA/OnlyA.utoc");
    mkdirs("ModB v2");   mkutoc("ModB v2/shared_p.utoc");                  /* other case, newer */
    mkdirs("ModC");      mkutoc("ModC/Shared_P.utoc");                     /* oldest */
    set_mtime("ModA/Shared_P.utoc", 3600);
    set_mtime("ModB v2/shared_p.utoc", 60);
    set_mtime("ModC/Shared_P.utoc", 7200);

    void* mod;
    start_and_mount_pakchunk0(&mod);
    expect_call(2, "/ModA/OnlyA.utoc", 1000);
    expect_call(3, "/ModB v2/shared_p.utoc", 1001);
    CHECK(ncalls == 4, "expected 4 calls, got %d", ncalls);

    uninstall_mod(mod);
    common_end_checks();
    read_log();
    CHECK(strstr(logText, "ModA/Shared_P.utoc") || strstr(logText, "ModA\\Shared_P.utoc"), "older copy not named");
    CHECK(strstr(logText, "and only one can load (the newer one is used)") != NULL, "duplicate not logged");
    CHECK(strstr(logText, "done: 2 of 2 containers mounted from 2 mod(s)") != NULL, "summary wrong");
}

/* K: the game's own ~mods folder (found from a relative pakchunk0 path) */
static void scenario_tilde_mods(int withOrder)
{
    setup(0);
    /* a game layout next to the UE4SS folder: <tmp>/Game/Binaries/Win64/AceCombat8.exe */
    char exe[1024];
    snprintf(exe, sizeof(exe), "%s/../../Game/Binaries/Win64/AceCombat8.exe", root);
    for (char* q = exe; *q; ++q) if (*q == '/') *q = '\\';
    shim_exe_path[u8_to_u16(exe, strlen(exe), shim_exe_path, 1023)] = 0;
    mkdirs("../../Game/Binaries/Win64");
    mkdirs("../../Game/Content/Paks/~mods/Folder Mod/Sub"); mkutoc("../../Game/Content/Paks/~mods/Folder Mod/Sub/f.utoc");
    mkutoc("../../Game/Content/Paks/~mods/Loose_P.utoc");
    mkfile("../../Game/Content/Paks/~mods/Loose_P.pak", "x", 1);
    mkdirs("../../Game/Content/Paks/~mods/old"); mkutoc("../../Game/Content/Paks/~mods/old/o.utoc");
    mkdirs("Here"); mkutoc("Here/h.utoc");
    if (withOrder) {
        const char* txt = "+~mods/Loose_P.utoc\n-~mods\\Folder Mod\n";
        mkfile("loadorder.txt", txt, strlen(txt));
    }

    DllMain((HANDLE)0x1234, DLL_PROCESS_ATTACH, NULL);
    void* mod = start_mod();
    wchar_t w[256];
    const char* rel = "..\\..\\..\\Game\\Content\\Paks\\global.utoc";
    w[u8_to_u16(rel, strlen(rel), w, 255)] = 0;
    static const uint8_t guid[16], key[32];
    ((MountFn)(void*)fn)(&selfObj, stockOut, w, 3, guid, key);
    rel = "../../../Game/Content/Paks/pakchunk0-Windows.utoc";             /* the game uses / here */
    w[u8_to_u16(rel, strlen(rel), w, 255)] = 0;
    ((MountFn)(void*)fn)(&selfObj, stockOut, w, 4, guid, key);

    if (!withOrder) {
        expect_call(2, "/Here/h.utoc", 1000);
        expect_call(3, "/~mods/Folder Mod/Sub/f.utoc", 1001);
        expect_call(4, "/~mods/Loose_P.utoc", 1002);
        CHECK(ncalls == 5, "expected 5 calls, got %d", ncalls);
    } else {
        expect_call(2, "/~mods/Loose_P.utoc", 1000);
        expect_call(3, "/Here/h.utoc", 1001);
        CHECK(ncalls == 4, "expected 4 calls, got %d", ncalls);
    }
    for (int i = 2; i < ncalls; ++i) CHECK(calls[i].code == 0, "mount %d code=%d (%s)", i, calls[i].code, calls[i].path);

    uninstall_mod(mod);
    common_end_checks();
    read_log();
    CHECK(strstr(logText, withOrder ? "mods found=3 (loader folder 1, ~mods 2)" : "mods found=3 (loader folder 1, ~mods 2)") != NULL, "count wrong");
    if (withOrder) CHECK(strstr(logText, "  off  ~mods\\Folder Mod") != NULL, "~mods entry not switched off");
}
static void scenario_tilde_mods_plain(void) { scenario_tilde_mods(0); }
static void scenario_tilde_mods_order(void) { scenario_tilde_mods(1); }

/* L: when the loader counts as started late (pakchunk0 already gone) */
static void scenario_late(int variant)
{
    setup(0);
    mkdirs("M"); mkutoc("M/m.utoc");
    DllMain((HANDLE)0x1234, DLL_PROCESS_ATTACH, NULL);
    void* mod = start_mod();
    char p[1024];
#define MOUNT(name, ord) do { snprintf(p, sizeof(p), "%s/../../Content/Paks/%s", root, name); game_mounts(p, ord); } while (0)
    int late = 0;
    if (variant == 0) {           /* hooked after global.utoc: pakchunk1 comes first, pakchunk0 still to come */
        MOUNT("pakchunk2-Windows.utoc", 6); MOUNT("pakchunk1-Windows.utoc", 5);
        CHECK(ncalls == 2, "triggered before pakchunk0 (%d calls)", ncalls);
        MOUNT("pakchunk0-Windows.utoc", 4);
        expect_call(3, "/M/m.utoc", 1000);
        CHECK(ncalls == 4, "expected 4 calls, got %d", ncalls);
    } else if (variant == 1) {    /* normal start */
        MOUNT("global.utoc", 3); MOUNT("pakchunk1-Windows.utoc", 5);
        CHECK(ncalls == 2, "normal start triggered early (%d calls)", ncalls);
        MOUNT("pakchunk0-Windows.utoc", 4);
        expect_call(3, "/M/m.utoc", 1000);
        CHECK(ncalls == 4, "expected 4 calls, got %d", ncalls);
    } else if (variant == 2) {    /* first seen sorts below pakchunk0: it already happened -> mount now */
        MOUNT("Engine-Windows.utoc", 2);
        expect_call(1, "/M/m.utoc", 1000);
        CHECK(ncalls == 2, "late start: expected 2 calls, got %d", ncalls);
        MOUNT("Fonts-Windows.utoc", 1);
        CHECK(ncalls == 3, "mounted twice (%d calls)", ncalls);
        late = 1;
    } else if (variant == 3) {    /* first seen is in ~mods: the game mounts those before pakchunks */
        mkdirs("../../Content/Paks/~mods");
        MOUNT("~mods/Other_P.utoc", 9);
        CHECK(ncalls == 1, "triggered on a ~mods container");
        MOUNT("pakchunk0-Windows.utoc", 4);
        expect_call(2, "/M/m.utoc", 1000);
        CHECK(ncalls == 3, "expected 3 calls, got %d", ncalls);
    } else {                      /* late, and the first one seen failed: the next good one triggers */
        failStockOnce = 1;
        MOUNT("Alpha-Windows.utoc", 2);
        CHECK(ncalls == 1, "triggered on a failed mount");
        MOUNT("Beta-Windows.utoc", 1);
        expect_call(2, "/M/m.utoc", 1000);
        CHECK(ncalls == 3, "expected 3 calls, got %d", ncalls);
        late = 1;
    }
#undef MOUNT
    uninstall_mod(mod);
    common_end_checks();
    read_log();
    int said = strstr(logText, "the loader started after the game mounted pakchunk0") != NULL;
    CHECK(late ? said : !said, "late-start note %s", said ? "present" : "absent");
}
static void scenario_late_0(void) { scenario_late(0); }
static void scenario_late_1(void) { scenario_late(1); }
static void scenario_late_2(void) { scenario_late(2); }
static void scenario_late_3(void) { scenario_late(3); }
static void scenario_late_4(void) { scenario_late(4); }

/* Q: a ~mods container the game managed to mount itself is not mounted again */
static void scenario_game_mounted_tilde(void)
{
    setup(0);
    tildeIsStock = 1;
    mkdirs("../../Content/Paks/~mods"); mkutoc("../../Content/Paks/~mods/Signed_P.utoc");
    mkutoc("../../Content/Paks/~mods/Other_P.utoc");
    DllMain((HANDLE)0x1234, DLL_PROCESS_ATTACH, NULL);
    void* mod = start_mod();
    char p[1024];
    snprintf(p, sizeof(p), "%s/../../Content/Paks/global.utoc", root);            game_mounts(p, 3);
    snprintf(p, sizeof(p), "%s/../../Content/Paks/~mods/Signed_P.utoc", root);    game_mounts(p, 9);
    snprintf(p, sizeof(p), "%s/../../Content/Paks/pakchunk0-Windows.utoc", root); game_mounts(p, 4);
    expect_call(3, "/~mods/Other_P.utoc", 1000);
    CHECK(ncalls == 4, "expected 4 calls, got %d", ncalls);
    uninstall_mod(mod);
    common_end_checks();
    read_log();
    CHECK(strstr(logText, "Signed_P.utoc: the game has already mounted a container with this name") != NULL, "not logged");
}

/* R: links inside a mod folder are not followed; a linked mod folder itself is fine */
static void scenario_links(void)
{
    setup(0);
    mkdirs("LinkMod/real"); mkutoc("LinkMod/real/r.utoc");
    mkdirs("../../Elsewhere/stuff"); mkutoc("../../Elsewhere/stuff/x.utoc");
    mkdirs("../../DevMod"); mkutoc("../../DevMod/dev.utoc");
    char a[2048], b[2048];
    snprintf(a, sizeof(a), "%s/../../Elsewhere", root); snprintf(b, sizeof(b), "%s/LinkMod/link", root);
    if (symlink(a, b) != 0) { perror("symlink"); abort(); }
    snprintf(a, sizeof(a), "%s/../../DevMod", root); snprintf(b, sizeof(b), "%s/TopLink", root);
    if (symlink(a, b) != 0) { perror("symlink"); abort(); }
    void* mod;
    start_and_mount_pakchunk0(&mod);
    expect_call(2, "/LinkMod/real/r.utoc", 1000);
    expect_call(3, "/TopLink/dev.utoc", 1001);
    CHECK(ncalls == 4, "expected 4 calls, got %d", ncalls);
    uninstall_mod(mod);
    common_end_checks();
}

/* S: UE4SS unloads and starts the mod again in the same game session */
static void scenario_restart(void)
{
    setup(0);
    mkdirs("M"); mkutoc("M/m.utoc");
    void* mod;
    start_and_mount_pakchunk0(&mod);
    CHECK(count_mod_calls() == 1, "first mount");
    CHECK(shim_pins == 1, "DLL not pinned (%d)", shim_pins);
    uninstall_mod(mod);
    CHECK(memcmp(fn, origFn, 32) == 0, "hook not removed");
    mod = start_mod();
    CHECK(memcmp(fn, origFn, 14) != 0, "not hooked again");
    char p[1024];
    snprintf(p, sizeof(p), "%s/../../Content/Paks/pakchunk1-Windows.utoc", root); game_mounts(p, 5);
    snprintf(p, sizeof(p), "%s/../../Content/Paks/pakchunk0-Windows.utoc", root); game_mounts(p, 4);
    CHECK(count_mod_calls() == 1, "mods mounted again after a restart (%d)", count_mod_calls());
    uninstall_mod(mod);
    common_end_checks();
    read_log();
    CHECK(strstr(logText, "done: 1 of 1 containers mounted") != NULL, "first run's log was lost");
    CHECK(strstr(logText, "(started again by UE4SS)") != NULL, "restart not logged");
    CHECK(strstr(logText, "the mods were already mounted in this game session") != NULL, "restart state not logged");
}

/* M: another loader already hooked the function */
static void other_detour(void) {}
static void scenario_other_loader(int shortJump)
{
    setup(0);
    standard_mods();
    const char* other = "C:\\Games\\ue4ss\\Mods\\OldLoader\\dlls\\main.dll";
    shim_other_path[u8_to_u16(other, strlen(other), shim_other_path, 1023)] = 0;
    uint8_t* target = (uint8_t*)(void*)other_detour;
    if (!shortJump) { shim_other_lo = (uintptr_t)target; shim_other_hi = (uintptr_t)target + 16; }

    shim_set_page_prot(fn, 32, PAGE_EXECUTE_READWRITE);
    if (shortJump) {                                      /* jmp rel32 into a relay page */
        fn[0] = 0xE9; int32_t rel = 0x1000; memcpy(fn + 1, &rel, 4);
    } else {                                              /* jmp [rip+0] ; dq target */
        fn[0] = 0xFF; fn[1] = 0x25; memset(fn + 2, 0, 4); uint64_t t = (uint64_t)(uintptr_t)target; memcpy(fn + 6, &t, 8);
    }
    shim_set_page_prot(fn, 32, PAGE_EXECUTE_READ);
    uint8_t before[32]; memcpy(before, fn, 32);

    DllMain((HANDLE)0x1234, DLL_PROCESS_ATTACH, NULL);
    void* mod = start_mod();
    CHECK(memcmp(fn, before, 32) == 0, "already-hooked function was changed");
    uninstall_mod(mod);
    CHECK(memcmp(fn, before, 32) == 0, "already-hooked function was changed on unload");
    read_log();
    if (shortJump) CHECK(strstr(logText, "already hooked by: (unknown code, not a DLL file)") != NULL, "unknown hook not reported");
    else           CHECK(strstr(logText, "already hooked by: C:\\Games\\ue4ss\\Mods\\OldLoader\\dlls\\main.dll") != NULL, "other loader not named");
    CHECK(strstr(logText, "This copy changes nothing.") != NULL, "no 'changes nothing' line");
}
static void scenario_other_loader_abs(void)   { scenario_other_loader(0); }
static void scenario_other_loader_short(void) { scenario_other_loader(1); }

/* N: UE4SS starts the mod twice (mods.txt and enabled.txt) */
static void scenario_double_start(void)
{
    setup(0);
    mkdirs("M"); mkutoc("M/m.utoc");
    DllMain((HANDLE)0x1234, DLL_PROCESS_ATTACH, NULL);
    void* a = start_mod();
    void* b = start_mod();
    CHECK(a && b && a != b, "two mod objects expected");
    char p[1024];
    snprintf(p, sizeof(p), "%s/../../Content/Paks/global.utoc", root);            game_mounts(p, 3);
    snprintf(p, sizeof(p), "%s/../../Content/Paks/pakchunk0-Windows.utoc", root); game_mounts(p, 4);
    CHECK(count_mod_calls() == 1, "mod mounted %d times", count_mod_calls());
    uninstall_mod(a);
    CHECK(memcmp(fn, origFn, 14) != 0, "hook removed while the second object is alive");
    uninstall_mod(b);
    CHECK(memcmp(fn, origFn, 32) == 0, "hook not removed after the last object");
    common_end_checks();
    read_log();
    char* first = strstr(logText, "AC8 mod loader 1.3.0");
    CHECK(first && !strstr(first + 1, "AC8 mod loader 1.3.0"), "installed twice");
}


/* ---- T: new files - each mod container handed to the game's package store ---------- */
static void* pak_mounts(const char* posixPath, int order)
{
    static const uint8_t guid[16] = {1,2,3}, key[32] = {4,5,6};
    wchar_t w[1024];
    char    p[1024];
    snprintf(p, sizeof(p), "%s", posixPath);
    for (char* q = p; *q; ++q) if (*q == '/') *q = '\\';
    size_t n = u8_to_u16(p, strlen(p), w, 1023);
    w[n] = 0;
    return ((PakMountFn)(void*)pakMountAt)(pakObj, stockOut, w, order, guid, key);
}

static THdr skinA, skinB;
static void two_skin_mods(void)
{
    memset(&skinA, 0, sizeof(skinA)); memset(&skinB, 0, sizeof(skinB));
    skinA.cid = 0xA1A1; skinB.cid = 0xB2B2;               /* empty headers: the sync skips them */
    register_header("/SkinA/a.utoc", &skinA);
    register_header("/SkinB/b.utoc", &skinB);
    mkdirs("SkinA"); mkutoc("SkinA/a.utoc");
    mkdirs("SkinB"); mkutoc("SkinB/b.utoc");
}

static uint32_t entry_u32(int i, int off) { uint32_t v; memcpy(&v, ((TA*)(backendObj + 0x38))->data + (size_t)i * 32 + off, 4); return v; }
static void*    entry_hdr(int i)          { void* v; memcpy(&v, ((TA*)(backendObj + 0x38))->data + (size_t)i * 32, 8); return v; }

static void run_with(void* (*mountFn)(const char*, int), void** modOut)
{
    DllMain((HANDLE)0x1234, DLL_PROCESS_ATTACH, NULL);
    *modOut = start_mod();
    char p[1024];
    snprintf(p, sizeof(p), "%s/../../Content/Paks/global.utoc", root);            mountFn(p, 3);
    snprintf(p, sizeof(p), "%s/../../Content/Paks/pakchunk0-Windows.utoc", root); mountFn(p, 4);
}

static void scenario_new_files(void)
{
    setup(0);
    init_backend();
    two_skin_mods();
    void* mod;
    run_with(pak_mounts, &mod);
    expect_call(0, "/Paks/global.utoc", 3);
    expect_call(1, "/Paks/pakchunk0-Windows.utoc", 4);
    expect_call(2, "/SkinA/a.utoc", 1000);
    expect_call(3, "/SkinB/b.utoc", 1001);
    TA* t = (TA*)(backendObj + 0x38);
    CHECK(t->num == 4, "store has %d containers, expected 4", t->num);
    CHECK(growCalls == 1, "array grown %d times", growCalls);
    CHECK(sortCalls == 2 && sortUnlocked == 0, "sorted %d times (%d outside the lock)", sortCalls, sortUnlocked);
    CHECK(lockCount == 2 && unlockCount == 2 && !lockedNow, "lock %d unlock %d", lockCount, unlockCount);
    CHECK(backendObj[0x118] == 1, "container update flag not set");
    CHECK(entry_u32(0, 8) == 1001 && entry_u32(1, 8) == 1000 && entry_u32(2, 8) == 3 && entry_u32(3, 8) == 0,
          "order %u %u %u %u", entry_u32(0, 8), entry_u32(1, 8), entry_u32(2, 8), entry_u32(3, 8));
    CHECK(entry_u32(1, 12) == 2 && entry_u32(0, 12) == 3, "sequence %u %u", entry_u32(1, 12), entry_u32(0, 12));
    int32_t next; memcpy(&next, backendObj + 0x48, 4);
    CHECK(next == 4, "next sequence %d", next);
    const uint8_t* hb = entry_hdr(0), *ha = entry_hdr(1);
    CHECK(ha && memcmp(ha, &skinA, sizeof(THdr)) == 0, "SkinA's header not handed over");
    CHECK(hb && memcmp(hb, &skinB, sizeof(THdr)) == 0, "SkinB's header not handed over");
    CHECK(ha != hb && (const uint8_t*)ha != stockOut + 0x108, "header copies not separate");
    CHECK(entry_hdr(2) == (void*)0x111100 && entry_hdr(3) == (void*)0x222200, "the game's own entries changed");
    uint64_t z[2] = {0, 0};
    CHECK(memcmp(t->data + 16, z, 16) == 0 && memcmp(t->data + 48, z, 16) == 0, "entry tails not zero");
    uninstall_mod(mod);
    common_end_checks();
    read_log();
    CHECK(strstr(logText, "new files: the game's package store code found at") != NULL, "code not found");
    CHECK(strstr(logText, "new files: the game's package store was found; each mod is handed to it") != NULL, "store line");
    CHECK(strstr(logText, "new files: 2 of 2 containers handed to the game's package store") != NULL, "summary line");
}

/* T2: a game build without that code: mods mount as before, the store is left alone */
static void scenario_new_files_no_code(void)
{
    noStoreReg = 1;
    setup(0);
    init_backend();
    two_skin_mods();
    void* mod;
    run_with(game_mounts, &mod);
    expect_call(2, "/SkinA/a.utoc", 1000);
    expect_call(3, "/SkinB/b.utoc", 1001);
    CHECK(((TA*)(backendObj + 0x38))->num == 2 && lockCount == 0, "store touched");
    uninstall_mod(mod);
    common_end_checks();
    read_log();
    CHECK(strstr(logText, "new files: the game's package store code was found 0 times (expected once) - mods can replace game files but not add new ones") != NULL, "missing-code line");
    CHECK(strstr(logText, "new files: the game's package store code was not found - mods can replace game files but not add new ones") != NULL, "mount-time line");
}

/* T3: pakchunk0 mounted from somewhere else: no guessing */
static void scenario_new_files_other_caller(void)
{
    setup(0);
    init_backend();
    two_skin_mods();
    void* mod;
    run_with(game_mounts, &mod);
    expect_call(2, "/SkinA/a.utoc", 1000);
    CHECK(((TA*)(backendObj + 0x38))->num == 2 && lockCount == 0, "store touched");
    uninstall_mod(mod);
    common_end_checks();
    read_log();
    CHECK(strstr(logText, "new files: the game mounted pakchunk0 from a place this loader does not know - mods can replace game files but not add new ones") != NULL, "line");
}

/* T4: one mod container fails to mount: only the ones that mounted are handed over */
static void scenario_new_files_one_bad(void)
{
    setup(0);
    init_backend();
    two_skin_mods();
    mkdirs("Broken"); mkutoc("Broken/bad.utoc");
    void* mod;
    run_with(pak_mounts, &mod);
    expect_call(2, "/Broken/bad.utoc", 1000);
    expect_call(3, "/SkinA/a.utoc", 1001);
    expect_call(4, "/SkinB/b.utoc", 1002);
    TA* t = (TA*)(backendObj + 0x38);
    CHECK(t->num == 4 && lockCount == 2, "store has %d containers after %d locks", t->num, lockCount);
    CHECK(entry_u32(0, 8) == 1002 && entry_u32(1, 8) == 1001, "order %u %u", entry_u32(0, 8), entry_u32(1, 8));
    uninstall_mod(mod);
    common_end_checks();
    read_log();
    CHECK(strstr(logText, "new files: 2 of 2 containers handed to the game's package store") != NULL, "summary line");
}

/* T5: the store does not look like one (flag byte not 0/1, or unreadable) */
static void scenario_new_files_odd_store(int variant)
{
    setup(0);
    init_backend();
    if (variant == 0) backendObj[0x118] = 7;
    else {
        /* the pak object points at memory that cannot be read */
        void* bad = (void*)(uintptr_t)0x7f0000010000ull;
        memcpy(pakObj + 0xE0, &bad, 8);
        shim_noaccess_lo = 0x7f0000010000ull; shim_noaccess_hi = 0x7f0000020000ull;
    }
    two_skin_mods();
    void* mod;
    run_with(pak_mounts, &mod);
    expect_call(2, "/SkinA/a.utoc", 1000);
    CHECK(((TA*)(backendObj + 0x38))->num == 2 && lockCount == 0, "store touched");
    uninstall_mod(mod);
    common_end_checks();
    read_log();
    CHECK(strstr(logText, "new files: the game's package store did not look as expected - mods can replace game files but not add new ones") != NULL, "line");
}
static void scenario_new_files_odd_store_0(void) { scenario_new_files_odd_store(0); }
static void scenario_new_files_odd_store_1(void) { scenario_new_files_odd_store(1); }

/* ---- O: package store sync ---------------------------------------------------------- */
typedef struct { int n; uint64_t* ids; uint32_t* cnt; uint64_t** lists; THdr hdr; uint8_t* ent; } Model;
static uint64_t rng = 88172645463325252ull;
static uint64_t rnd(void) { rng ^= rng << 13; rng ^= rng >> 7; rng ^= rng << 17; return rng; }

static Model make_model(int n, uint64_t idBase, uint64_t idStep, int maxImp)
{
    Model m; memset(&m, 0, sizeof(m));
    m.n = n; m.ids = calloc(n, 8); m.cnt = calloc(n, 4); m.lists = calloc(n, sizeof(uint64_t*));
    for (int i = 0; i < n; i++) {
        m.ids[i] = idBase + (uint64_t)i * idStep;
        m.cnt[i] = (uint32_t)(rnd() % (maxImp + 1));
        m.lists[i] = calloc(m.cnt[i] + 1, 8);
        for (uint32_t j = 0; j < m.cnt[i]; j++) m.lists[i][j] = rnd();
    }
    return m;
}

/* lays the model out the way the game does: entries, then the lists */
static void build_header(Model* m)
{
    size_t bytes = (size_t)m->n * 16;
    for (int i = 0; i < m->n; i++) bytes += (size_t)m->cnt[i] * 8;
    uint8_t* ent = mmap(NULL, bytes + 4096, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    uint64_t* ids = malloc((size_t)m->n * 8);
    size_t off = (size_t)m->n * 16;
    for (int i = 0; i < m->n; i++) {
        uint32_t* e = (uint32_t*)(ent + (size_t)i * 16);
        e[0] = m->cnt[i]; e[1] = m->cnt[i] ? (uint32_t)(off - (size_t)i * 16) : 0; e[2] = 0; e[3] = 0;
        memcpy(ent + off, m->lists[i], (size_t)m->cnt[i] * 8);
        off += (size_t)m->cnt[i] * 8;
        ids[i] = m->ids[i];
    }
    m->ent = ent;
    m->hdr.cid = 0x1234; m->hdr.idsData = ids; m->hdr.idsNum = m->hdr.idsMax = m->n;
    m->hdr.entData = ent; m->hdr.entNum = m->hdr.entMax = (int32_t)bytes;
}


/* what the game would now read for package k of a base container */
static int entry_is(const Model* b, int k, const uint64_t* want, uint32_t n)
{
    const uint32_t* e = (const uint32_t*)(b->ent + (size_t)k * 16);
    if (e[0] != n) return 0;
    const uint64_t* l = (const uint64_t*)((const uint8_t*)e + e[1]);
    for (uint32_t i = 0; i < n; i++) if (l[i] != want[i]) return 0;
    return 1;
}

static void scenario_store_sync(void)
{
    setup(0);
    Model b1 = make_model(3000, 0x1000, 16, 12);
    Model b2 = make_model(400, 0x900000, 16, 8);            /* mounted by the game after the mods */

    int iA = 100, iB = 200, iC = 300, iH = 0, iD = 400, iE = 500;
    while (b1.cnt[iA] < 2) iA++;
    while (b1.cnt[iB] < 3) iB++;
    while (b1.cnt[iC] < 1 || b1.cnt[iC] > 6) iC++;
    for (iH = 1000; b1.cnt[iH] <= b1.cnt[iC] || b1.cnt[iH] < 2; iH++) {}
    while (b1.cnt[iD] < 1 || b1.cnt[iD] > 6) iD++;
    while (b1.cnt[iE] < 1) iE++;
    int iF = 7; while (b2.cnt[iF] < 1) iF++;

    /* mod 1: A same length new ids, B shorter, C longer (= a list that exists: reuse),
              D longer, brand new (fresh memory), E identical, F only in the late container */
    Model m1; memset(&m1, 0, sizeof(m1));
    m1.n = 6; m1.ids = calloc(6, 8); m1.cnt = calloc(6, 4); m1.lists = calloc(6, sizeof(uint64_t*));
    uint64_t idF = b2.ids[iF];
    int src[6] = { iA, iB, iC, iD, iE, -1 };
    for (int k = 0; k < 6; k++) {
        m1.ids[k] = k < 5 ? b1.ids[src[k]] : idF;
        uint32_t base = k < 5 ? b1.cnt[src[k]] : b2.cnt[iF];
        uint32_t n = k == 1 ? base - 1 : k == 2 ? b1.cnt[iH] : k == 3 ? base + 5 : base;
        m1.cnt[k] = n; m1.lists[k] = calloc(n + 1, 8);
        for (uint32_t j = 0; j < n; j++)
            m1.lists[k][j] = k == 2 ? b1.lists[iH][j] : k == 4 ? b1.lists[iE][j] : 0xAB000000ull + (uint64_t)k * 100 + j;
    }
    /* ids sorted like the game's: A<B<C<D<E<F already */

    Model m2; memset(&m2, 0, sizeof(m2));                  /* later mod: wins package A */
    m2.n = 1; m2.ids = calloc(1, 8); m2.cnt = calloc(1, 4); m2.lists = calloc(1, sizeof(uint64_t*));
    m2.ids[0] = b1.ids[iA]; m2.cnt[0] = m1.cnt[0]; m2.lists[0] = calloc(m2.cnt[0], 8);
    for (uint32_t j = 0; j < m2.cnt[0]; j++) m2.lists[0][j] = 0xCD000000ull + j;

    Model mb = m2;                                         /* broken header: refused */
    build_header(&b1); build_header(&b2); build_header(&m1); build_header(&m2); build_header(&mb);
    ((uint32_t*)mb.ent)[1] = 0x7FFFFFFF;

    /* copies of the untouched base lists, to prove nothing else moved */
    uint8_t* before = malloc((size_t)b1.hdr.entNum); memcpy(before, b1.ent, (size_t)b1.hdr.entNum);

    register_header("/Paks/pakchunk0-Windows.utoc", &b1.hdr);
    register_header("/Paks/pakchunk1-Windows.utoc", &b2.hdr);
    register_header("/SyncMod/sync.utoc", &m1.hdr);
    register_header("/SyncMod2/sync2.utoc", &m2.hdr);
    register_header("/ABroken/broken.utoc", &mb.hdr);
    mkdirs("SyncMod");  mkutoc("SyncMod/sync.utoc");
    mkdirs("SyncMod2"); mkutoc("SyncMod2/sync2.utoc");
    mkdirs("ABroken");  mkutoc("ABroken/broken.utoc");

    void* mod;
    start_and_mount_pakchunk0(&mod);
    expect_call(2, "/ABroken/broken.utoc", 1000);
    expect_call(3, "/SyncMod/sync.utoc", 1001);
    expect_call(4, "/SyncMod2/sync2.utoc", 1002);

    CHECK(entry_is(&b1, iA, m2.lists[0], m2.cnt[0]), "A: the later mod should win");
    CHECK(entry_is(&b1, iB, m1.lists[1], m1.cnt[1]), "B (shorter) not applied");
    CHECK(entry_is(&b1, iC, m1.lists[2], m1.cnt[2]), "C (longer, reuse) not applied");
    CHECK(entry_is(&b1, iH, b1.lists[iH], b1.cnt[iH]), "H (the list C now shares) was changed");
    CHECK(entry_is(&b1, iD, m1.lists[3], m1.cnt[3]), "D (longer, fresh memory) not applied");
    CHECK(entry_is(&b1, iE, b1.lists[iE], b1.cnt[iE]), "E changed");
    {
        const uint32_t* eC = (const uint32_t*)(b1.ent + (size_t)iC * 16);
        const uint8_t*  lC = (const uint8_t*)eC + eC[1];
        CHECK(lC > b1.ent && lC < b1.ent + b1.hdr.entNum, "C should point at the existing list inside the block");
        const uint32_t* eD = (const uint32_t*)(b1.ent + (size_t)iD * 16);
        const uint8_t*  lD = (const uint8_t*)eD + eD[1];
        CHECK(lD >= b1.ent + b1.hdr.entNum && lD < b1.ent + 0xE0000000ull, "D should point at new memory above the block");
    }
    int touched = 0;
    for (int k = 0; k < b1.n; k++) {
        if (k == iA || k == iB || k == iC || k == iD) continue;
        if (memcmp(b1.ent + (size_t)k * 16, before + (size_t)k * 16, 16) != 0) touched++;
    }
    CHECK(touched == 0, "%d unrelated entries changed", touched);
    CHECK(memcmp(b1.ids, b1.hdr.idsData, 8) == 0, "package ids changed");

    /* the game mounts another container after the mods */
    char p[1024];
    snprintf(p, sizeof(p), "%s/../../Content/Paks/pakchunk1-Windows.utoc", root);
    game_mounts(p, 5);
    CHECK(entry_is(&b2, iF, m1.lists[5], m1.cnt[5]), "F in the late container not applied");

    uninstall_mod(mod);
    common_end_checks();
    read_log();
    CHECK(strstr(logText, "package store: mod container header not recognised - import lists not updated") != NULL, "broken header not refused");
    CHECK(strstr(logText, "package store: 6 packages, 4 import lists updated, 1 already matching, 1 not in the base game") != NULL, "mod 1 summary");
    CHECK(strstr(logText, "package store: 2 lists needed more room (1 reuse an identical list already in memory, 1 got new memory)") != NULL, "growth line");
    CHECK(strstr(logText, "package store: 1 packages, 1 import lists updated, 0 already matching, 0 not in the base game") != NULL, "mod 2 summary");
    CHECK(strstr(logText, "package store: 1 import lists updated in") != NULL && strstr(logText, "(mounted after the mods)") != NULL, "late container line");
    CHECK(shim_rw_allocs >= 2, "expected the pool and one arena allocation (%d)", shim_rw_allocs);
}

/* P: a base container header that does not look right is reported and left alone */
static void scenario_store_bad_base(void)
{
    setup(0);
    Model b = make_model(50, 0x10, 16, 4);
    build_header(&b);
    THdr bad = b.hdr; bad.entNum = 7;                      /* fewer bytes than entries */
    register_header("/Paks/pakchunk0-Windows.utoc", &bad);
    mkdirs("M"); mkutoc("M/m.utoc");
    void* mod;
    start_and_mount_pakchunk0(&mod);
    expect_call(2, "/M/m.utoc", 1000);
    uninstall_mod(mod);
    common_end_checks();
    read_log();
    CHECK(strstr(logText, "layout of") && strstr(logText, "pakchunk0-Windows.utoc not recognised"), "bad base not reported");
}

static const struct { const char* name; void (*run)(void); } scenarios[] = {
    { "A default order, full life cycle",        scenario_default_order      },
    { "B loadorder.txt UTF-8 (BOM, CRLF)",       scenario_loadorder_utf8     },
    { "C loadorder.txt UTF-16, non-ASCII names", scenario_loadorder_utf16    },
    { "D all mods switched off",                 scenario_all_disabled       },
    { "E ambiguous pattern (wrong game build)",  scenario_ambiguous_pattern  },
    { "F one container fails to mount",          scenario_one_bad_container  },
    { "G no mod folders",                        scenario_no_mods            },
    { "H mods inside extra folders, tool folders skipped", scenario_nested   },
    { "I .utoc without its .ucas",               scenario_missing_ucas       },
    { "J same container in two places",          scenario_duplicates         },
    { "K game's ~mods folder",                   scenario_tilde_mods_plain   },
    { "K2 ~mods with loadorder.txt",             scenario_tilde_mods_order   },
    { "L hooked after global: waits for pakchunk0", scenario_late_0          },
    { "L2 normal start ignores early pakchunk1", scenario_late_1             },
    { "L3 started late: mounts at the next one", scenario_late_2             },
    { "L4 ~mods seen first is not late",         scenario_late_3             },
    { "L5 late, first one failed",               scenario_late_4             },
    { "Q game mounted a ~mods container itself", scenario_game_mounted_tilde },
    { "R links inside mod folders",              scenario_links              },
    { "S UE4SS restarts the mod",                scenario_restart            },
    { "M another loader hooked first (jmp [rip])", scenario_other_loader_abs },
    { "M2 another hook (jmp rel32)",             scenario_other_loader_short },
    { "N started twice by UE4SS",                scenario_double_start       },
    { "O package store sync",                    scenario_store_sync         },
    { "P package store: odd base header",        scenario_store_bad_base     },
    { "T new files: mods handed to the package store", scenario_new_files    },
    { "T2 new files: game code not there",       scenario_new_files_no_code  },
    { "T3 new files: pakchunk0 from elsewhere",  scenario_new_files_other_caller },
    { "T4 new files: one container fails",       scenario_new_files_one_bad  },
    { "T5 new files: store looks wrong",         scenario_new_files_odd_store_0 },
    { "T6 new files: store unreadable",          scenario_new_files_odd_store_1 },
};

/* --dry <loader folder> <Paks folder>: run the loader against a real folder tree
 * (the game is simulated) and print its log. Nothing in the tree is changed
 * except the loader's own log file.                                          */
static int dry_run(const char* loaderDir, const char* paksDir)
{
    snprintf(root, sizeof(root), "%s", loaderDir);
    char modPath[1024];
    snprintf(modPath, sizeof(modPath), "%s/dlls/main.dll", root);
    for (char* p = modPath; *p; ++p) if (*p == '/') *p = '\\';
    shim_module_path[u8_to_u16(modPath, strlen(modPath), shim_module_path, 1023)] = 0;
    build_image(0);
    DllMain((HANDLE)0x1234, DLL_PROCESS_ATTACH, NULL);
    void* mod = start_mod();
    char p[1024];
    snprintf(p, sizeof(p), "%s/global.utoc", paksDir);            game_mounts(p, 3);
    snprintf(p, sizeof(p), "%s/pakchunk0-Windows.utoc", paksDir); game_mounts(p, 4);
    uninstall_mod(mod);
    read_log();
    fputs(logText, stdout);
    return 0;
}

int main(int argc, char** argv)
{
    if (argc == 4 && strcmp(argv[1], "--dry") == 0)
        return dry_run(argv[2], argv[3]);
    int showLog = argc > 1 && strcmp(argv[1], "--log") == 0;
    int bad = 0;

    for (size_t i = 0; i < sizeof(scenarios) / sizeof(scenarios[0]); ++i) {
        fflush(stdout);
        pid_t pid = fork();
        if (pid == 0) {
            scenarios[i].run();
            if (showLog && i == 0) { read_log(); printf("---- log of scenario A ----\n%s---------------------------\n", logText); }
            fflush(stdout);
            _exit(failures ? 1 : 0);
        }
        int status = 0;
        waitpid(pid, &status, 0);
        int ok = WIFEXITED(status) && WEXITSTATUS(status) == 0;
        if (WIFSIGNALED(status)) printf("    CRASH: signal %d\n", WTERMSIG(status));
        printf("%s  %s\n", ok ? "PASS" : "FAIL", scenarios[i].name);
        if (!ok) bad++;
    }
    printf("\n%d scenario(s) failed\n", bad);
    return bad ? 1 : 0;
}
