// Command replay streams a corpus through the framework and writes a result JSON
// carrying full provenance: run identity, git state, corpus checksums, row counts, the
// frozen burn-in split, parameters, and the measured outcomes. The report renderer
// reads only these files; a number that did not come from a run of this command does
// not exist as far as the report is concerned.
package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"reflect"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/JohnPierman/ethogram/application"
	"github.com/JohnPierman/ethogram/domain/calibration"
	"github.com/JohnPierman/ethogram/domain/cellgrid"
	"github.com/JohnPierman/ethogram/domain/cooccurrence"
	"github.com/JohnPierman/ethogram/domain/derive"
	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/drift"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/marginal"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/noveltyrate"
	"github.com/JohnPierman/ethogram/domain/objective"
	"github.com/JohnPierman/ethogram/domain/pairing"
	"github.com/JohnPierman/ethogram/domain/registry"
	"github.com/JohnPierman/ethogram/domain/timing"
	"github.com/JohnPierman/ethogram/domain/volume"
	"github.com/JohnPierman/ethogram/infrastructure/corpus"
	"github.com/JohnPierman/ethogram/infrastructure/partition"
	"github.com/JohnPierman/ethogram/infrastructure/state/memory"
	"github.com/JohnPierman/ethogram/internal/provenance"
)

func main() {
	var (
		authPath    = flag.String("auth", "data/lanl/auth.txt.gz", "path to auth.txt.gz")
		redteamPath = flag.String("redteam", "data/lanl/redteam.txt.gz", "path to redteam.txt.gz")
		outPath     = flag.String("out", "", "result JSON path (required)")
		runID       = flag.String("run-id", "", "run identifier (required)")
		burnInSec   = flag.Int64("burnin", 604800, "burn-in end, seconds on the corpus epoch")
		store       = flag.String("store", "memory",
			"the state backing store: memory or postgres (#48)")
		dsn = flag.String("dsn", "postgres://cad:cad_dev_only@127.0.0.1:55432/cad",
			"connection string, read only when -store postgres")
		storeTruncate = flag.Bool("store-truncate", false,
			"empty this store's own tables before the run. For the -store equivalence "+
				"harness, which compares against a memory store that necessarily starts "+
				"empty; it destroys persisted state, so it is off by default")
		online = flag.String("online", "none",
			"run an online error-control rule beside the per-day step-up: none or lord (#16)")
		onlineArm = flag.String("online-arm", "novelty",
			"the detector whose stream the online rule reports as its headline; the "+
				"composite is always carried beside it as the negative control")
		weighting = flag.String("weighting", "none",
			"reweight the selection by a covariate: none, history or asset (#15)")
		maxRows       = flag.Int64("maxrows", 0, "stop after this many admitted events (0 = all); partial coverage is recorded")
		maxSeconds    = flag.Int64("maxseconds", 0, "stop at this corpus timestamp, seconds (0 = all); partial coverage is recorded in corpus time")
		skipHash      = flag.Bool("skip-hash", false, "skip corpus SHA-256 (smoke runs only; recorded as unhashed)")
		halfLifeDay   = flag.Float64("half-life-days", 7, "decay half-life T1/2 in days")
		bandwidth     = flag.Float64("bandwidth-hours", 1.5, "timing kernel bandwidth in hours (equation 8)")
		alphaFlag     = flag.Float64("alpha", 1.0, "Dirichlet concentration for equation (4)")
		topK          = flag.Int("topk", 200, "alerts retained per day (must cover the largest budget)")
		budgetSpec    = flag.String("budgets", "10,25,50,100", "comma-separated per-day alert budgets to report detections at. Every budget must be within -topk, since a day's retained alert list is what a budget is drawn from. Recorded in the result")
		entitySample  = flag.Int("entity-sample", 0, "keep 1 entity in N (0 or 1 = all); every labelled entity is kept regardless, which inflates the labelled share and is recorded")
		allowResample = flag.Bool("allow-resampling", false, "permit -entity-sample on a corpus whose manifest already records entity sampling. Refused by default: the two selectors can be disjoint, in which case every background entity is dropped and the run measures labelled traffic against itself. Recorded in the result when used")
		detached      = flag.Bool("detached", false, "ignore console interrupts, for a long batch run that must not be lost to a stray console event; stop it with taskkill /F")
		cpuProfile    = flag.String("cpuprofile", "", "write a CPU profile (diagnostics only)")
		exportGraph   = flag.String("export-graph", "", "write the co-occurrence graph TSV at burn-in end (for the offline Leiden batch)")
		partitionIn   = flag.String("partition", "", "partition JSON from sidecar/partition.py; absent means the (15) fallback, recorded")
		shadowCells   = flag.Bool("shadow-cells", false, "score the 168-cell ablation as a shadow and record the E9 substituted combination")
		openVocab     = flag.Bool("open-vocabulary", false, "reserve Detector I's unseen mass by Good-Turing rather than by equation (4)'s fixed alpha, for fields whose value set is unbounded (addresses, hostnames, user agents). Recorded in the result")
		deriveOn      = flag.Bool("derive", false, "infer structure inside field values and score a coarser field beside each one that has it: a /24 beside an address, a parent domain beside a hostname, a major version beside a build string. A novel /24 is a different and usually stronger signal than a novel exact address, and on an open vocabulary the exact form is mostly singletons so every event looks like a first. Names no field: the decision is made from the observed values (R2). Recorded in the result")
		noveltyRateOn = flag.Bool("novelty-rate", true, "add Detector V, the novelty-RATE detector: is this entity producing first-ever values at a higher rate than it historically does, K ~ BetaBinomial(m, a, b) over an hourly window. Detector I asks how improbable one novel value was, and its answer is essentially 1/n, so an account needs ~117,000 events of history before any single novel value can win an alert slot and 736 of 856 planted attack events were unreachable. This asks a scale-free question instead. ON by default: two recorded corpora justify the flip -- 0/22/185 detections at 10/100/1000 alerts a day on r11 and 0/21/384 on the injected corpus, where it is the broadest arm on planted attacks, reaching four of six mechanisms. Recorded in the result")
		pairingOn     = flag.Bool("pairing", true, "replace Detector III's population co-occurrence null with its per-entity form: is this pairing novel for THIS entity, against its own history. ON by default: two recorded corpora justify the flip -- 4/59/127 detections at 10/100/1000 alerts a day on r11 and 4/59/142 on the injected corpus -- and the form it replaces is measured miscalibrated, with 18.4% of scored events below 1e-12 and no detections at any budget. Pass -pairing=false for the population form, which is retained for the ablation of section 12.3 and is not a configuration to deploy. Recorded in the result")
		conformalOn   = flag.Bool("conformal", false, "apply Ã‚Â§10.1 conformal calibration: replace each detector's model tail with its rank in that detector's burn-in distribution, frozen at the boundary. Recorded in the result; floors every p-value at 1/(n+1)")
		schemaPath    = flag.String("schema", "", "schema configuration JSON (config/schemas/*.json); empty uses the built-in LANL auth schema")
		leidenPy      = flag.String("leiden", "", "python interpreter for sidecar/partition.py; set to run the offline partition at the burn-in boundary and score the (14) arm as a shadow (E4)")
		leidenSeed    = flag.Int64("leiden-seed", 42, "seed for the offline Leiden batch, recorded")
		weightedOn    = flag.Bool("weighted", false, "add the weighted arm: score each alert by the log-likelihood ratio its own detector's fitted weight implies over that detector's frozen burn-in null, and give the day's budget to the highest scores. It divides a fixed budget by demonstrated quality where the union arm divides it by quota. Needs the burn-in mirror, so it costs what -ledger costs. Off by default until a recorded run justifies the flip; recorded in the result either way")
		timStandard   = flag.Bool("timing-standardise", timing.DefaultStandardise, "score the timing arm by U standardised against the entity's own realised ln U, rather than by the level-set mass of equation (9). The mass is floored at 1/(2G) = 9.77e-04, which is at or above its own realised alert cut at the tighter budgets, so it cannot express a departure it has detected; the standardised form is unbounded below. Needs enough per-entity history to estimate the null, and abstains below it rather than mixing two statistics in one arm. Off by default until a recorded run justifies the flip; recorded in the result either way")
		volMinPeriods = flag.Int64("volume-min-periods", volume.DefaultMinPeriods, "the fewest completed periods the volume arm will form an opinion on; below it the arm abstains under R3 rather than reporting the prior's tail as the entity's. Zero disables the gate, which is the pre-#25 behaviour and what the volume_gate_probe diagnostic needs in order to measure every candidate threshold from one pass. Recorded in the result")
		driftOn       = flag.Bool("drift", false, "add the sequential-change arm: Page's one-sided cumulative sum over the entity's per-period counts, standardised against the sums its own closed periods produced. It answers a question equation (11) cannot -- an over-dispersed marginal test of one period cannot see a shift that is small in every period, because the evidence is in the sequence. Off by default until a recorded run justifies the flip; recorded in the result either way")
		driftShift    = flag.Float64("drift-shift", drift.DefaultShift, "the multiplicative rate shift the cumulative sum's reference value is tuned for. A stated parameter, never fitted: tuning it on the labels the arm is scored against would make its sensitivity a restatement of the corpus. Recorded in the result")
		driftMinPer   = flag.Int64("drift-min-periods", drift.DefaultMinPeriods, "the fewest closed periods the drift arm will form an opinion on; below it there is no null to standardise against and the arm abstains under R3. Recorded in the result")
		ledgerPath    = flag.String("ledger", "", "write the alert ledger here: every per-detector arm's ranked queue per day, for both the burn-in and the scoring window, so budget-allocation rules can be screened offline instead of at eighty minutes a candidate. Also mirrors the per-arm ranking across burn-in, which roughly doubles burn-in cost. An intermediate artefact: never write it into results/, which holds measurements with provenance")
	)
	flag.Parse()

	// Parsed and validated at the boundary: an unanswerable budget must stop the run
	// before it reads a corpus, not after. A budget above the retained per-day alert
	// list cannot be answered, and the shortfall would read as a queue that found
	// nothing rather than as a question this run cannot answer.
	budgets, budgetErr := objective.ParseBudgets(*budgetSpec)
	if budgetErr != nil {
		log.Fatal(budgetErr)
	}
	if budgets.Max() > *topK {
		log.Fatalf("replay: budget %d exceeds -topk %d; raise -topk to at least %d",
			budgets.Max(), *topK, budgets.Max())
	}
	if *outPath == "" || *runID == "" {
		log.Fatal("both -out and -run-id are required")
	}
	if *detached {
		// A full replay is hours of work whose only output is written at the end, so a
		// stray console control event destroys the entire run. This has happened: a run
		// seven eighths of the way through its burn-in was terminated with
		// STATUS_CONTROL_C_EXIT by a console event it had no interest in, having
		// consumed an hour of compute and produced nothing.
		//
		// Ignoring the interrupt is confined to this flag so that an interactive run
		// stays interruptible in the ordinary way. A detached batch run remains
		// killable by any means that does not go through the console, which is what an
		// operator stopping it deliberately would use.
		// Go delivers Ctrl+C and Ctrl+Break as os.Interrupt, and console close, logoff
		// and shutdown as SIGTERM; both routes have to be closed for the run to be
		// safe from a console it never asked for.
		signal.Ignore(os.Interrupt, syscall.SIGTERM)
		log.Print("detached: console interrupts ignored; stop this run with taskkill /F")
	}
	if *cpuProfile != "" {
		pf, err := os.Create(*cpuProfile)
		if err != nil {
			log.Fatal(err)
		}
		if err := pprof.StartCPUProfile(pf); err != nil {
			log.Fatal(err)
		}
		defer pprof.StopCPUProfile()
	}

	onlineRule, err := parseOnline(*online)
	if err != nil {
		log.Fatal(err)
	}
	backingStore, err := parseStore(*store)
	if err != nil {
		log.Fatal(err)
	}
	mode, err := parseWeighting(*weighting)
	if err != nil {
		log.Fatal(err)
	}

	if err := run(runConfig{
		authPath: *authPath, redteamPath: *redteamPath, outPath: *outPath, runID: *runID,
		burnInSec: *burnInSec, maxRows: *maxRows, skipHash: *skipHash,
		online:        onlineRule,
		onlineArm:     detector.ID(*onlineArm),
		store:         backingStore,
		dsn:           *dsn,
		storeTruncate: *storeTruncate,
		weighting:     mode,
		halfLifeDays:  *halfLifeDay, bandwidthHours: *bandwidth, alpha: *alphaFlag,
		volMinPeriods: *volMinPeriods, timStandardise: *timStandard,
		drift: *driftOn, driftShift: *driftShift, driftMinPeriods: *driftMinPer,
		topK: *topK, budgets: budgets, entitySample: *entitySample, allowResampling: *allowResample,
		exportGraph: *exportGraph, partitionIn: *partitionIn,
		maxSeconds: *maxSeconds, shadowCells: *shadowCells, conformal: *conformalOn,
		openVocabulary: *openVocab,
		pairing:        *pairingOn,
		noveltyRate:    *noveltyRateOn,
		derive:         *deriveOn,
		schemaPath:     *schemaPath,
		leidenPy:       *leidenPy, leidenSeed: *leidenSeed,
		ledgerPath: *ledgerPath,
		weighted:   *weightedOn,
	}); err != nil {
		log.Fatal(err)
	}
}

type runConfig struct {
	authPath, redteamPath, outPath, runID string
	burnInSec, maxRows                    int64
	// online selects the online error-control rule (#16), and onlineArm the detector whose
	// stream it reports as its headline. The default runs no rule.
	online    onlineMode
	onlineArm detector.ID
	// store selects the state backing store and dsn its connection string (#48). The
	// default is memory, which is what every earlier run used.
	store storeKind
	dsn   string
	// storeTruncate empties the persistent store before the run, for the equivalence
	// harness. Off by default: it destroys state.
	storeTruncate bool
	// weighting selects the covariate the selection is reweighted by (#15). The default
	// is none, which is the unweighted ranking every earlier run used.
	weighting                           weightingMode
	skipHash                            bool
	halfLifeDays, bandwidthHours, alpha float64
	volMinPeriods                       int64
	timStandardise                      bool
	drift                               bool
	driftShift                          float64
	driftMinPeriods                     int64
	topK                                int
	budgets                             objective.Budgets
	entitySample                        int
	allowResampling                     bool
	exportGraph, partitionIn            string
	maxSeconds                          int64
	shadowCells                         bool
	conformal                           bool
	openVocabulary                      bool
	pairing                             bool
	noveltyRate                         bool
	derive                              bool
	schemaPath                          string
	leidenPy                            string
	leidenSeed                          int64
	ledgerPath                          string
	weighted                            bool
}

