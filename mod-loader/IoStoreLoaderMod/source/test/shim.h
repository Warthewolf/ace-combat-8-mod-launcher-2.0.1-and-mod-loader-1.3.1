#ifndef SHIM_H
#define SHIM_H
#include "../src/win32_min.h"

extern wchar_t  shim_module_path[1024];
extern wchar_t  shim_exe_path[1024];
extern wchar_t  shim_other_path[1024];
extern uintptr_t shim_other_lo, shim_other_hi;
extern int      shim_rw_allocs;
extern int      shim_pins;
extern uint8_t* shim_exe_base;
extern int      shim_fake_threads;
extern uint64_t shim_busy_rip;
extern int      shim_busy_times;
extern int      shim_suspends, shim_resumes, shim_thread_opens, shim_thread_closes;
extern int      shim_frozen_now, shim_alloc_while_frozen;
extern int      shim_ctor_calls, shim_dtor_calls;
extern uintptr_t shim_noaccess_lo, shim_noaccess_hi;
extern int      shim_queries;

size_t u16_to_u8(const wchar_t* w, size_t wn, char* out, size_t cap);
size_t u8_to_u16(const char* s, size_t n, wchar_t* out, size_t cap);
size_t wlen16(const wchar_t* w);
void   win_path_to_posix(const wchar_t* w, char* out, size_t cap);
void   shim_set_page_prot(void* addr, size_t size, DWORD prot);
const wchar_t* shim_read_wstring(void* obj, int index);

#endif
