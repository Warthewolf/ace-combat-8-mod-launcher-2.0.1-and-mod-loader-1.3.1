/* =============================================================================
 *  AC8 Mod Loader 1.3.0  --  UE4SS C++ mod (main.dll)
 *
 *  Based on the single-mod "IoStoreLoaderMod 0.1.0-ac8-1.1.2.0-ue5.4" DLL.
 *  The IoStore hook target and container mounting interface remain the same.
 *  1.2.0 also hooks the Legacy Pak constructor: only *_P.pak gets signedMode=0.
 *  This hook must be installed before the game's native Pak scan reaches it;
 *  1.3.0 scans and actively mounts *_P.pak through Legacy Mount. Paired UTOC
 *  files belong to that Pak and are not mounted/registered a second time.
 *  1.2.1 also covers native *_P.utoc using
 *  the same scoped signature policy as the existing explicit mod mount plan.
 *
 *  1.0.0 and later are meant to be the one build that every AC8 mod can rely on:
 *
 *    - New files (1.1.0): every mod container is also handed to the game's
 *      package store, the way the game hands over its own containers. Until
 *      then the store only knew the game's own files, so a mod could replace
 *      files but a file that is new to the game (a new skin's material,
 *      texture or icon) could not be found and did not load.
 *    - Package store sync (was a binary patch on 0.2.0, called 0.3.x): after a
 *      mod container is mounted, each replaced package's list of imported
 *      packages is copied into the game's package store. Without it, a mod that
 *      changes what a file imports fails to load (e.g. an aircraft that now
 *      needs a different weapon).
 *    - Mods are found wherever people tend to put them:
 *         <loader folder>\<Mod>\...any depth...\*.utoc
 *         <loader folder>\*.utoc                       (loose files)
 *         <game>\Content\Paks\~mods\<Mod>\...\*.utoc   (the usual UE place)
 *         <game>\Content\Paks\~mods\*.utoc
 *      Folders that hold tool output or old copies are not searched:
 *      dlls, Scripts, source, build, packed, export, backup(s), old, disabled,
 *      removed-mods.
 *    - A container whose name was already seen in another folder is loaded
 *      once (the newest copy); a .utoc without its .ucas is skipped. Both are
 *      written to the log.
 *    - loadorder.txt is optional. Without it every mod loads, A to Z, loader
 *      folder first, then ~mods.
 *    - If UE4SS started the loader after the game had already mounted
 *      pakchunk0, the mods are mounted with the next container the game mounts
 *      instead of not at all.
 *    - A container the game has already mounted itself is not mounted again.
 *    - If another copy of a loader has already hooked the game, this copy
 *      does nothing and the log names the other copy's file.
 *
 *  Folder layout:
 *      <UE4SS Mods>\IoStoreLoaderMod\dlls\main.dll         this file
 *      <UE4SS Mods>\IoStoreLoaderMod\enabled.txt           makes UE4SS start it
 *      <UE4SS Mods>\IoStoreLoaderMod\loadorder.txt         optional
 *      <UE4SS Mods>\IoStoreLoaderMod\AC8IoStoreLoader.log  written at every start
 *
 *  loadorder.txt: one mod per line, "+Name" = on, "-Name" = off, "#" = comment.
 *  Name is the folder name, a loose file's name ("x.utoc"), or either of those
 *  with "~mods\" in front. Top of the file is mounted first; later mods get a
 *  higher mount order and win conflicts. Mods that are not listed are mounted
 *  after the listed ones, in the default order.
 *
 *  Written in plain C with no C runtime and no Windows SDK, so it builds with
 *  clang + lld-link alone (build.sh), or MinGW GCC + VS lib/link (build.ps1).
 *  The UE4SS base class is used through
 *  its two exported functions and a hand-laid-out object.
 * ============================================================================= */

#include "win32_min.h"

#define LOADER_VERSION   L"1.3.1"
#define PATH_CAP         1024          /* wide characters per path buffer        */
#define BASE_MOUNT_ORDER 1000          /* first mod; each next container is +1   */
#define MAX_SEARCH_DEPTH 6             /* folder levels searched inside a mod    */

#ifdef _WIN32
#  define EXPORT __declspec(dllexport)
#else
#  define EXPORT
#endif

void* memset(void* dst, int c, size_t n);
void* memcpy(void* dst, const void* src, size_t n);
int   memcmp(const void* a, const void* b, size_t n);

#ifdef _WIN32
/* No C runtime is linked, so these three have to exist here. */
void* memset(void* dst, int c, size_t n)
{
    volatile uint8_t* d = (volatile uint8_t*)dst;
    while (n--) *d++ = (uint8_t)c;
    return dst;
}
void* memcpy(void* dst, const void* src, size_t n)
{
    volatile uint8_t* d = (volatile uint8_t*)dst;
    const uint8_t*    s = (const uint8_t*)src;
    while (n--) *d++ = *s++;
    return dst;
}
int memcmp(const void* a, const void* b, size_t n)
{
    const uint8_t* x = (const uint8_t*)a;
    const uint8_t* y = (const uint8_t*)b;
    for (; n; --n, ++x, ++y)
        if (*x != *y) return (int)*x - (int)*y;
    return 0;
}
#endif

/* -----------------------------------------------------------------------------
 *  Byte patterns in the game executable (unchanged from the original DLL)
 * ----------------------------------------------------------------------------- */

/* Start of the game's IoStore container mount function, 29 bytes, exact:
 *   +0  40 55                    push rbp
 *   +2  53                       push rbx
 *   +3  56                       push rsi
 *   +4  57                       push rdi
 *   +5  41 54                    push r12
 *   +7  41 55                    push r13
 *   +9  41 57                    push r15
 *   +11 48 8D AC 24 F0 FC FF FF  lea  rbp,[rsp-0x310]
 *   +19 48 81 EC 10 04 00 00     sub  rsp,0x410
 *   +26 48 8B 05 ...             mov  rax,[rip+...]                            */
static const uint8_t kMountSig[29] = {
    0x40,0x55,0x53,0x56,0x57,0x41,0x54,0x41,0x55,0x41,0x57,
    0x48,0x8D,0xAC,0x24,0xF0,0xFC,0xFF,0xFF,
    0x48,0x81,0xEC,0x10,0x04,0x00,0x00,
    0x48,0x8B,0x05
};
/* The first 19 bytes are whole instructions with nothing RIP-relative in them,
 * so they can be moved into a trampoline as-is.                               */
#define HOOK_COPY_LEN   19
#define HOOK_PATCH_LEN  14             /* jmp [rip+0] ; dq target               */

/* Legacy Pak constructor, verified at RVA 0x027F6C80 in AC8 1.1.2.0.
 * The first 15 bytes are three whole mov [rsp+...],reg instructions.
 * Everything through +28 is position independent; the next instruction is
 * RIP-relative and must NOT be copied without relocation. */
static const uint8_t kPakCtorSig[67] = {
    0x48,0x89,0x5C,0x24,0x10, 0x48,0x89,0x6C,0x24,0x18,
    0x48,0x89,0x74,0x24,0x20, 0x57,0x41,0x54,0x41,0x55,
    0x41,0x56,0x41,0x57, 0x48,0x83,0xEC,0x30, 0x48,0x8D,0x05,
    0,0,0,0,                          /* +31..34 RIP displacement, wildcard */
    0x4C,0x8B,0xEA, 0x48,0x89,0x01, 0x4C,0x8B,0xF1, 0x48,0x8D,0x05,
    0,0,0,0,                          /* +47..50 RIP displacement, wildcard */
    0x33,0xFF, 0x48,0x89,0x41,0x10, 0x49,0x8B,0xD0, 0x89,0x79,0x08,
    0x41,0x0F,0xB6,0xF1              /* movzx esi,r9b */
};
#define PAK_CTOR_COPY_LEN 15

/* Legacy Mount, RVA 0x02800260. Unlike the IoStore ABI, argument 2 is the
 * Pak path. Only +31..34 is RIP-relative. Read-only lookup: no third hook.
 * The native IoStore call returns at +0x85C, with this object in R14. */
static const uint8_t kPakMountSig[] = {
    0x40,0x55,0x53,0x56,0x57,0x41,0x54,0x41,0x55,0x41,0x56,0x41,0x57,
    0x48,0x8D,0xAC,0x24,0xC8,0xFC,0xFF,0xFF,
    0x48,0x81,0xEC,0x38,0x04,0x00,0x00,0x48,0x8B,0x05,0,0,0,0,
    0x48,0x33,0xC4,0x48,0x89,0x85,0x28,0x03,0,0,
    0x48,0x8B,0x85,0xA8,0x03,0,0,0x48,0x8B,0xD9,0x48,0x89,0x4D,0xB8,
    0x4D,0x8B,0xF1,0x48,0x8B,0x49,0x08,0x45,0x8B,0xF8
};
#define PAK_IOSTORE_RETURN 0x85C

/* "Does this container need a signature?" check, 35 bytes, bytes 1..4 wild:
 *   call ???? ; cmp [rax+8],r14d ; je .. ; mov rcx,[rax] ; test rcx,rcx ; je ..
 *   mov rax,[rcx] ; call [rax+0x30] ; test al,al
 *   +0x1B  74 44   je +0x44        <- patched to EB (jmp) while mods are mounted
 *   test dil,4 ; jne ..                                                        */
static const uint8_t kSigCheck[35] = {
    0xE8,0x00,0x00,0x00,0x00, 0x44,0x39,0x70,0x08, 0x74,0x56,
    0x48,0x8B,0x08, 0x48,0x85,0xC9, 0x74,0x4E,
    0x48,0x8B,0x01, 0xFF,0x50,0x30, 0x84,0xC0,
    0x74,0x44, 0x40,0xF6,0xC7,0x04, 0x75,0x48
};
#define SIG_BRANCH_OFFSET 0x1B

/* -----------------------------------------------------------------------------
 *  Globals
 * ----------------------------------------------------------------------------- */

/* Parameter meanings are inferred from the original DLL: (this, result buffer,
 * .utoc path, mount order, 16-byte key GUID, 32-byte AES key). The result is a
 * TIoStatusOr<FIoContainerHeader>: an int error code and 128 wide characters of
 * message text, then (from +0x108) the container header.                      */
typedef void* (WINAPI *MountFn)(void* self, void* outResult, const wchar_t* tocPath,
                                int32_t order, const void* keyGuid, const void* aesKey);
typedef void* (WINAPI *PakCtorFn)(void* self, void* innerPlatformFile,
                                 const wchar_t* pakPath, uint8_t signedMode,
                                 uint8_t parserFlag);
typedef uint8_t (WINAPI *PakMountFn)(void* self, const wchar_t* pakPath,
    int32_t order, const wchar_t* mountPoint, uint8_t parserFlag, void* outRef);
static PakMountFn g_pakMount;
static uint8_t* g_platformFile; /* captured from a verified native call, never fabricated */
static int g_pendingPaks; /* retry only deferred Paks after a native object becomes known */

static HANDLE   g_hModule;
static HANDLE   g_log = INVALID_HANDLE_VALUE;
static wchar_t  g_modRoot[PATH_CAP];           /* folder that contains "dlls"   */

static volatile int32_t g_state;               /* 0 waiting, 1 mounting, 2 done */
static volatile int32_t g_logLock;
static volatile int32_t g_stockSeen;           /* game mounts seen by the hook  */
static volatile int32_t g_startedLate;         /* first one seen was not global */
static int              g_instances;           /* start_mod calls still alive   */
static int              g_installRuns;         /* Install calls in this process */

static MountFn  g_origMount;                   /* trampoline into the game      */
static uint8_t* g_mountFn;                     /* hooked address                */
static uint8_t  g_savedBytes[HOOK_PATCH_LEN];
static int      g_hooked;
static uint8_t* g_sigBranch;
/* Serialize hooked mounts and the explicit plan: the signature byte is shared
 * game code. Thread ownership permits synchronous nested mounts; each nested
 * call restores its caller's policy, including stock calls inside a mod mount. */
static volatile DWORD g_mountOwner;
static unsigned       g_mountDepth;
static int            g_mountEnabled;
static uint8_t        g_signaturePolicy = 0x74;
static PakCtorFn g_origPakCtor;
static uint8_t*  g_pakCtorFn;
static uint8_t   g_pakSavedBytes[HOOK_PATCH_LEN];
static int       g_pakHooked;

static uint8_t  g_zeroGuid[16];
static uint8_t  g_zeroKey[32];
static uint8_t  g_resultBuf[0x1000] __attribute__((aligned(16)));

/* -----------------------------------------------------------------------------
 *  Small string helpers (UTF-16)
 * ----------------------------------------------------------------------------- */
static size_t WLen(const wchar_t* s)
{
    size_t n = 0;
    while (s[n]) ++n;
    return n;
}

/* Upper-case folding, so "A to Z" here means the same as in ModLauncher
 * (which sorts with .NET's OrdinalIgnoreCase).                              */
