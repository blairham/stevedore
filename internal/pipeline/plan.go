package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blairham/stevedore/internal/changed"
	"github.com/blairham/stevedore/internal/fingerprint"
)

// imageEval is one image's change-detection outcome from the pre-pass.
type imageEval struct {
	plan    ImagePlan
	fp      string
	changed bool
	reason  string
}

// evaluateImages runs change detection and fingerprinting for every plan.
// Under --only, selection was the planner's decision — the selected images
// build unconditionally.
func evaluateImages(o Options, p *Prepared, state fingerprint.State) ([]imageEval, error) {
	cd := p.Config.ChangeDetection
	shared := cd.SharedPaths

	// --changed-since: resolve the git diff once, up front.
	var changedFiles []string
	if o.ChangedSince != "" {
		var err error
		changedFiles, err = changed.FilesSince(o.Dir, o.ChangedSince)
		if err != nil {
			return nil, err
		}
	}

	// Marker mode: with no explicit --changed-since or --only, use each
	// image's own release-marker ref as its change-detection base. Fetch the
	// latest markers first (important on a fresh CI checkout).
	markerMode := cd.MarkerRefs && o.ChangedSince == "" && len(o.Only) == 0
	if markerMode {
		changed.FetchMarkers(o.Dir, cd.MarkerPrefix)
	}

	var evals []imageEval
	for _, plan := range p.Plans {
		ch, reason := true, ""
		switch {
		case len(o.Only) > 0:
			reason = "selected via --only"
		case o.ChangedSince != "":
			d := changed.Evaluate(plan.Paths, shared, changedFiles)
			ch, reason = d.Changed, fmt.Sprintf("%s since %s", d.Reason, o.ChangedSince)
		case markerMode:
			ref := changed.MarkerRef(cd.MarkerPrefix, plan.Image.ID)
			if changed.RefExists(o.Dir, ref) {
				files, err := changed.FilesSince(o.Dir, ref)
				if err != nil {
					return nil, err
				}
				d := changed.Evaluate(plan.Paths, shared, files)
				ch, reason = d.Changed, fmt.Sprintf("%s since its release marker", d.Reason)
			} else {
				reason = "no release marker yet (never released)"
			}
		}
		fpScoped := scopedFingerprintPaths(plan.Paths, shared)
		fp, err := fingerprint.Compute(o.Dir, plan.Image, p.Config.Dist, fpScoped)
		if err != nil {
			return nil, fmt.Errorf("fingerprint %s: %w", plan.Image.ID, err)
		}
		if ch && o.OnlyChanged && state[plan.Image.ID] == fp {
			ch, reason = false, "inputs unchanged"
		}
		evals = append(evals, imageEval{plan: plan, fp: fp, changed: ch, reason: reason})
	}
	return evals, nil
}

// groupPlans groups evaluated images by identical build spec — a group builds
// once and pushes to every member's repositories/tags, so it builds if ANY
// member changed. Groups come back in config order; unchanged groups' members
// are returned as skipped.
func groupPlans(dir string, evals []imageEval) (toBuild [][]imageEval, skipped []imageEval) {
	var order []string
	groups := map[string][]imageEval{}
	for _, e := range evals {
		k := buildKey(dir, e.plan)
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], e)
	}
	for _, k := range order {
		members := groups[k]
		anyChanged := false
		for _, m := range members {
			anyChanged = anyChanged || m.changed
		}
		if !anyChanged {
			skipped = append(skipped, members...)
			continue
		}
		toBuild = append(toBuild, members)
	}
	return toBuild, skipped
}

func evalIDs(evals []imageEval) []string {
	ids := make([]string, len(evals))
	for i, e := range evals {
		ids[i] = e.plan.Image.ID
	}
	return ids
}

