# FECBox Proxy for macOS

A native SwiftUI local-proxy GUI around the same maintained FECBox core. It does not use Network Extension, install a privileged helper, or change system proxy/default routes. The bundle ID is `dev.zenglineng.fecbox.macos.proxy`; it coexists with SFM and the Android client uses its own WireGuard identity.

Import a loopback-only mixed/HTTP/SOCKS JSON profile. The GUI validates it with the bundled core before saving it with private permissions. Start/stop controls only its child process; closing the window stops that process. A saved profile never autostarts. TUN, system-interface Endpoints and `set_system_proxy` profiles are rejected by this edition.

The app supports macOS 13+ on Apple Silicon and Intel. GitHub Actions builds both slices, creates a Universal app and DMG, verifies code signatures, smoke-launches the UI without starting networking, checks `wireguard-fec` in the bundled core schema and verifies the disk image.

This edition is ad-hoc signed and is not Apple-notarized. First launch can require approval under System Settings → Privacy & Security. No global Gatekeeper setting needs to be changed. The official SFM Network Extension edition additionally requires Apple Developer ID signing and provisioning; it is not this proxy edition.

Run **macOS Proxy DMG** in Actions to add the GUI to an existing FECBox Release. The full Release workflow also builds this app from that run's CLI artifacts. The DMG is accompanied by its own `.sha256` and BUILDINFO file. App source remains in this repository; binary core source is the corresponding core Release.