static wchar_t Fold(wchar_t c)
{
    return (c >= L'a' && c <= L'z') ? (wchar_t)(c - 32) : c;
}

static int WICmp(const wchar_t* a, const wchar_t* b)
{
    for (;; ++a, ++b) {
        wchar_t x = Fold(*a), y = Fold(*b);
        if (x != y) return x < y ? -1 : 1;
        if (!x) return 0;
    }
}

static int WEndsWithI(const wchar_t* s, const wchar_t* suffix)
{
    size_t n = WLen(s), m = WLen(suffix);
    return n >= m && WICmp(s + n - m, suffix) == 0;
}

static wchar_t* WDup(const wchar_t* s, size_t n)       /* copies n chars + NUL */
{
    wchar_t* p = (wchar_t*)malloc((n + 1) * sizeof(wchar_t));
    if (!p) return NULL;
    memcpy(p, s, n * sizeof(wchar_t));
    p[n] = 0;
    return p;
}

/* The part after the last \ or / */
static const wchar_t* FileNamePart(const wchar_t* path)
{
    const wchar_t* name = path;
    for (const wchar_t* p = path; *p; ++p)
        if (*p == L'\\' || *p == L'/') name = p + 1;
    return name;
}

static int IsFileNamed(const wchar_t* path, const wchar_t* name)
{
    return path && WICmp(FileNamePart(path), name) == 0;
}

static int IsAbsolutePath(const wchar_t* p)
{
    return (p[0] && p[1] == L':') || p[0] == L'\\' || p[0] == L'/';
}

/* Tiny formatter. Supports: %s wide string, %d int, %u size_t, %x unsigned hex,
 * %p pointer, %%. Output is always NUL-terminated and never overruns `cap`.
 * Note %u takes a size_t: cast smaller values when passing them.            */
static void VFmt(wchar_t* out, size_t cap, const wchar_t* fmt, va_list ap)
{
    wchar_t* p   = out;
    wchar_t* end = out + cap - 1;
#define PUT(ch) do { if (p < end) *p++ = (wchar_t)(ch); } while (0)

    for (; *fmt; ++fmt) {
        if (*fmt != L'%') { PUT(*fmt); continue; }
        ++fmt;
        if (*fmt == 0) break;

        if (*fmt == L's') {
            const wchar_t* s = va_arg(ap, const wchar_t*);
            if (!s) s = L"<null>";
            while (*s) PUT(*s++);
        } else if (*fmt == L'd' || *fmt == L'u') {
            uint64_t v;
            if (*fmt == L'd') {
                int32_t d = va_arg(ap, int32_t);
                if (d < 0) { PUT(L'-'); v = (uint64_t)(-(int64_t)d); }
                else       { v = (uint64_t)d; }
            } else {
                v = va_arg(ap, size_t);
            }
            wchar_t tmp[24];
            int n = 0;
            do { tmp[n++] = (wchar_t)(L'0' + (v % 10)); v /= 10; } while (v);
            while (n) PUT(tmp[--n]);
        } else if (*fmt == L'x' || *fmt == L'p') {
            uint64_t v;
            int digits;
            if (*fmt == L'p') { v = (uint64_t)(uintptr_t)va_arg(ap, void*); digits = 16; }
            else              { v = va_arg(ap, uint32_t);                    digits = 0;  }
            wchar_t tmp[16];
            int n = 0;
            do { tmp[n++] = L"0123456789ABCDEF"[v & 15]; v >>= 4; } while (v);
            while (n < digits) tmp[n++] = L'0';
            while (n) PUT(tmp[--n]);
        } else {
            PUT(*fmt);
        }
    }
    *p = 0;
#undef PUT
}

static wchar_t* NewPath(const wchar_t* fmt, ...)          /* malloc'd, PATH_CAP */
{
    wchar_t* p = (wchar_t*)malloc(PATH_CAP * sizeof(wchar_t));
    if (!p) return NULL;
    va_list ap;
    va_start(ap, fmt);
    VFmt(p, PATH_CAP, fmt, ap);
    va_end(ap);
    return p;
}

/* -----------------------------------------------------------------------------
 *  Log file: one UTF-8 line per call, flushed immediately
 * ----------------------------------------------------------------------------- */
static void Log(const wchar_t* fmt, ...)
{
    static wchar_t wide[2048];
    static char    utf8[2048 * 3 + 2];

    if (g_log == INVALID_HANDLE_VALUE)
        return;

    while (__sync_lock_test_and_set(&g_logLock, 1))
        Sleep(0);
    if (g_log == INVALID_HANDLE_VALUE) {
        __sync_lock_release(&g_logLock);
        return;
    }

    va_list ap;
    va_start(ap, fmt);
    VFmt(wide, 2048, fmt, ap);
    va_end(ap);

    int n = WideCharToMultiByte(CP_UTF8, 0, wide, (int)WLen(wide),
                                utf8, (int)sizeof(utf8) - 2, NULL, NULL);
    if (n > 0) {
        DWORD written = 0;
        utf8[n++] = '\r';
        utf8[n++] = '\n';
        WriteFile(g_log, utf8, (DWORD)n, &written, NULL);
        FlushFileBuffers(g_log);
    }

    __sync_lock_release(&g_logLock);
}

/* -----------------------------------------------------------------------------
 *  Growable lists
 * ----------------------------------------------------------------------------- */
static int Grow(void** v, size_t* cap, size_t n, size_t elem)
{
    if (n < *cap) return 1;
    size_t newCap = *cap ? *cap * 2 : 16;
    void*  nv     = malloc(newCap * elem);
    if (!nv) return 0;
    if (n) memcpy(nv, *v, n * elem);
    free(*v);
    *v   = nv;
    *cap = newCap;
    return 1;
}

/* -----------------------------------------------------------------------------
 *  File system
 * ----------------------------------------------------------------------------- */
typedef struct {
    wchar_t* name;
    int      isDir;
    int      isLink;     /* junction or symbolic link */
    uint64_t mtime;
} DirEntry;

typedef struct {
    DirEntry* v;
    size_t    n, cap;
} DirList;

static void DL_Free(DirList* l)
{
    for (size_t i = 0; i < l->n; ++i) free(l->v[i].name);
    free(l->v);
    l->v = NULL;
    l->n = l->cap = 0;
}

static void DL_SortI(DirList* l)                         /* A to Z, ignore case */
{
    for (size_t i = 1; i < l->n; ++i) {
        DirEntry key = l->v[i];
        size_t   j   = i;
        while (j > 0 && WICmp(l->v[j - 1].name, key.name) > 0) {
            l->v[j] = l->v[j - 1];
            --j;
        }
        l->v[j] = key;
    }
}

/* UTOC keeps the existing naming policy; only *_P.pak is a custom Legacy Pak. */
static int IsPak(const wchar_t* path) { return WEndsWithI(path, L"_P.pak"); }

static wchar_t* WithExtension(const wchar_t* path, const wchar_t* ext)
{
    size_t n = WLen(path);
    size_t cut = n;
    while (cut && path[cut - 1] != L'.' && path[cut - 1] != L'\\' && path[cut - 1] != L'/') --cut;
    if (!cut || path[cut - 1] != L'.') return NULL;
    wchar_t* stem = WDup(path, cut);
    wchar_t* out = stem ? NewPath(L"%s%s", stem, ext) : NULL;
    free(stem);
    return out;
}

/* A same-directory sidecar belongs to the Pak, including its loadorder switch. */
static int IsPakSidecar(const wchar_t* path)
{
    if (!WEndsWithI(path, L"_P.utoc")) return 0;
    wchar_t* pak = WithExtension(path, L"pak");
    DWORD a = pak ? GetFileAttributesW(pak) : INVALID_FILE_ATTRIBUTES;
    free(pak);
    return a != INVALID_FILE_ATTRIBUTES && !(a & FILE_ATTRIBUTE_DIRECTORY);
}

/* Folders, .utoc and *_P.pak files directly inside `dir`, sorted A to Z. */
static void ListDir(const wchar_t* dir, DirList* out)
{
    wchar_t* pattern = NewPath(L"%s\\*", dir);
    if (!pattern) return;

    WIN32_FIND_DATAW fd;
    memset(&fd, 0, sizeof(fd));
    HANDLE h = FindFirstFileW(pattern, &fd);
    free(pattern);
    if (h == INVALID_HANDLE_VALUE)
        return;

    do {
        const wchar_t* name  = fd.cFileName;
        int            isDir = (fd.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) != 0;

        if (name[0] == L'.' && (name[1] == 0 || (name[1] == L'.' && name[2] == 0))) continue;
        if (!isDir && !WEndsWithI(name, L".utoc") && !IsPak(name)) continue;
        if (!Grow((void**)&out->v, &out->cap, out->n, sizeof(DirEntry))) break;

        wchar_t* copy = WDup(name, WLen(name));
        if (!copy) break;
        out->v[out->n].name  = copy;
        out->v[out->n].isDir = isDir;
        out->v[out->n].isLink = (fd.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT) != 0;
        out->v[out->n].mtime = ((uint64_t)fd.ftLastWriteTime[1] << 32) | fd.ftLastWriteTime[0];
        out->n++;
    } while (FindNextFileW(h, &fd));

    FindClose(h);
    DL_SortI(out);
}

static const wchar_t* const kSkipDirs[] = {
    L"dlls", L"scripts", L"source", L"build", L"packed", L"export",
    L"backup", L"backups", L"old", L"disabled", L"removed-mods",
};

static int IsSkippedDir(const wchar_t* name)
{
    for (size_t i = 0; i < sizeof(kSkipDirs) / sizeof(kSkipDirs[0]); ++i)
        if (WICmp(name, kSkipDirs[i]) == 0) return 1;
    return 0;
}

static int FileExists(const wchar_t* path)
{
    DWORD a = GetFileAttributesW(path);
    return a != INVALID_FILE_ATTRIBUTES && !(a & FILE_ATTRIBUTE_DIRECTORY);
}

static int DirExists(const wchar_t* path)
{
    DWORD a = GetFileAttributesW(path);
    return a != INVALID_FILE_ATTRIBUTES && (a & FILE_ATTRIBUTE_DIRECTORY);
}

/* Whole text file as UTF-16 (accepts UTF-8 with or without BOM, or UTF-16 LE). */
static wchar_t* ReadTextFile(const wchar_t* path)
{
    HANDLE h = CreateFileW(path, GENERIC_READ, FILE_SHARE_READ | FILE_SHARE_WRITE, NULL,
                           OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, NULL);
    if (h == INVALID_HANDLE_VALUE)
        return NULL;

    wchar_t* text = NULL;
    DWORD    size = GetFileSize(h, NULL);

    if (size != 0xFFFFFFFFu && size <= 0x100000u) {
        uint8_t* raw = (uint8_t*)malloc((size_t)size + 2);
        DWORD    got = 0;
        if (raw && ReadFile(h, raw, size, &got, NULL)) {
            raw[got] = raw[got + 1] = 0;

            if (got >= 2 && raw[0] == 0xFF && raw[1] == 0xFE) {
                text = WDup((const wchar_t*)(raw + 2), (got - 2) / 2);
            } else {
                const char* s = (const char*)raw;
                int         n = (int)got;
                if (n >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF) { s += 3; n -= 3; }

                text = (wchar_t*)malloc(((size_t)n + 1) * sizeof(wchar_t));
                if (text) {
                    int w = n > 0 ? MultiByteToWideChar(CP_UTF8, 0, s, n, text, n) : 0;
                    if (w < 0) w = 0;
                    text[w] = 0;
                }
            }
        }
        free(raw);
    }

    CloseHandle(h);
    return text;
}

/* -----------------------------------------------------------------------------
 *  Finding mods
 * ----------------------------------------------------------------------------- */

/* A mod: a folder (searched to any depth) or one loose .utoc file. */
typedef struct {
    wchar_t* name;      /* key for loadorder.txt and the log                 */
    wchar_t* path;      /* the folder, or the loose file                     */
    int      isFile;
    uint64_t mtime;     /* loose file only                                   */
} Mod;

typedef struct {
    Mod*   v;
    size_t n, cap;
} ModList;

static void ML_Free(ModList* l)
{
    for (size_t i = 0; i < l->n; ++i) { free(l->v[i].name); free(l->v[i].path); }
    free(l->v);
    l->v = NULL;
    l->n = l->cap = 0;
}

/* One container that is going to be mounted. */
typedef struct {
    wchar_t*       path;    /* full path of .utoc or *_P.pak                 */
    const wchar_t* mod;     /* name of the Mod it came from (not owned)      */
    uint64_t       mtime;
    int            skip;    /* 1 = not mounted (older duplicate, no .ucas)   */
} Container;

typedef struct {
    Container* v;
    size_t     n, cap;
} ContainerList;

static void CL_Free(ContainerList* l)
{
    for (size_t i = 0; i < l->n; ++i) free(l->v[i].path);
    free(l->v);
    l->v = NULL;
    l->n = l->cap = 0;
}

