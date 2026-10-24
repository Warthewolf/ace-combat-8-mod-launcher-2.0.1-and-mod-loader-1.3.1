# For mod makers

How to package a mod so it installs with one drop on the launcher and loads with
the Mod Loader.

## Pak mods

Ship one folder per mod with every file the mod needs:

```
YourName_ModName\
  YourName_ModName_P.utoc
  YourName_ModName_P.ucas
  YourName_ModName_P.pak        (if your packer made one)
```

or, for content the game keeps in `.pak` files (text, fonts):

```
YourName_ModName\
  YourName_ModName_P.pak
```

Rules the Mod Loader applies (1.3.1):

- A `.pak` only loads if its name ends in `_P.pak` (any case).
- A `_P.pak` with a same-named `_P.utoc` next to it is mounted as one unit: the
  game mounts the `.utoc` as part of the `.pak`. That `.utoc` then needs its `.ucas`.
- A `.pak` without a `.utoc` doesn't need a `.ucas`.
- A `.utoc` without its `.ucas` is skipped.
- File names must be unique across all mods: two files with the same name can't
  both load (the newest copy wins). Use something like `YourName_ModName_P`.
- Folders named `backup`, `backups`, `old`, `disabled`, `build`, `packed`,
  `export`, `source`, `dlls` or `Scripts` are not loaded: keep old versions and
  working files in them.
- Mods can replace game files, and (since Mod Loader 1.1.0) add new files at new
  asset paths, such as new skins with their own textures and icons.

Players can put the folder in either place (the launcher uses the first):

- `Game\Binaries\Win64\ue4ss\Mods\IoStoreLoaderMod\YourName_ModName\`
- `Game\Content\Paks\~mods\`

## Several versions in one download

Put each version in its own folder ("Blue", "Red"). The launcher asks the player
which one to install. Everything in a mod's folder loads, so don't leave two
versions side by side in one folder.

## Don't ship

- `loadorder.txt`: it would replace the player's own load order.
- `.exe`, `.bat`, `.ps1`, `.cmd` or `.lnk` files: the launcher skips them when
  adding mods.
- Archives inside the archive (`.zip` in `.zip`, `.7z`, `.rar`).

## UE4SS script mods

A folder with `Scripts\main.lua` (Lua) or `dlls\main.dll` (C++), plus an empty
`enabled.txt`:

```
YourScriptMod\
  enabled.txt
  Scripts\main.lua
```

The launcher installs it to `ue4ss\Mods\YourScriptMod\` and lets players switch
it on and off.

## LogicMods (Blueprint mods)

Ship them in a `LogicMods` folder; the launcher puts them in
`Game\Content\Paks\LogicMods\`. They need UE4SS's `BPModLoaderMod`, which isn't
in the Mod Loader download.

## Mods that depend on other mods

Say which mods must load first; players order them in the launcher (lower loads
later and wins). For a mod that has to load after everything else, a name starting
`zz_` sorts last when there is no load order, but the player's `loadorder.txt`
always decides.

## Testing your mod

Start the game Modded, then open **Mod Loader log** in the launcher. Your mod
should show `result code=0` (a `.utoc`) or `Legacy result AL=1 pak-in-table=1`
(a `_P.pak`). The `package store:` line shows how many of its files are new to the
game. See [Troubleshooting](troubleshooting.md#what-the-mod-loaders-log-lines-mean).