func run(cfg runConfig) error {
	// One context for the whole run, shared by the state store and the corpus execution:
	// they are the same unit of work, and a store outliving the replay it backed would be a
	// leak rather than a feature.
	ctx := context.Background()

	authPath, redteamPath, outPath, runID := cfg.authPath, cfg.redteamPath, cfg.outPath, cfg.runID
	burnInSec, maxRows, skipHash := cfg.burnInSec, cfg.maxRows, cfg.skipHash
	halfLifeDays, bandwidthHours, alpha, topK := cfg.halfLifeDays, cfg.bandwidthHours, cfg.alpha, cfg.topK
	started := time.Now().UTC()

	// Before anything expensive: refuse to sample a corpus that is already a sample.
	// Checked here rather than after burn-in because the failure is silent in the
	// result, so it has to be caught before the run rather than diagnosed after it.
	subset, err := readCorpusSubset(authPath)
	if err != nil {
		return err
	}
	if err = checkResampling(cfg, subset); err != nil {
		return err
	}
	if subset != nil {
		log.Printf("corpus is an entity subset: 1 in %d, residue %d, %d distinct users",
			subset.Sampling.KeepOneEntityInN, subset.Sampling.SampleResidue,
			subset.Counts.DistinctUsersKept)
	}

	// A higher collection target trades heap for throughput on this batch
	// workload; it changes no score (R4 concerns the arithmetic, not the
	// allocator) and is recorded in the result for reproducibility of T5.
	debug.SetGCPercent(400)

	labels, err := loadRedTeam(redteamPath)
	if err != nil {
		return fmt.Errorf("load red team: %w", err)
	}
	log.Printf("red-team labels: %d rows, %d distinct keys", labels.rows, len(labels.keys))

	authHash := "not-computed(smoke)"
	if !skipHash {
		authHash, err = fileSHA256(authPath)
		if err != nil {
			return err
		}
		log.Printf("auth sha256 = %s", authHash)
	}

	f, err := os.Open(authPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }() // read-only; close errors carry no information
	zr, err := gzip.NewReader(bufio.NewReaderSize(f, 4<<20))
	if err != nil {
		return err
	}

	halfLife := novelty.HalfLife(halfLifeDays * float64(event.Day))
	// The schema is configuration (R2, E6): a file when given, otherwise the
	// built-in LANL auth literal with the frozen entity population applied
	// ingest-side. Rows outside the population never build events; the
	// engine-level filter below remains the semantic gate.
	var schema corpus.Schema
	schemaProvenance := "built-in lanl.auth literal"
	if cfg.schemaPath != "" {
		schema, err = corpus.LoadSchema(cfg.schemaPath)
		if err != nil {
			return fmt.Errorf("load schema: %w", err)
		}
		schemaProvenance = cfg.schemaPath
	} else {
		schema = lanlAuthSchema()
		schema.EntityFilter = func(entity string) bool {
			return len(entity) > 1 && entity[0] == 'U' && entity[1] >= '0' && entity[1] <= '9'
		}
	}
	reader := corpus.NewReader(zr, schema)

	fieldRegistry := registry.New(registry.DefaultPolicy())
	stores, err := newStateStores(ctx, cfg.store, cfg.dsn, cfg.storeTruncate, halfLife)
	if err != nil {
		return err
	}
	defer stores.close()
	novStore := stores.novelty
	nrStore := stores.noveltyRate
	timStore := stores.timing
	volStore := stores.volume
	driStore := stores.drift
	margStore := stores.marginal
	graph := stores.graph

	var part *cooccurrence.Partition
	partitionMode := "fallback: single-block degeneration (15), recorded per \u00a78.4"
	_ = part
	if cfg.partitionIn != "" {
		part, err = partition.Load(cfg.partitionIn)
		if err != nil {
			return fmt.Errorf("load partition: %w", err)
		}
		partitionMode = fmt.Sprintf("leiden seed=%d checksum=%s resolution=%g",
			part.Seed, part.GraphChecksum[:12], part.Resolution)
	}
	coocDetector := cooccurrence.NewDetector(graph, fieldRegistry, part, halfLife)

	detectors := detector.NewRegistry()
	noveltyDetector := novelty.NewDetector(novStore, fieldRegistry, alpha, halfLife)
	if cfg.openVocabulary {
		noveltyDetector = noveltyDetector.WithOpenVocabulary()
	}

	// Detector III's slot. The population co-occurrence null tests departure from the
	// POPULATION norm, which Ã‚Â§7.6 disavows and which measured as meaningless rather than
	// merely loose: 18.4% of events below 1eÃ¢Ë†â€™12, ln P reaching Ã¢Ë†â€™39,278, and no detection.
	// Its per-entity replacement asks the same question of the entity's own history.
	//
	// Off by default until a recorded run justifies the flip, on this project's standing
	// rule that a default which changes what every result means is not changed on an
	// argument. Both are recorded in parameters either way.
	relationalDetector := detector.Detector(coocDetector)
	if cfg.pairing {
		// Its own store instance rather than novelty's: a pairing is addressed as a value
		// so it needs no new infrastructure, but keeping the state separate leaves
		// Detector I's row counts and its measured figures untouched, so the two arms
		// stay comparable across runs that differ only in this flag.
		pairingDetector := pairing.NewDetector(
			memory.NewNoveltyStore(halfLife), fieldRegistry, alpha, halfLife)
		if cfg.openVocabulary {
			pairingDetector = pairingDetector.WithOpenVocabulary()
		}
		relationalDetector = pairingDetector
	}

	registered := []detector.Detector{
		noveltyDetector,
		timing.NewDetector(timStore, bandwidthHours, halfLife, cfg.timStandardise),
		volume.NewDetector(volStore, timStore, bandwidthHours, halfLife, cfg.volMinPeriods),
		relationalDetector,
		marginal.NewDetector(margStore, fieldRegistry, alpha,
			minMarginalObservations, halfLife),
	}
	if cfg.drift {
		// Its own state rather than the volume arm's. The baseline a change statistic
		// measures against is the level *before* the change, and an estimator shared with
		// an arm that updates on every event would let the drift being tested raise its
		// own baseline.
		driftDetector, driftErr := drift.NewDetector(
			driStore, halfLife, cfg.driftShift, cfg.driftMinPeriods)
		if driftErr != nil {
			return driftErr
		}
		registered = append(registered, driftDetector)
	}
	if cfg.noveltyRate {
		// It reads novelty's own value store rather than a copy: "has this entity seen
		// this value before" is the same question in both places, and answering it from a
		// second store would let the two disagree about what is novel.
		registered = append(registered, noveltyrate.NewDetector(
			nrStore, novStore, fieldRegistry, halfLife))
	}
	for _, d := range registered {
		if regErr := detectors.Register(d); regErr != nil {
			return regErr
		}
	}

	var shadows []detector.Detector
	if cfg.shadowCells {
		shadows = append(shadows, cellgrid.NewDetector(
			memory.NewNoveltyStore(halfLife), alpha, halfLife))
	}

	// The E4 ablation: a second co-occurrence detector over the SAME graph, whose
	// partition is installed at the burn-in boundary. Both arms therefore see
	// identical graph state and identical events; the only difference between them
	// is whether ÃŽÂ» comes from equation (14) with the discovered blocks or from its
	// single-block degeneration (15). Ã‚Â§12.3 calls this "direct ablation on identical
	// data", and running it as a shadow in one pass is what makes the data identical
	// rather than merely comparable.
	var coocPartitioned *cooccurrence.Detector
	if cfg.leidenPy != "" {
		coocPartitioned = cooccurrence.NewDetector(graph, fieldRegistry, nil, halfLife).
			WithID(coocPartitionedID).ReadOnly()
		shadows = append(shadows, coocPartitioned)
	}

	acc := newAccumulator(labels, topK, cfg.budgets, cfg.pairing, cfg.weighting,
		cfg.online, cfg.onlineArm)
	acc.volGate = newVolumeGateProbe(topK, cfg.volMinPeriods)
	var rowsSeen int64
	src := &cappedSource{reader: reader, max: maxRows, seen: &rowsSeen,
		maxAt: event.Timestamp(cfg.maxSeconds) * event.Second}

	// The dependence between detectors, estimated on burn-in and frozen at the
	// boundary, so Ã‚Â§10.2's combination applies Brown's correction rather than assuming
	// an independence these detectors plainly do not have.
	correlations := calibration.NewCorrelations(minCorrelationObservations)

	// Ã‚Â§10.1's conformal calibration, off unless asked for. It replaces each detector's
	// model tail with its rank in that detector's own burn-in distribution, which is
	// super-uniform whether or not the model is right Ã¢â‚¬â€ but it also floors every
	// p-value at 1/(n+1), so it changes what a recorded number means and must be an
	// explicit choice rather than a default that arrives silently.
	var conformal *calibration.Conformal
	if cfg.conformal {
		conformal = calibration.NewConformal(minConformalObservations)
	}

	cmd := &application.ReplayCorpusCommand{
		Source:        src,
		Correlations:  correlations,
		Conformal:     conformal,
		Detectors:     detectors,
		FieldRegistry: fieldRegistry,
		BurnInEnd:     event.Timestamp(burnInSec) * event.Second,
		IncludeEntity: includeEntity(cfg, labels),
		Shadows:       shadows,
		Sink:          acc.observe,
	}
	if cfg.derive {
		cmd.Deriver = derive.NewInferrer(derive.DefaultPolicy())
	}
	// The burn-in mirror. Either consumer needs it: the ledger dumps it for offline
	// screening, and the weighted arm fits its nulls and its weights from it.
	if cfg.ledgerPath != "" || cfg.weighted {
		acc.burnInFitDays = labels.days
		// Mirroring the per-arm ranking across burn-in is what makes a weight fittable
		// on data the scoring window has not seen. It is off unless a ledger is asked
		// for, because it costs a second ranking over tens of millions of events and
		// buys nothing for a run that is not screening an allocation rule.
		cmd.BurnInSink = acc.observeBurnIn
	}
	if acc.weighting.on() {
		// History weighting needs its own burn-in pass, and a much cheaper one: no
		// ranking, one decimated sample per arm. Composed with the ledger's mirror rather
		// than replacing it, so asking for both does both.
		ledgerSink := cmd.BurnInSink
		cmd.BurnInSink = func(se application.ScoredEvent) error {
			acc.weighting.observeBurnInEvent(string(se.Event.Entity()), se.Verdicts)
			if ledgerSink != nil {
				return ledgerSink(se)
			}
			return nil
		}
	}
	if cfg.exportGraph != "" || cfg.leidenPy != "" {
		graphPath := cfg.exportGraph
		if graphPath == "" {
			graphPath = outPath + ".graph.tsv"
		}
		cmd.OnBurnInComplete = func() error {
			gf, gErr := os.Create(graphPath) //nolint:gosec // the path the flag names
			if gErr != nil {
				return gErr
			}
			edges, gErr := graph.ExportEdges(gf, schema.Source,
				event.Timestamp(burnInSec)*event.Second, 1e-9)
			if cErr := gf.Close(); gErr == nil {
				gErr = cErr
			}
			if gErr != nil {
				return gErr
			}
			log.Printf("burn-in graph exported: %d edges -> %s", edges, graphPath)

			if cfg.leidenPy == "" {
				return nil
			}
			// Ã‚Â§8.2: partitioning is a scheduled batch computation, never in the
			// scoring path. It runs here, once, at the boundary between warm-up and
			// scoring, on burn-in state only Ã¢â‚¬â€ so the partition cannot have
			// conditioned on any event it will later be used to score.
			// The partition is an intermediate artefact, not a result, and it is
			// written beside the graph it was computed from rather than into the
			// results directory. A file in results/ is a measurement with provenance;
			// putting anything else there makes the provenance gate reject the whole
			// directory, which it did Ã¢â‚¬â€ correctly Ã¢â‚¬â€ when this was `outPath +
			// ".partition.json"`.
			partPath := graphPath + ".partition.json"
			batchStart := time.Now()
			out, runErr := exec.Command(cfg.leidenPy, "sidecar/partition.py", //nolint:gosec // operator-supplied interpreter
				"--graph", graphPath, "--out", partPath,
				"--seed", fmt.Sprintf("%d", cfg.leidenSeed)).CombinedOutput()
			if runErr != nil {
				return fmt.Errorf("leiden batch: %w: %s", runErr, string(out))
			}
			log.Printf("leiden batch (%s): %s", time.Since(batchStart).Round(time.Second),
				strings.TrimSpace(string(out)))

			loaded, loadErr := partition.Load(partPath)
			if loadErr != nil {
				return fmt.Errorf("load partition: %w", loadErr)
			}
			coocPartitioned.SetPartition(loaded)
			partitionMode = fmt.Sprintf("leiden seed=%d checksum=%s resolution=%g (E4 arm, "+
				"computed at the burn-in boundary from burn-in state only)",
				loaded.Seed, loaded.GraphChecksum[:12], loaded.Resolution)
			return nil
		}
	}

	// Fitting the weights happens here and nowhere else: after every burn-in event and
	// before the first scored one, which is what makes them independent of every p-value
	// they rank. Registered last so it wraps the graph export above rather than being
	// overwritten by it, and both still run.
	if acc.weighting.on() {
		alsoAtBoundary := cmd.OnBurnInComplete
		cmd.OnBurnInComplete = func() error {
			if fitErr := acc.weighting.freeze(); fitErr != nil {
				return fitErr
			}
			if alsoAtBoundary != nil {
				return alsoAtBoundary()
			}
			return nil
		}
	}

	progress := time.NewTicker(60 * time.Second)
	defer progress.Stop()
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-progress.C:
				log.Printf("... rows=%d scored=%d alerts-tracked=%d", rowsSeen,
					acc.scored, acc.tracked)
			case <-done:
				return
			}
		}
	}()

	report, err := cmd.Execute(ctx)
	close(done)
	if err != nil {
		return fmt.Errorf("replay: %w", err)
	}
	finished := time.Now().UTC()

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	result := map[string]any{
		"schema_version": "1",
		"kind":           "replay",
		"hypothesis":     hypothesisList(cfg, len(acc.redTeamScored)),
		"paper_refs": map[string]any{
			"sections":  []string{"Ã‚Â§6", "Ã‚Â§7.2", "Ã‚Â§7.4", "Ã‚Â§10.2", "Ã‚Â§12.3", "Ã‚Â§12.4"},
			"equations": []int{4, 5, 6, 7, 8, 9, 10, 11, 18},
		},
		"run": map[string]any{
			"run_id":      runID,
			"started_at":  started.Format(time.RFC3339),
			"finished_at": finished.Format(time.RFC3339),
			"git_sha":     gitSHA(),
			"git_dirty":   gitDirty(),
			"go_version":  runtime.Version(),
			"os_arch":     runtime.GOOS + "/" + runtime.GOARCH,
			// Derived from the registry that actually scored, not written by hand. The
			// hand-written list said four detectors after Detector IV was added, so
			// every result claimed a composition it had not used Ã¢â‚¬â€ the one thing a
			// provenance block exists to prevent.
			"detectors": registeredDetectorIDs(detectors),
			"partition": partitionMode,
			"schema":    schemaProvenance,
		},
		"corpus": map[string]any{
			"files": []map[string]any{
				{"path": provenance.RecordedPath(authPath), "sha256": authHash},
				{"path": redteamPath, "sha256": labels.sha256},
			},
			"rows_read":        reader.Rows(),
			"rows_prefiltered": reader.FilteredByEntity(),
			"row_errors":       report.RowErrors,
			"events_skipped":   report.EventsSkipped,
			"events_warmed":    report.EventsWarmed,
			"events_scored":    report.EventsScored,
			"no_opinion":       report.NoOpinion,
			"coverage": map[string]any{
				"kind":        coverageKind(maxRows, cfg.maxSeconds),
				"max_rows":    maxRows,
				"max_seconds": cfg.maxSeconds,
				"statement":   coverageStatement(burnInSec, maxRows, cfg.maxSeconds),
				"entity_population": "source users matching U*@ (human accounts); " +
					"see DATA.md burn-in section",
			},
			"burn_in": map[string]any{
				"end_seconds":     burnInSec,
				"fixed_at_commit": "24c5a53",
			},
		},
		"seeds": map[string]any{"note": "no stochastic component in the scoring path (R4)"},
		"parameters": map[string]any{
			"alpha": alpha,
			// Whether Detector I reserved unseen mass by (4)'s fixed ÃŽÂ± or by GoodÃ¢â‚¬â€œTuring.
			// Two runs differing only in this are not comparable on novelty's figures, so
			// the difference is recorded rather than inferred from the command line.
			"open_vocabulary": cfg.openVocabulary,
			// Which question Detector III's slot asked. Two runs differing only in this
			// are not comparable on the relational arm's figures, so it is recorded
			// rather than inferred from the command line.
			"relational_detector": relationalRecord(cfg),
			// Whether Detector V ran. It adds an arm rather than replacing one, so a run
			// with it on is comparable to one without on every other arm.
			"novelty_rate": noveltyRateRecord(cfg),
			// The volume arm's R3 abstention. Two runs differing only in this are not
			// comparable on the volume arm's figures, so it is recorded rather than
			// inferred from the command line.
			"volume": volumeRecord(cfg),
			"drift":  driftRecord(cfg),
			// Whether coarse fields were derived from inferred value structure. A run
			// with derivation on scores strictly more fields than one without, so the two
			// are not comparable on any per-field figure.
			"derived_fields":  deriveRecord(cfg),
			"half_life_days":  halfLifeDays,
			"bandwidth_hours": bandwidthHours,
			"grid":            timing.GridSize,
			"top_k_per_day":   topK,
			"budgets":         []int(cfg.budgets),
			"entity_sample":   entitySampleRecord(cfg),
			// What the CORPUS was, as distinct from what this run did to it. A replay
			// that applied no sample can still have read a subset, and only the manifest
			// beside the corpus knows which.
			"corpus_subset":      corpusSubsetRecord(subset),
			"resampling_allowed": cfg.allowResampling,
			"combination": map[string]any{
				"method": "Fisher (18) with Brown's correction (19); the covariance is the " +
					"direct sample covariance of -2 ln P per detector pair, measured on " +
					"burn-in only and frozen at the boundary",
				"min_pair_observations": minCorrelationObservations,
			},
		},
		"dependence": dependenceRecord(cmd.Covariance(), cmd.ConformalModel() != nil),
		"conformal":  conformalRecord(cmd.ConformalModel()),
		"results":    acc.results(),
		"runtime": map[string]any{
			"wall_seconds":   finished.Sub(started).Seconds(),
			"events_per_sec": float64(report.EventsWarmed+report.EventsScored) / finished.Sub(started).Seconds(),
			"heap_alloc_mb":  float64(mem.HeapAlloc) / (1 << 20),
			"heap_sys_mb":    float64(mem.HeapSys) / (1 << 20),
			"graph_nodes":    graph.Nodes(),
			"graph_edges":    graph.Edges(),
			"gc_percent":     400,
		},
		"store":               stores.record(),
		"store_sizes":         stores.counts(ctx),
		"provenance_complete": !skipHash,
	}

	if err := writeJSON(outPath, result); err != nil {
		return err
	}
	log.Printf("wrote %s: rows=%d scored=%d redteam-scored=%d",
		outPath, report.RowsRead, report.EventsScored, len(acc.redTeamScored))

	if cfg.ledgerPath != "" {
		if err := writeJSON(cfg.ledgerPath,
			acc.ledger(runID, cfg.budgets.Max(), burnInSec)); err != nil {
			return err
		}
		log.Printf("wrote ledger %s: %d labelled on burn-in, %d on the scoring window",
			cfg.ledgerPath, len(acc.burnInLabelled), len(acc.redTeamScored))
	}
	return nil
}