static void CL_Add(ContainerList* l, wchar_t* path, const wchar_t* mod, uint64_t mtime)
{
    if (!path) return;
    if (!Grow((void**)&l->v, &l->cap, l->n, sizeof(Container))) { free(path); return; }
    l->v[l->n].path  = path;
    l->v[l->n].mod   = mod;
    l->v[l->n].mtime = mtime;
    l->v[l->n].skip  = 0;
    l->n++;
}

/* Every .utoc inside `dir`, its sub-folders first-to-last A to Z, files before
 * the folders next to them. Skipped folder names are not entered, and neither
 * are links inside a mod folder (a link to a drive would be searched for long). */
static void CollectUtoc(const wchar_t* dir, int depth, const wchar_t* mod, ContainerList* out)
{
    DirList entries = {0};
    ListDir(dir, &entries);

    for (int pass = 0; pass < 2; ++pass) {                 /* 0 files, 1 folders */
        for (size_t i = 0; i < entries.n; ++i) {
            DirEntry* e = &entries.v[i];
            if (e->isDir != pass) continue;
            if (!e->isDir) {
                wchar_t* path = NewPath(L"%s\\%s", dir, e->name);
                if (path && IsPakSidecar(path)) free(path);
                else CL_Add(out, path, mod, e->mtime);
            } else if (depth < MAX_SEARCH_DEPTH && !e->isLink && !IsSkippedDir(e->name)) {
                wchar_t* sub = NewPath(L"%s\\%s", dir, e->name);
                if (sub) CollectUtoc(sub, depth + 1, mod, out);
                free(sub);
            }
        }
    }
    DL_Free(&entries);
}

static int HasUtoc(const wchar_t* dir, int depth)
{
    DirList entries = {0};
    int     found   = 0;
    ListDir(dir, &entries);
    for (size_t i = 0; i < entries.n && !found; ++i) {
        DirEntry* e = &entries.v[i];
        if (!e->isDir) {
            found = 1;
        } else if (depth < MAX_SEARCH_DEPTH && !e->isLink && !IsSkippedDir(e->name)) {
            wchar_t* sub = NewPath(L"%s\\%s", dir, e->name);
            if (sub) found = HasUtoc(sub, depth + 1);
            free(sub);
        }
    }
    DL_Free(&entries);
    return found;
}

/* The mods directly inside `dir`, A to Z. `prefix` goes in front of each name. */
static size_t FindMods(const wchar_t* dir, const wchar_t* prefix, ModList* out)
{
    DirList entries = {0};
    size_t  added   = 0;
    ListDir(dir, &entries);

    for (size_t i = 0; i < entries.n; ++i) {
        DirEntry* e    = &entries.v[i];
        wchar_t*  path = NewPath(L"%s\\%s", dir, e->name);
        if (!path) continue;
        if (!e->isDir && IsPakSidecar(path)) { free(path); continue; }
        if (e->isDir && (IsSkippedDir(e->name) || !HasUtoc(path, 1))) { free(path); continue; }

        wchar_t* name = NewPath(L"%s%s", prefix, e->name);
        if (!name || !Grow((void**)&out->v, &out->cap, out->n, sizeof(Mod))) {
            free(name); free(path);
            continue;
        }
        out->v[out->n].name   = name;
        out->v[out->n].path   = path;
        out->v[out->n].isFile = !e->isDir;
        out->v[out->n].mtime  = e->mtime;
        out->n++;
        added++;
    }
    DL_Free(&entries);
    return added;
}

static int FindModI(const ModList* l, const wchar_t* name)
{
    for (size_t i = 0; i < l->n; ++i)
        if (WICmp(l->v[i].name, name) == 0) return (int)i;
    return -1;
}

/* Folder of the game's own containers, worked out from the path the game used
 * to mount one of them (relative paths are relative to the game's exe folder). */
static wchar_t* PaksDirFrom(const wchar_t* tocPath)
{
    if (!tocPath) return NULL;
    size_t   dirLen = (size_t)(FileNamePart(tocPath) - tocPath);
    while (dirLen > 0 && (tocPath[dirLen - 1] == L'\\' || tocPath[dirLen - 1] == L'/')) --dirLen;
    wchar_t* dir = WDup(tocPath, dirLen);
    if (!dir) return NULL;
    if (IsAbsolutePath(dir)) return dir;

    wchar_t* exe = (wchar_t*)malloc(PATH_CAP * sizeof(wchar_t));
    wchar_t* out = NULL;
    if (exe) {
        DWORD n = GetModuleFileNameW(NULL, exe, PATH_CAP);
        if (n > 0 && n < PATH_CAP) {
            *(wchar_t*)FileNamePart(exe) = 0;              /* keeps the last '\' */
            out = NewPath(L"%s%s", exe, dir);
        }
        free(exe);
    }
    free(dir);
    return out;
}

/* -----------------------------------------------------------------------------
 *  Mount plan: which containers, in which order
 * ----------------------------------------------------------------------------- */
static void AddModContainers(const Mod* m, ContainerList* plan)
{
    if (m->isFile) {
        CL_Add(plan, WDup(m->path, WLen(m->path)), m->name, m->mtime);
        return;
    }
    size_t before = plan->n;
    CollectUtoc(m->path, 1, m->name, plan);
    if (plan->n == before)
        Log(L"  (%s no longer has a .utoc or *_P.pak in it)", m->name);
}

/* File names of the containers the game mounted itself (successfully). */
#define MAX_GAME_MOUNTS 256
static wchar_t* g_gameMounts[MAX_GAME_MOUNTS];
static size_t   g_gameMountCount;

static void RememberGameMount(const wchar_t* path)
{
    const wchar_t* name = FileNamePart(path);
    for (size_t i = 0; i < g_gameMountCount; ++i)
        if (WICmp(g_gameMounts[i], name) == 0) return;
    if (g_gameMountCount < MAX_GAME_MOUNTS) {
        wchar_t* copy = WDup(name, WLen(name));
        if (copy) g_gameMounts[g_gameMountCount++] = copy;
    }
}

static int GameMounted(const wchar_t* path)
{
    const wchar_t* name = FileNamePart(path);
    for (size_t i = 0; i < g_gameMountCount; ++i)
        if (WICmp(g_gameMounts[i], name) == 0) return 1;
    return 0;
}

/* Same container name twice: only the newest copy is mounted (two containers
 * with one name share a container id in the game, so both cannot load).
 * The same goes for a name the game has already mounted itself.
 * A .utoc whose .ucas is missing cannot load either.                          */
static void CheckContainers(ContainerList* plan)
{
    for (size_t i = 0; i < plan->n; ++i) {
        Container* c = &plan->v[i];
        wchar_t* toc = IsPak(c->path) ? WithExtension(c->path, L"utoc") : NULL;
        if (GameMounted(c->path) || (toc && GameMounted(toc))) {
            c->skip = 1;
            Log(L"  skipped %s: the game has already mounted a container with this name", c->path);
        }
        free(toc);
    }

    for (size_t i = 0; i < plan->n; ++i) {
        Container* c = &plan->v[i];
        if (c->skip) continue;
        for (size_t j = i + 1; j < plan->n; ++j) {
            Container* d = &plan->v[j];
            if (d->skip) continue;
            wchar_t* ck = WithExtension(c->path, L"utoc");
            wchar_t* dk = WithExtension(d->path, L"utoc");
            int same = ck && dk && WICmp(FileNamePart(ck), FileNamePart(dk)) == 0;
            free(ck); free(dk);
            if (!same) continue;
            Container* older = d->mtime > c->mtime ? c : d;
            Container* newer = older == c ? d : c;
            older->skip = 1;
            Log(L"  skipped %s: same name as %s, and only one can load (the newer one is used)", older->path, newer->path);
            if (older == c) break;
        }
    }

    for (size_t i = 0; i < plan->n; ++i) {
        Container* c = &plan->v[i];
        if (c->skip) continue;
        wchar_t* toc = IsPak(c->path) ? WithExtension(c->path, L"utoc") : NULL;
        if (IsPak(c->path) && (!toc || !FileExists(toc))) { free(toc); continue; }
        free(toc);
        wchar_t* ucas = WithExtension(c->path, L"ucas");
        if (!ucas) continue;
        if (!FileExists(ucas)) {
            c->skip = 1;
            Log(L"  skipped %s: its .ucas file is missing (%s)", c->path, FileNamePart(ucas));
        }
        free(ucas);
    }
}

/* `plan` points at names owned by `mods`: free the plan first, then the mods. */
static void BuildPlan(ContainerList* plan, ModList* mods, const wchar_t* paksDir)
{
    memset(plan, 0, sizeof(*plan));
    memset(mods, 0, sizeof(*mods));

    /* 1. find the mods: the loader's folder first, then the game's ~mods */
    size_t here = FindMods(g_modRoot, L"", mods);

    wchar_t* tildeMods = paksDir ? NewPath(L"%s\\~mods", paksDir) : NULL;
    int      hasTilde  = tildeMods && DirExists(tildeMods);
    size_t   there     = hasTilde ? FindMods(tildeMods, L"~mods\\", mods) : 0;

    Log(L"mods found=%u (loader folder %u, ~mods %u)", mods->n, here, there);
    Log(L"~mods folder=%s", hasTilde ? tildeMods : L"(none)");
    free(tildeMods);

    /* 2. loadorder.txt decides order and on/off for the mods it lists */
    uint8_t* placed = (uint8_t*)malloc(mods->n + 1);
    if (!placed) return;
    memset(placed, 0, mods->n + 1);

    wchar_t* orderPath = NewPath(L"%s\\loadorder.txt", g_modRoot);
    wchar_t* text      = orderPath ? ReadTextFile(orderPath) : NULL;
    free(orderPath);
    Log(L"loadorder.txt=%s", text ? L"found" : L"not found (all mods on, A to Z)");

    for (wchar_t* p = text; p && *p; ) {
        wchar_t* line = p;
        while (*p && *p != L'\r' && *p != L'\n') ++p;
        wchar_t* stop = p;
        while (*p == L'\r' || *p == L'\n') ++p;         /* p now at next line  */

        while (line < stop && (*line == L' ' || *line == L'\t' || *line == 0xFEFF)) ++line;
        if (line == stop || *line == L'#' || *line == L';')
            continue;

        int enabled = 1;
        if (*line == L'+' || *line == L'-') {
            enabled = (*line == L'+');
            ++line;
        }
        while (line < stop && (*line == L' ' || *line == L'\t')) ++line;
        while (stop > line && (stop[-1] == L' ' || stop[-1] == L'\t')) --stop;
        if (line == stop)
            continue;

        wchar_t* name = WDup(line, (size_t)(stop - line));
        if (!name) continue;
        for (wchar_t* q = name; *q; ++q) if (*q == L'/') *q = L'\\';
        int idx = FindModI(mods, name);

        if (idx < 0) {
            Log(L"  listed but not found (ignored): %s", name);
        } else if (!placed[idx]) {
            placed[idx] = 1;
            if (enabled) {
                Log(L"  on   %s", mods->v[idx].name);
                AddModContainers(&mods->v[idx], plan);
            } else {
                Log(L"  off  %s", mods->v[idx].name);
            }
        }
        free(name);
    }

    /* 3. mods the file does not mention: on, after the listed ones */
    for (size_t i = 0; i < mods->n; ++i) {
        if (placed[i]) continue;
        Log(L"  on   %s%s", mods->v[i].name, text ? L"  (not in loadorder.txt)" : L"");
        AddModContainers(&mods->v[i], plan);
    }

    free(text);
    free(placed);

    CheckContainers(plan);
}

/* -----------------------------------------------------------------------------
 *  Package store sync
 *
 *  Mounting a mod container makes the game read the mod's package data, but
 *  the container header the mount returns (which lists, for each package,
 *  the packages it imports) is never handed to the game's package store. The
 *  game keeps the base game's import list for a replaced package. That is
 *  harmless while a mod imports the same packages as the file it replaces; as
 *  soon as it imports different ones, every import index in the modded file
 *  points at the wrong package and the file fails to load.
 *
 *  So: remember where each base container's store entries live (they stay in
 *  place when the game moves the header into its package store), and after
 *  each mod mount copy the mod's import lists over the base game's entries for
 *  the same package ids. Containers the game mounts after the mods get the
 *  same treatment from a copy of every mod entry.
 *
 *  Layout (UE 5.4): TIoStatusOr<FIoContainerHeader> has the header at +0x108;
 *  FIoContainerHeader starts { u64 ContainerId; TArray<FPackageId> PackageIds;
 *  TArray<uint8> StoreEntries; ... } and StoreEntries holds one
 *  FFilePackageStoreEntry per package { u32 ImportNum, ImportOffsetFromHere,
 *  ShaderMapNum, ShaderMapOffsetFromHere } followed by the lists.
 *  Anything that does not look like that is left alone and logged.
 * ----------------------------------------------------------------------------- */
