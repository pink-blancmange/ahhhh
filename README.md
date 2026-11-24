<div align="center">

# `ahhhh` // Ahhhh

**A fast, zero-bloat Linux AppImage workbench with a lovely web UI.**

Browse 4,200+ AppImages, roll a die when you're bored, drag-and-drop local `.AppImage` files, and wire them cleanly into your desktop menu—all from one single Go binary.

[![Release](https://img.shields.io/github/v/release/pink-blancmange/ahhhh?style=for-the-badge&color=ff2a85&labelColor=0c0d11)](https://github.com/pink-blancmange/ahhhh/releases)
[![License](https://img.shields.io/github/license/pink-blancmange/ahhhh?style=for-the-badge&color=a3e635&labelColor=0c0d11)](LICENSE)
[![Platform](https://img.shields.io/badge/Linux-amd64%20%7C%20arm64-22d3ee?style=for-the-badge&labelColor=0c0d11)](https://github.com/pink-blancmange/ahhhh/releases)

<br />

<img src="example.png" alt="Ahhhh (ahhhh) web dashboard" width="900" />

</div>

---

## Installation

`Ahhhh` (`ahhhh` on the CLI) is distributed as a portable Linux **AppImage** and installs system-wide into `/opt/appimages/` with a command symlink at `/usr/local/bin/ahhhh` and a desktop entry at `/usr/share/applications/ahhhh.desktop`.

### 1. Download & Self-Install the AppImage (Recommended)
1. Download the latest `ahhhh_*_x86_64.AppImage` (or `aarch64.AppImage`) from the [GitHub Releases](https://github.com/pink-blancmange/ahhhh/releases) page.
2. Make it executable and run `--self-install` to place it into `/opt/appimages/` and link `/usr/local/bin/ahhhh`:
   ```bash
   chmod +x ./ahhhh_*_x86_64.AppImage
   ./ahhhh_*_x86_64.AppImage --self-install
   ```
3. Launch `Ahhhh` from your desktop application menu or by running `ahhhh` in a terminal, then open `http://127.0.0.1:8745` in your browser. Once installed as an AppImage, future `Ahhhh` updates can be installed right inside the web UI just like any other tracked GitHub AppImage.

---

## Supported Systems & Requirements

- **Architectures:** `x86_64` (`amd64`) and `aarch64` (`arm64`).
- **Distros:** Desktop Linux distros with `glibc`, FUSE support, and writable `/opt` and `/usr/local/bin` (Debian, Ubuntu, Linux Mint, MX Linux, Pop!_OS, Fedora, Arch, openSUSE, and `glibc` Void Linux). Read-only/immutable root distros (Silverblue, SteamOS, NixOS) and `musl`-based systems (Alpine or `musl` Void) aren't supported out of the box.
- **Runtime needs:** `pkexec` (Polkit) or `sudo` for system-wide installs/removals, FUSE (`libfuse2` / `libfuse2t64` or `fuse3`) to extract icons and run Type-2 AppImages, and a web browser for the UI.

---

## Why `ahhhh`?

- **Single binary, zero bloat** — Go backend with the web UI embedded inside. Runs on `127.0.0.1:8745`.
- **Desktop integration** — Installs AppImages into `/opt/appimages/`, symlinks into `/usr/local/bin/`, pulls icons, and generates `.desktop` entries using catalog categories when available (falling back to `Utility`).
- **Local `.AppImage` drop** — Drop a local `.AppImage` onto the target box, optionally name it, and extract its icon automatically.
- **Richer app details** — Pulls descriptions, GitHub repo info, and version tags in the inspector modal.
- **Dice roller** — Roll random installable apps from the catalog or active category crate.
- **Self-updating & CLI ready** — Tracks updates for installed GitHub AppImages (including `Ahhhh` itself) and supports CLI installs.

---

## Usage

### GUI

Launch **Ahhhh** from your desktop application menu (or run `ahhhh` in a terminal and open `http://127.0.0.1:8745`). Running `ahhhh` in a terminal keeps the local server running in the foreground (`Ctrl+C` to stop) and requires port `8745` to be free. Use the web UI to browse the catalog, drag-and-drop local `.AppImage` files, install from a GitHub repo/URL, update installed apps, or remove them.

### CLI

```bash
ahhhh                                       # Start the web workbench on 127.0.0.1:8745
ahhhh --self-install                        # Install the AppImage system-wide to /opt/appimages
ahhhh install <owner/repo-or-url> [name]    # Install an app directly from GitHub or a URL
```

---

## Updates

Because `Ahhhh` is installed as a tracked AppImage, the GUI checks for updates to `Ahhhh` and your other installed GitHub AppImages when it first opens and every 30 minutes thereafter. When updates are available, an **`Updates`** button appears in the header so you can update everything right inside the app.

---

## Build from Source

```bash
git clone https://github.com/pink-blancmange/ahhhh.git
cd ahhhh
go build -o ahhhh .
```

*(To package as an AppImage, wrap the compiled `ahhhh` binary in an `AppDir` with `appimagetool`.)*

---

## Uninstall

Remove installed apps (including `Ahhhh` itself) via the **Remove** button in the web UI, or clean up `Ahhhh` manually from the terminal:

```bash
sudo rm -rf /opt/appimages/ahhhh /opt/appimages/ahhhh.AppImage /opt/appimages/ahhhh.meta.json /usr/local/bin/ahhhh /usr/share/applications/ahhhh.desktop /usr/share/icons/hicolor/256x256/apps/ahhhh.png
```

---

<div align="center">

**GPL-3.0-or-later** · [**pink-blancmange**](https://github.com/pink-blancmange)

</div>