// lanlAuthSchema is configuration, held at the composition root: which columns carry
// what. Changing corpora means changing this literal, not the reader (R2, E6).
// hypothesisList states which hypotheses a run's results actually bear on.
//
// A run claims a hypothesis only when it holds the evidence for it. E1 and E2 compare
// detections against ground truth, and E3 measures realised FDR against it, so a run
// that matched no labelled events claims none of them however well it scored: a card
// reading "0 of 0 red-team events" asserts a measurement that was never made. The
// LANL dns source is the case in point Ã¢â‚¬â€ it carries no red-team labels of its own,
// and its value is E6, the onboarding, not detection.
//
// T5 is always claimed: throughput and state size are measured on every run.
func hypothesisList(cfg runConfig, redTeamScored int) []string {
	hs := []string{"T5"}
	if redTeamScored > 0 {
		hs = append(hs, "E1", "E2", "E3")
	}
	if cfg.partitionIn != "" {
		hs = append(hs, "E4")
	}
	if cfg.shadowCells && redTeamScored > 0 {
		hs = append(hs, "E9")
	}
	if cfg.leidenPy != "" && redTeamScored > 0 {
		hs = append(hs, "E4")
	}
	if cfg.schemaPath != "" {
		// A schema-file run demonstrates source onboarding without code changes.
		hs = append(hs, "E6")
	}
	return hs
}

func lanlAuthSchema() corpus.Schema {
	return corpus.Schema{
		Source:       "lanl.auth",
		Delimiter:    ',',
		TimeColumn:   0,
		TimeUnit:     event.Second,
		EntityColumn: 1,
		Columns: []event.FieldPath{
			"", // time
			"auth.source_user",
			"auth.destination_user",
			"auth.source_computer",
			"auth.destination_computer",
			"auth.authentication_type",
			"auth.logon_type",
			"auth.authentication_orientation",
			"auth.success_failure",
		},
		MissingToken: "?",
	}
}

// isHumanAccount implements the frozen entity population: source users matching U*@.
func isHumanAccount(e event.EntityID) bool {
	return len(e) > 1 && e[0] == 'U' && e[1] >= '0' && e[1] <= '9'
}

// includeEntity picks the engine-level population gate. With a schema file the
// ingest-side admit regex already restricted the population, and the file is the
// single authority (E6: configuration, not code); the built-in LANL schema keeps its
// frozen human-accounts gate at both levels.
// includeEntity picks the engine-level population gate, optionally narrowed to a
// deterministic sample of entities.
//
// Sampling exists so a full pass can be rehearsed in minutes rather than hours. It is a
// sample of ENTITIES, not of events: a per-entity detector is a statement about one
// entity's own history, so thinning events within an entity would corrupt exactly the
// histories being tested, while dropping whole entities leaves every retained history
// intact. What it does change is the co-occurrence graph and the population marginals,
// which are built from whoever remains.
//
// Every labelled entity is kept regardless of the sample, so the labelled population is
// not itself thinned. That inflates the labelled share of the corpus relative to the
// full population, which makes a sampled run unsuitable for a headline detection rate;
// the inflation is recorded in the result so a reader cannot mistake one for the other.
func includeEntity(cfg runConfig, labels *redTeamLabels) func(event.EntityID) bool {
	if cfg.entitySample <= 1 {
		if cfg.schemaPath != "" {
			return nil
		}
		return isHumanAccount
	}
	base := isHumanAccount
	if cfg.schemaPath != "" {
		base = func(event.EntityID) bool { return true }
	}
	n := uint64(cfg.entitySample)
	return func(e event.EntityID) bool {
		if !base(e) {
			return false
		}
		if labels != nil {
			if _, labelled := labels.users[string(e)]; labelled {
				return true
			}
		}
		h := fnv.New64a()
		_, _ = h.Write([]byte(e))
		return h.Sum64()%n == 0
	}
}

// minCorrelationObservations is the support a detector pair must reach on burn-in
// before its measured covariance is used in equation (19).
//
// Burn-in supplies tens of millions of events, so any pair that genuinely co-evaluates
// clears this easily; the threshold exists to exclude pairs that barely co-occur, whose
// covariance would be an artefact of a handful of events rather than a measurement. A
// pair below it contributes zero, which is Fisher's assumption for that pair alone.
const minCorrelationObservations = 1000

// deriveRecord states whether coarse fields were derived from inferred value structure.
func deriveRecord(cfg runConfig) map[string]any {
	if !cfg.derive {
		return map[string]any{
			"applied": false,
			"note": "every value was scored at the granularity the source emitted it; a " +
				"novel /24 and a novel exact address are the same event to this run",
		}
	}
	policy := derive.DefaultPolicy()
	return map[string]any{
		"applied":             true,
		"decompositions":      []string{"net24", "parent", "major"},
		"min_distinct_values": policy.MinDistinctValues,
		"min_parse_fraction":  policy.MinParseFraction,
		"max_tracked_values":  policy.MaxTrackedValues,
		"note": "a fixed set of decompositions was tried against every field's observed " +
			"values, and a coarser field registered beside each field whose values " +
			"consistently parsed. No field is named: the decision is made from the values " +
			"(R2). A field matching two decompositions is left undecided rather than given " +
			"an arbitrary winner. Derived fields are scored beside the originals, not " +
			"instead of them, so the exact value keeps its precise signal",
	}
}

// relationalRecord states which question Detector III's slot asked.
//
// The two forms test the same signal Ã¢â‚¬â€ a combination of values scarcely seen together Ã¢â‚¬â€
// at different scopes, and they are not comparable to one another. A run that does not
// say which it used leaves a reader to infer it from a detector name that is identical
// in shape either way.
// noveltyRateRecord describes Detector V's presence, so that a result states whether the
// arm existed rather than leaving a reader to infer it from the absence of a key.
func noveltyRateRecord(cfg runConfig) map[string]any {
	if !cfg.noveltyRate {
		return map[string]any{
			"applied": false,
			"note": "Detector V did not run. Detector I remains the only categorical " +
				"novelty arm, and its p-value for a first-ever value is essentially 1/n " +
				"(measured: p x n has median 1.15 across 32 planted victims), so accounts " +
				"below roughly 117,000 events of history cannot reach a useful alert " +
				"threshold on a single novel value however unprecedented it is",
		}
	}
	return map[string]any{
		"applied":        true,
		"window_seconds": noveltyrate.WindowSeconds,
		"prior":          "Jeffreys Beta(1/2, 1/2) on the per-entity novelty rate",
		"min_history":    noveltyrate.MinHistory,
		"note": "Detector V asks whether the entity is producing first-ever values at a " +
			"higher rate than its own history, K ~ BetaBinomial(m, a, b) over an hourly " +
			"window. Scale-free by construction: the comparison is against the same " +
			"account's rate, never another account's volume. It sees ONLY novelty, so an " +
			"attack departing in timing or volume alone is invisible to it by design",
	}
}

// driftRecord describes the sequential-change arm's presence, so that a result states
// whether the arm existed rather than leaving a reader to infer it from a missing key.
func driftRecord(cfg runConfig) map[string]any {
	if !cfg.drift {
		return map[string]any{
			"applied": false,
			"note": "the sequential-change arm did not run. The volume arm is then the " +
				"only rate test, and equation (11)'s predictive is structurally " +
				"over-dispersed, so a modest shift sustained over many periods sits " +
				"inside its null in every period; measured median p 0.72 on planted " +
				"low-and-slow against 0.29 on the other mechanisms",
		}
	}
	return map[string]any{
		"applied":     true,
		"shift":       cfg.driftShift,
		"min_periods": cfg.driftMinPeriods,
		"min_weight":  drift.MinWeight,
		"statistic": "S_t = max(0, S_{t-1} + k_t - k) with k = lambda0(rho-1)/ln rho, " +
			"Page's reference value; the p-value is the upper tail of S standardised " +
			"against the sums this entity's own closed periods produced",
		"note": "a shift accumulates linearly in the number of periods while the spread " +
			"of its null grows as the square root, which is the alternative a marginal " +
			"test of one period cannot see however well it is calibrated. The shift is " +
			"stated, never fitted",
	}
}

func relationalRecord(cfg runConfig) map[string]any {
	if !cfg.pairing {
		return map[string]any{
			"detector": "cooccurrence",
			"scope":    "population",
			"note": "the population co-occurrence null of Ã‚Â§8: how often the population's " +
				"degree structure predicts these values should have been paired. It fires " +
				"on stable personal preference, which Ã‚Â§7.6 disavows, and measured 18.4% of " +
				"scored events below 1e-12 with ln P reaching -39,278 while contributing " +
				"nothing to detection",
		}
	}
	return map[string]any{
		"detector": "pairing",
		"scope":    "per-entity",
		"note": "the per-entity form: is this pairing novel for THIS entity, against its " +
			"own history. A pairing is addressed as a value of a synthetic composite " +
			"field, so Detector I's estimator scores it with the same decay, reserved " +
			"unseen mass, cold-start convention and Good-Turing treatment, and nothing " +
			"downstream learns that pairs exist. The most surprising pairing on an event " +
			"is reported under a Sidak correction over the pairs tested, as Ã‚Â§8.5 does",
	}
}