#define RESULT_VALUE_OFFSET 0x108u
#define SS_MAX_BASE         64
#define SS_POOL_BYTES       (4u << 20)
#define SS_MAX_SHARED       512

typedef struct { uint8_t* data; int32_t num; int32_t max; } TArr;
typedef struct {
    uint64_t containerId;
    TArr     packageIds;      /* FPackageId[]                                    */
    TArr     storeEntries;    /* FFilePackageStoreEntry[num] followed by payload */
} ContainerHeader;
typedef struct { uint32_t impNum, impOff, shNum, shOff; } StoreEntry;
_Static_assert(sizeof(StoreEntry) == 16, "store entry layout");
_Static_assert(sizeof(TArr) == 16, "TArray layout");

typedef struct {
    uint64_t* ids;
    uint32_t  n;
    uint8_t*  ent;
    uint32_t  entSize;
    uint8_t*  arena;
    uint32_t  arenaUsed, arenaSize;
} BaseContainer;

static BaseContainer    g_base[SS_MAX_BASE];
static uint32_t         g_nbase;
static uint8_t*         g_pool;                  /* copies of every mod entry */
static uint32_t         g_poolUsed, g_poolSize;
static struct { const uint8_t* p; uint32_t len; } g_shared[SS_MAX_SHARED];
static uint32_t         g_nshared;
static uint32_t         g_reused, g_fresh;
static volatile int32_t g_storeLock;

static int PlausiblePtr(const void* p, uint64_t align)
{
    uint64_t v = (uint64_t)(uintptr_t)p;
    return v >= 0x10000ull && v < 0x00007FFFFFFF0000ull && (v & (align - 1)) == 0;
}

/* 1 = usable, 0 = empty (nothing to do), -1 = not the layout expected */
static int CheckHeader(const ContainerHeader* h)
{
    int32_t n = h->packageIds.num;
    if (n == 0 && h->storeEntries.num == 0) return 0;
    if (n <= 0 || n > 8000000 || h->packageIds.max < n) return -1;
    if (h->storeEntries.num <= 0 || h->storeEntries.max < h->storeEntries.num) return -1;
    uint32_t se = (uint32_t)h->storeEntries.num;
    if ((uint64_t)se < (uint64_t)n * sizeof(StoreEntry)) return -1;
    if (!PlausiblePtr(h->packageIds.data, 8) || !PlausiblePtr(h->storeEntries.data, 4)) return -1;
    const StoreEntry* e = (const StoreEntry*)h->storeEntries.data;
    for (int32_t i = 0; i < n; i++) {
        uint64_t here = (uint64_t)i * sizeof(StoreEntry);
        if (e[i].impNum) {
            uint64_t a = here + e[i].impOff, b = a + (uint64_t)e[i].impNum * 8;
            if (a < (uint64_t)n * sizeof(StoreEntry) || b > se) return -1;
        }
        if (e[i].shNum) {
            uint64_t a = here + 8 + e[i].shOff, b = a + (uint64_t)e[i].shNum * 20;
            if (a < (uint64_t)n * sizeof(StoreEntry) || b > se) return -1;
        }
    }
    return 1;
}

static uint64_t LoadU64(const uint8_t* p)
{
    uint64_t v;
    memcpy(&v, p, 8);
    return v;
}

/* Lists that more than one entry points at (see FindExisting): never
 * overwritten in place.                                                       */
static int IsShared(const uint8_t* p, uint32_t len)
{
    for (uint32_t i = 0; i < g_nshared; i++)
        if (p < g_shared[i].p + g_shared[i].len && g_shared[i].p < p + len) return 1;
    return 0;
}

/* An entry stores a 32-bit forward offset to its list. Before asking for new
 * memory, look for the very same list already in a base container's entry
 * block within reach: a mod file that is a copy of another game file imports
 * exactly what that file imports, so its list is usually there already.     */
static const uint8_t* FindExisting(const StoreEntry* e, const uint64_t* ids, uint32_t n)
{
    uint64_t need = (uint64_t)n * 8;
    for (uint32_t s = 0; s < g_nbase; s++) {
        const BaseContainer* b  = &g_base[s];
        const uint8_t*       lo = b->ent + (uint64_t)b->n * sizeof(StoreEntry);
        const uint8_t*       hi = b->ent + b->entSize;
        if (hi <= (const uint8_t*)e || (uint64_t)(hi - lo) < need) continue;
        if (lo <= (const uint8_t*)e) lo = (const uint8_t*)e + sizeof(StoreEntry);
        for (const uint8_t* p = lo; p + need <= hi; p += 4) {      /* 4-byte steps: */
            if (LoadU64(p) != ids[0]) continue;                       /* may be unaligned */
            if ((uint64_t)(p - (const uint8_t*)e) > 0xFFFFFFF0ull) break;
            uint32_t i;
            for (i = 1; i < n; i++) if (LoadU64(p + (size_t)i * 8) != ids[i]) break;
            if (i == n) return p;
        }
    }
    return NULL;
}

static uint8_t* ArenaAlloc(BaseContainer* b, uint32_t bytes)
{
    if (!b->arena || b->arenaUsed + bytes > b->arenaSize) {
        /* the memory has to sit above the base container's entry block and
         * within 4 GB of it                                                 */
        uint64_t start = ((uint64_t)(uintptr_t)b->ent + b->entSize + 0xFFFFull) & ~0xFFFFull;
        uint64_t limit = (uint64_t)(uintptr_t)b->ent + 0xE0000000ull;
        uint32_t size  = (bytes + 0xFFFFu) & ~0xFFFFu;
        b->arena = NULL;
        for (uint64_t a = start; a + size < limit; a += 0x10000ull) {
            uint8_t* p = (uint8_t*)VirtualAlloc((void*)(uintptr_t)a, size, MEM_COMMIT | MEM_RESERVE, PAGE_READWRITE);
            if (p) { b->arena = p; b->arenaUsed = 0; b->arenaSize = size; break; }
        }
        if (!b->arena) return NULL;
    }
    uint8_t* p = b->arena + b->arenaUsed;
    b->arenaUsed += (bytes + 7u) & ~7u;
    return p;
}

/* 0 = package not in this base container, 1 = list already identical,
 * 2 = updated, -1 = failed                                                    */
static int ApplyOne(BaseContainer* b, uint64_t id, const uint64_t* ids, uint32_t n)
{
    uint32_t lo = 0, hi = b->n, k = 0xFFFFFFFFu, i;
    /* ids are sorted in every header seen so far; fall back to a scan */
    while (lo < hi) { uint32_t mid = lo + (hi - lo) / 2; if (b->ids[mid] < id) lo = mid + 1; else hi = mid; }
    if (lo < b->n && b->ids[lo] == id) k = lo;
    else for (i = 0; i < b->n; i++) if (b->ids[i] == id) { k = i; break; }
    if (k == 0xFFFFFFFFu) return 0;

    StoreEntry* e   = (StoreEntry*)(b->ent + (uint64_t)k * sizeof(StoreEntry));
    uint64_t*   cur = (uint64_t*)((uint8_t*)e + e->impOff);
    if (e->impNum == n) {
        for (i = 0; i < n; i++) if (cur[i] != ids[i]) break;
        if (i == n) return 1;
    }
    if (n == 0) { e->impNum = 0; return 2; }
    if (n <= e->impNum && !IsShared((const uint8_t*)cur, e->impNum * 8)) {
        for (i = 0; i < n; i++) cur[i] = ids[i];
        e->impNum = n;
    } else {
        const uint8_t* where = FindExisting(e, ids, n);
        if (where) {
            if (g_nshared < SS_MAX_SHARED) { g_shared[g_nshared].p = where; g_shared[g_nshared].len = n * 8; g_nshared++; }
            g_reused++;
        } else {
            uint64_t* fresh = (uint64_t*)ArenaAlloc(b, n * 8);
            if (!fresh) return -1;
            for (i = 0; i < n; i++) fresh[i] = ids[i];
            where = (const uint8_t*)fresh;
            g_fresh++;
        }
        e->impOff = (uint32_t)(where - (const uint8_t*)e);
        e->impNum = n;
    }
    return 2;
}

static void Remember(uint64_t id, const uint64_t* ids, uint32_t n)
{
    uint32_t need = 16 + n * 8;
    if (!g_pool) {
        g_pool = (uint8_t*)VirtualAlloc(NULL, SS_POOL_BYTES, MEM_COMMIT | MEM_RESERVE, PAGE_READWRITE);
        if (!g_pool) return;
        g_poolSize = SS_POOL_BYTES;
        g_poolUsed = 0;
    }
    if (g_poolUsed + need > g_poolSize) {
        static int said;
        if (!said++)
            Log(L"  package store: too many mod packages to remember; containers the game mounts later will not get their import lists");
        return;
    }
    uint64_t* rec = (uint64_t*)(g_pool + g_poolUsed);
    rec[0] = id;
    rec[1] = n;
    for (uint32_t i = 0; i < n; i++) rec[2 + i] = ids[i];
    g_poolUsed += need;
}

static void StoreLock(void)   { while (__sync_lock_test_and_set(&g_storeLock, 1)) Sleep(0); }
static void StoreUnlock(void) { __sync_lock_release(&g_storeLock); }

/* Called for every container the game itself mounts. */
static void StoreSync_OnStock(const uint8_t* result, const wchar_t* path)
{
    if (!result || *(const int32_t*)result != 0) return;
    const ContainerHeader* h = (const ContainerHeader*)(result + RESULT_VALUE_OFFSET);

    StoreLock();
    int ok = CheckHeader(h);
    if (ok < 0) {
        Log(L"  package store: layout of %s not recognised - import lists of mods that replace its files will not be updated", path);
    } else if (ok > 0 && g_nbase < SS_MAX_BASE) {
        BaseContainer* b = &g_base[g_nbase++];
        b->ids       = (uint64_t*)h->packageIds.data;
        b->n         = (uint32_t)h->packageIds.num;
        b->ent       = h->storeEntries.data;
        b->entSize   = (uint32_t)h->storeEntries.num;
        b->arena     = NULL;
        b->arenaUsed = b->arenaSize = 0;

        /* mods already mounted? then this container came late: bring it in line */
        uint32_t done = 0;
        for (uint32_t off = 0; off < g_poolUsed; ) {
            uint64_t* rec = (uint64_t*)(g_pool + off);
            uint32_t  n   = (uint32_t)rec[1];
            if (ApplyOne(b, rec[0], rec + 2, n) == 2) done++;
            off += 16 + n * 8;
        }
        if (done)
            Log(L"  package store: %u import lists updated in %s (mounted after the mods)", (size_t)done, path);
    }
    StoreUnlock();
}

/* Called after the loader mounted one mod container. */
static void StoreSync_OnMod(const uint8_t* result)
{
    if (!result || *(const int32_t*)result != 0) return;
    const ContainerHeader* h = (const ContainerHeader*)(result + RESULT_VALUE_OFFSET);

    StoreLock();
    int ok = CheckHeader(h);
    if (ok < 0) {
        Log(L"  package store: mod container header not recognised - import lists not updated");
    } else if (ok > 0) {
        uint32_t          n = (uint32_t)h->packageIds.num;
        const StoreEntry* e = (const StoreEntry*)h->storeEntries.data;
        uint32_t updated = 0, same = 0, missing = 0, failed = 0;
        for (uint32_t i = 0; i < n; i++) {
            uint64_t        id   = ((const uint64_t*)h->packageIds.data)[i];
            const uint64_t* ids  = (const uint64_t*)((const uint8_t*)&e[i] + e[i].impOff);
            uint32_t        cnt  = e[i].impNum;
            int             best = 0;
            for (uint32_t s = 0; s < g_nbase; s++) {
                int r = ApplyOne(&g_base[s], id, ids, cnt);
                if (r == 2 || (r == 1 && best == 0) || (r == -1 && best != 2)) best = r;
            }
            if (best == 2) updated++; else if (best == 1) same++; else if (best == 0) missing++; else failed++;
            Remember(id, ids, cnt);
        }
        Log(L"  package store: %u packages, %u import lists updated, %u already matching, %u not in the base game",
            (size_t)n, (size_t)updated, (size_t)same, (size_t)missing);
        if (g_reused + g_fresh) {
            Log(L"  package store: %u lists needed more room (%u reuse an identical list already in memory, %u got new memory)",
                (size_t)(g_reused + g_fresh), (size_t)g_reused, (size_t)g_fresh);
            g_reused = g_fresh = 0;
        }
        if (failed)
            Log(L"  package store: %u import lists could not be updated (no memory near the base entries)", (size_t)failed);
        if (missing && g_startedLate && g_nbase == 0)
            Log(L"  package store: the game's own containers were mounted before the loader started, so import lists could not be updated");
    }
    StoreUnlock();
}