// PlanEntry is one build group in the emitted plan — one matrix job. Only and
// Pins are ready to splice into a `stevedore release` invocation.
type PlanEntry struct {
	// Group is a display name: the first member's image ID.
	Group string `json:"group"`
	// IDs are every member image built (and pushed) by this entry.
	IDs []string `json:"ids"`
	// Only is the comma-joined IDs, for `release --only <only>`.
	Only string `json:"only"`
	// Versions maps each member ID to the version the plan resolved.
	Versions map[string]string `json:"versions"`
	// Pins is ready-made `--pin-version id=ver` flags so the matrix job tags
	// exactly what the plan resolved.
	Pins string `json:"pins"`
	// Reason is why this group builds (first changed member's reason).
	Reason string `json:"reason"`
	// Platform is set under --split-platforms: this entry builds one platform
	// of its group, via `release --only <only> <pins> --split <platform>`.
	Platform string `json:"platform,omitempty"`
	// Runner is the suggested GitHub-hosted runner label for Platform
	// (`runs-on: ${{ matrix.runner }}`); empty when there is no native
	// GitHub-hosted runner for the platform.
	Runner string `json:"runner,omitempty"`
}

// defaultRunner maps a build platform to the GitHub-hosted runner that executes
// it natively. Platforms without a hosted native runner return "" — the
// workflow picks (or emulates) one itself.
func defaultRunner(platform string) string {
	switch platform {
	case "linux/amd64":
		return "ubuntu-24.04"
	case "linux/arm64":
		return "ubuntu-24.04-arm"
	}
	return ""
}

// PlanSkip is one image the plan decided not to build.
type PlanSkip struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// PlanResult is the resolved build plan. Include is in GitHub Actions matrix
// `include` shape: `strategy: matrix: ${{ fromJson(plan) }}` fans one job out
// per build group. Skipped is informational.
type PlanResult struct {
	Include []PlanEntry `json:"include"`
	Skipped []PlanSkip  `json:"skipped"`
}

// Plan resolves versions, runs change detection, and groups identical build
// specs — everything a release run decides before building — and returns it
// without building anything. Matrix mode: CI runs `plan --output json`, fans a
// job out per Include entry, and each job runs `release --only <entry.only>
// <entry.pins>`.
func Plan(o Options) (*PlanResult, error) {
	// Progress goes to stderr so stdout carries only the plan document.
	progress = os.Stderr
	defer func() { progress = os.Stdout }()

	p, err := Prepare(o)
	if err != nil {
		return nil, err
	}
	// Fingerprint state feeds --only-changed, same as a release run.
	state, err := fingerprint.Load(filepath.Join(o.Dir, p.Config.Dist, "fingerprints.json"))
	if err != nil {
		return nil, err
	}
	evals, err := evaluateImages(o, p, state)
	if err != nil {
		return nil, err
	}
	toBuild, skipped := groupPlans(o.Dir, evals)
	return newPlanResult(toBuild, skipped, o.SplitPerPlatform), nil
}

// newPlanResult renders build groups and skips into the emitted plan document.
// Under splitPerPlatform each group multiplies into one entry per platform
// (with a native-runner hint) so a workflow can fan a leg out per arch; groups
// with no configured platforms keep a single, unsplit entry.
func newPlanResult(toBuild [][]imageEval, skipped []imageEval, splitPerPlatform bool) *PlanResult {
	result := &PlanResult{Include: []PlanEntry{}, Skipped: []PlanSkip{}}
	for _, grp := range toBuild {
		entry := PlanEntry{
			Group:    grp[0].plan.Image.ID,
			IDs:      evalIDs(grp),
			Versions: map[string]string{},
		}
		entry.Only = strings.Join(entry.IDs, ",")
		var pins []string
		for _, m := range grp {
			entry.Versions[m.plan.Image.ID] = m.plan.Version
			pins = append(pins, fmt.Sprintf("--pin-version %s=%s", m.plan.Image.ID, m.plan.Version))
			if entry.Reason == "" && m.changed {
				entry.Reason = m.reason
			}
		}
		entry.Pins = strings.Join(pins, " ")
		if platforms := grp[0].plan.Image.Platforms; splitPerPlatform && len(platforms) > 0 {
			for _, platform := range platforms {
				e := entry
				e.Platform = platform
				e.Runner = defaultRunner(platform)
				result.Include = append(result.Include, e)
			}
			continue
		}
		result.Include = append(result.Include, entry)
	}
	for _, m := range skipped {
		result.Skipped = append(result.Skipped, PlanSkip{ID: m.plan.Image.ID, Reason: m.reason})
	}
	sort.Slice(result.Skipped, func(i, j int) bool { return result.Skipped[i].ID < result.Skipped[j].ID })
	return result
}