// conformalRecord states whether Ã‚Â§10.1's calibration was applied, and on what, so a
// result cannot be compared with an uncalibrated one without the difference being
// visible. The floor is reported per detector because it bounds what the run could have
// expressed: no combined p-value can be more extreme than the combination of the floors.
func conformalRecord(m *calibration.ConformalModel) map[string]any {
	if m == nil {
		return map[string]any{
			"applied": false,
			"note": "each detector kept its own model tail; p-values are calibrated only " +
				"in so far as the nulls of Ã‚Â§6-Ã‚Â§9 hold on this corpus",
		}
	}
	calibrated := m.Calibrated()
	detectors := make([]map[string]any, 0, len(calibrated))
	ids := make([]string, 0, len(calibrated))
	for id := range calibrated {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		floor, _ := m.Floor(id)
		detectors = append(detectors, map[string]any{
			"detector":       id,
			"burn_in_scores": calibrated[id],
			"floor":          floor,
			"floor_log":      math.Log(floor),
		})
	}
	return map[string]any{
		"applied":   true,
		"detectors": detectors,
		"note": "each detector's model tail was replaced by its rank in that detector's " +
			"own burn-in distribution, frozen at the boundary: p_conf = (1 + #{i : p_i <= p}) " +
			"/ (n + 1), which is super-uniform under exchangeability whether or not the null " +
			"holds. A conformal p-value cannot fall below its floor, so events past the " +
			"burn-in tail tie there and are ordered by model_log_p, which is the same " +
			"combination over the detectors' own p-values and is a tie-break only",
	}
}

// dependenceRecord describes equation (19)'s dependence correction.
//
// `estimated` and `applied` are separate fields on purpose. Under conformal calibration
// the covariance is still measured Ã¢â‚¬â€ it is frozen at the burn-in boundary alongside the
// conformal model Ã¢â‚¬â€ but it is deliberately NOT applied to the combination, because the
// two live on different scales: Ã¢Ë†â€™2 ln P runs to thousands on a miscalibrated model tail
// and to tens on a rank, so dividing the calibrated statistic by a scale measured on the
// model one produces a number that means nothing.
//
// Collapsing the two into one `applied` flag, as this record previously did, told a
// reader of a conformal result that Brown had corrected the combination when it had not.
func dependenceRecord(m *calibration.CovarianceModel, conformalApplied bool) map[string]any {
	if m == nil {
		return map[string]any{
			"estimated": false,
			"applied":   false,
			"note": "no dependence was estimated; the combination is plain Fisher (18), " +
				"which assumes the detectors are independent",
		}
	}
	est := m.Estimates()
	used := 0
	for _, e := range est {
		if e.Used {
			used++
		}
	}
	record := map[string]any{
		"estimated":   true,
		"applied":     !conformalApplied,
		"pairs":       est,
		"pairs_total": len(est),
		"pairs_used":  used,
		"note": "cov(-2 ln P_i, -2 ln P_j) estimated directly from burn-in co-evaluations " +
			"and frozen at the boundary, per the rule Ã‚Â§8.2 applies to the partition: a " +
			"quantity used to score an event may not have been fitted on it. The " +
			"Kost-McDermott value implied by the same correlation is recorded beside each " +
			"direct estimate for comparison",
	}
	if conformalApplied {
		record["withheld_reason"] = "the covariance is estimated on the detectors' own " +
			"p-values, and under conformal calibration the combination consumes " +
			"calibrated ones instead. -2 ln P runs to thousands on a model tail and to " +
			"tens on a rank, so applying a scale measured on the first to a statistic " +
			"built from the second yields a meaningless c. Equation (19) is therefore " +
			"withheld from the calibrated statistic and the combination degrades to " +
			"Fisher (18), which Ã‚Â§10.2 requires and which costs little here: the measured " +
			"correlations are 0.03 to 0.15. It IS applied to the model-scale combination " +
			"carried on every alert as ModelLogP, where its covariance is on scale. " +
			"Estimating the covariance on calibrated values instead would be circular, " +
			"conformal being frozen at the same boundary; a split burn-in would resolve " +
			"it and is recorded as future work"
	}
	return record
}

// registeredDetectorIDs lists the detectors that scored, in registration order, for the
// provenance block.
func registeredDetectorIDs(reg *detector.Registry) []string {
	all := reg.All()
	out := make([]string, 0, len(all))
	for _, d := range all {
		out = append(out, string(d.ID()))
	}
	sort.Strings(out)
	return out
}

// entitySampleRecord describes the entity sampling in the result, so that a sampled
// run announces itself rather than being distinguishable only by its row counts.
func entitySampleRecord(cfg runConfig) map[string]any {
	if cfg.entitySample <= 1 {
		return map[string]any{
			"applied": false,
			// Deliberately not "the full population was scored". This replay applied no
			// sample; whether the corpus it read was already one is recorded beside this,
			// under corpus_subset, and is the only place that can answer it.
			"note": "this run applied no entity sampling of its own; see corpus_subset " +
				"for whether the corpus it read was itself a sample",
		}
	}
	return map[string]any{
		"applied":                  true,
		"keep_one_in_n":            cfg.entitySample,
		"selector":                 "FNV-1a 64 of the entity identifier, modulo N, equals zero",
		"labelled_entities_exempt": true,
		"note": "a deterministic sample of ENTITIES, not of events: per-entity histories " +
			"are left whole, and only whole entities are dropped. Every labelled entity " +
			"is kept regardless of the sample, so the labelled share of this corpus is " +
			"inflated relative to the full population and a detection rate measured here " +
			"is NOT comparable to one measured on the full population. The co-occurrence " +
			"graph and the population marginals are built from the retained entities only",
	}
}

func coverageKind(maxRows, maxSeconds int64) string {
	if maxRows > 0 || maxSeconds > 0 {
		return "prefix"
	}
	return "full"
}

// coverageStatement renders the run's coverage in the terms that were actually
// applied. A run capped by corpus time is not "the first N rows", and reporting it
// that way would misdescribe what was measured.
func coverageStatement(burnInSec, maxRows, maxSeconds int64) string {
	const day = 86400
	switch {
	case maxSeconds > 0:
		return fmt.Sprintf("corpus days %.2f to %.2f scored (burn-in days 0 to %.2f)",
			float64(burnInSec)/day, float64(maxSeconds)/day, float64(burnInSec)/day)
	case maxRows > 0:
		return fmt.Sprintf("first %d admitted events (burn-in days 0 to %.2f)",
			maxRows, float64(burnInSec)/day)
	default:
		return fmt.Sprintf("full corpus scored from day %.2f", float64(burnInSec)/day)
	}
}

// cappedSource stops after max rows, for smoke runs whose partial coverage is recorded.
type cappedSource struct {
	reader *corpus.Reader
	max    int64
	seen   *int64
	// maxAt ends the stream at a corpus timestamp, so partial coverage is stated in
	// corpus time ("days 0 to N") rather than in a row count whose meaning shifts
	// with the prefilter.
	maxAt event.Timestamp
}

func (s *cappedSource) Next() (*event.Event, error) {
	if s.max > 0 && *s.seen >= s.max {
		return nil, io.EOF
	}
	*s.seen++
	e, err := s.reader.Next()
	if err != nil {
		return nil, err
	}
	if s.maxAt > 0 && e.OccurredAt() >= s.maxAt {
		return nil, io.EOF
	}
	return e, nil
}

// ---------------------------------------------------------------------------
// Red-team ground truth
// ---------------------------------------------------------------------------

type redTeamLabels struct {
	keys map[string]struct{}
	// users is the set of entities named by at least one label. Entity sampling keeps
	// them all, so that reducing the corpus does not also reduce the thing being
	// measured.
	users map[string]struct{}
	// days is every corpus day a label falls on. The alert ledger's burn-in mirror uses
	// it to skip days that carry no labelled event, which cannot inform a fitted weight.
	days   map[int64]bool
	rows   int
	sha256 string
}

// redKey matches a scored auth event against a red-team row on the published
// four-tuple: time, user, source computer, destination computer.
func redKey(tSeconds int64, user, srcComp, dstComp string) string {
	return fmt.Sprintf("%d|%s|%s|%s", tSeconds, user, srcComp, dstComp)
}

func loadRedTeam(path string) (*redTeamLabels, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)

	zr, err := gzip.NewReader(strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	labels := &redTeamLabels{
		keys:   make(map[string]struct{}),
		users:  make(map[string]struct{}),
		days:   make(map[int64]bool),
		sha256: hex.EncodeToString(sum[:]),
	}
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) != 4 {
			return nil, fmt.Errorf("redteam row %d: %d fields", labels.rows+1, len(parts))
		}
		labels.rows++
		var t int64
		if _, err := fmt.Sscanf(parts[0], "%d", &t); err != nil {
			return nil, fmt.Errorf("redteam row %d: time %q", labels.rows, parts[0])
		}
		labels.keys[redKey(t, parts[1], parts[2], parts[3])] = struct{}{}
		labels.users[parts[1]] = struct{}{}
		labels.days[t/86400] = true
	}
	return labels, sc.Err()
}

// ---------------------------------------------------------------------------
// Accumulator: everything the result JSON reports, computed streamingly
// ---------------------------------------------------------------------------

// anomalyCategory names a structural property of an event relative to the history it
// was scored against. The categories exist so that detection can be reported per kind
// of anomaly rather than only in aggregate, and each maps to one of the limitations
// Ã‚Â§3 establishes for the standard formulation.
//
// They are deliberately defined by STRUCTURAL facts Ã¢â‚¬â€ did this entity ever use this
// value before, had this pair ever co-occurred, is this hour less likely than chance
// for this entity Ã¢â‚¬â€ and not by which detector produced the smallest p-value. A
// partition defined by which of our own detectors fired would be a partition chosen in
// our favour, and any per-category comparison drawn on it would flatter the framework
// by construction. These tests are properties of the event and the history, and any
// method scoring the same stream would agree on them.
type anomalyCategory string

const (
	// catNovelValue: the entity has history for this field but has never taken this
	// value. Ã‚Â§3.1's "this entity has not previously exhibited this value", which a
	// detector without per-entity state cannot express.
	catNovelValue anomalyCategory = "novel_value"

	// catNovelPair: two eligible values that have never co-occurred, in a graph that
	// has mass. Ã‚Â§3.3's conditional anomaly: individually ordinary, jointly not.
	catNovelPair anomalyCategory = "novel_pair"

	// catOffHours: the entity's own fitted density at this time of day is below the
	// uniform level, so this hour is less likely than chance FOR THIS ENTITY. Ã‚Â§7.1
	// and Ã‚Â§7.2; note this is per-entity, not a fixed unsociable-hours window (Ã‚Â§7.6).
	catOffHours anomalyCategory = "off_hours"

	// catVolumeBurst: the running count in the window exceeds the entity's own
	// expectation for it. Ã‚Â§7.4.
	catVolumeBurst anomalyCategory = "volume_burst"

	// catPopulationRare: the value is rare in the population marginal. Ã‚Â§9, and the
	// category a conventional isolation forest over a pooled feature cloud is
	// designed to catch; it is the control against which the others are read.
	catPopulationRare anomalyCategory = "population_rare"
)

// allCategories fixes the reporting order.
func allCategories() []anomalyCategory {
	return []anomalyCategory{
		catPopulationRare, catNovelValue, catOffHours, catVolumeBurst, catNovelPair,
	}
}

// classify derives an event's structural categories from the verdicts' evidence.
//
// Evidence is the right source: Ã‚Â§6.4, Ã‚Â§7.7 and Ã‚Â§8.3 require the sufficient statistics
// to travel with the verdict, so the classification reads the same numbers an analyst
// would read off the card, and needs no second pass over state.
func classify(verdicts detector.Verdicts) map[anomalyCategory]bool {
	out := map[anomalyCategory]bool{}
	for _, v := range verdicts.Evaluated() {
		ev := v.Evidence().Stats
		switch v.DetectorID() {
		case novelty.DetectorID:
			// History exists for this (entity, field), and this value is absent from
			// it.
			if ev["N"] > 0 && ev["n_v"] == 0 {
				out[catNovelValue] = true
			}
		case timing.DetectorID:
			// Below the uniform density means this time is less likely than chance
			// for this entity. Require some weight, or a cold-start entity would
			// qualify trivially.
			if ev["W"] >= 8 && ev["density_at_phi"] < uniformCircularDensity {
				out[catOffHours] = true
			}
		case volume.DetectorID:
			if ev["expected_count"] > 0 && ev["k_obs"] > ev["expected_count"] {
				out[catVolumeBurst] = true
			}
		case cooccurrence.DetectorID:
			if ev["m"] > 0 && ev["w_min_pair"] == 0 {
				out[catNovelPair] = true
			}
		case pairing.DetectorID:
			// The same category from the per-entity form, and the test is the per-entity
			// one: this entity has pairing history and has never exhibited this pairing.
			//
			// The SCOPE of the category changes with the detector, and that is recorded in
			// the result rather than absorbed silently. Under co-occurrence the question is
			// whether the population graph ever carried the pair; here it is whether this
			// entity ever did. Both are "individually ordinary, jointly not", but a census
			// taken under one is not comparable to a census taken under the other.
			//
			// Without this case the category simply empties, which is how replacing a
			// detector would quietly delete a row from the evaluation's central table.
			if ev["N"] > 0 && ev["n_v"] == 0 {
				out[catNovelPair] = true
			}
		case marginalDetectorID:
			// Rare in the population marginal: a frequency share below one in a
			// thousand of the field's observed mass.
			if ev["N"] > 0 && (ev["n_v"]/ev["N"]) < 0.001 {
				out[catPopulationRare] = true
			}
		}
	}
	return out
}

// uniformCircularDensity is 1/2Ãâ‚¬, the density of the uniform distribution on the
// circle: the level below which a time is less likely than chance.
const uniformCircularDensity = 0.15915494309189535

