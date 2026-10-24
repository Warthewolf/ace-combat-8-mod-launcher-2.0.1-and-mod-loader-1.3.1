/* Linux stand-ins for the Win32 calls the loader uses, for the self-test only.
 * Every function is ms_abi so the loader's calls look exactly as on Windows. */
#include "../src/win32_min.h"
#include "shim.h"

#include <dirent.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <unistd.h>

/* ---- test knobs / counters ------------------------------------------------ */
wchar_t  shim_module_path[1024];
wchar_t  shim_exe_path[1024];      /* what GetModuleFileNameW(NULL) reports    */
wchar_t  shim_other_path[1024];    /* a second "DLL" for the other-loader test */
uintptr_t shim_other_lo, shim_other_hi;
int      shim_rw_allocs;
int      shim_pins;
uint8_t* shim_exe_base;
int      shim_fake_threads;        /* how many "other threads" to report       */
uint64_t shim_busy_rip;            /* a fake thread sits here ...              */
int      shim_busy_times;          /* ... for this many freeze attempts        */
int      shim_suspends, shim_resumes, shim_thread_opens, shim_thread_closes;
int      shim_frozen_now;          /* >0 while fake threads are suspended      */
int      shim_alloc_while_frozen;  /* loader allocated/wrote files while frozen */
int      shim_ctor_calls, shim_dtor_calls;
wchar_t  shim_mod_strings[4][128];

/* ---- UTF-8 <-> UTF-16 ------------------------------------------------------ */
size_t u16_to_u8(const wchar_t* w, size_t wn, char* out, size_t cap)
{
    size_t o = 0;
    for (size_t i = 0; i < wn; ++i) {
        uint32_t c = (uint16_t)w[i];
        if (c >= 0xD800 && c < 0xDC00 && i + 1 < wn) {
            uint32_t lo = (uint16_t)w[i + 1];
            c = 0x10000 + ((c - 0xD800) << 10) + (lo - 0xDC00);
            ++i;
        }
        char b[4]; int n;
        if (c < 0x80)         { b[0] = (char)c; n = 1; }
        else if (c < 0x800)   { b[0] = (char)(0xC0 | (c >> 6)); b[1] = (char)(0x80 | (c & 63)); n = 2; }
        else if (c < 0x10000) { b[0] = (char)(0xE0 | (c >> 12)); b[1] = (char)(0x80 | ((c >> 6) & 63)); b[2] = (char)(0x80 | (c & 63)); n = 3; }
        else                  { b[0] = (char)(0xF0 | (c >> 18)); b[1] = (char)(0x80 | ((c >> 12) & 63)); b[2] = (char)(0x80 | ((c >> 6) & 63)); b[3] = (char)(0x80 | (c & 63)); n = 4; }
        if (o + (size_t)n > cap) break;
        memcpy(out + o, b, (size_t)n); o += (size_t)n;
    }
    return o;
}

size_t u8_to_u16(const char* s, size_t n, wchar_t* out, size_t cap)
{
    size_t o = 0;
    for (size_t i = 0; i < n; ) {
        uint32_t c = (uint8_t)s[i];
        int extra = c < 0x80 ? 0 : c < 0xE0 ? 1 : c < 0xF0 ? 2 : 3;
        if (extra == 1) c &= 0x1F; else if (extra == 2) c &= 0x0F; else if (extra == 3) c &= 0x07;
        ++i;
        for (int k = 0; k < extra && i < n; ++k, ++i) c = (c << 6) | ((uint8_t)s[i] & 63);
        if (c >= 0x10000) {
            if (o + 2 > cap) break;
            c -= 0x10000;
            out[o++] = (wchar_t)(0xD800 + (c >> 10));
            out[o++] = (wchar_t)(0xDC00 + (c & 0x3FF));
        } else {
            if (o + 1 > cap) break;
            out[o++] = (wchar_t)c;
        }
    }
    return o;
}

size_t wlen16(const wchar_t* w) { size_t n = 0; while (w[n]) ++n; return n; }

void win_path_to_posix(const wchar_t* w, char* out, size_t cap)
{
    size_t n = u16_to_u8(w, wlen16(w), out, cap - 1);
    out[n] = 0;
    for (char* p = out; *p; ++p) if (*p == '\\') *p = '/';
}

