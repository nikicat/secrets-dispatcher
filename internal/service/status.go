package service

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/systemd"
)

// Prints a doctor-style health report: who owns org.freedesktop.secrets,
// which units are installed and whether they run, and whether the
// mask/demotion state is consistent with the topology. Errors when the report
// finds a problem.
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

	var installed []systemd.Unit
	for _, u := range allUnitNames {
		if _, err := os.Stat(u.Path(unitsDir)); err == nil {
			installed = append(installed, u)
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

	states := make(map[systemd.Unit]unitState, len(installed))
	for _, u := range installed {
		states[u] = stateOf(u)
	}
	st := statusState{
		provider: p,
		masked:   masked,
		demoted:  demoted,
		shadowed: shadowed,
		units:    states,
	}
	inst := installationOf(installed)
	inst.report(st)
	problems := inst.problems(st)

	if len(problems) == 0 {
		fmt.Println("✓ no problems found")
		return nil
	}
	for _, pr := range problems {
		fmt.Printf("⚠ %s\n", pr)
	}
	return fmt.Errorf("%d problem(s) found", len(problems))
}

// What the installed unit files set up, as Status judges it.
type installation interface {
	// Prints what is installed.
	report(st statusState)
	// The problems status reports in state st, in the order it reports them.
	problems(st statusState) []string
}

// Returns the installation that the installed unit files set up: a takeover
// when they include the private bus socket. The files cannot tell full from
// local, so a full install is judged as a takeover.
func installationOf(installed []systemd.Unit) installation {
	switch {
	case len(installed) == 0:
		return uninstalled{}
	case slices.Contains(installed, busSocketUnit):
		return takeoverInstall{}
	default:
		return remoteInstall{}
	}
}

// No unit is installed.
type uninstalled struct{}

func (uninstalled) report(statusState) { fmt.Println("Installed: no") }

// Takeover residue without units means an uninstall never ran, or died
// halfway, and the stock provider stays masked or demoted.
func (uninstalled) problems(st statusState) []string {
	var problems []string
	for _, what := range st.residue() {
		problems = append(problems, fmt.Sprintf("leftover %s but no units installed — run `secrets-dispatcher service uninstall` to restore", what))
	}
	return problems
}

// Some units are installed.
type unitsInstalled struct{}

func (unitsInstalled) report(st statusState) {
	fmt.Println("Installed units:")
	for _, u := range allUnitNames {
		if state, ok := st.units[u]; ok {
			fmt.Printf("  %-38s %s\n", u, state)
		}
	}
	fmt.Printf("D-Bus activation masked: %v, gnome-keyring demoted: %v\n", st.masked, st.demoted)
}

// The remote topology is installed. The dispatcher fronts a remote bus and
// must not hold takeover state on this session.
type remoteInstall struct{ unitsInstalled }

func (remoteInstall) problems(st statusState) []string {
	var problems []string
	if len(st.residue()) > 0 {
		problems = append(problems, "remote topology installed but local takeover state present (mask/demotion) — run `service uninstall`, then reinstall")
	}
	if _, ok := st.units[proxyServiceUnit]; !ok {
		problems = append(problems, fmt.Sprintf("%s is missing while other units are installed — reinstall or uninstall", proxyServiceUnit))
	}
	problems = append(problems, st.notActive(remote{}.activated()...)...)
	// With no owner upstream, every secret lookup hangs while the proxy unit
	// still looks active.
	if st.provider.Kind == ProviderNone {
		problems = append(problems, "org.freedesktop.secrets has no owner — no Secret Service provider is running upstream, so secret lookups will hang")
	}
	return problems
}

// A takeover is installed.
type takeoverInstall struct{ unitsInstalled }

func (takeoverInstall) problems(st statusState) []string {
	var problems []string
	if st.provider.Kind != ProviderDispatcher {
		problems = append(problems, fmt.Sprintf("installed for takeover but NOT in front — the owner is %s (re-grab? try `systemctl --user restart %s`)", st.provider, proxyServiceUnit))
	}
	if !st.masked {
		problems = append(problems, "D-Bus activation of org.freedesktop.secrets is not masked — the stock provider can be activated back in front")
	}
	if st.demoted != st.shadowed {
		problems = append(problems, fmt.Sprintf("gnome-keyring demotion is inconsistent (drop-in: %v, autostart shadow: %v) — reinstall or uninstall to converge", st.demoted, st.shadowed))
	}
	// The proxy pulls in the backend through Requires=; the bus service is
	// socket-activated and may sit idle.
	return append(problems, st.notActive(busSocketUnit, backendServiceUnit, proxyServiceUnit)...)
}

// What Status observed on this desktop, for an installation to judge.
type statusState struct {
	// The owner of org.freedesktop.secrets.
	provider Provider
	// Whether the D-Bus activation mask, the gnome-keyring demotion drop-in
	// and the gnome-keyring autostart shadow are in place.
	masked, demoted, shadowed bool
	// The state of each installed unit.
	units map[systemd.Unit]unitState
}

// Returns the takeover residue in place, in the order status reports it.
func (st statusState) residue() []string {
	var residue []string
	for _, r := range []struct {
		present bool
		what    string
	}{
		{st.masked, "D-Bus activation mask"},
		{st.demoted, "gnome-keyring demotion drop-in"},
		{st.shadowed, "gnome-keyring autostart shadow"},
	} {
		if r.present {
			residue = append(residue, r.what)
		}
	}
	return residue
}

// Returns a "<unit> is <state>" problem for each of units that is installed
// but not active, in the order given. A unit that is not installed is skipped.
func (st statusState) notActive(units ...systemd.Unit) []string {
	var problems []string
	for _, u := range units {
		if state, ok := st.units[u]; ok && state != unitActive {
			problems = append(problems, fmt.Sprintf("%s is %s", u, state))
		}
	}
	return problems
}

// The systemctl is-active state of a unit, such as "active" or "failed".
type unitState string

const unitActive unitState = "active"

// Returns what systemctl is-active prints for u, or "unknown" when it prints
// nothing. is-active exits non-zero for anything but active, so the error is
// ignored in favor of the printed state.
func stateOf(u systemd.Unit) unitState {
	out, _ := execOutputFunc("systemctl", "--user", string(verbIsActive), string(u))
	if s := strings.TrimSpace(string(out)); s != "" {
		return unitState(s)
	}
	return "unknown"
}
