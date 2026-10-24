#!/bin/sh
# Builds loader.c natively (Linux x86-64) against the Win32 stand-ins and runs
# the scenarios, once with sanitizers and once optimised like the real DLL.
set -e
cd "$(dirname "$0")"
mkdir -p ../build
FLAGS="-fshort-wchar -Wall -Wextra -Wno-unused-parameter -I../src"
gcc $FLAGS -O1 -g -fsanitize=address,undefined -fno-sanitize-recover=undefined \
      ../src/loader.c shim.c test_main.c -o ../build/selftest_asan
clang $FLAGS -O2 ../src/loader.c shim.c test_main.c -o ../build/selftest_o2
echo "== sanitizer build =="; ../build/selftest_asan "$@"
echo "== optimised build =="; ../build/selftest_o2