/* -----------------------------------------------------------------------------
 *  Package store registration (1.1.0)
 *
 *  The game mounts each of its own containers in one function: it calls the
 *  mount function this loader hooks, then hands the container header to its
 *  package store (FFilePackageStoreBackend::Mount, inlined there):
 *
 *      Backend = PakPlatformFile->PackageStore            (+0xE0)
 *      WriteLock(&Backend->Lock)                          (+0x08, an SRW lock)
 *      Seq = Backend->NextSequence++                      (+0x48)
 *      Backend->MountedContainers.Add({ Header, Order, Seq })   (+0x38, 32 bytes each)
 *      Algo::Sort(MountedContainers)                      (highest order first)
 *      Backend->bNeedsContainerUpdate = true              (+0x118)
 *      WriteUnlock(&Backend->Lock)
 *
 *  The loader mounts mod containers by calling the mount function directly,
 *  so their headers never reached the store: replaced files still loaded (the
 *  store knew their ids from the game's own containers) but new files did not.
 *  Now each mod header is handed over in exactly the way above, using the
 *  game's own array-grow and sort functions and lock functions, all read from
 *  that piece of game code (kStoreReg). The header copies live in memory this
 *  loader owns and never frees, like the game's own headers.
 *
 *  Where the PakPlatformFile is: the game function keeps it in r14 around its
 *  call to the mount function ("mov rcx,[r14+0xD0]; ... call Mount"). A small
 *  entry stub in front of HookedMount saves r14; it is used only when the hook
 *  was called from exactly that call (return address) and [r14+0xD0] is the
 *  object the mount function was called on.
 * ----------------------------------------------------------------------------- */