/* ---- handles ---------------------------------------------------------------- */
enum { H_FILE = 0x46494C45, H_FIND = 0x46494E44, H_SNAP = 0x534E4150, H_THREAD = 0x54485244 };
typedef struct { int magic; FILE* f; DIR* d; char dir[2048]; char mask[256]; int idx; } Obj;

static Obj* new_obj(int magic) { Obj* o = calloc(1, sizeof(Obj)); o->magic = magic; return o; }

/* ---- modules ----------------------------------------------------------------- */
HANDLE WINAPI GetModuleHandleW(const wchar_t* name) { (void)name; return shim_exe_base; }

#define OTHER_MODULE ((HANDLE)(intptr_t)0x5151)

DWORD WINAPI GetModuleFileNameW(HANDLE module, wchar_t* buf, DWORD cap)
{
    const wchar_t* src = module == NULL ? shim_exe_path
                       : module == OTHER_MODULE ? shim_other_path : shim_module_path;
    size_t n = wlen16(src);
    if (n == 0) return 0;
    if (n >= cap) n = cap - 1;
    memcpy(buf, src, n * sizeof(wchar_t));
    buf[n] = 0;
    return (DWORD)n;
}

BOOL WINAPI GetModuleHandleExW(DWORD flags, const void* addr, HANDLE* module)
{
    *module = NULL;
    if (flags == (GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS | GET_MODULE_HANDLE_EX_FLAG_PIN)) {
        shim_pins++;                              /* the loader pinning itself */
        *module = (HANDLE)(intptr_t)0x1234;
        return 1;
    }
    if (flags != (GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS | GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT)) {
        fprintf(stderr, "GetModuleHandleExW: unexpected flags %x\n", flags); abort();
    }
    if ((uintptr_t)addr >= shim_other_lo && (uintptr_t)addr < shim_other_hi) { *module = OTHER_MODULE; return 1; }
    return 0;
}

BOOL WINAPI DisableThreadLibraryCalls(HANDLE module) { (void)module; return 1; }

/* ---- files -------------------------------------------------------------------- */
HANDLE WINAPI CreateFileW(const wchar_t* path, DWORD access, DWORD share, void* sec,
                          DWORD disposition, DWORD flags, HANDLE templ)
{
    (void)share; (void)sec; (void)disposition; (void)flags; (void)templ;
    char p[4096];
    win_path_to_posix(path, p, sizeof(p));
    FILE* f = fopen(p, (access & GENERIC_WRITE) ? "wb" : (access & FILE_APPEND_DATA) ? "ab" : "rb");
    if (!f) return INVALID_HANDLE_VALUE;
    Obj* o = new_obj(H_FILE);
    o->f = f;
    return o;
}

BOOL WINAPI ReadFile(HANDLE h, void* buf, DWORD n, DWORD* got, void* ov)
{
    (void)ov;
    *got = (DWORD)fread(buf, 1, n, ((Obj*)h)->f);
    return 1;
}

BOOL WINAPI WriteFile(HANDLE h, const void* buf, DWORD n, DWORD* put, void* ov)
{
    (void)ov;
    if (shim_frozen_now) shim_alloc_while_frozen++;
    *put = (DWORD)fwrite(buf, 1, n, ((Obj*)h)->f);
    return 1;
}

DWORD WINAPI GetFileSize(HANDLE h, DWORD* high)
{
    FILE* f = ((Obj*)h)->f;
    long cur = ftell(f);
    fseek(f, 0, SEEK_END);
    long size = ftell(f);
    fseek(f, cur, SEEK_SET);
    if (high) *high = 0;
    return (DWORD)size;
}

BOOL WINAPI FlushFileBuffers(HANDLE h) { fflush(((Obj*)h)->f); return 1; }

BOOL WINAPI CloseHandle(HANDLE h)
{
    Obj* o = (Obj*)h;
    if (o->magic == H_FILE) fclose(o->f);
    else if (o->magic == H_THREAD) shim_thread_closes++;
    else if (o->magic != H_SNAP) { fprintf(stderr, "CloseHandle on bad handle\n"); abort(); }
    o->magic = 0;
    free(o);
    return 1;
}