// novelPairScope describes the test that produced this run's novel_pair census.
//
// It is the only category whose structural test depends on the run's composition. Both
// forms answer "individually ordinary, jointly not", but against different histories, and
// a census taken under one must not be read against a census taken under the other.
func novelPairScope(perEntity bool) map[string]any {
	if perEntity {
		return map[string]any{
			"scope":    "per-entity",
			"detector": string(pairing.DetectorID),
			"test": "this entity has pairing history and has never exhibited this " +
				"pairing (N > 0 and n_v = 0 on the pairing verdict)",
			"note": "NOT comparable to a census taken under the population co-occurrence " +
				"form, which asks whether the population graph ever carried the pair " +
				"rather than whether this entity ever did",
		}
	}
	return map[string]any{
		"scope":    "population",
		"detector": string(cooccurrence.DetectorID),
		"test": "two eligible values have never co-occurred, in a graph that carries " +
			"mass (m > 0 and w = 0 on the minimising pair)",
		"note": "NOT comparable to a census taken under the per-entity pairing form",
	}
}

// minConformalObservations is the burn-in count below which a detector is left on its
// model p-value rather than calibrated (Ã‚Â§10.1).
//
// It is deliberately large. The conformal p-value is a rank, and its resolution is
// exactly 1/(n+1): a detector calibrated on a thousand observations cannot express
// anything below 1eÃ¢Ë†â€™3, so combining it with detectors calibrated on millions would let
// the coarsest one dominate the composite through its floor rather than through its
// evidence. Ten thousand keeps the floor at 1eÃ¢Ë†â€™4 for the sparsest detector admitted.
const minConformalObservations = 10000

// minMarginalObservations is Detector IV's abstention floor (Ã‚Â§9, "abstaining below a
// minimum observation count").
//
// It is the reciprocal of the one-in-a-thousand share the population_rare category
// tests for, and deliberately so. With fewer than a thousand observations of a field
// the smallest frequency share the population marginal can resolve is coarser than the
// threshold being tested, so a verdict below this floor would assert a rarity its own
// sample cannot distinguish from a single observation. Tying the floor to the threshold
// makes the abstention a statement about resolution rather than a tuned constant, and
// fixes both together: neither may be moved after seeing a result without moving the
// other and re-running.
const minMarginalObservations = 1000

// marginalDetectorID names Detector IV (Ã‚Â§9).
const marginalDetectorID = marginal.DetectorID

type alert struct {
	P float64 `json:"p"`
	// LogP is ln P, the quantity alerts are ranked on; see alertLess.
	LogP float64 `json:"log_p"`
	// ModelLogP is the same combination over the detectors' own p-values. It equals
	// LogP unless conformal calibration was applied, and it is the tie-break that
	// calibration makes necessary; see alertLess.
	ModelLogP float64 `json:"model_log_p"`
	TSeconds  int64   `json:"t"`
	Entity    string  `json:"entity"`
	SrcComp   string  `json:"src"`
	DstComp   string  `json:"dst"`
	IsRedTeam bool    `json:"is_red_team"`
	J         int     `json:"j"`
	// MinDetector names the detector holding the smallest p-value, populated on the
	// min-p arm so a reader can see which detector carried each alert.
	MinDetector string `json:"min_detector,omitempty"`

	// Categories are the structural kinds of anomaly this event exhibits, for the
	// per-category detection tables.
	Categories []string `json:"categories,omitempty"`
}

type dayAlerts struct {
	// alerts holds the day's K most extreme combined scores, kept sorted by alertLess
	// with the least extreme truncated away, so the slice is the answer at every moment
	// and no final sort is needed.
	//
	// Not a heap, whatever an earlier comment here claimed: a heap orders only enough to
	// find its root, and this is read in full and in order.
	alerts []alert
}

type redTeamScore struct {
	Key       string  `json:"key"`
	P         float64 `json:"p"`
	LogP      float64 `json:"log_p"`
	ModelLogP float64 `json:"model_log_p"`
	TSeconds  int64   `json:"t"`
	Entity    string  `json:"entity"`
	J         int     `json:"j"`

	// HistoryN is the entity's history length when this event was scored: the covariate
	// of #15, recorded per labelled event so p x n can be recomputed by hand from the
	// result rather than taken on trust from a summary (R5). Zero when no weighting mode
	// asked for the covariate to be tracked.
	HistoryN int64 `json:"history_n,omitempty"`

	// Detectors is each detector's own model p-value for this labelled event.
	//
	// It answers the question the combined score cannot: calibration and discrimination
	// are different properties, and conformal calibration (Ã‚Â§10.1) only fixes the first.
	// Recalibrating a detector is a monotone transform of its score, so it cannot change
	// which events that detector ranks above which others Ã¢â‚¬â€ if a detector does not
	// separate labelled events from background, no calibration will make it. Comparing
	// these against the same detector's histogram over all scored events is what
	// establishes whether any detector discriminates at all, and it is cheap because
	// there are only a few hundred labelled events.
	Detectors map[string]float64 `json:"detectors,omitempty"`

	Categories []string `json:"categories,omitempty"`
}

type histogram struct {
	// Log-spaced bins over [1e-12, 1]: bin i covers p in (10^(-12+i/20), ...].
	Counts []int64 `json:"counts"`
	Under  int64   `json:"under_1e_12"`
}

const histBins = 240 // 20 bins per decade over 12 decades

func newHistogram() *histogram { return &histogram{Counts: make([]int64, histBins)} }

func (h *histogram) add(p float64) {
	if p <= 1e-12 {
		h.Under++
		return
	}
	// log10(p) in [-12, 0]; bin index from the bottom.
	idx := int((12 + math.Log10(p)) * 20)
	if idx < 0 {
		idx = 0
	}
	if idx >= histBins {
		idx = histBins - 1
	}
	h.Counts[idx]++
}

type accumulator struct {
	labels  *redTeamLabels
	topK    int
	budgets objective.Budgets

	perDay        map[int64]*dayAlerts
	redTeamScored []redTeamScore
	combinedHist  *histogram
	detectorHist  map[detector.ID]*histogram
	statusCounts  map[detector.ID]map[string]int64
	// abstainCauses counts abstentions by the reason the verdict carries, not merely by
	// status. #37's first requirement: an arm can abstain for causes that point opposite
	// ways -- a warm-up that passes against a property of the account that never will --
	// and a single `abstained_unusable` total cannot tell them apart.
	abstainCauses map[detector.ID]map[string]int64
	// volGate measures what a completed-period abstention would do to the volume arm,
	// for every candidate threshold of #25, from this one pass.
	volGate *volumeGateProbe
	scored  int64
	tracked int64

	// The E9 arm: the same combination with the circular timing p-value replaced by
	// the 168-cell shadow's, per-day alert sets and red-team records kept in
	// parallel, and per-entity night-activity counters for the midnight-straddler
	// split Ã‚Â§12.3 requires E9 to report separately.
	cellPerDay        map[int64]*dayAlerts
	cellRedTeamScored []redTeamScore
	entityNight       map[string]*[2]int64 // entity -> {events in 22:00-02:00, total}

	// The E4 arm: the same combination with the co-occurrence p-value from the
	// single-block degeneration (15) replaced by the partitioned form (14). Both
	// arms read the same graph and the same events, so the ablation is on identical
	// data as Ã‚Â§12.3 requires.
	coocPerDay        map[int64]*dayAlerts
	coocRedTeamScored []redTeamScore

	// The min-p arm: the same evidence combined by the Ã…Â idÃƒÂ¡k-corrected smallest
	// p-value rather than by Fisher's sum, kept as a parallel alert set so the two
	// combinations are compared on identical events, as Ã‚Â§12.3 requires of an ablation.
	//
	// It exists because the measurement demanded it. On LANL days 7-8 the labelled
	// events sit at the 0.07th percentile of novelty's own distribution and at the 18th
	// to 36th of every other detector's; novelty alone would surface 57 of 262 at 100
	// alerts a day, and Fisher over all five surfaces none. A sum asks whether the
	// evidence is jointly unusual, which averages an informative detector with four that
	// are not; the minimum asks whether ANY detector found the entity out of character,
	// which is the question the framework poses.
	minPPerDay        map[int64]*dayAlerts
	minPRedTeamScored []redTeamScore

	// One alert arm per detector, ranked on that detector's own p-value alone.
	//
	// The measurement forced these into existence. Where the labelled events sit in each
	// detector's own distribution differs by four orders of magnitude Ã¢â‚¬â€ novelty's median
	// labelled event is at its 0.07th percentile, every other detector's is between the
	// 18th and the 36th Ã¢â‚¬â€ and any combination pays a multiplicity cost of order J for
	// carrying the ones that know nothing. Fisher pays all of it and detects none; the
	// Ã…Â idÃƒÂ¡k minimum over calibrated detectors pays less and detects 20 of 262; novelty
	// alone was estimated to reach 57.
	//
	// An arm names no field, so this respects R2 exactly as the composite does: novelty
	// scores whatever the registry reports as categorical, whether that is an operating
	// system, an address, or a field nobody has seen before.
	//
	// It is also the more useful output. "This account used a value it has never used"
	// is a triage instruction; a blended score is not.
	detectorPerDay        map[detector.ID]map[int64]*dayAlerts
	detectorRedTeamScored map[detector.ID][]redTeamScore

	// categoryCounts and redTeamCategoryCounts record how often each structural
	// category occurs, over all scored events and over the labelled ones, which is
	// the denominator every per-category rate is read against.
	categoryCounts        map[anomalyCategory]int64
	redTeamCategoryCounts map[anomalyCategory]int64

	// pairingRelational records which detector filled Detector III's slot, because the
	// novel_pair category's structural test changes with it: the population form asks
	// whether the population graph ever carried the pair, the per-entity form whether
	// this entity ever did. Two censuses under one name would not be comparable.
	pairingRelational bool

	// scoredPerDay is the exact number of combined events per corpus day. It is the
	// denominator m of the Benjamini-Hochberg step-up, and retaining only the top K
	// alerts destroys it unless it is counted as events arrive; without it the
	// analysis would have to approximate m by a run-wide mean, which shifts every
	// realised-FDR figure it produces.
	scoredPerDay map[int64]int64

	// The alert ledger's accumulators, populated only when a ledger is asked for. See
	// ledger.go for why the ledger exists and why it is not a result.
	//
	// armScored is populated always, being one integer increment per arm per event, and
	// it is the denominator a within-arm rank needs to mean anything.
	armScored      map[detector.ID]map[int64]int64
	burnInPerDay   map[detector.ID]map[int64]*dayAlerts
	burnInScored   map[detector.ID]map[int64]int64
	burnInLabelled []ledgerLabelled
	// burnInFitDays is the burn-in days the mirror retains: those carrying at least one
	// labelled event. Empty mirrors nothing, which is the correct behaviour for a corpus
	// whose labels all fall after the boundary -- there is nothing to fit a weight on.
	burnInFitDays map[int64]bool

	// online runs the alpha-investing rules beside the per-day step-up (#16). Never nil:
	// the none mode is a live object that runs nothing, so no call site needs a guard.
	online *onlineControl

	// weighting carries the history-length covariate and the weight tables frozen at the
	// burn-in boundary (#15). Never nil: the none mode is a live object that reweights
	// nothing, so no call site needs a guard.
	weighting *historyWeighting

	// entityDays accumulates one record per (entity, corpus day).
	//
	// The framework's premise is that the unit of analysis is the individual Ã¢â‚¬â€ a verdict
	// answers whether THIS entity acted out of character Ã¢â‚¬â€ but the alert budget is spent
	// on individual events, and an analyst triages accounts rather than log lines. The
	// two are not the same question, and ranking events discards the structure an attack
	// reliably has: on LANL day 8, 273 labelled events fall on 45 entities, one of them
	// carrying 30. An account with thirty anomalous events is a far stronger signal than
	// any one of them.
	//
	// Every entity-day is kept, not a top-K: the whole population is the denominator any
	// entity-level budget is read against, and it is bounded by entities times days
	// rather than by events.
	entityDays map[entityDayKey]*entityDay
}

// entityDayKey identifies one entity on one corpus day.
type entityDayKey struct {
	entity string
	day    int64
}

// entityDay is fixed-size evidence about one entity's day, sufficient for both
// aggregations worth comparing without keeping any event.
type entityDay struct {
	// Events is how many of the entity's events were scored that day, and it is what
	// the multiple-comparison correction is taken over: an entity with more events has
	// more chances at an extreme one.
	Events int64 `json:"events"`
	// MinLogP is the most extreme combined log p-value among them. With Events it gives
	// the Bonferroni-corrected best event, MinLogP + ln(Events), which asks whether the
	// entity had an event more extreme than its number of chances explains.
	MinLogP float64 `json:"min_log_p"`
	// SumX2 is ÃŽÂ£ Ã¢Ë†â€™2 ln P over the entity's events: Fisher's statistic at entity scope,
	// which unlike the corrected minimum accumulates many moderate anomalies into one
	// score. Its reference distribution is Ãâ€¡Ã‚Â²(2Ã‚*Events) only if the events are
	// independent, which for one entity's own stream they are not Ã¢â‚¬â€ so it ranks, and it
	// does not carry a calibrated p-value. Recorded because a campaign shows up here and
	// nowhere else.
	SumX2 float64 `json:"sum_x2"`
	// RedTeamEvents is how many of the day's events were labelled, so an entity-day is a
	// true positive when it is non-zero.
	RedTeamEvents int64 `json:"red_team_events"`

	// Tail is the entityDayTailDepth smallest log p-values of the day, ascending.
	//
	// Higher Criticism needs order statistics rather than a fixed-size summary, which is the
	// one thing an entity-day was built not to keep. A bounded tail is the compromise, and
	// the bound is a measurement rather than a taste: see entityDayTailDepth.
	Tail []float64 `json:"-"`
	// TailJSON is Tail in a form JSON can carry; see logProbability.
	TailJSON []logProbability `json:"tail"`
}

// entityDayTailDepth is how many of an entity-day's smallest log p-values are retained.
//
// Higher Criticism takes its maximum over the smallest p-values, and under the sparse
// alternative it is attained at small rank -- so a bounded tail reproduces the full-data
// statistic exactly, provided the bound is large enough. How large is measured, in
// domain/calibration's TestTopKReproducesTheFullStatisticOnASparseSignal: over 300 synthetic
// days of 5,000 events carrying 20 planted ones,
//
//	k    days agreeing exactly with the full statistic
//	 8    25 of 300
//	16    42 of 300
//	32   300 of 300
//	64   300 of 300
//
// So 8 and 16 are not enough and 32 is, on the sparsity a campaign presents. 32 rather than
// 64 because the extra 32 buy nothing measurable and every entity-day pays for them.
//
// Where a bounded tail is not enough -- a day whose signal is dense rather than sparse --
// the statistic reports itself as truncated, so a bounded maximum is never presented as a
// complete one.
const entityDayTailDepth = 32

