/* Minimal hand-written Win32 declarations, so the loader builds with nothing
 * but clang + lld-link (no Windows SDK, no C runtime).
 *
 * The same header is used by the Linux self-test, where every "Win32" call is
 * provided by test/shim.c and marked ms_abi so calling conventions match.     */
#ifndef WIN32_MIN_H
#define WIN32_MIN_H

#include <stdint.h>
#include <stddef.h>
#include <stdarg.h>

#ifdef _WIN32
#  define WINAPI                       /* x64 Windows has one calling convention */
#  define IMPORT __declspec(dllimport)
#else
#  define WINAPI __attribute__((ms_abi))
#  define IMPORT
#endif

typedef void*    HANDLE;
typedef uint32_t DWORD;
typedef int32_t  BOOL;

#define INVALID_HANDLE_VALUE      ((HANDLE)(intptr_t)-1)
#define INVALID_FILE_ATTRIBUTES   0xFFFFFFFFu
#define FILE_ATTRIBUTE_DIRECTORY  0x10u
#define FILE_ATTRIBUTE_NORMAL     0x80u
#define FILE_ATTRIBUTE_REPARSE_POINT 0x400u
#define GENERIC_READ              0x80000000u
#define GENERIC_WRITE             0x40000000u
#define FILE_SHARE_READ           1u
#define FILE_SHARE_WRITE          2u
#define FILE_SHARE_DELETE         4u
#define CREATE_ALWAYS             2u
#define OPEN_ALWAYS               4u
#define FILE_APPEND_DATA          0x0004u
#define OPEN_EXISTING             3u
#define CP_UTF8                   65001u
#define PAGE_READWRITE            0x04u
#define PAGE_EXECUTE_READ         0x20u
#define PAGE_EXECUTE_READWRITE    0x40u
#define MEM_COMMIT                0x1000u
#define MEM_RESERVE               0x2000u
#define PAGE_NOACCESS             0x01u
#define PAGE_GUARD                0x100u
#define TH32CS_SNAPTHREAD         4u
#define THREAD_SUSPEND_RESUME     0x0002u
#define THREAD_GET_CONTEXT        0x0008u
#define THREAD_QUERY_INFORMATION  0x0040u
#define CONTEXT_CONTROL           0x00100001u
#define DLL_PROCESS_ATTACH        1u
#define IMAGE_SCN_MEM_EXECUTE     0x20000000u
#define GET_MODULE_HANDLE_EX_FLAG_PIN                0x1u
#define GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT 0x2u
#define GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS       0x4u

typedef struct {
    DWORD    dwFileAttributes;
    uint32_t ftCreationTime[2];
    uint32_t ftLastAccessTime[2];
    uint32_t ftLastWriteTime[2];
    DWORD    nFileSizeHigh;
    DWORD    nFileSizeLow;
    DWORD    dwReserved0;
    DWORD    dwReserved1;
    wchar_t  cFileName[260];
    wchar_t  cAlternateFileName[14];
} WIN32_FIND_DATAW;                              /* 592 bytes */

typedef struct {
    DWORD   dwSize;
    DWORD   cntUsage;
    DWORD   th32ThreadID;
    DWORD   th32OwnerProcessID;
    int32_t tpBasePri;
    int32_t tpDeltaPri;
    DWORD   dwFlags;
} THREADENTRY32;                                 /* 28 bytes */

/* x64 CONTEXT: 1232 bytes, 16-byte aligned. Only two fields are needed. */
typedef struct {
    uint8_t  pad0[0x30];
    DWORD    ContextFlags;                       /* 0x30 */
    uint8_t  pad1[0xF8 - 0x34];
    uint64_t Rip;                                /* 0xF8 */
    uint8_t  pad2[0x4D0 - 0x100];
} __attribute__((aligned(16))) CONTEXT64;

typedef struct {
    void*    BaseAddress;
    void*    AllocationBase;
    DWORD    AllocationProtect;
    uint16_t PartitionId;
    uint16_t pad0;
    size_t   RegionSize;
    DWORD    State;
    DWORD    Protect;
    DWORD    Type;
    DWORD    pad1;
} MEMORY_BASIC_INFORMATION;                      /* 48 bytes */

_Static_assert(sizeof(MEMORY_BASIC_INFORMATION) == 48, "MEMORY_BASIC_INFORMATION layout");
_Static_assert(sizeof(wchar_t) == 2,              "wchar_t must be UTF-16");
_Static_assert(sizeof(WIN32_FIND_DATAW) == 592,   "WIN32_FIND_DATAW layout");
_Static_assert(offsetof(WIN32_FIND_DATAW, cFileName) == 44, "cFileName offset");
_Static_assert(sizeof(THREADENTRY32) == 28,       "THREADENTRY32 layout");
_Static_assert(sizeof(CONTEXT64) == 0x4D0,        "CONTEXT size");
_Static_assert(offsetof(CONTEXT64, Rip) == 0xF8,  "CONTEXT.Rip offset");

