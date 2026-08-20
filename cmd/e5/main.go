// Command e5 runs the §11.3 schema-growth experiment (hypothesis E5).
//
// On a fixed-schema corpus, a field is held out: the combination behaves as though
// the field did not exist (its verdicts are dropped before equation (18), reducing J,
// exactly the §10.2 mask mechanism). At the introduction boundary the field "arrives"
// as schema growth per equation (20): a mask no calibration era contained. Treatments
// (§11.2):
//
//	A — abstain until calibrated: the field's verdicts stay dropped until n₀ of them
//	    have accrued after introduction, then join.
//	B — detector-marginal composition: the field's verdicts join immediately; validity
//	    rests on (18)'s J-dependent reference, the framework's default.
//	C — mask embedding and borrowing: NOT run. A faithful implementation requires a
//	    mask-similarity model over registry statistics; the paper's own analysis
//	    (§11.2) records that a shallow version degrades to A without saying so, which
//	    is worse than reporting the treatment as unimplemented.
//
// One replay pass evaluates every held-out field and treatment simultaneously: the
// detectors score each event once, and per-(field, treatment) accumulators recompute
// the combination with the appropriate verdicts dropped. Realised FDR at nominal q is
// measured per §12.3 with the same per-day BH construction as cmd/analyse, on the
// pre-introduction and post-introduction eras separately. The §11.3 prediction: if
// treatment B's mask-invariance assumption is load-bearing, realised FDR degrades
// monotonically with the introduced field's correlation to the retained set, measured
// here as the maximum pairwise mutual information (a documented simplification of
// "the mutual information between the held-out field and the retained ones").
//
// The co-occurrence verdict is dropped in the held-out eras when its minimising pair
// touches the held-out field, an approximation of the reduced-schema graph that is
// recorded in the output rather than concealed.
package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/JohnPierman/ethogram/application"
	"github.com/JohnPierman/ethogram/domain/calibration"
	"github.com/JohnPierman/ethogram/domain/cooccurrence"
	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/registry"
	"github.com/JohnPierman/ethogram/domain/timing"
	"github.com/JohnPierman/ethogram/domain/volume"
	"github.com/JohnPierman/ethogram/infrastructure/corpus"
	"github.com/JohnPierman/ethogram/infrastructure/state/memory"
	"github.com/JohnPierman/ethogram/internal/provenance"
)

func main() {
	var (
		authPath     = flag.String("auth", "data/lanl/auth.txt.gz", "path to auth.txt.gz")
		redteamPath  = flag.String("redteam", "data/lanl/redteam.txt.gz", "path to redteam.txt.gz")
		outPath      = flag.String("out", "", "result JSON path (required)")
		runID        = flag.String("run-id", "", "run identifier (required)")
		burnInSec    = flag.Int64("burnin", 604800, "burn-in end (frozen split)")
		introduceSec = flag.Int64("introduce", 1209600, "introduction boundary: the held-out field arrives at this corpus second (day 14)")
		endSec       = flag.Int64("end", 1814400, "coverage end (day 21)")
		n0           = flag.Int64("n0", 1000, "treatment A: verdicts required before the new field joins")
	)
	flag.Parse()
	if *outPath == "" || *runID == "" {
		log.Fatal("-out and -run-id are required")
	}
	if err := run(*authPath, *redteamPath, *outPath, *runID, *burnInSec, *introduceSec, *endSec, *n0); err != nil {
		log.Fatal(err)
	}
}

// heldOutFields are the fields introduced as synthetic schema growth, chosen to span
// correlation strengths with the retained set; the run measures the MI rather than
// assuming it.
var heldOutFields = []event.FieldPath{
	"auth.logon_type",
	"auth.authentication_orientation",
	"auth.destination_computer",
}

type armKey struct {
	field     event.FieldPath
	treatment string // "A" or "B"
}

type dayStats struct {
	alerts []alertRec
}

type alertRec struct {
	p     float64
	t     int64
	isRed bool
}

type arm struct {
	// verdictsSeen counts the held-out field's verdicts since introduction, for A's
	// n₀ gate.
	verdictsSeen int64
	perDay       map[int64]*dayStats
}

