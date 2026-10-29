# Utane

Private music library and player written in Go. It runs as a Windows desktop app (Tauri window around a local Go server) or as a plain local web server.

Everything is stored on disk, with no Docker or database server: the library lives in `%APPDATA%\Utane` (`utane.db` plus a `media` folder). Set `UTANE_DATA` to use another folder.

## Run in a browser

```sh
go run . serve
```

Open `http://127.0.0.1:8080`. `UTANE_ADDR` changes the address; only loopback addresses are accepted because the app has no login.

## Desktop app

Prerequisites: Go, Node, and Rust with the GNU toolchain plus MinGW (no Visual Studio needed). With Scoop: `scoop install mingw rustup-gnu`. The WebView2 runtime ships with current Windows.

If you use the MSVC toolchain instead, rename the sidecar in `package.json` to `utane-x86_64-pc-windows-msvc.exe`; the suffix must match `rustc -vV` host.

```sh
npm install
npx tauri icon web/static/icons/icon-512.svg   # once; generates src-tauri/icons
npm run dev      # builds the Go sidecar, opens the app window
npm run build    # NSIS installer in src-tauri/target/release/bundle/nsis
```

To publish a release, set the same `version` in `src-tauri/tauri.conf.json` and `src-tauri/Cargo.toml`, then push a tag (`git tag v0.1.0 && git push origin v0.1.0`). `.github/workflows/release.yml` builds the installer and attaches it to a draft GitHub Release; review it and click Publish.

The window talks to a Go server started as a sidecar on `127.0.0.1:47821` (a free port if that one is taken). The port is fixed on purpose: browser storage such as playback position is kept per origin.

## Sync between devices

The sync button at the bottom of the sidebar keeps tracks, covers, lyrics and playlists the same on every device, every minute and on "Sync now". Settings are stored in `sync.json` next to the database. Edits merge by newest change per item, so keep device clocks roughly right; deletes propagate too.

- **Shared folder:** point every device at the same synced folder, e.g. one inside Google Drive for desktop. Each device writes its own `<id>.json` and copies audio into `media/`.
- **Same network:** tick "Sync with devices on this network" and enter the same sync code everywhere. Devices find each other by UDP broadcast (port 47823) and exchange data over HTTP on port 47822 (`UTANE_SYNC_ADDR` changes it), protected by the code. Traffic is not encrypted, so use it on a network you trust; Windows Firewall asks once.

## Test

```sh
go test ./...
node --test web/static/*.test.mjs
```
