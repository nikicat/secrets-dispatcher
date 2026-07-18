package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Status prints a doctor-style health report (US-11): who owns
// org.freedesktop.secrets, which units are installed and whether they run,
// and whether the mask/demotion state is consistent with the topology. It
// returns an error when problems were found so the CLI exits non-zero.
func Status() error {
	unitsDir, err := unitDir()
	if err != nil {
		return err
	}
	dbusDir, err := dbusServiceDir()
	if err != nil {
		return err
	}
	asDir, err := autostartDir()
	if err != nil {
		return err
	}

	var installed []string
	takeover := false // local/full topology: the dispatcher must own the name
	for _, name := range allUnitNames {
		if _, err := os.Stat(filepath.Join(unitsDir, name)); err == nil {
			installed = append(installed, name)
			if name == "secrets-dispatcher-bus.socket" {
				takeover = true
			}
		}
	}

	maskContent, _ := os.ReadFile(filepath.Join(dbusDir, dbusServiceFile))
	masked := string(maskContent) == dbusActivationMask
	_, dropInErr := os.Stat(filepath.Join(unitsDir, gkDropInDirName, gkDropInName))
	demoted := dropInErr == nil
	shadowContent, _ := os.ReadFile(filepath.Join(asDir, gkAutostartName))
	shadowed := string(shadowContent) == gkAutostartShadow

	p := DetectProvider()
	fmt.Printf("org.freedesktop.secrets owner: %s\n", p)

	var problems []string

	if len(installed) == 0 {
		fmt.Println("Installed: no")
		// Takeover residue without units means an uninstall never ran (or
		// died halfway) — the stock provider stays masked/demoted.
		for _, r := range []struct {
			present bool
			what    string
		}{
			{masked, "D-Bus activation mask"},
			{demoted, "gnome-keyring demotion drop-in"},
			{shadowed, "gnome-keyring autostart shadow"},
		} {
			if r.present {
				problems = append(problems, fmt.Sprintf("leftover %s but no units installed — run `secrets-dispatcher service uninstall` to restore", r.what))
			}
		}
	} else {
		fmt.Println("Installed units:")
		states := make(map[string]string, len(installed))
		for _, name := range installed {
			states[name] = unitState(name)
			fmt.Printf("  %-38s %s\n", name, states[name])
		}
		fmt.Printf("D-Bus activation masked: %v, gnome-keyring demoted: %v\n", masked, demoted)

		if takeover {
			if p.Kind != ProviderDispatcher {
				problems = append(problems, fmt.Sprintf("installed for takeover but NOT in front — the owner is %s (re-grab? try `systemctl --user restart %s`)", p, unitFileName))
			}
			if !masked {
				problems = append(problems, "D-Bus activation of org.freedesktop.secrets is not masked — the stock provider can be activated back in front")
			}
			if demoted != shadowed {
				problems = append(problems, fmt.Sprintf("gnome-keyring demotion is inconsistent (drop-in: %v, autostart shadow: %v) — reinstall or uninstall to converge", demoted, shadowed))
			}
			for _, name := range []string{"secrets-dispatcher-bus.socket", "secrets-dispatcher-backend.service", unitFileName} {
				if state, ok := states[name]; ok && state != "active" {
					problems = append(problems, fmt.Sprintf("%s is %s", name, state))
				}
			}
		} else {
			// Remote topology: the dispatcher fronts a remote bus and must NOT
			// hold takeover state on this session.
			if masked || demoted || shadowed {
				problems = append(problems, "remote topology installed but local takeover state present (mask/demotion) — run `service uninstall`, then reinstall")
			}
			if state, ok := states[unitFileName]; !ok {
				problems = append(problems, unitFileName+" is missing while other units are installed — reinstall or uninstall")
			} else if state != "active" {
				problems = append(problems, fmt.Sprintf("%s is %s", unitFileName, state))
			}
		}
	}

	if len(problems) == 0 {
		fmt.Println("✓ no problems found")
		return nil
	}
	for _, pr := range problems {
		fmt.Printf("⚠ %s\n", pr)
	}
	return fmt.Errorf("%d problem(s) found", len(problems))
}

// unitState reports systemctl is-active for a unit ("active", "inactive",
// "failed", ...). is-active exits non-zero for anything but active, so the
// error is ignored in favor of the printed state.
func unitState(name string) string {
	out, _ := execOutputFunc("systemctl", "--user", "is-active", name)
	if s := strings.TrimSpace(string(out)); s != "" {
		return s
	}
	return "unknown"
}