// topK is the number of alerts retained per day per arm, and it bounds the number of
// discoveries this command can observe.
//
// That bound is not a detail. The BH cut is the largest rank i whose p-value satisfies
// p₍ᵢ₎ ≤ (i/m)q, and this command can only search for it among the alerts it kept. If
// the cut lands on the last retained alert then the true cut is at or beyond the
// retention boundary and its position is unknown: the discovery count is
// right-censored, and every quantity derived from it — the realised FDR above all — is
// not a measurement but a statement about how many alerts were stored.
//
// The first run of this experiment reported exactly that failure without saying so. It
// retained 200 alerts a day and reported 800 discoveries before the schema grew and 600
// after, identically at every q from 0.001 to 0.1 and identically for both treatments,
// because 800 and 600 are four and three days of the cap rather than counts of
// anything. The value is raised here so the cut has room, and censoring is now detected
// and reported rather than left to be read off a suspiciously round number.
const topK = 20000

// alertLess is the total order on retained alerts: ascending p, ties broken by
// timestamp so the retained set does not depend on arrival order.
func alertLess(x, y alertRec) bool {
	if x.p != y.p {
		return x.p < y.p
	}
	return x.t < y.t
}

// push retains the day's topK smallest p-values as a max-heap, so the largest is at the
// root and is the one evicted when a better alert arrives.
//
// The heap replaces a sorted-slice insertion, which moved O(topK) elements per accepted
// alert. That was affordable while topK was 200 and is not at the value needed to keep
// the BH cut away from the retention boundary; the same structure is used in the replay
// engine for the same reason. Ordering is restored by one sort at the end, in
// sortedAlerts, since nothing reads the alerts until then.
func (a *arm) push(day int64, rec alertRec) {
	ds, ok := a.perDay[day]
	if !ok {
		ds = &dayStats{}
		a.perDay[day] = ds
	}

	if len(ds.alerts) < topK {
		ds.alerts = append(ds.alerts, rec)
		// Sift up against the max-heap order.
		for i := len(ds.alerts) - 1; i > 0; {
			parent := (i - 1) / 2
			if alertLess(ds.alerts[parent], ds.alerts[i]) {
				ds.alerts[parent], ds.alerts[i] = ds.alerts[i], ds.alerts[parent]
				i = parent
				continue
			}
			break
		}
		return
	}

	// Full: keep the alert only if it beats the current worst, which sits at the root.
	if !alertLess(rec, ds.alerts[0]) {
		return
	}
	ds.alerts[0] = rec
	for i := 0; ; {
		l, r := 2*i+1, 2*i+2
		largest := i
		if l < len(ds.alerts) && alertLess(ds.alerts[largest], ds.alerts[l]) {
			largest = l
		}
		if r < len(ds.alerts) && alertLess(ds.alerts[largest], ds.alerts[r]) {
			largest = r
		}
		if largest == i {
			return
		}
		ds.alerts[i], ds.alerts[largest] = ds.alerts[largest], ds.alerts[i]
		i = largest
	}
}

// sortedAlerts returns the day's retained alerts in ascending p, which is the order the
// BH scan requires. The heap is unordered beyond its root, so this sorts once.
func (ds *dayStats) sortedAlerts() []alertRec {
	sort.Slice(ds.alerts, func(i, j int) bool { return alertLess(ds.alerts[i], ds.alerts[j]) })
	return ds.alerts
}

// miCounter accumulates joint counts between the held-out field's value and each
// retained categorical field's value, over a deterministic 1-in-16 event sample, for
// the pairwise mutual information estimate.
type miCounter struct {
	joint map[event.FieldPath]map[[2]string]int64
	total map[event.FieldPath]int64
}

func newMICounter() *miCounter {
	return &miCounter{
		joint: map[event.FieldPath]map[[2]string]int64{},
		total: map[event.FieldPath]int64{},
	}
}

func (m *miCounter) observe(held string, retainedField event.FieldPath, retained string) {
	j, ok := m.joint[retainedField]
	if !ok {
		j = map[[2]string]int64{}
		m.joint[retainedField] = j
	}
	j[[2]string{held, retained}]++
	m.total[retainedField]++
}

