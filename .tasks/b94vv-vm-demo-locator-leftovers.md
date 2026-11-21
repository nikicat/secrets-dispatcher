---
status: todo
tags: [e2e]
---
# The demo's locator and pointer helpers still have open smells

## Goal
Close the findings that the third and last re-check of unsmell run
20261120-230709 left open in the Tier-2 VM demo (run directory
`.git/unsmell/20261120-230709`, `findings/recheck-3.yaml`).

## Next
- **Label means two things** (r3-1, `e2e/gnome/vm/locator-extension/extension.js:61-64, 194`):
  `_matchByText` also matches `accessible_name` now, but the Locate and
  ClickButton docs, the extension header, `demo.sh:11` and `metadata.json` say
  "visible label". Rename it `_matchByLabel`, define a label once on it (the
  visible text, or an icon-only button's accessible name), and point the docs
  at it.
- **Locate doc is 9 lines** (r3-2, `extension.js:61-69`): it narrates the
  match. Keep the return value and the coordinate space only.
- **gui_press repeats gui_click's locate-and-glide** (r3-3, `e2e/gnome/vm/demo.sh:126-128, 144-145`):
  extract `gui_glide <label> <timeout>`; gui_click and gui_press call it.
  gui_click's `[[ -n $xy ]]` check is dead.
- **Panel accessible names are checked on noble only** (r3-4, `demo.sh:341-342`):
  demos.yml also runs resolute. Verify "System" and "Power Off Menu" there
  (`UBUNTU_SERIES=resolute make demo`). If they hold, drop "GNOME 46" from the
  comment. If not, pass per-series names, or fail the demo instead of warning.
- **Logout comment restates the menu path** (r3-5, `demo.sh:329-337`): keep
  only why the logout is mouse-driven and why TimedLogin brings the session back.
- **Window layout is fixed 1280x800** (r3-6, `qmpinput.py:50`, `demo-driver.sh`,
  `demo-driver-install.sh`): the parked point follows the screen size, but the
  windows it must clear do not. Place them relative to the size passed in.