// observeTail files one log p-value into the retained tail, keeping it ascending and bounded.
//
// A sorted insert rather than keeping everything and sorting once: the slice is at most 32
// long, and the alternative is the unbounded per-entity-day state this design exists to
// avoid.
func (ed *entityDay) observeTail(logP float64) {
	if len(ed.Tail) >= entityDayTailDepth && logP >= ed.Tail[len(ed.Tail)-1] {
		return
	}
	at := sort.SearchFloat64s(ed.Tail, logP)
	ed.Tail = append(ed.Tail, 0)
	copy(ed.Tail[at+1:], ed.Tail[at:])
	ed.Tail[at] = logP
	if len(ed.Tail) > entityDayTailDepth {
		ed.Tail = ed.Tail[:entityDayTailDepth]
	}
}

func newAccumulator(labels *redTeamLabels, topK int, budgets objective.Budgets,
	pairingRelational bool, weighting weightingMode, online onlineMode,
	onlineArm detector.ID) *accumulator {

	return &accumulator{
		labels:                labels,
		weighting:             newHistoryWeighting(weighting),
		online:                newOnlineControl(online, onlineArm),
		topK:                  topK,
		budgets:               budgets,
		pairingRelational:     pairingRelational,
		perDay:                make(map[int64]*dayAlerts),
		combinedHist:          newHistogram(),
		detectorHist:          make(map[detector.ID]*histogram),
		statusCounts:          make(map[detector.ID]map[string]int64),
		abstainCauses:         make(map[detector.ID]map[string]int64),
		cellPerDay:            make(map[int64]*dayAlerts),
		entityNight:           make(map[string]*[2]int64),
		coocPerDay:            make(map[int64]*dayAlerts),
		scoredPerDay:          make(map[int64]int64),
		categoryCounts:        make(map[anomalyCategory]int64),
		redTeamCategoryCounts: make(map[anomalyCategory]int64),
		entityDays:            make(map[entityDayKey]*entityDay),
		minPPerDay:            make(map[int64]*dayAlerts),
		detectorPerDay:        make(map[detector.ID]map[int64]*dayAlerts),
		detectorRedTeamScored: make(map[detector.ID][]redTeamScore),
		armScored:             make(map[detector.ID]map[int64]int64),
		burnInPerDay:          make(map[detector.ID]map[int64]*dayAlerts),
		burnInScored:          make(map[detector.ID]map[int64]int64),
	}
}

// observeDetectorArms files the event into one alert arm per detector that evaluated it,
// ranked on that detector's own log p-value.
//
// A detector may emit several verdicts for one event Ã¢â‚¬â€ Detector I scores every eligible
// field Ã¢â‚¬â€ and the most extreme is what a ranking on that detector would use, so that is
// what the arm sees. Ties resolve to the first in canonical order, which the caller has
// already fixed (R4).
func (a *accumulator) observeDetectorArms(se application.ScoredEvent, day int64,
	tSeconds int64, entity, srcComp, dstComp, key string, isRed bool, cats []string) {
	best := map[detector.ID]float64{}
	for _, v := range se.Verdicts {
		logP, ok := v.LogPValue()
		if !ok {
			continue
		}
		if prev, seen := best[v.DetectorID()]; !seen || logP < prev {
			best[v.DetectorID()] = logP
		}
	}

	ids := make([]detector.ID, 0, len(best))
	for id := range best {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, id := range ids {
		modelLogP := best[id]
		logP := a.weighting.adjust(id, entity, modelLogP)
		// One evaluation by this arm on this day. It is the denominator of a within-arm
		// rank, which is the only cross-arm quantity §3.4's diagnosis leaves admissible:
		// rank 100 of 600,000 and rank 100 of 900 are not the same evidence, and an
		// allocation rule that reads position without scale cannot tell them apart.
		a.bumpScored(a.armScored, id, day)
		byDay, ok := a.detectorPerDay[id]
		if !ok {
			byDay = make(map[int64]*dayAlerts)
			a.detectorPerDay[id] = byDay
		}
		da, ok := byDay[day]
		if !ok {
			da = &dayAlerts{}
			byDay[day] = da
		}
		da.push(alert{
			P: math.Exp(logP), LogP: logP, ModelLogP: modelLogP, TSeconds: tSeconds,
			Entity: entity, SrcComp: srcComp, DstComp: dstComp,
			IsRedTeam: isRed, J: 1, MinDetector: string(id), Categories: cats,
		}, a.topK)
		// Error control is a statement about the detector's own evidence, so the rule reads
		// the arm's model log p-value.
		a.online.observe(string(id), day, logP, isRed)
		if isRed {
			a.detectorRedTeamScored[id] = append(a.detectorRedTeamScored[id], redTeamScore{
				Key: key, P: math.Exp(logP), LogP: logP, ModelLogP: modelLogP,
				TSeconds: tSeconds, Entity: entity, J: 1, Categories: cats,
				HistoryN: a.weighting.entityEvents[entity],
			})
		}
	}
}

// detectorArmResults reports each detector's own arm beside the composite's.
func (a *accumulator) detectorArmResults(budgets []int) map[string]any {
	ids := make([]detector.ID, 0, len(a.detectorPerDay))
	for id := range a.detectorPerDay {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	arms := make(map[string]any, len(ids))
	for _, id := range ids {
		byDay := a.detectorPerDay[id]
		days := make([]int64, 0, len(byDay))
		for d := range byDay {
			days = append(days, d)
		}
		sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })

		detections := make(map[string]any, len(budgets))
		for _, b := range budgets {
			alerts, tp := 0, 0
			for _, d := range days {
				day := byDay[d].alerts
				n := b
				if n > len(day) {
					n = len(day)
				}
				alerts += n
				for _, al := range day[:n] {
					if al.IsRedTeam {
						tp++
					}
				}
			}
			detections[fmt.Sprintf("budget_%d_per_day", b)] = map[string]any{
				"alerts":         alerts,
				"true_positives": tp,
				"red_team_total": len(a.detectorRedTeamScored[id]),
			}
		}
		arms[string(id)] = map[string]any{
			"detections_at_budget": detections,
			"red_team_scored":      len(a.detectorRedTeamScored[id]),
		}
	}

	return map[string]any{
		"note": "one alert arm per detector, ranked on that detector's own p-value alone, " +
			"beside the composite rather than instead of it. A combination pays a " +
			"multiplicity cost of order J for carrying detectors that do not discriminate; " +
			"these arms measure what each detector is worth on its own so the cost is " +
			"visible rather than argued. An arm names no field and so respects R2",
		"arms": arms,
	}
}

// observeEntityDay folds one scored event into its entity's day.
func (a *accumulator) observeEntityDay(entity string, day int64, logP float64, isRed bool) {
	key := entityDayKey{entity: entity, day: day}
	ed, ok := a.entityDays[key]
	if !ok {
		ed = &entityDay{MinLogP: logP}
		a.entityDays[key] = ed
	}
	ed.Events++
	if logP < ed.MinLogP {
		ed.MinLogP = logP
	}
	// Fisher's statistic is Ã¢Ë†â€™2 ln P summed; LogP is already ln P.
	ed.SumX2 += -2 * logP
	ed.observeTail(logP)
	if isRed {
		ed.RedTeamEvents++
	}
}

func (a *accumulator) observe(se application.ScoredEvent) error {
	a.scored++

	for _, v := range se.Verdicts {
		byStatus, ok := a.statusCounts[v.DetectorID()]
		if !ok {
			byStatus = make(map[string]int64, 4)
			a.statusCounts[v.DetectorID()] = byStatus
		}
		byStatus[v.Status().String()]++
		if reason := v.Reason(); reason != "" {
			byCause, ok := a.abstainCauses[v.DetectorID()]
			if !ok {
				byCause = make(map[string]int64, 4)
				a.abstainCauses[v.DetectorID()] = byCause
			}
			byCause[reason]++
		}
		if p, ok := v.PValue(); ok {
			h, ok := a.detectorHist[v.DetectorID()]
			if !ok {
				h = newHistogram()
				a.detectorHist[v.DetectorID()] = h
			}
			h.add(p)
		}
	}

	for _, v := range se.ShadowVerdicts {
		if p, ok := v.PValue(); ok {
			h, ok := a.detectorHist[v.DetectorID()]
			if !ok {
				h = newHistogram()
				a.detectorHist[v.DetectorID()] = h
			}
			h.add(p)
		}
	}

	if se.Combined == nil {
		return nil
	}
	a.combinedHist.add(se.Combined.P)

	cats := classify(se.Verdicts)
	catNames := make([]string, 0, len(cats))
	for _, c := range allCategories() {
		if cats[c] {
			catNames = append(catNames, string(c))
			a.categoryCounts[c]++
		}
	}

	tSeconds := int64(se.Event.OccurredAt() / event.Second)
	srcComp, dstComp := fieldText(se.Event, "auth.source_computer"), fieldText(se.Event, "auth.destination_computer")
	key := redKey(tSeconds, string(se.Event.Entity()), srcComp, dstComp)
	_, isRed := a.labels.keys[key]
	if isRed {
		perDetector := make(map[string]float64, len(se.Verdicts))
		for _, v := range se.Verdicts {
			if p, ok := v.PValue(); ok {
				// A detector may emit several verdicts for one event; the most extreme is
				// what any ranking of that detector would use.
				if prev, seen := perDetector[string(v.DetectorID())]; !seen || p < prev {
					perDetector[string(v.DetectorID())] = p
				}
			}
		}
		a.redTeamScored = append(a.redTeamScored, redTeamScore{
			Key: key, P: se.Combined.P, LogP: se.Combined.LogP,
			ModelLogP: se.Combined.ModelLogP, TSeconds: tSeconds,
			Entity: string(se.Event.Entity()), J: se.Combined.J,
			Detectors:  perDetector,
			Categories: catNames,
			HistoryN:   a.weighting.entityEvents[string(se.Event.Entity())],
		})
		for _, c := range allCategories() {
			if cats[c] {
				a.redTeamCategoryCounts[c]++
			}
		}
	}

	day := tSeconds / 86400
	a.scoredPerDay[day]++
	a.online.observe(onlineNegative, day, se.Combined.LogP, isRed)
	a.observeEntityDay(string(se.Event.Entity()), day, se.Combined.LogP, isRed)
	a.observeDetectorArms(se, day, tSeconds, string(se.Event.Entity()),
		srcComp, dstComp, key, isRed, catNames)
	if a.volGate != nil {
		a.volGate.observe(se, day, isRed)
	}

	// The min-p arm, over the same event and the same verdicts.
	minPDay, ok := a.minPPerDay[day]
	if !ok {
		minPDay = &dayAlerts{}
		a.minPPerDay[day] = minPDay
	}
	// Unweighted, and it cannot be otherwise: no combined score exists before the burn-in
	// boundary, because the covariance and conformal models are not frozen until it, so
	// there is no sample on which a combined arm's weights could be fitted without
	// reading the p-values they would then rank. See history.go.
	minPDay.push(alert{
		P: math.Exp(se.Combined.MinPLogP), LogP: se.Combined.MinPLogP,
		ModelLogP: se.Combined.MinPLogP, TSeconds: tSeconds,
		Entity:  string(se.Event.Entity()),
		SrcComp: srcComp, DstComp: dstComp, IsRedTeam: isRed, J: se.Combined.J,
		MinDetector: se.Combined.MinDetector,
		Categories:  catNames,
	}, a.topK)
	if isRed {
		a.minPRedTeamScored = append(a.minPRedTeamScored, redTeamScore{
			Key: key, P: math.Exp(se.Combined.MinPLogP), LogP: se.Combined.MinPLogP,
			ModelLogP: se.Combined.MinPLogP, TSeconds: tSeconds,
			Entity: string(se.Event.Entity()), J: se.Combined.J,
			Categories: catNames,
			HistoryN:   a.weighting.entityEvents[string(se.Event.Entity())],
		})
	}
	da, ok := a.perDay[day]
	if !ok {
		da = &dayAlerts{}
		a.perDay[day] = da
	}
	// Unweighted, for the reason given at the min-p arm above.
	da.push(alert{
		P: se.Combined.P, LogP: se.Combined.LogP, ModelLogP: se.Combined.ModelLogP,
		TSeconds: tSeconds,
		Entity:   string(se.Event.Entity()),
		SrcComp:  srcComp, DstComp: dstComp, IsRedTeam: isRed, J: se.Combined.J,
		Categories: catNames,
	}, a.topK)
	a.tracked = int64(len(a.perDay))

	// The event now belongs to its entity's history. Counted here, after every reader of
	// the covariate above, so a covariate is always history strictly before the event it
	// weighted (#15).
	a.weighting.seen(string(se.Event.Entity()))

	// Night-activity counters for the E9 midnight-straddler split.
	nc, ok := a.entityNight[string(se.Event.Entity())]
	if !ok {
		nc = &[2]int64{}
		a.entityNight[string(se.Event.Entity())] = nc
	}
	hourOfDay := (tSeconds % 86400) / 3600
	if hourOfDay >= 22 || hourOfDay < 2 {
		nc[0]++
	}
	nc[1]++

	// The E4 substituted combination: identical inputs with the (15) fallback
	// co-occurrence p-value replaced by the partitioned (14) form.
	if coocP, ok := shadowP(se.ShadowVerdicts, coocPartitionedID); ok {
		ps := make([]float64, 0, 4)
		for _, v := range se.Verdicts.Evaluated() {
			p, _ := v.PValue()
			if v.DetectorID() == cooccurrence.DetectorID {
				p = coocP
			}
			ps = append(ps, p)
		}
		if len(ps) > 0 {
			if _, _, tail, err := calibration.Fisher(ps); err == nil {
				cda, exists := a.coocPerDay[day]
				if !exists {
					cda = &dayAlerts{}
					a.coocPerDay[day] = cda
				}
				cda.push(alert{
					P: tail, TSeconds: tSeconds, Entity: string(se.Event.Entity()),
					SrcComp: srcComp, DstComp: dstComp, IsRedTeam: isRed, J: len(ps),
				}, a.topK)
				if isRed {
					a.coocRedTeamScored = append(a.coocRedTeamScored, redTeamScore{
						Key: key, P: tail, TSeconds: tSeconds,
						Entity: string(se.Event.Entity()), J: len(ps),
					})
				}
			}
		}
	}

	// The E9 substituted combination: identical inputs with the circular timing
	// p-value replaced by the 168-cell shadow's. Recomputed here from the recorded
	// verdicts through the same equation (18) code path.
	if cellP, ok := shadowP(se.ShadowVerdicts, cellgrid.DetectorID); ok {
		ps := make([]float64, 0, 4)
		for _, v := range se.Verdicts.Evaluated() {
			p, _ := v.PValue()
			if v.DetectorID() == "timing" {
				p = cellP
			}
			ps = append(ps, p)
		}
		if len(ps) > 0 {
			if _, _, tail, err := calibration.Fisher(ps); err == nil {
				cda, ok := a.cellPerDay[day]
				if !ok {
					cda = &dayAlerts{}
					a.cellPerDay[day] = cda
				}
				cda.push(alert{
					P: tail, TSeconds: tSeconds, Entity: string(se.Event.Entity()),
					SrcComp: srcComp, DstComp: dstComp, IsRedTeam: isRed, J: len(ps),
				}, a.topK)
				if isRed {
					a.cellRedTeamScored = append(a.cellRedTeamScored, redTeamScore{
						Key: key, P: tail, TSeconds: tSeconds,
						Entity: string(se.Event.Entity()), J: len(ps),
					})
				}
			}
		}
	}
	return nil
}