// maxPairwiseMI returns the maximum, over retained fields, of the empirical mutual
// information in nats.
func (m *miCounter) maxPairwiseMI() (float64, string) {
	best, bestField := 0.0, ""
	for field, joint := range m.joint {
		n := float64(m.total[field])
		if n == 0 {
			continue
		}
		margH := map[string]float64{}
		margR := map[string]float64{}
		for k, c := range joint {
			margH[k[0]] += float64(c)
			margR[k[1]] += float64(c)
		}
		mi := 0.0
		for k, c := range joint {
			pxy := float64(c) / n
			px := margH[k[0]] / n
			py := margR[k[1]] / n
			if pxy > 0 && px > 0 && py > 0 {
				mi += pxy * math.Log(pxy/(px*py))
			}
		}
		if mi > best {
			best, bestField = mi, string(field)
		}
	}
	return best, bestField
}

func run(authPath, redteamPath, outPath, runID string, burnInSec, introduceSec, endSec, n0 int64) error {
	started := time.Now().UTC()

	labels, err := loadRedTeam(redteamPath)
	if err != nil {
		return err
	}

	f, err := os.Open(authPath) //nolint:gosec // the corpus the flag names
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(bufio.NewReaderSize(f, 4<<20))
	if err != nil {
		return err
	}

	const halfLife = novelty.HalfLife(7 * event.Day)
	schema := corpus.Schema{
		Source: "lanl.auth", Delimiter: ',', TimeColumn: 0, TimeUnit: event.Second,
		EntityColumn: 1,
		Columns: []event.FieldPath{
			"", "auth.source_user", "auth.destination_user", "auth.source_computer",
			"auth.destination_computer", "auth.authentication_type", "auth.logon_type",
			"auth.authentication_orientation", "auth.success_failure",
		},
		MissingToken: "?",
		EntityFilter: func(entity string) bool {
			return len(entity) > 1 && entity[0] == 'U' && entity[1] >= '0' && entity[1] <= '9'
		},
	}
	reader := corpus.NewReader(zr, schema)

	fieldRegistry := registry.New(registry.DefaultPolicy())
	timStore := memory.NewTimingStore()
	detectors := detector.NewRegistry()
	for _, d := range []detector.Detector{
		novelty.NewDetector(memory.NewNoveltyStore(halfLife), fieldRegistry, 1.0, halfLife),
		timing.NewDetector(timStore, 1.5, halfLife, timing.DefaultStandardise),
		volume.NewDetector(memory.NewVolumeStore(), timStore, 1.5, halfLife, volume.DefaultMinPeriods),
		cooccurrence.NewDetector(cooccurrence.NewMemoryGraph(halfLife), fieldRegistry, nil, halfLife),
	} {
		if regErr := detectors.Register(d); regErr != nil {
			return regErr
		}
	}

	arms := map[armKey]*arm{}
	mi := map[event.FieldPath]*miCounter{}
	for _, h := range heldOutFields {
		arms[armKey{h, "A"}] = &arm{perDay: map[int64]*dayStats{}}
		arms[armKey{h, "B"}] = &arm{perDay: map[int64]*dayStats{}}
		mi[h] = newMICounter()
	}

	introduceAt := event.Timestamp(introduceSec) * event.Second
	endAt := event.Timestamp(endSec) * event.Second
	var scored int64

	sink := func(se application.ScoredEvent) error {
		scored++
		tSeconds := int64(se.Event.OccurredAt() / event.Second)
		day := tSeconds / 86400
		key := fmt.Sprintf("%d|%s|%s|%s", tSeconds, se.Event.Entity(),
			fieldText(se.Event, "auth.source_computer"), fieldText(se.Event, "auth.destination_computer"))
		_, isRed := labels.keys[key]

		// MI counters, on a deterministic sample: every 16th scored event.
		if scored%16 == 0 {
			for _, h := range heldOutFields {
				hv := fieldText(se.Event, h)
				if hv == "" {
					continue
				}
				for _, r := range []event.FieldPath{
					"auth.authentication_type", "auth.logon_type",
					"auth.authentication_orientation", "auth.success_failure",
					"auth.destination_computer",
				} {
					if r == h {
						continue
					}
					if rv := fieldText(se.Event, r); rv != "" {
						mi[h].observe(hv, r, rv)
					}
				}
			}
		}

		evaluated := se.Verdicts.Evaluated()
		for _, h := range heldOutFields {
			introduced := se.Event.OccurredAt() >= introduceAt
			for _, treatment := range []string{"A", "B"} {
				a := arms[armKey{h, treatment}]

				include := introduced
				if treatment == "A" && introduced {
					// Count the field's verdicts since introduction; join after n₀.
					include = a.verdictsSeen >= n0
				}

				ps := make([]float64, 0, len(evaluated))
				for _, v := range evaluated {
					if touchesField(v, h) && !include {
						continue
					}
					p, _ := v.PValue()
					ps = append(ps, p)
				}
				if len(ps) == 0 {
					continue
				}
				_, _, tail, ferr := calibration.Fisher(ps)
				if ferr != nil {
					return ferr
				}
				a.push(day, alertRec{p: tail, t: tSeconds, isRed: isRed})
			}
			if introduced {
				for _, v := range evaluated {
					if touchesField(v, h) {
						arms[armKey{h, "A"}].verdictsSeen++
						break
					}
				}
			}
		}
		return nil
	}

	var admitted int64
	cmd := &application.ReplayCorpusCommand{
		Source:        &timeCappedSource{reader: reader, endAt: endAt, seen: &admitted},
		Detectors:     detectors,
		FieldRegistry: fieldRegistry,
		BurnInEnd:     event.Timestamp(burnInSec) * event.Second,
		IncludeEntity: func(e event.EntityID) bool { return len(e) > 1 && e[0] == 'U' },
		Sink:          sink,
	}
	report, err := cmd.Execute(context.Background())
	if err != nil {
		return err
	}

	// Realised FDR per era per arm, at each nominal q, with the same per-day BH
	// construction as cmd/analyse.
	qGrid := []float64{0.001, 0.01, 0.05, 0.1}
	introDay := introduceSec / 86400
	meanPerDay := float64(scored) / float64((endSec-burnInSec)/86400)

	type eraResult struct {
		Era         string  `json:"era"`
		NominalQ    float64 `json:"nominal_q"`
		Discoveries int     `json:"discoveries"`
		TP          int     `json:"true_positives"`

		// RealisedFDR is a pointer so that a censored era carries no figure at all
		// rather than a zero. A zero would read as a perfect result, which is the
		// opposite of what a censored measurement means.
		RealisedFDR *float64 `json:"realised_fdr,omitempty"`

		// Censored records that the BH cut reached the retention boundary, in which
		// case Discoveries is a lower bound and no FDR is reported.
		Censored     bool   `json:"censored,omitempty"`
		CensoredDays int    `json:"censored_days,omitempty"`
		Note         string `json:"note,omitempty"`
	}
	armResults := map[string]any{}
	for _, h := range heldOutFields {
		miVal, miField := mi[h].maxPairwiseMI()
		for _, treatment := range []string{"A", "B"} {
			a := arms[armKey{h, treatment}]
			var rows []eraResult
			for _, q := range qGrid {
				for _, era := range []string{"pre_introduction", "post_introduction"} {
					disc, tp := 0, 0
					// censoredDays counts the days whose BH cut reached the last
					// retained alert. On such a day the true cut lies at or beyond the
					// retention boundary, so the discovery count is a lower bound and
					// the realised FDR derived from it is not a measurement.
					censoredDays := 0
					for day, ds := range a.perDay {
						if (era == "pre_introduction") != (day < introDay) {
							continue
						}
						alerts := ds.sortedAlerts()
						cut := -1
						for i, r := range alerts {
							if r.p <= (float64(i+1)/meanPerDay)*q {
								cut = i
							}
						}
						if cut == len(alerts)-1 && len(alerts) == topK {
							censoredDays++
						}
						for i := 0; i <= cut; i++ {
							disc++
							if alerts[i].isRed {
								tp++
							}
						}
					}
					row := eraResult{Era: era, NominalQ: q, Discoveries: disc, TP: tp}
					if censoredDays > 0 {
						// Report the censoring instead of a figure. A realised FDR
						// computed from a censored count would look like a measurement
						// and behave like an artefact of the retention limit.
						row.Censored = true
						row.CensoredDays = censoredDays
						row.Note = fmt.Sprintf(
							"right-censored: on %d day(s) the BH cut reached the last of "+
								"%d retained alerts, so the discovery count is a lower bound "+
								"and no realised FDR is reported for this era",
							censoredDays, topK)
					} else if disc > 0 {
						fdr := float64(disc-tp) / float64(disc)
						row.RealisedFDR = &fdr
					}
					rows = append(rows, row)
				}
			}
			armResults[fmt.Sprintf("%s|%s", h, treatment)] = map[string]any{
				"held_out_field":   string(h),
				"treatment":        treatment,
				"max_pairwise_mi":  miVal,
				"mi_against_field": miField,
				"eras":             rows,
			}
		}
	}

	out := map[string]any{
		"schema_version": "1",
		"kind":           "e5",
		"hypothesis":     []string{"E5"},
		"paper_refs":     map[string]any{"sections": []string{"§11.2", "§11.3", "§12.3"}, "equations": []int{18, 20}},
		"run": map[string]any{
			"run_id": runID, "started_at": started.Format(time.RFC3339),
			"finished_at": time.Now().UTC().Format(time.RFC3339),
			"go_version":  runtime.Version(),
		},
		"corpus": map[string]any{
			"files":         []map[string]any{{"path": provenance.RecordedPath(authPath), "sha256": "windowed run; full-corpus hash carried by the primary replay result"}},
			"rows_read":     reader.Rows(),
			"events_scored": report.EventsScored,
			"coverage": map[string]any{
				"kind": "window", "from_seconds": burnInSec, "to_seconds": endSec,
				"entity_population": "U*@ human accounts",
			},
			"burn_in": map[string]any{"end_seconds": burnInSec, "fixed_at_commit": "24c5a53"},
		},
		"parameters": map[string]any{
			"held_out_fields": heldOutFields,
			"introduce_at":    introduceSec,
			"n0":              n0,
			"q_grid":          qGrid,
			"treatment_c": "NOT RUN: mask embedding and borrowing requires a mask-similarity " +
				"model; a shallow version degrades to treatment A without reporting it (§11.2), " +
				"which would be worse than declaring the treatment unimplemented",
			"cooccurrence_approximation": "the co-occurrence verdict is dropped in held-out " +
				"eras when its minimising pair touches the held-out field; the graph itself " +
				"still contains the field's nodes, an approximation recorded here",
			"mi_estimator": "maximum pairwise empirical MI (nats) against retained categorical " +
				"fields, on a deterministic 1-in-16 event sample",
		},
		"results":             map[string]any{"arms": armResults},
		"provenance_complete": true,
	}
	if err := writeJSON(outPath, out); err != nil {
		return err
	}
	log.Printf("E5 complete: %d scored events, %d arms", scored, len(armResults))
	return nil
}

