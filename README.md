<div align="center">

# `ahhhh` // APPAPPAPP

**A fast, zero-bloat Linux AppImage workbench with a lovely web UI.**

Browse 4,200+ AppImages, roll a die when you're bored, drag-and-drop local `.AppImage` files, and wire them cleanly into your desktop menu—all from one single Go binary.

[![Release](https://img.shields.io/github/v/release/pink-blancmange/ahhhh?style=for-the-badge&color=ff2a85&labelColor=0c0d11)](https://github.com/pink-blancmange/ahhhh/releases)
[![License](https://img.shields.io/github/license/pink-blancmange/ahhhh?style=for-the-badge&color=a3e635&labelColor=0c0d11)](LICENSE)
[![Platform](https://img.shields.io/badge/Linux-amd64%20%7C%20arm64-22d3ee?style=for-the-badge&labelColor=0c0d11)](https://github.com/pink-blancmange/ahhhh/releases)

<br />

<img src="example.png" alt="AppAppApp (ahhhh) web dashboard" width="900" />

</div>

---

## Install

### Quick One-Liner

```bash
curl -fsSL https://raw.githubusercontent.com/pink-blancmange/ahhhh/main/install.sh | bash
```

### AppImage (System-Wide Self-Install or Portable)

Grab the `.AppImage` (`x86_64` / `aarch64`) from the [**Releases**](https://github.com/pink-blancmange/ahhhh/releases) tab and make it executable:

```bash
chmod +x ahhhh_*_x86_64.AppImage
```

* **Install `ahhhh` system-wide (recommended):** Installs to `/opt/appimages/ahhhh/`, links `/usr/local/bin/ahhhh`, adds your desktop menu icon, and enables built-in self-updates:
  ```bash
  ./ahhhh_*_x86_64.AppImage --self-install
  ```
* **Run portable without installing:**
  ```bash
  ./ahhhh_*_x86_64.AppImage
  ```

### `.deb` Package

```bash
sudo dpkg -i ahhhh_*_amd64.deb
```

---

## Supported Systems & Requirements

* **Architectures:** `x86_64` (`amd64`) and `aarch64` (`arm64`).
* **Distros:** Standard `glibc` desktop Linux distros with writable `/opt` and `/usr/local/bin` (Debian, Ubuntu, Linux Mint, MX Linux, Pop!_OS, Fedora, Arch, openSUSE). Read-only/immutable root distros (Silverblue, SteamOS, NixOS) and `musl` distros (Alpine) aren't supported out of the box.
* **Runtime needs:** `pkexec` (Polkit) for GUI system-wide installs/removals, FUSE (`libfuse2` / `libfuse2t64` or `fuse3`) to extract metadata and run Type-2 AppImages, and a browser (`xdg-open`) for the web UI (CLI works headless).

---

## Why `ahhhh`?

* **Single binary, zero bloat** — Go backend with the web UI embedded inside. Runs on `127.0.0.1:8745` and opens your browser.
* **Desktop integration** — Installs AppImages into `/opt/appimages/`, symlinks into `/usr/local/bin/`, pulls icons, and adds `.desktop` launchers.
* **Local `.AppImage` drop** — Drop a local `.AppImage` onto the target box, optionally name it, and extract its metadata/icon automatically.
* **Richer app details** — Pulls AppStream XML descriptions, `.desktop` comments, GitHub repo topics, and latest release notes in the inspector modal.
* **Dice roller** — Roll 1–6 random installable apps from the catalog or active category crate.
* **Self-updating & CLI ready** — Tracks updates for installed GitHub AppImages (including `ahhhh` itself when installed via `--self-install`) and supports headless CLI commands.

---

## Usage

### GUI

Launch **ahhhh** from your desktop application menu (or run `ahhhh` in a terminal to open `http://127.0.0.1:8745`).

### CLI

```bash
ahhhh                             # Launch the web workbench
ahhhh list                        # List installed system-wide AppImages
ahhhh install <app-name>          # Install an app from the catalog
ahhhh install ./MyTool.AppImage   # Install a local .AppImage
ahhhh update                      # Update GitHub-tracked AppImages
ahhhh remove <app-name>           # Remove binary, icon, symlink & launcher
```

---

## Updates

The GUI checks for updates to your installed AppImages when it first opens and every 30 minutes thereafter. When updates are available, an **`# Updates`** button appears in the header so you can install individual updates or update everything at once.

---

## Build from Source

```bash
git clone https://github.com/pink-blancmange/ahhhh.git
cd ahhhh
go build -o ahhhh .
./ahhhh --self-install   # Optional: install system-wide with menu icon & self-updates
```

---

## Uninstall

* **Self-installed AppImage (`--self-install`):**
  ```bash
  ahhhh remove ahhhh
  ```
* **`.deb` package:**
  ```bash
  sudo apt remove ahhhh
  ```
* **One-liner (`install.sh`):**
  ```bash
  sudo rm -f /usr/local/bin/ahhhh && rm -f ~/.local/share/applications/ahhhh.desktop ~/.local/share/icons/hicolor/256x256/apps/ahhhh.png && gtk-update-icon-cache -f -t ~/.local/share/icons/hicolor >/dev/null 2>&1 || true
  ```

---

<div align="center">

**GPL-3.0-or-later** · [**pink-blancmange**](https://github.com/pink-blancmange)

</div>