// coocPartitionedID names the E4 ablation arm: the same co-occurrence detector over
// the same graph, with the offline partition installed.
const coocPartitionedID = detector.ID("cooccurrence-partitioned")

// shadowP extracts a named shadow detector's p-value, when that shadow ran.
func shadowP(shadow detector.Verdicts, id detector.ID) (float64, bool) {
	for _, v := range shadow {
		if v.DetectorID() == id {
			return v.PValue()
		}
	}
	return 0, false
}

func fieldText(e *event.Event, f event.FieldPath) string {
	v, ok := e.Get(f)
	if !ok {
		return ""
	}
	return v.Text()
}

// push maintains the K smallest by P, ties broken deterministically by (t, entity) so
// a replay reproduces the same alert set (R4). The slice is kept sorted and each
// arrival is placed by binary search; re-sorting per event is measurable at corpus
// scale, and most arrivals fail the cheap is-it-better-than-the-worst test outright.
func (d *dayAlerts) push(al alert, k int) {
	if len(d.alerts) >= k && !alertLess(al, d.alerts[len(d.alerts)-1]) {
		return
	}
	idx := sort.Search(len(d.alerts), func(i int) bool { return alertLess(al, d.alerts[i]) })
	d.alerts = append(d.alerts, alert{})
	copy(d.alerts[idx+1:], d.alerts[idx:])
	d.alerts[idx] = al
	if len(d.alerts) > k {
		d.alerts = d.alerts[:k]
	}
}

// alertLess is the deterministic total order on alerts: ascending LogP, then time,
// then entity.
//
// The order is taken on LogP rather than P because P underflows, and the alert set is
// drawn from exactly the region where it does. On LANL days 7Ã¢â‚¬â€œ13 every one of the 1,400
// retained alerts had P exactly zero, so the whole budget was allocated by the
// timestamp tie-break below rather than by how extreme the events were, and labelled
// attack events at P = 1eÃ¢Ë†â€™274 Ã¢â‚¬â€ representable, and far less extreme than the zeros Ã¢â‚¬â€
// could not enter the set at all. LogP is the same ordering with no floor, so the
// tie-break returns to breaking genuine ties.
//
// Conformal calibration reintroduces the same hazard by another route: a conformal
// p-value cannot fall below 1/(n+1), so every event past the burn-in tail ties at that
// floor and LogP alone would once again allocate the budget by timestamp. ModelLogP Ã¢â‚¬â€
// the same combination over the detectors' own p-values Ã¢â‚¬â€ separates them, so the order
// within a tie is decided by how extreme the evidence was rather than by when it
// arrived. It is a tie-break only: it can never reorder events the calibrated value
// already separates.
func alertLess(a, b alert) bool {
	if a.LogP != b.LogP {
		return a.LogP < b.LogP
	}
	if a.ModelLogP != b.ModelLogP {
		return a.ModelLogP < b.ModelLogP
	}
	if a.TSeconds != b.TSeconds {
		return a.TSeconds < b.TSeconds
	}
	return a.Entity < b.Entity
}

// minPArmResults reports the min-p combination's alert sets and detection, beside the
// Fisher figures rather than in place of them: the point is the comparison, on identical
// events and identical verdicts.
func (a *accumulator) minPArmResults(budgets []int) map[string]any {
	days := make([]int64, 0, len(a.minPPerDay))
	for d := range a.minPPerDay {
		days = append(days, d)
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })

	perDay := make(map[string]any, len(days))
	for _, d := range days {
		perDay[fmt.Sprintf("day_%02d", d)] = a.minPPerDay[d].alerts
	}

	detections := make(map[string]any, len(budgets))
	for _, b := range budgets {
		alerts, tp := 0, 0
		for _, d := range days {
			day := a.minPPerDay[d].alerts
			n := b
			if n > len(day) {
				n = len(day)
			}
			alerts += n
			for _, al := range day[:n] {
				if al.IsRedTeam {
					tp++
				}
			}
		}
		detections[fmt.Sprintf("budget_%d_per_day", b)] = map[string]any{
			"alerts":         alerts,
			"true_positives": tp,
			"red_team_total": len(a.minPRedTeamScored),
		}
	}

	// Which detector carried each alert, over the retained sets: a min-p arm whose
	// alerts all come from one detector is that detector with extra steps, and a reader
	// is entitled to see that rather than infer it.
	carried := map[string]int{}
	for _, d := range days {
		for _, al := range a.minPPerDay[d].alerts {
			carried[al.MinDetector]++
		}
	}

	return map[string]any{
		"combination": "Sidak over detectors, P = 1 - (1 - min_i P_i)^J, evaluated in log " +
			"space; equation (16) applied one level above the pairwise use in section 8.5",
		"rationale": "Fisher (18) sums log p-values and so averages an informative detector " +
			"with uninformative ones. Measured on this corpus the labelled events sit at the " +
			"0.07th percentile of novelty's own distribution and at the 18th to 36th of every " +
			"other detector's, so the sum discards the signal the minimum keeps",
		"detections_at_budget": detections,
		"alerts_per_day":       perDay,
		"red_team_scored":      a.minPRedTeamScored,
		"alerts_carried_by":    carried,
	}
}

// entityDayRow is one ranked entity-day.
type entityDayRow struct {
	Entity string `json:"entity"`
	Day    int64  `json:"day"`
	entityDay
	// CorrectedLogP is MinLogP + ln(Events): the entity's best event, penalised by how
	// many chances it had. It is a Bonferroni correction in log space and is the score
	// the "corrected minimum" ranking uses.
	CorrectedLogP float64 `json:"corrected_log_p"`

	// StandardisedX2 is (SumX2 Ã¢Ë†â€™ 2n) / (2Ã¢Ë†Å¡n) for n = Events.
	//
	// It exists because SumX2 alone is not a ranking of anomaly, it is very largely a
	// ranking of activity: Fisher's statistic grows linearly in the number of events, so
	// the busiest accounts sort to the top whatever their behaviour, and on LANL an
	// account with 177,748 events in a day outranks everything by arithmetic rather than
	// by evidence. Under the null ÃŽÂ£ Ã¢Ë†â€™2 ln P is Ãâ€¡Ã‚Â²(2n), with mean 2n and variance 4n, so
	// subtracting the mean and dividing by the standard deviation asks the question the
	// raw sum cannot: is this entity more anomalous than its event count already
	// explains?
	//
	// It is a standardised score and not a p-value: the Ãâ€¡Ã‚Â²(2n) reference needs the
	// entity's events to be independent, and one entity's own event stream is the least
	// independent data in the corpus.
	StandardisedX2 float64 `json:"standardised_x2"`

	// HigherCriticism is the third aggregation, and the only one of the three normalised so
	// that its scale does not grow with the entity's event count. See
	// domain/calibration/highercriticism.go for why that is the property that matters here.
	HigherCriticism entityDayHigherCriticism `json:"higher_criticism"`
}

// entityDayHigherCriticism is one entity-day's Higher Criticism statistic, flattened for the
// result file so the ranking is recomputable from the row alone (R5).
type entityDayHigherCriticism struct {
	// LogStatistic is what the ranking is taken on, because the statistic itself overflows
	// float64 on this corpus while its logarithm does not.
	//
	// A pointer, and null in JSON where the statistic is not positive: the logarithm of a
	// non-positive number does not exist, the domain returns negative infinity for it, and
	// JSON has no infinity. Writing it as a float64 is what made the first corpus run of
	// this code fail at the moment it wrote its result, after the whole replay.
	LogStatistic *float64 `json:"log_statistic"`
	// Statistic is the reading of it, and is null in JSON where it overflowed.
	Statistic *float64       `json:"statistic"`
	Positive  bool           `json:"positive"`
	Rank      int            `json:"rank"`
	PValueLog logProbability `json:"rank_log_p"`
	// Considered is how many ranks fell inside the alpha0 cap, and Truncated says whether
	// the retained tail was shorter than that -- so a bounded maximum is distinguishable
	// from a complete one.
	Considered int  `json:"considered"`
	Truncated  bool `json:"truncated"`
	// NullScale is sqrt(2 ln ln n), the order of the statistic under the global null. The
	// statistic has no fixed critical value and grows very slowly with n, so a reader
	// comparing two days of different sizes needs this beside them.
	NullScale float64 `json:"null_scale"`
	// Error is the reason the statistic is absent, where it is. An entity-day with no
	// retained tail has none, which is a state of the data rather than a fault.
	Error string `json:"error,omitempty"`
}

// entityDayResults ranks entity-days two ways and reports detection at entity-level
// budgets, alongside the event-level table rather than in place of it.
//
// Two rankings, because they answer different questions and a campaign may only show up
// in one. The corrected minimum asks whether the entity had a single event more extreme
// than its event count explains; Fisher's sum asks whether it had many moderately
// unusual ones. Neither is presented as a calibrated p-value at entity scope Ã¢â‚¬â€ an
// entity's own events are not independent, which is exactly why Ã‚Â§10.2's dependence
// correction exists at event scope Ã¢â‚¬â€ so these rank, and the detection counts beside them
// are the measurement.
func (a *accumulator) entityDayResults(budgets []int) map[string]any {
	rows := make([]entityDayRow, 0, len(a.entityDays))
	labelled := 0
	for key, ed := range a.entityDays {
		row := entityDayRow{Entity: key.entity, Day: key.day, entityDay: *ed}
		n := float64(ed.Events)
		row.CorrectedLogP = ed.MinLogP + math.Log(n)
		row.StandardisedX2 = (ed.SumX2 - 2*n) / (2 * math.Sqrt(n))
		row.HigherCriticism = higherCriticismOf(ed)
		row.TailJSON = logProbabilities(ed.Tail)
		rows = append(rows, row)
		if ed.RedTeamEvents > 0 {
			labelled++
		}
	}

	// A canonical total order first, so both rankings below are reproducible whatever
	// order the map yielded (R4).
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Day != rows[j].Day {
			return rows[i].Day < rows[j].Day
		}
		return rows[i].Entity < rows[j].Entity
	})

	byDay := func(score func(entityDayRow) float64, ascending bool) map[int64][]entityDayRow {
		grouped := map[int64][]entityDayRow{}
		for _, r := range rows {
			grouped[r.Day] = append(grouped[r.Day], r)
		}
		for day := range grouped {
			g := grouped[day]
			sort.SliceStable(g, func(i, j int) bool {
				si, sj := score(g[i]), score(g[j])
				if si != sj {
					if ascending {
						return si < sj
					}
					return si > sj
				}
				return g[i].Entity < g[j].Entity
			})
		}
		return grouped
	}

	// Higher Criticism cannot be ranked by a single float: the comparison is on the
	// logarithm where the statistic is positive and on the raw value where it is not, since
	// a day quieter than uniform has a negative statistic whose logarithm does not exist.
	// So it gets a comparator rather than a score, and [calibration.MoreExtreme] is the
	// total order.
	byDayLess := func(less func(a, b entityDayRow) bool) map[int64][]entityDayRow {
		grouped := map[int64][]entityDayRow{}
		for _, r := range rows {
			grouped[r.Day] = append(grouped[r.Day], r)
		}
		for day := range grouped {
			g := grouped[day]
			sort.SliceStable(g, func(i, j int) bool {
				if less(g[i], g[j]) {
					return true
				}
				if less(g[j], g[i]) {
					return false
				}
				return g[i].Entity < g[j].Entity
			})
		}
		return grouped
	}

	detectionsFor := func(grouped map[int64][]entityDayRow) map[string]any {
		out := make(map[string]any, len(budgets))
		for _, b := range budgets {
			alerts, tp := 0, 0
			for _, g := range grouped {
				n := b
				if n > len(g) {
					n = len(g)
				}
				alerts += n
				for _, r := range g[:n] {
					if r.RedTeamEvents > 0 {
						tp++
					}
				}
			}
			out[fmt.Sprintf("budget_%d_per_day", b)] = map[string]any{
				"entity_days_alerted":  alerts,
				"true_positives":       tp,
				"labelled_entity_days": labelled,
			}
		}
		return out
	}

	corrected := byDay(func(r entityDayRow) float64 { return r.CorrectedLogP }, true)
	fisher := byDay(func(r entityDayRow) float64 { return r.SumX2 }, false)
	standardised := byDay(func(r entityDayRow) float64 { return r.StandardisedX2 }, false)
	criticism := byDayLess(func(a, b entityDayRow) bool {
		return calibration.MoreExtreme(a.HigherCriticism.result(), b.HigherCriticism.result())
	})

	// The top of each ranking, for reading; the full population stays in the counts.
	const topN = 25
	head := func(grouped map[int64][]entityDayRow) map[string]any {
		out := map[string]any{}
		for day, g := range grouped {
			n := topN
			if n > len(g) {
				n = len(g)
			}
			out[fmt.Sprintf("day_%02d", day)] = g[:n]
		}
		return out
	}

	return map[string]any{
		"note": "one record per (entity, corpus day). The framework's premise is that the " +
			"unit of analysis is the individual, while the alert budget is spent on events; " +
			"these are the same evidence aggregated to the unit the premise names, and to " +
			"the unit an analyst actually triages. No score here is a calibrated p-value at " +
			"entity scope: an entity's own events are not independent",
		"total_entity_days":     len(rows),
		"labelled_entity_days":  labelled,
		"from_the_event_budget": a.entityDaysFromEventBudget(budgets, labelled),
		"corrected_minimum": map[string]any{
			"score":                "min_log_p + ln(events), ascending",
			"detections_at_budget": detectionsFor(corrected), "top": head(corrected),
		},
		"fisher_over_the_day": map[string]any{
			"score": "sum of -2 ln P over the entity's events, descending. READ WITH CARE: " +
				"Fisher's statistic grows linearly in the event count, so this ranks activity " +
				"at least as much as anomaly and the busiest accounts sort to the top by " +
				"arithmetic. Any detection it reports is confounded with how active the " +
				"entity was; standardised_x2 is the same evidence with that confound removed",
			"detections_at_budget": detectionsFor(fisher), "top": head(fisher),
		},
		"standardised": map[string]any{
			"score": "(sum_x2 - 2n) / (2*sqrt(n)) for n = events, descending: how far the " +
				"entity's accumulated evidence exceeds what its event count alone predicts " +
				"under chi-square(2n)",
			"detections_at_budget": detectionsFor(standardised), "top": head(standardised),
		},
		"higher_criticism": map[string]any{
			"score": "max over the smallest ranks of sqrt(n)(i/n - p_(i))/sqrt(p_(i)(1-p_(i))), " +
				"descending, from the retained tail of the day's log p-values. Unlike the " +
				"other two it is normalised so its null distribution barely moves with the " +
				"event count -- measured, the 95th percentile grows 17% between days of 100 " +
				"and 10,000 events where Fisher's sum grows a hundredfold -- so two " +
				"entity-days of very different size can be ranked against each other. It is " +
				"built for a sparse cluster of moderate anomalies in an otherwise ordinary " +
				"day, which is what a campaign is and what Fisher's sum dilutes",
			"alpha0":               calibration.DefaultAlpha0,
			"tail_depth":           entityDayTailDepth,
			"detections_at_budget": detectionsFor(criticism), "top": head(criticism),
			"truncated_entity_days": func() int {
				truncated := 0
				for _, r := range rows {
					if r.HigherCriticism.Truncated {
						truncated++
					}
				}
				return truncated
			}(),
			"truncation_note": "an entity-day is truncated when its event count put the " +
				"alpha0 cap beyond the retained tail, so its maximum was taken over a " +
				"prefix. Counted rather than hidden: on a sparse signal the maximum is " +
				"attained at small rank and truncation costs nothing, and where it would " +
				"cost something this is how a reader knows",
		},
		// Every entity-day, not a sample: 1,777 rows on the sampled corpus and bounded by
		// entities times days, so the file stays small while any re-ranking becomes a
		// re-analysis rather than another run.
		"rows": rows,
	}
}

