package service

import (
	"context"
	"fmt"
	"slices"

	"github.com/nikicat/secrets-dispatcher/internal/systemd"
)

// Prints a doctor-style report on the owner of org.freedesktop.secrets, the
// installed units and the takeover files. Errors when the report finds a
// problem, such as an unreachable systemd manager. ctx bounds the query.
func Status(ctx context.Context) error {
	unitsDir, err := unitDir()
	if err != nil {
		return err
	}
	files, err := locateTakeoverFiles()
	if err != nil {
		return err
	}

	var installed []systemd.Unit
	for _, u := range allUnitNames {
		if unitsDir.UnitFile(u).Exists() {
			installed = append(installed, u)
		}
	}

	p := DetectProvider(ctx)
	fmt.Printf("org.freedesktop.secrets owner: %s\n", p)

	st := statusState{
		provider:  p,
		mask:      place(files.mask),
		dropIn:    place(files.dropIn),
		shadow:    place(files.shadow),
		installed: installed,
		states:    activeStates(ctx, installed),
	}
	inst := st.installation()
	inst.report()
	problems := inst.problems()

	if len(problems) == 0 {
		fmt.Println("✓ no problems found")
		return nil
	}
	for _, pr := range problems {
		fmt.Printf("⚠ %s\n", pr)
	}
	return fmt.Errorf("%d problem(s) found", len(problems))
}

// The topology that the installed unit files set up, together with the state
// that Status observed.
type installation interface {
	// Prints what is installed.
	report()
	// The problems status reports, in the order it reports them.
	problems() []string
}

// Returns the installation that the installed unit files set up: a takeover
// when they include the private bus socket. The files cannot tell full from
// local, so a full install is judged as a takeover.
func (st statusState) installation() installation {
	switch {
	case len(st.installed) == 0:
		return uninstalled{st}
	case slices.Contains(st.installed, busSocketUnit):
		return takeoverInstall{unitsInstalled{st}}
	default:
		return remoteInstall{unitsInstalled{st}}
	}
}

// No unit is installed.
type uninstalled struct{ statusState }

func (uninstalled) report() { fmt.Println("Installed: no") }

// Takeover residue without units means an uninstall never ran, or died
// halfway, and the stock provider stays masked or demoted.
func (st uninstalled) problems() []string {
	var problems []string
	for _, what := range st.residue() {
		problems = append(problems, fmt.Sprintf("leftover %s but no units installed — run %s to restore", what, UninstallCommand.hint()))
	}
	return problems
}

// Some units are installed.
type unitsInstalled struct{ statusState }

func (st unitsInstalled) report() {
	fmt.Println("Installed units:")
	st.states.report(st.installed)
	fmt.Printf("D-Bus activation masked: %v, gnome-keyring demoted: %v\n", st.mask.placed, st.dropIn.placed)
}

// The remote topology is installed. The dispatcher fronts a remote bus and
// must not hold takeover state on this session.
type remoteInstall struct{ unitsInstalled }

func (st remoteInstall) problems() []string {
	var problems []string
	if len(st.residue()) > 0 {
		problems = append(problems, fmt.Sprintf("remote topology installed but local takeover state present (mask/demotion) — run %s, then reinstall", UninstallCommand.hint()))
	}
	if !slices.Contains(st.installed, proxyServiceUnit) {
		problems = append(problems, fmt.Sprintf("%s is missing while other units are installed — reinstall or uninstall", proxyServiceUnit))
	}
	problems = append(problems, st.states.notActive(remote{}.activated()...)...)
	// With no owner upstream, every secret lookup hangs while the proxy unit
	// still looks active.
	if st.provider.Owner == nil {
		problems = append(problems, "org.freedesktop.secrets has no owner — no Secret Service provider is running upstream, so secret lookups will hang")
	}
	return problems
}

// A takeover is installed.
type takeoverInstall struct{ unitsInstalled }