// touchesField reports whether a verdict is about the held-out field.
func touchesField(v detector.Verdict, h event.FieldPath) bool {
	for _, f := range v.Target().Fields {
		if f == h {
			return true
		}
	}
	return false
}

func fieldText(e *event.Event, f event.FieldPath) string {
	v, ok := e.Get(f)
	if !ok || !v.IsUsable() {
		return ""
	}
	return v.Text()
}

type timeCappedSource struct {
	reader *corpus.Reader
	endAt  event.Timestamp
	seen   *int64
}

func (s *timeCappedSource) Next() (*event.Event, error) {
	*s.seen++
	e, err := s.reader.Next()
	if err != nil {
		return nil, err
	}
	if e.OccurredAt() >= s.endAt {
		return nil, io.EOF
	}
	return e, nil
}

type redTeamLabels struct {
	keys map[string]struct{}
}

func loadRedTeam(path string) (*redTeamLabels, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the ground truth the flag names
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	labels := &redTeamLabels{keys: map[string]struct{}{}}
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) != 4 {
			return nil, fmt.Errorf("redteam: %d fields", len(parts))
		}
		labels.keys[parts[0]+"|"+parts[1]+"|"+parts[2]+"|"+parts[3]] = struct{}{}
	}
	return labels, sc.Err()
}

func writeJSON(path string, v any) error {
	dir := "."
	if idx := strings.LastIndexAny(path, `/\`); idx >= 0 {
		dir = path[:idx]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.Create(path) //nolint:gosec // the output the flag names
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", " ")
	if err := enc.Encode(v); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
