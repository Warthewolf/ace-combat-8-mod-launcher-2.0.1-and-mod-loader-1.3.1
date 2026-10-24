# How the Mod Loader works

AC8 Mod Loader (`IoStoreLoaderMod`) is a UE4SS C++ mod: `dlls\main.dll`, started by
UE4SS because of the empty `enabled.txt` next to it. It is written in plain C with
no C runtime and no Windows SDK: `mod-loader/IoStoreLoaderMod/source/src/loader.c`.

## What happens at each game start

1. **Find the game code.** It scans the executable sections of `AceCombat8.exe` in
   memory for five byte patterns (below). Each must be found **exactly once**,
   otherwise it logs `patterns not found exactly once` and changes nothing.
2. **Check for another loader.** If the mount function is already hooked, it logs
   which file did it and stops.
3. **Install hooks:**
   - on the IoStore container **mount** function, so it sees every container the
     game mounts;
   - on the **Legacy Pak constructor**, so `*_P.pak` files (and only those) are
     opened with signature checking off.
4. **Wait for the game.** When the game mounts `pakchunk0-Windows.utoc` (or the
   next container, if UE4SS started late), the loader builds its plan:
   - find mods in `IoStoreLoaderMod\` (any depth) and `Content\Paks\~mods\`,
     skipping `backup(s)`, `old`, `disabled`, `build`, `packed`, `export`,
     `source`, `dlls`, `Scripts`;
   - drop duplicates by file name (newest wins) and `.utoc` files without `.ucas`;
   - apply `loadorder.txt` (`+Name` on, `-Name` off; unlisted mods after, A to Z,
     loader folder before `~mods`).
5. **Mount the mods**, in order, with mount orders from 1000 up, so later mods win:
   - `.utoc` mods through the game's own mount function, with the "needs a
     signature" check branch patched for that call only;
   - `*_P.pak` mods through the game's Legacy Pak mount (`FPakPlatformFile::Mount`),
     using the game's real platform-file object captured from a native mount. A
     `_P.pak` with a same-named `_P.utoc` is one unit: the engine mounts the `.utoc`
     as part of it.
6. **Register with the package store.** Each mod's container header is handed to
   the game's package store the same way the game does it, so files that are **new**
   to the game load; replaced files get their import lists updated.
7. **Log** everything to `AC8IoStoreLoader.log`, ending with
   `done: X of Y attempted mount requests returned success`.

## The byte patterns

Defined at the top of `loader.c` (and `kStoreReg` further down). Addresses are
from game version 1.1.2.0; the log prints the address each one was found at.

| Pattern | What it finds | Length | Found at (1.1.2.0) |
|---|---|---|---|
| `kMountSig` | Start of the IoStore container mount function (hooked) | 29 bytes, exact | `0x1427E4AA0` |
| `kPakCtorSig` | Start of the Legacy Pak (`FPakFile`) constructor (hooked) | 67 bytes, 8 wildcards (RIP displacements) | `0x1427F6C80` |
| `kPakMountSig` | Start of `FPakPlatformFile::Mount` (called, not hooked) | 69 bytes, RIP displacement (+31..34) wild | `0x142800260` |
| `kSigCheck` | The "does this container need a signature?" check; byte `+0x1B` (`74`, `je`) is patched to `EB` during mod mounts | 35 bytes, call target wild | branch at `0x141796B23` |
| `kStoreReg` | The inlined package-store registration code (`FFilePackageStoreBackend::Mount`); the loader reads the game's own lock, grow and sort functions from it | 156 bytes, mask in `kStoreRegWild` | `0x142800BAC` |

Things that depend on the exact layout, besides the patterns: `HOOK_COPY_LEN` (19),
`PAK_CTOR_COPY_LEN` (15), `PAK_IOSTORE_RETURN` (`0x85C`), `SIG_BRANCH_OFFSET`
(`0x1B`), and the object offsets used for the package store and the platform file
(`+0xD0`, `+0xE0`, `+0x118`), documented in comments in `loader.c`.

## Updating the patterns after a game update

When a patch changes `AceCombat8.exe`, the functions usually stay the same code at
new addresses, so the patterns often still match. When one doesn't:

1. **Keep the old exe** (1.1.2.0) and get the new one.
2. **Find each function in the old exe.** The table above gives its address;
   subtract the image base `0x140000000` to get the RVA, and map that to a file
   offset with the PE section table. Take the whole function, not just the pattern.
3. **Find the same function in the new exe:** search for the old function's bytes
   with RIP-relative displacements, call targets and jump distances treated as
   wildcards. Stack frame sizes (`sub rsp, 0x410`) and struct offsets are the parts
   most likely to change; check them by hand. A disassembler (Ghidra, IDA,
   x64dbg) makes this much quicker: compare the decompiled code of old and new.
4. **Write the new pattern** so it is found exactly once in the new exe, keeping
   wildcards for anything address-dependent. If the instructions at the start of a
   hooked function changed, re-check `HOOK_COPY_LEN` / `PAK_CTOR_COPY_LEN`: the
   copied bytes must be whole instructions with nothing RIP-relative in them.
5. **Re-check the offsets** listed above against the new code.
6. Bump `LOADER_VERSION`, update the README's game version and the CHANGELOG, build,
   run the tests, and test in the game: the log should show each pattern with
   `matches=1` and your mods loading.

## Building

From `mod-loader/IoStoreLoaderMod/source/src`:

| Script | Needs |
|---|---|
| `build-LLVM.sh` | LLVM only: `clang`, `lld-link`, `llvm-dlltool` (Linux, macOS or Windows) |
| `build-MSVC.ps1` | x64 MinGW GCC plus Visual Studio's `lib.exe`/`link.exe`; pass `-VisualStudio <path>` if Visual Studio isn't at the script's default path |

Both write `source\build\main.dll`. Copy it to `IoStoreLoaderMod\dlls\main.dll` to
use it. Never install the test `UE4SS.dll` stub the Windows tests build.

## Testing

- `source/test/run_tests.sh` (Linux): builds `loader.c` natively against Win32
  stand-ins (`shim.c`) and runs the scenarios twice, with sanitizers and optimised.
  Some expectations (version string, summary wording) date from before 1.3.1.
- `source/test/run_pak_windows.ps1` (Windows): builds the real DLL with a stub
  UE4SS and checks `.pak` scenarios.

The launcher's own tests can compare its mod discovery with the loader's: see
[How the launcher works](launcher-internals.md#tests).