func (st takeoverInstall) problems() []string {
	var problems []string
	if !st.provider.is(ProviderDispatcher) {
		problems = append(problems, fmt.Sprintf("installed for takeover but NOT in front — the owner is %s (re-grab? try `%s`)", st.provider, opRestart.commandFor(proxyServiceUnit)))
	}
	if !st.mask.placed {
		problems = append(problems, "D-Bus activation of org.freedesktop.secrets is not masked — the stock provider can be activated back in front")
	}
	if st.dropIn.placed != st.shadow.placed {
		problems = append(problems, fmt.Sprintf("gnome-keyring demotion is inconsistent (drop-in: %v, autostart shadow: %v) — reinstall or uninstall to converge", st.dropIn.placed, st.shadow.placed))
	}
	// The proxy pulls in the backend through Requires=; the bus service is
	// socket-activated and may sit idle.
	return append(problems, st.states.notActive(busSocketUnit, backendServiceUnit, proxyServiceUnit)...)
}

// What Status observed on this desktop, for an installation to judge.
type statusState struct {
	// The owner of org.freedesktop.secrets.
	provider Provider
	// The takeover files, each with whether it is in place.
	mask, dropIn, shadow placedFile
	// The installed units, in allUnitNames order.
	installed []systemd.Unit
	// The installed units' states.
	states unitStates
}

// A takeover file, and whether it was in place when Status looked.
type placedFile struct {
	file   takeoverFile
	placed bool
}

func place(f takeoverFile) placedFile { return placedFile{f, f.present()} }

// Returns the takeover residue in place, in the order status reports it.
func (st statusState) residue() []string {
	var residue []string
	for _, f := range []placedFile{st.mask, st.dropIn, st.shadow} {
		if f.placed {
			residue = append(residue, f.file.label())
		}
	}
	return residue
}

// The states of the installed units: either what the user's systemd manager
// reported, or why it could not be reached.
type unitStates interface {
	// Prints each installed unit with its state.
	report(installed []systemd.Unit)
	// Returns a problem for each given unit that is installed and not active,
	// in the given order.
	notActive(units ...systemd.Unit) []string
}

// The state of each installed unit, as the user's systemd manager reported it.
type reportedStates map[systemd.Unit]unitState

func (s reportedStates) report(installed []systemd.Unit) {
	for _, u := range installed {
		printUnitState(u, s[u])
	}
}

// Returns a "<unit> is <state>" problem for each of units that is installed
// but not active. A unit that is not installed is skipped.
func (s reportedStates) notActive(units ...systemd.Unit) []string {
	var problems []string
	for _, u := range units {
		if state, ok := s[u]; ok && state != unitActive {
			problems = append(problems, fmt.Sprintf("%s is %s", u, state))
		}
	}
	return problems
}

// Why the user's systemd manager reported no unit states.
type unreachableManager struct{ err error }

func (m unreachableManager) report(installed []systemd.Unit) {
	for _, u := range installed {
		printUnitState(u, unitUnknown)
	}
}

// Returns the reason the manager was unreachable as the one problem.
func (m unreachableManager) notActive(...systemd.Unit) []string { return []string{m.err.Error()} }

func printUnitState(u systemd.Unit, state unitState) { fmt.Printf("  %-38s %s\n", u, state) }

// Returns the state of each given unit as the user's systemd manager reports
// it. A unit that the manager leaves out is unitUnknown. When the manager
// cannot be reached, the result holds the reason instead.
func activeStates(ctx context.Context, units []systemd.Unit) unitStates {
	states := make(reportedStates, len(units))
	for _, u := range units {
		states[u] = unitUnknown
	}
	if len(units) == 0 {
		return states
	}
	m, err := openUnitManager(ctx)
	if err != nil {
		return unreachableManager{err}
	}
	defer m.Close()
	reported, err := m.ActiveStates(ctx, units)
	if err != nil {
		return unreachableManager{err}
	}
	for _, u := range units {
		if s, ok := reported[u]; ok {
			states[u] = s
		}
	}
	return states
}