DWORD WINAPI GetFileAttributesW(const wchar_t* path)
{
    char p[4096];
    struct stat st;
    win_path_to_posix(path, p, sizeof(p));
    if (stat(p, &st) != 0) return INVALID_FILE_ATTRIBUTES;
    return S_ISDIR(st.st_mode) ? FILE_ATTRIBUTE_DIRECTORY : FILE_ATTRIBUTE_NORMAL;
}

/* Deliberately sloppier than Windows: "*.utoc" also returns names that merely
 * contain ".utoc", so the loader's own suffix check gets exercised.           */
static int find_next(Obj* o, WIN32_FIND_DATAW* fd)
{
    struct dirent* e;
    while ((e = readdir(o->d)) != NULL) {
        if (strcmp(o->mask, "*") != 0) {
            const char* want = o->mask + 1;              /* ".utoc" */
            if (!strcasestr(e->d_name, want)) continue;
        }
        char full[4096];
        struct stat st;
        snprintf(full, sizeof(full), "%s/%s", o->dir, e->d_name);
        if (stat(full, &st) != 0) continue;
        struct stat lst;
        int isLink = lstat(full, &lst) == 0 && S_ISLNK(lst.st_mode);
        memset(fd, 0, sizeof(*fd));
        fd->dwFileAttributes = S_ISDIR(st.st_mode) ? FILE_ATTRIBUTE_DIRECTORY : FILE_ATTRIBUTE_NORMAL;
        if (isLink) fd->dwFileAttributes |= FILE_ATTRIBUTE_REPARSE_POINT;
        uint64_t ft = (uint64_t)st.st_mtim.tv_sec * 10000000ull + (uint64_t)st.st_mtim.tv_nsec / 100u + 116444736000000000ull;
        fd->ftLastWriteTime[0] = (uint32_t)ft;
        fd->ftLastWriteTime[1] = (uint32_t)(ft >> 32);
        size_t n = u8_to_u16(e->d_name, strlen(e->d_name), fd->cFileName, 259);
        fd->cFileName[n] = 0;
        return 1;
    }
    return 0;
}

HANDLE WINAPI FindFirstFileW(const wchar_t* pattern, WIN32_FIND_DATAW* fd)
{
    char p[4096];
    win_path_to_posix(pattern, p, sizeof(p));
    char* slash = strrchr(p, '/');
    if (!slash) return INVALID_HANDLE_VALUE;
    *slash = 0;

    DIR* d = opendir(p);
    if (!d) return INVALID_HANDLE_VALUE;
    Obj* o = new_obj(H_FIND);
    o->d = d;
    snprintf(o->dir, sizeof(o->dir), "%.2000s", p);
    snprintf(o->mask, sizeof(o->mask), "%s", slash + 1);
    if (!find_next(o, fd)) { closedir(d); free(o); return INVALID_HANDLE_VALUE; }
    return o;
}

BOOL WINAPI FindNextFileW(HANDLE h, WIN32_FIND_DATAW* fd) { return find_next((Obj*)h, fd); }

BOOL WINAPI FindClose(HANDLE h)
{
    Obj* o = (Obj*)h;
    if (o->magic != H_FIND) { fprintf(stderr, "FindClose on bad handle\n"); abort(); }
    closedir(o->d);
    free(o);
    return 1;
}

int WINAPI MultiByteToWideChar(DWORD cp, DWORD flags, const char* in, int inLen, wchar_t* out, int outCap)
{
    (void)cp; (void)flags;
    return (int)u8_to_u16(in, (size_t)inLen, out, (size_t)outCap);
}

int WINAPI WideCharToMultiByte(DWORD cp, DWORD flags, const wchar_t* in, int inLen,
                               char* out, int outCap, const char* def, BOOL* used)
{
    (void)cp; (void)flags; (void)def; (void)used;
    return (int)u16_to_u8(in, (size_t)inLen, out, (size_t)outCap);
}

/* ---- memory: protections are really enforced, so a missing VirtualProtect in
 *      the loader shows up as a crash here just as it would on Windows -------- */
static struct { uintptr_t page; DWORD prot; } s_pages[64];
static int s_pageCount;

