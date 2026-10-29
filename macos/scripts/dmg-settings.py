# dmgbuild settings for Zapretify-macOS-vX.dmg; build-dmg.sh passes -D app=… -D bg=…
# Icon positions match macos/app/dmg-background.swift (arrow from the app to Applications).
import os

app = defines["app"]  # noqa: F821 (dmgbuild provides `defines`)
background = defines["bg"]  # noqa: F821 — bg.png with bg@2x.png next to it for Retina

format = "UDZO"
filesystem = "HFS+"
files = [app]
symlinks = {"Applications": "/Applications"}
icon = os.path.join(app, "Contents", "Resources", "Zapretify.icns")  # volume icon

window_rect = ((200, 140), (660, 420))
default_view = "icon-view"
show_status_bar = False
show_tab_view = False
show_toolbar = False
show_pathbar = False
show_sidebar = False
icon_size = 112
text_size = 13
icon_locations = {
    os.path.basename(app): (180, 205),
    "Applications": (480, 205),
}
