#!/bin/sh
# Builds dlls/main.dll with LLVM only (clang, lld-link, llvm-dlltool).
# No Windows SDK or Visual Studio needed. Works the same on Linux, macOS or
# Windows (run the same three steps from a shell that has LLVM on PATH).
set -e
cd "$(dirname "$0")"
OUT=${OUT:-../build}
mkdir -p "$OUT"

for lib in kernel32 ue4ss ucrtheap; do
    llvm-dlltool -m i386:x86-64 -d $lib.def -l "$OUT/$lib.lib"
done

clang --target=x86_64-pc-windows-msvc -O2 -ffreestanding -fno-builtin -nostdlibinc \
      -fno-stack-protector -funwind-tables -Wall -Wextra -Werror \
      -c loader.c -o "$OUT/loader.obj"

lld-link /dll /nodefaultlib /entry:DllMain /subsystem:windows \
         /out:"$OUT/main.dll" /implib:"$OUT/main_exports.lib" \
         "$OUT/loader.obj" "$OUT/kernel32.lib" "$OUT/ue4ss.lib" "$OUT/ucrtheap.lib"

echo "built $OUT/main.dll"