void shim_set_page_prot(void* addr, size_t size, DWORD prot)
{
    uintptr_t lo = (uintptr_t)addr & ~(uintptr_t)4095;
    uintptr_t hi = ((uintptr_t)addr + size + 4095) & ~(uintptr_t)4095;
    int native = prot == PAGE_EXECUTE_READWRITE ? (PROT_READ | PROT_WRITE | PROT_EXEC)
                                                : (PROT_READ | PROT_EXEC);
    if (mprotect((void*)lo, hi - lo, native) != 0) { perror("mprotect"); abort(); }
    for (uintptr_t pg = lo; pg < hi; pg += 4096) {
        int i;
        for (i = 0; i < s_pageCount; ++i) if (s_pages[i].page == pg) break;
        if (i == s_pageCount) s_pages[s_pageCount++].page = pg;
        s_pages[i].prot = prot;
    }
}

#ifndef MAP_FIXED_NOREPLACE
#define MAP_FIXED_NOREPLACE 0x100000
#endif

void* WINAPI VirtualAlloc(void* addr, size_t size, DWORD type, DWORD protect)
{
    if (shim_frozen_now) shim_alloc_while_frozen++;
    if (type != (MEM_COMMIT | MEM_RESERVE)) { fprintf(stderr, "VirtualAlloc type %x\n", type); abort(); }
    size = (size + 4095) & ~(size_t)4095;
    if (protect == PAGE_READWRITE) {               /* data: like Windows, a wanted address is exact or fails */
        if (addr && ((uintptr_t)addr & 0xFFFF)) { fprintf(stderr, "VirtualAlloc address not 64K aligned\n"); abort(); }
        void* p = mmap(addr, size, PROT_READ | PROT_WRITE,
                       MAP_PRIVATE | MAP_ANONYMOUS | (addr ? MAP_FIXED_NOREPLACE : 0), -1, 0);
        if (p == MAP_FAILED) return NULL;
        if (addr && p != addr) { munmap(p, size); return NULL; }
        shim_rw_allocs++;
        return p;
    }
    if (addr) { fprintf(stderr, "VirtualAlloc of code at a fixed address\n"); abort(); }
    void* p = mmap(NULL, size, PROT_READ | PROT_WRITE | PROT_EXEC, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    if (p == MAP_FAILED) return NULL;
    shim_set_page_prot(p, size, protect);
    return p;
}

BOOL WINAPI VirtualProtect(void* addr, size_t size, DWORD protect, DWORD* old)
{
    uintptr_t pg = (uintptr_t)addr & ~(uintptr_t)4095;
    DWORD prev = PAGE_EXECUTE_READ;
    for (int i = 0; i < s_pageCount; ++i) if (s_pages[i].page == pg) prev = s_pages[i].prot;
    if (!old) return 0;                         /* Windows fails without it */
    *old = prev;
    shim_set_page_prot(addr, size, protect);
    return 1;
}

/* Pages that are mapped answer "committed, read/write" (or the protection the
 * loader gave them); unmapped pages and the shim_noaccess range answer "free". */
uintptr_t shim_noaccess_lo, shim_noaccess_hi;
int       shim_queries;
size_t WINAPI VirtualQuery(const void* addr, MEMORY_BASIC_INFORMATION* info, size_t size)
{
    shim_queries++;
    if (size < sizeof(*info)) return 0;
    uintptr_t pg = (uintptr_t)addr & ~(uintptr_t)4095;
    memset(info, 0, sizeof(*info));
    info->BaseAddress = (void*)pg;
    info->RegionSize  = 4096;
    int mapped = pg >= 0x10000 && msync((void*)pg, 4096, MS_ASYNC) == 0;
    if (!mapped || (pg >= shim_noaccess_lo && pg < shim_noaccess_hi)) {
        info->State = 0x10000;                  /* MEM_FREE */
        info->Protect = PAGE_NOACCESS;
        return sizeof(*info);
    }
    info->State   = MEM_COMMIT;
    info->Protect = PAGE_READWRITE;
    for (int i = 0; i < s_pageCount; ++i) if (s_pages[i].page == pg) info->Protect = s_pages[i].prot;
    return sizeof(*info);
}

BOOL WINAPI FlushInstructionCache(HANDLE process, const void* addr, size_t size)
{
    (void)process; (void)addr; (void)size;
    return 1;
}

HANDLE WINAPI GetCurrentProcess(void)   { return (HANDLE)(intptr_t)-1; }
DWORD  WINAPI GetCurrentProcessId(void) { return 4242; }
DWORD  WINAPI GetCurrentThreadId(void)  { return 1; }
void WINAPI EnterCriticalSection(void* section) { (void)section; }
void WINAPI LeaveCriticalSection(void* section) { (void)section; }

/* ---- fake "other threads" ------------------------------------------------------ */
HANDLE WINAPI CreateToolhelp32Snapshot(DWORD flags, DWORD pid)
{
    (void)flags; (void)pid;
    return new_obj(H_SNAP);
}

static BOOL thread_entry(Obj* snap, THREADENTRY32* te)
{
    /* index 0 = the calling thread, 1 = a thread of another process, then fakes */
    int total = 2 + shim_fake_threads;
    if (snap->idx >= total) return 0;
    if (te->dwSize != sizeof(THREADENTRY32)) { fprintf(stderr, "bad THREADENTRY32.dwSize\n"); abort(); }
    te->th32ThreadID       = (DWORD)(snap->idx + 1);
    te->th32OwnerProcessID = snap->idx == 1 ? 7777 : 4242;
    snap->idx++;
    return 1;
}

BOOL WINAPI Thread32First(HANDLE snap, THREADENTRY32* te) { ((Obj*)snap)->idx = 0; return thread_entry(snap, te); }
BOOL WINAPI Thread32Next(HANDLE snap, THREADENTRY32* te)  { return thread_entry(snap, te); }

HANDLE WINAPI OpenThread(DWORD access, BOOL inherit, DWORD tid)
{
    (void)access; (void)inherit;
    if (tid == 1 || tid == 2) { fprintf(stderr, "loader opened its own or a foreign thread\n"); abort(); }
    Obj* o = new_obj(H_THREAD);
    o->idx = (int)tid;
    shim_thread_opens++;
    return o;
}

DWORD WINAPI SuspendThread(HANDLE thread) { (void)thread; shim_suspends++; shim_frozen_now++; return 0; }
DWORD WINAPI ResumeThread(HANDLE thread)  { (void)thread; shim_resumes++;  shim_frozen_now--; return 1; }

BOOL WINAPI GetThreadContext(HANDLE thread, CONTEXT64* ctx)
{
    Obj* o = (Obj*)thread;
    if (ctx->ContextFlags != CONTEXT_CONTROL) { fprintf(stderr, "bad ContextFlags\n"); abort(); }
    ctx->Rip = 0x1000;
    if (o->idx == 3 && shim_busy_times > 0) {            /* first fake thread */
        ctx->Rip = shim_busy_rip;
        shim_busy_times--;
    }
    return 1;
}

void WINAPI Sleep(DWORD ms) { usleep(ms * 1000); }

/* ---- fake UE4SS.dll --------------------------------------------------------------- */
typedef struct { union { wchar_t buf[8]; wchar_t* ptr; } u; size_t size, cap; } FakeWString;

void* WINAPI UE4SS_ModBase_Ctor(void* self)
{
    uint8_t* p = (uint8_t*)self;
    shim_ctor_calls++;
    memset(p, 0, 0xC0);
    *(void**)p = (void*)0xBA5EBA5E;                       /* base-class vtable */
    for (int i = 0; i < 5; ++i) ((FakeWString*)(p + 0x20 + 0x20 * i))->cap = 7;
    return self;
}

void WINAPI UE4SS_ModBase_Dtor(void* self)
{
    uint8_t* p = (uint8_t*)self;
    shim_dtor_calls++;
    for (int i = 0; i < 5; ++i) {
        FakeWString* s = (FakeWString*)(p + 0x20 + 0x20 * i);
        if (s->cap > 7) free(s->u.ptr);                    /* what MSVC's ~wstring does */
        s->size = 0; s->cap = 7; s->u.buf[0] = 0;
    }
}

const wchar_t* shim_read_wstring(void* obj, int index)
{
    FakeWString* s = (FakeWString*)((uint8_t*)obj + 0x20 + 0x20 * index);
    const wchar_t* d = s->cap > 7 ? s->u.ptr : s->u.buf;
    if (d[s->size] != 0) { fprintf(stderr, "wstring not terminated\n"); abort(); }
    return d;
}
