package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

// bulkPlan is the snapshot a confirmation shows and a confirmed run registers.
type bulkPlan struct {
	Context           string
	Targets           []kube.ServiceRef
	Namespaces        int
	NotRegistrable    int
	AlreadyRegistered int
	// Excluded lists the services left out before the run, with the reason, for the result report.
	Excluded []kube.ServiceRegistrationFailure
}

// planBulk sorts the services of a context the way the Services pane marks them: a service with a tunnel and
// a service whose hostname another tunnel uses both count as already registered. Targets carry the
// remembered setting of each service.
func planBulk(contextName string, resources kube.ContextResources, settings map[string]store.TunnelSetting, index tunnelIndex) bulkPlan {
	plan := bulkPlan{Context: contextName}
	for _, namespace := range slices.Sorted(maps.Keys(resources.Namespaces)) {
		candidates := 0
		for _, svc := range resources.Namespaces[namespace] {
			row := newServiceRow(contextName, svc, settings, index)
			switch {
			case row.Registered:
				plan.AlreadyRegistered++
				plan.Excluded = append(plan.Excluded, excluded(svc, "already registered as "+tunnelURL(row.Tunnel)))
			case row.NameUser != "":
				plan.AlreadyRegistered++
				plan.Excluded = append(plan.Excluded, excluded(svc, fmt.Sprintf("name http://%s is used by context %s", row.Hostname(), row.NameUser)))
			case row.Reason != "":
				plan.NotRegistrable++
				plan.Excluded = append(plan.Excluded, excluded(svc, row.Reason))
			default:
				plan.Targets = append(plan.Targets, row.Target)
				candidates++
			}
		}
		if candidates > 0 {
			plan.Namespaces++
		}
	}
	return plan
}

func excluded(svc kube.Service, reason string) kube.ServiceRegistrationFailure {
	return kube.ServiceRegistrationFailure{Namespace: svc.Namespace, Service: svc.Name, Reason: reason}
}

func nothingToRegisterText(plan bulkPlan) string {
	return fmt.Sprintf("Nothing to register in %s (%d not registrable, %d already registered)",
		plan.Context, plan.NotRegistrable, plan.AlreadyRegistered)
}

func confirmText(plan bulkPlan) string {
	scope := plural(plan.Namespaces, "namespace")
	var skipped []string
	if plan.NotRegistrable > 0 {
		skipped = append(skipped, fmt.Sprintf("%d not registrable", plan.NotRegistrable))
	}
	if plan.AlreadyRegistered > 0 {
		skipped = append(skipped, fmt.Sprintf("%d already registered", plan.AlreadyRegistered))
	}
	if len(skipped) > 0 {
		scope += " · skipping " + strings.Join(skipped, ", ")
	}
	return fmt.Sprintf("Register %s in context %s?\n%s\nUses the list from the last refresh; each service is checked again.",
		plural(len(plan.Targets), "service"), plan.Context, scope)
}

func (a *App) confirmBulk() {
	row, ok := a.selectedContextRow()
	if !ok {
		a.store.Notify(store.LevelInfo, "No context under the cursor")
		return
	}
	state := a.store.GetState()
	plan := planBulk(row.Name, state.Resources[row.Name], state.TunnelSettings, indexTunnels(a.tunnels))
	if len(plan.Targets) == 0 {
		a.store.Notify(store.LevelWarning, nothingToRegisterText(plan))
		return
	}
	a.showConfirm(plan)
}

func (a *App) startBulk(plan bulkPlan) {
	label := fmt.Sprintf("Registering %s in %s…", plural(len(plan.Targets), "service"), plan.Context)
	if !a.store.TryBeginLoading(label) {
		return
	}
	go a.runBulk(plan)
}

func (a *App) runBulk(plan bulkPlan) {
	defer a.store.SetLoading(false)
	progress := func(p kube.RegistrationProgress) {
		a.store.SetLoadingLabel(progressLabel(plan.Context, p))
	}
	result, err := a.manager.RegisterServices(a.ctx, plan.Context, plan.Targets, progress)
	a.store.TunnelsChanged()
	a.store.Notify(bulkReport(plan, result, err))
}

func progressLabel(contextName string, p kube.RegistrationProgress) string {
	if p.Current == "" {
		return fmt.Sprintf("Finishing registration in %s — %d/%d", contextName, p.Done, p.Total)
	}
	return fmt.Sprintf("Registering %s in %s — %d/%d", p.Current, contextName, p.Done+1, p.Total)
}

// bulkReport builds one event for a bulk run: the summary on the first line, then one line for each service the
// plan excluded, each skipped, not registrable or failed service, and each tunnel whose requested local port was
// busy, so the run counts once in the unread total. A busy port makes a successful run a warning.
func bulkReport(plan bulkPlan, result dns.BulkRegistrationResult, err error) (store.Level, string) {
	level, summary := bulkSummary(plan.Context, len(plan.Targets), result, err)
	if n := len(plan.Excluded); n > 0 {
		summary += fmt.Sprintf(" · %d excluded before start", n)
	}
	busy := busyPortLines(plan.Context, result.Registered)
	if len(busy) > 0 {
		level = max(level, store.LevelWarning)
	}
	details := append(failureLines("excluded", plan.Context, plan.Excluded), bulkDetails(plan.Context, result, busy, err)...)
	if len(details) == 0 {
		return level, summary
	}
	return level, summary + "\n" + strings.Join(details, "\n")
}

func bulkDetails(contextName string, result dns.BulkRegistrationResult, busy []string, err error) []string {
	details := failureLines("skipped", contextName, result.Skipped)
	details = append(details, failureLines("not registrable", contextName, result.Ineligible)...)
	details = append(details, failureLines("failed", contextName, result.Failures)...)
	details = append(details, busy...)
	if err != nil {
		details = append(details, "error: "+err.Error())
	}
	return details
}

func busyPortLines(contextName string, tunnels []dns.DNSTunnel) []string {
	var lines []string
	for _, tunnel := range tunnels {
		if fallback, busy := portFallback(tunnel); busy {
			lines = append(lines, fmt.Sprintf("port %s %s/%s: %s", contextName, tunnel.Namespace, tunnel.Service, fallback))
		}
	}
	return lines
}

func failureLines(label, contextName string, failures []kube.ServiceRegistrationFailure) []string {
	lines := make([]string, 0, len(failures))
	for _, failure := range failures {
		lines = append(lines, fmt.Sprintf("%s %s %s/%s: %s", label, contextName, failure.Namespace, failure.Service, failure.Reason))
	}
	return lines
}

func bulkSummary(contextName string, total int, result dns.BulkRegistrationResult, err error) (store.Level, string) {
	summary := fmt.Sprintf("Registered %d of %d in %s", len(result.Registered), total, contextName)
	var parts []string
	if n := len(result.Skipped); n > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", n))
	}
	if n := len(result.Ineligible); n > 0 {
		parts = append(parts, fmt.Sprintf("%d not registrable", n))
	}
	if n := len(result.Failures); n > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", n))
	}
	if err != nil {
		first, _ := singleLine(err.Error())
		parts = append(parts, "error: "+first)
	}
	if len(parts) == 0 {
		return store.LevelSuccess, summary
	}
	level := store.LevelWarning
	if err != nil {
		level = store.LevelError
	}
	return level, summary + " · " + strings.Join(parts, " · ")
}