/* ---- KERNEL32.dll ------------------------------------------------------- */
IMPORT HANDLE WINAPI GetModuleHandleW(const wchar_t* name);
IMPORT DWORD  WINAPI GetModuleFileNameW(HANDLE module, wchar_t* buf, DWORD cap);
IMPORT BOOL   WINAPI GetModuleHandleExW(DWORD flags, const void* nameOrAddress, HANDLE* module);
IMPORT BOOL   WINAPI DisableThreadLibraryCalls(HANDLE module);
IMPORT HANDLE WINAPI CreateFileW(const wchar_t* path, DWORD access, DWORD share, void* sec,
                                 DWORD disposition, DWORD flags, HANDLE templ);
IMPORT BOOL   WINAPI ReadFile(HANDLE h, void* buf, DWORD n, DWORD* got, void* ov);
IMPORT BOOL   WINAPI WriteFile(HANDLE h, const void* buf, DWORD n, DWORD* put, void* ov);
IMPORT DWORD  WINAPI GetFileSize(HANDLE h, DWORD* high);
IMPORT BOOL   WINAPI FlushFileBuffers(HANDLE h);
IMPORT BOOL   WINAPI CloseHandle(HANDLE h);
IMPORT DWORD  WINAPI GetFileAttributesW(const wchar_t* path);
IMPORT HANDLE WINAPI FindFirstFileW(const wchar_t* pattern, WIN32_FIND_DATAW* fd);
IMPORT BOOL   WINAPI FindNextFileW(HANDLE h, WIN32_FIND_DATAW* fd);
IMPORT BOOL   WINAPI FindClose(HANDLE h);
IMPORT int    WINAPI MultiByteToWideChar(DWORD cp, DWORD flags, const char* in, int inLen,
                                         wchar_t* out, int outCap);
IMPORT int    WINAPI WideCharToMultiByte(DWORD cp, DWORD flags, const wchar_t* in, int inLen,
                                         char* out, int outCap, const char* def, BOOL* used);
IMPORT void*  WINAPI VirtualAlloc(void* addr, size_t size, DWORD type, DWORD protect);
IMPORT BOOL   WINAPI VirtualProtect(void* addr, size_t size, DWORD protect, DWORD* old);
IMPORT size_t WINAPI VirtualQuery(const void* addr, MEMORY_BASIC_INFORMATION* info, size_t size);
IMPORT BOOL   WINAPI FlushInstructionCache(HANDLE process, const void* addr, size_t size);
IMPORT HANDLE WINAPI GetCurrentProcess(void);
IMPORT DWORD  WINAPI GetCurrentProcessId(void);
IMPORT DWORD  WINAPI GetCurrentThreadId(void);
IMPORT void   WINAPI EnterCriticalSection(void* section);
IMPORT void   WINAPI LeaveCriticalSection(void* section);
IMPORT HANDLE WINAPI CreateToolhelp32Snapshot(DWORD flags, DWORD pid);
IMPORT BOOL   WINAPI Thread32First(HANDLE snap, THREADENTRY32* te);
IMPORT BOOL   WINAPI Thread32Next(HANDLE snap, THREADENTRY32* te);
IMPORT HANDLE WINAPI OpenThread(DWORD access, BOOL inherit, DWORD tid);
IMPORT DWORD  WINAPI SuspendThread(HANDLE thread);
IMPORT DWORD  WINAPI ResumeThread(HANDLE thread);
IMPORT BOOL   WINAPI GetThreadContext(HANDLE thread, CONTEXT64* ctx);
IMPORT void   WINAPI Sleep(DWORD ms);

/* ---- api-ms-win-crt-heap-l1-1-0.dll (the same heap UE4SS.dll uses) ------- */
IMPORT void* malloc(size_t n);
IMPORT void  free(void* p);

/* ---- UE4SS.dll: RC::CppUserModBase constructor / destructor -------------- */
#ifdef _WIN32
#if defined(__GNUC__) && !defined(__clang__)
/* GNU as needs quoted MSVC names. Direct import thunks avoid GCC inserting
 * an unquoted __imp_ prefix before a symbol containing '?' characters. */
void* UE4SS_ModBase_Ctor(void* self) __asm__("\"??0CppUserModBase@RC@@QEAA@XZ\"");
void  UE4SS_ModBase_Dtor(void* self) __asm__("\"??1CppUserModBase@RC@@UEAA@XZ\"");
#else
IMPORT void* UE4SS_ModBase_Ctor(void* self) __asm__("??0CppUserModBase@RC@@QEAA@XZ");
IMPORT void  UE4SS_ModBase_Dtor(void* self) __asm__("??1CppUserModBase@RC@@UEAA@XZ");
#endif
#else
void* WINAPI UE4SS_ModBase_Ctor(void* self);
void  WINAPI UE4SS_ModBase_Dtor(void* self);
#endif

#endif /* WIN32_MIN_H */
