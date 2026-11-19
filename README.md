<div align="center">

# `ahhhh` // AppAppApp

**A fast, zero-bloat Linux AppImage workbench with a punk DIY web UI.**

Browse ~1,400+ AppImages, roll a die when you're bored, drag-and-drop local `.AppImage` files, and wire them cleanly into your desktop menu—all from one single Go binary.

[![Release](https://img.shields.io/github/v/release/pink-blancmange/ahhhh?style=for-the-badge&color=ff2a85&labelColor=0c0d11)](https://github.com/pink-blancmange/ahhhh/releases)
[![License](https://img.shields.io/github/license/pink-blancmange/ahhhh?style=for-the-badge&color=a3e635&labelColor=0c0d11)](LICENSE)
[![Platform](https://img.shields.io/badge/Linux-amd64%20%7C%20arm64-22d3ee?style=for-the-badge&labelColor=0c0d11)](https://github.com/pink-blancmange/ahhhh/releases)

<br />

<img src="example.png" alt="AppAppApp (ahhhh) web dashboard" width="900" />

</div>

---

## Why `ahhhh`?

Most Linux software centers feel like enterprise configuration panels. `ahhhh` is an opinionated local workbench built for people who actually use AppImages:

* **One binary, zero runtime bloat** — Written in Go with the entire web UI embedded inside the executable. Launches on `127.0.0.1:8745` and pops straight into your browser.
* **Proper system integration** — Installs AppImages into `/opt/appimages/`, symlinks commands into `/usr/local/bin/`, extracts bundled icons, and writes `.desktop` launchers so apps show up in your menu like native packages.
* **Drag-and-drop local `.AppImage` files** — Drop any standalone `.AppImage` onto the target box, optionally rename it, and let `ahhhh` extract its AppStream metadata, description, and icon automatically.
* **Rich upstream metadata** — Pulls AppStream XML descriptions, `.desktop` comments, GitHub repo topics, and latest release notes right into the inspector modal.
* **Roll the dice** — Stuck on what to try next? Hit the die button to deal 1–6 random installable apps from the catalog (or filtered by category crate).
* **Full CLI included** — Prefer the terminal? Every action works headless too.

---

## Install

### Debian / Ubuntu / Linux Mint (`.deb`)

Grab the latest `amd64` or `arm64` `.deb` from the [**Releases**](https://github.com/pink-blancmange/ahhhh/releases) page:

```bash
sudo dpkg -i ahhhh_*_amd64.deb