/* the inlined FFilePackageStoreBackend::Mount, 156 bytes; 0xFF in kStoreRegWild = any byte */
static const uint8_t kStoreReg[156] = {
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
/* bytes that differ between builds: a field offset of the pak file object, two
 * import-table slots and two call targets                                      */
static int StoreRegWild(size_t i)
{
    return (i >= 0x0A && i < 0x0E) || (i >= 0x14 && i < 0x18) || (i >= 0x51 && i < 0x55)
        || (i >= 0x86 && i < 0x8A) || (i >= 0x98 && i < 0x9C);
}
#define SR_LOCK_IAT     0x14            /* disp32 of call [rip+x], next ip at 0x18 */
#define SR_GROW_CALL    0x50            /* call rel32                              */
#define SR_SORT_CALL    0x85
#define SR_UNLOCK_IAT   0x98            /* next ip at 0x9C                         */

#define PAK_DISPATCHER  0xD0            /* PakPlatformFile -> the mount function's object */
#define PAK_STORE       0xE0            /* PakPlatformFile -> FFilePackageStoreBackend    */
#define PS_LOCK         0x08
#define PS_CONTAINERS   0x38            /* TArray<FMountedContainer> (data, num, max)     */
#define PS_NEXT_SEQ     0x48
#define PS_NEEDS_UPDATE 0x118
#define PS_ENTRY_SIZE   32
#define HEADER_SIZE     0x78            /* sizeof(FIoContainerHeader)                     */

/* "mov rcx,[r14+0xD0]" - how the game function loads the mount function's object */
static const uint8_t kLoadDispatcher[7] = { 0x49,0x8B,0x8E,0xD0,0x00,0x00,0x00 };

typedef void (WINAPI *LockFn)(void* lock);
typedef void (WINAPI *GrowFn)(void* array, int32_t oldNum);
typedef void (WINAPI *SortFn)(void* data, int32_t num, uint8_t pred1, uint8_t pred2);

static struct {
    int            found;           /* kStoreReg found once, call site found */
    const uint8_t* at;
    const uint8_t* mountRet;        /* return address of the game's mount call there */
    GrowFn         grow;
    SortFn         sort;
    LockFn*        lockSlot;        /* the game's import-table slots */
    LockFn*        unlockSlot;
} g_reg;

static uint8_t* g_backend;              /* the game's FFilePackageStoreBackend, once known     */
static int      g_backendWhy;           /* why it is not known: see BackendProblem              */
static uint8_t* g_hdrArena;             /* copies of mod container headers, never freed         */
static size_t   g_hdrArenaUsed, g_hdrArenaSize;
static uint32_t g_registered, g_registerFailed;

static int Readable(const void* p, size_t n)
{
    const uint8_t* a = (const uint8_t*)p;
    const uint8_t* end = a + n;
    while (a < end) {
        MEMORY_BASIC_INFORMATION mbi;
        if (VirtualQuery(a, &mbi, sizeof(mbi)) != sizeof(mbi)) return 0;
        if (mbi.State != MEM_COMMIT) return 0;
        if ((mbi.Protect & (PAGE_NOACCESS | PAGE_GUARD)) || !(mbi.Protect & 0xEEu)) return 0;
        const uint8_t* next = (const uint8_t*)mbi.BaseAddress + mbi.RegionSize;
        if (next <= a) return 0;
        a = next;
    }
    return 1;
}

/* FPakPlatformFile::PakFiles is TArray<{u32 order,u32 pad,FPakFile*}> at +0x10.
 * FPakFile::Filename is FString at +0x18 (the +0x10 slot is a vtable).
 * Confirmed from the live game's official Pak entry. Do not infer success from AL
 * alone: this game's core may insert a Pak before its sidecar fails. */
static int PakPresentLocked(const wchar_t* path)
{
    if (!g_platformFile || !Readable(g_platformFile, 0xE8)) return -1;
    TArr list;
    memcpy(&list, g_platformFile + 0x10, sizeof(list));
    if (list.num < 0 || list.max < list.num || list.max > 100000) return -1;
    if (list.num && (!PlausiblePtr(list.data, 8) || !Readable(list.data, (size_t)list.num * 16))) return -1;
    for (int32_t i = 0; i < list.num; ++i) {
        uint8_t* pak;
        memcpy(&pak, list.data + (size_t)i * 16 + 8, 8);
        if (!PlausiblePtr(pak, 8) || !Readable(pak + 0x18, sizeof(TArr))) return -1;
        TArr name;
        memcpy(&name, pak + 0x18, sizeof(name));
        if (name.num < 1 || name.num > 32768 || name.max < name.num
            || !PlausiblePtr(name.data, 2) || !Readable(name.data, (size_t)name.num * 2)) return -1;
        const wchar_t* text = (const wchar_t*)name.data;
        if (text[name.num - 1]) return -1;
        if (WICmp(FileNamePart(text), FileNamePart(path)) == 0) return 1;
    }
    return 0;
}

static int PakPresent(const wchar_t* path)
{
    if (!g_platformFile || !Readable(g_platformFile, 0xE8)) return -1;
    EnterCriticalSection(g_platformFile + 0x38);
    int present = PakPresentLocked(path);
    LeaveCriticalSection(g_platformFile + 0x38);
    return present;
}

static void NotePlatformFile(void* dispatcher, const void* caller, void* r14)
{
    if (g_platformFile || !g_pakMount
        || caller != (const uint8_t*)(void*)g_pakMount + PAK_IOSTORE_RETURN) return;
    uint8_t* pak = (uint8_t*)r14;
    if (!PlausiblePtr(pak, 8) || !Readable(pak, 0xE8)) return;
    void* found;
    memcpy(&found, pak + PAK_DISPATCHER, 8);
    if (found != dispatcher) return;
    g_platformFile = pak;
    if (PakPresent(L"") < 0) { g_platformFile = NULL; return; }
    Log(L"Legacy Pak: captured native FPakPlatformFile=%p dispatcher=%p", pak, dispatcher);
}

static int32_t LoadI32(const uint8_t* p) { int32_t v; memcpy(&v, p, 4); return v; }

/* Locate kStoreReg and the game's call to the mount function just before it. */
static void FindStoreReg(uint8_t* base, const uint8_t* mountFn)
{
    int32_t  lfanew;
    uint16_t sectionCount, optSize;
    size_t   hits = 0;
    const uint8_t* at = NULL;
    const uint8_t* secLo = NULL;          /* the section kStoreReg is in */

    memset(&g_reg, 0, sizeof(g_reg));
    memcpy(&lfanew, base + 0x3C, 4);
    uint8_t* nt = base + lfanew;
    memcpy(&sectionCount, nt + 0x06, 2);
    memcpy(&optSize,      nt + 0x14, 2);
    uint8_t* sec = nt + 0x18 + optSize;
    for (uint16_t s = 0; s < sectionCount; ++s, sec += 40) {
        uint32_t vsize, vaddr, flags;
        memcpy(&vsize, sec + 8,  4);
        memcpy(&vaddr, sec + 12, 4);
        memcpy(&flags, sec + 36, 4);
        if (!(flags & IMAGE_SCN_MEM_EXECUTE) || vsize < sizeof(kStoreReg)) continue;
        const uint8_t* p = base + vaddr;
        for (size_t i = 0; i + sizeof(kStoreReg) <= vsize; ++i) {
            if (p[i] != 0x4D || p[i + 1] != 0x8B || p[i + 2] != 0xB6) continue;
            size_t k;
            for (k = 3; k < sizeof(kStoreReg); ++k)
                if (!StoreRegWild(k) && p[i + k] != kStoreReg[k]) break;
            if (k == sizeof(kStoreReg)) { at = p + i; secLo = p; hits++; }
        }
    }
    if (hits != 1) {
        Log(L"new files: the game's package store code was found %u times (expected once) - mods can replace game files but not add new ones", hits);
        return;
    }
    /* the call to the mount function, at most 0x400 bytes before, with the
     * dispatcher loaded from [r14+0xD0] shortly before that call            */
    const uint8_t* call = NULL;
    const uint8_t* lowest = (at - secLo > 0x400) ? at - 0x400 : secLo;
    for (const uint8_t* q = at - 5; q >= lowest; --q) {
        if (q[0] != 0xE8) continue;
        if (q + 5 + LoadI32(q + 1) == mountFn) { call = q; break; }
    }
    int loadSeen = 0;
    if (call)
        for (const uint8_t* q = call - (int)sizeof(kLoadDispatcher); q >= call - 0x40 && q >= secLo; --q)
            if (memcmp(q, kLoadDispatcher, sizeof(kLoadDispatcher)) == 0) { loadSeen = 1; break; }
    if (!call || !loadSeen) {
        Log(L"new files: the game's own mount call was not found next to its package store code - mods can replace game files but not add new ones");
        return;
    }
    g_reg.at         = at;
    g_reg.mountRet   = call + 5;
    g_reg.grow       = (GrowFn)(void*)(at + SR_GROW_CALL + 5 + LoadI32(at + SR_GROW_CALL + 1));
    g_reg.sort       = (SortFn)(void*)(at + SR_SORT_CALL + 5 + LoadI32(at + SR_SORT_CALL + 1));
    g_reg.lockSlot   = (LockFn*)(void*)(at + SR_LOCK_IAT + 4 + LoadI32(at + SR_LOCK_IAT));
    g_reg.unlockSlot = (LockFn*)(void*)(at + SR_UNLOCK_IAT + 4 + LoadI32(at + SR_UNLOCK_IAT));
    g_reg.found      = 1;
    Log(L"new files: the game's package store code found at %p", at);
}

/* Called at the start of the hook, for the game's own mounts. */
static void NoteStoreBackend(void* self, const void* returnAddress, void* r14)
{
    if (g_backend || !g_reg.found) return;
    if (returnAddress != g_reg.mountRet) { g_backendWhy = 1; return; }
    uint8_t* pak = (uint8_t*)r14;
    if (!PlausiblePtr(pak, 8) || !Readable(pak + PAK_DISPATCHER, PAK_STORE + 8 - PAK_DISPATCHER)) { g_backendWhy = 2; return; }
    void* dispatcher;
    uint8_t* backend;
    memcpy(&dispatcher, pak + PAK_DISPATCHER, 8);
    memcpy(&backend,    pak + PAK_STORE, 8);
    if (dispatcher != self) { g_backendWhy = 2; return; }
    if (!PlausiblePtr(backend, 8) || !Readable(backend, PS_NEEDS_UPDATE + 1)) { g_backendWhy = 3; return; }
    const TArr* list = (const TArr*)(backend + PS_CONTAINERS);
    uint8_t flag = backend[PS_NEEDS_UPDATE];
    if (list->num < 0 || list->max < list->num || list->max > 100000 || flag > 1
        || (list->max > 0 && !PlausiblePtr(list->data, 8))) { g_backendWhy = 3; return; }
    if (!Readable(g_reg.lockSlot, 8) || !Readable(g_reg.unlockSlot, 8)) { g_backendWhy = 3; return; }
    g_backend = backend;
}

static const wchar_t* BackendProblem(void)
{
    switch (g_backendWhy) {
    case 1:  return L"the game mounted pakchunk0 from a place this loader does not know";
    case 2:  return L"the game's pak file object was not where it was expected";
    case 3:  return L"the game's package store did not look as expected";
    default: return L"the game's package store code was not found";
    }
}

/* Hand one freshly mounted mod container to the package store.
 * 1 = done, 0 = not possible (logged by the caller).                         */
static int RegisterModContainer(const uint8_t* result, int32_t order)
{
    if (!g_backend || !result || *(const int32_t*)result != 0) return 0;
    if (!g_hdrArena || g_hdrArenaUsed + HEADER_SIZE > g_hdrArenaSize) {
        g_hdrArena = (uint8_t*)VirtualAlloc(NULL, 0x10000, MEM_COMMIT | MEM_RESERVE, PAGE_READWRITE);
        if (!g_hdrArena) return 0;
        g_hdrArenaUsed = 0;
        g_hdrArenaSize = 0x10000;
    }
    uint8_t* hdr = g_hdrArena + g_hdrArenaUsed;
    g_hdrArenaUsed += (HEADER_SIZE + 15u) & ~(size_t)15u;
    /* the header moves here: its arrays now belong to this copy (the result
     * buffer is cleared before the next mount, it never frees anything)     */
    memcpy(hdr, result + RESULT_VALUE_OFFSET, HEADER_SIZE);

    LockFn lock = *g_reg.lockSlot, unlock = *g_reg.unlockSlot;
    lock(g_backend + PS_LOCK);
    int32_t seq  = __sync_fetch_and_add((volatile int32_t*)(void*)(g_backend + PS_NEXT_SEQ), 1);
    TArr*   list = (TArr*)(void*)(g_backend + PS_CONTAINERS);
    int32_t old  = list->num;
    list->num = old + 1;
    if (list->num > list->max)
        g_reg.grow(list, old);
    uint8_t* e = list->data + (size_t)old * PS_ENTRY_SIZE;
    uint64_t h = (uint64_t)(uintptr_t)hdr;
    uint32_t o = (uint32_t)order, q = (uint32_t)seq;
    memset(e, 0, PS_ENTRY_SIZE);
    memcpy(e, &h, 8);
    memcpy(e + 8, &o, 4);
    memcpy(e + 12, &q, 4);
    g_reg.sort(list->data, list->num, 0, 0);
    g_backend[PS_NEEDS_UPDATE] = 1;
    unlock(g_backend + PS_LOCK);
    return 1;
}

/* -----------------------------------------------------------------------------
 *  Code patching
 * ----------------------------------------------------------------------------- */

/* Swap one byte of game code, only if it currently holds `expected`. */
static int PatchByte(uint8_t* addr, uint8_t expected, uint8_t replacement)
{
    DWORD oldProtect = 0, ignored = 0;

    if (!addr || *addr != expected)
        return 0;
    if (!VirtualProtect(addr, 1, PAGE_EXECUTE_READWRITE, &oldProtect))
        return 0;

    *addr = replacement;
    FlushInstructionCache(GetCurrentProcess(), addr, 1);
    VirtualProtect(addr, 1, oldProtect, &ignored);
    return 1;
}

static int TryEnterMount(void)
{
    DWORD tid = GetCurrentThreadId();
    if (__sync_val_compare_and_swap(&g_mountOwner, 0, tid) != 0 &&
        __sync_val_compare_and_swap(&g_mountOwner, tid, tid) != tid)
        return 0;
    ++g_mountDepth;
    return 1;
}

static void EnterMount(void)
{
    while (!TryEnterMount()) Sleep(1);
}

static void LeaveMount(void)
{
    if (--g_mountDepth == 0) __sync_lock_release(&g_mountOwner);
}

/* Both native mounts and MountAll must enter here. No batch-wide patch window:
 * restore immediately after the real call, even for failure or NULL results.
 * All six arguments and the engine's actual return value remain unchanged. */
static void* MountWithSignaturePolicy(void* self, void* outResult,
                                      const wchar_t* tocPath, int32_t order,
                                      const void* keyGuid, const void* aesKey,
                                      int allowUnsigned)
{
    EnterMount();
    uint8_t actual = g_sigBranch ? *g_sigBranch : 0;
    uint8_t previous = g_signaturePolicy;
    uint8_t wanted = g_mountEnabled && allowUnsigned ? 0xEB : 0x74;
    if ((actual != 0x74 && actual != 0xEB) ||
        (actual != previous && !PatchByte(g_sigBranch, actual, previous)) ||
        (previous != wanted && !PatchByte(g_sigBranch, previous, wanted))) {
        Log(L"IoStore signature policy unavailable; mount not attempted path=%s", tocPath ? tocPath : L"<null>");
        LeaveMount();
        return NULL;
    }
    if (wanted == 0xEB)
        Log(L"unsigned utoc scope: path=%s", tocPath);
    g_signaturePolicy = wanted;
    void* result = g_origMount(self, outResult, tocPath, order, keyGuid, aesKey);
    g_signaturePolicy = previous;
    if (previous != wanted && !PatchByte(g_sigBranch, wanted, previous))
        Log(L"ERROR: IoStore signature policy restoration failed path=%s", tocPath ? tocPath : L"<null>");
    LeaveMount();
    return result;
}

/* Other threads are paused while several bytes of a function are rewritten,
 * so none of them can be running half-way through the bytes being replaced.
 * Nothing between Freeze and Thaw may allocate memory or write the log.       */
#define MAX_THREADS 1024
static DWORD  s_threadIds[MAX_THREADS];
static HANDLE s_threads[MAX_THREADS];
static int    s_threadCount;

static void Thaw(void)
{
    for (int i = 0; i < s_threadCount; ++i) {
        ResumeThread(s_threads[i]);
        CloseHandle(s_threads[i]);
    }
    s_threadCount = 0;
}

/* Returns 1 with other threads paused and none inside [lo, lo+len); 0 if not. */
static int Freeze(const uint8_t* lo, size_t len)
{
    DWORD pid = GetCurrentProcessId();
    DWORD tid = GetCurrentThreadId();

    for (int attempt = 0; attempt < 100; ++attempt) {
        int    count = 0;
        HANDLE snap  = CreateToolhelp32Snapshot(TH32CS_SNAPTHREAD, 0);
        if (snap == INVALID_HANDLE_VALUE)
            return 0;

        THREADENTRY32 te;
        memset(&te, 0, sizeof(te));
        te.dwSize = sizeof(te);
        if (Thread32First(snap, &te)) {
            do {
                if (te.th32OwnerProcessID == pid && te.th32ThreadID != tid && count < MAX_THREADS)
                    s_threadIds[count++] = te.th32ThreadID;
                te.dwSize = sizeof(te);
            } while (Thread32Next(snap, &te));
        }
        CloseHandle(snap);

        s_threadCount = 0;
        for (int i = 0; i < count; ++i) {
            HANDLE th = OpenThread(THREAD_SUSPEND_RESUME | THREAD_GET_CONTEXT | THREAD_QUERY_INFORMATION,
                                   0, s_threadIds[i]);
            if (!th) continue;
            if (SuspendThread(th) == 0xFFFFFFFFu) { CloseHandle(th); continue; }
            s_threads[s_threadCount++] = th;
        }

        int busy = 0;
        for (int i = 0; i < s_threadCount; ++i) {
            CONTEXT64 ctx;
            ctx.ContextFlags = CONTEXT_CONTROL;
            ctx.Rip          = 0;
            if (GetThreadContext(s_threads[i], &ctx)) {
                const uint8_t* rip = (const uint8_t*)(uintptr_t)ctx.Rip;
                if (rip > lo && rip < lo + len)
                    busy = 1;
            }
        }
        if (!busy)
            return 1;

        Thaw();
        Sleep(1);
    }
    return 0;
}

static int WriteCode(uint8_t* addr, const uint8_t* bytes, size_t len)
{
    DWORD oldProtect = 0, ignored = 0;
    int   ok = 0;

    if (!Freeze(addr, len))
        return 0;

    if (VirtualProtect(addr, len, PAGE_EXECUTE_READWRITE, &oldProtect)) {
        memcpy(addr, bytes, len);
        VirtualProtect(addr, len, oldProtect, &ignored);
        FlushInstructionCache(GetCurrentProcess(), addr, len);
        ok = 1;
    }

    Thaw();
    return ok;
}

static void PutAbsJump(uint8_t* at, const void* target)     /* 14 bytes */
{
    uint64_t t = (uint64_t)(uintptr_t)target;
    at[0] = 0xFF; at[1] = 0x25; at[2] = at[3] = at[4] = at[5] = 0;   /* jmp [rip+0] */
    memcpy(at + 6, &t, 8);
}

/* Only for the explicitly verified, position-independent prologues above. */
static uint8_t* MakeTrampoline(uint8_t* fn, size_t copyLen)
{
    DWORD ignored = 0;
    uint8_t* tramp = (uint8_t*)VirtualAlloc(NULL, 64, MEM_COMMIT | MEM_RESERVE, PAGE_EXECUTE_READWRITE);
    if (!tramp) return NULL;
    memcpy(tramp, fn, copyLen);
    PutAbsJump(tramp + copyLen, fn + copyLen);
    if (!VirtualProtect(tramp, 64, PAGE_EXECUTE_READ, &ignored))
        return NULL;
    FlushInstructionCache(GetCurrentProcess(), tramp, 64);
    return tramp;
}

/* Redirect `fn` to `detour`. *origOut receives a callable copy of the original.
 * Only valid for a function that starts with kMountSig (see HOOK_COPY_LEN).   */
static int InstallHook(uint8_t* fn, void* detour, MountFn* origOut)
{
    uint8_t  patch[HOOK_PATCH_LEN];
    uint8_t* tramp = MakeTrampoline(fn, HOOK_COPY_LEN);
    if (!tramp)
        return 0;

    memcpy(g_savedBytes, fn, HOOK_PATCH_LEN);
    *origOut = (MountFn)(void*)tramp;                       /* must be set before patching */

    PutAbsJump(patch, detour);
    if (!WriteCode(fn, patch, HOOK_PATCH_LEN)) {
        *origOut = NULL;
        return 0;
    }
    g_mountFn = fn;
    g_hooked  = 1;
    return 1;
}

static void RemoveHook(void)
{
    if (!g_hooked)
        return;
    /* The trampoline is deliberately left allocated: a thread may still be in it. */
    if (WriteCode(g_mountFn, g_savedBytes, HOOK_PATCH_LEN))
        g_hooked = 0;
}

/* Change this Pak's reader selection, never the shared platform-file mode
 * or the Pak validity flag. The engine must still parse the actual file.
 * HookedMount handles an IoStore sidecar's separate signature requirement. */
static void* WINAPI HookedPakCtor(void* self, void* innerPlatformFile,
                                  const wchar_t* pakPath, uint8_t signedMode,
                                  uint8_t parserFlag)
{
    if (pakPath && WEndsWithI(pakPath, L"_P.pak")) {
        Log(L"custom pak: signedMode=%d -> 0 path=%s", (int)signedMode, pakPath);
        signedMode = 0;
    }
    return g_origPakCtor(self, innerPlatformFile, pakPath, signedMode, parserFlag);
}

static int InstallPakHook(uint8_t* fn)
{
    uint8_t patch[HOOK_PATCH_LEN];
    uint8_t* tramp = MakeTrampoline(fn, PAK_CTOR_COPY_LEN);
    if (!tramp) return 0;
    memcpy(g_pakSavedBytes, fn, HOOK_PATCH_LEN);
    g_origPakCtor = (PakCtorFn)(void*)tramp;  /* publish before redirecting */
    PutAbsJump(patch, (void*)HookedPakCtor);
    if (!WriteCode(fn, patch, HOOK_PATCH_LEN)) {
        g_origPakCtor = NULL;
        return 0;
    }
    g_pakCtorFn = fn;
    g_pakHooked = 1;
    return 1;
}

static void RemovePakHook(void)
{
    /* Keep the trampoline callable for any constructor already in flight. */
    if (g_pakHooked && WriteCode(g_pakCtorFn, g_pakSavedBytes, HOOK_PATCH_LEN))
        g_pakHooked = 0;
}

/* -----------------------------------------------------------------------------
 *  Mount every container in the plan (runs on the game's thread, inside the hook)
 * ----------------------------------------------------------------------------- */
static void MountAll(void* self, const wchar_t* triggerPath, int pakOnly)
{
    wchar_t*      paksDir = PaksDirFrom(triggerPath);
    ContainerList plan;
    ModList       modList;
    BuildPlan(&plan, &modList, paksDir);
    free(paksDir);

    size_t total = 0, ok = 0, mods = 0, utocOk = 0, tableOnly = 0, attempted = 0;
    for (size_t i = 0; i < plan.n; ++i) {
        if (plan.v[i].skip || (pakOnly && !IsPak(plan.v[i].path))) continue;
        total++;
        int first = 1;
        for (size_t j = 0; j < i; ++j)
            if (!plan.v[j].skip && plan.v[j].mod == plan.v[i].mod) { first = 0; break; }
        mods += first;
    }

    if (total == 0) {
        Log(L"nothing to mount");
        CL_Free(&plan);
        ML_Free(&modList);
        return;
    }

    if (g_backend)
        Log(L"new files: the game's package store was found; each mod is handed to it, so files new to the game load too");
    else
        Log(L"new files: %s - mods can replace game files but not add new ones", BackendProblem());

    {
        int32_t order = BASE_MOUNT_ORDER;
        for (size_t i = 0; i < plan.n; ++i) {
            if (plan.v[i].skip) continue;
            int32_t thisOrder = order++;
            if (pakOnly && !IsPak(plan.v[i].path)) continue;

            if (IsPak(plan.v[i].path)) {
                const wchar_t* path = plan.v[i].path;
                int present = PakPresent(path);
                if (!g_pakMount || present < 0) {
                    Log(L"  skipped Pak %s: verified Legacy Mount or native platformFile unavailable", path);
                    if (g_pakMount && !g_platformFile) g_pendingPaks = 1;
                    continue;
                }
                if (present) {
                    Log(L"  skipped Pak %s: already present in the native mount table", path);
                    continue;
                }
                Log(L"mounting Legacy Pak [%s] order=%d path=%s", plan.v[i].mod, thisOrder, path);
                /* NULL output is explicitly supported by the core; unlike the
                 * wrapper's shared ref, it needs no engine allocator cleanup. */
                ++attempted;
                uint8_t result = g_pakMount(g_platformFile, path, thisOrder, NULL, 1, NULL);
                present = PakPresent(path);
                Log(L"  Legacy result AL=%u pak-in-table=%d (paired UTOC registration is owned by the engine)",
                    (size_t)result, present);
                if (result) ++ok;
                else if (present == 1) ++tableOnly;
                if (present == 1) RememberGameMount(path);
                /* Do not retry a sidecar separately, even on AL=0: the engine
                 * may already own a Pak entry/header. No fake success status. */
                continue;
            }

            /* Fresh result buffer: 0x104 zero bytes, the rest 0xCD (as the original did). */
            memset(g_resultBuf, 0, 0x104);
            memset(g_resultBuf + 0x104, 0xCD, sizeof(g_resultBuf) - 0x104);
            memset(g_zeroGuid, 0, sizeof(g_zeroGuid));
            memset(g_zeroKey, 0, sizeof(g_zeroKey));

            Log(L"mounting [%s] order=%d path=%s", plan.v[i].mod, thisOrder, plan.v[i].path);

            ++attempted;
            void* r = MountWithSignaturePolicy(self, g_resultBuf, plan.v[i].path,
                                               thisOrder, g_zeroGuid, g_zeroKey, 1);

            int32_t code = -1;
            wchar_t msg[129];
            msg[0] = 0;
            if (r) {
                memcpy(&code, r, sizeof(code));
                memcpy(msg, (const uint8_t*)r + 4, 128 * sizeof(wchar_t));
                msg[128] = 0;
            }
            Log(L"  result code=%d message=%s", code, r ? msg : L"<null>");
            if (code == 0) {
                ++ok;
                ++utocOk;
                StoreSync_OnMod((const uint8_t*)r);
                if (RegisterModContainer((const uint8_t*)r, thisOrder))
                    g_registered++;
                else
                    g_registerFailed++;
            }
        }

    }

    Log(L"done: %u of %u attempted mount requests returned success (%u planned, %u mod(s))", ok, attempted, total, mods);
    if (tableOnly)
        Log(L"Legacy Pak: %u additional Pak(s) entered the native table but returned AL=0; pure Pak or sidecar failure, see individual results", tableOnly);
    if (g_pendingPaks) Log(L"Legacy Pak: deferred until a verified native platformFile is captured; UTOC results retained");
    if (g_backend)
        Log(L"new files: %u explicit UTOC containers handed to the game's package store (%u UTOC successes this pass); Legacy sidecars are registered by the engine%s", (size_t)g_registered, utocOk,
            g_registerFailed ? L" (the rest could not be: out of memory)" : L"");
    CL_Free(&plan);
    ML_Free(&modList);
}

/* -----------------------------------------------------------------------------
 *  The hook
 * ----------------------------------------------------------------------------- */

/* Case-insensitive compare the way Unreal's FString does it (lower case). */
static int UeICmp(const wchar_t* a, const wchar_t* b)
{
    for (;; ++a, ++b) {
        wchar_t x = (*a >= L'A' && *a <= L'Z') ? (wchar_t)(*a + 32) : *a;
        wchar_t y = (*b >= L'A' && *b <= L'Z') ? (wchar_t)(*b + 32) : *b;
        if (x != y) return x < y ? -1 : 1;
        if (!x) return 0;
    }
}

static int InTildeFolder(const wchar_t* path)
{
    for (const wchar_t* p = path; *p; ++p)
        if ((*p == L'\\' || *p == L'/') && p[1] == L'~') return 1;
    return 0;
}

/* The game mounts global.utoc first, then its pak files in descending name
 * order: ~mods, pakchunkN ... pakchunk1, pakchunk0, then anything that sorts
 * below pakchunk0. So if the first container this hook ever sees sorts below
 * pakchunk0-Windows.utoc, pakchunk0 was mounted before the hook was in place.
 * Anything else means pakchunk0 is still to come, exactly as before.        */
static int MountedAfterPakchunk0(const wchar_t* path)
{
    if (IsFileNamed(path, L"global.utoc") || InTildeFolder(path)) return 0;
    return UeICmp(FileNamePart(path), L"pakchunk0-windows.utoc") < 0;
}
/* Keep r14 and the original caller on this invocation's stack, so concurrent
 * or nested entries cannot overwrite another call's backend-discovery inputs.
 * The first six arguments retain the game's ABI; the last two are hook-only. */
void* WINAPI HookedMount(void* self, void* outResult, const wchar_t* tocPath,
                         int32_t order, const void* keyGuid, const void* aesKey,
                         void* entryR14, void* caller);
void HookEntry(void);
__asm__(
    ".text\n"
    ".p2align 4\n"
    ".globl HookEntry\n"
#ifdef _WIN32
    ".seh_proc HookEntry\n"
#endif
    "HookEntry:\n"
    "    subq $0x48, %rsp\n"
#ifdef _WIN32
    ".seh_stackalloc 0x48\n"
    ".seh_endprologue\n"
#endif
    "    movq 0x70(%rsp), %rax\n"
    "    movq %rax, 0x20(%rsp)\n" /* original fifth argument */
    "    movq 0x78(%rsp), %rax\n"
    "    movq %rax, 0x28(%rsp)\n" /* original sixth argument */
    "    movq %r14, 0x30(%rsp)\n"
    "    movq 0x48(%rsp), %rax\n"
    "    movq %rax, 0x38(%rsp)\n" /* original return address */
    "    call HookedMount\n"
    "    addq $0x48, %rsp\n"
    "    ret\n"
#ifdef _WIN32
    ".seh_endproc\n"
#endif
);

__attribute__((used, noinline))
void* WINAPI HookedMount(void* self, void* outResult, const wchar_t* tocPath,
                         int32_t order, const void* keyGuid, const void* aesKey,
                         void* entryR14, void* caller)
{
    EnterMount();
    if (!g_mountEnabled) {
        void* r = MountWithSignaturePolicy(self, outResult, tocPath, order, keyGuid, aesKey, 0);
        LeaveMount();
        return r;
    }
    NoteStoreBackend(self, caller, entryR14);
    NotePlatformFile(self, caller, entryR14);

    /* Native custom sidecars use the same policy as the explicit plan. Stock
     * containers keep JE, including nested stock calls from a custom mount. */
    int custom = tocPath && WEndsWithI(tocPath, L"_P.utoc");
    void* result = MountWithSignaturePolicy(self, outResult, tocPath, order, keyGuid, aesKey, custom);

    int32_t code = -1;
    if (result) memcpy(&code, result, sizeof(code));
    Log(L"stock mount code=%d order=%d path=%s", code, order, tocPath);

    if (__sync_fetch_and_add(&g_stockSeen, 1) == 0 && tocPath && MountedAfterPakchunk0(tocPath))
        g_startedLate = 1;

    int ok = result && code == 0 && tocPath;
    if (ok)
        RememberGameMount(tocPath);
    StoreSync_OnStock((const uint8_t*)result, tocPath);
    /* Native mounting already handles package-store registration. Keep mod
     * import synchronization when CheckContainers later skips this sidecar. */
    if (ok && custom) StoreSync_OnMod((const uint8_t*)result);

    /* Act once: right after the game's base container mounted successfully,
     * or, if the loader started too late to see that one, at the next
     * container the game mounts successfully.                              */
    int normal = ok && IsFileNamed(tocPath, L"pakchunk0-Windows.utoc");
    int late   = ok && !normal && g_startedLate && !InTildeFolder(tocPath);
    if ((normal || late) && __sync_bool_compare_and_swap(&g_state, 0, 1)) {
        if (late)
            Log(L"the loader started after the game mounted pakchunk0-Windows.utoc; mounting the mods now, with %s", FileNamePart(tocPath));
        MountAll(self, tocPath, 0);
        g_state = 2;
    }
    if (ok && g_platformFile && g_pendingPaks && __sync_bool_compare_and_swap(&g_state, 2, 1)) {
        g_pendingPaks = 0;
        Log(L"Legacy Pak: native object is now available; retrying deferred Pak plan only");
        MountAll(self, tocPath, 1);
        g_state = 2;
    }

    LeaveMount();
    return result;                               /* the game sees its own result */
}

/* -----------------------------------------------------------------------------
 *  Pattern scan over the executable sections of the game .exe
 * ----------------------------------------------------------------------------- */
static int MatchSigCheck(const uint8_t* p)
{
    for (size_t i = 5; i < sizeof(kSigCheck); ++i)        /* bytes 1..4 are wild */
        if (p[i] != kSigCheck[i]) return 0;
    return 1;
}

static int MatchCodeSignature(const uint8_t* p, const uint8_t* sig,
                               size_t sigLen, size_t from)
{
    for (size_t i = from; i < sigLen; ++i) {
        if (sig == kPakCtorSig && ((i >= 31 && i < 35) || (i >= 47 && i < 51)))
            continue;
        if (sig == kPakMountSig && i >= 31 && i < 35) continue;
        if (p[i] != sig[i]) return 0;
    }
    return 1;
}

/* The mount function as another hook leaves it: the start replaced by a jump
 * (14-byte "jmp [rip]" or 5-byte "jmp rel32"), the rest still in place.      */
static const uint8_t* MatchHookedPrefix(const uint8_t* p, size_t avail,
                                      const uint8_t* sig, size_t sigLen)
{
    if (avail >= sigLen && p[0] == 0xFF && p[1] == 0x25 && p[2] == 0 && p[3] == 0 && p[4] == 0
        && p[5] == 0 && MatchCodeSignature(p, sig, sigLen, 14)) {
        uint64_t t;
        memcpy(&t, p + 6, 8);
        return (const uint8_t*)(uintptr_t)t;
    }
    if (avail >= sigLen && p[0] == 0xE9 && MatchCodeSignature(p, sig, sigLen, 5)) {
        int32_t rel;
        memcpy(&rel, p + 1, 4);
        return p + 5 + rel;
    }
    return NULL;
}

typedef struct {
    uint8_t*       mountAt;
    size_t         mountHits;
    uint8_t*       sigAt;
    size_t         sigHits;
    const uint8_t* hookTarget;        /* where an existing hook jumps to */
    size_t         hookedHits;
    uint8_t*       pakCtorAt;
    size_t         pakCtorHits;
    const uint8_t* pakHookTarget;
    size_t         pakHookedHits;
    uint8_t*       pakMountAt;
    size_t         pakMountHits;
} ScanResult;

static void ScanImage(uint8_t* base, ScanResult* r)
{
    int32_t  lfanew;
    uint16_t sectionCount, optSize;

    memset(r, 0, sizeof(*r));

    memcpy(&lfanew, base + 0x3C, 4);
    uint8_t* nt = base + lfanew;
    memcpy(&sectionCount, nt + 0x06, 2);
    memcpy(&optSize,      nt + 0x14, 2);
    uint8_t* sec = nt + 0x18 + optSize;

    for (uint16_t s = 0; s < sectionCount; ++s, sec += 40) {
        uint32_t vsize, vaddr, flags;
        memcpy(&vsize, sec + 8,  4);
        memcpy(&vaddr, sec + 12, 4);
        memcpy(&flags, sec + 36, 4);
        if (!(flags & IMAGE_SCN_MEM_EXECUTE))
            continue;

        const uint8_t* p = base + vaddr;
        for (size_t i = 0; i < vsize; ++i) {
            uint8_t c = p[i];
            if (c == 0x40) {
                if (vsize - i >= sizeof(kPakMountSig)
                    && MatchCodeSignature(p + i, kPakMountSig, sizeof(kPakMountSig), 0)) {
                    r->pakMountAt = (uint8_t*)p + i;
                    ++r->pakMountHits;
                }
                if (vsize - i >= sizeof(kMountSig) && memcmp(p + i, kMountSig, sizeof(kMountSig)) == 0) {
                    r->mountAt = (uint8_t*)p + i;
                    ++r->mountHits;
                }
            } else if (c == kPakCtorSig[0]) {
                if (vsize - i >= sizeof(kPakCtorSig) && memcmp(p + i, kPakCtorSig, 31) == 0
                    && MatchCodeSignature(p + i, kPakCtorSig, sizeof(kPakCtorSig), 35)) {
                    r->pakCtorAt = (uint8_t*)p + i;
                    ++r->pakCtorHits;
                }
            } else if (c == 0xE8) {
                if (vsize - i >= sizeof(kSigCheck) && MatchSigCheck(p + i)) {
                    r->sigAt = (uint8_t*)p + i;
                    ++r->sigHits;
                }
            } else if (c == 0xFF || c == 0xE9) {
                const uint8_t* t = MatchHookedPrefix(p + i, vsize - i, kMountSig, sizeof(kMountSig));
                if (t) {
                    r->hookTarget = t;
                    ++r->hookedHits;
                }
                t = MatchHookedPrefix(p + i, vsize - i, kPakCtorSig, sizeof(kPakCtorSig));
                if (t) {
                    r->pakHookTarget = t;
                    ++r->pakHookedHits;
                }
            }
        }
    }
}

/* -----------------------------------------------------------------------------
 *  Install: runs once when UE4SS creates the mod
 * ----------------------------------------------------------------------------- */
static void StripLastComponent(wchar_t* path)
{
    size_t n = WLen(path);
    while (n > 0 && path[n - 1] != L'\\' && path[n - 1] != L'/') --n;
    path[n ? n - 1 : 0] = 0;
}

static void ReportOtherHook(const uint8_t* target)
{
    HANDLE   other = NULL;
    wchar_t* who   = (wchar_t*)malloc(PATH_CAP * sizeof(wchar_t));
    if (!who) return;
    who[0] = 0;
    if (GetModuleHandleExW(GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS | GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT,
                           target, &other) && other) {
        DWORD n = GetModuleFileNameW(other, who, PATH_CAP);
        if (n == 0 || n >= PATH_CAP) who[0] = 0;
    }
    Log(L"the game's mount function is already hooked by: %s", who[0] ? who : L"(unknown code, not a DLL file)");
    Log(L"another copy of the mod loader (or another tool that hooks the same function) is installed.");
    Log(L"Only one can work: keep IoStoreLoaderMod and remove the other. This copy changes nothing.");
    free(who);
}

static int Install(void)
{
    /* ...\<ThisMod>\dlls\main.dll  ->  ...\<ThisMod> */
    DWORD len = GetModuleFileNameW(g_hModule, g_modRoot, PATH_CAP);
    if (len == 0 || len >= PATH_CAP)
        return 0;
    StripLastComponent(g_modRoot);
    StripLastComponent(g_modRoot);

    /* A new log at each game start; added to if UE4SS restarts the mod. */
    int      again   = g_installRuns++ > 0;
    wchar_t* logPath = NewPath(L"%s\\AC8IoStoreLoader.log", g_modRoot);
    if (logPath) {
        g_log = CreateFileW(logPath, again ? FILE_APPEND_DATA : GENERIC_WRITE,
                            FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
                            NULL, again ? OPEN_ALWAYS : CREATE_ALWAYS, FILE_ATTRIBUTE_NORMAL, NULL);
        free(logPath);
    }

    Log(L"AC8 mod loader %s%s", LOADER_VERSION, again ? L" (started again by UE4SS)" : L"");
    Log(L"mod root=%s", g_modRoot);

    uint8_t* exe = (uint8_t*)GetModuleHandleW(NULL);
    if (!exe) {
        Log(L"could not find the game executable in memory");
        return 0;
    }

    ScanResult scan;
    ScanImage(exe, &scan);

    Log(L"mount signature matches=%u address=%p", scan.mountHits, scan.mountAt);
    Log(L"pak constructor matches=%u address=%p", scan.pakCtorHits, scan.pakCtorAt);
    Log(L"Legacy Pak mount matches=%u address=%p", scan.pakMountHits, scan.pakMountAt);
    Log(L"signature requirement matches=%u branch=%p", scan.sigHits,
        scan.sigHits == 1 ? scan.sigAt + SIG_BRANCH_OFFSET : NULL);

    int ownMount = g_hooked && g_origMount && scan.mountHits == 0 && scan.hookedHits == 1
        && (scan.hookTarget == (const uint8_t*)(void*)HookEntry || scan.hookTarget == (const uint8_t*)(void*)HookedMount);
    int ownPak = g_pakHooked && g_origPakCtor && scan.pakCtorHits == 0 && scan.pakHookedHits == 1
        && scan.pakHookTarget == (const uint8_t*)(void*)HookedPakCtor;
    if (scan.hookedHits && !ownMount) {
        ReportOtherHook(scan.hookTarget);
        return 0;
    }
    if (scan.pakHookedHits && !ownPak) {
        Log(L"Pak constructor already hooked by another loader at %p; nothing changed", scan.pakHookTarget);
        return 0;
    }
    if ((!ownMount && scan.mountHits != 1) || (!ownPak && scan.pakCtorHits != 1) || scan.sigHits != 1) {
        Log(L"patterns not found exactly once - is this a different game version? nothing changed");
        Log(L"(made for Ace Combat 8 1.1.2.0. After a game update the loader needs updating too.)");
        return 0;
    }
    g_sigBranch = scan.sigAt + SIG_BRANCH_OFFSET;
    /* Bind capture to the verified call inside this exact Legacy function.
     * Old or differently hooked builds retain UTOC loading but cannot mount Pak. */
    g_pakMount = NULL;
    if (scan.pakMountHits == 1) {
        uint8_t* call = scan.pakMountAt + PAK_IOSTORE_RETURN - 5;
        uint8_t* mountAt = ownMount ? g_mountFn : scan.mountAt;
        if (Readable(call, 5) && call[0] == 0xE8 && call + 5 + LoadI32(call + 1) == mountAt)
            g_pakMount = (PakMountFn)(void*)scan.pakMountAt;
    }
    if (!g_pakMount) Log(L"Legacy Pak active mounting unavailable: unique entry/native IoStore call not verified; UTOC loading retained");
    FindStoreReg(exe, ownMount ? g_mountFn : scan.mountAt);
    /* The game calls into this DLL from now on, so it must never be unloaded
     * (UE4SS can unload and reload mods). Pinned, it stays until the game ends. */
    HANDLE self = NULL;
    if (!GetModuleHandleExW(GET_MODULE_HANDLE_EX_FLAG_PIN | GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS,
                            (const void*)HookedMount, &self))
    {
        Log(L"could not pin main.dll in memory; no new hooks installed");
        return 0;
    }
    if (!ownPak && !InstallPakHook(scan.pakCtorAt)) {
        Log(L"Pak constructor hook install failed; no new hooks installed");
        return 0;
    }
    g_mountEnabled = 1; /* publish before another thread can enter the hook */
    if (!ownMount && !InstallHook(scan.mountAt, (void*)HookEntry, &g_origMount)) {
        g_mountEnabled = 0;
        if (!ownPak) RemovePakHook();
        Log(L"IoStore hook install failed; Pak hook remains=%s", g_pakHooked ? L"yes" : L"no");
        return 0;
    }
    Log(L"Pak hook installed: only *_P.pak uses unsigned reader; must be installed before its constructor runs");
    Log(L"IoStore signature policy: native *_P.utoc and explicit mod plan share per-call bypass; stock checks retained");
    if (g_state == 2)
        Log(L"hook installed again; the mods were already mounted in this game session");
    else
        Log(L"hook installed, waiting for the game to mount pakchunk0-Windows.utoc");
    return 1;
}

static void Uninstall(void)
{
    /* Wait for native and explicit mounts, not just MountAll. If a call is
     * still in flight, keep the pinned hooks rather than tear down its policy. */
    int entered = 0;
    for (int i = 0; i < 20000; ++i) {
        if ((entered = TryEnterMount())) break;
        Sleep(1);
    }
    if (!entered) {
        Log(L"unload deferred: a mount is still active; pinned hooks retained");
        return;
    }
    if (g_mountDepth > 1 || (g_sigBranch && *g_sigBranch != 0x74 &&
        !PatchByte(g_sigBranch, 0xEB, 0x74))) {
        Log(L"unload deferred: signature scope active or check could not be restored");
        LeaveMount();
        return;
    }
    g_mountEnabled = 0;
    RemoveHook();
    RemovePakHook();
    LeaveMount();
    if (g_log != INVALID_HANDLE_VALUE) {
        Log(L"unloaded, hook removed=%s", g_hooked ? L"no" : L"yes");
        Log(L"Pak constructor hook removed=%s", g_pakHooked ? L"no" : L"yes");
        while (__sync_lock_test_and_set(&g_logLock, 1))
            Sleep(0);
        HANDLE h = g_log;
        g_log = INVALID_HANDLE_VALUE;
        __sync_lock_release(&g_logLock);
        CloseHandle(h);
    }
}

/* -----------------------------------------------------------------------------
 *  The UE4SS mod object
 *
 *  RC::CppUserModBase is a C++ class inside UE4SS.dll. This is the same object
 *  the original DLL created, laid out by hand:
 *      +0x00  vtable pointer     (slot 0 = deleting destructor, rest = callbacks)
 *      +0x20  std::wstring ModName
 *      +0x40  std::wstring ModVersion
 *      +0x60  std::wstring ModDescription
 *      +0x80  std::wstring ModAuthors
 *  The original object was 0xC0 bytes and overrode no callbacks.
 * ----------------------------------------------------------------------------- */
typedef struct {                       /* MSVC std::wstring, 32 bytes */
    union { wchar_t buf[8]; wchar_t* ptr; } u;
    size_t size;
    size_t cap;
} MsvcWString;
_Static_assert(sizeof(MsvcWString) == 32, "std::wstring layout");

static void SetWString(MsvcWString* s, const wchar_t* text)
{
    size_t n = WLen(text);
    if (n <= 7) {
        memcpy(s->u.buf, text, (n + 1) * sizeof(wchar_t));
        s->size = n;
        s->cap  = 7;
        return;
    }
    size_t   cap = n | 7;
    wchar_t* p   = (wchar_t*)malloc((cap + 1) * sizeof(wchar_t));   /* freed by UE4SS */
    if (!p) return;
    memcpy(p, text, (n + 1) * sizeof(wchar_t));
    s->u.ptr = p;
    s->size  = n;
    s->cap   = cap;
}

#define MOD_OBJECT_SIZE 0x200          /* 0xC0 is used; the rest is headroom    */
#define VTABLE_SLOTS    40             /* 16 are used; the rest is headroom     */

static void* WINAPI Mod_DeletingDtor(void* self, uint32_t flags)
{
    if (g_instances > 0 && --g_instances == 0)
        Uninstall();
    UE4SS_ModBase_Dtor(self);
    if (flags & 1) free(self);
    return self;
}

static void* WINAPI Mod_NoOp(void)     /* every UE4SS callback: do nothing      */
{
    return NULL;
}

static void* const g_vtable[1 + VTABLE_SLOTS] = {
    [0]                    = NULL,                          /* RTTI slot, unused */
    [1]                    = (void*)Mod_DeletingDtor,
    [2 ... VTABLE_SLOTS]   = (void*)Mod_NoOp,
};

EXPORT void* start_mod(void)
{
    uint8_t* mod = (uint8_t*)malloc(MOD_OBJECT_SIZE);
    if (!mod)
        return NULL;
    memset(mod, 0, MOD_OBJECT_SIZE);

    UE4SS_ModBase_Ctor(mod);
    *(void* const**)mod = &g_vtable[1];

    SetWString((MsvcWString*)(mod + 0x20), L"AC8ModLoader");
    SetWString((MsvcWString*)(mod + 0x40), LOADER_VERSION L"-ac8-1.1.2.0-ue5.4");
    SetWString((MsvcWString*)(mod + 0x60), L"Loads IoStore mods and handles signatures for custom *_P.pak / *_P.utoc");
    SetWString((MsvcWString*)(mod + 0x80), L"voidcallsreaper");

    /* Started twice (e.g. listed in mods.txt and enabled.txt)? Install once. */
    if (g_instances++ == 0)
        Install();
    return mod;
}

EXPORT void uninstall_mod(void* mod)
{
    typedef void* (WINAPI *DeletingDtor)(void*, uint32_t);
    if (mod)
        ((DeletingDtor)(**(void***)mod))(mod, 1);
}

BOOL WINAPI DllMain(HANDLE module, DWORD reason, void* reserved)
{
    (void)reserved;
    if (reason == DLL_PROCESS_ATTACH) {
        g_hModule = module;
        DisableThreadLibraryCalls(module);
    }
    return 1;
}