func (a *accumulator) results() map[string]any {
	days := make([]int64, 0, len(a.perDay))
	for d := range a.perDay {
		days = append(days, d)
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })

	perDay := make(map[string]any, len(days))
	for _, d := range days {
		perDay[fmt.Sprintf("day_%02d", d)] = a.perDay[d].alerts
	}

	// Detection at matched alert budgets (Ã‚Â§12.4): for budget b, the day's alerts are
	// its b smallest combined p-values; a detection is an alerted red-team event.
	// The set is the run's -budgets parameter, validated against -topk before the corpus
	// was read, rather than a list fixed in code.
	budgets := []int(a.budgets)
	detections := make(map[string]any, len(budgets))
	for _, b := range budgets {
		tp := 0
		alerts := 0
		for _, d := range days {
			day := a.perDay[d].alerts
			n := b
			if n > len(day) {
				n = len(day)
			}
			alerts += n
			for _, al := range day[:n] {
				if al.IsRedTeam {
					tp++
				}
			}
		}
		detections[fmt.Sprintf("budget_%d_per_day", b)] = map[string]any{
			"alerts":         alerts,
			"true_positives": tp,
			"red_team_total": len(a.redTeamScored),
		}
	}

	hists := make(map[string]any, len(a.detectorHist)+1)
	hists["combined"] = a.combinedHist
	for id, h := range a.detectorHist {
		hists[string(id)] = h
	}

	scoredPerDay := make(map[string]int64, len(days))
	for _, d := range days {
		scoredPerDay[fmt.Sprintf("day_%02d", d)] = a.scoredPerDay[d]
	}

	categories := make(map[string]any, len(allCategories()))
	for _, c := range allCategories() {
		categories[string(c)] = map[string]any{
			"scored_events":   a.categoryCounts[c],
			"red_team_events": a.redTeamCategoryCounts[c],
		}
	}

	out := map[string]any{
		"anomaly_categories": map[string]any{
			"definition": "structural properties of an event relative to the history it " +
				"was scored against, derived from verdict evidence and NOT from which " +
				"detector produced the smallest p-value; a partition defined by our own " +
				"detectors would flatter the framework by construction",
			// novel_pair is the one category whose test changes with the run's
			// composition, so the run states which test produced its census rather than
			// leaving two incomparable numbers under one name.
			"novel_pair_scope": novelPairScope(a.pairingRelational),
			"counts":           categories,
		},
		"detections_at_budget": detections,
		"online_control":       a.online.record(),
		"online_never_silent":  a.online.neverSilent(),
		"min_p_arm":            a.minPArmResults(budgets),
		"detector_arms":        a.detectorArmResults(budgets),
		"history_weighting":    a.weighting.record(),
		"labelled_history_product": map[string]any{
			"composite": historyOfLabelled(a.redTeamScored),
			"note": "p x n over the labelled events, the statistic #15's premise rests " +
				"on. Recomputed under whatever ranking this run used, so whether the 1/n " +
				"dependence actually moved is read off the result rather than assumed " +
				"from the fact that a weighting was applied",
		},
		"union_arm":         a.unionArmResults(budgets),
		"weighted_arm":      a.weightedResults(budgets),
		"entity_days":       a.entityDayResults(budgets),
		"alerts_per_day":    perDay,
		"scored_per_day":    scoredPerDay,
		"red_team_scored":   a.redTeamScored,
		"p_histograms":      hists,
		"status_counts":     a.statusCounts,
		"abstain_causes":    a.abstainCauses,
		"volume_gate_probe": a.volumeGateResults(budgets),
	}

	if len(a.coocPerDay) > 0 {
		coocDetections := make(map[string]any, len(budgets))
		for _, b := range budgets {
			tp := 0
			for _, d := range days {
				cda, exists := a.coocPerDay[d]
				if !exists {
					continue
				}
				n := b
				if n > len(cda.alerts) {
					n = len(cda.alerts)
				}
				for _, al := range cda.alerts[:n] {
					if al.IsRedTeam {
						tp++
					}
				}
			}
			coocDetections[fmt.Sprintf("budget_%d_per_day", b)] = map[string]any{
				"true_positives": tp,
				"red_team_total": len(a.coocRedTeamScored),
			}
		}
		coocPerDayOut := make(map[string]any, len(days))
		for _, d := range days {
			if cda, exists := a.coocPerDay[d]; exists {
				coocPerDayOut[fmt.Sprintf("day_%02d", d)] = cda.alerts
			}
		}
		out["e4_partitioned_arm"] = map[string]any{
			"description": "the same combination with the co-occurrence p-value taken from " +
				"the partitioned form (14) instead of the single-block degeneration (15); " +
				"both arms read identical graph state and identical events",
			"detections_at_budget": coocDetections,
			"red_team_scored":      a.coocRedTeamScored,
			// The per-day alert sets are what make the comparison testable as paired
			// data: without them the analysis can only compare counts, and McNemar
			// needs to know WHICH events each arm caught.
			"alerts_per_day": coocPerDayOut,
		}
	}

	if len(a.cellPerDay) > 0 {
		cellDetections := make(map[string]any, len(budgets))
		for _, b := range budgets {
			tp := 0
			for _, d := range days {
				cda, ok := a.cellPerDay[d]
				if !ok {
					continue
				}
				n := b
				if n > len(cda.alerts) {
					n = len(cda.alerts)
				}
				for _, al := range cda.alerts[:n] {
					if al.IsRedTeam {
						tp++
					}
				}
			}
			cellDetections[fmt.Sprintf("budget_%d_per_day", b)] = map[string]any{
				"true_positives": tp,
				"red_team_total": len(a.cellRedTeamScored),
			}
		}

		straddlers := make(map[string]bool, len(a.entityNight))
		straddlerCount := 0
		for entity, nc := range a.entityNight {
			isStraddler := nc[1] > 0 && float64(nc[0])/float64(nc[1]) >= 0.5
			straddlers[entity] = isStraddler
			if isStraddler {
				straddlerCount++
			}
		}

		cellPerDayOut := make(map[string]any, len(days))
		for _, d := range days {
			if cda, exists := a.cellPerDay[d]; exists {
				cellPerDayOut[fmt.Sprintf("day_%02d", d)] = cda.alerts
			}
		}
		out["e9_cell_arm"] = map[string]any{
			"detections_at_budget": cellDetections,
			"red_team_scored":      a.cellRedTeamScored,
			// See the E4 arm: the paired test needs which events, not how many.
			"alerts_per_day":     cellPerDayOut,
			"straddler_entities": straddlerCount,
			"straddler_rule":     "entity with >= 50% of scored events in 22:00-02:00",
			"straddler_red_team": a.straddlerRedTeamSplit(straddlers),
		}
	}
	return out
}

// straddlerRedTeamSplit reports, for both arms, red-team p-value summaries split by
// whether the entity straddles midnight Ã¢â‚¬â€ the population Ã‚Â§12.3 says the cell
// representation provably mishandles.
func (a *accumulator) straddlerRedTeamSplit(straddlers map[string]bool) map[string]any {
	summary := func(scores []redTeamScore, wantStraddler bool) map[string]any {
		n := 0
		below01 := 0
		for _, r := range scores {
			if straddlers[r.Entity] != wantStraddler {
				continue
			}
			n++
			if r.P <= 0.01 {
				below01++
			}
		}
		return map[string]any{"n": n, "p_below_0.01": below01}
	}
	return map[string]any{
		"circular_arm_straddlers":    summary(a.redTeamScored, true),
		"circular_arm_nonstraddlers": summary(a.redTeamScored, false),
		"cell_arm_straddlers":        summary(a.cellRedTeamScored, true),
		"cell_arm_nonstraddlers":     summary(a.cellRedTeamScored, false),
	}
}

// ---------------------------------------------------------------------------
// Provenance helpers
// ---------------------------------------------------------------------------

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }() // read-only
	h := sha256.New()
	if _, err := io.Copy(h, bufio.NewReaderSize(f, 4<<20)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func gitSHA() string {
	if out, err := exec.Command("git", "rev-parse", "HEAD").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				return s.Value
			}
		}
	}
	return "unknown"
}

func gitDirty() bool {
	out, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		return true
	}
	return len(strings.TrimSpace(string(out))) > 0
}

// writeJSON encodes a result to disk, first naming any value JSON cannot carry.
//
// The naming is the point. `json: unsupported value: -Inf` is what encoding/json says on its
// own, and it says nothing about which field: an eighty-minute replay ended twice on that
// message, once for a log statistic and once for a retained tail, and each time the field had
// to be found by elimination. A path costs one walk of a map that is already in memory.
//
// It refuses rather than sanitising. An infinity in a result means a quantity was computed
// that the file cannot express, and quietly writing a substitute would put a number in a
// measurement where the truth was "unrepresentable".
func writeJSON(path string, v any) error {
	if bad := nonFinite(v, ""); len(bad) > 0 {
		return fmt.Errorf("result contains %d value(s) JSON cannot carry, at %s: an "+
			"infinity or NaN in a result means a quantity was computed that the file "+
			"cannot express, so it is refused rather than substituted",
			len(bad), strings.Join(bad, ", "))
	}
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", " ")
	if err := enc.Encode(v); err != nil {
		_ = f.Close()
		return err
	}
	// The close error matters on the write path: it is the last chance to hear that
	// the result file did not reach disk intact.
	return f.Close()
}

func dirOf(path string) string {
	idx := strings.LastIndexAny(path, `/\`)
	if idx < 0 {
		return "."
	}
	return path[:idx]
}

// nonFinitePathCap bounds how many paths are reported, so a systematically broken field names
// itself without printing a hundred thousand entity-days.
const nonFinitePathCap = 12

// nonFinite walks a value about to be encoded and returns the paths of any float JSON cannot
// carry.
//
// It walks the same shapes encoding/json will: maps, slices, structs and pointers, plus the
// float kinds. A type implementing json.Marshaler is trusted to handle its own infinities,
// which is what logProbability does, so those are not walked.
func nonFinite(v any, path string) []string {
	if v == nil {
		return nil
	}
	if _, ok := v.(json.Marshaler); ok {
		return nil
	}
	return nonFiniteValue(reflect.ValueOf(v), path, 0)
}

func nonFiniteValue(rv reflect.Value, path string, depth int) []string {
	// A depth cap so a cyclic structure cannot spin. Nothing here is cyclic; the cap is
	// insurance against a future field that is.
	if depth > 24 || !rv.IsValid() {
		return nil
	}
	if rv.CanInterface() {
		if _, ok := rv.Interface().(json.Marshaler); ok {
			return nil
		}
	}

	var out []string
	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return []string{path}
		}
	case reflect.Interface, reflect.Ptr:
		if !rv.IsNil() {
			out = append(out, nonFiniteValue(rv.Elem(), path, depth+1)...)
		}
	case reflect.Map:
		keys := make([]string, 0, rv.Len())
		byKey := map[string]reflect.Value{}
		for _, k := range rv.MapKeys() {
			name := fmt.Sprint(k.Interface())
			keys = append(keys, name)
			byKey[name] = rv.MapIndex(k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, nonFiniteValue(byKey[k], path+"/"+k, depth+1)...)
			if len(out) >= nonFinitePathCap {
				return out[:nonFinitePathCap]
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			out = append(out, nonFiniteValue(rv.Index(i), fmt.Sprintf("%s[%d]", path, i), depth+1)...)
			if len(out) >= nonFinitePathCap {
				return out[:nonFinitePathCap]
			}
		}
	case reflect.Struct:
		t := rv.Type()
		for i := 0; i < rv.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			name := field.Name
			if tag := field.Tag.Get("json"); tag != "" && tag != "-" {
				name = strings.Split(tag, ",")[0]
			} else if tag == "-" {
				// Not encoded, so it cannot break the encoding.
				continue
			}
			out = append(out, nonFiniteValue(rv.Field(i), path+"/"+name, depth+1)...)
			if len(out) >= nonFinitePathCap {
				return out[:nonFinitePathCap]
			}
		}
	}
	return out
}
